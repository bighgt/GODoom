package vulkan

import (
	"cmp"
	_ "embed"
	"fmt"
	"log"
	"slices"
	"unsafe"

	vk "github.com/goki/vulkan"

	"twopointfive/assets"
	"twopointfive/assets/models"
	"twopointfive/render/worldgeo"
)

// Textured 3D models (MD2/MD3/DMD from a MODELDEF pack — assets/models) on
// the hardware path: the voxel pipeline's sibling. Each model frame a pack
// binds is one static device-local vertex buffer (position, normal, UV),
// uploaded in one batch at level load (WarmModels); each part's skin is an
// ordinary world texture (uploadTexture / texCache, set 0), and the
// World UBO rides in set 1 — the same two-set layout as the world pipeline,
// so drawing models leaves the world's descriptor bindings compatible.
// Per-instance transform + shading are push constants.

//go:embed shaders/model.vert.spv
var modelVertSPV []byte

//go:embed shaders/model.frag.spv
var modelFragSPV []byte

// Push constants, two non-overlapping ranges (see voxel's note):
//
//	[0,112)   vertex            — mat4 mvp (64) + model rows 0..2 (48)
//	[112,128) vertex + fragment — vec4 shade: light, fullbright, frame blend,
//	          additive alpha (0 = solid)
//
// Frame interpolation (GZDoom's): binding 1 is the frame being blended
// toward (ModelInstance.Next — the same buffer as binding 0 when not
// blending), and the vertex stage mixes position and normal by shade.z.
const (
	modelPushVert  = 112
	modelPushFrag  = 16
	modelPushBytes = modelPushVert + modelPushFrag // 128 — exactly the guaranteed minimum
	modelVertSize  = models.FloatsPerVertex * 4    // 32 bytes
)

func (r *Renderer) worldCreateModelPipeline() error {
	wr := r.wr
	layoutInfo := vk.PipelineLayoutCreateInfo{
		SType:                  vk.StructureTypePipelineLayoutCreateInfo,
		SetLayoutCount:         2,
		PSetLayouts:            []vk.DescriptorSetLayout{wr.dsl0, wr.dsl1},
		PushConstantRangeCount: 2,
		PPushConstantRanges: []vk.PushConstantRange{
			{StageFlags: vk.ShaderStageFlags(vk.ShaderStageVertexBit), Offset: 0, Size: modelPushVert},
			{StageFlags: vk.ShaderStageFlags(vk.ShaderStageVertexBit) | vk.ShaderStageFlags(vk.ShaderStageFragmentBit), Offset: modelPushVert, Size: modelPushFrag},
		},
	}
	var pl vk.PipelineLayout
	if res := vk.CreatePipelineLayout(r.device, &layoutInfo, nil, &pl); res != vk.Success {
		return fmt.Errorf("vulkan: model pipeline layout: %s", vk.Error(res))
	}
	wr.plModel = pl

	vert, err := r.createShaderModule(modelVertSPV)
	if err != nil {
		return fmt.Errorf("vulkan: model vert: %w", err)
	}
	defer vk.DestroyShaderModule(r.device, vert, nil)
	frag, err := r.createShaderModule(modelFragSPV)
	if err != nil {
		return fmt.Errorf("vulkan: model frag: %w", err)
	}
	defer vk.DestroyShaderModule(r.device, frag, nil)

	stages := []vk.PipelineShaderStageCreateInfo{
		{SType: vk.StructureTypePipelineShaderStageCreateInfo, Stage: vk.ShaderStageVertexBit, Module: vert, PName: nz("main")},
		{SType: vk.StructureTypePipelineShaderStageCreateInfo, Stage: vk.ShaderStageFragmentBit, Module: frag, PName: nz("main")},
	}
	vkAttrs := []vk.VertexInputAttributeDescription{
		{Location: 0, Binding: 0, Format: vk.FormatR32g32b32Sfloat, Offset: 0},
		{Location: 1, Binding: 0, Format: vk.FormatR32g32b32Sfloat, Offset: 12},
		{Location: 2, Binding: 0, Format: vk.FormatR32g32Sfloat, Offset: 24},
		{Location: 3, Binding: 1, Format: vk.FormatR32g32b32Sfloat, Offset: 0},  // blend-target position
		{Location: 4, Binding: 1, Format: vk.FormatR32g32b32Sfloat, Offset: 12}, // blend-target normal
	}
	bindings := []vk.VertexInputBindingDescription{
		{Binding: 0, Stride: modelVertSize, InputRate: vk.VertexInputRateVertex},
		{Binding: 1, Stride: modelVertSize, InputRate: vk.VertexInputRateVertex},
	}
	vertexInput := vk.PipelineVertexInputStateCreateInfo{
		SType:                           vk.StructureTypePipelineVertexInputStateCreateInfo,
		VertexBindingDescriptionCount:   uint32(len(bindings)),
		PVertexBindingDescriptions:      bindings,
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
		// Pack models don't agree on a winding (and several are open
		// shells — flames, capes, trails), so both faces draw.
		CullMode:  vk.CullModeFlags(vk.CullModeNone),
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
		Layout:              wr.plModel,
		RenderPass:          wr.rpOpaque, // render-pass compatible with rpWater too — see the voxel pipeline
		Subpass:             0,
	}
	// The additive variant (ModelInstance.Additive — ZDoom's RenderStyle
	// "Add", a fireball's glow): same shaders and state, but no depth write
	// and one:one colour blending, with alpha left out of the write mask so
	// it can't disturb the scene alpha's water-reflection tag — the same
	// blend/depth state as pipeParticleAdd (worldsetup.go).
	addDepth := depthStencil
	addDepth.DepthWriteEnable = vk.False
	addCBA := vk.PipelineColorBlendAttachmentState{
		BlendEnable:         vk.True,
		SrcColorBlendFactor: vk.BlendFactorOne,
		DstColorBlendFactor: vk.BlendFactorOne,
		ColorBlendOp:        vk.BlendOpAdd,
		SrcAlphaBlendFactor: vk.BlendFactorZero,
		DstAlphaBlendFactor: vk.BlendFactorOne,
		AlphaBlendOp:        vk.BlendOpAdd,
		ColorWriteMask: vk.ColorComponentFlags(vk.ColorComponentRBit) | vk.ColorComponentFlags(vk.ColorComponentGBit) |
			vk.ColorComponentFlags(vk.ColorComponentBBit),
	}
	addBlend := vk.PipelineColorBlendStateCreateInfo{
		SType: vk.StructureTypePipelineColorBlendStateCreateInfo, AttachmentCount: 1,
		PAttachments: []vk.PipelineColorBlendAttachmentState{addCBA},
	}
	addInfo := info
	addInfo.PDepthStencilState = &addDepth
	addInfo.PColorBlendState = &addBlend

	// The translucent-glass variant (ModelInstance.Alpha — the powerup
	// spheres): no depth write, src-alpha blending, alpha again left out
	// of the write mask — pipeParticleTrans's blend. No depth prepass
	// (unlike the voxel glass): a sphere model's inner parts should show
	// through its shell. recordModels sorts these far to near.
	glassCBA := addCBA
	glassCBA.SrcColorBlendFactor = vk.BlendFactorSrcAlpha
	glassCBA.DstColorBlendFactor = vk.BlendFactorOneMinusSrcAlpha
	glassBlend := vk.PipelineColorBlendStateCreateInfo{
		SType: vk.StructureTypePipelineColorBlendStateCreateInfo, AttachmentCount: 1,
		PAttachments: []vk.PipelineColorBlendAttachmentState{glassCBA},
	}
	glassInfo := addInfo
	glassInfo.PColorBlendState = &glassBlend

	out := make([]vk.Pipeline, 3)
	if res := vk.CreateGraphicsPipelines(r.device, nil, 3, []vk.GraphicsPipelineCreateInfo{info, addInfo, glassInfo}, nil, out); res != vk.Success {
		return fmt.Errorf("vulkan: model pipeline: %s", vk.Error(res))
	}
	wr.pipeModel, wr.pipeModelAdd, wr.pipeModelGlass = out[0], out[1], out[2]
	return nil
}

// WarmModels uploads every frame mesh and skin of a model set at level load
// — one staging buffer and one submit for all the meshes (the whole Doom
// pack is ~32 MB of vertices), then each skin as a world texture — so no
// first-sight upload stalls the render loop. Safe on a nil / non-hardware
// renderer.
func (r *Renderer) WarmModels(set *models.Set) {
	if r == nil || !r.hw || r.wr == nil || set == nil {
		return
	}
	wr := r.wr
	type pending struct {
		fm        *models.FrameMesh
		vbuf      vk.Buffer
		mem       vk.DeviceMemory
		off, size int
	}
	var todo []pending
	var scratch []byte
	var skins []*assets.RGBA
	seenSkin := map[*assets.RGBA]bool{}
	for _, fm := range set.AllFrames() {
		if fm == nil || fm.Count == 0 || wr.modelCache[fm] != nil {
			continue
		}
		data := unsafe.Slice((*byte)(unsafe.Pointer(&fm.Verts[0])), len(fm.Verts)*4)
		vbuf, mem, err := r.deviceLocalVertexBuffer(len(data))
		if err != nil {
			log.Printf("vulkan: model upload: %v", err)
			break
		}
		todo = append(todo, pending{fm, vbuf, mem, len(scratch), len(data)})
		scratch = append(scratch, data...)
	}
	for _, t := range set.Skins() {
		if !seenSkin[t] {
			seenSkin[t] = true
			skins = append(skins, t)
		}
	}
	if len(todo) > 0 {
		stage, stageMem, mapped, err := r.wHostBuffer(len(scratch), vk.BufferUsageFlags(vk.BufferUsageTransferSrcBit))
		if err == nil {
			vk.Memcopy(mapped, scratch)
			err = r.submitOneShot(func(cmd vk.CommandBuffer) {
				for _, p := range todo {
					vk.CmdCopyBuffer(cmd, stage, p.vbuf, 1, []vk.BufferCopy{{SrcOffset: vk.DeviceSize(p.off), Size: vk.DeviceSize(p.size)}})
				}
			})
			vk.UnmapMemory(r.device, stageMem)
			vk.DestroyBuffer(r.device, stage, nil)
			vk.FreeMemory(r.device, stageMem, nil)
		}
		for _, p := range todo {
			if err != nil {
				vk.DestroyBuffer(r.device, p.vbuf, nil)
				vk.FreeMemory(r.device, p.mem, nil)
				continue
			}
			wr.modelCache[p.fm] = &voxelMesh{vbuf: p.vbuf, vbufMem: p.mem, count: uint32(p.fm.Count)}
		}
		if err != nil {
			log.Printf("vulkan: model upload: %v", err)
		}
	}
	for _, t := range skins {
		if wr.texCache[t] == nil {
			if err := r.uploadTexture(&wr.texResidency, t, nil); err != nil {
				log.Printf("vulkan: model skin upload: %v", err)
			}
		}
	}
	log.Printf("vulkan: models — %d frame mesh(es), %d skin(s) resident", len(wr.modelCache), len(skins))
}

// syncModels uploads any mesh or skin referenced this frame that WarmModels
// didn't (it normally uploads everything). Drains the device only when
// there's genuinely new work.
func (r *Renderer) syncModels(g *worldgeo.Geometry) error {
	wr := r.wr
	pending := false
	for i := range g.Models {
		m := &g.Models[i]
		if (m.Mesh != nil && wr.modelCache[m.Mesh] == nil) || (m.Next != nil && wr.modelCache[m.Next] == nil) ||
			(m.Skin != nil && wr.texCache[m.Skin] == nil) {
			pending = true
			break
		}
	}
	if !pending {
		return nil
	}
	vk.DeviceWaitIdle(r.device)
	for i := range g.Models {
		m := &g.Models[i]
		if m.Skin != nil && wr.texCache[m.Skin] == nil {
			if err := r.uploadTexture(&wr.texResidency, m.Skin, nil); err != nil {
				return err
			}
		}
		for _, fm := range []*models.FrameMesh{m.Mesh, m.Next} {
			if err := r.uploadModelFrame(fm); err != nil {
				return err
			}
		}
	}
	return nil
}

// uploadModelFrame makes one frame mesh resident (no-op if it already is).
func (r *Renderer) uploadModelFrame(fm *models.FrameMesh) error {
	wr := r.wr
	if fm == nil || fm.Count == 0 || wr.modelCache[fm] != nil {
		return nil
	}
	data := unsafe.Slice((*byte)(unsafe.Pointer(&fm.Verts[0])), len(fm.Verts)*4)
	vbuf, mem, err := r.uploadVertexData(data)
	if err != nil {
		return err
	}
	wr.modelCache[fm] = &voxelMesh{vbuf: vbuf, vbufMem: mem, count: uint32(fm.Count)}
	return nil
}

// recordModels appends this frame's model draws inside the (already begun)
// world render pass — right after the voxels, before the particle passes:
// the solid parts first (opaque, depth-writing, like voxels), then the
// additive ones (pipeModelAdd), which test against that depth but don't
// write it, so their order among themselves doesn't matter, then the
// translucent-glass ones (pipeModelGlass), far to near.
func (r *Renderer) recordModels(cmd vk.CommandBuffer, sl *worldSlot, g *worldgeo.Geometry, cam worldgeo.Camera) {
	wr := r.wr
	if wr.pipeModel == nil || len(g.Models) == 0 {
		return
	}
	vk.CmdBindPipeline(cmd, vk.PipelineBindPointGraphics, wr.pipeModel)
	vk.CmdBindDescriptorSets(cmd, vk.PipelineBindPointGraphics, wr.plModel, 1, 1, []vk.DescriptorSet{sl.set1}, 0, nil)

	vp := worldgeo.ViewProj(cam, wr.w, wr.h)
	var pc [modelPushBytes / 4]float32
	var lastMesh, lastTo *voxelMesh
	var lastSkin *worldTex
	// Split the parts by pass: 0 solid, 1 additive, 2 glass. (All three
	// pipelines are created together, so pipeModel != nil covers them.)
	pass := func(inst *worldgeo.ModelInstance) int {
		switch {
		case inst.Additive > 0:
			return 1
		case inst.Alpha > 0:
			return 2
		}
		return 0
	}
	order := wr.modelOrderScratch[:0]
	for p := 0; p < 3; p++ {
		start := len(order)
		for i := range g.Models {
			if pass(&g.Models[i]) == p {
				order = append(order, i)
			}
		}
		if p == 2 {
			// Far to near by the part's world origin; stable, so one
			// object's parts keep their MODELDEF order.
			d2 := func(i int) float64 {
				m := &g.Models[i].Model
				dx, dy := float64(m[12])-cam.X, float64(m[13])-cam.Y
				return dx*dx + dy*dy
			}
			slices.SortStableFunc(order[start:], func(a, b int) int { return cmp.Compare(d2(b), d2(a)) })
		}
	}
	wr.modelOrderScratch = order

	drawn := 0
	bound := 0
	for _, i := range order {
		inst := &g.Models[i]
		if p := pass(inst); p != bound {
			pl := wr.pipeModelAdd
			if p == 2 {
				pl = wr.pipeModelGlass
			}
			vk.CmdBindPipeline(cmd, vk.PipelineBindPointGraphics, pl)
			bound = p
		}
		mm := wr.modelCache[inst.Mesh]
		wt := wr.texCache[inst.Skin]
		if mm == nil || mm.count == 0 || wt == nil {
			continue
		}
		to, lerp := mm, float32(0) // binding 1: the blend target, or itself
		if inst.Next != nil {
			if nm := wr.modelCache[inst.Next]; nm != nil && nm.count == mm.count {
				to, lerp = nm, inst.Lerp
			}
		}
		mvp := worldgeo.Mat4Mul(vp, inst.Model)
		copy(pc[0:16], mvp[:])
		// Rows 0..2 of the column-major model matrix.
		for row := 0; row < 3; row++ {
			for col := 0; col < 4; col++ {
				pc[16+row*4+col] = inst.Model[col*4+row]
			}
		}
		fb := float32(0)
		if inst.FullBright {
			fb = 1
		}
		style := inst.Additive // shade.w: > 0 additive alpha, < 0 glass alpha
		if bound == 2 {
			style = -inst.Alpha
		}
		pc[28], pc[29], pc[30], pc[31] = inst.Light, fb, lerp, style
		vk.CmdPushConstants(cmd, wr.plModel, vk.ShaderStageFlags(vk.ShaderStageVertexBit), 0, modelPushVert, unsafe.Pointer(&pc[0]))
		vk.CmdPushConstants(cmd, wr.plModel, vk.ShaderStageFlags(vk.ShaderStageVertexBit)|vk.ShaderStageFlags(vk.ShaderStageFragmentBit),
			modelPushVert, modelPushFrag, unsafe.Pointer(&pc[28]))
		if wt != lastSkin {
			vk.CmdBindDescriptorSets(cmd, vk.PipelineBindPointGraphics, wr.plModel, 0, 1, []vk.DescriptorSet{wt.set}, 0, nil)
			lastSkin = wt
		}
		if mm != lastMesh || to != lastTo {
			vk.CmdBindVertexBuffers(cmd, 0, 2, []vk.Buffer{mm.vbuf, to.vbuf}, []vk.DeviceSize{0, 0})
			lastMesh, lastTo = mm, to
		}
		vk.CmdDraw(cmd, mm.count, 1, 0, 0)
		drawn++
	}
	if wr.modelDbg == 0 && drawn > 0 {
		wr.modelDbg = 1
		log.Printf("vulkan: 3D models — drew %d of %d part(s) this frame, %d frame mesh(es) resident", drawn, len(g.Models), len(wr.modelCache))
	}
}

func (r *Renderer) destroyModelResources() {
	wr := r.wr
	if wr == nil {
		return
	}
	for _, mm := range wr.modelCache {
		mm.free(r.device)
	}
	wr.modelCache = nil
	if wr.pipeModel != nil {
		vk.DestroyPipeline(r.device, wr.pipeModel, nil)
		wr.pipeModel = nil
	}
	if wr.pipeModelGlass != nil {
		vk.DestroyPipeline(r.device, wr.pipeModelGlass, nil)
		wr.pipeModelGlass = nil
	}
	if wr.pipeModelAdd != nil {
		vk.DestroyPipeline(r.device, wr.pipeModelAdd, nil)
		wr.pipeModelAdd = nil
	}
	if wr.plModel != nil {
		vk.DestroyPipelineLayout(r.device, wr.plModel, nil)
		wr.plModel = nil
	}
}
