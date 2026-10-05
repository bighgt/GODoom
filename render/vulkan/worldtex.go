package vulkan

import (
	"fmt"
	"slices"

	vk "github.com/goki/vulkan"

	"twopointfive/assets"
	"twopointfive/render/worldgeo"
)

// Per-texture GPU residency for the hardware-geometry path. Every wall
// texture / flat / sky the visible geometry references is uploaded once as a
// device-local image with a full mip chain (built on the GPU with
// vkCmdBlitImage) and given the set-0 descriptor world.frag samples it
// through. Uploads happen at level load, not per frame.

// barrier issues a single image-memory pipeline barrier.
func barrier(cmd vk.CommandBuffer, img vk.Image, rng vk.ImageSubresourceRange,
	oldL, newL vk.ImageLayout, srcA, dstA vk.AccessFlags, srcS, dstS vk.PipelineStageFlags) {
	b := vk.ImageMemoryBarrier{
		SType:               vk.StructureTypeImageMemoryBarrier,
		OldLayout:           oldL,
		NewLayout:           newL,
		SrcQueueFamilyIndex: vk.QueueFamilyIgnored,
		DstQueueFamilyIndex: vk.QueueFamilyIgnored,
		Image:               img,
		SubresourceRange:    rng,
		SrcAccessMask:       srcA,
		DstAccessMask:       dstA,
	}
	vk.CmdPipelineBarrier(cmd, srcS, dstS, 0, 0, nil, 0, nil, 1, []vk.ImageMemoryBarrier{b})
}

// submitOneShot records fn into a throwaway primary command buffer, submits
// it to the graphics queue, and blocks until it retires. For the one-time
// texture / LUT uploads only — never on the per-frame path.
func (r *Renderer) submitOneShot(fn func(cmd vk.CommandBuffer)) error {
	allocInfo := vk.CommandBufferAllocateInfo{
		SType:              vk.StructureTypeCommandBufferAllocateInfo,
		CommandPool:        r.commandPool,
		Level:              vk.CommandBufferLevelPrimary,
		CommandBufferCount: 1,
	}
	cmds := make([]vk.CommandBuffer, 1)
	if res := vk.AllocateCommandBuffers(r.device, &allocInfo, cmds); res != vk.Success {
		return fmt.Errorf("vulkan: one-shot cmd alloc: %s", vk.Error(res))
	}
	defer vk.FreeCommandBuffers(r.device, r.commandPool, 1, cmds)
	cmd := cmds[0]

	begin := vk.CommandBufferBeginInfo{
		SType: vk.StructureTypeCommandBufferBeginInfo,
		Flags: vk.CommandBufferUsageFlags(vk.CommandBufferUsageOneTimeSubmitBit),
	}
	if res := vk.BeginCommandBuffer(cmd, &begin); res != vk.Success {
		return fmt.Errorf("vulkan: one-shot begin: %s", vk.Error(res))
	}
	fn(cmd)
	if res := vk.EndCommandBuffer(cmd); res != vk.Success {
		return fmt.Errorf("vulkan: one-shot end: %s", vk.Error(res))
	}

	var fence vk.Fence
	if res := vk.CreateFence(r.device, &vk.FenceCreateInfo{SType: vk.StructureTypeFenceCreateInfo}, nil, &fence); res != vk.Success {
		return fmt.Errorf("vulkan: one-shot fence: %s", vk.Error(res))
	}
	defer vk.DestroyFence(r.device, fence, nil)

	submit := vk.SubmitInfo{SType: vk.StructureTypeSubmitInfo, CommandBufferCount: 1, PCommandBuffers: cmds}
	if res := vk.QueueSubmit(r.graphicsQueue, 1, []vk.SubmitInfo{submit}, fence); res != vk.Success {
		return fmt.Errorf("vulkan: one-shot submit: %s", vk.Error(res))
	}
	vk.WaitForFences(r.device, 1, []vk.Fence{fence}, vk.True, vk.MaxUint64)
	return nil
}

// texResidency is the per-texture (and per-voxel-model) GPU state both the
// FPS hardware path (worldResources) and the isometric path (isoResources)
// keep — embedded in each, so wr.texCache / ir.texCache etc. read as plain
// fields. hw and iso are mutually exclusive per Renderer instance, so each
// owns its own copy rather than sharing one.
type texResidency struct {
	texSampler vk.Sampler // wall/flat/sprite textures: linear + mip + anisotropic, REPEAT
	dummyTex   litImage   // 1x1 black — set-0 binding 1 for textures with no brightmap

	dsl0    vk.DescriptorSetLayout // set 0: per-texture (tex, brightmap)
	texPool vk.DescriptorPool      // per-texture set0, grows over a session

	texCache map[*assets.RGBA]*worldTex // keyed by the shared bitmap pointer
	voxCache map[*assets.VoxelModel]*voxelMesh
}

// maxResidentTextures sizes texPool: many level loads' worth of unique
// wall/flat/sprite names (the cache never evicts within a session).
const maxResidentTextures = 1024

// initTexResidency creates tr's sampler, black dummy brightmap, set-0
// layout and descriptor pool, and its empty caches. what ("world" / "iso")
// only labels error messages. Needs the command pool (the dummy upload is
// a one-shot submit).
func (r *Renderer) initTexResidency(tr *texResidency, what string) error {
	tr.texCache = make(map[*assets.RGBA]*worldTex)
	tr.voxCache = make(map[*assets.VoxelModel]*voxelMesh)

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
		texInfo.AnisotropyEnable = vk.True
		texInfo.MaxAnisotropy = min(r.maxAnisotropy, 16)
	}
	var texSamp vk.Sampler
	if res := vk.CreateSampler(r.device, &texInfo, nil, &texSamp); res != vk.Success {
		return fmt.Errorf("vulkan: %s texture sampler: %s", what, vk.Error(res))
	}
	tr.texSampler = texSamp

	// 1x1 black dummy — bound to set-0 binding 1 for any texture that has no
	// brightmap, so the fragment shader can sample it unconditionally.
	dummy, err := r.wMakeImage(1, 1, vk.FormatR8g8b8a8Unorm,
		vk.ImageUsageFlags(vk.ImageUsageTransferDstBit)|vk.ImageUsageFlags(vk.ImageUsageSampledBit),
		vk.ImageAspectFlags(vk.ImageAspectColorBit), 1)
	if err != nil {
		return err
	}
	tr.dummyTex = dummy
	if err := r.uploadImagePixels(dummy.image, 1, 1, []byte{0, 0, 0, 255}, vk.ImageLayoutShaderReadOnlyOptimal); err != nil {
		return err
	}

	if tr.dsl0, err = r.newDescriptorSetLayout(what+" texture", samplerBinding(0), samplerBinding(1)); err != nil {
		return err
	}

	// Each set-0 has 2 combined-image-samplers (base + brightmap).
	poolInfo := vk.DescriptorPoolCreateInfo{
		SType:         vk.StructureTypeDescriptorPoolCreateInfo,
		MaxSets:       maxResidentTextures,
		PoolSizeCount: 1,
		PPoolSizes:    []vk.DescriptorPoolSize{{Type: vk.DescriptorTypeCombinedImageSampler, DescriptorCount: 2 * maxResidentTextures}},
	}
	var pool vk.DescriptorPool
	if res := vk.CreateDescriptorPool(r.device, &poolInfo, nil, &pool); res != vk.Success {
		return fmt.Errorf("vulkan: %s texture descriptor pool: %s", what, vk.Error(res))
	}
	tr.texPool = pool
	return nil
}

// destroy frees everything initTexResidency and the sync/upload methods
// below created. Safe on a partially initialized tr.
func (tr *texResidency) destroy(dev vk.Device) {
	for _, vm := range tr.voxCache {
		vm.free(dev)
	}
	tr.voxCache = nil
	for _, wt := range tr.texCache {
		freeLitImage(dev, wt.img)
		freeLitImage(dev, wt.bright)
		// wt.set is freed implicitly by destroying texPool below.
	}
	tr.texCache = nil
	if tr.texPool != nil {
		vk.DestroyDescriptorPool(dev, tr.texPool, nil)
		tr.texPool = nil
	}
	if tr.dsl0 != nil {
		vk.DestroyDescriptorSetLayout(dev, tr.dsl0, nil)
		tr.dsl0 = nil
	}
	freeLitImage(dev, tr.dummyTex)
	tr.dummyTex = litImage{}
	if tr.texSampler != nil {
		vk.DestroySampler(dev, tr.texSampler, nil)
		tr.texSampler = nil
	}
}

// syncTextures uploads any bitmap in g (wall/flat/sprite textures + the
// sky) that isn't resident in tr yet. Drains the device first, but only
// when there's actually something new — so it's free once a level is warm.
func (r *Renderer) syncTextures(tr *texResidency, g *worldgeo.Geometry) error {
	missing := func(t *assets.RGBA) bool { return t != nil && tr.texCache[t] == nil }

	pending := missing(g.Sky) || slices.ContainsFunc(g.Textures, missing)
	if !pending {
		return nil
	}

	vk.DeviceWaitIdle(r.device)
	for i, t := range g.Textures {
		if !missing(t) {
			continue
		}
		var bright *assets.RGBA
		if i < len(g.Brightmaps) {
			bright = g.Brightmaps[i]
		}
		if err := r.uploadTexture(tr, t, bright); err != nil {
			return err
		}
	}
	if missing(g.Sky) {
		if err := r.uploadTexture(tr, g.Sky, nil); err != nil {
			return err
		}
	}
	return nil
}

// uploadTexture makes t resident in tr: a device-local RGBA8 image with a
// full mip chain and its set-0 descriptor, cached by the bitmap pointer.
func (r *Renderer) uploadTexture(tr *texResidency, t, bright *assets.RGBA) error {
	img, mips, err := r.makeResidentImage(t)
	if err != nil {
		return err
	}

	// The brightmap (if any) becomes its own resident image; its view feeds
	// set-0 binding 1. No brightmap -> the shared black dummy.
	var bimg litImage
	brightView := tr.dummyTex.view
	if bright != nil {
		bimg, _, err = r.makeResidentImage(bright)
		if err != nil {
			freeLitImage(r.device, img)
			return err
		}
		brightView = bimg.view
	}

	set, err := r.allocTexSet(tr, img.view, brightView)
	if err != nil {
		freeLitImage(r.device, img)
		freeLitImage(r.device, bimg)
		return err
	}
	tr.texCache[t] = &worldTex{img: img, bright: bimg, set: set, mips: mips}
	return nil
}

// makeResidentImage uploads t as a device-local RGBA8 image with a full mip
// chain, left in SHADER_READ_ONLY_OPTIMAL. It does NOT allocate a
// descriptor set — the caller wires the view into one.
func (r *Renderer) makeResidentImage(t *assets.RGBA) (litImage, uint32, error) {
	npix := t.Width * t.Height * 4
	if t.Width <= 0 || t.Height <= 0 || len(t.Pix) < npix {
		return litImage{}, 0, fmt.Errorf("vulkan: world texture %dx%d has %d bytes (want %d)", t.Width, t.Height, len(t.Pix), npix)
	}
	mips := uint32(1)
	for s := max(t.Width, t.Height); s > 1; s >>= 1 {
		mips++
	}
	img, err := r.wMakeImage(t.Width, t.Height, vk.FormatR8g8b8a8Unorm,
		vk.ImageUsageFlags(vk.ImageUsageTransferSrcBit)|vk.ImageUsageFlags(vk.ImageUsageTransferDstBit)|vk.ImageUsageFlags(vk.ImageUsageSampledBit),
		vk.ImageAspectFlags(vk.ImageAspectColorBit), mips)
	if err != nil {
		return litImage{}, 0, err
	}
	buf, mem, mapped, err := r.wHostBuffer(npix, vk.BufferUsageFlags(vk.BufferUsageTransferSrcBit))
	if err != nil {
		freeLitImage(r.device, img)
		return litImage{}, 0, err
	}
	vk.Memcopy(mapped, t.Pix[:npix])
	defer func() {
		vk.UnmapMemory(r.device, mem)
		vk.DestroyBuffer(r.device, buf, nil)
		vk.FreeMemory(r.device, mem, nil)
	}()
	if err := r.submitOneShot(func(cmd vk.CommandBuffer) {
		r.recordTextureUpload(cmd, img.image, buf, t.Width, t.Height, mips)
	}); err != nil {
		freeLitImage(r.device, img)
		return litImage{}, 0, err
	}
	return img, mips, nil
}

// recordTextureUpload copies mip 0 from staging, then blits the mip chain
// down level by level, leaving every level in SHADER_READ_ONLY_OPTIMAL.
func (r *Renderer) recordTextureUpload(cmd vk.CommandBuffer, img vk.Image, staging vk.Buffer, w, h int, mips uint32) {
	colorAspect := vk.ImageAspectFlags(vk.ImageAspectColorBit)

	all := vk.ImageSubresourceRange{AspectMask: colorAspect, LevelCount: mips, LayerCount: 1}
	barrier(cmd, img, all, vk.ImageLayoutUndefined, vk.ImageLayoutTransferDstOptimal,
		0, vk.AccessFlags(vk.AccessTransferWriteBit),
		vk.PipelineStageFlags(vk.PipelineStageTopOfPipeBit), vk.PipelineStageFlags(vk.PipelineStageTransferBit))

	region := vk.BufferImageCopy{
		ImageSubresource: vk.ImageSubresourceLayers{AspectMask: colorAspect, MipLevel: 0, LayerCount: 1},
		ImageExtent:      vk.Extent3D{Width: uint32(w), Height: uint32(h), Depth: 1},
	}
	vk.CmdCopyBufferToImage(cmd, staging, img, vk.ImageLayoutTransferDstOptimal, 1, []vk.BufferImageCopy{region})

	mw, mh := int32(w), int32(h)
	for i := uint32(1); i < mips; i++ {
		prev := vk.ImageSubresourceRange{AspectMask: colorAspect, BaseMipLevel: i - 1, LevelCount: 1, LayerCount: 1}
		// level i-1: transfer-dst -> transfer-src (blit source)
		barrier(cmd, img, prev, vk.ImageLayoutTransferDstOptimal, vk.ImageLayoutTransferSrcOptimal,
			vk.AccessFlags(vk.AccessTransferWriteBit), vk.AccessFlags(vk.AccessTransferReadBit),
			vk.PipelineStageFlags(vk.PipelineStageTransferBit), vk.PipelineStageFlags(vk.PipelineStageTransferBit))

		nw, nh := mw/2, mh/2
		if nw < 1 {
			nw = 1
		}
		if nh < 1 {
			nh = 1
		}
		blit := vk.ImageBlit{
			SrcSubresource: vk.ImageSubresourceLayers{AspectMask: colorAspect, MipLevel: i - 1, LayerCount: 1},
			SrcOffsets:     [2]vk.Offset3D{{X: 0, Y: 0, Z: 0}, {X: mw, Y: mh, Z: 1}},
			DstSubresource: vk.ImageSubresourceLayers{AspectMask: colorAspect, MipLevel: i, LayerCount: 1},
			DstOffsets:     [2]vk.Offset3D{{X: 0, Y: 0, Z: 0}, {X: nw, Y: nh, Z: 1}},
		}
		vk.CmdBlitImage(cmd, img, vk.ImageLayoutTransferSrcOptimal, img, vk.ImageLayoutTransferDstOptimal, 1, []vk.ImageBlit{blit}, vk.FilterLinear)

		// level i-1 is done — hand it to the fragment shader
		barrier(cmd, img, prev, vk.ImageLayoutTransferSrcOptimal, vk.ImageLayoutShaderReadOnlyOptimal,
			vk.AccessFlags(vk.AccessTransferReadBit), vk.AccessFlags(vk.AccessShaderReadBit),
			vk.PipelineStageFlags(vk.PipelineStageTransferBit), vk.PipelineStageFlags(vk.PipelineStageFragmentShaderBit))
		mw, mh = nw, nh
	}
	// the last level never became a blit source — transition it from dst
	last := vk.ImageSubresourceRange{AspectMask: colorAspect, BaseMipLevel: mips - 1, LevelCount: 1, LayerCount: 1}
	barrier(cmd, img, last, vk.ImageLayoutTransferDstOptimal, vk.ImageLayoutShaderReadOnlyOptimal,
		vk.AccessFlags(vk.AccessTransferWriteBit), vk.AccessFlags(vk.AccessShaderReadBit),
		vk.PipelineStageFlags(vk.PipelineStageTransferBit), vk.PipelineStageFlags(vk.PipelineStageFragmentShaderBit))
}

// uploadImagePixels stages pix into a single-mip image and leaves it in
// finalLayout. Used for the fade LUTs.
func (r *Renderer) uploadImagePixels(img vk.Image, w, h int, pix []byte, finalLayout vk.ImageLayout) error {
	buf, mem, mapped, err := r.wHostBuffer(len(pix), vk.BufferUsageFlags(vk.BufferUsageTransferSrcBit))
	if err != nil {
		return err
	}
	vk.Memcopy(mapped, pix)
	defer func() {
		vk.UnmapMemory(r.device, mem)
		vk.DestroyBuffer(r.device, buf, nil)
		vk.FreeMemory(r.device, mem, nil)
	}()

	return r.submitOneShot(func(cmd vk.CommandBuffer) {
		rng := vk.ImageSubresourceRange{AspectMask: vk.ImageAspectFlags(vk.ImageAspectColorBit), LevelCount: 1, LayerCount: 1}
		barrier(cmd, img, rng, vk.ImageLayoutUndefined, vk.ImageLayoutTransferDstOptimal,
			0, vk.AccessFlags(vk.AccessTransferWriteBit),
			vk.PipelineStageFlags(vk.PipelineStageTopOfPipeBit), vk.PipelineStageFlags(vk.PipelineStageTransferBit))
		region := vk.BufferImageCopy{
			ImageSubresource: vk.ImageSubresourceLayers{AspectMask: vk.ImageAspectFlags(vk.ImageAspectColorBit), LayerCount: 1},
			ImageExtent:      vk.Extent3D{Width: uint32(w), Height: uint32(h), Depth: 1},
		}
		vk.CmdCopyBufferToImage(cmd, buf, img, vk.ImageLayoutTransferDstOptimal, 1, []vk.BufferImageCopy{region})
		barrier(cmd, img, rng, vk.ImageLayoutTransferDstOptimal, finalLayout,
			vk.AccessFlags(vk.AccessTransferWriteBit), vk.AccessFlags(vk.AccessShaderReadBit),
			vk.PipelineStageFlags(vk.PipelineStageTransferBit), vk.PipelineStageFlags(vk.PipelineStageFragmentShaderBit))
	})
}

// allocTexSet allocates and writes one set-0 descriptor for a resident
// texture: binding 0 = the base texture, binding 1 = its brightmap mask
// (the shared black dummy view when it has none). Both use tr's
// anisotropic sampler.
func (r *Renderer) allocTexSet(tr *texResidency, baseView, brightView vk.ImageView) (vk.DescriptorSet, error) {
	s, err := r.allocDescriptorSet(tr.texPool, tr.dsl0, fmt.Sprintf("texture (%d resident)", len(tr.texCache)))
	if err != nil {
		return nil, err
	}
	vk.UpdateDescriptorSets(r.device, 2, []vk.WriteDescriptorSet{
		imageWrite(s, 0, tr.texSampler, baseView),
		imageWrite(s, 1, tr.texSampler, brightView),
	}, 0, nil)
	return s, nil
}
