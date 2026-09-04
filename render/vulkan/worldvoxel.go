package vulkan

import (
	_ "embed"
	"fmt"
	"math"
	"unsafe"

	vk "github.com/goki/vulkan"

	"twopointfive/assets"
	"twopointfive/render/worldgeo"
)

// Voxel models on the hardware path: each KVX model is meshed once
// (worldgeo.BuildVoxelMesh — exposed cube faces, vertex colour) into a
// static device-local vertex buffer, and drawn every frame with a dedicated
// pipeline (voxel.vert/frag) that reuses set 1 (the fade LUTs + World UBO)
// and takes the per-instance transform + shading as push constants. The
// draws land inside the world render pass so the depth buffer sorts a voxel
// monster against the level and the textured geometry.

//go:embed shaders/voxel.vert.spv
var voxelVertSPV []byte

//go:embed shaders/voxel.frag.spv
var voxelFragSPV []byte

// Push constant split into two NON-overlapping ranges so the two shader
// stages' blocks can't be pruned by -O into incompatible layouts (an AMD
// driver returns VK_ERROR_UNKNOWN from vkCreateGraphicsPipelines for that):
//   [0,96)   vertex   — mat4 mvp (64) + vec4 trans + vec4 rot (32)
//   [96,112) fragment — vec4 shade
const (
	voxelPushVert  = 96
	voxelPushFrag  = 16
	voxelPushBytes = voxelPushVert + voxelPushFrag // 112, within the 128 min
)

type voxelMesh struct {
	vbuf    vk.Buffer
	vbufMem vk.DeviceMemory
	count   uint32 // vertices (0 = model meshed to nothing; skip)
}

// worldCreateVoxelPipeline builds the voxel model pipeline + its layout.
// Run as a NewWorld step after worldCreatePipelines (needs dsl1 + rpScene).
func (r *Renderer) worldCreateVoxelPipeline() error {
	wr := r.wr

	layoutInfo := vk.PipelineLayoutCreateInfo{
		SType:          vk.StructureTypePipelineLayoutCreateInfo,
		SetLayoutCount: 1,
		PSetLayouts:            []vk.DescriptorSetLayout{wr.dsl1},
		PushConstantRangeCount: 2,
		PPushConstantRanges: []vk.PushConstantRange{
			{StageFlags: vk.ShaderStageFlags(vk.ShaderStageVertexBit), Offset: 0, Size: voxelPushVert},
			{StageFlags: vk.ShaderStageFlags(vk.ShaderStageFragmentBit), Offset: voxelPushVert, Size: voxelPushFrag},
		},
	}
	var pl vk.PipelineLayout
	if res := vk.CreatePipelineLayout(r.device, &layoutInfo, nil, &pl); res != vk.Success {
		return fmt.Errorf("vulkan: voxel pipeline layout: %s", vk.Error(res))
	}
	wr.plVoxel = pl

	vert, err := r.createShaderModule(voxelVertSPV)
	if err != nil {
		return fmt.Errorf("vulkan: voxel vert: %w", err)
	}
	defer vk.DestroyShaderModule(r.device, vert, nil)
	frag, err := r.createShaderModule(voxelFragSPV)
	if err != nil {
		return fmt.Errorf("vulkan: voxel frag: %w", err)
	}
	defer vk.DestroyShaderModule(r.device, frag, nil)

	stages := []vk.PipelineShaderStageCreateInfo{
		{SType: vk.StructureTypePipelineShaderStageCreateInfo, Stage: vk.ShaderStageVertexBit, Module: vert, PName: nz("main")},
		{SType: vk.StructureTypePipelineShaderStageCreateInfo, Stage: vk.ShaderStageFragmentBit, Module: frag, PName: nz("main")},
	}

	attrs := worldgeo.VoxelVertexAttributes()
	vkAttrs := make([]vk.VertexInputAttributeDescription, len(attrs))
	for i, a := range attrs {
		vkAttrs[i] = vk.VertexInputAttributeDescription{
			Location: uint32(a.Location), Binding: 0, Format: vkFormatOf(a.Format), Offset: uint32(a.Offset),
		}
	}
	binding := vk.VertexInputBindingDescription{Binding: 0, Stride: uint32(worldgeo.VoxelPackedVertexSize), InputRate: vk.VertexInputRateVertex}
	vertexInput := vk.PipelineVertexInputStateCreateInfo{
		SType:                           vk.StructureTypePipelineVertexInputStateCreateInfo,
		VertexBindingDescriptionCount:   1,
		PVertexBindingDescriptions:      []vk.VertexInputBindingDescription{binding},
		VertexAttributeDescriptionCount: uint32(len(vkAttrs)),
		PVertexAttributeDescriptions:    vkAttrs,
	}
	inputAssembly := vk.PipelineInputAssemblyStateCreateInfo{
		SType: vk.StructureTypePipelineInputAssemblyStateCreateInfo, Topology: vk.PrimitiveTopologyTriangleList,
	}
	viewportState := vk.PipelineViewportStateCreateInfo{
		SType: vk.StructureTypePipelineViewportStateCreateInfo, ViewportCount: 1, ScissorCount: 1,
	}
	rasterizer := vk.PipelineRasterizationStateCreateInfo{
		SType:       vk.StructureTypePipelineRasterizationStateCreateInfo,
		PolygonMode: vk.PolygonModeFill,
		// BuildVoxelMesh winds each face CCW about its outward normal in
		// model space; through worldgeo.ViewProj that lands as CCW in the
		// framebuffer, so front = counter-clockwise and the hidden inward
		// faces cull. (Confirmed against a running frame: Clockwise culled
		// the *visible* faces and models rendered see-through.)
		CullMode:  vk.CullModeFlags(vk.CullModeBackBit),
		FrontFace: vk.FrontFaceCounterClockwise,
		LineWidth: 1,
	}
	multisample := vk.PipelineMultisampleStateCreateInfo{
		SType: vk.StructureTypePipelineMultisampleStateCreateInfo, RasterizationSamples: vk.SampleCount1Bit,
	}
	depthStencil := vk.PipelineDepthStencilStateCreateInfo{
		SType:           vk.StructureTypePipelineDepthStencilStateCreateInfo,
		DepthTestEnable: vk.True, DepthWriteEnable: vk.True, DepthCompareOp: vk.CompareOpLessOrEqual,
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
		PDepthStencilState:  &depthStencil,
		PColorBlendState:    &colorBlend,
		PDynamicState:       &dynamic,
		Layout:              wr.plVoxel,
		RenderPass:          wr.rpScene,
		Subpass:             0,
	}
	out := make([]vk.Pipeline, 1)
	if res := vk.CreateGraphicsPipelines(r.device, nil, 1, []vk.GraphicsPipelineCreateInfo{info}, nil, out); res != vk.Success {
		return fmt.Errorf("vulkan: voxel pipeline: %s", vk.Error(res))
	}
	wr.pipeVoxel = out[0]
	return nil
}

// WarmVoxels meshes + uploads every model up front — one shared staging
// buffer and a single command submit for the whole pack, rather than a
// device stall per model on first sight in the render loop (Cheello's Doom1
// pack has ~300 distinct models). Called at level load on the hardware
// path; safe on a nil / non-hardware renderer.
func (r *Renderer) WarmVoxels(models []*assets.VoxelModel) {
	if r == nil || !r.hw || r.wr == nil {
		return
	}
	wr := r.wr

	type pending struct {
		m      *assets.VoxelModel
		vbuf   vk.Buffer
		vbufMem vk.DeviceMemory
		off    int
		size   int
		count  uint32
	}
	var todo []pending
	var scratch []byte // all meshes concatenated, staged in one buffer

	for _, m := range models {
		if m == nil || wr.voxCache[m] != nil {
			continue
		}
		data := worldgeo.BuildVoxelMesh(m)
		if len(data) == 0 {
			wr.voxCache[m] = &voxelMesh{}
			continue
		}
		vbuf, vmem, err := r.deviceLocalVertexBuffer(len(data))
		if err != nil {
			return
		}
		todo = append(todo, pending{m: m, vbuf: vbuf, vbufMem: vmem, off: len(scratch), size: len(data),
			count: uint32(len(data) / worldgeo.VoxelPackedVertexSize)})
		scratch = append(scratch, data...)
	}
	if len(todo) == 0 {
		return
	}

	stage, stageMem, mapped, err := r.wHostBuffer(len(scratch), vk.BufferUsageFlags(vk.BufferUsageTransferSrcBit))
	if err != nil {
		for _, p := range todo {
			vk.DestroyBuffer(r.device, p.vbuf, nil)
			vk.FreeMemory(r.device, p.vbufMem, nil)
		}
		return
	}
	vk.Memcopy(mapped, scratch)

	err = r.submitOneShot(func(cmd vk.CommandBuffer) {
		for _, p := range todo {
			vk.CmdCopyBuffer(cmd, stage, p.vbuf, 1, []vk.BufferCopy{{
				SrcOffset: vk.DeviceSize(p.off), Size: vk.DeviceSize(p.size),
			}})
		}
	})
	vk.UnmapMemory(r.device, stageMem)
	vk.DestroyBuffer(r.device, stage, nil)
	vk.FreeMemory(r.device, stageMem, nil)
	if err != nil {
		for _, p := range todo {
			vk.DestroyBuffer(r.device, p.vbuf, nil)
			vk.FreeMemory(r.device, p.vbufMem, nil)
		}
		return
	}
	for _, p := range todo {
		wr.voxCache[p.m] = &voxelMesh{vbuf: p.vbuf, vbufMem: p.vbufMem, count: p.count}
	}
}

// deviceLocalVertexBuffer creates an unmapped device-local buffer usable as
// a vertex buffer and a transfer destination.
func (r *Renderer) deviceLocalVertexBuffer(size int) (vk.Buffer, vk.DeviceMemory, error) {
	info := vk.BufferCreateInfo{
		SType:       vk.StructureTypeBufferCreateInfo,
		Size:        vk.DeviceSize(size),
		Usage:       vk.BufferUsageFlags(vk.BufferUsageTransferDstBit) | vk.BufferUsageFlags(vk.BufferUsageVertexBufferBit),
		SharingMode: vk.SharingModeExclusive,
	}
	var buf vk.Buffer
	if res := vk.CreateBuffer(r.device, &info, nil, &buf); res != vk.Success {
		return nil, nil, fmt.Errorf("vulkan: voxel vbuf: %s", vk.Error(res))
	}
	var memReq vk.MemoryRequirements
	vk.GetBufferMemoryRequirements(r.device, buf, &memReq)
	memReq.Deref()
	mt, err := r.findMemoryType(memReq.MemoryTypeBits, vk.MemoryPropertyFlags(vk.MemoryPropertyDeviceLocalBit))
	if err != nil {
		vk.DestroyBuffer(r.device, buf, nil)
		return nil, nil, err
	}
	alloc := vk.MemoryAllocateInfo{SType: vk.StructureTypeMemoryAllocateInfo, AllocationSize: memReq.Size, MemoryTypeIndex: mt}
	var mem vk.DeviceMemory
	if res := vk.AllocateMemory(r.device, &alloc, nil, &mem); res != vk.Success {
		vk.DestroyBuffer(r.device, buf, nil)
		return nil, nil, fmt.Errorf("vulkan: voxel vbuf alloc: %s", vk.Error(res))
	}
	if res := vk.BindBufferMemory(r.device, buf, mem, 0); res != vk.Success {
		vk.DestroyBuffer(r.device, buf, nil)
		vk.FreeMemory(r.device, mem, nil)
		return nil, nil, fmt.Errorf("vulkan: voxel vbuf bind: %s", vk.Error(res))
	}
	return buf, mem, nil
}

// syncVoxelMeshes meshes + uploads any voxel model referenced this frame
// that isn't resident. Drains the device first, but only when there is
// genuinely new work.
func (r *Renderer) syncVoxelMeshes(g *worldgeo.Geometry) error {
	wr := r.wr
	pending := false
	for i := range g.Voxels {
		if m := g.Voxels[i].Model; m != nil && wr.voxCache[m] == nil {
			pending = true
			break
		}
	}
	if !pending {
		return nil
	}
	vk.DeviceWaitIdle(r.device)
	for i := range g.Voxels {
		m := g.Voxels[i].Model
		if m == nil || wr.voxCache[m] != nil {
			continue
		}
		if err := r.uploadVoxelMesh(m); err != nil {
			return err
		}
	}
	return nil
}

func (r *Renderer) uploadVoxelMesh(m *assets.VoxelModel) error {
	wr := r.wr
	data := worldgeo.BuildVoxelMesh(m)
	if len(data) == 0 {
		wr.voxCache[m] = &voxelMesh{} // meshed to nothing — cache the miss
		return nil
	}

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

	vbuf, vmem, err := r.deviceLocalVertexBuffer(len(data))
	if err != nil {
		return err
	}
	if err := r.submitOneShot(func(cmd vk.CommandBuffer) {
		vk.CmdCopyBuffer(cmd, stage, vbuf, 1, []vk.BufferCopy{{Size: vk.DeviceSize(len(data))}})
	}); err != nil {
		vk.DestroyBuffer(r.device, vbuf, nil)
		vk.FreeMemory(r.device, vmem, nil)
		return err
	}
	wr.voxCache[m] = &voxelMesh{vbuf: vbuf, vbufMem: vmem, count: uint32(len(data) / worldgeo.VoxelPackedVertexSize)}
	return nil
}

// recordVoxels appends this frame's voxel-model draws inside the (already
// begun) world render pass. Viewport/scissor are the full-res ones the
// caller set for the textured geometry.
func (r *Renderer) recordVoxels(cmd vk.CommandBuffer, sl *worldSlot, g *worldgeo.Geometry, cam worldgeo.Camera) {
	wr := r.wr
	if wr.pipeVoxel == nil || len(g.Voxels) == 0 {
		return
	}
	vk.CmdBindPipeline(cmd, vk.PipelineBindPointGraphics, wr.pipeVoxel)
	vk.CmdBindDescriptorSets(cmd, vk.PipelineBindPointGraphics, wr.plVoxel, 0, 1, []vk.DescriptorSet{sl.set1}, 0, nil)

	vp := worldgeo.ViewProj(cam, wr.w, wr.h) // once per frame, not per instance
	var pc [voxelPushBytes / 4]float32       // 28 float32
	for i := range g.Voxels {
		inst := g.Voxels[i]
		vm := wr.voxCache[inst.Model]
		if vm == nil || vm.count == 0 {
			continue
		}
		mvp := worldgeo.Mat4Mul(vp, worldgeo.VoxelModelMatrix(inst))
		copy(pc[0:16], mvp[:])
		pc[16], pc[17], pc[18], pc[19] = inst.X, inst.Y, inst.Z, inst.Scale
		pc[20], pc[21], pc[22], pc[23] = float32(math.Cos(float64(inst.Yaw))), float32(math.Sin(float64(inst.Yaw))), 0, 0
		fb := float32(0)
		if inst.FullBright {
			fb = 1
		}
		pc[24], pc[25], pc[26], pc[27] = inst.Light, fb, 0, 0

		vk.CmdPushConstants(cmd, wr.plVoxel, vk.ShaderStageFlags(vk.ShaderStageVertexBit),
			0, voxelPushVert, unsafe.Pointer(&pc[0]))
		vk.CmdPushConstants(cmd, wr.plVoxel, vk.ShaderStageFlags(vk.ShaderStageFragmentBit),
			voxelPushVert, voxelPushFrag, unsafe.Pointer(&pc[24]))
		vk.CmdBindVertexBuffers(cmd, 0, 1, []vk.Buffer{vm.vbuf}, []vk.DeviceSize{0})
		vk.CmdDraw(cmd, vm.count, 1, 0, 0)
	}
}

func (r *Renderer) destroyVoxelResources() {
	wr := r.wr
	if wr == nil {
		return
	}
	for _, vm := range wr.voxCache {
		if vm.vbuf != nil {
			vk.DestroyBuffer(r.device, vm.vbuf, nil)
		}
		if vm.vbufMem != nil {
			vk.FreeMemory(r.device, vm.vbufMem, nil)
		}
	}
	wr.voxCache = nil
	if wr.pipeVoxel != nil {
		vk.DestroyPipeline(r.device, wr.pipeVoxel, nil)
		wr.pipeVoxel = nil
	}
	if wr.plVoxel != nil {
		vk.DestroyPipelineLayout(r.device, wr.plVoxel, nil)
		wr.plVoxel = nil
	}
}
