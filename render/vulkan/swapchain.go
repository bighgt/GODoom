package vulkan

import (
	"fmt"

	vk "github.com/goki/vulkan"
)

func (r *Renderer) createSwapchain() error {
	var caps vk.SurfaceCapabilities
	if res := vk.GetPhysicalDeviceSurfaceCapabilities(r.physicalDevice, r.surface, &caps); res != vk.Success {
		return fmt.Errorf("vulkan: get surface capabilities: %s", vk.Error(res))
	}
	caps.Deref()
	caps.CurrentExtent.Deref()
	caps.MinImageExtent.Deref()
	caps.MaxImageExtent.Deref()

	format, err := r.chooseSurfaceFormat()
	if err != nil {
		return err
	}
	presentMode := r.choosePresentMode()
	extent := chooseExtent(caps)

	imageCount := caps.MinImageCount + 1
	if caps.MaxImageCount > 0 && imageCount > caps.MaxImageCount {
		imageCount = caps.MaxImageCount
	}

	createInfo := vk.SwapchainCreateInfo{
		SType:            vk.StructureTypeSwapchainCreateInfo,
		Surface:          r.surface,
		MinImageCount:    imageCount,
		ImageFormat:      format.Format,
		ImageColorSpace:  format.ColorSpace,
		ImageExtent:      extent,
		ImageArrayLayers: 1,
		ImageUsage:       vk.ImageUsageFlags(vk.ImageUsageColorAttachmentBit),
		PreTransform:     caps.CurrentTransform,
		CompositeAlpha:   vk.CompositeAlphaFlagBits(vk.CompositeAlphaOpaqueBit),
		PresentMode:      presentMode,
		Clipped:          vk.True,
	}

	if r.graphicsFamily != r.presentFamily {
		createInfo.ImageSharingMode = vk.SharingModeConcurrent
		createInfo.QueueFamilyIndexCount = 2
		createInfo.PQueueFamilyIndices = []uint32{r.graphicsFamily, r.presentFamily}
	} else {
		createInfo.ImageSharingMode = vk.SharingModeExclusive
	}

	var swapchain vk.Swapchain
	if res := vk.CreateSwapchain(r.device, &createInfo, nil, &swapchain); res != vk.Success {
		return fmt.Errorf("vulkan: create swapchain: %s", vk.Error(res))
	}
	r.swapchain = swapchain
	r.swapchainFormat = format.Format
	r.swapchainExtent = extent

	var actualCount uint32
	vk.GetSwapchainImages(r.device, swapchain, &actualCount, nil)
	images := make([]vk.Image, actualCount)
	vk.GetSwapchainImages(r.device, swapchain, &actualCount, images)
	r.swapchainImages = images

	return nil
}

func (r *Renderer) chooseSurfaceFormat() (vk.SurfaceFormat, error) {
	var count uint32
	vk.GetPhysicalDeviceSurfaceFormats(r.physicalDevice, r.surface, &count, nil)
	if count == 0 {
		return vk.SurfaceFormat{}, fmt.Errorf("vulkan: surface supports no formats")
	}
	formats := make([]vk.SurfaceFormat, count)
	vk.GetPhysicalDeviceSurfaceFormats(r.physicalDevice, r.surface, &count, formats)

	for i := range formats {
		formats[i].Deref()
		if formats[i].Format == vk.FormatB8g8r8a8Srgb && formats[i].ColorSpace == vk.ColorSpaceSrgbNonlinear {
			return formats[i], nil
		}
	}
	return formats[0], nil
}

func (r *Renderer) choosePresentMode() vk.PresentMode {
	var count uint32
	vk.GetPhysicalDeviceSurfacePresentModes(r.physicalDevice, r.surface, &count, nil)
	if count == 0 {
		return vk.PresentModeFifo
	}
	modes := make([]vk.PresentMode, count)
	vk.GetPhysicalDeviceSurfacePresentModes(r.physicalDevice, r.surface, &count, modes)

	for _, m := range modes {
		if m == vk.PresentModeMailbox {
			return m
		}
	}
	// FIFO (classic vsync) is the only mode every Vulkan implementation must support.
	return vk.PresentModeFifo
}

func clampU32(v, lo, hi uint32) uint32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func chooseExtent(caps vk.SurfaceCapabilities) vk.Extent2D {
	if caps.CurrentExtent.Width != vk.MaxUint32 {
		return caps.CurrentExtent
	}
	// vk.MaxUint32 in CurrentExtent.Width signals "pick anything within
	// bounds"; Phase 1 has no live framebuffer-size callback yet, so fall
	// back to the minimum supported extent.
	return vk.Extent2D{
		Width:  clampU32(caps.MinImageExtent.Width, caps.MinImageExtent.Width, caps.MaxImageExtent.Width),
		Height: clampU32(caps.MinImageExtent.Height, caps.MinImageExtent.Height, caps.MaxImageExtent.Height),
	}
}

func (r *Renderer) createImageViews() error {
	r.swapchainImageViews = make([]vk.ImageView, len(r.swapchainImages))
	for i, img := range r.swapchainImages {
		createInfo := vk.ImageViewCreateInfo{
			SType:    vk.StructureTypeImageViewCreateInfo,
			Image:    img,
			ViewType: vk.ImageViewType2d,
			Format:   r.swapchainFormat,
			Components: vk.ComponentMapping{
				R: vk.ComponentSwizzleIdentity,
				G: vk.ComponentSwizzleIdentity,
				B: vk.ComponentSwizzleIdentity,
				A: vk.ComponentSwizzleIdentity,
			},
			SubresourceRange: vk.ImageSubresourceRange{
				AspectMask:     vk.ImageAspectFlags(vk.ImageAspectColorBit),
				BaseMipLevel:   0,
				LevelCount:     1,
				BaseArrayLayer: 0,
				LayerCount:     1,
			},
		}
		var view vk.ImageView
		if res := vk.CreateImageView(r.device, &createInfo, nil, &view); res != vk.Success {
			return fmt.Errorf("vulkan: create image view %d: %s", i, vk.Error(res))
		}
		r.swapchainImageViews[i] = view
	}
	return nil
}

// destroySwapchain releases the swapchain and everything that depends on its
// image count/extent (image views, framebuffers) — but not the render pass
// or command pool, which don't need to change. Used by Destroy, and will be
// reused by a future recreateSwapchain on window resize / ErrorOutOfDate.
func (r *Renderer) destroySwapchain() {
	for _, fb := range r.framebuffers {
		vk.DestroyFramebuffer(r.device, fb, nil)
	}
	r.framebuffers = nil

	if r.renderPass != nil {
		vk.DestroyRenderPass(r.device, r.renderPass, nil)
		r.renderPass = nil
	}

	for _, v := range r.swapchainImageViews {
		vk.DestroyImageView(r.device, v, nil)
	}
	r.swapchainImageViews = nil
	r.swapchainImages = nil

	if r.swapchain != nil {
		vk.DestroySwapchain(r.device, r.swapchain, nil)
		r.swapchain = nil
	}
}
