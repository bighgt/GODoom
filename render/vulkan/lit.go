package vulkan

import (
	_ "embed"
	"fmt"
	"sync"
	"unsafe"

	vk "github.com/goki/vulkan"

	"twopointfive/render"
)

// This file is the "enhanced" (config lightingMode "enhanced") rendering
// path: instead of the CPU handing over a finished frame for a plain blit,
// it hands over a G-buffer (unlit albedo, packed normal + material, sector
// light, camera-space depth) plus the frame's dynamic lights, and the GPU
// does the lighting.
//
// Per frame, recordLitCommandBuffer runs four fullscreen passes:
//
//	A  light.frag      G-buffer + lights           -> hdrColor   (RGBA16F, render res)
//	B1 bright.frag      hdrColor  > threshold       -> bloomA     (RGBA16F, 1/2 res)
//	B2 blur.frag (H)    bloomA blurred horizontally -> bloomB
//	B3 blur.frag (V)    bloomB blurred vertically   -> bloomA
//	C  composite.frag   hdrColor + bloomA, tonemap,
//	                    then the 2D overlay on top  -> swapchain image (letterboxed)
//
// Pass C targets the same render pass / framebuffers as the vanilla blit
// (createRenderPass / createFramebuffers), so those are shared; everything
// below is extra. The offscreen targets are render-resolution and fixed, so
// a window resize only rebuilds the swapchain-sized resources, exactly as
// in vanilla mode.

//go:embed shaders/light.frag.spv
var litLightFragSPV []byte

//go:embed shaders/bright.frag.spv
var litBrightFragSPV []byte

//go:embed shaders/blur.frag.spv
var litBlurFragSPV []byte

//go:embed shaders/composite.frag.spv
var litCompositeFragSPV []byte

// litMaxLights mirrors render.MaxLights and the fixed lights[] array in
// light.frag — keep the three in lockstep.
const litMaxLights = render.MaxLights

// litSceneFloats is the Scene UBO size in float32s (std140):
// camPos_extra(4) + view(4) + screen(4) + tuning(4) + fog(4) +
// spotPosRadius(4) + spotDirCosOuter(4) + spotColorIntensity(4) +
// spotCosInner(4) + lights[litMaxLights] * 8.
const litSceneHead = 36
const litSceneFloats = litSceneHead + litMaxLights*8

// litImage is one GPU image plus the handles needed to free it.
type litImage struct {
	image  vk.Image
	memory vk.DeviceMemory
	view   vk.ImageView
}

// gbufSlot is one in-flight slot's full set of uploaded G-buffer images
// plus their shared current layout. Double-buffering these (one set per
// frame in flight) lets frame N+1's staging->image copy run without
// waiting on frame N's lighting pass still sampling them.
type gbufSlot struct {
	albedo, normal, lightParam, overlay, depth litImage
	layout                                     vk.ImageLayout // shared by the five above
}

type litResources struct {
	w, h   int // render resolution (upload + offscreen size)
	bw, bh int // bloom resolution (w/2, h/2, min 1)

	// Per-in-flight-slot host-visible staging: the five G-buffer planes
	// packed back to back (albedo, normal, lightParam, overlay as RGBA8,
	// then depth as R32F), plus the Scene UBO.
	staging       []vk.Buffer
	stagingMem    []vk.DeviceMemory
	stagingMapped []unsafe.Pointer
	stagingBytes  int

	ubo       []vk.Buffer
	uboMem    []vk.DeviceMemory
	uboMapped []unsafe.Pointer

	// Device-local G-buffer images the passes sample — one full set per
	// in-flight slot (see gbufSlot).
	gbuf [maxFramesInFlight]gbufSlot

	// Offscreen HDR + bloom targets (RGBA16F).
	hdr, bloomA, bloomB litImage

	// curExposure is this frame's config exposure (from render.Frame),
	// stashed by upload for the composite pass's push constants.
	curExposure float32

	// Overlay dirty-rect (see render.Frame.OverlayY/H). overlayPending[j] is
	// the union of every frame's overlay dirty span since slot j's image was
	// last written; upload grows all slots by this frame's span, then takes
	// and clears its own — so a slot reused every maxFramesInFlight frames
	// still catches every change. curOverlayY/H is the span chosen for the
	// current slot, read by recordLitCommandBuffer for the image copy.
	// overlayInit[j] forces slot j's first upload to the whole plane so its
	// untouched rows start defined.
	curOverlayY, curOverlayH int
	overlayPending           [maxFramesInFlight]struct{ y0, y1 int }
	overlayInit              [maxFramesInFlight]bool

	sampNearest vk.Sampler // G-buffer reads (point)
	sampLinear  vk.Sampler // hdr/bloom/overlay reads (bilinear, clamp)

	rpHDR  vk.RenderPass // shared by the light + bloom passes
	fbHDR  vk.Framebuffer
	fbBloA vk.Framebuffer
	fbBloB vk.Framebuffer

	dslLight, dslBloom, dslComposite vk.DescriptorSetLayout
	pool                             vk.DescriptorPool
	setLight                         []vk.DescriptorSet // per slot (UBO + gbuf images differ)
	setBright, setBlurH, setBlurV    vk.DescriptorSet
	setComposite                     [maxFramesInFlight]vk.DescriptorSet // per slot (overlay image differs)

	plLight, plBloom, plComposite vk.PipelineLayout
	pipeLight, pipeBright         vk.Pipeline
	pipeBlur, pipeComposite       vk.Pipeline
}

func (r *Renderer) createLitResources(w, h int) error {
	r.srcWidth, r.srcHeight = w, h // letterboxViewport reads these

	lr := &litResources{w: w, h: h} // gbuf[*].layout zero value == ImageLayoutUndefined
	lr.bw, lr.bh = w/2, h/2
	if lr.bw < 1 {
		lr.bw = 1
	}
	if lr.bh < 1 {
		lr.bh = 1
	}
	r.litRes = lr

	steps := []func() error{
		r.litCreateImages,
		r.litCreateStaging,
		r.litCreateSamplers,
		r.litCreateRenderPass,
		r.litCreateFramebuffers,
		r.litCreateDescriptorLayouts,
		r.litCreateDescriptors,
		r.litCreatePipelines,
	}
	for _, s := range steps {
		if err := s(); err != nil {
			return err
		}
	}
	return nil
}

// --- resource creation ----------------------------------------------------

func (r *Renderer) litMakeImage(w, h int, format vk.Format, usage vk.ImageUsageFlags) (litImage, error) {
	var li litImage
	info := vk.ImageCreateInfo{
		SType:         vk.StructureTypeImageCreateInfo,
		ImageType:     vk.ImageType2d,
		Format:        format,
		Extent:        vk.Extent3D{Width: uint32(w), Height: uint32(h), Depth: 1},
		MipLevels:     1,
		ArrayLayers:   1,
		Samples:       vk.SampleCount1Bit,
		Tiling:        vk.ImageTilingOptimal,
		Usage:         usage,
		SharingMode:   vk.SharingModeExclusive,
		InitialLayout: vk.ImageLayoutUndefined,
	}
	if res := vk.CreateImage(r.device, &info, nil, &li.image); res != vk.Success {
		return li, fmt.Errorf("vulkan: lit create image: %s", vk.Error(res))
	}
	var memReq vk.MemoryRequirements
	vk.GetImageMemoryRequirements(r.device, li.image, &memReq)
	memReq.Deref()
	mt, err := r.findMemoryType(memReq.MemoryTypeBits, vk.MemoryPropertyFlags(vk.MemoryPropertyDeviceLocalBit))
	if err != nil {
		return li, fmt.Errorf("vulkan: lit image memory: %w", err)
	}
	alloc := vk.MemoryAllocateInfo{SType: vk.StructureTypeMemoryAllocateInfo, AllocationSize: memReq.Size, MemoryTypeIndex: mt}
	if res := vk.AllocateMemory(r.device, &alloc, nil, &li.memory); res != vk.Success {
		return li, fmt.Errorf("vulkan: lit image alloc: %s", vk.Error(res))
	}
	if res := vk.BindImageMemory(r.device, li.image, li.memory, 0); res != vk.Success {
		return li, fmt.Errorf("vulkan: lit image bind: %s", vk.Error(res))
	}
	viewInfo := vk.ImageViewCreateInfo{
		SType:            vk.StructureTypeImageViewCreateInfo,
		Image:            li.image,
		ViewType:         vk.ImageViewType2d,
		Format:           format,
		Components:       vk.ComponentMapping{R: vk.ComponentSwizzleIdentity, G: vk.ComponentSwizzleIdentity, B: vk.ComponentSwizzleIdentity, A: vk.ComponentSwizzleIdentity},
		SubresourceRange: vk.ImageSubresourceRange{AspectMask: vk.ImageAspectFlags(vk.ImageAspectColorBit), LevelCount: 1, LayerCount: 1},
	}
	if res := vk.CreateImageView(r.device, &viewInfo, nil, &li.view); res != vk.Success {
		return li, fmt.Errorf("vulkan: lit image view: %s", vk.Error(res))
	}
	return li, nil
}

func (r *Renderer) litCreateImages() error {
	lr := r.litRes
	const rgba8 = vk.FormatR8g8b8a8Unorm
	const rgba16f = vk.FormatR16g16b16a16Sfloat
	sampledDst := vk.ImageUsageFlags(vk.ImageUsageTransferDstBit) | vk.ImageUsageFlags(vk.ImageUsageSampledBit)
	attachSampled := vk.ImageUsageFlags(vk.ImageUsageColorAttachmentBit) | vk.ImageUsageFlags(vk.ImageUsageSampledBit)

	var err error
	for s := range lr.gbuf {
		gs := &lr.gbuf[s]
		if gs.albedo, err = r.litMakeImage(lr.w, lr.h, rgba8, sampledDst); err != nil {
			return err
		}
		if gs.normal, err = r.litMakeImage(lr.w, lr.h, rgba8, sampledDst); err != nil {
			return err
		}
		if gs.lightParam, err = r.litMakeImage(lr.w, lr.h, rgba8, sampledDst); err != nil {
			return err
		}
		if gs.overlay, err = r.litMakeImage(lr.w, lr.h, rgba8, sampledDst); err != nil {
			return err
		}
		if gs.depth, err = r.litMakeImage(lr.w, lr.h, vk.FormatR32Sfloat, sampledDst); err != nil {
			return err
		}
	}
	if lr.hdr, err = r.litMakeImage(lr.w, lr.h, rgba16f, attachSampled); err != nil {
		return err
	}
	if lr.bloomA, err = r.litMakeImage(lr.bw, lr.bh, rgba16f, attachSampled); err != nil {
		return err
	}
	if lr.bloomB, err = r.litMakeImage(lr.bw, lr.bh, rgba16f, attachSampled); err != nil {
		return err
	}
	return nil
}

func (r *Renderer) litCreateStaging() error {
	lr := r.litRes
	// Five planes: 4x RGBA8 + 1x R32F, all w*h.
	lr.stagingBytes = lr.w * lr.h * (4 + 4 + 4 + 4 + 4)
	uboBytes := litSceneFloats * 4

	lr.staging = make([]vk.Buffer, maxFramesInFlight)
	lr.stagingMem = make([]vk.DeviceMemory, maxFramesInFlight)
	lr.stagingMapped = make([]unsafe.Pointer, maxFramesInFlight)
	lr.ubo = make([]vk.Buffer, maxFramesInFlight)
	lr.uboMem = make([]vk.DeviceMemory, maxFramesInFlight)
	lr.uboMapped = make([]unsafe.Pointer, maxFramesInFlight)

	mk := func(size int, usage vk.BufferUsageFlags) (vk.Buffer, vk.DeviceMemory, unsafe.Pointer, error) {
		info := vk.BufferCreateInfo{
			SType:       vk.StructureTypeBufferCreateInfo,
			Size:        vk.DeviceSize(size),
			Usage:       usage,
			SharingMode: vk.SharingModeExclusive,
		}
		var buf vk.Buffer
		if res := vk.CreateBuffer(r.device, &info, nil, &buf); res != vk.Success {
			return nil, nil, nil, fmt.Errorf("vulkan: lit buffer: %s", vk.Error(res))
		}
		var memReq vk.MemoryRequirements
		vk.GetBufferMemoryRequirements(r.device, buf, &memReq)
		memReq.Deref()
		mt, err := r.findMemoryType(memReq.MemoryTypeBits,
			vk.MemoryPropertyFlags(vk.MemoryPropertyHostVisibleBit)|vk.MemoryPropertyFlags(vk.MemoryPropertyHostCoherentBit))
		if err != nil {
			return nil, nil, nil, err
		}
		alloc := vk.MemoryAllocateInfo{SType: vk.StructureTypeMemoryAllocateInfo, AllocationSize: memReq.Size, MemoryTypeIndex: mt}
		var mem vk.DeviceMemory
		if res := vk.AllocateMemory(r.device, &alloc, nil, &mem); res != vk.Success {
			return nil, nil, nil, fmt.Errorf("vulkan: lit buffer alloc: %s", vk.Error(res))
		}
		if res := vk.BindBufferMemory(r.device, buf, mem, 0); res != vk.Success {
			return nil, nil, nil, fmt.Errorf("vulkan: lit buffer bind: %s", vk.Error(res))
		}
		var p unsafe.Pointer
		if res := vk.MapMemory(r.device, mem, 0, vk.DeviceSize(size), 0, &p); res != vk.Success {
			return nil, nil, nil, fmt.Errorf("vulkan: lit buffer map: %s", vk.Error(res))
		}
		return buf, mem, p, nil
	}

	for i := 0; i < maxFramesInFlight; i++ {
		var err error
		if lr.staging[i], lr.stagingMem[i], lr.stagingMapped[i], err = mk(lr.stagingBytes, vk.BufferUsageFlags(vk.BufferUsageTransferSrcBit)); err != nil {
			return err
		}
		if lr.ubo[i], lr.uboMem[i], lr.uboMapped[i], err = mk(uboBytes, vk.BufferUsageFlags(vk.BufferUsageUniformBufferBit)); err != nil {
			return err
		}
	}
	return nil
}

func (r *Renderer) litCreateSamplers() error {
	lr := r.litRes
	mk := func(filter vk.Filter) (vk.Sampler, error) {
		info := vk.SamplerCreateInfo{
			SType:        vk.StructureTypeSamplerCreateInfo,
			MagFilter:    filter,
			MinFilter:    filter,
			MipmapMode:   vk.SamplerMipmapModeNearest,
			AddressModeU: vk.SamplerAddressModeClampToEdge,
			AddressModeV: vk.SamplerAddressModeClampToEdge,
			AddressModeW: vk.SamplerAddressModeClampToEdge,
			MaxLod:       1,
		}
		var s vk.Sampler
		if res := vk.CreateSampler(r.device, &info, nil, &s); res != vk.Success {
			return nil, fmt.Errorf("vulkan: lit sampler: %s", vk.Error(res))
		}
		return s, nil
	}
	var err error
	if lr.sampNearest, err = mk(vk.FilterNearest); err != nil {
		return err
	}
	if lr.sampLinear, err = mk(vk.FilterLinear); err != nil {
		return err
	}
	return nil
}

func (r *Renderer) litCreateRenderPass() error {
	att := vk.AttachmentDescription{
		Format:         vk.FormatR16g16b16a16Sfloat,
		Samples:        vk.SampleCount1Bit,
		LoadOp:         vk.AttachmentLoadOpClear,
		StoreOp:        vk.AttachmentStoreOpStore,
		StencilLoadOp:  vk.AttachmentLoadOpDontCare,
		StencilStoreOp: vk.AttachmentStoreOpDontCare,
		InitialLayout:  vk.ImageLayoutUndefined,
		FinalLayout:    vk.ImageLayoutShaderReadOnlyOptimal,
	}
	ref := vk.AttachmentReference{Attachment: 0, Layout: vk.ImageLayoutColorAttachmentOptimal}
	sub := vk.SubpassDescription{
		PipelineBindPoint:    vk.PipelineBindPointGraphics,
		ColorAttachmentCount: 1,
		PColorAttachments:    []vk.AttachmentReference{ref},
	}
	deps := []vk.SubpassDependency{
		{ // a prior pass's fragment reads of this image must finish before we overwrite it
			SrcSubpass:    vk.SubpassExternal,
			DstSubpass:    0,
			SrcStageMask:  vk.PipelineStageFlags(vk.PipelineStageFragmentShaderBit),
			DstStageMask:  vk.PipelineStageFlags(vk.PipelineStageColorAttachmentOutputBit),
			SrcAccessMask: vk.AccessFlags(vk.AccessShaderReadBit),
			DstAccessMask: vk.AccessFlags(vk.AccessColorAttachmentWriteBit),
		},
		{ // our writes must be visible to the next pass's fragment reads
			SrcSubpass:    0,
			DstSubpass:    vk.SubpassExternal,
			SrcStageMask:  vk.PipelineStageFlags(vk.PipelineStageColorAttachmentOutputBit),
			DstStageMask:  vk.PipelineStageFlags(vk.PipelineStageFragmentShaderBit),
			SrcAccessMask: vk.AccessFlags(vk.AccessColorAttachmentWriteBit),
			DstAccessMask: vk.AccessFlags(vk.AccessShaderReadBit),
		},
	}
	info := vk.RenderPassCreateInfo{
		SType:           vk.StructureTypeRenderPassCreateInfo,
		AttachmentCount: 1,
		PAttachments:    []vk.AttachmentDescription{att},
		SubpassCount:    1,
		PSubpasses:      []vk.SubpassDescription{sub},
		DependencyCount: uint32(len(deps)),
		PDependencies:   deps,
	}
	var rp vk.RenderPass
	if res := vk.CreateRenderPass(r.device, &info, nil, &rp); res != vk.Success {
		return fmt.Errorf("vulkan: lit render pass: %s", vk.Error(res))
	}
	r.litRes.rpHDR = rp
	return nil
}

func (r *Renderer) litFramebuffer(view vk.ImageView, w, h int) (vk.Framebuffer, error) {
	info := vk.FramebufferCreateInfo{
		SType:           vk.StructureTypeFramebufferCreateInfo,
		RenderPass:      r.litRes.rpHDR,
		AttachmentCount: 1,
		PAttachments:    []vk.ImageView{view},
		Width:           uint32(w),
		Height:          uint32(h),
		Layers:          1,
	}
	var fb vk.Framebuffer
	if res := vk.CreateFramebuffer(r.device, &info, nil, &fb); res != vk.Success {
		return nil, fmt.Errorf("vulkan: lit framebuffer: %s", vk.Error(res))
	}
	return fb, nil
}

func (r *Renderer) litCreateFramebuffers() error {
	lr := r.litRes
	var err error
	if lr.fbHDR, err = r.litFramebuffer(lr.hdr.view, lr.w, lr.h); err != nil {
		return err
	}
	if lr.fbBloA, err = r.litFramebuffer(lr.bloomA.view, lr.bw, lr.bh); err != nil {
		return err
	}
	if lr.fbBloB, err = r.litFramebuffer(lr.bloomB.view, lr.bw, lr.bh); err != nil {
		return err
	}
	return nil
}

func samplerBinding(n uint32) vk.DescriptorSetLayoutBinding {
	return vk.DescriptorSetLayoutBinding{
		Binding:         n,
		DescriptorType:  vk.DescriptorTypeCombinedImageSampler,
		DescriptorCount: 1,
		StageFlags:      vk.ShaderStageFlags(vk.ShaderStageFragmentBit),
	}
}

func (r *Renderer) litCreateDescriptorLayouts() error {
	lr := r.litRes
	mk := func(bindings []vk.DescriptorSetLayoutBinding) (vk.DescriptorSetLayout, error) {
		info := vk.DescriptorSetLayoutCreateInfo{
			SType:        vk.StructureTypeDescriptorSetLayoutCreateInfo,
			BindingCount: uint32(len(bindings)),
			PBindings:    bindings,
		}
		var l vk.DescriptorSetLayout
		if res := vk.CreateDescriptorSetLayout(r.device, &info, nil, &l); res != vk.Success {
			return nil, fmt.Errorf("vulkan: lit dsl: %s", vk.Error(res))
		}
		return l, nil
	}

	uboBinding := vk.DescriptorSetLayoutBinding{
		Binding: 4, DescriptorType: vk.DescriptorTypeUniformBuffer, DescriptorCount: 1,
		StageFlags: vk.ShaderStageFlags(vk.ShaderStageFragmentBit),
	}
	var err error
	if lr.dslLight, err = mk([]vk.DescriptorSetLayoutBinding{
		samplerBinding(0), samplerBinding(1), samplerBinding(2), samplerBinding(3), uboBinding,
	}); err != nil {
		return err
	}
	if lr.dslBloom, err = mk([]vk.DescriptorSetLayoutBinding{samplerBinding(0)}); err != nil {
		return err
	}
	if lr.dslComposite, err = mk([]vk.DescriptorSetLayoutBinding{
		samplerBinding(0), samplerBinding(1), samplerBinding(2),
	}); err != nil {
		return err
	}
	return nil
}

func (r *Renderer) litCreateDescriptors() error {
	lr := r.litRes
	poolInfo := vk.DescriptorPoolCreateInfo{
		SType: vk.StructureTypeDescriptorPoolCreateInfo,
		// Sets: setLight (mff) + bright + blurH + blurV + setComposite (mff).
		// Samplers: 4/light-set + 1 each for bright/blurH/blurV + 3/composite-set.
		MaxSets:       uint32(2*maxFramesInFlight + 4),
		PoolSizeCount: 2,
		PPoolSizes: []vk.DescriptorPoolSize{
			{Type: vk.DescriptorTypeCombinedImageSampler, DescriptorCount: uint32(8*maxFramesInFlight + 4)},
			{Type: vk.DescriptorTypeUniformBuffer, DescriptorCount: uint32(maxFramesInFlight)},
		},
	}
	// NB: write into a local, not &lr.pool — passing a pointer to a field
	// inside the heap-allocated litResources makes Go's cgo checker scan the
	// whole struct (which has Go slice fields) and panic. Every other lit*
	// creator here follows the same local-then-assign pattern for this
	// reason, matching descriptors.go / pipeline.go.
	var pool vk.DescriptorPool
	if res := vk.CreateDescriptorPool(r.device, &poolInfo, nil, &pool); res != vk.Success {
		return fmt.Errorf("vulkan: lit descriptor pool: %s", vk.Error(res))
	}
	lr.pool = pool

	alloc := func(layout vk.DescriptorSetLayout) (vk.DescriptorSet, error) {
		info := vk.DescriptorSetAllocateInfo{
			SType:              vk.StructureTypeDescriptorSetAllocateInfo,
			DescriptorPool:     lr.pool,
			DescriptorSetCount: 1,
			PSetLayouts:        []vk.DescriptorSetLayout{layout},
		}
		var s vk.DescriptorSet
		if res := vk.AllocateDescriptorSets(r.device, &info, &s); res != vk.Success {
			return nil, fmt.Errorf("vulkan: lit descriptor set: %s", vk.Error(res))
		}
		return s, nil
	}

	img := func(set vk.DescriptorSet, binding uint32, sampler vk.Sampler, view vk.ImageView) vk.WriteDescriptorSet {
		return vk.WriteDescriptorSet{
			SType:           vk.StructureTypeWriteDescriptorSet,
			DstSet:          set,
			DstBinding:      binding,
			DescriptorCount: 1,
			DescriptorType:  vk.DescriptorTypeCombinedImageSampler,
			PImageInfo: []vk.DescriptorImageInfo{{
				Sampler: sampler, ImageView: view, ImageLayout: vk.ImageLayoutShaderReadOnlyOptimal,
			}},
		}
	}

	var writes []vk.WriteDescriptorSet

	lr.setLight = make([]vk.DescriptorSet, maxFramesInFlight)
	for i := 0; i < maxFramesInFlight; i++ {
		s, err := alloc(lr.dslLight)
		if err != nil {
			return err
		}
		lr.setLight[i] = s
		gs := &lr.gbuf[i]
		writes = append(writes,
			img(s, 0, lr.sampNearest, gs.albedo.view),
			img(s, 1, lr.sampNearest, gs.normal.view),
			img(s, 2, lr.sampNearest, gs.lightParam.view),
			img(s, 3, lr.sampNearest, gs.depth.view),
			vk.WriteDescriptorSet{
				SType:           vk.StructureTypeWriteDescriptorSet,
				DstSet:          s,
				DstBinding:      4,
				DescriptorCount: 1,
				DescriptorType:  vk.DescriptorTypeUniformBuffer,
				PBufferInfo:     []vk.DescriptorBufferInfo{{Buffer: lr.ubo[i], Offset: 0, Range: vk.DeviceSize(litSceneFloats * 4)}},
			},
		)
	}

	var err error
	if lr.setBright, err = alloc(lr.dslBloom); err != nil {
		return err
	}
	if lr.setBlurH, err = alloc(lr.dslBloom); err != nil {
		return err
	}
	if lr.setBlurV, err = alloc(lr.dslBloom); err != nil {
		return err
	}
	writes = append(writes,
		img(lr.setBright, 0, lr.sampLinear, lr.hdr.view),
		img(lr.setBlurH, 0, lr.sampLinear, lr.bloomA.view),
		img(lr.setBlurV, 0, lr.sampLinear, lr.bloomB.view),
	)
	// One composite set per in-flight slot: hdr/bloom are shared, but the
	// overlay image is now double-buffered like the rest of the G-buffer.
	for i := range lr.setComposite {
		s, e := alloc(lr.dslComposite)
		if e != nil {
			return e
		}
		lr.setComposite[i] = s
		writes = append(writes,
			img(s, 0, lr.sampLinear, lr.hdr.view),
			img(s, 1, lr.sampLinear, lr.bloomA.view),
			img(s, 2, lr.sampLinear, lr.gbuf[i].overlay.view),
		)
	}

	vk.UpdateDescriptorSets(r.device, uint32(len(writes)), writes, 0, nil)
	return nil
}

func (r *Renderer) litPipelineLayout(setLayout vk.DescriptorSetLayout, pushSize uint32) (vk.PipelineLayout, error) {
	info := vk.PipelineLayoutCreateInfo{
		SType:          vk.StructureTypePipelineLayoutCreateInfo,
		SetLayoutCount: 1,
		PSetLayouts:    []vk.DescriptorSetLayout{setLayout},
	}
	if pushSize > 0 {
		info.PushConstantRangeCount = 1
		info.PPushConstantRanges = []vk.PushConstantRange{{
			StageFlags: vk.ShaderStageFlags(vk.ShaderStageFragmentBit), Offset: 0, Size: pushSize,
		}}
	}
	var l vk.PipelineLayout
	if res := vk.CreatePipelineLayout(r.device, &info, nil, &l); res != vk.Success {
		return nil, fmt.Errorf("vulkan: lit pipeline layout: %s", vk.Error(res))
	}
	return l, nil
}

func (r *Renderer) litPipeline(fragSPV []byte, renderPass vk.RenderPass, layout vk.PipelineLayout) (vk.Pipeline, error) {
	vert, err := r.createShaderModule(blitVertSPV)
	if err != nil {
		return nil, fmt.Errorf("vulkan: lit vert module: %w", err)
	}
	defer vk.DestroyShaderModule(r.device, vert, nil)
	frag, err := r.createShaderModule(fragSPV)
	if err != nil {
		return nil, fmt.Errorf("vulkan: lit frag module: %w", err)
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
		RenderPass:          renderPass,
		Subpass:             0,
	}
	out := make([]vk.Pipeline, 1)
	if res := vk.CreateGraphicsPipelines(r.device, nil, 1, []vk.GraphicsPipelineCreateInfo{info}, nil, out); res != vk.Success {
		return nil, fmt.Errorf("vulkan: lit pipeline: %s", vk.Error(res))
	}
	return out[0], nil
}

func (r *Renderer) litCreatePipelines() error {
	lr := r.litRes
	const vec4Bytes = 16
	var err error
	if lr.plLight, err = r.litPipelineLayout(lr.dslLight, 0); err != nil {
		return err
	}
	if lr.plBloom, err = r.litPipelineLayout(lr.dslBloom, vec4Bytes); err != nil {
		return err
	}
	if lr.plComposite, err = r.litPipelineLayout(lr.dslComposite, vec4Bytes); err != nil {
		return err
	}
	if lr.pipeLight, err = r.litPipeline(litLightFragSPV, lr.rpHDR, lr.plLight); err != nil {
		return err
	}
	if lr.pipeBright, err = r.litPipeline(litBrightFragSPV, lr.rpHDR, lr.plBloom); err != nil {
		return err
	}
	if lr.pipeBlur, err = r.litPipeline(litBlurFragSPV, lr.rpHDR, lr.plBloom); err != nil {
		return err
	}
	if lr.pipeComposite, err = r.litPipeline(litCompositeFragSPV, r.renderPass, lr.plComposite); err != nil {
		return err
	}
	return nil
}

// --- per-frame ----------------------------------------------------------

// litTuning holds the bloom look knobs the passes take as push constants.
// exposure is the fallback when a frame doesn't carry one (render.Frame.
// Exposure, config exposure, overrides it per frame).
var litTuning = struct {
	bloomThreshold float32
	bloomStrength  float32
	exposure       float32
}{bloomThreshold: 1.0, bloomStrength: 0.65, exposure: 1.0}

// DrawFrameLit runs the enhanced pipeline for one frame. Mirrors
// DrawFrame's acquire/submit/present bookkeeping.
func (r *Renderer) DrawFrameLit(f *render.Frame) error {
	if !r.lit {
		return fmt.Errorf("vulkan: DrawFrameLit called on a vanilla renderer (use DrawFrame)")
	}
	if r.hw {
		return fmt.Errorf("vulkan: DrawFrameLit called on a hardware-geometry renderer (use DrawWorld)")
	}
	lr := r.litRes
	if f.W != lr.w || f.H != lr.h {
		return fmt.Errorf("vulkan: lit frame is %dx%d, renderer expects %dx%d", f.W, f.H, lr.w, lr.h)
	}
	if w, h := r.win.FramebufferSize(); w == 0 || h == 0 {
		return nil
	}
	if r.needRecreate {
		if err := r.recreateSwapchain(); err != nil {
			if err == errSwapchainZeroExtent {
				return nil
			}
			return err
		}
		r.needRecreate = false
	}

	frame := r.currentFrame
	vk.WaitForFences(r.device, 1, []vk.Fence{r.inFlight[frame]}, vk.True, vk.MaxUint64)

	if err := lr.upload(f, frame); err != nil {
		return err
	}

	var imageIndex uint32
	acq := vk.AcquireNextImage(r.device, r.swapchain, vk.MaxUint64, r.imageAvailable[frame], nil, &imageIndex)
	if acq == vk.ErrorOutOfDate {
		r.needRecreate = true
		return nil
	} else if acq != vk.Success && acq != vk.Suboptimal {
		return fmt.Errorf("vulkan: acquire next image: %s", vk.Error(acq))
	}

	if fence := r.imagesInFlight[imageIndex]; fence != nil {
		vk.WaitForFences(r.device, 1, []vk.Fence{fence}, vk.True, vk.MaxUint64)
	}
	r.imagesInFlight[imageIndex] = r.inFlight[frame]
	vk.ResetFences(r.device, 1, []vk.Fence{r.inFlight[frame]})

	cmd := r.commandBuffers[imageIndex]
	vk.ResetCommandBuffer(cmd, 0)
	if err := r.recordLitCommandBuffer(cmd, imageIndex, frame); err != nil {
		return err
	}

	signal := []vk.Semaphore{r.renderFinished[imageIndex]}
	submit := vk.SubmitInfo{
		SType:                vk.StructureTypeSubmitInfo,
		WaitSemaphoreCount:   1,
		PWaitSemaphores:      []vk.Semaphore{r.imageAvailable[frame]},
		PWaitDstStageMask:    []vk.PipelineStageFlags{vk.PipelineStageFlags(vk.PipelineStageColorAttachmentOutputBit)},
		CommandBufferCount:   1,
		PCommandBuffers:      []vk.CommandBuffer{cmd},
		SignalSemaphoreCount: 1,
		PSignalSemaphores:    signal,
	}
	if res := vk.QueueSubmit(r.graphicsQueue, 1, []vk.SubmitInfo{submit}, r.inFlight[frame]); res != vk.Success {
		return fmt.Errorf("vulkan: queue submit: %s", vk.Error(res))
	}

	present := vk.PresentInfo{
		SType:              vk.StructureTypePresentInfo,
		WaitSemaphoreCount: 1,
		PWaitSemaphores:    signal,
		SwapchainCount:     1,
		PSwapchains:        []vk.Swapchain{r.swapchain},
		PImageIndices:      []uint32{imageIndex},
	}
	pr := vk.QueuePresent(r.presentQueue, &present)
	if pr == vk.ErrorOutOfDate || pr == vk.Suboptimal {
		r.needRecreate = true
	} else if pr != vk.Success {
		return fmt.Errorf("vulkan: queue present: %s", vk.Error(pr))
	}

	r.currentFrame = (frame + 1) % maxFramesInFlight
	return nil
}

// upload packs the G-buffer planes into staging slot `slot` and fills that
// slot's Scene UBO.
func (lr *litResources) upload(f *render.Frame, slot int) error {
	px := lr.w * lr.h * 4
	want := px*4 + lr.w*lr.h*4
	if len(f.Albedo) != px || len(f.Normal) != px || len(f.LightParam) != px || len(f.Overlay) != px || len(f.Depth) != lr.w*lr.h {
		return fmt.Errorf("vulkan: lit frame plane sizes wrong (albedo %d normal %d light %d overlay %d depth %d, want %d/%d)",
			len(f.Albedo), len(f.Normal), len(f.LightParam), len(f.Overlay), len(f.Depth), px, lr.w*lr.h)
	}
	if want != lr.stagingBytes {
		return fmt.Errorf("vulkan: lit staging size mismatch (%d vs %d)", want, lr.stagingBytes)
	}
	dst := unsafe.Slice((*byte)(lr.stagingMapped[slot]), lr.stagingBytes)
	depthBytes := unsafe.Slice((*byte)(unsafe.Pointer(&f.Depth[0])), len(f.Depth)*4)

	// Overlay: grow every slot's pending dirty span by this frame's touched
	// rows, then take (and reset) this slot's — everything changed since this
	// slot's image was last written. First use of a slot does the whole plane.
	if f.OverlayH > 0 {
		iy0, iy1 := f.OverlayY, f.OverlayY+f.OverlayH
		for j := range lr.overlayPending {
			p := &lr.overlayPending[j]
			switch {
			case p.y1 <= p.y0:
				p.y0, p.y1 = iy0, iy1
			default:
				if iy0 < p.y0 {
					p.y0 = iy0
				}
				if iy1 > p.y1 {
					p.y1 = iy1
				}
			}
		}
	}
	oy, oh := lr.overlayPending[slot].y0, lr.overlayPending[slot].y1-lr.overlayPending[slot].y0
	lr.overlayPending[slot] = struct{ y0, y1 int }{}
	if !lr.overlayInit[slot] {
		oy, oh = 0, lr.h
		lr.overlayInit[slot] = true
	}
	if oy < 0 {
		oy = 0
	}
	if oh < 0 {
		oh = 0
	}
	if oy > lr.h {
		oy = lr.h
	}
	if oy+oh > lr.h {
		oh = lr.h - oy
	}
	lr.curOverlayY, lr.curOverlayH = oy, oh

	// The four full planes are ~15 MB each at 1440p — a serial memcpy here is
	// several ms on the render-loop goroutine, fully in front of the GPU
	// submit. They're independent copies into disjoint staging ranges, so
	// fan them out; the (usually much smaller) overlay slice rides along on
	// this goroutine.
	var wg sync.WaitGroup
	cp := func(d, s []byte) { copy(d, s); wg.Done() }
	wg.Add(4)
	go cp(dst[0:px], f.Albedo)
	go cp(dst[px:2*px], f.Normal)
	go cp(dst[2*px:3*px], f.LightParam)
	go cp(dst[4*px:], depthBytes)
	if oh > 0 {
		rb := lr.w * 4
		a, b := oy*rb, (oy+oh)*rb
		copy(dst[3*px+a:3*px+b], f.Overlay[a:b])
	}
	wg.Wait()

	scene := unsafe.Slice((*float32)(lr.uboMapped[slot]), litSceneFloats)
	scene[0], scene[1], scene[2], scene[3] = f.CamX, f.CamY, f.CamZ, f.ExtraLight
	scene[4], scene[5], scene[6], scene[7] = f.SinA, f.CosA, f.Focal, f.AnimTime // view.w = water clock
	n := len(f.Lights)
	if n > litMaxLights {
		n = litMaxLights
	}
	scene[8], scene[9], scene[10], scene[11] = float32(lr.w), float32(lr.h), f.HorizonY, float32(n)

	lightScale, exposure := f.LightScale, f.Exposure
	if lightScale <= 0 {
		lightScale = 1
	}
	if exposure <= 0 {
		exposure = 1
	}
	shadow := f.ShadowStrength
	if shadow < 0 {
		shadow = 0
	} else if shadow > 1 {
		shadow = 1
	}
	// shadowN: how many leading lights get the screen-space march. Clamped
	// to the actual light count; forced to 0 when shadowing is disabled so
	// the shader can skip the whole branch.
	shadowN := f.ShadowLights
	if shadowN > n {
		shadowN = n
	}
	if shadowN < 0 || shadow == 0 {
		shadowN = 0
	}
	scene[12], scene[13], scene[14], scene[15] = lightScale, exposure, shadow, float32(shadowN)
	scene[16], scene[17], scene[18], scene[19] = f.FogR, f.FogG, f.FogB, f.FogDensity
	lr.curExposure = exposure

	// Flashlight spot (see render.Frame.Spot* doc). SpotIntensity <= 0 is
	// "off" — light.frag gates the whole term on colorIntensity.a > 0, so
	// the other three fields don't need to be zeroed too.
	spotRad := f.SpotRadius
	if spotRad < 0.001 {
		spotRad = 0.001
	}
	scene[20], scene[21], scene[22], scene[23] = f.SpotX, f.SpotY, f.SpotZ, 1.0/(spotRad*spotRad)
	scene[24], scene[25], scene[26], scene[27] = f.SpotDX, f.SpotDY, f.SpotDZ, f.SpotCosOuter
	scene[28], scene[29], scene[30], scene[31] = f.SpotR, f.SpotG, f.SpotB, f.SpotIntensity
	scene[32], scene[33], scene[34], scene[35] = f.SpotCosInner, 0, 0, 0

	for i := 0; i < n; i++ {
		b := litSceneHead + i*8
		l := f.Lights[i]
		// Pass 1/radius^2: light.frag's cull and falloff are both in terms
		// of squared distance, so it never needs the radius itself or a sqrt.
		rad := l.Radius
		if rad < 0.001 {
			rad = 0.001
		}
		scene[b], scene[b+1], scene[b+2], scene[b+3] = l.X, l.Y, l.Z, 1.0/(rad*rad)
		scene[b+4], scene[b+5], scene[b+6], scene[b+7] = l.R, l.G, l.B, l.Intensity
	}
	return nil
}

func fullViewport(w, h int) vk.Viewport {
	return vk.Viewport{Width: float32(w), Height: float32(h), MaxDepth: 1}
}

func fullScissor(w, h int) vk.Rect2D {
	return vk.Rect2D{Extent: vk.Extent2D{Width: uint32(w), Height: uint32(h)}}
}

func (r *Renderer) recordLitCommandBuffer(cmd vk.CommandBuffer, imageIndex uint32, slot int) error {
	lr := r.litRes
	if res := vk.BeginCommandBuffer(cmd, &vk.CommandBufferBeginInfo{SType: vk.StructureTypeCommandBufferBeginInfo}); res != vk.Success {
		return fmt.Errorf("vulkan: begin command buffer: %s", vk.Error(res))
	}

	sub := vk.ImageSubresourceRange{AspectMask: vk.ImageAspectFlags(vk.ImageAspectColorBit), LevelCount: 1, LayerCount: 1}
	gs := &lr.gbuf[slot]
	gbuf := []vk.Image{gs.albedo.image, gs.normal.image, gs.lightParam.image, gs.overlay.image, gs.depth.image}

	// (1) upload: transition this slot's five G-buffer images to transfer-dst,
	// copy this slot's staging into them, transition back to shader-read. The
	// prior producer of THIS slot's images is the same slot's lighting pass
	// two frames ago (already long finished — maxFramesInFlight is 2), so the
	// source scope stays FRAGMENT_SHADER for correctness but no longer
	// serialises against the immediately preceding frame. The very first use
	// of a slot has no prior producer (layout still UNDEFINED).
	srcStage := vk.PipelineStageFlags(vk.PipelineStageFragmentShaderBit)
	srcAccess := vk.AccessFlags(vk.AccessShaderReadBit)
	if gs.layout == vk.ImageLayoutUndefined {
		srcStage = vk.PipelineStageFlags(vk.PipelineStageTopOfPipeBit)
		srcAccess = 0
	}
	toDst := make([]vk.ImageMemoryBarrier, len(gbuf))
	for i, im := range gbuf {
		toDst[i] = vk.ImageMemoryBarrier{
			SType: vk.StructureTypeImageMemoryBarrier, OldLayout: gs.layout, NewLayout: vk.ImageLayoutTransferDstOptimal,
			SrcQueueFamilyIndex: vk.QueueFamilyIgnored, DstQueueFamilyIndex: vk.QueueFamilyIgnored,
			Image: im, SubresourceRange: sub,
			SrcAccessMask: srcAccess, DstAccessMask: vk.AccessFlags(vk.AccessTransferWriteBit),
		}
	}
	vk.CmdPipelineBarrier(cmd,
		srcStage, vk.PipelineStageFlags(vk.PipelineStageTransferBit),
		0, 0, nil, 0, nil, uint32(len(toDst)), toDst)

	px := uint32(lr.w * lr.h * 4)
	offsets := []vk.DeviceSize{0, vk.DeviceSize(px), vk.DeviceSize(2 * px), vk.DeviceSize(3 * px), vk.DeviceSize(4 * px)}
	rowBytes := uint32(lr.w * 4)
	for i, im := range gbuf {
		// Index 3 is the overlay: upload only the dirty row span (upload()
		// already limited the staging copy to it). A zero span means the
		// HUD is unchanged since this slot's last frame — skip it entirely.
		y, h := 0, lr.h
		if i == 3 {
			if lr.curOverlayH == 0 {
				continue
			}
			y, h = lr.curOverlayY, lr.curOverlayH
		}
		region := vk.BufferImageCopy{
			BufferOffset:     offsets[i] + vk.DeviceSize(uint32(y)*rowBytes),
			ImageSubresource: vk.ImageSubresourceLayers{AspectMask: vk.ImageAspectFlags(vk.ImageAspectColorBit), LayerCount: 1},
			ImageOffset:      vk.Offset3D{Y: int32(y)},
			ImageExtent:      vk.Extent3D{Width: uint32(lr.w), Height: uint32(h), Depth: 1},
		}
		vk.CmdCopyBufferToImage(cmd, lr.staging[slot], im, vk.ImageLayoutTransferDstOptimal, 1, []vk.BufferImageCopy{region})
	}

	toRead := make([]vk.ImageMemoryBarrier, len(gbuf))
	for i, im := range gbuf {
		toRead[i] = vk.ImageMemoryBarrier{
			SType: vk.StructureTypeImageMemoryBarrier, OldLayout: vk.ImageLayoutTransferDstOptimal, NewLayout: vk.ImageLayoutShaderReadOnlyOptimal,
			SrcQueueFamilyIndex: vk.QueueFamilyIgnored, DstQueueFamilyIndex: vk.QueueFamilyIgnored,
			Image: im, SubresourceRange: sub,
			SrcAccessMask: vk.AccessFlags(vk.AccessTransferWriteBit), DstAccessMask: vk.AccessFlags(vk.AccessShaderReadBit),
		}
	}
	vk.CmdPipelineBarrier(cmd,
		vk.PipelineStageFlags(vk.PipelineStageTransferBit), vk.PipelineStageFlags(vk.PipelineStageFragmentShaderBit),
		0, 0, nil, 0, nil, uint32(len(toRead)), toRead)
	gs.layout = vk.ImageLayoutShaderReadOnlyOptimal

	black := vk.NewClearValue([]float32{0, 0, 0, 1})

	pass := func(rp vk.RenderPass, fb vk.Framebuffer, w, h int, pipe vk.Pipeline, layout vk.PipelineLayout, set vk.DescriptorSet, push *[4]float32) {
		vk.CmdBeginRenderPass(cmd, &vk.RenderPassBeginInfo{
			SType: vk.StructureTypeRenderPassBeginInfo, RenderPass: rp, Framebuffer: fb,
			RenderArea:      vk.Rect2D{Extent: vk.Extent2D{Width: uint32(w), Height: uint32(h)}},
			ClearValueCount: 1, PClearValues: []vk.ClearValue{black},
		}, vk.SubpassContentsInline)
		vk.CmdBindPipeline(cmd, vk.PipelineBindPointGraphics, pipe)
		vk.CmdSetViewport(cmd, 0, 1, []vk.Viewport{fullViewport(w, h)})
		vk.CmdSetScissor(cmd, 0, 1, []vk.Rect2D{fullScissor(w, h)})
		vk.CmdBindDescriptorSets(cmd, vk.PipelineBindPointGraphics, layout, 0, 1, []vk.DescriptorSet{set}, 0, nil)
		if push != nil {
			vk.CmdPushConstants(cmd, layout, vk.ShaderStageFlags(vk.ShaderStageFragmentBit), 0, 16, unsafe.Pointer(push))
		}
		vk.CmdDraw(cmd, 3, 1, 0, 0)
		vk.CmdEndRenderPass(cmd)
	}

	// (A) lighting -> hdr
	pass(lr.rpHDR, lr.fbHDR, lr.w, lr.h, lr.pipeLight, lr.plLight, lr.setLight[slot], nil)

	// (B) bloom: bright-pass then separable blur, all at 1/2 res.
	invBW, invBH := 1/float32(lr.bw), 1/float32(lr.bh)
	brightPush := [4]float32{invBW, invBH, litTuning.bloomThreshold, 0}
	pass(lr.rpHDR, lr.fbBloA, lr.bw, lr.bh, lr.pipeBright, lr.plBloom, lr.setBright, &brightPush)
	blurHPush := [4]float32{invBW, invBH, invBW, 0}
	pass(lr.rpHDR, lr.fbBloB, lr.bw, lr.bh, lr.pipeBlur, lr.plBloom, lr.setBlurH, &blurHPush)
	blurVPush := [4]float32{invBW, invBH, 0, invBH}
	pass(lr.rpHDR, lr.fbBloA, lr.bw, lr.bh, lr.pipeBlur, lr.plBloom, lr.setBlurV, &blurVPush)

	// (C) composite -> swapchain image, letterboxed like the vanilla blit.
	vp := r.letterboxViewport()
	vk.CmdBeginRenderPass(cmd, &vk.RenderPassBeginInfo{
		SType: vk.StructureTypeRenderPassBeginInfo, RenderPass: r.renderPass, Framebuffer: r.framebuffers[imageIndex],
		RenderArea:      vk.Rect2D{Extent: r.swapchainExtent},
		ClearValueCount: 1, PClearValues: []vk.ClearValue{black},
	}, vk.SubpassContentsInline)
	vk.CmdBindPipeline(cmd, vk.PipelineBindPointGraphics, lr.pipeComposite)
	vk.CmdSetViewport(cmd, 0, 1, []vk.Viewport{vp})
	vk.CmdSetScissor(cmd, 0, 1, []vk.Rect2D{{Extent: r.swapchainExtent}})
	vk.CmdBindDescriptorSets(cmd, vk.PipelineBindPointGraphics, lr.plComposite, 0, 1, []vk.DescriptorSet{lr.setComposite[slot]}, 0, nil)
	expo := lr.curExposure
	if expo <= 0 {
		expo = litTuning.exposure
	}
	compPush := [4]float32{expo, litTuning.bloomStrength, 0, 0}
	vk.CmdPushConstants(cmd, lr.plComposite, vk.ShaderStageFlags(vk.ShaderStageFragmentBit), 0, 16, unsafe.Pointer(&compPush))
	vk.CmdDraw(cmd, 3, 1, 0, 0)
	vk.CmdEndRenderPass(cmd)

	if res := vk.EndCommandBuffer(cmd); res != vk.Success {
		return fmt.Errorf("vulkan: end command buffer: %s", vk.Error(res))
	}
	return nil
}

// --- teardown ---------------------------------------------------------

func (r *Renderer) destroyLitResources() {
	lr := r.litRes
	if lr == nil {
		return
	}
	dev := r.device
	for _, p := range []vk.Pipeline{lr.pipeLight, lr.pipeBright, lr.pipeBlur, lr.pipeComposite} {
		if p != nil {
			vk.DestroyPipeline(dev, p, nil)
		}
	}
	for _, l := range []vk.PipelineLayout{lr.plLight, lr.plBloom, lr.plComposite} {
		if l != nil {
			vk.DestroyPipelineLayout(dev, l, nil)
		}
	}
	if lr.pool != nil {
		vk.DestroyDescriptorPool(dev, lr.pool, nil)
	}
	for _, l := range []vk.DescriptorSetLayout{lr.dslLight, lr.dslBloom, lr.dslComposite} {
		if l != nil {
			vk.DestroyDescriptorSetLayout(dev, l, nil)
		}
	}
	for _, fb := range []vk.Framebuffer{lr.fbHDR, lr.fbBloA, lr.fbBloB} {
		if fb != nil {
			vk.DestroyFramebuffer(dev, fb, nil)
		}
	}
	if lr.rpHDR != nil {
		vk.DestroyRenderPass(dev, lr.rpHDR, nil)
	}
	for _, s := range []vk.Sampler{lr.sampNearest, lr.sampLinear} {
		if s != nil {
			vk.DestroySampler(dev, s, nil)
		}
	}
	imgs := []litImage{lr.hdr, lr.bloomA, lr.bloomB}
	for s := range lr.gbuf {
		gs := &lr.gbuf[s]
		imgs = append(imgs, gs.albedo, gs.normal, gs.lightParam, gs.overlay, gs.depth)
	}
	for _, li := range imgs {
		if li.view != nil {
			vk.DestroyImageView(dev, li.view, nil)
		}
		if li.image != nil {
			vk.DestroyImage(dev, li.image, nil)
		}
		if li.memory != nil {
			vk.FreeMemory(dev, li.memory, nil)
		}
	}
	for i := range lr.staging {
		if lr.stagingMapped != nil && lr.stagingMapped[i] != nil {
			vk.UnmapMemory(dev, lr.stagingMem[i])
		}
		if lr.staging[i] != nil {
			vk.DestroyBuffer(dev, lr.staging[i], nil)
		}
		if lr.stagingMem[i] != nil {
			vk.FreeMemory(dev, lr.stagingMem[i], nil)
		}
	}
	for i := range lr.ubo {
		if lr.uboMapped != nil && lr.uboMapped[i] != nil {
			vk.UnmapMemory(dev, lr.uboMem[i])
		}
		if lr.ubo[i] != nil {
			vk.DestroyBuffer(dev, lr.ubo[i], nil)
		}
		if lr.uboMem[i] != nil {
			vk.FreeMemory(dev, lr.uboMem[i], nil)
		}
	}
	r.litRes = nil
}
