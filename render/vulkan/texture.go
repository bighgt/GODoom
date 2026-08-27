package vulkan

import (
	"fmt"
	"unsafe"

	vk "github.com/goki/vulkan"
)

// createTextureResources allocates the GPU-side image the CPU-rendered
// raster.Renderer frame is uploaded into every frame (srcW x srcH,
// R8G8B8A8_UNORM, sampled with nearest filtering — see pipeline.go for why
// nearest: it's what keeps the classic chunky-pixel Doom look when this
// small internal frame is upscaled to the real window size), the host
// -visible staging buffer frames are copied through, and the sampler the
// fragment shader reads the image with.
func (r *Renderer) createTextureResources(srcW, srcH int) error {
	r.srcWidth, r.srcHeight = srcW, srcH

	imgInfo := vk.ImageCreateInfo{
		SType:         vk.StructureTypeImageCreateInfo,
		ImageType:     vk.ImageType2d,
		Format:        vk.FormatR8g8b8a8Unorm,
		Extent:        vk.Extent3D{Width: uint32(srcW), Height: uint32(srcH), Depth: 1},
		MipLevels:     1,
		ArrayLayers:   1,
		Samples:       vk.SampleCount1Bit,
		Tiling:        vk.ImageTilingOptimal,
		Usage:         vk.ImageUsageFlags(vk.ImageUsageTransferDstBit) | vk.ImageUsageFlags(vk.ImageUsageSampledBit),
		SharingMode:   vk.SharingModeExclusive,
		InitialLayout: vk.ImageLayoutUndefined,
	}
	var image vk.Image
	if res := vk.CreateImage(r.device, &imgInfo, nil, &image); res != vk.Success {
		return fmt.Errorf("vulkan: create frame texture image: %s", vk.Error(res))
	}
	r.textureImage = image
	r.textureLayout = vk.ImageLayoutUndefined

	var memReq vk.MemoryRequirements
	vk.GetImageMemoryRequirements(r.device, image, &memReq)
	memReq.Deref()
	memType, err := r.findMemoryType(memReq.MemoryTypeBits, vk.MemoryPropertyFlags(vk.MemoryPropertyDeviceLocalBit))
	if err != nil {
		return fmt.Errorf("vulkan: frame texture memory: %w", err)
	}
	allocInfo := vk.MemoryAllocateInfo{SType: vk.StructureTypeMemoryAllocateInfo, AllocationSize: memReq.Size, MemoryTypeIndex: memType}
	var mem vk.DeviceMemory
	if res := vk.AllocateMemory(r.device, &allocInfo, nil, &mem); res != vk.Success {
		return fmt.Errorf("vulkan: allocate frame texture memory: %s", vk.Error(res))
	}
	r.textureMemory = mem
	if res := vk.BindImageMemory(r.device, image, mem, 0); res != vk.Success {
		return fmt.Errorf("vulkan: bind frame texture memory: %s", vk.Error(res))
	}

	viewInfo := vk.ImageViewCreateInfo{
		SType:    vk.StructureTypeImageViewCreateInfo,
		Image:    image,
		ViewType: vk.ImageViewType2d,
		Format:   vk.FormatR8g8b8a8Unorm,
		Components: vk.ComponentMapping{
			R: vk.ComponentSwizzleIdentity, G: vk.ComponentSwizzleIdentity,
			B: vk.ComponentSwizzleIdentity, A: vk.ComponentSwizzleIdentity,
		},
		SubresourceRange: vk.ImageSubresourceRange{
			AspectMask: vk.ImageAspectFlags(vk.ImageAspectColorBit), LevelCount: 1, LayerCount: 1,
		},
	}
	var view vk.ImageView
	if res := vk.CreateImageView(r.device, &viewInfo, nil, &view); res != vk.Success {
		return fmt.Errorf("vulkan: create frame texture view: %s", vk.Error(res))
	}
	r.textureView = view

	samplerInfo := vk.SamplerCreateInfo{
		SType: vk.StructureTypeSamplerCreateInfo,
		// Linear, not nearest: the GPU sampler bilinearly filters the
		// internal 320x200 frame (3D view and HUD text alike, both drawn
		// into the same raster.Renderer.Pix buffer — see raster/hud.go) as
		// it's upscaled to the window's real size, which is the "hardware
		// text smoothing" this project's design settled on: one sampler
		// smooths everything, rather than a second GPU-side text pipeline
		// with its own filtering. See game_design.txt section 7.
		MagFilter:    vk.FilterLinear,
		MinFilter:    vk.FilterLinear,
		MipmapMode:   vk.SamplerMipmapModeNearest,
		AddressModeU: vk.SamplerAddressModeClampToEdge,
		AddressModeV: vk.SamplerAddressModeClampToEdge,
		AddressModeW: vk.SamplerAddressModeClampToEdge,
		MaxLod:       1,
	}
	var sampler vk.Sampler
	if res := vk.CreateSampler(r.device, &samplerInfo, nil, &sampler); res != vk.Success {
		return fmt.Errorf("vulkan: create frame texture sampler: %s", vk.Error(res))
	}
	r.textureSampler = sampler

	return r.createStagingBuffer(srcW * srcH * 4)
}

func (r *Renderer) createStagingBuffer(size int) error {
	bufInfo := vk.BufferCreateInfo{
		SType:       vk.StructureTypeBufferCreateInfo,
		Size:        vk.DeviceSize(size),
		Usage:       vk.BufferUsageFlags(vk.BufferUsageTransferSrcBit),
		SharingMode: vk.SharingModeExclusive,
	}
	var buf vk.Buffer
	if res := vk.CreateBuffer(r.device, &bufInfo, nil, &buf); res != vk.Success {
		return fmt.Errorf("vulkan: create staging buffer: %s", vk.Error(res))
	}
	r.stagingBuffer = buf

	var memReq vk.MemoryRequirements
	vk.GetBufferMemoryRequirements(r.device, buf, &memReq)
	memReq.Deref()
	memType, err := r.findMemoryType(memReq.MemoryTypeBits,
		vk.MemoryPropertyFlags(vk.MemoryPropertyHostVisibleBit)|vk.MemoryPropertyFlags(vk.MemoryPropertyHostCoherentBit))
	if err != nil {
		return fmt.Errorf("vulkan: staging buffer memory: %w", err)
	}
	allocInfo := vk.MemoryAllocateInfo{SType: vk.StructureTypeMemoryAllocateInfo, AllocationSize: memReq.Size, MemoryTypeIndex: memType}
	var mem vk.DeviceMemory
	if res := vk.AllocateMemory(r.device, &allocInfo, nil, &mem); res != vk.Success {
		return fmt.Errorf("vulkan: allocate staging buffer memory: %s", vk.Error(res))
	}
	r.stagingMemory = mem
	if res := vk.BindBufferMemory(r.device, buf, mem, 0); res != vk.Success {
		return fmt.Errorf("vulkan: bind staging buffer memory: %s", vk.Error(res))
	}

	// Mapped once and kept for the renderer's lifetime — every frame just
	// memcpy's into it (safe because DrawFrame waits on this frame slot's
	// fence, meaning any GPU read of the previous contents has finished,
	// before writing new pixels; see maxFramesInFlight's doc comment).
	var mapped unsafe.Pointer
	if res := vk.MapMemory(r.device, mem, 0, vk.DeviceSize(size), 0, &mapped); res != vk.Success {
		return fmt.Errorf("vulkan: map staging buffer: %s", vk.Error(res))
	}
	r.stagingMapped = mapped
	r.stagingSize = size
	return nil
}

func (r *Renderer) findMemoryType(typeBits uint32, properties vk.MemoryPropertyFlags) (uint32, error) {
	var props vk.PhysicalDeviceMemoryProperties
	vk.GetPhysicalDeviceMemoryProperties(r.physicalDevice, &props)
	props.Deref()
	for i := uint32(0); i < props.MemoryTypeCount; i++ {
		mt := props.MemoryTypes[i]
		mt.Deref()
		if typeBits&(1<<i) != 0 && mt.PropertyFlags&properties == properties {
			return i, nil
		}
	}
	return 0, fmt.Errorf("no suitable GPU memory type for requirements 0x%x / flags 0x%x", typeBits, properties)
}

// uploadFrame copies pix into the mapped staging buffer. Call after this
// frame's in-flight fence has been waited on (DrawFrame does this before
// recording), and before recordCommandBuffer, which issues the actual
// buffer->image copy on the GPU.
func (r *Renderer) uploadFrame(pix []byte) error {
	if len(pix) != r.stagingSize {
		return fmt.Errorf("vulkan: frame is %d bytes, expected %d (%dx%d RGBA8)", len(pix), r.stagingSize, r.srcWidth, r.srcHeight)
	}
	vk.Memcopy(r.stagingMapped, pix)
	return nil
}

func (r *Renderer) destroyTextureResources() {
	if r.textureSampler != nil {
		vk.DestroySampler(r.device, r.textureSampler, nil)
		r.textureSampler = nil
	}
	if r.textureView != nil {
		vk.DestroyImageView(r.device, r.textureView, nil)
		r.textureView = nil
	}
	if r.textureImage != nil {
		vk.DestroyImage(r.device, r.textureImage, nil)
		r.textureImage = nil
	}
	if r.textureMemory != nil {
		vk.FreeMemory(r.device, r.textureMemory, nil)
		r.textureMemory = nil
	}
	if r.stagingMapped != nil {
		vk.UnmapMemory(r.device, r.stagingMemory)
		r.stagingMapped = nil
	}
	if r.stagingBuffer != nil {
		vk.DestroyBuffer(r.device, r.stagingBuffer, nil)
		r.stagingBuffer = nil
	}
	if r.stagingMemory != nil {
		vk.FreeMemory(r.device, r.stagingMemory, nil)
		r.stagingMemory = nil
	}
}
