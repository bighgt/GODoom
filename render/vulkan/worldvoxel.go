package vulkan

import (
	_ "embed"
	"encoding/binary"
	"fmt"
	"log"
	"math"
	"runtime"
	"slices"
	"sync"
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
//
//	[0,96)   vertex   — mat4 mvp (64) + vec4 trans + vec4 rot (32);
//	                    rot.z = the mesh's model-space centre height,
//	                    rot.w = VoxelInstance.Sector (dynamic-light reach)
//	[96,112) fragment — vec4 shade (light, fullbright, glass alpha, -)
const (
	voxelPushVert  = 96
	voxelPushFrag  = 16
	voxelPushBytes = voxelPushVert + voxelPushFrag // 112, within the 128 min
)

type voxelMesh struct {
	vbuf    vk.Buffer
	vbufMem vk.DeviceMemory
	count   uint32  // vertices (0 = model meshed to nothing; skip)
	centerZ float32 // model-space height of the mesh's vertical midpoint (voxel units)
}

// voxelMeshCenterZ is the midpoint of a packed voxel mesh's vertex z range
// — the centre the translucent-glass shading (voxel.frag) measures its
// round normal from. A KVX model's pivot already centres it in x/y.
func voxelMeshCenterZ(data []byte) float32 {
	lo, hi := float32(math.Inf(1)), float32(math.Inf(-1))
	for o := 8; o+4 <= len(data); o += worldgeo.VoxelPackedVertexSize {
		z := math.Float32frombits(binary.LittleEndian.Uint32(data[o:]))
		lo, hi = min(lo, z), max(hi, z)
	}
	if lo > hi {
		return 0
	}
	return (lo + hi) / 2
}

func (vm *voxelMesh) free(dev vk.Device) {
	if vm.vbuf != nil {
		vk.DestroyBuffer(dev, vm.vbuf, nil)
	}
	if vm.vbufMem != nil {
		vk.FreeMemory(dev, vm.vbufMem, nil)
	}
}

// worldCreateVoxelPipeline builds the voxel model pipeline + its layout.
// Run as a NewWorld step after worldCreatePipelines (needs dsl1 + rpOpaque).
func (r *Renderer) worldCreateVoxelPipeline() error {
	wr := r.wr

	layoutInfo := vk.PipelineLayoutCreateInfo{
		SType:                  vk.StructureTypePipelineLayoutCreateInfo,
		SetLayoutCount:         1,
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
		// rpOpaque and rpWater are render-pass compatible (identical
		// attachment formats/sample counts/subpass shape — only
		// LoadOp/InitialLayout/FinalLayout differ, none of which affect
		// compatibility), so a pipeline created against one is valid to
		// bind during an instance of the other. Voxels actually draw
		// inside rpWater (waterRefraction on) or the single retained
		// rpOpaque (off) — see recordWorldCommandBuffer — either way this
		// pipeline object works.
		RenderPass: wr.rpOpaque,
		Subpass:    0,
	}

	// Translucent glass (VoxelInstance.Alpha — the powerup spheres), two
	// passes per instance, see recordVoxels: a depth-only prepass (colour
	// writes off) lays down the model's nearest surface, then the blended
	// pass (same depth test, no depth write) colours only that surface — so
	// a voxel mesh's own overlapping faces can't blend over each other in
	// arbitrary order. Alpha is left out of the write mask so the scene
	// alpha's water/sky tag survives — the same blend as pipeParticleTrans.
	depthOnlyCBA := vk.PipelineColorBlendAttachmentState{}
	depthOnlyBlend := vk.PipelineColorBlendStateCreateInfo{
		SType: vk.StructureTypePipelineColorBlendStateCreateInfo, AttachmentCount: 1,
		PAttachments: []vk.PipelineColorBlendAttachmentState{depthOnlyCBA},
	}
	depthInfo := info
	depthInfo.PColorBlendState = &depthOnlyBlend

	glassDepth := depthStencil
	glassDepth.DepthWriteEnable = vk.False
	glassCBA := vk.PipelineColorBlendAttachmentState{
		BlendEnable:         vk.True,
		SrcColorBlendFactor: vk.BlendFactorSrcAlpha,
		DstColorBlendFactor: vk.BlendFactorOneMinusSrcAlpha,
		ColorBlendOp:        vk.BlendOpAdd,
		SrcAlphaBlendFactor: vk.BlendFactorZero,
		DstAlphaBlendFactor: vk.BlendFactorOne,
		AlphaBlendOp:        vk.BlendOpAdd,
		ColorWriteMask: vk.ColorComponentFlags(vk.ColorComponentRBit) | vk.ColorComponentFlags(vk.ColorComponentGBit) |
			vk.ColorComponentFlags(vk.ColorComponentBBit),
	}
	glassBlend := vk.PipelineColorBlendStateCreateInfo{
		SType: vk.StructureTypePipelineColorBlendStateCreateInfo, AttachmentCount: 1,
		PAttachments: []vk.PipelineColorBlendAttachmentState{glassCBA},
	}
	glassInfo := info
	glassInfo.PDepthStencilState = &glassDepth
	glassInfo.PColorBlendState = &glassBlend

	out := make([]vk.Pipeline, 3)
	if res := vk.CreateGraphicsPipelines(r.device, nil, 3, []vk.GraphicsPipelineCreateInfo{info, depthInfo, glassInfo}, nil, out); res != vk.Success {
		return fmt.Errorf("vulkan: voxel pipeline: %s", vk.Error(res))
	}
	wr.pipeVoxel, wr.pipeVoxelDepth, wr.pipeVoxelGlass = out[0], out[1], out[2]
	return nil
}

// WarmVoxels meshes + uploads every model up front — one shared staging
// buffer and a single command submit for the whole pack, rather than a
// device stall per model on first sight in the render loop (Cheello's Doom1
// pack has ~300 distinct models). Called at level load on the hardware
// path; safe on a nil / non-hardware renderer.
//
// The meshing itself (worldgeo.BuildVoxelMesh: an occupancy flood-fill plus
// exposed-face emission per model) is pure CPU work with no Vulkan calls and
// no shared state between models, and dominates this function's cost — on a
// ~300-model pack it was measured taking several seconds run one model at a
// time, most of a level load's total time. It's spread across every core
// below; only that meshing step is concurrent — buffer creation and the
// GPU upload after it still run on this goroutine exactly as before.
func (r *Renderer) WarmVoxels(models []*assets.VoxelModel) {
	if r == nil || !r.hw || r.wr == nil {
		return
	}
	wr := r.wr

	type job struct {
		m    *assets.VoxelModel
		data []byte
	}
	jobs := make([]job, 0, len(models))
	for _, m := range models {
		if m == nil || wr.voxCache[m] != nil {
			continue
		}
		jobs = append(jobs, job{m: m})
	}
	if len(jobs) == 0 {
		return
	}

	workers := runtime.GOMAXPROCS(0)
	if workers > len(jobs) {
		workers = len(jobs)
	}
	next := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				jobs[i].data = worldgeo.BuildVoxelMesh(jobs[i].m)
			}
		}()
	}
	for i := range jobs {
		next <- i
	}
	close(next)
	wg.Wait()

	type pending struct {
		m       *assets.VoxelModel
		vbuf    vk.Buffer
		vbufMem vk.DeviceMemory
		off     int
		size    int
		count   uint32
		centerZ float32
	}
	var todo []pending
	var scratch []byte // all meshes concatenated, staged in one buffer

	for _, j := range jobs {
		if len(j.data) == 0 {
			wr.voxCache[j.m] = &voxelMesh{}
			continue
		}
		vbuf, vmem, err := r.deviceLocalVertexBuffer(len(j.data))
		if err != nil {
			return
		}
		todo = append(todo, pending{m: j.m, vbuf: vbuf, vbufMem: vmem, off: len(scratch), size: len(j.data),
			count: uint32(len(j.data) / worldgeo.VoxelPackedVertexSize), centerZ: voxelMeshCenterZ(j.data)})
		scratch = append(scratch, j.data...)
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
		wr.voxCache[p.m] = &voxelMesh{vbuf: p.vbuf, vbufMem: p.vbufMem, count: p.count, centerZ: p.centerZ}
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
// that isn't resident in tr. Drains the device first, but only when there
// is genuinely new work. The iso path calls this lazily (mesh-on-first-
// sight) rather than via a WarmVoxels-style upfront batch.
func (r *Renderer) syncVoxelMeshes(tr *texResidency, g *worldgeo.Geometry) error {
	pending := false
	for i := range g.Voxels {
		if m := g.Voxels[i].Model; m != nil && tr.voxCache[m] == nil {
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
		if m == nil || tr.voxCache[m] != nil {
			continue
		}
		if err := r.uploadVoxelMesh(tr, m); err != nil {
			return err
		}
	}
	return nil
}

func (r *Renderer) uploadVoxelMesh(tr *texResidency, m *assets.VoxelModel) error {
	data := worldgeo.BuildVoxelMesh(m)
	if len(data) == 0 {
		tr.voxCache[m] = &voxelMesh{} // meshed to nothing — cache the miss
		return nil
	}
	vbuf, vmem, err := r.uploadVertexData(data)
	if err != nil {
		return err
	}
	tr.voxCache[m] = &voxelMesh{vbuf: vbuf, vbufMem: vmem, count: uint32(len(data) / worldgeo.VoxelPackedVertexSize),
		centerZ: voxelMeshCenterZ(data)}
	return nil
}

// recordVoxels appends this frame's voxel-model draws inside the (already
// begun) world render pass: the solid instances first, then the
// translucent-glass ones (VoxelInstance.Alpha) far to near, each as a
// depth prepass plus a blended pass. Viewport/scissor are the full-res ones
// the caller set for the textured geometry.
func (r *Renderer) recordVoxels(cmd vk.CommandBuffer, sl *worldSlot, g *worldgeo.Geometry, cam worldgeo.Camera) {
	wr := r.wr
	if wr.pipeVoxel == nil || len(g.Voxels) == 0 {
		return
	}
	vk.CmdBindPipeline(cmd, vk.PipelineBindPointGraphics, wr.pipeVoxel)
	vk.CmdBindDescriptorSets(cmd, vk.PipelineBindPointGraphics, wr.plVoxel, 0, 1, []vk.DescriptorSet{sl.set1}, 0, nil)

	vp := worldgeo.ViewProj(cam, wr.w, wr.h) // once per frame, not per instance
	nDrawn, nUncached := 0, 0
	// lastVM tracks the model bound by the previous drawn instance: a
	// pack of identical monsters/items shares one model, so consecutive
	// instances very often don't need a new vertex-buffer bind at all.
	// haveLast (rather than comparing lastVM to nil) forces the first
	// instance to always bind.
	var lastVM *voxelMesh
	haveLast := false
	bind := func(vm *voxelMesh) {
		if !haveLast || vm != lastVM {
			vk.CmdBindVertexBuffers(cmd, 0, 1, []vk.Buffer{vm.vbuf}, []vk.DeviceSize{0})
			lastVM, haveLast = vm, true
		}
	}
	glass := wr.glassScratch[:0]
	for i := range g.Voxels {
		inst := &g.Voxels[i]
		vm := wr.voxCache[inst.Model]
		if vm == nil || vm.count == 0 {
			if vm == nil {
				nUncached++
			}
			continue
		}
		nDrawn++
		if inst.Alpha > 0 && wr.pipeVoxelGlass != nil {
			glass = append(glass, i)
			continue
		}
		r.pushVoxel(cmd, vp, inst, vm)
		bind(vm)
		vk.CmdDraw(cmd, vm.count, 1, 0, 0)
	}

	if len(glass) > 0 {
		// Far to near, so each glass model blends over everything behind it.
		dist2 := func(i int) float64 {
			dx, dy := float64(g.Voxels[i].X)-cam.X, float64(g.Voxels[i].Y)-cam.Y
			return dx*dx + dy*dy
		}
		slices.SortFunc(glass, func(a, b int) int {
			da, db := dist2(a), dist2(b)
			switch {
			case da > db:
				return -1
			case da < db:
				return 1
			}
			return 0
		})
		for _, i := range glass {
			inst := &g.Voxels[i]
			vm := wr.voxCache[inst.Model]
			r.pushVoxel(cmd, vp, inst, vm)
			bind(vm)
			vk.CmdBindPipeline(cmd, vk.PipelineBindPointGraphics, wr.pipeVoxelDepth)
			vk.CmdDraw(cmd, vm.count, 1, 0, 0)
			vk.CmdBindPipeline(cmd, vk.PipelineBindPointGraphics, wr.pipeVoxelGlass)
			vk.CmdDraw(cmd, vm.count, 1, 0, 0)
		}
	}
	wr.glassScratch = glass

	// One line, the first frame any voxel model reaches the GPU, so the log
	// makes it unambiguous whether the hardware path is drawing them — the
	// mirror of engine.drawMobjs' software-path "map objects this frame" line.
	if wr.voxDbg == 0 && nDrawn > 0 {
		wr.voxDbg = 1
		log.Printf("vulkan: voxel models — drew %d of %d instance(s) this frame (%d not yet meshed), %d model(s) resident",
			nDrawn, len(g.Voxels), nUncached, len(wr.voxCache))
	}
}

// pushVoxel pushes inst's transform and shading (see the push-constant
// layout above).
func (r *Renderer) pushVoxel(cmd vk.CommandBuffer, vp [16]float32, inst *worldgeo.VoxelInstance, vm *voxelMesh) {
	var pc [voxelPushBytes / 4]float32 // 28 float32
	mvp := worldgeo.Mat4Mul(vp, worldgeo.VoxelModelMatrix(*inst))
	copy(pc[0:16], mvp[:])
	pc[16], pc[17], pc[18], pc[19] = inst.X, inst.Y, inst.Z, inst.Scale
	pc[20], pc[21], pc[22], pc[23] = float32(math.Cos(float64(inst.Yaw))), float32(math.Sin(float64(inst.Yaw))), vm.centerZ, float32(inst.Sector)
	fb := float32(0)
	if inst.FullBright {
		fb = 1
	}
	pc[24], pc[25], pc[26], pc[27] = inst.Light, fb, inst.Alpha, 0
	vk.CmdPushConstants(cmd, r.wr.plVoxel, vk.ShaderStageFlags(vk.ShaderStageVertexBit),
		0, voxelPushVert, unsafe.Pointer(&pc[0]))
	vk.CmdPushConstants(cmd, r.wr.plVoxel, vk.ShaderStageFlags(vk.ShaderStageFragmentBit),
		voxelPushVert, voxelPushFrag, unsafe.Pointer(&pc[24]))
}

func (r *Renderer) destroyVoxelResources() {
	wr := r.wr
	if wr == nil {
		return
	}
	for _, p := range []*vk.Pipeline{&wr.pipeVoxel, &wr.pipeVoxelDepth, &wr.pipeVoxelGlass} {
		if *p != nil {
			vk.DestroyPipeline(r.device, *p, nil)
			*p = nil
		}
	}
	if wr.plVoxel != nil {
		vk.DestroyPipelineLayout(r.device, wr.plVoxel, nil)
		wr.plVoxel = nil
	}
}
