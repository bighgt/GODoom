package vulkan

import (
	"fmt"

	vk "github.com/goki/vulkan"
)

// Small create/alloc wrappers for the boilerplate every pass setup repeats.
// what only labels the error message ("bloom", "ambient", ...). Each returns
// its handle through a local var, never a pointer into a caller's struct —
// see the cgocheck note in worldbloom.go's descriptor setup.

// newDescriptorSetLayout creates a set layout from bindings.
func (r *Renderer) newDescriptorSetLayout(what string, bindings ...vk.DescriptorSetLayoutBinding) (vk.DescriptorSetLayout, error) {
	info := vk.DescriptorSetLayoutCreateInfo{
		SType:        vk.StructureTypeDescriptorSetLayoutCreateInfo,
		BindingCount: uint32(len(bindings)),
		PBindings:    bindings,
	}
	var l vk.DescriptorSetLayout
	if res := vk.CreateDescriptorSetLayout(r.device, &info, nil, &l); res != vk.Success {
		return nil, fmt.Errorf("vulkan: %s dsl: %s", what, vk.Error(res))
	}
	return l, nil
}

// allocDescriptorSet allocates one set of layout from pool.
func (r *Renderer) allocDescriptorSet(pool vk.DescriptorPool, layout vk.DescriptorSetLayout, what string) (vk.DescriptorSet, error) {
	info := vk.DescriptorSetAllocateInfo{
		SType:              vk.StructureTypeDescriptorSetAllocateInfo,
		DescriptorPool:     pool,
		DescriptorSetCount: 1,
		PSetLayouts:        []vk.DescriptorSetLayout{layout},
	}
	var s vk.DescriptorSet
	if res := vk.AllocateDescriptorSets(r.device, &info, &s); res != vk.Success {
		return nil, fmt.Errorf("vulkan: %s descriptor set: %s", what, vk.Error(res))
	}
	return s, nil
}

// imageWrite is a one-descriptor combined-image-sampler write of view
// (in SHADER_READ_ONLY_OPTIMAL) to set's binding.
func imageWrite(set vk.DescriptorSet, binding uint32, samp vk.Sampler, view vk.ImageView) vk.WriteDescriptorSet {
	return vk.WriteDescriptorSet{
		SType:           vk.StructureTypeWriteDescriptorSet,
		DstSet:          set,
		DstBinding:      binding,
		DescriptorCount: 1,
		DescriptorType:  vk.DescriptorTypeCombinedImageSampler,
		PImageInfo: []vk.DescriptorImageInfo{{
			Sampler: samp, ImageView: view, ImageLayout: vk.ImageLayoutShaderReadOnlyOptimal,
		}},
	}
}

// newFramebuffer creates a w x h single-layer framebuffer over views.
func (r *Renderer) newFramebuffer(rp vk.RenderPass, w, h int, what string, views ...vk.ImageView) (vk.Framebuffer, error) {
	info := vk.FramebufferCreateInfo{
		SType:           vk.StructureTypeFramebufferCreateInfo,
		RenderPass:      rp,
		AttachmentCount: uint32(len(views)),
		PAttachments:    views,
		Width:           uint32(w),
		Height:          uint32(h),
		Layers:          1,
	}
	var fb vk.Framebuffer
	if res := vk.CreateFramebuffer(r.device, &info, nil, &fb); res != vk.Success {
		return nil, fmt.Errorf("vulkan: %s framebuffer: %s", what, vk.Error(res))
	}
	return fb, nil
}

// newSampledColorPass builds the one-colour-attachment render pass the
// post-process passes (bloom, ambient, resolve) all use: the target is
// left shader-readable, and symmetric SubpassExternal dependencies make a
// previous pass's fragment read finish before this one's load/write, and
// this one's write visible to the next pass's fragment read — the targets
// are reused across frames in flight. loadOp/initLayout choose between
// clearing (Clear, Undefined) and blending onto the existing contents
// (Load, ShaderReadOnlyOptimal).
func (r *Renderer) newSampledColorPass(format vk.Format, loadOp vk.AttachmentLoadOp, initLayout vk.ImageLayout, what string) (vk.RenderPass, error) {
	att := vk.AttachmentDescription{
		Format: format, Samples: vk.SampleCount1Bit,
		LoadOp: loadOp, StoreOp: vk.AttachmentStoreOpStore,
		StencilLoadOp: vk.AttachmentLoadOpDontCare, StencilStoreOp: vk.AttachmentStoreOpDontCare,
		InitialLayout: initLayout, FinalLayout: vk.ImageLayoutShaderReadOnlyOptimal,
	}
	ref := vk.AttachmentReference{Attachment: 0, Layout: vk.ImageLayoutColorAttachmentOptimal}
	sub := vk.SubpassDescription{
		PipelineBindPoint: vk.PipelineBindPointGraphics, ColorAttachmentCount: 1,
		PColorAttachments: []vk.AttachmentReference{ref},
	}
	deps := []vk.SubpassDependency{
		{
			SrcSubpass: vk.SubpassExternal, DstSubpass: 0,
			SrcStageMask:  vk.PipelineStageFlags(vk.PipelineStageFragmentShaderBit),
			DstStageMask:  vk.PipelineStageFlags(vk.PipelineStageColorAttachmentOutputBit),
			SrcAccessMask: vk.AccessFlags(vk.AccessShaderReadBit),
			DstAccessMask: vk.AccessFlags(vk.AccessColorAttachmentReadBit) | vk.AccessFlags(vk.AccessColorAttachmentWriteBit),
		},
		{
			SrcSubpass: 0, DstSubpass: vk.SubpassExternal,
			SrcStageMask:  vk.PipelineStageFlags(vk.PipelineStageColorAttachmentOutputBit),
			DstStageMask:  vk.PipelineStageFlags(vk.PipelineStageFragmentShaderBit),
			SrcAccessMask: vk.AccessFlags(vk.AccessColorAttachmentWriteBit),
			DstAccessMask: vk.AccessFlags(vk.AccessShaderReadBit),
		},
	}
	info := vk.RenderPassCreateInfo{
		SType: vk.StructureTypeRenderPassCreateInfo, AttachmentCount: 1, PAttachments: []vk.AttachmentDescription{att},
		SubpassCount: 1, PSubpasses: []vk.SubpassDescription{sub},
		DependencyCount: uint32(len(deps)), PDependencies: deps,
	}
	var rp vk.RenderPass
	if res := vk.CreateRenderPass(r.device, &info, nil, &rp); res != vk.Success {
		return nil, fmt.Errorf("vulkan: %s render pass: %s", what, vk.Error(res))
	}
	return rp, nil
}

// uploadVertexData copies data into a new device-local vertex buffer
// through a throwaway staging buffer (blocking one-shot submit — load-time
// / first-sight uploads only, never the per-frame path).
func (r *Renderer) uploadVertexData(data []byte) (vk.Buffer, vk.DeviceMemory, error) {
	vbuf, vmem, err := r.deviceLocalVertexBuffer(len(data))
	if err != nil {
		return nil, nil, err
	}
	if err := r.copyToDeviceBuffer(vbuf, data); err != nil {
		vk.DestroyBuffer(r.device, vbuf, nil)
		vk.FreeMemory(r.device, vmem, nil)
		return nil, nil, err
	}
	return vbuf, vmem, nil
}

// copyToDeviceBuffer copies data to the start of dst (a device-local
// buffer with TRANSFER_DST usage) through a throwaway staging buffer.
func (r *Renderer) copyToDeviceBuffer(dst vk.Buffer, data []byte) error {
	stage, stageMem, mapped, err := r.wHostBuffer(len(data), vk.BufferUsageFlags(vk.BufferUsageTransferSrcBit))
	if err != nil {
		return err
	}
	vk.Memcopy(mapped, data)
	defer func() {
		vk.UnmapMemory(r.device, stageMem)
		vk.DestroyBuffer(r.device, stage, nil)
		vk.FreeMemory(r.device, stageMem, nil)
	}()
	return r.submitOneShot(func(cmd vk.CommandBuffer) {
		vk.CmdCopyBuffer(cmd, stage, dst, 1, []vk.BufferCopy{{Size: vk.DeviceSize(len(data))}})
	})
}
