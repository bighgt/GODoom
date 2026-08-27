package vulkan

import (
	"fmt"

	vk "github.com/goki/vulkan"
)

// createDescriptors sets up the single combined-image-sampler binding the
// blit fragment shader reads the uploaded frame texture through: a
// descriptor set layout (the binding's shape), a pool sized for exactly the
// one set this renderer ever needs, the set itself, and its write pointing
// at r.textureView/r.textureSampler.
func (r *Renderer) createDescriptors() error {
	binding := vk.DescriptorSetLayoutBinding{
		Binding:         0,
		DescriptorType:  vk.DescriptorTypeCombinedImageSampler,
		DescriptorCount: 1,
		StageFlags:      vk.ShaderStageFlags(vk.ShaderStageFragmentBit),
	}
	layoutInfo := vk.DescriptorSetLayoutCreateInfo{
		SType:        vk.StructureTypeDescriptorSetLayoutCreateInfo,
		BindingCount: 1,
		PBindings:    []vk.DescriptorSetLayoutBinding{binding},
	}
	var layout vk.DescriptorSetLayout
	if res := vk.CreateDescriptorSetLayout(r.device, &layoutInfo, nil, &layout); res != vk.Success {
		return fmt.Errorf("vulkan: create descriptor set layout: %s", vk.Error(res))
	}
	r.descriptorSetLayout = layout

	poolInfo := vk.DescriptorPoolCreateInfo{
		SType:         vk.StructureTypeDescriptorPoolCreateInfo,
		MaxSets:       1,
		PoolSizeCount: 1,
		PPoolSizes:    []vk.DescriptorPoolSize{{Type: vk.DescriptorTypeCombinedImageSampler, DescriptorCount: 1}},
	}
	var pool vk.DescriptorPool
	if res := vk.CreateDescriptorPool(r.device, &poolInfo, nil, &pool); res != vk.Success {
		return fmt.Errorf("vulkan: create descriptor pool: %s", vk.Error(res))
	}
	r.descriptorPool = pool

	allocInfo := vk.DescriptorSetAllocateInfo{
		SType:              vk.StructureTypeDescriptorSetAllocateInfo,
		DescriptorPool:     pool,
		DescriptorSetCount: 1,
		PSetLayouts:        []vk.DescriptorSetLayout{layout},
	}
	var set vk.DescriptorSet
	if res := vk.AllocateDescriptorSets(r.device, &allocInfo, &set); res != vk.Success {
		return fmt.Errorf("vulkan: allocate descriptor set: %s", vk.Error(res))
	}
	r.descriptorSet = set

	r.updateDescriptorSet()
	return nil
}

// updateDescriptorSet points the descriptor set at the current
// texture view/sampler; called once at startup (texture.go creates the view
// and sampler once and never recreates them, so this never needs to run again).
func (r *Renderer) updateDescriptorSet() {
	imageInfo := vk.DescriptorImageInfo{
		Sampler:     r.textureSampler,
		ImageView:   r.textureView,
		ImageLayout: vk.ImageLayoutShaderReadOnlyOptimal,
	}
	write := vk.WriteDescriptorSet{
		SType:           vk.StructureTypeWriteDescriptorSet,
		DstSet:          r.descriptorSet,
		DstBinding:      0,
		DescriptorCount: 1,
		DescriptorType:  vk.DescriptorTypeCombinedImageSampler,
		PImageInfo:      []vk.DescriptorImageInfo{imageInfo},
	}
	vk.UpdateDescriptorSets(r.device, 1, []vk.WriteDescriptorSet{write}, 0, nil)
}

func (r *Renderer) destroyDescriptors() {
	if r.descriptorPool != nil {
		vk.DestroyDescriptorPool(r.device, r.descriptorPool, nil)
		r.descriptorPool = nil
	}
	if r.descriptorSetLayout != nil {
		vk.DestroyDescriptorSetLayout(r.device, r.descriptorSetLayout, nil)
		r.descriptorSetLayout = nil
	}
}
