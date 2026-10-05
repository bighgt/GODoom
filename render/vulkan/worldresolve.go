package vulkan

import (
	_ "embed"
	"fmt"
	"unsafe"

	vk "github.com/goki/vulkan"
)

// Full-render-resolution "resolve" pass for the hardware path: water SSR +
// the ambient (SSAO/god-ray/dyn-shadow) combine + the ACES tonemap, all run
// here exactly once per pixel into one shared LDR image, instead of inside
// worldblit.frag itself. worldblit.frag used to re-derive that whole HDR ->
// LDR chain (2 texture fetches + a full 3-channel ACES curve) as its own
// FXAA/CAS neighbour taps' "mappedAt" helper — up to 8-10 times per pixel,
// since each tap needs a colour value at a different screen position. That
// made post-process AA/sharpen the single most expensive part of the whole
// pipeline, bigger than the world pass's own per-fragment lighting, despite
// running on effectively every opaque screen pixel. Resolving once here and
// letting worldblit.frag's taps do a plain texture() read of the result
// instead is the standard architecture for post-process AA — this codebase
// only skipped it because the pass boundary didn't exist yet (see
// worldblit.frag's own header for what moved and what stayed).
//
// Shape mirrors worldambient.go (one shared image reused every frame within
// the same command buffer, built via wMakeImage + the generic
// litPipeline/litPipelineLayout helpers, its own small fixed-size
// descriptor pool kept apart from worldsetup.go's shared framePool) but at
// full wr.w x wr.h instead of quarter-res, one pass instead of
// generate-then-blur, and an 8-bit LDR output format (the input is already
// tonemapped 0..1 here, so there's nothing to gain from keeping it in the
// HDR format the scene/bloom images use — this is a bandwidth cut on top of
// the ALU one).

//go:embed shaders/worldresolve.frag.spv
var worldResolveFragSPV []byte

// resolveFormat is 8-bit LDR: worldresolve.frag's output is already past
// the ACES tonemap (0..1), so there's no HDR precision left to preserve —
// keeping it in sceneHDRFormat (RGBA16F) would just double the bandwidth
// every one of worldblit.frag's FXAA/CAS taps then has to move for no
// visual benefit.
const resolveFormat = vk.FormatR8g8b8a8Unorm

// worldResolve holds the resolve pass's resources: one shared image at the
// render resolution (not double-buffered — every slot's pass writes into
// the same image in turn, exactly like worldbloom.go/worldambient.go's own
// shared images), its render pass/framebuffer/pipeline, and descriptors.
type worldResolve struct {
	img litImage
	fb  vk.Framebuffer
	rp  vk.RenderPass

	// dslSrc: binding0 = this slot's scene colour, binding1 = the shared
	// bloom mip-0 image, binding2 = this slot's depth, binding3 = the
	// shared (blurred) ambient term.
	dslSrc    vk.DescriptorSetLayout
	dslSample vk.DescriptorSetLayout // binding0 = the shared resolved image — worldblit.frag's 2nd descriptor set
	pool      vk.DescriptorPool

	plGen   vk.PipelineLayout
	pipeGen vk.Pipeline

	setSrc    [maxFramesInFlight]vk.DescriptorSet
	setSample vk.DescriptorSet // one set: the image it points at never changes per slot
}

// worldCreateResolvePass builds the resolve pass. Must run after
// worldCreateOffscreen (needs sl.scene/sl.depth), worldCreateBloom (needs
// wr.bloom.mip[0]) and worldCreateAmbientPass (needs wr.ambient.imgBlur),
// and before worldCreatePipelines (needs wr.resolve.dslSample for plBlit's
// second descriptor set).
func (r *Renderer) worldCreateResolvePass() error {
	wr := r.wr
	w, h := wr.w, wr.h

	var err error
	wr.resolve.img, err = r.wMakeImage(w, h, resolveFormat,
		vk.ImageUsageFlags(vk.ImageUsageColorAttachmentBit)|vk.ImageUsageFlags(vk.ImageUsageSampledBit),
		vk.ImageAspectFlags(vk.ImageAspectColorBit), 1)
	if err != nil {
		return err
	}

	rp, err := r.newSampledColorPass(resolveFormat, vk.AttachmentLoadOpClear, vk.ImageLayoutUndefined, "resolve")
	if err != nil {
		return err
	}
	wr.resolve.rp = rp

	fb, err := r.newFramebuffer(rp, w, h, "resolve", wr.resolve.img.view)
	if err != nil {
		return err
	}
	wr.resolve.fb = fb

	if wr.resolve.dslSrc, err = r.newDescriptorSetLayout("resolve", samplerBinding(0), samplerBinding(1), samplerBinding(2), samplerBinding(3)); err != nil {
		return err
	}
	if wr.resolve.dslSample, err = r.newDescriptorSetLayout("resolve", samplerBinding(0)); err != nil {
		return err
	}

	// One dedicated, fixed-size pool — sized once here and never grown or
	// touched again, kept entirely separate from worldCreateDescriptors'
	// shared framePool (see that pool's own "GOTCHA that bit twice" note:
	// this pass never touches its arithmetic).
	poolInfo := vk.DescriptorPoolCreateInfo{
		SType:         vk.StructureTypeDescriptorPoolCreateInfo,
		MaxSets:       uint32(maxFramesInFlight + 1), // setSrc per slot + shared setSample
		PoolSizeCount: 1,
		PPoolSizes: []vk.DescriptorPoolSize{
			// 4 samplers per setSrc, + 1 for the shared setSample. No UBO —
			// everything this pass needs travels through the push constant.
			{Type: vk.DescriptorTypeCombinedImageSampler, DescriptorCount: uint32(4*maxFramesInFlight + 1)},
		},
	}
	var pool vk.DescriptorPool
	if res := vk.CreateDescriptorPool(r.device, &poolInfo, nil, &pool); res != vk.Success {
		return fmt.Errorf("vulkan: resolve descriptor pool: %s", vk.Error(res))
	}
	wr.resolve.pool = pool

	alloc := func(layout vk.DescriptorSetLayout) (vk.DescriptorSet, error) {
		return r.allocDescriptorSet(pool, layout, "resolve")
	}

	var writes []vk.WriteDescriptorSet
	for i := range wr.slots {
		sl := &wr.slots[i]
		s, aerr := alloc(wr.resolve.dslSrc)
		if aerr != nil {
			return aerr
		}
		wr.resolve.setSrc[i] = s
		writes = append(writes,
			imageWrite(s, 0, wr.clampSampler, sl.scene.view),
			imageWrite(s, 1, wr.clampSampler, wr.bloom.mip[0].img.view),
			imageWrite(s, 2, wr.depthSampler, sl.depth.view),
			imageWrite(s, 3, wr.clampSampler, wr.ambient.imgBlur.view),
		)
	}
	ss, err := alloc(wr.resolve.dslSample)
	if err != nil {
		return err
	}
	wr.resolve.setSample = ss
	writes = append(writes, imageWrite(ss, 0, wr.clampSampler, wr.resolve.img.view))
	vk.UpdateDescriptorSets(r.device, uint32(len(writes)), writes, 0, nil)

	if wr.resolve.plGen, err = r.litPipelineLayout(wr.resolve.dslSrc, 64); err != nil {
		return err
	}
	if wr.resolve.pipeGen, err = r.litPipeline(worldResolveFragSPV, wr.resolve.rp, wr.resolve.plGen); err != nil {
		return err
	}
	return nil
}

// recordResolve draws this frame's resolved (SSR + ambient-combined +
// tonemapped) LDR buffer. Called from recordWorldCommandBuffer right after
// the bloom pass and before the present blit. pc0 is the SAME first 64
// bytes (p0..p3) recordWorldCommandBuffer already built for the present
// blit's own push constant (blitPC) — this pass needs exactly that subset
// (exposure/bloomStrength/waterClock/nearPlane, the camera basis, ssr/
// godray strength) and nothing else, so the caller passes it through rather
// than this file re-deriving its own copy that could drift out of sync.
func (r *Renderer) recordResolve(cmd vk.CommandBuffer, frame int, pc0 *[16]float32) {
	wr := r.wr
	rs := &wr.resolve
	if rs.pipeGen == nil {
		return
	}
	vk.CmdBeginRenderPass(cmd, &vk.RenderPassBeginInfo{
		SType: vk.StructureTypeRenderPassBeginInfo, RenderPass: rs.rp, Framebuffer: rs.fb,
		RenderArea:      vk.Rect2D{Extent: vk.Extent2D{Width: uint32(wr.w), Height: uint32(wr.h)}},
		ClearValueCount: 1, PClearValues: []vk.ClearValue{vk.NewClearValue([]float32{0, 0, 0, 1})},
	}, vk.SubpassContentsInline)
	vk.CmdBindPipeline(cmd, vk.PipelineBindPointGraphics, rs.pipeGen)
	vk.CmdSetViewport(cmd, 0, 1, []vk.Viewport{fullViewport(wr.w, wr.h)})
	vk.CmdSetScissor(cmd, 0, 1, []vk.Rect2D{fullScissor(wr.w, wr.h)})
	vk.CmdBindDescriptorSets(cmd, vk.PipelineBindPointGraphics, rs.plGen, 0, 1, []vk.DescriptorSet{rs.setSrc[frame]}, 0, nil)
	vk.CmdPushConstants(cmd, rs.plGen, vk.ShaderStageFlags(vk.ShaderStageFragmentBit), 0, 64, unsafe.Pointer(pc0))
	vk.CmdDraw(cmd, 3, 1, 0, 0)
	vk.CmdEndRenderPass(cmd)
}

func (r *Renderer) destroyResolvePass() {
	wr := r.wr
	if wr == nil {
		return
	}
	rs := &wr.resolve
	dev := r.device
	if rs.pipeGen != nil {
		vk.DestroyPipeline(dev, rs.pipeGen, nil)
	}
	if rs.plGen != nil {
		vk.DestroyPipelineLayout(dev, rs.plGen, nil)
	}
	if rs.pool != nil {
		vk.DestroyDescriptorPool(dev, rs.pool, nil)
	}
	if rs.dslSrc != nil {
		vk.DestroyDescriptorSetLayout(dev, rs.dslSrc, nil)
	}
	if rs.dslSample != nil {
		vk.DestroyDescriptorSetLayout(dev, rs.dslSample, nil)
	}
	if rs.fb != nil {
		vk.DestroyFramebuffer(dev, rs.fb, nil)
	}
	if rs.rp != nil {
		vk.DestroyRenderPass(dev, rs.rp, nil)
	}
	freeLitImage(dev, rs.img)
	*rs = worldResolve{}
}
