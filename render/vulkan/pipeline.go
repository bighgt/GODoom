package vulkan

import (
	_ "embed"
	"fmt"

	vk "github.com/goki/vulkan"
)

// The blit pipeline's shaders, precompiled to SPIR-V by glslc from the
// .vert/.frag sources next to them (see shaders/blit.vert, shaders/blit.frag)
// and embedded directly into the binary — no runtime shader compilation, no
// external files to ship alongside the executable.
//
//go:embed shaders/blit.vert.spv
var blitVertSPV []byte

//go:embed shaders/blit.frag.spv
var blitFragSPV []byte

// createPipeline builds the (trivial) graphics pipeline that draws a single
// fullscreen triangle sampling r.textureView — this is the entire "GPU
// rendering" stage that presents raster.Renderer's CPU-built frame. No
// vertex buffers: blit.vert synthesizes its 3 vertices from gl_VertexIndex.
func (r *Renderer) createPipeline() error {
	vertModule, err := r.createShaderModule(blitVertSPV)
	if err != nil {
		return fmt.Errorf("vulkan: blit vertex shader: %w", err)
	}
	defer vk.DestroyShaderModule(r.device, vertModule, nil)

	fragModule, err := r.createShaderModule(blitFragSPV)
	if err != nil {
		return fmt.Errorf("vulkan: blit fragment shader: %w", err)
	}
	defer vk.DestroyShaderModule(r.device, fragModule, nil)

	stages := []vk.PipelineShaderStageCreateInfo{
		{SType: vk.StructureTypePipelineShaderStageCreateInfo, Stage: vk.ShaderStageVertexBit, Module: vertModule, PName: nz("main")},
		{SType: vk.StructureTypePipelineShaderStageCreateInfo, Stage: vk.ShaderStageFragmentBit, Module: fragModule, PName: nz("main")},
	}

	vertexInput := vk.PipelineVertexInputStateCreateInfo{SType: vk.StructureTypePipelineVertexInputStateCreateInfo}
	inputAssembly := vk.PipelineInputAssemblyStateCreateInfo{
		SType:    vk.StructureTypePipelineInputAssemblyStateCreateInfo,
		Topology: vk.PrimitiveTopologyTriangleList,
	}
	viewportState := vk.PipelineViewportStateCreateInfo{
		SType: vk.StructureTypePipelineViewportStateCreateInfo, ViewportCount: 1, ScissorCount: 1,
	}
	rasterizer := vk.PipelineRasterizationStateCreateInfo{
		SType:       vk.StructureTypePipelineRasterizationStateCreateInfo,
		PolygonMode: vk.PolygonModeFill,
		CullMode:    vk.CullModeFlags(vk.CullModeNone),
		FrontFace:   vk.FrontFaceClockwise,
		LineWidth:   1,
	}
	multisample := vk.PipelineMultisampleStateCreateInfo{
		SType: vk.StructureTypePipelineMultisampleStateCreateInfo, RasterizationSamples: vk.SampleCount1Bit,
	}
	colorBlendAttachment := vk.PipelineColorBlendAttachmentState{
		ColorWriteMask: vk.ColorComponentFlags(vk.ColorComponentRBit) | vk.ColorComponentFlags(vk.ColorComponentGBit) |
			vk.ColorComponentFlags(vk.ColorComponentBBit) | vk.ColorComponentFlags(vk.ColorComponentABit),
	}
	colorBlend := vk.PipelineColorBlendStateCreateInfo{
		SType: vk.StructureTypePipelineColorBlendStateCreateInfo, AttachmentCount: 1,
		PAttachments: []vk.PipelineColorBlendAttachmentState{colorBlendAttachment},
	}
	dynamicState := vk.PipelineDynamicStateCreateInfo{
		SType: vk.StructureTypePipelineDynamicStateCreateInfo, DynamicStateCount: 2,
		PDynamicStates: []vk.DynamicState{vk.DynamicStateViewport, vk.DynamicStateScissor},
	}

	layoutInfo := vk.PipelineLayoutCreateInfo{
		SType: vk.StructureTypePipelineLayoutCreateInfo, SetLayoutCount: 1,
		PSetLayouts: []vk.DescriptorSetLayout{r.descriptorSetLayout},
	}
	var layout vk.PipelineLayout
	if res := vk.CreatePipelineLayout(r.device, &layoutInfo, nil, &layout); res != vk.Success {
		return fmt.Errorf("vulkan: create pipeline layout: %s", vk.Error(res))
	}
	r.pipelineLayout = layout

	pipelineInfo := vk.GraphicsPipelineCreateInfo{
		SType:               vk.StructureTypeGraphicsPipelineCreateInfo,
		StageCount:          2,
		PStages:             stages,
		PVertexInputState:   &vertexInput,
		PInputAssemblyState: &inputAssembly,
		PViewportState:      &viewportState,
		PRasterizationState: &rasterizer,
		PMultisampleState:   &multisample,
		PColorBlendState:    &colorBlend,
		PDynamicState:       &dynamicState,
		Layout:              layout,
		RenderPass:          r.renderPass,
		Subpass:             0,
	}
	pipelines := make([]vk.Pipeline, 1)
	if res := vk.CreateGraphicsPipelines(r.device, nil, 1, []vk.GraphicsPipelineCreateInfo{pipelineInfo}, nil, pipelines); res != vk.Success {
		return fmt.Errorf("vulkan: create graphics pipeline: %s", vk.Error(res))
	}
	r.pipeline = pipelines[0]

	return nil
}

func (r *Renderer) createShaderModule(code []byte) (vk.ShaderModule, error) {
	// SPIR-V is a stream of uint32 words; the bytes embedded via go:embed
	// are already in the target's native (little-endian) byte order because
	// glslc wrote them that way, so a straight reinterpret is safe here.
	words := make([]uint32, len(code)/4)
	for i := range words {
		words[i] = uint32(code[i*4]) | uint32(code[i*4+1])<<8 | uint32(code[i*4+2])<<16 | uint32(code[i*4+3])<<24
	}

	createInfo := vk.ShaderModuleCreateInfo{
		SType:    vk.StructureTypeShaderModuleCreateInfo,
		CodeSize: uint64(len(code)),
		PCode:    words,
	}
	var module vk.ShaderModule
	if res := vk.CreateShaderModule(r.device, &createInfo, nil, &module); res != vk.Success {
		return nil, fmt.Errorf("%s", vk.Error(res))
	}
	return module, nil
}

func (r *Renderer) destroyPipeline() {
	if r.pipeline != nil {
		vk.DestroyPipeline(r.device, r.pipeline, nil)
		r.pipeline = nil
	}
	if r.pipelineLayout != nil {
		vk.DestroyPipelineLayout(r.device, r.pipelineLayout, nil)
		r.pipelineLayout = nil
	}
}
