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

// litImage is a device-local image + its memory + one full-image view — the
// shape every offscreen render target here (world's ambient/bloom/HDR
// targets, the iso viewer's offscreen scene) is built from. wMakeImage
// (worldsetup.go) builds one.
type litImage struct {
	image  vk.Image
	memory vk.DeviceMemory
	view   vk.ImageView
}

// samplerBinding is a combined-image-sampler descriptor binding at binding
// slot n, fragment-stage only — the shape every offscreen pass here (world's
// ambient/bloom/blit passes, the iso viewer's present pass) samples its
// source image(s) through. Named "sampler" rather than after any one
// caller since every one of them needs exactly this.
func samplerBinding(n uint32) vk.DescriptorSetLayoutBinding {
	return vk.DescriptorSetLayoutBinding{
		Binding:         n,
		DescriptorType:  vk.DescriptorTypeCombinedImageSampler,
		DescriptorCount: 1,
		StageFlags:      vk.ShaderStageFlags(vk.ShaderStageFragmentBit),
	}
}

// fullViewport / fullScissor are the dynamic viewport/scissor for a pass
// that draws over its whole w x h target — every fullscreen-triangle pass
// (world's ambient/bloom/blit, the iso viewer's present) sets these once
// per command buffer since none of them ever draws to a sub-rectangle.
func fullViewport(w, h int) vk.Viewport {
	return vk.Viewport{Width: float32(w), Height: float32(h), MinDepth: 0, MaxDepth: 1}
}

func fullScissor(w, h int) vk.Rect2D {
	return vk.Rect2D{Extent: vk.Extent2D{Width: uint32(w), Height: uint32(h)}}
}

// litPipelineLayout builds a pipeline layout with one descriptor set and an
// optional fragment-stage push-constant range — every offscreen fullscreen-
// triangle pass here (world's ambient/bloom/blit passes) shares this exact
// shape, varying only the descriptor set layout and push-constant size.
func (r *Renderer) litPipelineLayout(setLayout vk.DescriptorSetLayout, pushSize uint32) (vk.PipelineLayout, error) {
	info := vk.PipelineLayoutCreateInfo{
		SType:          vk.StructureTypePipelineLayoutCreateInfo,
		SetLayoutCount: 1,
		PSetLayouts:    []vk.DescriptorSetLayout{setLayout},
	}
	if pushSize > 0 {
		info.PushConstantRangeCount = 1
		info.PPushConstantRanges = []vk.PushConstantRange{{
			StageFlags: vk.ShaderStageFlags(vk.ShaderStageFragmentBit), Offset: 0, Size: pushSize,
		}}
	}
	var l vk.PipelineLayout
	if res := vk.CreatePipelineLayout(r.device, &info, nil, &l); res != vk.Success {
		return nil, fmt.Errorf("vulkan: pipeline layout: %s", vk.Error(res))
	}
	return l, nil
}

// litPipeline builds a graphics pipeline that draws blitVertSPV's synthesized
// fullscreen triangle through fragSPV into renderPass, with layout — the one
// shape every offscreen fullscreen-triangle pass here (world's ambient/
// bloom/blit passes) needs, varying only the fragment shader/render pass/
// layout. No vertex buffers, no depth test, dynamic viewport/scissor.
func (r *Renderer) litPipeline(fragSPV []byte, renderPass vk.RenderPass, layout vk.PipelineLayout) (vk.Pipeline, error) {
	vert, err := r.createShaderModule(blitVertSPV)
	if err != nil {
		return nil, fmt.Errorf("vulkan: fullscreen vert module: %w", err)
	}
	defer vk.DestroyShaderModule(r.device, vert, nil)
	frag, err := r.createShaderModule(fragSPV)
	if err != nil {
		return nil, fmt.Errorf("vulkan: fullscreen frag module: %w", err)
	}
	defer vk.DestroyShaderModule(r.device, frag, nil)

	stages := []vk.PipelineShaderStageCreateInfo{
		{SType: vk.StructureTypePipelineShaderStageCreateInfo, Stage: vk.ShaderStageVertexBit, Module: vert, PName: nz("main")},
		{SType: vk.StructureTypePipelineShaderStageCreateInfo, Stage: vk.ShaderStageFragmentBit, Module: frag, PName: nz("main")},
	}
	vertexInput := vk.PipelineVertexInputStateCreateInfo{SType: vk.StructureTypePipelineVertexInputStateCreateInfo}
	inputAssembly := vk.PipelineInputAssemblyStateCreateInfo{
		SType: vk.StructureTypePipelineInputAssemblyStateCreateInfo, Topology: vk.PrimitiveTopologyTriangleList,
	}
	viewportState := vk.PipelineViewportStateCreateInfo{
		SType: vk.StructureTypePipelineViewportStateCreateInfo, ViewportCount: 1, ScissorCount: 1,
	}
	rasterizer := vk.PipelineRasterizationStateCreateInfo{
		SType: vk.StructureTypePipelineRasterizationStateCreateInfo, PolygonMode: vk.PolygonModeFill,
		CullMode: vk.CullModeFlags(vk.CullModeNone), FrontFace: vk.FrontFaceClockwise, LineWidth: 1,
	}
	multisample := vk.PipelineMultisampleStateCreateInfo{
		SType: vk.StructureTypePipelineMultisampleStateCreateInfo, RasterizationSamples: vk.SampleCount1Bit,
	}
	cba := vk.PipelineColorBlendAttachmentState{
		ColorWriteMask: vk.ColorComponentFlags(vk.ColorComponentRBit) | vk.ColorComponentFlags(vk.ColorComponentGBit) |
			vk.ColorComponentFlags(vk.ColorComponentBBit) | vk.ColorComponentFlags(vk.ColorComponentABit),
	}
	colorBlend := vk.PipelineColorBlendStateCreateInfo{
		SType: vk.StructureTypePipelineColorBlendStateCreateInfo, AttachmentCount: 1,
		PAttachments: []vk.PipelineColorBlendAttachmentState{cba},
	}
	dynamic := vk.PipelineDynamicStateCreateInfo{
		SType: vk.StructureTypePipelineDynamicStateCreateInfo, DynamicStateCount: 2,
		PDynamicStates: []vk.DynamicState{vk.DynamicStateViewport, vk.DynamicStateScissor},
	}
	info := vk.GraphicsPipelineCreateInfo{
		SType:               vk.StructureTypeGraphicsPipelineCreateInfo,
		StageCount:          2,
		PStages:             stages,
		PVertexInputState:   &vertexInput,
		PInputAssemblyState: &inputAssembly,
		PViewportState:      &viewportState,
		PRasterizationState: &rasterizer,
		PMultisampleState:   &multisample,
		PColorBlendState:    &colorBlend,
		PDynamicState:       &dynamic,
		Layout:              layout,
		RenderPass:          renderPass,
		Subpass:             0,
	}
	out := make([]vk.Pipeline, 1)
	if res := vk.CreateGraphicsPipelines(r.device, nil, 1, []vk.GraphicsPipelineCreateInfo{info}, nil, out); res != vk.Success {
		return nil, fmt.Errorf("vulkan: fullscreen pipeline: %s", vk.Error(res))
	}
	return out[0], nil
}
