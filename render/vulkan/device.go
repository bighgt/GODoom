package vulkan

import (
	"fmt"

	vk "github.com/goki/vulkan"
)

const swapchainExtensionName = "VK_KHR_swapchain"

func (r *Renderer) pickPhysicalDevice() error {
	var count uint32
	if res := vk.EnumeratePhysicalDevices(r.instance, &count, nil); res != vk.Success {
		return fmt.Errorf("vulkan: enumerate physical devices: %s", vk.Error(res))
	}
	if count == 0 {
		return fmt.Errorf("vulkan: no GPU on this system supports Vulkan")
	}
	devices := make([]vk.PhysicalDevice, count)
	if res := vk.EnumeratePhysicalDevices(r.instance, &count, devices); res != vk.Success {
		return fmt.Errorf("vulkan: enumerate physical devices: %s", vk.Error(res))
	}

	var (
		best      vk.PhysicalDevice
		bestScore = -1
		bestGFam  uint32
		bestPFam  uint32
		bestProps vk.PhysicalDeviceProperties
	)
	for _, dev := range devices {
		gfam, pfam, ok := r.findQueueFamilies(dev)
		if !ok || !deviceSupportsSwapchain(dev) {
			continue
		}

		var props vk.PhysicalDeviceProperties
		vk.GetPhysicalDeviceProperties(dev, &props)
		props.Deref()

		score := 1
		if props.DeviceType == vk.PhysicalDeviceTypeDiscreteGpu {
			score = 2
		}
		if score > bestScore {
			best, bestScore, bestGFam, bestPFam, bestProps = dev, score, gfam, pfam, props
		}
	}

	if bestScore < 0 {
		return fmt.Errorf("vulkan: no GPU with graphics+present queue support and swapchain support was found")
	}

	r.physicalDevice = best
	r.graphicsFamily = bestGFam
	r.presentFamily = bestPFam
	// Exposed so the caller can log/assert real GPU rendering is in effect
	// rather than a CPU fallback (VK_PHYSICAL_DEVICE_TYPE_CPU) — see
	// DeviceName/DeviceTypeName and cmd/engine/main.go.
	r.DeviceName = vk.ToString(bestProps.DeviceName[:])
	r.DeviceTypeName = physicalDeviceTypeName(bestProps.DeviceType)
	r.DeviceIsSoftware = bestProps.DeviceType == vk.PhysicalDeviceTypeCpu
	return nil
}

func physicalDeviceTypeName(t vk.PhysicalDeviceType) string {
	switch t {
	case vk.PhysicalDeviceTypeDiscreteGpu:
		return "discrete GPU"
	case vk.PhysicalDeviceTypeIntegratedGpu:
		return "integrated GPU"
	case vk.PhysicalDeviceTypeVirtualGpu:
		return "virtual GPU"
	case vk.PhysicalDeviceTypeCpu:
		return "CPU (software rasterizer — not real GPU rendering)"
	default:
		return "unknown device type"
	}
}

// findQueueFamilies looks for a queue family that supports graphics commands
// and one that supports presenting to r.surface (often, but not always, the
// same family).
func (r *Renderer) findQueueFamilies(dev vk.PhysicalDevice) (graphics, present uint32, ok bool) {
	var count uint32
	vk.GetPhysicalDeviceQueueFamilyProperties(dev, &count, nil)
	if count == 0 {
		return 0, 0, false
	}
	families := make([]vk.QueueFamilyProperties, count)
	vk.GetPhysicalDeviceQueueFamilyProperties(dev, &count, families)

	var haveGraphics, havePresent bool
	for i := uint32(0); i < count; i++ {
		families[i].Deref()

		if families[i].QueueFlags&vk.QueueFlags(vk.QueueGraphicsBit) != 0 {
			graphics = i
			haveGraphics = true
		}

		var presentSupport vk.Bool32
		vk.GetPhysicalDeviceSurfaceSupport(dev, i, r.surface, &presentSupport)
		if presentSupport.B() {
			present = i
			havePresent = true
		}

		if haveGraphics && havePresent {
			break
		}
	}
	return graphics, present, haveGraphics && havePresent
}

func deviceSupportsSwapchain(dev vk.PhysicalDevice) bool {
	return deviceHasExtension(dev, swapchainExtensionName)
}

// deviceHasExtension reports whether the physical device advertises a
// device-level extension by name.
func deviceHasExtension(dev vk.PhysicalDevice, name string) bool {
	var count uint32
	vk.EnumerateDeviceExtensionProperties(dev, "", &count, nil)
	if count == 0 {
		return false
	}
	props := make([]vk.ExtensionProperties, count)
	vk.EnumerateDeviceExtensionProperties(dev, "", &count, props)
	for i := range props {
		props[i].Deref()
		if vk.ToString(props[i].ExtensionName[:]) == name {
			return true
		}
	}
	return false
}

func (r *Renderer) createLogicalDevice() error {
	families := map[uint32]struct{}{r.graphicsFamily: {}, r.presentFamily: {}}
	priorities := []float32{1.0}

	queueInfos := make([]vk.DeviceQueueCreateInfo, 0, len(families))
	for fam := range families {
		queueInfos = append(queueInfos, vk.DeviceQueueCreateInfo{
			SType:            vk.StructureTypeDeviceQueueCreateInfo,
			QueueFamilyIndex: fam,
			QueueCount:       1,
			PQueuePriorities: priorities,
		})
	}

	extNames := []string{swapchainExtensionName}
	// A portability-subset device (MoltenVK, some layered drivers) MUST have
	// VK_KHR_portability_subset enabled when it's present — the Vulkan spec
	// requires it. Native Windows / Linux devices never advertise it, so
	// this is inert there.
	if deviceHasExtension(r.physicalDevice, vk.KhrPortabilitySubsetExtensionName) {
		extNames = append(extNames, vk.KhrPortabilitySubsetExtensionName)
	}
	extensions := nzAll(extNames)
	features := []vk.PhysicalDeviceFeatures{{}}
	// The hardware-geometry path samples mip-mapped wall/flat textures; turn
	// on anisotropic filtering when the device offers it so minified,
	// oblique surfaces stay sharp (the software path's textureQuality knob,
	// done in fixed-function hardware here). Inert for the vanilla / lit
	// blit paths.
	if r.hw {
		var supported vk.PhysicalDeviceFeatures
		vk.GetPhysicalDeviceFeatures(r.physicalDevice, &supported)
		supported.Deref()
		if supported.SamplerAnisotropy == vk.True {
			features[0].SamplerAnisotropy = vk.True
			var props vk.PhysicalDeviceProperties
			vk.GetPhysicalDeviceProperties(r.physicalDevice, &props)
			props.Deref()
			props.Limits.Deref()
			r.maxAnisotropy = props.Limits.MaxSamplerAnisotropy
		}
	}

	createInfo := vk.DeviceCreateInfo{
		SType:                   vk.StructureTypeDeviceCreateInfo,
		QueueCreateInfoCount:    uint32(len(queueInfos)),
		PQueueCreateInfos:       queueInfos,
		EnabledExtensionCount:   uint32(len(extensions)),
		PpEnabledExtensionNames: extensions,
		PEnabledFeatures:        features,
	}

	var device vk.Device
	if res := vk.CreateDevice(r.physicalDevice, &createInfo, nil, &device); res != vk.Success {
		return fmt.Errorf("vulkan: create logical device: %s", vk.Error(res))
	}
	r.device = device

	var graphicsQueue, presentQueue vk.Queue
	vk.GetDeviceQueue(device, r.graphicsFamily, 0, &graphicsQueue)
	vk.GetDeviceQueue(device, r.presentFamily, 0, &presentQueue)
	r.graphicsQueue = graphicsQueue
	r.presentQueue = presentQueue

	return nil
}
