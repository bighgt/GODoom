package vulkan

import (
	"fmt"
	"unsafe"

	vk "github.com/goki/vulkan"

	"twopointfive/render"
	"twopointfive/render/worldgeo"
)

// Real (cube) shadow maps for a small budget of this frame's leading
// dynamic lights (config realShadows) — additive to (not a replacement
// for) worldambient.go's always-on screen-space dynShadow term. Each
// shadow-casting light gets a depth-only cube map rendered from its own
// world position (6 faces, one render pass each), storing linear
// light-to-fragment distance (not projective depth) via shadow.frag's
// gl_FragDepth write — the standard point-shadow technique, camera-
// agnostic since it never depends on what the main viewing camera can see.
// world.frag's shadowCubeSample() samples it back with a plain
// distance-vs-stored-distance comparison.
//
// Slot assignment is deliberately simple: shadow-casting light i is always
// lights[i] for i < shadowLightSlots — collectLights already orders the
// gameplay-critical dynamic emitters first, then the nearest static ones
// (see its own doc comment), so "the first two lights in the array" is
// already "the two most important lights this frame" without this package
// needing its own prioritization pass.

// shadowMapSize is each cube face's resolution — modest and fixed for v1,
// not user-configurable yet (see config.RealShadows's own doc comment).
const shadowMapSize = 512

// shadowLightSlots is the shadow-casting light budget: each slot costs 6
// depth-only draws of the static geometry, so this stays small.
const shadowLightSlots = 2

// shadowCubeTarget is one light's cube depth map: the 6-layer, cube-
// compatible D32 image, one VK_IMAGE_VIEW_TYPE_CUBE view for sampling it in
// world.frag, and (only when built as a render target, i.e. not the dummy)
// six single-layer 2D views + framebuffers, one per face, for rendering
// into it.
type shadowCubeTarget struct {
	image    vk.Image
	memory   vk.DeviceMemory
	cubeView vk.ImageView
	faces    [6]shadowFace
}

type shadowFace struct {
	view vk.ImageView
	fb   vk.Framebuffer
}

// shadowBudget decides, from this frame's light list, how many leading
// lights (0, 1, or shadowLightSlots) get a real shadow map and what far
// plane their cube maps share. Called from both uploadWorldFrame (so the
// World UBO's ShadowCount/ShadowFar match) and recordShadowMaps (so what's
// actually rendered matches) — factored out once so the two can never
// drift apart.
func shadowBudget(wr *worldResources, lights []render.Light) (n int, far float32) {
	if !wr.realShadows {
		return 0, 0
	}
	n = len(lights)
	if n > shadowLightSlots {
		n = shadowLightSlots
	}
	if n == 0 {
		return 0, 0
	}
	for i := 0; i < n; i++ {
		if lights[i].Radius > far {
			far = lights[i].Radius
		}
	}
	if far < 64 {
		far = 64 // a degenerate/tiny radius would otherwise crush the whole map into a few depth steps
	}
	return n, far
}

// worldCreateShadowRenderPass builds rpShadow: a depth-only render pass (no
// colour attachment at all) for one cube-map face. No-op when !realShadows.
func (r *Renderer) worldCreateShadowRenderPass() error {
	wr := r.wr
	if !wr.realShadows {
		return nil
	}
	depth := vk.AttachmentDescription{
		Format:         vk.FormatD32Sfloat,
		Samples:        vk.SampleCount1Bit,
		LoadOp:         vk.AttachmentLoadOpClear,
		StoreOp:        vk.AttachmentStoreOpStore,
		StencilLoadOp:  vk.AttachmentLoadOpDontCare,
		StencilStoreOp: vk.AttachmentStoreOpDontCare,
		InitialLayout:  vk.ImageLayoutUndefined, // discards whatever the last frame left here — no per-face layout tracking needed
		FinalLayout:    vk.ImageLayoutDepthStencilAttachmentOptimal,
	}
	depthRef := vk.AttachmentReference{Attachment: 0, Layout: vk.ImageLayoutDepthStencilAttachmentOptimal}
	sub := vk.SubpassDescription{
		PipelineBindPoint:       vk.PipelineBindPointGraphics,
		PDepthStencilAttachment: &depthRef,
	}
	deps := []vk.SubpassDependency{
		{ // last frame's colour-pass sampling read of this face finishes before we clear/write it again
			SrcSubpass:    vk.SubpassExternal,
			DstSubpass:    0,
			SrcStageMask:  vk.PipelineStageFlags(vk.PipelineStageFragmentShaderBit),
			DstStageMask:  vk.PipelineStageFlags(vk.PipelineStageEarlyFragmentTestsBit) | vk.PipelineStageFlags(vk.PipelineStageLateFragmentTestsBit),
			SrcAccessMask: vk.AccessFlags(vk.AccessShaderReadBit),
			DstAccessMask: vk.AccessFlags(vk.AccessDepthStencilAttachmentWriteBit),
		},
		{ // our write is visible to whatever reads it next (an explicit barrier() in recordShadowMaps, not this pass's own FinalLayout)
			SrcSubpass:    0,
			DstSubpass:    vk.SubpassExternal,
			SrcStageMask:  vk.PipelineStageFlags(vk.PipelineStageLateFragmentTestsBit),
			DstStageMask:  vk.PipelineStageFlags(vk.PipelineStageFragmentShaderBit),
			SrcAccessMask: vk.AccessFlags(vk.AccessDepthStencilAttachmentWriteBit),
			DstAccessMask: vk.AccessFlags(vk.AccessShaderReadBit),
		},
	}
	info := vk.RenderPassCreateInfo{
		SType:           vk.StructureTypeRenderPassCreateInfo,
		AttachmentCount: 1,
		PAttachments:    []vk.AttachmentDescription{depth},
		SubpassCount:    1,
		PSubpasses:      []vk.SubpassDescription{sub},
		DependencyCount: uint32(len(deps)),
		PDependencies:   deps,
	}
	var rp vk.RenderPass
	if res := vk.CreateRenderPass(r.device, &info, nil, &rp); res != vk.Success {
		return fmt.Errorf("vulkan: shadow render pass: %s", vk.Error(res))
	}
	wr.rpShadow = rp
	return nil
}

// wMakeCubeImage allocates a 6-layer, cube-compatible D32 image plus its
// cube sampling view, and — when attachment is true — six single-layer 2D
// views and framebuffers (one per face) against wr.rpShadow for rendering
// into it. attachment is false only for the always-built 1x1 dummy (never
// rendered into, just sampled as a harmless fallback).
func (r *Renderer) wMakeCubeImage(size int, attachment bool) (shadowCubeTarget, error) {
	var t shadowCubeTarget
	usage := vk.ImageUsageFlags(vk.ImageUsageSampledBit)
	if attachment {
		usage |= vk.ImageUsageFlags(vk.ImageUsageDepthStencilAttachmentBit)
	}
	info := vk.ImageCreateInfo{
		SType:         vk.StructureTypeImageCreateInfo,
		Flags:         vk.ImageCreateFlags(vk.ImageCreateCubeCompatibleBit),
		ImageType:     vk.ImageType2d,
		Format:        vk.FormatD32Sfloat,
		Extent:        vk.Extent3D{Width: uint32(size), Height: uint32(size), Depth: 1},
		MipLevels:     1,
		ArrayLayers:   6,
		Samples:       vk.SampleCount1Bit,
		Tiling:        vk.ImageTilingOptimal,
		Usage:         usage,
		SharingMode:   vk.SharingModeExclusive,
		InitialLayout: vk.ImageLayoutUndefined,
	}
	var image vk.Image
	if res := vk.CreateImage(r.device, &info, nil, &image); res != vk.Success {
		return t, fmt.Errorf("vulkan: shadow cube image: %s", vk.Error(res))
	}
	t.image = image

	var memReq vk.MemoryRequirements
	vk.GetImageMemoryRequirements(r.device, image, &memReq)
	memReq.Deref()
	mt, err := r.findMemoryType(memReq.MemoryTypeBits, vk.MemoryPropertyFlags(vk.MemoryPropertyDeviceLocalBit))
	if err != nil {
		return t, fmt.Errorf("vulkan: shadow cube memory: %w", err)
	}
	alloc := vk.MemoryAllocateInfo{SType: vk.StructureTypeMemoryAllocateInfo, AllocationSize: memReq.Size, MemoryTypeIndex: mt}
	var mem vk.DeviceMemory
	if res := vk.AllocateMemory(r.device, &alloc, nil, &mem); res != vk.Success {
		return t, fmt.Errorf("vulkan: shadow cube alloc: %s", vk.Error(res))
	}
	t.memory = mem
	if res := vk.BindImageMemory(r.device, image, mem, 0); res != vk.Success {
		return t, fmt.Errorf("vulkan: shadow cube bind: %s", vk.Error(res))
	}

	cubeViewInfo := vk.ImageViewCreateInfo{
		SType:            vk.StructureTypeImageViewCreateInfo,
		Image:            image,
		ViewType:         vk.ImageViewTypeCube,
		Format:           vk.FormatD32Sfloat,
		SubresourceRange: vk.ImageSubresourceRange{AspectMask: vk.ImageAspectFlags(vk.ImageAspectDepthBit), LevelCount: 1, LayerCount: 6},
	}
	if res := vk.CreateImageView(r.device, &cubeViewInfo, nil, &t.cubeView); res != vk.Success {
		return t, fmt.Errorf("vulkan: shadow cube view: %s", vk.Error(res))
	}

	if attachment {
		for f := 0; f < 6; f++ {
			faceInfo := vk.ImageViewCreateInfo{
				SType:    vk.StructureTypeImageViewCreateInfo,
				Image:    image,
				ViewType: vk.ImageViewType2d,
				Format:   vk.FormatD32Sfloat,
				SubresourceRange: vk.ImageSubresourceRange{
					AspectMask: vk.ImageAspectFlags(vk.ImageAspectDepthBit), BaseArrayLayer: uint32(f), LayerCount: 1, LevelCount: 1,
				},
			}
			var fv vk.ImageView
			if res := vk.CreateImageView(r.device, &faceInfo, nil, &fv); res != vk.Success {
				return t, fmt.Errorf("vulkan: shadow face view: %s", vk.Error(res))
			}
			fbInfo := vk.FramebufferCreateInfo{
				SType:           vk.StructureTypeFramebufferCreateInfo,
				RenderPass:      r.wr.rpShadow,
				AttachmentCount: 1,
				PAttachments:    []vk.ImageView{fv},
				Width:           uint32(size),
				Height:          uint32(size),
				Layers:          1,
			}
			var fb vk.Framebuffer
			if res := vk.CreateFramebuffer(r.device, &fbInfo, nil, &fb); res != vk.Success {
				return t, fmt.Errorf("vulkan: shadow framebuffer: %s", vk.Error(res))
			}
			t.faces[f] = shadowFace{view: fv, fb: fb}
		}
	}
	return t, nil
}

// worldCreateShadowTargets builds the always-present 1x1 dummy cube (so
// dsl1's two samplerCube bindings are always legal, even with realShadows
// off or a frame with no shadow-casting light) and, only when realShadows
// is on, each slot's real shadowCube[shadowLightSlots].
func (r *Renderer) worldCreateShadowTargets() error {
	wr := r.wr

	dummy, err := r.wMakeCubeImage(1, false)
	if err != nil {
		return err
	}
	wr.dummyShadowCube = dummy
	// One-shot layout transition only — its contents are never read
	// meaningfully (world.frag never actually samples it; it's just a
	// legal fallback binding), so nothing needs to be uploaded into it.
	if err := r.submitOneShot(func(cmd vk.CommandBuffer) {
		rng := vk.ImageSubresourceRange{AspectMask: vk.ImageAspectFlags(vk.ImageAspectDepthBit), LevelCount: 1, LayerCount: 6}
		barrier(cmd, dummy.image, rng, vk.ImageLayoutUndefined, vk.ImageLayoutShaderReadOnlyOptimal,
			0, vk.AccessFlags(vk.AccessShaderReadBit),
			vk.PipelineStageFlags(vk.PipelineStageTopOfPipeBit), vk.PipelineStageFlags(vk.PipelineStageFragmentShaderBit))
	}); err != nil {
		return err
	}

	if !wr.realShadows {
		return nil
	}
	for i := range wr.slots {
		sl := &wr.slots[i]
		for s := 0; s < shadowLightSlots; s++ {
			t, err := r.wMakeCubeImage(shadowMapSize, true)
			if err != nil {
				return err
			}
			sl.shadowCube[s] = t
		}
	}
	return nil
}

// worldCreateShadowPipeline builds plShadow (push-constant only — no
// descriptor sets, the shadow pass needs no textures/UBO) + pipeShadow
// (shadow.vert/frag against rpShadow). No-op when !realShadows.
func (r *Renderer) worldCreateShadowPipeline() error {
	wr := r.wr
	if !wr.realShadows {
		return nil
	}
	layoutInfo := vk.PipelineLayoutCreateInfo{
		SType:                  vk.StructureTypePipelineLayoutCreateInfo,
		PushConstantRangeCount: 1,
		PPushConstantRanges: []vk.PushConstantRange{{
			StageFlags: vk.ShaderStageFlags(vk.ShaderStageVertexBit) | vk.ShaderStageFlags(vk.ShaderStageFragmentBit),
			Offset:     0, Size: 80, // mat4 viewProj + vec4 lightPosFar
		}},
	}
	var layout vk.PipelineLayout
	if res := vk.CreatePipelineLayout(r.device, &layoutInfo, nil, &layout); res != vk.Success {
		return fmt.Errorf("vulkan: shadow pipeline layout: %s", vk.Error(res))
	}
	wr.plShadow = layout

	vert, err := r.createShaderModule(shadowVertSPV)
	if err != nil {
		return fmt.Errorf("vulkan: shadow vert: %w", err)
	}
	defer vk.DestroyShaderModule(r.device, vert, nil)
	frag, err := r.createShaderModule(shadowFragSPV)
	if err != nil {
		return fmt.Errorf("vulkan: shadow frag: %w", err)
	}
	defer vk.DestroyShaderModule(r.device, frag, nil)

	stages := []vk.PipelineShaderStageCreateInfo{
		{SType: vk.StructureTypePipelineShaderStageCreateInfo, Stage: vk.ShaderStageVertexBit, Module: vert, PName: nz("main")},
		{SType: vk.StructureTypePipelineShaderStageCreateInfo, Stage: vk.ShaderStageFragmentBit, Module: frag, PName: nz("main")},
	}
	binding := vk.VertexInputBindingDescription{Binding: 0, Stride: uint32(worldgeo.PackedVertexSize), InputRate: vk.VertexInputRateVertex}
	vertexInput := vk.PipelineVertexInputStateCreateInfo{
		SType:                           vk.StructureTypePipelineVertexInputStateCreateInfo,
		VertexBindingDescriptionCount:   1,
		PVertexBindingDescriptions:      []vk.VertexInputBindingDescription{binding},
		VertexAttributeDescriptionCount: 1,
		PVertexAttributeDescriptions: []vk.VertexInputAttributeDescription{
			{Location: 0, Binding: 0, Format: vk.FormatR32g32b32Sfloat, Offset: 0},
		},
	}
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
	depthStencil := vk.PipelineDepthStencilStateCreateInfo{
		SType:           vk.StructureTypePipelineDepthStencilStateCreateInfo,
		DepthTestEnable: vk.True, DepthWriteEnable: vk.True, DepthCompareOp: vk.CompareOpLess,
	}
	colorBlend := vk.PipelineColorBlendStateCreateInfo{SType: vk.StructureTypePipelineColorBlendStateCreateInfo} // no colour attachments at all
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
		Layout:              wr.plShadow,
		RenderPass:          wr.rpShadow,
		Subpass:             0,
	}
	out := make([]vk.Pipeline, 1)
	if res := vk.CreateGraphicsPipelines(r.device, nil, 1, []vk.GraphicsPipelineCreateInfo{info}, nil, out); res != vk.Success {
		return fmt.Errorf("vulkan: shadow pipeline: %s", vk.Error(res))
	}
	wr.pipeShadow = out[0]
	return nil
}

// recordShadowMaps renders this frame's shadow-casting lights' cube depth
// maps (see shadowBudget) into sl.shadowCube[], level geometry only
// (g.StaticDraws' opaque prefix plus the live sectors' solid walls and
// flats, g.LiveDraws[:LiveOpaqueDraws] — no sprites/voxels/particles: walls/
// floors/ceilings are what matters for room-shadowing, and skipping the
// rest keeps the 6-or-12 extra draws this costs cheap). No-op when
// !wr.realShadows or there's nothing to cast from.
func (r *Renderer) recordShadowMaps(cmd vk.CommandBuffer, frame int, g *worldgeo.Geometry, lights []render.Light) {
	wr := r.wr
	if wr.staticVBO == nil || g.StaticOpaqueCount == 0 {
		return
	}
	n, far := shadowBudget(wr, lights)
	if n == 0 {
		return
	}
	sl := &wr.slots[frame]

	vk.CmdBindPipeline(cmd, vk.PipelineBindPointGraphics, wr.pipeShadow)
	vk.CmdSetViewport(cmd, 0, 1, []vk.Viewport{fullViewport(shadowMapSize, shadowMapSize)})
	vk.CmdSetScissor(cmd, 0, 1, []vk.Rect2D{fullScissor(shadowMapSize, shadowMapSize)})

	for i := 0; i < n; i++ {
		l := lights[i]
		target := &sl.shadowCube[i]
		for face := 0; face < 6; face++ {
			vp := worldgeo.CubeFaceViewProj([3]float64{float64(l.X), float64(l.Y), float64(l.Z)}, face, 1, float64(far))
			var pc [20]float32 // mat4 viewProj (16) + vec4 lightPosFar (4)
			copy(pc[:16], vp[:])
			pc[16], pc[17], pc[18], pc[19] = l.X, l.Y, l.Z, far

			vk.CmdBeginRenderPass(cmd, &vk.RenderPassBeginInfo{
				SType:           vk.StructureTypeRenderPassBeginInfo,
				RenderPass:      wr.rpShadow,
				Framebuffer:     target.faces[face].fb,
				RenderArea:      vk.Rect2D{Extent: vk.Extent2D{Width: shadowMapSize, Height: shadowMapSize}},
				ClearValueCount: 1,
				PClearValues:    []vk.ClearValue{vk.NewClearDepthStencil(1, 0)},
			}, vk.SubpassContentsInline)
			vk.CmdPushConstants(cmd, wr.plShadow,
				vk.ShaderStageFlags(vk.ShaderStageVertexBit)|vk.ShaderStageFlags(vk.ShaderStageFragmentBit), 0, 80, unsafe.Pointer(&pc[0]))
			vk.CmdBindVertexBuffers(cmd, 0, 1, []vk.Buffer{wr.staticVBO}, []vk.DeviceSize{0})
			for _, d := range g.StaticDraws {
				if d.First >= g.StaticOpaqueCount || d.Count == 0 {
					continue
				}
				vk.CmdDraw(cmd, uint32(d.Count), 1, uint32(d.First), 0)
			}
			if live := g.LiveDraws[:g.LiveOpaqueDraws]; len(live) > 0 { // a moving door still blocks light
				vk.CmdBindVertexBuffers(cmd, 0, 1, []vk.Buffer{sl.vbuf}, []vk.DeviceSize{0})
				for _, d := range live {
					vk.CmdDraw(cmd, uint32(d.Count), 1, uint32(d.First), 0)
				}
			}
			vk.CmdEndRenderPass(cmd)

			rng := vk.ImageSubresourceRange{AspectMask: vk.ImageAspectFlags(vk.ImageAspectDepthBit), BaseArrayLayer: uint32(face), LayerCount: 1, LevelCount: 1}
			barrier(cmd, target.image, rng, vk.ImageLayoutDepthStencilAttachmentOptimal, vk.ImageLayoutShaderReadOnlyOptimal,
				vk.AccessFlags(vk.AccessDepthStencilAttachmentWriteBit), vk.AccessFlags(vk.AccessShaderReadBit),
				vk.PipelineStageFlags(vk.PipelineStageLateFragmentTestsBit), vk.PipelineStageFlags(vk.PipelineStageFragmentShaderBit))
		}
	}
}

func freeShadowCube(dev vk.Device, t shadowCubeTarget) {
	for _, f := range t.faces {
		if f.fb != nil {
			vk.DestroyFramebuffer(dev, f.fb, nil)
		}
		if f.view != nil {
			vk.DestroyImageView(dev, f.view, nil)
		}
	}
	if t.cubeView != nil {
		vk.DestroyImageView(dev, t.cubeView, nil)
	}
	if t.image != nil {
		vk.DestroyImage(dev, t.image, nil)
	}
	if t.memory != nil {
		vk.FreeMemory(dev, t.memory, nil)
	}
}
