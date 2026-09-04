package vulkan

import (
	"errors"
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
	extent := r.chooseExtent(caps)

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
	// FIFO (classic vsync) is the only mode every Vulkan implementation must
	// support, and it's what the vsync setting asks for.
	if r.vsync {
		return vk.PresentModeFifo
	}
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

func (r *Renderer) chooseExtent(caps vk.SurfaceCapabilities) vk.Extent2D {
	if caps.CurrentExtent.Width != vk.MaxUint32 {
		return caps.CurrentExtent
	}
	// vk.MaxUint32 in CurrentExtent.Width signals "the surface takes
	// whatever the swapchain asks for" — use the window's current
	// framebuffer size, clamped to what the surface actually supports.
	w, h := r.win.FramebufferSize()
	return vk.Extent2D{
		Width:  clampU32(uint32(w), caps.MinImageExtent.Width, caps.MaxImageExtent.Width),
		Height: clampU32(uint32(h), caps.MinImageExtent.Height, caps.MaxImageExtent.Height),
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

// destroyResizableResources releases the swapchain and everything sized to
// its image count/extent: image views, framebuffers, the per-image command
// buffers, and the per-image renderFinished semaphores. It deliberately
// leaves the render pass and command pool alone — the render pass's only
// input, the surface format, is stable across a resize, and a render pass
// with an unchanged attachment layout stays compatible with the existing
// pipeline. Shared by Destroy and recreateSwapchain.
func (r *Renderer) destroyResizableResources() {
	for _, s := range r.renderFinished {
		vk.DestroySemaphore(r.device, s, nil)
	}
	r.renderFinished = nil

	if len(r.commandBuffers) > 0 {
		vk.FreeCommandBuffers(r.device, r.commandPool, uint32(len(r.commandBuffers)), r.commandBuffers)
		r.commandBuffers = nil
	}

	for _, fb := range r.framebuffers {
		vk.DestroyFramebuffer(r.device, fb, nil)
	}
	r.framebuffers = nil

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

// errSwapchainZeroExtent is returned by recreateSwapchain while the window
// is minimized (0x0 client area): swapchain creation can't succeed against
// a zero extent, so DrawFrame just skips frames until the window is
// restored and a real size is available again.
var errSwapchainZeroExtent = errors.New("vulkan: swapchain extent is zero (window minimized)")

// recreateSwapchain rebuilds the swapchain and everything sized to it after
// a window resize / monitor change makes the surface out of date (see
// DrawFrame, which calls this when acquire or present reports
// ErrorOutOfDate/Suboptimal). The caller must not have a frame in flight;
// this waits for the device to go idle first.
func (r *Renderer) recreateSwapchain() error {
	if w, h := r.win.FramebufferSize(); w == 0 || h == 0 {
		return errSwapchainZeroExtent
	}

	vk.DeviceWaitIdle(r.device)
	r.destroyResizableResources()

	steps := []func() error{
		r.createSwapchain,
		r.createImageViews,
		r.createFramebuffers,
		r.createCommandBuffers,
		r.createPerImageSemaphores,
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return err
		}
	}
	r.currentFrame = 0
	return nil
}

// createPerImageSemaphores allocates the renderFinished semaphores — one
// per swapchain image, not per frame-in-flight slot (see createSyncObjects
// for why). Split out so recreateSwapchain can rebuild exactly these if the
// swapchain's image count changes.
func (r *Renderer) createPerImageSemaphores() error {
	semInfo := vk.SemaphoreCreateInfo{SType: vk.StructureTypeSemaphoreCreateInfo}
	r.renderFinished = make([]vk.Semaphore, len(r.swapchainImages))
	for i := range r.renderFinished {
		if res := vk.CreateSemaphore(r.device, &semInfo, nil, &r.renderFinished[i]); res != vk.Success {
			return fmt.Errorf("vulkan: create render-finished semaphore: %s", vk.Error(res))
		}
	}
	// Per-image "last frame that used this image" fence tracking — starts
	// empty; DrawFrame fills it as images are first used.
	r.imagesInFlight = make([]vk.Fence, len(r.swapchainImages))
	return nil
}
