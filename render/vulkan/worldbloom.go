package vulkan

import (
	_ "embed"
	"fmt"
	"unsafe"

	vk "github.com/goki/vulkan"
)

// HDR bloom for the hardware path — a true mip pyramid, the wide soft
// filmic glow a single-resolution blur can't reach. The world pass writes
// an RGBA16F scene image (dynamic lights pile past 1.0); this file
// bright-passes it to a half-res mip 0, downsamples through a chain of ever
// smaller mips, then upsamples back up ADDING each level's blur into the
// one above. The result sits in mip 0, which worldblit.frag samples and
// tonemaps as scene*exposure + bloom*strength.
//
// Its shaders are hardware-path-only (worldbright / worldbloomdown /
// worldbloomup) so tuning bloom never disturbs the lit path's shared
// bright.frag / blur.frag.

//go:embed shaders/worldbright.frag.spv
var worldBrightFragSPV []byte

//go:embed shaders/worldbloomdown.frag.spv
var worldBloomDownFragSPV []byte

//go:embed shaders/worldbloomup.frag.spv
var worldBloomUpFragSPV []byte

const (
	sceneHDRFormat = vk.FormatR16g16b16a16Sfloat
	// bloomMips: mip 0 is half the render resolution, each next one half
	// again. 5 spans down to ~1/32 res — wide enough that a bright source
	// glows most of the screen, small enough the extra passes are ~free.
	bloomMips = 5
)

// bloomMip is one level of the pyramid: its image, its size, a framebuffer
// for each of the two render passes (clear-load vs. load-and-add), and the
// descriptor that samples it.
type bloomMip struct {
	img     litImage
	w, h    int
	fbClear vk.Framebuffer
	fbAdd   vk.Framebuffer
	setSelf vk.DescriptorSet
}

type worldBloom struct {
	mip [bloomMips]bloomMip

	rpClear vk.RenderPass // LoadOp CLEAR — bright pass + every downsample
	rpAdd   vk.RenderPass // LoadOp LOAD + ONE:ONE blend — every upsample

	dsl  vk.DescriptorSetLayout // one combined-image-sampler at binding 0
	pool vk.DescriptorPool
	pl   vk.PipelineLayout

	pipeBright vk.Pipeline // scene   -> mip 0        (rpClear)
	pipeDown   vk.Pipeline // mip i   -> mip i+1      (rpClear)
	pipeUp     vk.Pipeline // mip i+1 -> mip i (add)  (rpAdd)

	setScene [maxFramesInFlight]vk.DescriptorSet // samples slot i's scene image

	// tuning (set by SetHDR, sane defaults here)
	exposure  float32
	strength  float32
	threshold float32
}

// SetHDR sets the hardware path's tonemap exposure and bloom strength
// (config exposure / a fixed bloom look). No-op on a non-hardware renderer.
func (r *Renderer) SetHDR(exposure, bloomStrength float32) {
	if r == nil || !r.hw || r.wr == nil {
		return
	}
	if exposure > 0 {
		r.wr.bloom.exposure = exposure
	}
	if bloomStrength >= 0 {
		r.wr.bloom.strength = bloomStrength
	}
}

func (r *Renderer) worldCreateBloom() error {
	wr := r.wr
	bl := &wr.bloom
	if bl.exposure == 0 {
		bl.exposure = 1
	}
	if bl.strength == 0 {
		bl.strength = 0.6
	}
	if bl.threshold == 0 {
		// With config lightScale often < 1 the whole scene sits under 1.0, so
		// a threshold near 1.0 would never bloom. High enough that plainly
		// lit walls/flats DON'T glow — only torch-/plasma-lit patches, the
		// sky, emissive texels and dynamic-light hotspots clear it. The
		// Karis-weighted bright pass keeps a lone hot texel from sparkling.
		bl.threshold = 0.55
	}

	// Mip sizes: half the render res, halving each level, floored at 1.
	mw, mh := wr.w/2, wr.h/2
	for i := 0; i < bloomMips; i++ {
		if mw < 1 {
			mw = 1
		}
		if mh < 1 {
			mh = 1
		}
		bl.mip[i].w, bl.mip[i].h = mw, mh
		mw /= 2
		mh /= 2
	}

	attachSampled := vk.ImageUsageFlags(vk.ImageUsageColorAttachmentBit) | vk.ImageUsageFlags(vk.ImageUsageSampledBit)
	for i := 0; i < bloomMips; i++ {
		img, err := r.wMakeImage(bl.mip[i].w, bl.mip[i].h, sceneHDRFormat, attachSampled, vk.ImageAspectFlags(vk.ImageAspectColorBit), 1)
		if err != nil {
			return err
		}
		bl.mip[i].img = img
	}

	// Two render passes, RGBA16F, both leaving the target shader-readable.
	// rpClear discards the previous contents (bright pass, downsample);
	// rpAdd keeps them (the upsample blends ONE:ONE onto the mip's own
	// downsampled content). Symmetric SubpassExternal deps so consecutive
	// passes serialise their read-after-write / write-after-read on the
	// shared mip images (the pyramid reuses images across frames-in-flight,
	// like the old ping-pong bloom did).
	mkRP := func(loadOp vk.AttachmentLoadOp, initLayout vk.ImageLayout) (vk.RenderPass, error) {
		att := vk.AttachmentDescription{
			Format: sceneHDRFormat, Samples: vk.SampleCount1Bit,
			LoadOp: loadOp, StoreOp: vk.AttachmentStoreOpStore,
			StencilLoadOp: vk.AttachmentLoadOpDontCare, StencilStoreOp: vk.AttachmentStoreOpDontCare,
			InitialLayout: initLayout, FinalLayout: vk.ImageLayoutShaderReadOnlyOptimal,
		}
		ref := vk.AttachmentReference{Attachment: 0, Layout: vk.ImageLayoutColorAttachmentOptimal}
		sub := vk.SubpassDescription{
			PipelineBindPoint: vk.PipelineBindPointGraphics, ColorAttachmentCount: 1,
			PColorAttachments: []vk.AttachmentReference{ref},
		}
		deps := []vk.SubpassDependency{
			{
				SrcSubpass: vk.SubpassExternal, DstSubpass: 0,
				SrcStageMask:  vk.PipelineStageFlags(vk.PipelineStageFragmentShaderBit),
				DstStageMask:  vk.PipelineStageFlags(vk.PipelineStageColorAttachmentOutputBit),
				SrcAccessMask: vk.AccessFlags(vk.AccessShaderReadBit),
				DstAccessMask: vk.AccessFlags(vk.AccessColorAttachmentReadBit) | vk.AccessFlags(vk.AccessColorAttachmentWriteBit),
			},
			{
				SrcSubpass: 0, DstSubpass: vk.SubpassExternal,
				SrcStageMask:  vk.PipelineStageFlags(vk.PipelineStageColorAttachmentOutputBit),
				DstStageMask:  vk.PipelineStageFlags(vk.PipelineStageFragmentShaderBit),
				SrcAccessMask: vk.AccessFlags(vk.AccessColorAttachmentWriteBit),
				DstAccessMask: vk.AccessFlags(vk.AccessShaderReadBit),
			},
		}
		info := vk.RenderPassCreateInfo{
			SType: vk.StructureTypeRenderPassCreateInfo, AttachmentCount: 1, PAttachments: []vk.AttachmentDescription{att},
			SubpassCount: 1, PSubpasses: []vk.SubpassDescription{sub},
			DependencyCount: uint32(len(deps)), PDependencies: deps,
		}
		var rp vk.RenderPass
		if res := vk.CreateRenderPass(r.device, &info, nil, &rp); res != vk.Success {
			return nil, fmt.Errorf("vulkan: bloom render pass: %s", vk.Error(res))
		}
		return rp, nil
	}
	var err error
	if bl.rpClear, err = mkRP(vk.AttachmentLoadOpClear, vk.ImageLayoutUndefined); err != nil {
		return err
	}
	if bl.rpAdd, err = mkRP(vk.AttachmentLoadOpLoad, vk.ImageLayoutShaderReadOnlyOptimal); err != nil {
		return err
	}

	mkFB := func(rp vk.RenderPass, view vk.ImageView, w, h int) (vk.Framebuffer, error) {
		info := vk.FramebufferCreateInfo{
			SType: vk.StructureTypeFramebufferCreateInfo, RenderPass: rp,
			AttachmentCount: 1, PAttachments: []vk.ImageView{view},
			Width: uint32(w), Height: uint32(h), Layers: 1,
		}
		var fb vk.Framebuffer
		if res := vk.CreateFramebuffer(r.device, &info, nil, &fb); res != vk.Success {
			return nil, fmt.Errorf("vulkan: bloom framebuffer: %s", vk.Error(res))
		}
		return fb, nil
	}
	for i := 0; i < bloomMips; i++ {
		if bl.mip[i].fbClear, err = mkFB(bl.rpClear, bl.mip[i].img.view, bl.mip[i].w, bl.mip[i].h); err != nil {
			return err
		}
		if bl.mip[i].fbAdd, err = mkFB(bl.rpAdd, bl.mip[i].img.view, bl.mip[i].w, bl.mip[i].h); err != nil {
			return err
		}
	}

	// Descriptors: one sampler binding. Sets: one per frame-in-flight
	// sampling that slot's scene, plus one per mip sampling itself.
	// NB: Vulkan handle-outs go through a local var, never &bl.field directly —
	// bl is inside the big worldResources heap object, and passing a pointer
	// into it to a cgo call trips Go's cgocheck ("Go pointer to unpinned Go
	// pointer") since the struct is full of pointer-typed vk handles.
	dslInfo := vk.DescriptorSetLayoutCreateInfo{
		SType: vk.StructureTypeDescriptorSetLayoutCreateInfo, BindingCount: 1,
		PBindings: []vk.DescriptorSetLayoutBinding{samplerBinding(0)},
	}
	var dsl vk.DescriptorSetLayout
	if res := vk.CreateDescriptorSetLayout(r.device, &dslInfo, nil, &dsl); res != vk.Success {
		return fmt.Errorf("vulkan: bloom dsl: %s", vk.Error(res))
	}
	bl.dsl = dsl

	nSets := maxFramesInFlight + bloomMips + 4
	poolInfo := vk.DescriptorPoolCreateInfo{
		SType:   vk.StructureTypeDescriptorPoolCreateInfo,
		MaxSets: uint32(nSets), PoolSizeCount: 1,
		PPoolSizes: []vk.DescriptorPoolSize{{Type: vk.DescriptorTypeCombinedImageSampler, DescriptorCount: uint32(nSets)}},
	}
	var pool vk.DescriptorPool
	if res := vk.CreateDescriptorPool(r.device, &poolInfo, nil, &pool); res != vk.Success {
		return fmt.Errorf("vulkan: bloom descriptor pool: %s", vk.Error(res))
	}
	bl.pool = pool

	alloc := func() (vk.DescriptorSet, error) {
		info := vk.DescriptorSetAllocateInfo{
			SType: vk.StructureTypeDescriptorSetAllocateInfo, DescriptorPool: bl.pool,
			DescriptorSetCount: 1, PSetLayouts: []vk.DescriptorSetLayout{bl.dsl},
		}
		var s vk.DescriptorSet
		if res := vk.AllocateDescriptorSets(r.device, &info, &s); res != vk.Success {
			return nil, fmt.Errorf("vulkan: bloom descriptor set: %s", vk.Error(res))
		}
		return s, nil
	}
	img := func(set vk.DescriptorSet, view vk.ImageView) vk.WriteDescriptorSet {
		return vk.WriteDescriptorSet{
			SType: vk.StructureTypeWriteDescriptorSet, DstSet: set, DstBinding: 0, DescriptorCount: 1,
			DescriptorType: vk.DescriptorTypeCombinedImageSampler,
			PImageInfo: []vk.DescriptorImageInfo{{
				Sampler: wr.clampSampler, ImageView: view, ImageLayout: vk.ImageLayoutShaderReadOnlyOptimal,
			}},
		}
	}
	var writes []vk.WriteDescriptorSet
	for i := range bl.setScene {
		if bl.setScene[i], err = alloc(); err != nil {
			return err
		}
		writes = append(writes, img(bl.setScene[i], wr.slots[i].scene.view))
	}
	for i := 0; i < bloomMips; i++ {
		if bl.mip[i].setSelf, err = alloc(); err != nil {
			return err
		}
		writes = append(writes, img(bl.mip[i].setSelf, bl.mip[i].img.view))
	}
	vk.UpdateDescriptorSets(r.device, uint32(len(writes)), writes, 0, nil)

	plInfo := vk.PipelineLayoutCreateInfo{
		SType: vk.StructureTypePipelineLayoutCreateInfo, SetLayoutCount: 1,
		PSetLayouts:            []vk.DescriptorSetLayout{bl.dsl},
		PushConstantRangeCount: 1,
		PPushConstantRanges:    []vk.PushConstantRange{{StageFlags: vk.ShaderStageFlags(vk.ShaderStageFragmentBit), Offset: 0, Size: 16}},
	}
	var pl vk.PipelineLayout
	if res := vk.CreatePipelineLayout(r.device, &plInfo, nil, &pl); res != vk.Success {
		return fmt.Errorf("vulkan: bloom pipeline layout: %s", vk.Error(res))
	}
	bl.pl = pl

	if bl.pipeBright, err = r.bloomPipeline(worldBrightFragSPV, bl.rpClear, bl.pl, false); err != nil {
		return err
	}
	if bl.pipeDown, err = r.bloomPipeline(worldBloomDownFragSPV, bl.rpClear, bl.pl, false); err != nil {
		return err
	}
	if bl.pipeUp, err = r.bloomPipeline(worldBloomUpFragSPV, bl.rpAdd, bl.pl, true); err != nil {
		return err
	}
	return nil
}

// bloomPipeline is litPipeline specialised for a bloom pass: fullscreen
// triangle, no vertex input, no depth. `additive` gives it ONE:ONE colour
// blend so an upsample pass accumulates onto the target instead of
// replacing it.
func (r *Renderer) bloomPipeline(fragSPV []byte, rp vk.RenderPass, layout vk.PipelineLayout, additive bool) (vk.Pipeline, error) {
	if !additive {
		return r.litPipeline(fragSPV, rp, layout)
	}
	vert, err := r.createShaderModule(blitVertSPV)
	if err != nil {
		return nil, fmt.Errorf("vulkan: bloom vert: %w", err)
	}
	defer vk.DestroyShaderModule(r.device, vert, nil)
	frag, err := r.createShaderModule(fragSPV)
	if err != nil {
		return nil, fmt.Errorf("vulkan: bloom frag: %w", err)
	}
	defer vk.DestroyShaderModule(r.device, frag, nil)

	stages := []vk.PipelineShaderStageCreateInfo{
		{SType: vk.StructureTypePipelineShaderStageCreateInfo, Stage: vk.ShaderStageVertexBit, Module: vert, PName: nz("main")},
		{SType: vk.StructureTypePipelineShaderStageCreateInfo, Stage: vk.ShaderStageFragmentBit, Module: frag, PName: nz("main")},
	}
	vertexInput := vk.PipelineVertexInputStateCreateInfo{SType: vk.StructureTypePipelineVertexInputStateCreateInfo}
	inputAssembly := vk.PipelineInputAssemblyStateCreateInfo{
		SType: vk.StructureTypePipelineInputAssemblyStateCreateInfo, Topology: vk.PrimitiveTopologyTriangleList,
	}
	viewportState := vk.PipelineViewportStateCreateInfo{
		SType: vk.StructureTypePipelineViewportStateCreateInfo, ViewportCount: 1, ScissorCount: 1,
	}
	rasterizer := vk.PipelineRasterizationStateCreateInfo{
		SType: vk.StructureTypePipelineRasterizationStateCreateInfo, PolygonMode: vk.PolygonModeFill,
		CullMode: vk.CullModeFlags(vk.CullModeNone), FrontFace: vk.FrontFaceClockwise, LineWidth: 1,
	}
	multisample := vk.PipelineMultisampleStateCreateInfo{
		SType: vk.StructureTypePipelineMultisampleStateCreateInfo, RasterizationSamples: vk.SampleCount1Bit,
	}
	cba := vk.PipelineColorBlendAttachmentState{
		BlendEnable:         vk.True,
		SrcColorBlendFactor: vk.BlendFactorOne, DstColorBlendFactor: vk.BlendFactorOne, ColorBlendOp: vk.BlendOpAdd,
		SrcAlphaBlendFactor: vk.BlendFactorOne, DstAlphaBlendFactor: vk.BlendFactorOne, AlphaBlendOp: vk.BlendOpAdd,
		ColorWriteMask: vk.ColorComponentFlags(vk.ColorComponentRBit) | vk.ColorComponentFlags(vk.ColorComponentGBit) |
			vk.ColorComponentFlags(vk.ColorComponentBBit) | vk.ColorComponentFlags(vk.ColorComponentABit),
	}
	colorBlend := vk.PipelineColorBlendStateCreateInfo{
		SType: vk.StructureTypePipelineColorBlendStateCreateInfo, AttachmentCount: 1,
		PAttachments: []vk.PipelineColorBlendAttachmentState{cba},
	}
	dynamic := vk.PipelineDynamicStateCreateInfo{
		SType: vk.StructureTypePipelineDynamicStateCreateInfo, DynamicStateCount: 2,
		PDynamicStates: []vk.DynamicState{vk.DynamicStateViewport, vk.DynamicStateScissor},
	}
	info := vk.GraphicsPipelineCreateInfo{
		SType:               vk.StructureTypeGraphicsPipelineCreateInfo,
		StageCount:          2,
		PStages:             stages,
		PVertexInputState:   &vertexInput,
		PInputAssemblyState: &inputAssembly,
		PViewportState:      &viewportState,
		PRasterizationState: &rasterizer,
		PMultisampleState:   &multisample,
		PColorBlendState:    &colorBlend,
		PDynamicState:       &dynamic,
		Layout:              layout,
		RenderPass:          rp,
		Subpass:             0,
	}
	out := make([]vk.Pipeline, 1)
	if res := vk.CreateGraphicsPipelines(r.device, nil, 1, []vk.GraphicsPipelineCreateInfo{info}, nil, out); res != vk.Success {
		return nil, fmt.Errorf("vulkan: bloom additive pipeline: %s", vk.Error(res))
	}
	return out[0], nil
}

// recordBloom builds the bloom pyramid for slot `slotIdx`, leaving the
// finished wide bloom in mip 0 (SHADER_READ_ONLY) for the present blit.
func (r *Renderer) recordBloom(cmd vk.CommandBuffer, slotIdx int) {
	bl := &r.wr.bloom
	if bl.pipeBright == nil {
		return
	}
	black := vk.NewClearValue([]float32{0, 0, 0, 1})

	run := func(rp vk.RenderPass, m *bloomMip, fb vk.Framebuffer, pipe vk.Pipeline, set vk.DescriptorSet, push [4]float32) {
		vk.CmdBeginRenderPass(cmd, &vk.RenderPassBeginInfo{
			SType: vk.StructureTypeRenderPassBeginInfo, RenderPass: rp, Framebuffer: fb,
			RenderArea:      vk.Rect2D{Extent: vk.Extent2D{Width: uint32(m.w), Height: uint32(m.h)}},
			ClearValueCount: 1, PClearValues: []vk.ClearValue{black},
		}, vk.SubpassContentsInline)
		vk.CmdBindPipeline(cmd, vk.PipelineBindPointGraphics, pipe)
		vk.CmdSetViewport(cmd, 0, 1, []vk.Viewport{fullViewport(m.w, m.h)})
		vk.CmdSetScissor(cmd, 0, 1, []vk.Rect2D{fullScissor(m.w, m.h)})
		vk.CmdBindDescriptorSets(cmd, vk.PipelineBindPointGraphics, bl.pl, 0, 1, []vk.DescriptorSet{set}, 0, nil)
		p := push
		vk.CmdPushConstants(cmd, bl.pl, vk.ShaderStageFlags(vk.ShaderStageFragmentBit), 0, 16, unsafe.Pointer(&p[0]))
		vk.CmdDraw(cmd, 3, 1, 0, 0)
		vk.CmdEndRenderPass(cmd)
	}
	inv := func(m *bloomMip) (float32, float32) { return 1 / float32(m.w), 1 / float32(m.h) }

	// Bright-pass + firefly-resistant downsample: scene -> mip 0.
	m0 := &bl.mip[0]
	ix, iy := inv(m0)
	run(bl.rpClear, m0, m0.fbClear, bl.pipeBright, bl.setScene[slotIdx], [4]float32{ix, iy, bl.threshold, 0})

	// Downsample the chain: mip i -> mip i+1 (tap offsets in source texels).
	for i := 0; i < bloomMips-1; i++ {
		src, dst := &bl.mip[i], &bl.mip[i+1]
		sx, sy := inv(src)
		run(bl.rpClear, dst, dst.fbClear, bl.pipeDown, src.setSelf, [4]float32{sx, sy, 0, 0})
	}

	// Upsample + accumulate: mip i+1 -> mip i, blended ONE:ONE.
	for i := bloomMips - 2; i >= 0; i-- {
		src, dst := &bl.mip[i+1], &bl.mip[i]
		sx, sy := inv(src)
		run(bl.rpAdd, dst, dst.fbAdd, bl.pipeUp, src.setSelf, [4]float32{sx, sy, 1.0, 0})
	}
}

func (r *Renderer) destroyBloom() {
	wr := r.wr
	if wr == nil {
		return
	}
	bl := &wr.bloom
	dev := r.device
	for _, p := range []vk.Pipeline{bl.pipeBright, bl.pipeDown, bl.pipeUp} {
		if p != nil {
			vk.DestroyPipeline(dev, p, nil)
		}
	}
	if bl.pl != nil {
		vk.DestroyPipelineLayout(dev, bl.pl, nil)
	}
	if bl.pool != nil {
		vk.DestroyDescriptorPool(dev, bl.pool, nil)
	}
	if bl.dsl != nil {
		vk.DestroyDescriptorSetLayout(dev, bl.dsl, nil)
	}
	for i := range bl.mip {
		if bl.mip[i].fbClear != nil {
			vk.DestroyFramebuffer(dev, bl.mip[i].fbClear, nil)
		}
		if bl.mip[i].fbAdd != nil {
			vk.DestroyFramebuffer(dev, bl.mip[i].fbAdd, nil)
		}
		freeLitImage(dev, bl.mip[i].img)
	}
	for _, rp := range []vk.RenderPass{bl.rpClear, bl.rpAdd} {
		if rp != nil {
			vk.DestroyRenderPass(dev, rp, nil)
		}
	}
	*bl = worldBloom{}
}
