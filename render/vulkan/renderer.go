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

// maxFramesInFlight is 2: the CPU builds and uploads frame N+1 while the
// GPU is still copying/blitting/presenting frame N, instead of the whole
// pipeline running strictly one frame at a time. Making that safe needs
// one host-visible staging buffer per in-flight slot (see
// createStagingBuffer) so the CPU's memcpy for the next frame never lands
// on a buffer the GPU is still reading; the frame texture stays single,
// with the record-time barrier ordering the next copy after the previous
// blit's sampling (see recordCommandBuffer). imageAvailable/inFlight are
// per-slot; renderFinished is per swapchain image (see createSyncObjects).
const maxFramesInFlight = 2

// Renderer is the Vulkan backend. It satisfies render.Renderer.
type Renderer struct {
	win   *window.Window
	debug bool
	vsync bool // prefer FIFO (tear-free) over mailbox when choosing a present mode

	// lit selects the pipeline built by New: false = the vanilla blit (the
	// CPU already shaded the frame, we just upscale/present it); true = the
	// enhanced deferred-lighting path in lit.go (G-buffer -> GPU lighting ->
	// bloom -> tonemap). The two are mutually exclusive — DrawFrame errors
	// in lit mode and DrawFrameLit errors otherwise — since the engine
	// knows at startup which one config asked for.
	lit    bool
	litRes *litResources

	// hw selects the hardware (GPU-geometry) path — the triangle list
	// render/worldgeo builds from the BSP walk, rasterised on the GPU (see
	// world.go). Built by NewWorld instead of New; mutually exclusive with
	// lit / vanilla. DrawWorld errors unless this is set, and DrawFrame /
	// DrawFrameLit error when it is.
	hw    bool
	wr    *worldResources
	// maxAnisotropy is the device's advertised MaxSamplerAnisotropy when the
	// samplerAnisotropy feature was enabled (hw path), else 0.
	maxAnisotropy float32

	// DeviceName/DeviceTypeName identify the physical GPU New selected
	// (set by pickPhysicalDevice) — read these after New returns to
	// confirm rendering is actually happening on a real GPU rather than a
	// software/CPU Vulkan implementation; see cmd/engine/main.go.
	DeviceName     string
	DeviceTypeName string
	// DeviceIsSoftware reports whether the selected device is a CPU/software
	// Vulkan implementation (VK_PHYSICAL_DEVICE_TYPE_CPU) rather than real
	// GPU hardware. Callers should check this field directly instead of
	// comparing DeviceTypeName against a string, since the latter is meant
	// for display and may be reworded independently of this check.
	DeviceIsSoftware bool

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
	// imagesInFlight[i] is the per-slot inFlight fence of the frame that
	// last submitted work for swapchain image i (nil if none yet). With
	// more than one frame in flight, acquiring image i doesn't imply the
	// slot that last used it has retired, so DrawFrame waits on this fence
	// before reusing image i's command buffer. Rebuilt with the per-image
	// semaphores on swapchain (re)creation.
	imagesInFlight []vk.Fence
	currentFrame   int

	// needRecreate is set when acquire/present reports the swapchain is out
	// of date or suboptimal (a window resize, a monitor change); DrawFrame
	// rebuilds the swapchain at the start of the next call rather than
	// mid-frame. See recreateSwapchain.
	needRecreate bool

	// The uploaded-frame texture the blit pipeline samples.
	srcWidth, srcHeight int
	textureImage        vk.Image
	textureMemory       vk.DeviceMemory
	textureView         vk.ImageView
	textureSampler      vk.Sampler
	textureLayout       vk.ImageLayout

	// One staging buffer per frame-in-flight slot (indexed by currentFrame),
	// each mapped for the renderer's lifetime.
	stagingBuffers []vk.Buffer
	stagingMemory  []vk.DeviceMemory
	stagingMapped  []unsafe.Pointer
	stagingSize    int

	descriptorSetLayout vk.DescriptorSetLayout
	descriptorPool      vk.DescriptorPool
	descriptorSet       vk.DescriptorSet

	pipelineLayout vk.PipelineLayout
	pipeline       vk.Pipeline
}

// New stands up the full Vulkan pipeline targeting win: instance, surface,
// physical/logical device, swapchain, render pass, framebuffers, command
// pool/buffers, sync objects, the frame-upload texture, and the blit
// pipeline that presents it. srcW/srcH is the size of the RGBA frames
// DrawFrame will be given (the CPU render resolution). vsync prefers the
// tear-free FIFO present mode over mailbox. debug enables
// VK_LAYER_KHRONOS_validation when it's present on the system.
func New(win *window.Window, srcW, srcH int, vsync, debug, lit bool) (*Renderer, error) {
	r := &Renderer{win: win, vsync: vsync, debug: debug, lit: lit}

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
	}
	if lit {
		// The enhanced path: a G-buffer upload set, offscreen HDR + bloom
		// targets, and the lighting/bright/blur/composite pipelines. The
		// composite pass reuses r.renderPass / r.framebuffers (it also
		// targets the swapchain), so those steps above still run.
		steps = append(steps, func() error { return r.createLitResources(srcW, srcH) })
	} else {
		steps = append(steps,
			func() error { return r.createTextureResources(srcW, srcH) },
			r.createDescriptors,
			r.createPipeline,
		)
	}
	for _, step := range steps {
		if err := step(); err != nil {
			r.Destroy()
			return nil, err
		}
	}

	return r, nil
}

// waitIdle blocks until the GPU has finished all outstanding work — done
// before teardown so no resource is freed while a frame still references it.
func (r *Renderer) waitIdle() {
	if r.device != nil {
		vk.DeviceWaitIdle(r.device)
	}
}

// Destroy releases every Vulkan (and GLFW-surface) resource this renderer
// owns, in reverse creation order. Safe to call on a partially-initialized
// Renderer (e.g. if New failed partway through).
func (r *Renderer) Destroy() {
	r.waitIdle()

	r.destroyWorldResources()
	r.destroyLitResources()
	r.destroyPipeline()
	r.destroyDescriptors()
	r.destroyTextureResources()

	// Command buffers, per-image (renderFinished) semaphores, framebuffers,
	// image views, and the swapchain itself — everything sized to the
	// swapchain, shared with recreateSwapchain's teardown.
	r.destroyResizableResources()

	if r.renderPass != nil {
		vk.DestroyRenderPass(r.device, r.renderPass, nil)
		r.renderPass = nil
	}

	for _, s := range r.imageAvailable {
		vk.DestroySemaphore(r.device, s, nil)
	}
	for _, f := range r.inFlight {
		vk.DestroyFence(r.device, f, nil)
	}
	r.imageAvailable, r.inFlight = nil, nil

	if r.commandPool != nil {
		vk.DestroyCommandPool(r.device, r.commandPool, nil)
		r.commandPool = nil
	}

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
