package vulkan

import (
	"fmt"

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

// syncWorldTextures uploads any bitmap in g (wall/flat textures + the sky)
// that isn't resident yet. Drains the device first, but only when there's
// actually something new — so it's free once a level is warm.
func (r *Renderer) syncWorldTextures(g *worldgeo.Geometry) error {
	wr := r.wr
	missing := func(t *assets.RGBA) bool { return t != nil && wr.texCache[t] == nil }

	pending := missing(g.Sky)
	if !pending {
		for _, t := range g.Textures {
			if missing(t) {
				pending = true
				break
			}
		}
	}
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
		if err := r.uploadWorldTexture(t, bright); err != nil {
			return err
		}
	}
	if missing(g.Sky) {
		if err := r.uploadWorldTexture(g.Sky, nil); err != nil {
			return err
		}
	}
	return nil
}

// uploadWorldTexture makes t resident: a device-local RGBA8 image with a
// full mip chain and its set-0 descriptor, cached by the bitmap pointer.
func (r *Renderer) uploadWorldTexture(t, bright *assets.RGBA) error {
	wr := r.wr

	img, mips, err := r.makeResidentImage(t)
	if err != nil {
		return err
	}

	// The brightmap (if any) becomes its own resident image; its view feeds
	// set-0 binding 1. No brightmap -> the shared black dummy, so world.frag
	// can sample binding 1 unconditionally.
	var bimg litImage
	brightView := wr.dummyTex.view
	if bright != nil {
		bimg, _, err = r.makeResidentImage(bright)
		if err != nil {
			freeLitImage(r.device, img)
			return err
		}
		brightView = bimg.view
	}

	set, err := r.allocTexSet(img.view, brightView)
	if err != nil {
		freeLitImage(r.device, img)
		freeLitImage(r.device, bimg)
		return err
	}
	wr.texCache[t] = &worldTex{img: img, bright: bimg, set: set, mips: mips}
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
// (the shared black dummy view when it has none). Both use the shared
// anisotropic sampler.
func (r *Renderer) allocTexSet(baseView, brightView vk.ImageView) (vk.DescriptorSet, error) {
	wr := r.wr
	info := vk.DescriptorSetAllocateInfo{
		SType:              vk.StructureTypeDescriptorSetAllocateInfo,
		DescriptorPool:     wr.texPool,
		DescriptorSetCount: 1,
		PSetLayouts:        []vk.DescriptorSetLayout{wr.dsl0},
	}
	var s vk.DescriptorSet
	if res := vk.AllocateDescriptorSets(r.device, &info, &s); res != vk.Success {
		return nil, fmt.Errorf("vulkan: world texture descriptor set (%d resident): %s", len(wr.texCache), vk.Error(res))
	}
	imgW := func(binding uint32, view vk.ImageView) vk.WriteDescriptorSet {
		return vk.WriteDescriptorSet{
			SType:           vk.StructureTypeWriteDescriptorSet,
			DstSet:          s,
			DstBinding:      binding,
			DescriptorCount: 1,
			DescriptorType:  vk.DescriptorTypeCombinedImageSampler,
			PImageInfo: []vk.DescriptorImageInfo{{
				Sampler: wr.texSampler, ImageView: view, ImageLayout: vk.ImageLayoutShaderReadOnlyOptimal,
			}},
		}
	}
	vk.UpdateDescriptorSets(r.device, 2, []vk.WriteDescriptorSet{
		imgW(0, baseView), imgW(1, brightView),
	}, 0, nil)
	return s, nil
}
