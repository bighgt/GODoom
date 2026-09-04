package vulkan

import (
	"fmt"

	"github.com/go-gl/glfw/v3.3/glfw"
	vk "github.com/goki/vulkan"
)

const validationLayerName = "VK_LAYER_KHRONOS_validation"

func (r *Renderer) createInstance() error {
	if !glfw.VulkanSupported() {
		return fmt.Errorf("vulkan: GLFW reports no Vulkan loader on this system")
	}

	// GLFW knows how to locate the platform's Vulkan loader; hand its
	// resolved vkGetInstanceProcAddr to the bindings instead of relying on
	// their own default search path.
	vk.SetGetInstanceProcAddr(glfw.GetVulkanGetInstanceProcAddress())
	if err := vk.Init(); err != nil {
		return fmt.Errorf("vulkan: init: %w", err)
	}

	extNames := r.win.RequiredInstanceExtensions()

	// On a portability Vulkan implementation (MoltenVK on macOS, some
	// layered drivers) the loader hides its physical devices unless the
	// instance opts in with VK_KHR_portability_enumeration + the matching
	// create flag. Where the extension isn't advertised — every native
	// Windows / Linux driver — this whole block is inert.
	var instanceFlags vk.InstanceCreateFlags
	if hasInstanceExtension(vk.KhrPortabilityEnumerationExtensionName) {
		extNames = append(extNames,
			vk.KhrPortabilityEnumerationExtensionName,
			vk.KhrGetPhysicalDeviceProperties2ExtensionName)
		instanceFlags = vk.InstanceCreateFlags(vk.InstanceCreateEnumeratePortabilityBit)
	}

	extensions := nzAll(extNames)
	if len(extensions) == 0 {
		return fmt.Errorf("vulkan: GLFW returned no required instance extensions for surface creation")
	}

	var layers []string
	if r.debug && hasInstanceLayer(validationLayerName) {
		layers = []string{nz(validationLayerName)}
	}

	appInfo := vk.ApplicationInfo{
		SType:              vk.StructureTypeApplicationInfo,
		PApplicationName:   nz("TwoPointFive"),
		ApplicationVersion: vk.MakeVersion(0, 1, 0),
		PEngineName:        nz("TwoPointFive Engine"),
		EngineVersion:      vk.MakeVersion(0, 1, 0),
		ApiVersion:         vk.MakeVersion(1, 0, 0),
	}

	createInfo := vk.InstanceCreateInfo{
		SType:                   vk.StructureTypeInstanceCreateInfo,
		Flags:                   instanceFlags,
		PApplicationInfo:        &appInfo,
		EnabledExtensionCount:   uint32(len(extensions)),
		PpEnabledExtensionNames: extensions,
		EnabledLayerCount:       uint32(len(layers)),
		PpEnabledLayerNames:     layers,
	}

	var instance vk.Instance
	if res := vk.CreateInstance(&createInfo, nil, &instance); res != vk.Success {
		return fmt.Errorf("vulkan: create instance: %s", vk.Error(res))
	}
	r.instance = instance

	if err := vk.InitInstance(instance); err != nil {
		return fmt.Errorf("vulkan: init instance: %w", err)
	}
	return nil
}

func hasInstanceLayer(name string) bool {
	var count uint32
	vk.EnumerateInstanceLayerProperties(&count, nil)
	if count == 0 {
		return false
	}
	props := make([]vk.LayerProperties, count)
	vk.EnumerateInstanceLayerProperties(&count, props)
	for i := range props {
		props[i].Deref()
		if vk.ToString(props[i].LayerName[:]) == name {
			return true
		}
	}
	return false
}

func hasInstanceExtension(name string) bool {
	var count uint32
	vk.EnumerateInstanceExtensionProperties("", &count, nil)
	if count == 0 {
		return false
	}
	props := make([]vk.ExtensionProperties, count)
	vk.EnumerateInstanceExtensionProperties("", &count, props)
	for i := range props {
		props[i].Deref()
		if vk.ToString(props[i].ExtensionName[:]) == name {
			return true
		}
	}
	return false
}
