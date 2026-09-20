package vulkan

import (
	"fmt"
	"unsafe"

	vk "github.com/goki/vulkan"

	"twopointfive/render/worldgeo"
)

// This file is the one-time resource build for the hardware-geometry path
// (see world.go): the offscreen scene + depth targets, the per-slot host
// buffers, the descriptor layouts / sets, and the world + present-blit
// pipelines. Per-texture GPU images live in worldtex.go.

// wMakeImage allocates a device-local image + view for an arbitrary format /
// usage / aspect / mip count. Mirrors litMakeImage but isn't limited to
// single-mip colour.
func (r *Renderer) wMakeImage(w, h int, format vk.Format, usage vk.ImageUsageFlags, aspect vk.ImageAspectFlags, mips uint32) (litImage, error) {
	var li litImage
	if mips == 0 {
		mips = 1
	}
	info := vk.ImageCreateInfo{
		SType:         vk.StructureTypeImageCreateInfo,
		ImageType:     vk.ImageType2d,
		Format:        format,
		Extent:        vk.Extent3D{Width: uint32(w), Height: uint32(h), Depth: 1},
		MipLevels:     mips,
		ArrayLayers:   1,
		Samples:       vk.SampleCount1Bit,
		Tiling:        vk.ImageTilingOptimal,
		Usage:         usage,
		SharingMode:   vk.SharingModeExclusive,
		InitialLayout: vk.ImageLayoutUndefined,
	}
	var image vk.Image
	if res := vk.CreateImage(r.device, &info, nil, &image); res != vk.Success {
		return li, fmt.Errorf("vulkan: world image: %s", vk.Error(res))
	}
	li.image = image

	var memReq vk.MemoryRequirements
	vk.GetImageMemoryRequirements(r.device, image, &memReq)
	memReq.Deref()
	mt, err := r.findMemoryType(memReq.MemoryTypeBits, vk.MemoryPropertyFlags(vk.MemoryPropertyDeviceLocalBit))
	if err != nil {
		return li, fmt.Errorf("vulkan: world image memory: %w", err)
	}
	alloc := vk.MemoryAllocateInfo{SType: vk.StructureTypeMemoryAllocateInfo, AllocationSize: memReq.Size, MemoryTypeIndex: mt}
	var mem vk.DeviceMemory
	if res := vk.AllocateMemory(r.device, &alloc, nil, &mem); res != vk.Success {
		return li, fmt.Errorf("vulkan: world image alloc: %s", vk.Error(res))
	}
	li.memory = mem
	if res := vk.BindImageMemory(r.device, image, mem, 0); res != vk.Success {
		return li, fmt.Errorf("vulkan: world image bind: %s", vk.Error(res))
	}

	viewInfo := vk.ImageViewCreateInfo{
		SType:    vk.StructureTypeImageViewCreateInfo,
		Image:    image,
		ViewType: vk.ImageViewType2d,
		Format:   format,
		Components: vk.ComponentMapping{
			R: vk.ComponentSwizzleIdentity, G: vk.ComponentSwizzleIdentity,
			B: vk.ComponentSwizzleIdentity, A: vk.ComponentSwizzleIdentity,
		},
		SubresourceRange: vk.ImageSubresourceRange{AspectMask: aspect, LevelCount: mips, LayerCount: 1},
	}
	var view vk.ImageView
	if res := vk.CreateImageView(r.device, &viewInfo, nil, &view); res != vk.Success {
		return li, fmt.Errorf("vulkan: world image view: %s", vk.Error(res))
	}
	li.view = view
	return li, nil
}

// wHostBuffer allocates a host-visible, host-coherent buffer and keeps it
// mapped for the renderer's lifetime.
func (r *Renderer) wHostBuffer(size int, usage vk.BufferUsageFlags) (vk.Buffer, vk.DeviceMemory, unsafe.Pointer, error) {
	info := vk.BufferCreateInfo{
		SType:       vk.StructureTypeBufferCreateInfo,
		Size:        vk.DeviceSize(size),
		Usage:       usage,
		SharingMode: vk.SharingModeExclusive,
	}
	var buf vk.Buffer
	if res := vk.CreateBuffer(r.device, &info, nil, &buf); res != vk.Success {
		return nil, nil, nil, fmt.Errorf("vulkan: world buffer: %s", vk.Error(res))
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
		return nil, nil, nil, fmt.Errorf("vulkan: world buffer alloc: %s", vk.Error(res))
	}
	if res := vk.BindBufferMemory(r.device, buf, mem, 0); res != vk.Success {
		return nil, nil, nil, fmt.Errorf("vulkan: world buffer bind: %s", vk.Error(res))
	}
	var p unsafe.Pointer
	if res := vk.MapMemory(r.device, mem, 0, vk.DeviceSize(size), 0, &p); res != vk.Success {
		return nil, nil, nil, fmt.Errorf("vulkan: world buffer map: %s", vk.Error(res))
	}
	return buf, mem, p, nil
}

func (r *Renderer) worldCreateSamplers() error {
	wr := r.wr

	texInfo := vk.SamplerCreateInfo{
		SType:        vk.StructureTypeSamplerCreateInfo,
		MagFilter:    vk.FilterLinear,
		MinFilter:    vk.FilterLinear,
		MipmapMode:   vk.SamplerMipmapModeLinear,
		AddressModeU: vk.SamplerAddressModeRepeat,
		AddressModeV: vk.SamplerAddressModeRepeat,
		AddressModeW: vk.SamplerAddressModeRepeat,
		MinLod:       0,
		MaxLod:       vk.LodClampNone,
	}
	if r.maxAnisotropy >= 2 {
		a := r.maxAnisotropy
		if a > 16 {
			a = 16
		}
		texInfo.AnisotropyEnable = vk.True
		texInfo.MaxAnisotropy = a
	}
	var texSamp vk.Sampler
	if res := vk.CreateSampler(r.device, &texInfo, nil, &texSamp); res != vk.Success {
		return fmt.Errorf("vulkan: world texture sampler: %s", vk.Error(res))
	}
	wr.texSampler = texSamp

	clampInfo := vk.SamplerCreateInfo{
		SType:        vk.StructureTypeSamplerCreateInfo,
		MagFilter:    vk.FilterLinear,
		MinFilter:    vk.FilterLinear,
		MipmapMode:   vk.SamplerMipmapModeNearest,
		AddressModeU: vk.SamplerAddressModeClampToEdge,
		AddressModeV: vk.SamplerAddressModeClampToEdge,
		AddressModeW: vk.SamplerAddressModeClampToEdge,
		MaxLod:       1,
	}
	var clampSamp vk.Sampler
	if res := vk.CreateSampler(r.device, &clampInfo, nil, &clampSamp); res != vk.Success {
		return fmt.Errorf("vulkan: world clamp sampler: %s", vk.Error(res))
	}
	wr.clampSampler = clampSamp

	// Depth sampler: NEAREST, clamp-to-edge. worldblit.frag samples the world
	// pass's D32 depth as an ordinary sampler2D (via texelFetch) for the water
	// SSR march — linear filtering of a depth format isn't a guaranteed
	// feature, and a marched depth read wants the exact texel anyway.
	depthInfo := vk.SamplerCreateInfo{
		SType:        vk.StructureTypeSamplerCreateInfo,
		MagFilter:    vk.FilterNearest,
		MinFilter:    vk.FilterNearest,
		MipmapMode:   vk.SamplerMipmapModeNearest,
		AddressModeU: vk.SamplerAddressModeClampToEdge,
		AddressModeV: vk.SamplerAddressModeClampToEdge,
		AddressModeW: vk.SamplerAddressModeClampToEdge,
		MaxLod:       1,
	}
	var depthSamp vk.Sampler
	if res := vk.CreateSampler(r.device, &depthInfo, nil, &depthSamp); res != vk.Success {
		return fmt.Errorf("vulkan: world depth sampler: %s", vk.Error(res))
	}
	wr.depthSampler = depthSamp
	return nil
}

// worldCreateOpaqueRenderPass builds rpOpaque: the opaque static geometry
// pass (walls/flats minus water, sky, depth pre-pass), always built
// regardless of wr.waterRefraction. Its FinalLayout/exit dependency differ
// by that toggle: off, this is the ONLY pass this frame (water draws inside
// it too, via the plain pipeline — see recordWorldCommandBuffer), so it
// hands straight to the present/ambient passes' fragment reads exactly like
// the old single rpScene did; on, its very next consumer is the capture
// copy ahead of rpWater, so it hands off to a transfer read instead.
func (r *Renderer) worldCreateOpaqueRenderPass() error {
	wr := r.wr
	finalLayout := vk.ImageLayoutShaderReadOnlyOptimal
	exitDstStage := vk.PipelineStageFlags(vk.PipelineStageFragmentShaderBit)
	exitDstAccess := vk.AccessFlags(vk.AccessShaderReadBit)
	if wr.waterRefraction {
		finalLayout = vk.ImageLayoutColorAttachmentOptimal // depth attachment below uses its own DepthStencilAttachmentOptimal
		exitDstStage = vk.PipelineStageFlags(vk.PipelineStageTransferBit)
		exitDstAccess = vk.AccessFlags(vk.AccessTransferReadBit)
	}
	color := vk.AttachmentDescription{
		Format:         sceneHDRFormat, // RGBA16F — dynamic lights can push past 1.0 for the bloom/tonemap
		Samples:        vk.SampleCount1Bit,
		LoadOp:         vk.AttachmentLoadOpClear,
		StoreOp:        vk.AttachmentStoreOpStore,
		StencilLoadOp:  vk.AttachmentLoadOpDontCare,
		StencilStoreOp: vk.AttachmentStoreOpDontCare,
		InitialLayout:  vk.ImageLayoutUndefined,
		FinalLayout:    finalLayout,
	}
	depthFinalLayout := vk.ImageLayoutShaderReadOnlyOptimal
	if wr.waterRefraction {
		depthFinalLayout = vk.ImageLayoutDepthStencilAttachmentOptimal
	}
	depth := vk.AttachmentDescription{
		Format:  vk.FormatD32Sfloat,
		Samples: vk.SampleCount1Bit,
		LoadOp:  vk.AttachmentLoadOpClear,
		// Kept (was DontCare) so the present blit can sample it for the water
		// SSR march; this pass's finalLayout hands it over as shader-read
		// (waterRefraction off) or attachment-writable (on, rpWater
		// continues into it via LoadOpLoad).
		StoreOp:        vk.AttachmentStoreOpStore,
		StencilLoadOp:  vk.AttachmentLoadOpDontCare,
		StencilStoreOp: vk.AttachmentStoreOpDontCare,
		InitialLayout:  vk.ImageLayoutUndefined,
		FinalLayout:    depthFinalLayout,
	}
	colorRef := vk.AttachmentReference{Attachment: 0, Layout: vk.ImageLayoutColorAttachmentOptimal}
	depthRef := vk.AttachmentReference{Attachment: 1, Layout: vk.ImageLayoutDepthStencilAttachmentOptimal}
	sub := vk.SubpassDescription{
		PipelineBindPoint:       vk.PipelineBindPointGraphics,
		ColorAttachmentCount:    1,
		PColorAttachments:       []vk.AttachmentReference{colorRef},
		PDepthStencilAttachment: &depthRef,
	}
	deps := []vk.SubpassDependency{
		{ // a previous frame's blit read of this slot's scene image finishes before we clear/write it
			SrcSubpass:    vk.SubpassExternal,
			DstSubpass:    0,
			SrcStageMask:  vk.PipelineStageFlags(vk.PipelineStageFragmentShaderBit),
			DstStageMask:  vk.PipelineStageFlags(vk.PipelineStageColorAttachmentOutputBit) | vk.PipelineStageFlags(vk.PipelineStageEarlyFragmentTestsBit),
			SrcAccessMask: vk.AccessFlags(vk.AccessShaderReadBit),
			DstAccessMask: vk.AccessFlags(vk.AccessColorAttachmentWriteBit) | vk.AccessFlags(vk.AccessDepthStencilAttachmentWriteBit),
		},
		{ // our colour + depth writes are visible to whatever reads them next (see exitDstStage/exitDstAccess above)
			SrcSubpass: 0,
			DstSubpass: vk.SubpassExternal,
			SrcStageMask: vk.PipelineStageFlags(vk.PipelineStageColorAttachmentOutputBit) |
				vk.PipelineStageFlags(vk.PipelineStageLateFragmentTestsBit),
			DstStageMask: exitDstStage,
			SrcAccessMask: vk.AccessFlags(vk.AccessColorAttachmentWriteBit) |
				vk.AccessFlags(vk.AccessDepthStencilAttachmentWriteBit),
			DstAccessMask: exitDstAccess,
		},
	}
	info := vk.RenderPassCreateInfo{
		SType:           vk.StructureTypeRenderPassCreateInfo,
		AttachmentCount: 2,
		PAttachments:    []vk.AttachmentDescription{color, depth},
		SubpassCount:    1,
		PSubpasses:      []vk.SubpassDescription{sub},
		DependencyCount: uint32(len(deps)),
		PDependencies:   deps,
	}
	var rp vk.RenderPass
	if res := vk.CreateRenderPass(r.device, &info, nil, &rp); res != vk.Success {
		return fmt.Errorf("vulkan: world opaque render pass: %s", vk.Error(res))
	}
	wr.rpOpaque = rp
	return nil
}

// worldCreateWaterRenderPass builds rpWater: water (real refraction) +
// sprites/voxels/particles, continuing from rpOpaque's already-drawn
// colour/depth via LoadOpLoad. A no-op (rpWater stays nil) when
// wr.waterRefraction is off — see recordWorldCommandBuffer's fallback.
// Its FinalLayout is ShaderReadOnlyOptimal — identical to the old single
// rpScene's — so the ambient/bloom/blit passes downstream need no changes
// either way.
func (r *Renderer) worldCreateWaterRenderPass() error {
	if !r.wr.waterRefraction {
		return nil
	}
	color := vk.AttachmentDescription{
		Format:         sceneHDRFormat,
		Samples:        vk.SampleCount1Bit,
		LoadOp:         vk.AttachmentLoadOpLoad,
		StoreOp:        vk.AttachmentStoreOpStore,
		StencilLoadOp:  vk.AttachmentLoadOpDontCare,
		StencilStoreOp: vk.AttachmentStoreOpDontCare,
		InitialLayout:  vk.ImageLayoutColorAttachmentOptimal,
		FinalLayout:    vk.ImageLayoutShaderReadOnlyOptimal,
	}
	depth := vk.AttachmentDescription{
		Format:         vk.FormatD32Sfloat,
		Samples:        vk.SampleCount1Bit,
		LoadOp:         vk.AttachmentLoadOpLoad,
		StoreOp:        vk.AttachmentStoreOpStore,
		StencilLoadOp:  vk.AttachmentLoadOpDontCare,
		StencilStoreOp: vk.AttachmentStoreOpDontCare,
		InitialLayout:  vk.ImageLayoutDepthStencilAttachmentOptimal,
		FinalLayout:    vk.ImageLayoutShaderReadOnlyOptimal,
	}
	colorRef := vk.AttachmentReference{Attachment: 0, Layout: vk.ImageLayoutColorAttachmentOptimal}
	depthRef := vk.AttachmentReference{Attachment: 1, Layout: vk.ImageLayoutDepthStencilAttachmentOptimal}
	sub := vk.SubpassDescription{
		PipelineBindPoint:       vk.PipelineBindPointGraphics,
		ColorAttachmentCount:    1,
		PColorAttachments:       []vk.AttachmentReference{colorRef},
		PDepthStencilAttachment: &depthRef,
	}
	deps := []vk.SubpassDependency{
		{ // belt-and-suspenders on top of recordWorldCommandBuffer's own explicit
			// capture-copy barriers (which already fully synchronize this hand-off) —
			// redundant but harmless, and cheap insurance against a barrier mistake.
			SrcSubpass:    vk.SubpassExternal,
			DstSubpass:    0,
			SrcStageMask:  vk.PipelineStageFlags(vk.PipelineStageTransferBit),
			DstStageMask:  vk.PipelineStageFlags(vk.PipelineStageColorAttachmentOutputBit) | vk.PipelineStageFlags(vk.PipelineStageEarlyFragmentTestsBit),
			SrcAccessMask: vk.AccessFlags(vk.AccessTransferWriteBit),
			DstAccessMask: vk.AccessFlags(vk.AccessColorAttachmentWriteBit) | vk.AccessFlags(vk.AccessDepthStencilAttachmentWriteBit),
		},
		{ // our colour + depth writes are visible to the present pass's fragment reads — identical to the old rpScene's own exit dependency
			SrcSubpass: 0,
			DstSubpass: vk.SubpassExternal,
			SrcStageMask: vk.PipelineStageFlags(vk.PipelineStageColorAttachmentOutputBit) |
				vk.PipelineStageFlags(vk.PipelineStageLateFragmentTestsBit),
			DstStageMask: vk.PipelineStageFlags(vk.PipelineStageFragmentShaderBit),
			SrcAccessMask: vk.AccessFlags(vk.AccessColorAttachmentWriteBit) |
				vk.AccessFlags(vk.AccessDepthStencilAttachmentWriteBit),
			DstAccessMask: vk.AccessFlags(vk.AccessShaderReadBit),
		},
	}
	info := vk.RenderPassCreateInfo{
		SType:           vk.StructureTypeRenderPassCreateInfo,
		AttachmentCount: 2,
		PAttachments:    []vk.AttachmentDescription{color, depth},
		SubpassCount:    1,
		PSubpasses:      []vk.SubpassDescription{sub},
		DependencyCount: uint32(len(deps)),
		PDependencies:   deps,
	}
	var rp vk.RenderPass
	if res := vk.CreateRenderPass(r.device, &info, nil, &rp); res != vk.Success {
		return fmt.Errorf("vulkan: world water render pass: %s", vk.Error(res))
	}
	r.wr.rpWater = rp
	return nil
}

func (r *Renderer) worldCreateOffscreen() error {
	wr := r.wr
	colUsage := vk.ImageUsageFlags(vk.ImageUsageColorAttachmentBit) | vk.ImageUsageFlags(vk.ImageUsageSampledBit)
	// SampledBit: the present blit reads this slot's depth for the water SSR march.
	depUsage := vk.ImageUsageFlags(vk.ImageUsageDepthStencilAttachmentBit) | vk.ImageUsageFlags(vk.ImageUsageSampledBit)
	for i := range wr.slots {
		sl := &wr.slots[i]
		var err error
		if sl.scene, err = r.wMakeImage(wr.w, wr.h, sceneHDRFormat, colUsage, vk.ImageAspectFlags(vk.ImageAspectColorBit), 1); err != nil {
			return err
		}
		if sl.depth, err = r.wMakeImage(wr.w, wr.h, vk.FormatD32Sfloat, depUsage, vk.ImageAspectFlags(vk.ImageAspectDepthBit), 1); err != nil {
			return err
		}
		if wr.waterRefraction {
			// Copy targets only (never rendered into — not framebuffer
			// attachments), so just TransferDst + Sampled, no attachment bit.
			captureColUsage := vk.ImageUsageFlags(vk.ImageUsageTransferDstBit) | vk.ImageUsageFlags(vk.ImageUsageSampledBit)
			if sl.preWaterColor, err = r.wMakeImage(wr.w, wr.h, sceneHDRFormat, captureColUsage, vk.ImageAspectFlags(vk.ImageAspectColorBit), 1); err != nil {
				return err
			}
			if sl.preWaterDepth, err = r.wMakeImage(wr.w, wr.h, vk.FormatD32Sfloat, captureColUsage, vk.ImageAspectFlags(vk.ImageAspectDepthBit), 1); err != nil {
				return err
			}
			sl.preWaterColorLayout = vk.ImageLayoutUndefined
			sl.preWaterDepthLayout = vk.ImageLayoutUndefined
		}
		fbInfo := vk.FramebufferCreateInfo{
			SType:           vk.StructureTypeFramebufferCreateInfo,
			RenderPass:      wr.rpOpaque,
			AttachmentCount: 2,
			PAttachments:    []vk.ImageView{sl.scene.view, sl.depth.view},
			Width:           uint32(wr.w),
			Height:          uint32(wr.h),
			Layers:          1,
		}
		var fb vk.Framebuffer
		if res := vk.CreateFramebuffer(r.device, &fbInfo, nil, &fb); res != vk.Success {
			return fmt.Errorf("vulkan: world scene framebuffer: %s", vk.Error(res))
		}
		sl.fb = fb
	}
	return nil
}

// worldCreateDummyTex builds the 1x1 black fallback brightmap. (Formerly
// also built the two vanilla-LUT images world.frag sampled its sector fade
// from; that fade is now the continuous formula in fade() — see world.frag
// — so the LUTs and their upload path were dead weight and were removed.)
func (r *Renderer) worldCreateDummyTex() error {
	wr := r.wr

	// 1x1 black dummy — bound to set-0 binding 1 for any texture that has no
	// brightmap, so world.frag can sample uBright unconditionally.
	dummy, err := r.wMakeImage(1, 1, vk.FormatR8g8b8a8Unorm,
		vk.ImageUsageFlags(vk.ImageUsageTransferDstBit)|vk.ImageUsageFlags(vk.ImageUsageSampledBit),
		vk.ImageAspectFlags(vk.ImageAspectColorBit), 1)
	if err != nil {
		return err
	}
	wr.dummyTex = dummy
	return r.uploadImagePixels(dummy.image, 1, 1, []byte{0, 0, 0, 255}, vk.ImageLayoutShaderReadOnlyOptimal)
}

func (r *Renderer) worldCreateBuffers() error {
	wr := r.wr
	ovBytes := wr.w * wr.h * 4
	const initVbuf = 1 << 20 // 1 MiB (~37k packed verts) — grows on demand

	for i := range wr.slots {
		sl := &wr.slots[i]
		var err error
		if sl.ubo, sl.uboMem, sl.uboMapped, err = r.wHostBuffer(worldUBOFloats*4, vk.BufferUsageFlags(vk.BufferUsageUniformBufferBit)); err != nil {
			return err
		}
		if sl.vbuf, sl.vbufMem, sl.vbufMapped, err = r.wHostBuffer(initVbuf, vk.BufferUsageFlags(vk.BufferUsageVertexBufferBit)); err != nil {
			return err
		}
		sl.vbufSize = initVbuf
		if sl.ovStage, sl.ovStageMem, sl.ovStageMapped, err = r.wHostBuffer(ovBytes, vk.BufferUsageFlags(vk.BufferUsageTransferSrcBit)); err != nil {
			return err
		}
		if sl.ovImg, err = r.wMakeImage(wr.w, wr.h, vk.FormatR8g8b8a8Unorm,
			vk.ImageUsageFlags(vk.ImageUsageTransferDstBit)|vk.ImageUsageFlags(vk.ImageUsageSampledBit),
			vk.ImageAspectFlags(vk.ImageAspectColorBit), 1); err != nil {
			return err
		}
		sl.ovLayout = vk.ImageLayoutUndefined
	}
	return nil
}

func (r *Renderer) worldCreateDescriptorLayouts() error {
	wr := r.wr
	mk := func(b []vk.DescriptorSetLayoutBinding) (vk.DescriptorSetLayout, error) {
		info := vk.DescriptorSetLayoutCreateInfo{
			SType:        vk.StructureTypeDescriptorSetLayoutCreateInfo,
			BindingCount: uint32(len(b)),
			PBindings:    b,
		}
		var l vk.DescriptorSetLayout
		if res := vk.CreateDescriptorSetLayout(r.device, &info, nil, &l); res != vk.Success {
			return nil, fmt.Errorf("vulkan: world dsl: %s", vk.Error(res))
		}
		return l, nil
	}
	uboBinding := vk.DescriptorSetLayoutBinding{
		Binding: 2, DescriptorType: vk.DescriptorTypeUniformBuffer, DescriptorCount: 1,
		StageFlags: vk.ShaderStageFlags(vk.ShaderStageFragmentBit),
	}
	// The blit reads the same world UBO (binding 4) for lightPos[] — the
	// dynamic-light screen-space shadow loop needs the light positions.
	uboBindingBlit := vk.DescriptorSetLayoutBinding{
		Binding: 4, DescriptorType: vk.DescriptorTypeUniformBuffer, DescriptorCount: 1,
		StageFlags: vk.ShaderStageFlags(vk.ShaderStageFragmentBit),
	}
	var err error
	if wr.dsl0, err = mk([]vk.DescriptorSetLayoutBinding{samplerBinding(0), samplerBinding(1)}); err != nil {
		return err
	}
	// binding 2 is a deliberate gap: bindings 0/1 used to be the two dead
	// vanilla-LUT samplers (see worldCreateDummyTex's comment) — leaving the
	// UBO at its existing binding number means voxel.frag (which shares this
	// exact layout, bound at its own set 0) needed no renumbering either.
	if wr.dsl1, err = mk([]vk.DescriptorSetLayoutBinding{uboBinding}); err != nil {
		return err
	}
	// present blit: uScene (HDR), uBloom (HDR half-res), uOverlay, uDepth (world pass D32), world UBO
	if wr.dslBlit, err = mk([]vk.DescriptorSetLayoutBinding{samplerBinding(0), samplerBinding(1), samplerBinding(2), samplerBinding(3), uboBindingBlit}); err != nil {
		return err
	}
	if wr.waterRefraction {
		// waterrefract.frag set 2: uPreWaterColor, uPreWaterDepth.
		if wr.dslWaterCapture, err = mk([]vk.DescriptorSetLayoutBinding{samplerBinding(0), samplerBinding(1)}); err != nil {
			return err
		}
	}
	return nil
}

func (r *Renderer) worldCreateDescriptors() error {
	wr := r.wr

	// waterCaptureSets/Samplers: one extra set per slot (setWaterCapture),
	// 2 combined-image-samplers each (uPreWaterColor, uPreWaterDepth) — only
	// when waterRefraction actually builds those sets (see the per-slot loop
	// below); zero otherwise, so the pool isn't oversized when the feature
	// is off.
	waterCaptureSets, waterCaptureSamplers := 0, 0
	if wr.waterRefraction {
		waterCaptureSets = maxFramesInFlight
		waterCaptureSamplers = 2 * maxFramesInFlight
	}
	framePoolInfo := vk.DescriptorPoolCreateInfo{
		SType:         vk.StructureTypeDescriptorPoolCreateInfo,
		MaxSets:       uint32(2*maxFramesInFlight + waterCaptureSets), // set1 + setBlit (+ setWaterCapture) per slot
		PoolSizeCount: 2,
		PPoolSizes: []vk.DescriptorPoolSize{
			// per slot: set1 = 0 samplers (its 2 LUT samplers were removed as
			// dead code), setBlit = 4 samplers (scene, bloom, overlay,
			// depth), setWaterCapture = 2 samplers when built; +2 slack.
			{Type: vk.DescriptorTypeCombinedImageSampler, DescriptorCount: uint32(4*maxFramesInFlight + 2 + waterCaptureSamplers)},
			// per slot: set1 + setBlit each bind the world UBO; +1 slack.
			{Type: vk.DescriptorTypeUniformBuffer, DescriptorCount: uint32(2*maxFramesInFlight + 1)},
		},
	}
	var fpool vk.DescriptorPool
	if res := vk.CreateDescriptorPool(r.device, &framePoolInfo, nil, &fpool); res != vk.Success {
		return fmt.Errorf("vulkan: world frame descriptor pool: %s", vk.Error(res))
	}
	wr.framePool = fpool

	// One set-0 per resident texture. Sized for many level loads' worth of
	// unique wall/flat names (the cache never evicts within a session).
	const maxTextures = 1024
	texPoolInfo := vk.DescriptorPoolCreateInfo{
		SType:   vk.StructureTypeDescriptorPoolCreateInfo,
		MaxSets: maxTextures,
		// Each set-0 has 2 combined-image-samplers now (base + brightmap).
		PoolSizeCount: 1,
		PPoolSizes:    []vk.DescriptorPoolSize{{Type: vk.DescriptorTypeCombinedImageSampler, DescriptorCount: 2 * maxTextures}},
	}
	var tpool vk.DescriptorPool
	if res := vk.CreateDescriptorPool(r.device, &texPoolInfo, nil, &tpool); res != vk.Success {
		return fmt.Errorf("vulkan: world texture descriptor pool: %s", vk.Error(res))
	}
	wr.texPool = tpool

	alloc := func(pool vk.DescriptorPool, layout vk.DescriptorSetLayout) (vk.DescriptorSet, error) {
		info := vk.DescriptorSetAllocateInfo{
			SType:              vk.StructureTypeDescriptorSetAllocateInfo,
			DescriptorPool:     pool,
			DescriptorSetCount: 1,
			PSetLayouts:        []vk.DescriptorSetLayout{layout},
		}
		var s vk.DescriptorSet
		if res := vk.AllocateDescriptorSets(r.device, &info, &s); res != vk.Success {
			return nil, fmt.Errorf("vulkan: world descriptor set: %s", vk.Error(res))
		}
		return s, nil
	}
	imgW := func(set vk.DescriptorSet, binding uint32, samp vk.Sampler, view vk.ImageView) vk.WriteDescriptorSet {
		return vk.WriteDescriptorSet{
			SType:           vk.StructureTypeWriteDescriptorSet,
			DstSet:          set,
			DstBinding:      binding,
			DescriptorCount: 1,
			DescriptorType:  vk.DescriptorTypeCombinedImageSampler,
			PImageInfo: []vk.DescriptorImageInfo{{
				Sampler: samp, ImageView: view, ImageLayout: vk.ImageLayoutShaderReadOnlyOptimal,
			}},
		}
	}

	var writes []vk.WriteDescriptorSet
	for i := range wr.slots {
		sl := &wr.slots[i]
		var err error
		if sl.set1, err = alloc(wr.framePool, wr.dsl1); err != nil {
			return err
		}
		if sl.setBlit, err = alloc(wr.framePool, wr.dslBlit); err != nil {
			return err
		}
		if wr.waterRefraction {
			if sl.setWaterCapture, err = alloc(wr.framePool, wr.dslWaterCapture); err != nil {
				return err
			}
			writes = append(writes,
				imgW(sl.setWaterCapture, 0, wr.clampSampler, sl.preWaterColor.view),
				imgW(sl.setWaterCapture, 1, wr.depthSampler, sl.preWaterDepth.view),
			)
		}
		writes = append(writes,
			vk.WriteDescriptorSet{
				SType:           vk.StructureTypeWriteDescriptorSet,
				DstSet:          sl.set1,
				DstBinding:      2,
				DescriptorCount: 1,
				DescriptorType:  vk.DescriptorTypeUniformBuffer,
				PBufferInfo:     []vk.DescriptorBufferInfo{{Buffer: sl.ubo, Offset: 0, Range: vk.DeviceSize(worldUBOFloats * 4)}},
			},
			imgW(sl.setBlit, 0, wr.clampSampler, sl.scene.view),
			imgW(sl.setBlit, 1, wr.clampSampler, wr.bloom.mip[0].img.view),
			imgW(sl.setBlit, 2, wr.clampSampler, sl.ovImg.view),
			imgW(sl.setBlit, 3, wr.depthSampler, sl.depth.view),
			vk.WriteDescriptorSet{
				SType:           vk.StructureTypeWriteDescriptorSet,
				DstSet:          sl.setBlit,
				DstBinding:      4,
				DescriptorCount: 1,
				DescriptorType:  vk.DescriptorTypeUniformBuffer,
				PBufferInfo:     []vk.DescriptorBufferInfo{{Buffer: sl.ubo, Offset: 0, Range: vk.DeviceSize(worldUBOFloats * 4)}},
			},
		)
	}
	vk.UpdateDescriptorSets(r.device, uint32(len(writes)), writes, 0, nil)
	return nil
}

func (r *Renderer) worldCreatePipelines() error {
	wr := r.wr

	// World pipeline layout: set 0 (per-texture), set 1 (World UBO),
	// push-constant mat4 (the view-projection) in the vertex stage.
	wlInfo := vk.PipelineLayoutCreateInfo{
		SType:                  vk.StructureTypePipelineLayoutCreateInfo,
		SetLayoutCount:         2,
		PSetLayouts:            []vk.DescriptorSetLayout{wr.dsl0, wr.dsl1},
		PushConstantRangeCount: 1,
		PPushConstantRanges:    []vk.PushConstantRange{{StageFlags: vk.ShaderStageFlags(vk.ShaderStageVertexBit), Offset: 0, Size: 64}},
	}
	var wl vk.PipelineLayout
	if res := vk.CreatePipelineLayout(r.device, &wlInfo, nil, &wl); res != vk.Success {
		return fmt.Errorf("vulkan: world pipeline layout: %s", vk.Error(res))
	}
	wr.plWorld = wl

	blInfo := vk.PipelineLayoutCreateInfo{
		SType:          vk.StructureTypePipelineLayoutCreateInfo,
		SetLayoutCount: 2,
		PSetLayouts:    []vk.DescriptorSetLayout{wr.dslBlit, wr.ambient.dslSample},
		// worldblit.frag PC (6 vec4 = 96B, well under the 128B
		// maxPushConstantsSize floor): p0 exposure,bloomStrength,waterClock,
		// near; p1 sinA,cosA,focal,far; p2 camX,camY,camZ,horizonY; p3
		// w,h,ssrStrength,godrayStrength; p4 sharpenStrength,ditherStrength,
		// unused,unused; p5 screenTint.rgb,amount. The dyn-light shadow
		// march, SSAO, and god rays' own sun direction all moved to
		// worldambient.go's own pass (sampled here via set 1 instead) — this
		// used to stay at the old 8-vec4/128B shape with those slots left
		// vestigial rather than repacked.
		PushConstantRangeCount: 1,
		PPushConstantRanges:    []vk.PushConstantRange{{StageFlags: vk.ShaderStageFlags(vk.ShaderStageFragmentBit), Offset: 0, Size: 96}},
	}
	var bl vk.PipelineLayout
	if res := vk.CreatePipelineLayout(r.device, &blInfo, nil, &bl); res != vk.Success {
		return fmt.Errorf("vulkan: world blit pipeline layout: %s", vk.Error(res))
	}
	wr.plBlit = bl

	vert, err := r.createShaderModule(worldVertSPV)
	if err != nil {
		return fmt.Errorf("vulkan: world vert: %w", err)
	}
	defer vk.DestroyShaderModule(r.device, vert, nil)
	frag, err := r.createShaderModule(worldFragSPV)
	if err != nil {
		return fmt.Errorf("vulkan: world frag: %w", err)
	}
	defer vk.DestroyShaderModule(r.device, frag, nil)

	stages := []vk.PipelineShaderStageCreateInfo{
		{SType: vk.StructureTypePipelineShaderStageCreateInfo, Stage: vk.ShaderStageVertexBit, Module: vert, PName: nz("main")},
		{SType: vk.StructureTypePipelineShaderStageCreateInfo, Stage: vk.ShaderStageFragmentBit, Module: frag, PName: nz("main")},
	}

	attrs := worldgeo.VertexAttributes()
	vkAttrs := make([]vk.VertexInputAttributeDescription, len(attrs))
	for i, a := range attrs {
		vkAttrs[i] = vk.VertexInputAttributeDescription{
			Location: uint32(a.Location), Binding: 0, Format: vkFormatOf(a.Format), Offset: uint32(a.Offset),
		}
	}
	binding := vk.VertexInputBindingDescription{Binding: 0, Stride: uint32(worldgeo.PackedVertexSize), InputRate: vk.VertexInputRateVertex}
	vertexInput := vk.PipelineVertexInputStateCreateInfo{
		SType:                           vk.StructureTypePipelineVertexInputStateCreateInfo,
		VertexBindingDescriptionCount:   1,
		PVertexBindingDescriptions:      []vk.VertexInputBindingDescription{binding},
		VertexAttributeDescriptionCount: uint32(len(vkAttrs)),
		PVertexAttributeDescriptions:    vkAttrs,
	}
	inputAssembly := vk.PipelineInputAssemblyStateCreateInfo{
		SType: vk.StructureTypePipelineInputAssemblyStateCreateInfo, Topology: vk.PrimitiveTopologyTriangleList,
	}
	viewportState := vk.PipelineViewportStateCreateInfo{
		SType: vk.StructureTypePipelineViewportStateCreateInfo, ViewportCount: 1, ScissorCount: 1,
	}
	rasterizer := vk.PipelineRasterizationStateCreateInfo{
		SType:       vk.StructureTypePipelineRasterizationStateCreateInfo,
		PolygonMode: vk.PolygonModeFill,
		CullMode:    vk.CullModeFlags(vk.CullModeNone), // winding isn't consistent across flats/walls yet
		FrontFace:   vk.FrontFaceCounterClockwise,
		LineWidth:   1,
	}
	multisample := vk.PipelineMultisampleStateCreateInfo{
		SType: vk.StructureTypePipelineMultisampleStateCreateInfo, RasterizationSamples: vk.SampleCount1Bit,
	}
	depthStencil := vk.PipelineDepthStencilStateCreateInfo{
		SType:           vk.StructureTypePipelineDepthStencilStateCreateInfo,
		DepthTestEnable: vk.True, DepthWriteEnable: vk.True, DepthCompareOp: vk.CompareOpLessOrEqual,
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
		PDepthStencilState:  &depthStencil,
		PColorBlendState:    &colorBlend,
		PDynamicState:       &dynamic,
		Layout:              wr.plWorld,
		RenderPass:          wr.rpOpaque,
		Subpass:             0,
	}
	out := make([]vk.Pipeline, 1)
	if res := vk.CreateGraphicsPipelines(r.device, nil, 1, []vk.GraphicsPipelineCreateInfo{info}, nil, out); res != vk.Success {
		return fmt.Errorf("vulkan: world pipeline: %s", vk.Error(res))
	}
	wr.pipeWorld = out[0]

	// Particle pipeline: same shaders/vertex-input/layout as pipeWorld (a
	// particle billboard is drawn with the ordinary KindSprite/KindSpriteFull
	// vertex kind — nothing shader-side distinguishes it), but depth-write
	// off and additive colour blending, so a dense burst of overlapping
	// billboards (engine.Particle) glows brighter where they overlap instead
	// of alpha-cutout-occluding each other the way the regular sprite pass
	// does. Depth TEST stays on (LESS_OR_EQUAL, against the already-written
	// world depth) so particles are still correctly hidden behind walls.
	// Alpha is excluded from ColorWriteMask so this pass can never flip the
	// scene alpha's water-reflection tag (world.frag writes alpha 0 for
	// water, 1 otherwise; see worldblit.frag).
	particleDepthStencil := vk.PipelineDepthStencilStateCreateInfo{
		SType:           vk.StructureTypePipelineDepthStencilStateCreateInfo,
		DepthTestEnable: vk.True, DepthWriteEnable: vk.False, DepthCompareOp: vk.CompareOpLessOrEqual,
	}
	particleCBA := vk.PipelineColorBlendAttachmentState{
		BlendEnable:         vk.True,
		SrcColorBlendFactor: vk.BlendFactorOne,
		DstColorBlendFactor: vk.BlendFactorOne,
		ColorBlendOp:        vk.BlendOpAdd,
		SrcAlphaBlendFactor: vk.BlendFactorZero,
		DstAlphaBlendFactor: vk.BlendFactorOne,
		AlphaBlendOp:        vk.BlendOpAdd,
		ColorWriteMask: vk.ColorComponentFlags(vk.ColorComponentRBit) | vk.ColorComponentFlags(vk.ColorComponentGBit) |
			vk.ColorComponentFlags(vk.ColorComponentBBit),
	}
	particleColorBlend := vk.PipelineColorBlendStateCreateInfo{
		SType: vk.StructureTypePipelineColorBlendStateCreateInfo, AttachmentCount: 1,
		PAttachments: []vk.PipelineColorBlendAttachmentState{particleCBA},
	}
	particleInfo := vk.GraphicsPipelineCreateInfo{
		SType:               vk.StructureTypeGraphicsPipelineCreateInfo,
		StageCount:          2,
		PStages:             stages,
		PVertexInputState:   &vertexInput,
		PInputAssemblyState: &inputAssembly,
		PViewportState:      &viewportState,
		PRasterizationState: &rasterizer,
		PMultisampleState:   &multisample,
		PDepthStencilState:  &particleDepthStencil,
		PColorBlendState:    &particleColorBlend,
		PDynamicState:       &dynamic,
		Layout:              wr.plWorld,
		RenderPass:          wr.rpOpaque,
		Subpass:             0,
	}
	particleOut := make([]vk.Pipeline, 1)
	if res := vk.CreateGraphicsPipelines(r.device, nil, 1, []vk.GraphicsPipelineCreateInfo{particleInfo}, nil, particleOut); res != vk.Success {
		return fmt.Errorf("vulkan: particle pipeline: %s", vk.Error(res))
	}
	wr.pipeParticleAdd = particleOut[0]

	// Translucent particle pipeline: same shaders/vertex-input/layout again,
	// but real SrcAlpha/OneMinusSrcAlpha blending instead of additive — for
	// KindSpriteTrans (engine.Particle's smoke), which wants to look like
	// haze that dims what's behind it, not another glowing light source.
	// Depth state and the alpha write mask match pipeParticleAdd exactly
	// (test only, no write; colour-only write mask to protect the scene
	// alpha's water-reflection tag) — only the blend factors differ. The
	// CPU side (worldgeo.Builder.AddTransparentSprites) sorts back-to-front
	// before this ever runs, since unlike additive this pipeline IS
	// order-dependent.
	transCBA := vk.PipelineColorBlendAttachmentState{
		BlendEnable:         vk.True,
		SrcColorBlendFactor: vk.BlendFactorSrcAlpha,
		DstColorBlendFactor: vk.BlendFactorOneMinusSrcAlpha,
		ColorBlendOp:        vk.BlendOpAdd,
		SrcAlphaBlendFactor: vk.BlendFactorZero,
		DstAlphaBlendFactor: vk.BlendFactorOne,
		AlphaBlendOp:        vk.BlendOpAdd,
		ColorWriteMask: vk.ColorComponentFlags(vk.ColorComponentRBit) | vk.ColorComponentFlags(vk.ColorComponentGBit) |
			vk.ColorComponentFlags(vk.ColorComponentBBit),
	}
	transColorBlend := vk.PipelineColorBlendStateCreateInfo{
		SType: vk.StructureTypePipelineColorBlendStateCreateInfo, AttachmentCount: 1,
		PAttachments: []vk.PipelineColorBlendAttachmentState{transCBA},
	}
	transInfo := vk.GraphicsPipelineCreateInfo{
		SType:               vk.StructureTypeGraphicsPipelineCreateInfo,
		StageCount:          2,
		PStages:             stages,
		PVertexInputState:   &vertexInput,
		PInputAssemblyState: &inputAssembly,
		PViewportState:      &viewportState,
		PRasterizationState: &rasterizer,
		PMultisampleState:   &multisample,
		PDepthStencilState:  &particleDepthStencil,
		PColorBlendState:    &transColorBlend,
		PDynamicState:       &dynamic,
		Layout:              wr.plWorld,
		RenderPass:          wr.rpOpaque,
		Subpass:             0,
	}
	transOut := make([]vk.Pipeline, 1)
	if res := vk.CreateGraphicsPipelines(r.device, nil, 1, []vk.GraphicsPipelineCreateInfo{transInfo}, nil, transOut); res != vk.Success {
		return fmt.Errorf("vulkan: translucent particle pipeline: %s", vk.Error(res))
	}
	wr.pipeParticleTrans = transOut[0]

	// Depth pre-pass pipeline: positions only (depthonly.vert), empty frag,
	// colour writes masked off, LESS depth write. Reuses plWorld (no
	// descriptor sets touched) + the interleaved static VBO (stride matches;
	// only attribute 0 is fetched).
	if err := r.worldCreateDepthPipeline(); err != nil {
		return err
	}

	if wr.waterRefraction {
		// Water (real refraction): same vertex stage/input/rasterizer/
		// multisample state as pipeWorld (world.vert is kind-agnostic, so
		// it's reused unmodified — see waterrefract.frag's own doc
		// comment), a dedicated fragment module, and its own pipeline
		// layout (set 2 = dslWaterCapture, on top of plWorld's set 0/1).
		// Depth test/write on (LessOrEqual) — must depth-test against
		// rpOpaque's already-written statics, and still write depth so
		// sprites/voxels/particles drawn after it in rpWater occlude
		// correctly, same as every other opaque-ish pass here.
		liqLayoutInfo := vk.PipelineLayoutCreateInfo{
			SType:                  vk.StructureTypePipelineLayoutCreateInfo,
			SetLayoutCount:         3,
			PSetLayouts:            []vk.DescriptorSetLayout{wr.dsl0, wr.dsl1, wr.dslWaterCapture},
			PushConstantRangeCount: 1,
			PPushConstantRanges:    []vk.PushConstantRange{{StageFlags: vk.ShaderStageFlags(vk.ShaderStageVertexBit), Offset: 0, Size: 64}},
		}
		var liqLayout vk.PipelineLayout
		if res := vk.CreatePipelineLayout(r.device, &liqLayoutInfo, nil, &liqLayout); res != vk.Success {
			return fmt.Errorf("vulkan: water liquid pipeline layout: %s", vk.Error(res))
		}
		wr.plWorldLiquid = liqLayout

		liqFrag, err := r.createShaderModule(waterRefractFragSPV)
		if err != nil {
			return fmt.Errorf("vulkan: water refract frag: %w", err)
		}
		defer vk.DestroyShaderModule(r.device, liqFrag, nil)
		liqStages := []vk.PipelineShaderStageCreateInfo{
			{SType: vk.StructureTypePipelineShaderStageCreateInfo, Stage: vk.ShaderStageVertexBit, Module: vert, PName: nz("main")},
			{SType: vk.StructureTypePipelineShaderStageCreateInfo, Stage: vk.ShaderStageFragmentBit, Module: liqFrag, PName: nz("main")},
		}
		liqInfo := vk.GraphicsPipelineCreateInfo{
			SType:               vk.StructureTypeGraphicsPipelineCreateInfo,
			StageCount:          2,
			PStages:             liqStages,
			PVertexInputState:   &vertexInput,
			PInputAssemblyState: &inputAssembly,
			PViewportState:      &viewportState,
			PRasterizationState: &rasterizer,
			PMultisampleState:   &multisample,
			PDepthStencilState:  &depthStencil,
			PColorBlendState:    &colorBlend,
			PDynamicState:       &dynamic,
			Layout:              wr.plWorldLiquid,
			RenderPass:          wr.rpWater,
			Subpass:             0,
		}
		liqOut := make([]vk.Pipeline, 1)
		if res := vk.CreateGraphicsPipelines(r.device, nil, 1, []vk.GraphicsPipelineCreateInfo{liqInfo}, nil, liqOut); res != vk.Success {
			return fmt.Errorf("vulkan: water liquid pipeline: %s", vk.Error(res))
		}
		wr.pipeWorldLiquid = liqOut[0]
	}

	// Present blit: the fullscreen triangle (blit.vert) + worldblit.frag,
	// no vertex input, no depth, targeting the swapchain render pass. Same
	// shape as the lit path's fullscreen passes, so reuse litPipeline.
	p, err := r.litPipeline(worldBlitFragSPV, r.renderPass, wr.plBlit)
	if err != nil {
		return err
	}
	wr.pipeBlit = p
	return nil
}

func (r *Renderer) worldCreateDepthPipeline() error {
	wr := r.wr
	vert, err := r.createShaderModule(depthOnlyVertSPV)
	if err != nil {
		return fmt.Errorf("vulkan: depth vert: %w", err)
	}
	defer vk.DestroyShaderModule(r.device, vert, nil)
	frag, err := r.createShaderModule(depthFragSPV)
	if err != nil {
		return fmt.Errorf("vulkan: depth frag: %w", err)
	}
	defer vk.DestroyShaderModule(r.device, frag, nil)

	stages := []vk.PipelineShaderStageCreateInfo{
		{SType: vk.StructureTypePipelineShaderStageCreateInfo, Stage: vk.ShaderStageVertexBit, Module: vert, PName: nz("main")},
		{SType: vk.StructureTypePipelineShaderStageCreateInfo, Stage: vk.ShaderStageFragmentBit, Module: frag, PName: nz("main")},
	}
	binding := vk.VertexInputBindingDescription{Binding: 0, Stride: uint32(worldgeo.PackedVertexSize), InputRate: vk.VertexInputRateVertex}
	vertexInput := vk.PipelineVertexInputStateCreateInfo{
		SType:                           vk.StructureTypePipelineVertexInputStateCreateInfo,
		VertexBindingDescriptionCount:   1,
		PVertexBindingDescriptions:      []vk.VertexInputBindingDescription{binding},
		VertexAttributeDescriptionCount: 1,
		PVertexAttributeDescriptions: []vk.VertexInputAttributeDescription{
			{Location: 0, Binding: 0, Format: vk.FormatR32g32b32Sfloat, Offset: 0},
		},
	}
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
	depthStencil := vk.PipelineDepthStencilStateCreateInfo{
		SType:           vk.StructureTypePipelineDepthStencilStateCreateInfo,
		DepthTestEnable: vk.True, DepthWriteEnable: vk.True, DepthCompareOp: vk.CompareOpLess,
	}
	// One colour attachment (rpOpaque has one) but write nothing.
	cba := vk.PipelineColorBlendAttachmentState{ColorWriteMask: 0}
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
		PDepthStencilState:  &depthStencil,
		PColorBlendState:    &colorBlend,
		PDynamicState:       &dynamic,
		Layout:              wr.plWorld,
		RenderPass:          wr.rpOpaque,
		Subpass:             0,
	}
	out := make([]vk.Pipeline, 1)
	if res := vk.CreateGraphicsPipelines(r.device, nil, 1, []vk.GraphicsPipelineCreateInfo{info}, nil, out); res != vk.Success {
		return fmt.Errorf("vulkan: depth pre-pass pipeline: %s", vk.Error(res))
	}
	wr.pipeDepth = out[0]
	return nil
}
