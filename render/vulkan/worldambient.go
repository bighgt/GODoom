package vulkan

import (
	_ "embed"
	"fmt"
	"unsafe"

	vk "github.com/goki/vulkan"

	"twopointfive/render/worldgeo"
)

// Quarter-resolution "ambient" pass for the hardware path: dynamic-light
// shadows, SSAO, and god rays, all pre-computed here (worldambient.frag)
// into one shared, low-resolution offscreen image instead of running per
// full-res pixel in worldblit.frag. worldblit.frag then samples that image
// back at full res through the ordinary linear/clamp sampler
// (wr.clampSampler) — the low-res-then-bilinear-upsample read IS a real
// soft blur from hardware texture filtering for all three (a shadow
// penumbra, an AO darkening, a god-ray glow are all inherently
// low-frequency), no separate blur pass needed, the same way
// worldbloom.go's mip pyramid leans on hardware filtering rather than a
// hand-rolled box/Gaussian kernel. Running SSAO's 12-tap disc gather and
// god rays' 24-step march at 1/16th the pixel count (instead of every
// full-res pixel, as both used to) is the actual performance win here; the
// softening is a side benefit that started this pass (this used to be
// shadow-only — see the "shadow" naming still on a few fields/constants
// below, kept rather than renamed everywhere for a purely cosmetic diff).
//
// Shape mirrors worldbloom.go (one shared image reused every frame, built
// via wMakeImage + the generic litPipeline/litPipelineLayout helpers), but
// with its own small, FIXED-size descriptor pool kept entirely separate from
// the shared framePool in worldsetup.go — this pass never touches that
// pool's already-fragile arithmetic.

//go:embed shaders/worldambient.frag.spv
var worldAmbientFragSPV []byte

//go:embed shaders/worldambientblur.frag.spv
var worldAmbientBlurFragSPV []byte

// dynShadowStrength/March/Bias are the shadow march's tuning constants, and
// aoStrength/aoRadius/aoBias/godrayStrength/sunX/Y/Z are the SSAO/god-ray
// ones moved here from recordWorldCommandBuffer's old local block when
// those two effects joined the shadow pass — named constants rather than a
// config knob since they're an internal quality/cost tradeoff, not
// something a player would tune. sunDir is a fixed, normalised world-space
// direction (high, toward +X/+Y) so god rays streak from a bright sky
// opening the player is roughly facing.
const (
	dynShadowStrength = 0.6
	dynShadowMarch    = 320.0
	dynShadowBias     = 2.0

	ambAOStrength     = 0.40 // deepest crease darkens ~40%; AO multiplies final light
	ambAORadius       = 48.0
	ambAOBias         = 1.5
	ambGodrayStrength = 0.35
	ambSunX           = 0.40 // |(ambSunX,ambSunY,ambSunZ)| == 1, ~60 deg elevation
	ambSunY           = 0.30
	ambSunZ           = 0.866
)

// worldAmbient holds the ambient pass's resources: one shared image at
// wr.w/4 x wr.h/4 (not double-buffered — every slot's pass writes into the
// same image in turn, exactly like worldbloom.go's mip images), its render
// pass/framebuffer/pipeline, and descriptors.
type worldAmbient struct {
	img  litImage
	w, h int
	fb   vk.Framebuffer
	rp   vk.RenderPass

	// dslSrc: binding0 = this slot's depth, binding1 = this slot's World
	// UBO, binding2 = this slot's scene colour (god rays only).
	dslSrc vk.DescriptorSetLayout
	// dslSample/setSample: binding0 = the shared ambient image (imgBlur).
	// Used to be worldblit's 2nd descriptor set directly; worldblit.frag no
	// longer samples the ambient term at all — worldresolve.go's resolve
	// pass does that now, through ITS OWN descriptor set bound to the same
	// imgBlur view (not this one), as part of folding SSR/ambient/tonemap
	// into one pre-blit pass (see worldresolve.go's doc comment). So
	// dslSample/setSample are unused dead weight now, left allocated/
	// written here rather than torn out — this pass's own small dedicated
	// pool, not worldsetup.go's shared framePool, so there's nothing to
	// save by removing it and a real (if small) risk in touching this
	// file's own pool arithmetic for a purely cosmetic trim.
	dslSample vk.DescriptorSetLayout
	pool      vk.DescriptorPool

	plGen   vk.PipelineLayout
	pipeGen vk.Pipeline

	setSrc    [maxFramesInFlight]vk.DescriptorSet
	setSample vk.DescriptorSet // one set: the image it points at never changes per slot

	// AO denoise blur (worldambientblur.frag — see its own doc comment): a
	// second small image/framebuffer sharing rp (same format/attachment
	// layout, so no second VkRenderPass needed), read from img and written
	// once per frame right after the generation pass above. setSample
	// (worldblit.frag's input) points at imgBlur, not img, once this exists
	// — see worldCreateAmbientPass.
	imgBlur    litImage
	fbBlur     vk.Framebuffer
	dslBlurSrc vk.DescriptorSetLayout
	setBlurSrc vk.DescriptorSet
	plBlur     vk.PipelineLayout
	pipeBlur   vk.Pipeline
}

func (r *Renderer) worldCreateAmbientPass() error {
	wr := r.wr
	w, h := wr.w/4, wr.h/4
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	wr.ambient.w, wr.ambient.h = w, h

	var err error
	wr.ambient.img, err = r.wMakeImage(w, h, sceneHDRFormat,
		vk.ImageUsageFlags(vk.ImageUsageColorAttachmentBit)|vk.ImageUsageFlags(vk.ImageUsageSampledBit),
		vk.ImageAspectFlags(vk.ImageAspectColorBit), 1)
	if err != nil {
		return err
	}

	// Render pass: one colour attachment, cleared then written every frame,
	// left shader-readable for worldblit.frag's bilinear sample.
	rp, err := r.newSampledColorPass(sceneHDRFormat, vk.AttachmentLoadOpClear, vk.ImageLayoutUndefined, "ambient")
	if err != nil {
		return err
	}
	wr.ambient.rp = rp

	fb, err := r.newFramebuffer(rp, w, h, "ambient", wr.ambient.img.view)
	if err != nil {
		return err
	}
	wr.ambient.fb = fb

	// Second image/framebuffer for the AO denoise blur (worldambientblur.frag)
	// — same rp (format/attachment layout only, decoupled from any specific
	// image), so no second VkRenderPass is needed, just a second framebuffer
	// target.
	wr.ambient.imgBlur, err = r.wMakeImage(w, h, sceneHDRFormat,
		vk.ImageUsageFlags(vk.ImageUsageColorAttachmentBit)|vk.ImageUsageFlags(vk.ImageUsageSampledBit),
		vk.ImageAspectFlags(vk.ImageAspectColorBit), 1)
	if err != nil {
		return err
	}
	fbBlur, err := r.newFramebuffer(rp, w, h, "ambient blur", wr.ambient.imgBlur.view)
	if err != nil {
		return err
	}
	wr.ambient.fbBlur = fbBlur

	uboBinding := vk.DescriptorSetLayoutBinding{
		Binding: 1, DescriptorType: vk.DescriptorTypeUniformBuffer, DescriptorCount: 1,
		StageFlags: vk.ShaderStageFlags(vk.ShaderStageFragmentBit),
	}
	if wr.ambient.dslSrc, err = r.newDescriptorSetLayout("ambient", samplerBinding(0), uboBinding, samplerBinding(2)); err != nil {
		return err
	}
	if wr.ambient.dslSample, err = r.newDescriptorSetLayout("ambient", samplerBinding(0)); err != nil {
		return err
	}
	if wr.ambient.dslBlurSrc, err = r.newDescriptorSetLayout("ambient", samplerBinding(0)); err != nil {
		return err
	}

	// One dedicated, fixed-size pool — sized once here and never grown or
	// touched again, and kept entirely separate from worldCreateDescriptors'
	// shared framePool so this pass can't perturb that pool's arithmetic.
	poolInfo := vk.DescriptorPoolCreateInfo{
		SType:         vk.StructureTypeDescriptorPoolCreateInfo,
		MaxSets:       uint32(maxFramesInFlight + 2), // setSrc per slot + shared setSample + shared setBlurSrc
		PoolSizeCount: 2,
		PPoolSizes: []vk.DescriptorPoolSize{
			// depth + scene samplers per setSrc, + one sampler each for the
			// shared setSample and setBlurSrc.
			{Type: vk.DescriptorTypeCombinedImageSampler, DescriptorCount: uint32(2*maxFramesInFlight + 2)},
			// World UBO per setSrc.
			{Type: vk.DescriptorTypeUniformBuffer, DescriptorCount: uint32(maxFramesInFlight)},
		},
	}
	var pool vk.DescriptorPool
	if res := vk.CreateDescriptorPool(r.device, &poolInfo, nil, &pool); res != vk.Success {
		return fmt.Errorf("vulkan: ambient descriptor pool: %s", vk.Error(res))
	}
	wr.ambient.pool = pool

	alloc := func(layout vk.DescriptorSetLayout) (vk.DescriptorSet, error) {
		return r.allocDescriptorSet(pool, layout, "ambient")
	}

	var writes []vk.WriteDescriptorSet
	for i := range wr.slots {
		sl := &wr.slots[i]
		s, aerr := alloc(wr.ambient.dslSrc)
		if aerr != nil {
			return aerr
		}
		wr.ambient.setSrc[i] = s
		writes = append(writes,
			imageWrite(s, 0, wr.depthSampler, sl.depth.view),
			vk.WriteDescriptorSet{
				SType: vk.StructureTypeWriteDescriptorSet, DstSet: s, DstBinding: 1,
				DescriptorCount: 1, DescriptorType: vk.DescriptorTypeUniformBuffer,
				PBufferInfo: []vk.DescriptorBufferInfo{{Buffer: sl.ubo, Offset: 0, Range: vk.DeviceSize(worldUBOFloats * 4)}},
			},
			imageWrite(s, 2, wr.clampSampler, sl.scene.view),
		)
	}
	// worldblit.frag samples the BLURRED buffer, not the raw generation
	// pass's own output — see worldambientblur.frag's doc comment.
	ss, err := alloc(wr.ambient.dslSample)
	if err != nil {
		return err
	}
	wr.ambient.setSample = ss
	writes = append(writes, imageWrite(ss, 0, wr.clampSampler, wr.ambient.imgBlur.view))

	bs, err := alloc(wr.ambient.dslBlurSrc)
	if err != nil {
		return err
	}
	wr.ambient.setBlurSrc = bs
	writes = append(writes, imageWrite(bs, 0, wr.clampSampler, wr.ambient.img.view))
	vk.UpdateDescriptorSets(r.device, uint32(len(writes)), writes, 0, nil)

	if wr.ambient.plGen, err = r.litPipelineLayout(wr.ambient.dslSrc, 96); err != nil {
		return err
	}
	if wr.ambient.pipeGen, err = r.litPipeline(worldAmbientFragSPV, wr.ambient.rp, wr.ambient.plGen); err != nil {
		return err
	}
	if wr.ambient.plBlur, err = r.litPipelineLayout(wr.ambient.dslBlurSrc, 16); err != nil {
		return err
	}
	if wr.ambient.pipeBlur, err = r.litPipeline(worldAmbientBlurFragSPV, wr.ambient.rp, wr.ambient.plBlur); err != nil {
		return err
	}
	return nil
}

// recordAmbientTerm draws the quarter-res shadow/AO/god-ray buffer for this
// slot. Called from recordWorldCommandBuffer right after the world pass
// ends and before the bloom pass — basis is the camera projection basis
// needed to reconstruct a world point from (screen pixel, sampled depth),
// computed once by the caller (computeCamBasis, world.go) and shared with
// the present blit pass's own push constants rather than each independently
// re-deriving it from cam.
func (r *Renderer) recordAmbientTerm(cmd vk.CommandBuffer, frame int, cam worldgeo.Camera, basis camBasis) {
	wr := r.wr
	am := &wr.ambient
	if am.pipeGen == nil {
		return
	}
	bFocal := basis.focal
	bSin := float32(basis.sin)
	bCos := float32(basis.cos)
	bHorizon := float32(basis.horizon)

	vk.CmdBeginRenderPass(cmd, &vk.RenderPassBeginInfo{
		SType: vk.StructureTypeRenderPassBeginInfo, RenderPass: am.rp, Framebuffer: am.fb,
		RenderArea:      vk.Rect2D{Extent: vk.Extent2D{Width: uint32(am.w), Height: uint32(am.h)}},
		ClearValueCount: 1, PClearValues: []vk.ClearValue{vk.NewClearValue([]float32{1, 1, 0, 1})}, // r=shadow,g=ao unshadowed/unoccluded; b=godray 0
	}, vk.SubpassContentsInline)
	vk.CmdBindPipeline(cmd, vk.PipelineBindPointGraphics, am.pipeGen)
	vk.CmdSetViewport(cmd, 0, 1, []vk.Viewport{fullViewport(am.w, am.h)})
	vk.CmdSetScissor(cmd, 0, 1, []vk.Rect2D{fullScissor(am.w, am.h)})
	vk.CmdBindDescriptorSets(cmd, vk.PipelineBindPointGraphics, am.plGen, 0, 1, []vk.DescriptorSet{am.setSrc[frame]}, 0, nil)

	push := [24]float32{
		float32(worldgeo.NearPlane), float32(worldgeo.FarPlane), bSin, bCos,
		float32(bFocal), float32(cam.X), float32(cam.Y), float32(cam.Z),
		bHorizon, float32(wr.w), float32(wr.h), float32(cam.ShadowCasterN),
		dynShadowStrength, dynShadowMarch, dynShadowBias, ambAOStrength,
		ambAORadius, ambAOBias, ambGodrayStrength, 0,
		ambSunX, ambSunY, ambSunZ, 0,
	}
	vk.CmdPushConstants(cmd, am.plGen, vk.ShaderStageFlags(vk.ShaderStageFragmentBit), 0, 96, unsafe.Pointer(&push[0]))
	vk.CmdDraw(cmd, 3, 1, 0, 0)
	vk.CmdEndRenderPass(cmd)

	// AO denoise blur (worldambientblur.frag) — reads the buffer just
	// written above (am.img, still bound as setBlurSrc's sampler) and
	// writes am.imgBlur, which worldblit.frag actually samples (setSample
	// points there — see worldCreateAmbientPass). Safe to read am.img
	// immediately: rp's own external subpass dependency (the same one that
	// already orders a later frame's write against a previous frame's read)
	// equally covers this same-frame, immediately-following read.
	if am.pipeBlur == nil {
		return
	}
	vk.CmdBeginRenderPass(cmd, &vk.RenderPassBeginInfo{
		SType: vk.StructureTypeRenderPassBeginInfo, RenderPass: am.rp, Framebuffer: am.fbBlur,
		RenderArea:      vk.Rect2D{Extent: vk.Extent2D{Width: uint32(am.w), Height: uint32(am.h)}},
		ClearValueCount: 1, PClearValues: []vk.ClearValue{vk.NewClearValue([]float32{1, 1, 0, 1})},
	}, vk.SubpassContentsInline)
	vk.CmdBindPipeline(cmd, vk.PipelineBindPointGraphics, am.pipeBlur)
	vk.CmdSetViewport(cmd, 0, 1, []vk.Viewport{fullViewport(am.w, am.h)})
	vk.CmdSetScissor(cmd, 0, 1, []vk.Rect2D{fullScissor(am.w, am.h)})
	vk.CmdBindDescriptorSets(cmd, vk.PipelineBindPointGraphics, am.plBlur, 0, 1, []vk.DescriptorSet{am.setBlurSrc}, 0, nil)
	blurPush := [4]float32{1 / float32(am.w), 1 / float32(am.h), 1.5, 0} // offset: 1.5 quarter-res texels
	vk.CmdPushConstants(cmd, am.plBlur, vk.ShaderStageFlags(vk.ShaderStageFragmentBit), 0, 16, unsafe.Pointer(&blurPush[0]))
	vk.CmdDraw(cmd, 3, 1, 0, 0)
	vk.CmdEndRenderPass(cmd)
}

func (r *Renderer) destroyAmbientPass() {
	wr := r.wr
	if wr == nil {
		return
	}
	am := &wr.ambient
	dev := r.device
	if am.pipeGen != nil {
		vk.DestroyPipeline(dev, am.pipeGen, nil)
	}
	if am.plGen != nil {
		vk.DestroyPipelineLayout(dev, am.plGen, nil)
	}
	if am.pipeBlur != nil {
		vk.DestroyPipeline(dev, am.pipeBlur, nil)
	}
	if am.plBlur != nil {
		vk.DestroyPipelineLayout(dev, am.plBlur, nil)
	}
	if am.pool != nil {
		vk.DestroyDescriptorPool(dev, am.pool, nil)
	}
	if am.dslSrc != nil {
		vk.DestroyDescriptorSetLayout(dev, am.dslSrc, nil)
	}
	if am.dslSample != nil {
		vk.DestroyDescriptorSetLayout(dev, am.dslSample, nil)
	}
	if am.dslBlurSrc != nil {
		vk.DestroyDescriptorSetLayout(dev, am.dslBlurSrc, nil)
	}
	if am.fb != nil {
		vk.DestroyFramebuffer(dev, am.fb, nil)
	}
	if am.fbBlur != nil {
		vk.DestroyFramebuffer(dev, am.fbBlur, nil)
	}
	if am.rp != nil {
		vk.DestroyRenderPass(dev, am.rp, nil)
	}
	freeLitImage(dev, am.img)
	freeLitImage(dev, am.imgBlur)
	*am = worldAmbient{}
}
