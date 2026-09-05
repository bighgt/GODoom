package vulkan

import (
	_ "embed"
	"fmt"
	"math"
	"unsafe"

	vk "github.com/goki/vulkan"

	"twopointfive/render/worldgeo"
)

// Quarter-resolution dynamic-light shadow-occlusion pass for the hardware
// path. dynShadow() (worldshadow.frag) marches this slot's full-res depth
// buffer toward the leading dynamic lights exactly as it used to inline in
// worldblit.frag, but now writes its result into ONE shared, low-resolution
// offscreen image instead of running per full-res pixel. worldblit.frag then
// samples that image back at full res through the ordinary linear/clamp
// sampler (wr.clampSampler) — the low-res-then-bilinear-upsample read IS the
// soft blur on the shadow edge, no separate blur pass needed, the same way
// worldbloom.go's mip pyramid leans on hardware filtering rather than a
// hand-rolled box/Gaussian kernel.
//
// Shape mirrors worldbloom.go (one shared image reused every frame, built
// via wMakeImage + the generic litPipeline/litPipelineLayout helpers), but
// with its own small, FIXED-size descriptor pool kept entirely separate from
// the shared framePool in worldsetup.go — this pass never touches that
// pool's already-fragile arithmetic.

//go:embed shaders/worldshadow.frag.spv
var worldShadowFragSPV []byte

// dynShadowStrength/March/Bias are the shadow march's tuning constants —
// named constants rather than a config knob since they're an internal
// quality/cost tradeoff, not something a player would tune. strength 0
// would disable the pass; march is how far along the ray-to-light to search
// for an occluder; bias is the depth-difference slack (camera-forward map
// units) before a sample counts as an occluder, so a surface doesn't shadow
// itself.
const (
	dynShadowStrength = 0.6
	dynShadowMarch    = 320.0
	dynShadowBias     = 2.0
)

// worldShadow holds the shadow-term pass's resources: one shared image at
// wr.w/4 x wr.h/4 (not double-buffered — every slot's pass writes into the
// same image in turn, exactly like worldbloom.go's mip images), its render
// pass/framebuffer/pipeline, and descriptors.
type worldShadow struct {
	img  litImage
	w, h int
	fb   vk.Framebuffer
	rp   vk.RenderPass

	dslSrc    vk.DescriptorSetLayout // binding0 = this slot's depth, binding1 = this slot's World UBO
	dslSample vk.DescriptorSetLayout // binding0 = the shared shadow image — worldblit's 2nd descriptor set
	pool      vk.DescriptorPool

	plGen   vk.PipelineLayout
	pipeGen vk.Pipeline

	setSrc    [maxFramesInFlight]vk.DescriptorSet
	setSample vk.DescriptorSet // one set: the image it points at never changes per slot
}

func (r *Renderer) worldCreateShadowPass() error {
	wr := r.wr
	w, h := wr.w/4, wr.h/4
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	wr.shadow.w, wr.shadow.h = w, h

	var err error
	wr.shadow.img, err = r.wMakeImage(w, h, sceneHDRFormat,
		vk.ImageUsageFlags(vk.ImageUsageColorAttachmentBit)|vk.ImageUsageFlags(vk.ImageUsageSampledBit),
		vk.ImageAspectFlags(vk.ImageAspectColorBit), 1)
	if err != nil {
		return err
	}

	// Render pass: one colour attachment, cleared then written every frame,
	// left shader-readable for worldblit.frag's bilinear sample. The same
	// two SubpassDependency entries worldCreateSceneRenderPass uses for its
	// own shared, frame-reused image: a previous frame's blit read must
	// finish before this frame's clear/write, and this frame's write must
	// be visible to the blit's later read.
	color := vk.AttachmentDescription{
		Format: sceneHDRFormat, Samples: vk.SampleCount1Bit,
		LoadOp: vk.AttachmentLoadOpClear, StoreOp: vk.AttachmentStoreOpStore,
		StencilLoadOp: vk.AttachmentLoadOpDontCare, StencilStoreOp: vk.AttachmentStoreOpDontCare,
		InitialLayout: vk.ImageLayoutUndefined, FinalLayout: vk.ImageLayoutShaderReadOnlyOptimal,
	}
	ref := vk.AttachmentReference{Attachment: 0, Layout: vk.ImageLayoutColorAttachmentOptimal}
	sub := vk.SubpassDescription{
		PipelineBindPoint: vk.PipelineBindPointGraphics, ColorAttachmentCount: 1,
		PColorAttachments: []vk.AttachmentReference{ref},
	}
	deps := []vk.SubpassDependency{
		{
			SrcSubpass:    vk.SubpassExternal,
			DstSubpass:    0,
			SrcStageMask:  vk.PipelineStageFlags(vk.PipelineStageFragmentShaderBit),
			DstStageMask:  vk.PipelineStageFlags(vk.PipelineStageColorAttachmentOutputBit),
			SrcAccessMask: vk.AccessFlags(vk.AccessShaderReadBit),
			DstAccessMask: vk.AccessFlags(vk.AccessColorAttachmentWriteBit),
		},
		{
			SrcSubpass:    0,
			DstSubpass:    vk.SubpassExternal,
			SrcStageMask:  vk.PipelineStageFlags(vk.PipelineStageColorAttachmentOutputBit),
			DstStageMask:  vk.PipelineStageFlags(vk.PipelineStageFragmentShaderBit),
			SrcAccessMask: vk.AccessFlags(vk.AccessColorAttachmentWriteBit),
			DstAccessMask: vk.AccessFlags(vk.AccessShaderReadBit),
		},
	}
	rpInfo := vk.RenderPassCreateInfo{
		SType: vk.StructureTypeRenderPassCreateInfo, AttachmentCount: 1,
		PAttachments: []vk.AttachmentDescription{color}, SubpassCount: 1,
		PSubpasses: []vk.SubpassDescription{sub}, DependencyCount: uint32(len(deps)), PDependencies: deps,
	}
	var rp vk.RenderPass
	if res := vk.CreateRenderPass(r.device, &rpInfo, nil, &rp); res != vk.Success {
		return fmt.Errorf("vulkan: shadow render pass: %s", vk.Error(res))
	}
	wr.shadow.rp = rp

	fbInfo := vk.FramebufferCreateInfo{
		SType: vk.StructureTypeFramebufferCreateInfo, RenderPass: rp,
		AttachmentCount: 1, PAttachments: []vk.ImageView{wr.shadow.img.view},
		Width: uint32(w), Height: uint32(h), Layers: 1,
	}
	var fb vk.Framebuffer
	if res := vk.CreateFramebuffer(r.device, &fbInfo, nil, &fb); res != vk.Success {
		return fmt.Errorf("vulkan: shadow framebuffer: %s", vk.Error(res))
	}
	wr.shadow.fb = fb

	mk := func(bindings []vk.DescriptorSetLayoutBinding) (vk.DescriptorSetLayout, error) {
		info := vk.DescriptorSetLayoutCreateInfo{
			SType:        vk.StructureTypeDescriptorSetLayoutCreateInfo,
			BindingCount: uint32(len(bindings)),
			PBindings:    bindings,
		}
		var l vk.DescriptorSetLayout
		if res := vk.CreateDescriptorSetLayout(r.device, &info, nil, &l); res != vk.Success {
			return nil, fmt.Errorf("vulkan: shadow dsl: %s", vk.Error(res))
		}
		return l, nil
	}
	uboBinding := vk.DescriptorSetLayoutBinding{
		Binding: 1, DescriptorType: vk.DescriptorTypeUniformBuffer, DescriptorCount: 1,
		StageFlags: vk.ShaderStageFlags(vk.ShaderStageFragmentBit),
	}
	if wr.shadow.dslSrc, err = mk([]vk.DescriptorSetLayoutBinding{samplerBinding(0), uboBinding}); err != nil {
		return err
	}
	if wr.shadow.dslSample, err = mk([]vk.DescriptorSetLayoutBinding{samplerBinding(0)}); err != nil {
		return err
	}

	// One dedicated, fixed-size pool — sized once here and never grown or
	// touched again, and kept entirely separate from worldCreateDescriptors'
	// shared framePool so this pass can't perturb that pool's arithmetic.
	poolInfo := vk.DescriptorPoolCreateInfo{
		SType:         vk.StructureTypeDescriptorPoolCreateInfo,
		MaxSets:       uint32(maxFramesInFlight + 1), // setSrc per slot + one shared setSample
		PoolSizeCount: 2,
		PPoolSizes: []vk.DescriptorPoolSize{
			// depth sampler per setSrc, + one sampler for the shared setSample.
			{Type: vk.DescriptorTypeCombinedImageSampler, DescriptorCount: uint32(maxFramesInFlight + 1)},
			// World UBO per setSrc.
			{Type: vk.DescriptorTypeUniformBuffer, DescriptorCount: uint32(maxFramesInFlight)},
		},
	}
	var pool vk.DescriptorPool
	if res := vk.CreateDescriptorPool(r.device, &poolInfo, nil, &pool); res != vk.Success {
		return fmt.Errorf("vulkan: shadow descriptor pool: %s", vk.Error(res))
	}
	wr.shadow.pool = pool

	alloc := func(layout vk.DescriptorSetLayout) (vk.DescriptorSet, error) {
		info := vk.DescriptorSetAllocateInfo{
			SType: vk.StructureTypeDescriptorSetAllocateInfo, DescriptorPool: pool,
			DescriptorSetCount: 1, PSetLayouts: []vk.DescriptorSetLayout{layout},
		}
		var s vk.DescriptorSet
		if res := vk.AllocateDescriptorSets(r.device, &info, &s); res != vk.Success {
			return nil, fmt.Errorf("vulkan: shadow descriptor set: %s", vk.Error(res))
		}
		return s, nil
	}

	var writes []vk.WriteDescriptorSet
	for i := range wr.slots {
		sl := &wr.slots[i]
		s, aerr := alloc(wr.shadow.dslSrc)
		if aerr != nil {
			return aerr
		}
		wr.shadow.setSrc[i] = s
		writes = append(writes,
			vk.WriteDescriptorSet{
				SType: vk.StructureTypeWriteDescriptorSet, DstSet: s, DstBinding: 0,
				DescriptorCount: 1, DescriptorType: vk.DescriptorTypeCombinedImageSampler,
				PImageInfo: []vk.DescriptorImageInfo{{
					Sampler: wr.depthSampler, ImageView: sl.depth.view, ImageLayout: vk.ImageLayoutShaderReadOnlyOptimal,
				}},
			},
			vk.WriteDescriptorSet{
				SType: vk.StructureTypeWriteDescriptorSet, DstSet: s, DstBinding: 1,
				DescriptorCount: 1, DescriptorType: vk.DescriptorTypeUniformBuffer,
				PBufferInfo: []vk.DescriptorBufferInfo{{Buffer: sl.ubo, Offset: 0, Range: vk.DeviceSize(worldUBOFloats * 4)}},
			},
		)
	}
	ss, err := alloc(wr.shadow.dslSample)
	if err != nil {
		return err
	}
	wr.shadow.setSample = ss
	writes = append(writes, vk.WriteDescriptorSet{
		SType: vk.StructureTypeWriteDescriptorSet, DstSet: ss, DstBinding: 0,
		DescriptorCount: 1, DescriptorType: vk.DescriptorTypeCombinedImageSampler,
		PImageInfo: []vk.DescriptorImageInfo{{
			Sampler: wr.clampSampler, ImageView: wr.shadow.img.view, ImageLayout: vk.ImageLayoutShaderReadOnlyOptimal,
		}},
	})
	vk.UpdateDescriptorSets(r.device, uint32(len(writes)), writes, 0, nil)

	if wr.shadow.plGen, err = r.litPipelineLayout(wr.shadow.dslSrc, 64); err != nil {
		return err
	}
	if wr.shadow.pipeGen, err = r.litPipeline(worldShadowFragSPV, wr.shadow.rp, wr.shadow.plGen); err != nil {
		return err
	}
	return nil
}

// recordShadowTerm draws the quarter-res occlusion buffer for this slot.
// Called from recordWorldCommandBuffer right after the world pass ends and
// before the bloom pass — cam gives it the camera basis needed to
// reconstruct a world point from (screen pixel, sampled depth), the same
// derivation the present blit pass makes for its own push constants (kept
// separate here rather than threading it through as a parameter, so this
// function stays self-contained).
func (r *Renderer) recordShadowTerm(cmd vk.CommandBuffer, frame int, cam worldgeo.Camera) {
	wr := r.wr
	sh := &wr.shadow
	if sh.pipeGen == nil {
		return
	}
	bFocal := worldgeo.FocalFor(cam, wr.h)
	bSin := float32(math.Sin(cam.Angle))
	bCos := float32(math.Cos(cam.Angle))
	bHorizon := float32(wr.h)*0.5 + float32(math.Tan(cam.Pitch)*bFocal)

	vk.CmdBeginRenderPass(cmd, &vk.RenderPassBeginInfo{
		SType: vk.StructureTypeRenderPassBeginInfo, RenderPass: sh.rp, Framebuffer: sh.fb,
		RenderArea:      vk.Rect2D{Extent: vk.Extent2D{Width: uint32(sh.w), Height: uint32(sh.h)}},
		ClearValueCount: 1, PClearValues: []vk.ClearValue{vk.NewClearValue([]float32{1, 1, 1, 1})}, // 1 = unshadowed
	}, vk.SubpassContentsInline)
	vk.CmdBindPipeline(cmd, vk.PipelineBindPointGraphics, sh.pipeGen)
	vk.CmdSetViewport(cmd, 0, 1, []vk.Viewport{fullViewport(sh.w, sh.h)})
	vk.CmdSetScissor(cmd, 0, 1, []vk.Rect2D{fullScissor(sh.w, sh.h)})
	vk.CmdBindDescriptorSets(cmd, vk.PipelineBindPointGraphics, sh.plGen, 0, 1, []vk.DescriptorSet{sh.setSrc[frame]}, 0, nil)

	push := [16]float32{
		float32(worldgeo.NearPlane), float32(worldgeo.FarPlane), bSin, bCos,
		float32(bFocal), float32(cam.X), float32(cam.Y), float32(cam.Z),
		bHorizon, float32(wr.w), float32(wr.h), float32(cam.ShadowCasterN),
		dynShadowStrength, dynShadowMarch, dynShadowBias, 0,
	}
	vk.CmdPushConstants(cmd, sh.plGen, vk.ShaderStageFlags(vk.ShaderStageFragmentBit), 0, 64, unsafe.Pointer(&push[0]))
	vk.CmdDraw(cmd, 3, 1, 0, 0)
	vk.CmdEndRenderPass(cmd)
}

func (r *Renderer) destroyShadowPass() {
	wr := r.wr
	if wr == nil {
		return
	}
	sh := &wr.shadow
	dev := r.device
	if sh.pipeGen != nil {
		vk.DestroyPipeline(dev, sh.pipeGen, nil)
	}
	if sh.plGen != nil {
		vk.DestroyPipelineLayout(dev, sh.plGen, nil)
	}
	if sh.pool != nil {
		vk.DestroyDescriptorPool(dev, sh.pool, nil)
	}
	if sh.dslSrc != nil {
		vk.DestroyDescriptorSetLayout(dev, sh.dslSrc, nil)
	}
	if sh.dslSample != nil {
		vk.DestroyDescriptorSetLayout(dev, sh.dslSample, nil)
	}
	if sh.fb != nil {
		vk.DestroyFramebuffer(dev, sh.fb, nil)
	}
	if sh.rp != nil {
		vk.DestroyRenderPass(dev, sh.rp, nil)
	}
	freeLitImage(dev, sh.img)
	*sh = worldShadow{}
}
