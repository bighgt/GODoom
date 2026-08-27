// Package vulkan is the Vulkan implementation of render.Renderer. It owns
// the instance, device, swapchain, and a small "blit" pipeline: every
// frame, it uploads the RGBA image package raster rendered on the CPU into
// a GPU texture and draws a single fullscreen triangle sampling it with
// nearest filtering. That's the entire GPU-rendering stage — see
// game_design.txt section 7 for why this project puts the Doom-faithful
// BSP/wall/flat rendering on the CPU (matching the original engine's own
// architecture) and reserves Vulkan for what a modern GPU does best:
// getting the finished frame on screen, upscaled, every vsync.
package vulkan

import (
	"fmt"
	"unsafe"

	"github.com/go-gl/glfw/v3.3/glfw"
	vk "github.com/goki/vulkan"

	"twopointfive/window"
)

// maxFramesInFlight is 1, not the usual double-buffered 2: the renderer's
// staging buffer and descriptor set are single, shared resources (there's
// exactly one small CPU-rendered frame to present, not a pool of vertex
// buffers), so keeping only one frame in flight is what makes "wait for
// this slot's fence, then safely overwrite the staging buffer" correct
// without extra bookkeeping. The CPU-side software rasterizer, not this
// serialization, is Phase 2's actual performance ceiling.
const maxFramesInFlight = 1

// Renderer is the Vulkan backend. It satisfies render.Renderer.
type Renderer struct {
	win   *window.Window
	debug bool

	instance       vk.Instance
	surface        vk.Surface
	physicalDevice vk.PhysicalDevice
	device         vk.Device

	graphicsFamily uint32
	presentFamily  uint32
	graphicsQueue  vk.Queue
	presentQueue   vk.Queue

	swapchain           vk.Swapchain
	swapchainImages     []vk.Image
	swapchainImageViews []vk.ImageView
	swapchainFormat     vk.Format
	swapchainExtent     vk.Extent2D

	renderPass   vk.RenderPass
	framebuffers []vk.Framebuffer

	commandPool    vk.CommandPool
	commandBuffers []vk.CommandBuffer

	imageAvailable []vk.Semaphore
	renderFinished []vk.Semaphore
	inFlight       []vk.Fence
	currentFrame   int

	// The uploaded-frame texture the blit pipeline samples.
	srcWidth, srcHeight int
	textureImage        vk.Image
	textureMemory       vk.DeviceMemory
	textureView         vk.ImageView
	textureSampler      vk.Sampler
	textureLayout       vk.ImageLayout

	stagingBuffer vk.Buffer
	stagingMemory vk.DeviceMemory
	stagingMapped unsafe.Pointer
	stagingSize   int

	descriptorSetLayout vk.DescriptorSetLayout
	descriptorPool      vk.DescriptorPool
	descriptorSet       vk.DescriptorSet

	pipelineLayout vk.PipelineLayout
	pipeline       vk.Pipeline
}

// New stands up the full Vulkan pipeline targeting win: instance, surface,
// physical/logical device, swapchain, render pass, framebuffers, command
// pool/buffers, sync objects, the frame-upload texture, and the blit
// pipeline that presents it. srcW/srcH must match the RGBA frames DrawFrame
// will be given (raster.InternalWidth/InternalHeight). debug enables
// VK_LAYER_KHRONOS_validation when it's present on the system.
func New(win *window.Window, srcW, srcH int, debug bool) (*Renderer, error) {
	r := &Renderer{win: win, debug: debug}

	steps := []func() error{
		r.createInstance,
		r.createSurface,
		r.pickPhysicalDevice,
		r.createLogicalDevice,
		r.createSwapchain,
		r.createImageViews,
		r.createRenderPass,
		r.createFramebuffers,
		r.createCommandPool,
		r.createCommandBuffers,
		r.createSyncObjects,
		func() error { return r.createTextureResources(srcW, srcH) },
		r.createDescriptors,
		r.createPipeline,
	}
	for _, step := range steps {
		if err := step(); err != nil {
			r.Destroy()
			return nil, err
		}
	}

	return r, nil
}

// WaitIdle blocks until the GPU has finished all outstanding work.
func (r *Renderer) WaitIdle() {
	if r.device != nil {
		vk.DeviceWaitIdle(r.device)
	}
}

// Destroy releases every Vulkan (and GLFW-surface) resource this renderer
// owns, in reverse creation order. Safe to call on a partially-initialized
// Renderer (e.g. if New failed partway through).
func (r *Renderer) Destroy() {
	r.WaitIdle()

	r.destroyPipeline()
	r.destroyDescriptors()
	r.destroyTextureResources()

	for _, s := range r.imageAvailable {
		vk.DestroySemaphore(r.device, s, nil)
	}
	for _, s := range r.renderFinished {
		vk.DestroySemaphore(r.device, s, nil)
	}
	for _, f := range r.inFlight {
		vk.DestroyFence(r.device, f, nil)
	}
	r.imageAvailable, r.renderFinished, r.inFlight = nil, nil, nil

	if r.commandPool != nil {
		vk.DestroyCommandPool(r.device, r.commandPool, nil)
		r.commandPool = nil
	}

	r.destroySwapchain()

	if r.device != nil {
		vk.DestroyDevice(r.device, nil)
		r.device = nil
	}
	if r.surface != vk.NullSurface && r.instance != nil {
		vk.DestroySurface(r.instance, r.surface, nil)
		r.surface = vk.NullSurface
	}
	if r.instance != nil {
		vk.DestroyInstance(r.instance, nil)
		r.instance = nil
	}
}

func (r *Renderer) createSurface() error {
	if !glfw.VulkanSupported() {
		return fmt.Errorf("vulkan: GLFW reports no Vulkan loader on this system")
	}
	surfacePtr, err := r.win.Handle().CreateWindowSurface(r.instance, nil)
	if err != nil {
		return fmt.Errorf("vulkan: create window surface: %w", err)
	}
	r.surface = vk.SurfaceFromPointer(surfacePtr)
	return nil
}
