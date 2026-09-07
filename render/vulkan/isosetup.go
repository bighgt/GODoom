package vulkan

import (
	_ "embed"
	"fmt"
	"unsafe"

	vk "github.com/goki/vulkan"

	"twopointfive/assets"
	"twopointfive/render/worldgeo"
)

// This file is the one-time resource build for the isometric-viewer path
// (see isoworld.go): a fixed-size offscreen colour+depth scene target, the
// per-texture descriptor cache (mirroring worldtex.go's worldTex/allocTexSet
// shape, but its own pool/sampler/cache rather than sharing worldResources' —
// hw and iso are mutually exclusive per Renderer instance, so nothing is
// saved by sharing, and keeping them separate means neither path's teardown
// has to reason about the other's fields), the world-geometry pipeline, and
// the present stage that blits the finished scene to the swapchain via the
// ordinary blit.vert/blit.frag pipeline shape (see pipeline.go) — its own
// pipeline/descriptor set here rather than reusing r.pipeline/r.pipelineLayout,
// again for the same isolation reason.
//
//go:embed shaders/iso.vert.spv
var isoVertSPV []byte

//go:embed shaders/iso.frag.spv
var isoFragSPV []byte

//go:embed shaders/isovoxel.vert.spv
var isoVoxelVertSPV []byte

//go:embed shaders/isovoxel.frag.spv
var isoVoxelFragSPV []byte

// maxIsoReveal is how many occluder-fade reveal points (see isoRevealUBO)
// iso.frag looks at per fragment — matches the FPS hardware path's own
// MAX_LIGHTS budget (worldMaxLights/world.frag's dynamic-light loop), the
// same kind of "small fixed cap on a per-fragment uniform array" trade.
const maxIsoReveal = 64

// isoRevealUBO is the Reveal UBO's exact memory layout (std140: every
// array element is a 16-byte-aligned vec4, matching Points' [4]float32
// rows directly) — one per frame-in-flight slot, rewritten every frame
// with this frame's monsters/items/barrels (see isoworld.go's
// updateIsoReveal). Count is a float (not int) only so its own row stays a
// plain vec4 like the rest; iso.frag rounds it back to int.
type isoRevealUBO struct {
	count      [4]float32 // x = count, yzw unused
	points     [maxIsoReveal][4]float32 // xy = screen pos (px), z = clip depth [0,1], w unused
}

// isoPushConstants is iso.vert/iso.frag's shared push-constant layout: the
// view-projection matrix (worldgeo.IsoViewProj), a fixed directional "sun"
// for shape (xyz direction toward it, w = strength), an ambient floor, the
// offscreen render target's own size, and the player-occlusion fade
// radius (see iso.frag's playerFade()). 96 bytes (6 vec4), read by both
// stages (see the shaders' own PC block).
//
// playerFade() doesn't need the player's own screen position as a push
// constant: the camera always tracks the player (see cmd/isoengine's main
// loop setting cam.TargetX/Y/Z from the player's own position every frame),
// so the player is always exactly at IsoViewProj's origin — eye-space
// (0,0,0) — which lands at NDC (0,0) and clip Z 0.5 (isoNear/isoFar are
// symmetric about 0) regardless of azimuth/elevation/zoom. screenW/H is
// what turns that NDC centre into the pixel coordinate iso.frag compares
// gl_FragCoord.xy against.
type isoPushConstants struct {
	viewProj                      [16]float32
	sunX, sunY, sunZ, sunStrength float32
	ambientFloor, screenW, screenH, fadeRadius float32
}

type isoResources struct {
	w, h int // fixed offscreen render resolution for the 3D scene

	rpScene  vk.RenderPass
	fbScene  vk.Framebuffer
	colorImg litImage
	depthImg litImage

	texSampler vk.Sampler // wall/flat/sprite textures: linear + mip, REPEAT
	dummyTex   litImage   // 1x1 black — set-0 binding 1 for textures with no brightmap

	dsl0    vk.DescriptorSetLayout // set 0: per-texture (tex, brightmap)
	texPool vk.DescriptorPool      // per-texture set0, grows over a session

	texCache map[*assets.RGBA]*worldTex // keyed by the shared bitmap pointer

	plWorld   vk.PipelineLayout
	pipeWorld vk.Pipeline

	// Reveal UBO (set 1): the screen position + depth of every monster/
	// item/barrel/other thing this frame's isometric front end wants a wall
	// to fade for if one's in front of it — the same occluder-fade iso.frag
	// already does for the player (always screen centre, no UBO needed —
	// see playerFade/occluderFade's own comments), generalised to an
	// arbitrary list for everything else. Host-visible and written
	// directly (no staging/copy — small and read-only by the shader,
	// unlike the sampled overlay image), one buffer/set per frame-in-flight
	// slot since it changes every frame (things move) and a single shared
	// one would race the GPU's read of the previous frame's contents.
	revealBuf       [maxFramesInFlight]vk.Buffer
	revealMem       [maxFramesInFlight]vk.DeviceMemory
	revealMapped    [maxFramesInFlight]unsafe.Pointer
	revealSetLayout vk.DescriptorSetLayout
	revealPool      vk.DescriptorPool
	revealSet       [maxFramesInFlight]vk.DescriptorSet

	// Voxel models — a dedicated pipeline (vertex colour, no texture,
	// isovoxel.vert/frag's own simpler shading), reusing the FPS hardware
	// path's mesh format/cache shape (worldgeo.BuildVoxelMesh, the
	// package-level voxelMesh type — see worldvoxel.go) but its own
	// isolated cache/pipeline: hw and iso are mutually exclusive per
	// Renderer instance, so nothing is saved by sharing wr's, and keeping
	// them separate means neither path's teardown has to reason about the
	// other's fields (the same rationale as texCache/dsl0 above).
	plVoxel   vk.PipelineLayout
	pipeVoxel vk.Pipeline
	voxCache  map[*assets.VoxelModel]*voxelMesh

	// Static (walls/flats) + dynamic (sprite billboard) geometry, one
	// device-local vertex buffer each. Not double-buffered per frame in
	// flight like worldResources' — this is a static viewer, not a
	// real-time game: Invalidate/AddSprites only rerun when the level or
	// the camera's azimuth changes enough to matter, not on every frame's
	// critical path, so the simpler single-buffer-plus-DeviceWaitIdle-on-
	// upload shape (matching uploadStaticWorld's own occasional-update
	// assumption) is enough.
	staticVBO     vk.Buffer
	staticVBOMem  vk.DeviceMemory
	staticVBOCap  int
	staticVersion uint32

	dynVBO    vk.Buffer
	dynVBOMem vk.DeviceMemory
	dynVBOCap int

	packBuf       []byte
	staticPackBuf []byte

	// Present stage: samples colorImg through the ordinary blit shader into
	// r.renderPass/r.framebuffers (the shared swapchain bootstrap) — no
	// resize-time recreation needed here, since colorImg is a fixed-size
	// offscreen target and this pipeline already uses dynamic viewport/
	// scissor.
	presentSampler   vk.Sampler
	presentSetLayout vk.DescriptorSetLayout
	presentPool      vk.DescriptorPool
	presentSet       vk.DescriptorSet
	presentLayout    vk.PipelineLayout
	presentPipeline  vk.Pipeline

	// HUD overlay: a per-frame CPU-drawn RGBA8 plane (package raster's
	// DrawStatusBar/DrawQuakeHUD — the same 2D overlay the FPS hardware
	// path composites, see raster.Renderer.Overlay's doc comment),
	// uploaded fresh every frame and alpha-blended over the finished scene
	// in the present pass (a second draw, same viewport, after the opaque
	// scene blit). Unlike the static/dynamic geometry buffers above, this
	// changes every single frame, so it needs the same per-frame-in-flight
	// staging (indexed by r.currentFrame) the vanilla blit path's own
	// uploadFrame/createStagingBuffer use — a DeviceWaitIdle-per-upload
	// (like uploadIsoStatic's occasional-change shortcut) would stall the
	// GPU every frame.
	overlayW, overlayH   int
	overlayStaging       [maxFramesInFlight]vk.Buffer
	overlayStagingMem    [maxFramesInFlight]vk.DeviceMemory
	overlayStagingMapped [maxFramesInFlight]unsafe.Pointer
	overlayImg           litImage
	overlayImgLayout     vk.ImageLayout
	overlayHasContent    bool // at least one frame has actually uploaded into overlayImg — gates the composite draw

	overlaySampler    vk.Sampler
	overlaySetLayout  vk.DescriptorSetLayout
	overlayPool       vk.DescriptorPool
	overlaySet        vk.DescriptorSet
	overlayPipeLayout vk.PipelineLayout
	overlayPipeline   vk.Pipeline
}

func (r *Renderer) isoCreateSamplers() error {
	ir := r.ir
	texInfo := vk.SamplerCreateInfo{
		SType:        vk.StructureTypeSamplerCreateInfo,
		MagFilter:    vk.FilterLinear,
		MinFilter:    vk.FilterLinear,
		MipmapMode:   vk.SamplerMipmapModeLinear,
		AddressModeU: vk.SamplerAddressModeRepeat,
		AddressModeV: vk.SamplerAddressModeRepeat,
		AddressModeW: vk.SamplerAddressModeRepeat,
		MinLod:       0,
		MaxLod:       vk.LodClampNone,
	}
	if r.maxAnisotropy >= 2 {
		a := r.maxAnisotropy
		if a > 16 {
			a = 16
		}
		texInfo.AnisotropyEnable = vk.True
		texInfo.MaxAnisotropy = a
	}
	var texSamp vk.Sampler
	if res := vk.CreateSampler(r.device, &texInfo, nil, &texSamp); res != vk.Success {
		return fmt.Errorf("vulkan: iso texture sampler: %s", vk.Error(res))
	}
	ir.texSampler = texSamp

	presentInfo := vk.SamplerCreateInfo{
		SType:        vk.StructureTypeSamplerCreateInfo,
		MagFilter:    vk.FilterLinear,
		MinFilter:    vk.FilterLinear,
		MipmapMode:   vk.SamplerMipmapModeNearest,
		AddressModeU: vk.SamplerAddressModeClampToEdge,
		AddressModeV: vk.SamplerAddressModeClampToEdge,
		AddressModeW: vk.SamplerAddressModeClampToEdge,
		MaxLod:       1,
	}
	var presentSamp vk.Sampler
	if res := vk.CreateSampler(r.device, &presentInfo, nil, &presentSamp); res != vk.Success {
		return fmt.Errorf("vulkan: iso present sampler: %s", vk.Error(res))
	}
	ir.presentSampler = presentSamp
	return nil
}

func (r *Renderer) isoCreateDummyTex() error {
	ir := r.ir
	dummy, err := r.wMakeImage(1, 1, vk.FormatR8g8b8a8Unorm,
		vk.ImageUsageFlags(vk.ImageUsageTransferDstBit)|vk.ImageUsageFlags(vk.ImageUsageSampledBit),
		vk.ImageAspectFlags(vk.ImageAspectColorBit), 1)
	if err != nil {
		return err
	}
	ir.dummyTex = dummy
	return r.uploadImagePixels(dummy.image, 1, 1, []byte{0, 0, 0, 255}, vk.ImageLayoutShaderReadOnlyOptimal)
}

// isoCreateSceneRenderPass builds the offscreen colour+depth render pass the
// world-geometry pipeline draws into. RGBA8 colour (no HDR/bloom needed —
// see iso.frag's doc comment), D32 depth, both handed to the present stage
// as shader-read afterward (only colour is actually sampled there; depth
// isn't currently read back, unlike the FPS hardware path's water SSR, but
// finishing it to shader-read costs nothing and keeps the two attachments'
// layout transitions symmetric).
func (r *Renderer) isoCreateSceneRenderPass() error {
	color := vk.AttachmentDescription{
		Format:         vk.FormatR8g8b8a8Unorm,
		Samples:        vk.SampleCount1Bit,
		LoadOp:         vk.AttachmentLoadOpClear,
		StoreOp:        vk.AttachmentStoreOpStore,
		StencilLoadOp:  vk.AttachmentLoadOpDontCare,
		StencilStoreOp: vk.AttachmentStoreOpDontCare,
		InitialLayout:  vk.ImageLayoutUndefined,
		FinalLayout:    vk.ImageLayoutShaderReadOnlyOptimal,
	}
	depth := vk.AttachmentDescription{
		Format:         vk.FormatD32Sfloat,
		Samples:        vk.SampleCount1Bit,
		LoadOp:         vk.AttachmentLoadOpClear,
		StoreOp:        vk.AttachmentStoreOpStore,
		StencilLoadOp:  vk.AttachmentLoadOpDontCare,
		StencilStoreOp: vk.AttachmentStoreOpDontCare,
		InitialLayout:  vk.ImageLayoutUndefined,
		FinalLayout:    vk.ImageLayoutShaderReadOnlyOptimal,
	}
	colorRef := vk.AttachmentReference{Attachment: 0, Layout: vk.ImageLayoutColorAttachmentOptimal}
	depthRef := vk.AttachmentReference{Attachment: 1, Layout: vk.ImageLayoutDepthStencilAttachmentOptimal}
	sub := vk.SubpassDescription{
		PipelineBindPoint:       vk.PipelineBindPointGraphics,
		ColorAttachmentCount:    1,
		PColorAttachments:       []vk.AttachmentReference{colorRef},
		PDepthStencilAttachment: &depthRef,
	}
	deps := []vk.SubpassDependency{
		{ // the previous frame's present-stage read of colorImg finishes before we clear/write it again
			SrcSubpass:    vk.SubpassExternal,
			DstSubpass:    0,
			SrcStageMask:  vk.PipelineStageFlags(vk.PipelineStageFragmentShaderBit),
			DstStageMask:  vk.PipelineStageFlags(vk.PipelineStageColorAttachmentOutputBit) | vk.PipelineStageFlags(vk.PipelineStageEarlyFragmentTestsBit),
			SrcAccessMask: vk.AccessFlags(vk.AccessShaderReadBit),
			DstAccessMask: vk.AccessFlags(vk.AccessColorAttachmentWriteBit) | vk.AccessFlags(vk.AccessDepthStencilAttachmentWriteBit),
		},
		{ // our colour write is visible to the present stage's fragment read
			SrcSubpass: 0,
			DstSubpass: vk.SubpassExternal,
			SrcStageMask: vk.PipelineStageFlags(vk.PipelineStageColorAttachmentOutputBit) |
				vk.PipelineStageFlags(vk.PipelineStageLateFragmentTestsBit),
			DstStageMask: vk.PipelineStageFlags(vk.PipelineStageFragmentShaderBit),
			SrcAccessMask: vk.AccessFlags(vk.AccessColorAttachmentWriteBit) |
				vk.AccessFlags(vk.AccessDepthStencilAttachmentWriteBit),
			DstAccessMask: vk.AccessFlags(vk.AccessShaderReadBit),
		},
	}
	info := vk.RenderPassCreateInfo{
		SType:           vk.StructureTypeRenderPassCreateInfo,
		AttachmentCount: 2,
		PAttachments:    []vk.AttachmentDescription{color, depth},
		SubpassCount:    1,
		PSubpasses:      []vk.SubpassDescription{sub},
		DependencyCount: uint32(len(deps)),
		PDependencies:   deps,
	}
	var rp vk.RenderPass
	if res := vk.CreateRenderPass(r.device, &info, nil, &rp); res != vk.Success {
		return fmt.Errorf("vulkan: iso scene render pass: %s", vk.Error(res))
	}
	r.ir.rpScene = rp
	return nil
}

// isoCreateOffscreen allocates the fixed-size colour+depth images and the
// one framebuffer the scene render pass targets.
func (r *Renderer) isoCreateOffscreen() error {
	ir := r.ir
	var err error
	if ir.colorImg, err = r.wMakeImage(ir.w, ir.h, vk.FormatR8g8b8a8Unorm,
		vk.ImageUsageFlags(vk.ImageUsageColorAttachmentBit)|vk.ImageUsageFlags(vk.ImageUsageSampledBit),
		vk.ImageAspectFlags(vk.ImageAspectColorBit), 1); err != nil {
		return err
	}
	if ir.depthImg, err = r.wMakeImage(ir.w, ir.h, vk.FormatD32Sfloat,
		vk.ImageUsageFlags(vk.ImageUsageDepthStencilAttachmentBit)|vk.ImageUsageFlags(vk.ImageUsageSampledBit),
		vk.ImageAspectFlags(vk.ImageAspectDepthBit), 1); err != nil {
		return err
	}
	fbInfo := vk.FramebufferCreateInfo{
		SType:           vk.StructureTypeFramebufferCreateInfo,
		RenderPass:      ir.rpScene,
		AttachmentCount: 2,
		PAttachments:    []vk.ImageView{ir.colorImg.view, ir.depthImg.view},
		Width:           uint32(ir.w),
		Height:          uint32(ir.h),
		Layers:          1,
	}
	var fb vk.Framebuffer
	if res := vk.CreateFramebuffer(r.device, &fbInfo, nil, &fb); res != vk.Success {
		return fmt.Errorf("vulkan: iso scene framebuffer: %s", vk.Error(res))
	}
	ir.fbScene = fb
	return nil
}

func (r *Renderer) isoCreateDescriptorLayouts() error {
	ir := r.ir
	mk := func(b []vk.DescriptorSetLayoutBinding) (vk.DescriptorSetLayout, error) {
		info := vk.DescriptorSetLayoutCreateInfo{
			SType:        vk.StructureTypeDescriptorSetLayoutCreateInfo,
			BindingCount: uint32(len(b)),
			PBindings:    b,
		}
		var l vk.DescriptorSetLayout
		if res := vk.CreateDescriptorSetLayout(r.device, &info, nil, &l); res != vk.Success {
			return nil, fmt.Errorf("vulkan: iso dsl: %s", vk.Error(res))
		}
		return l, nil
	}
	var err error
	if ir.dsl0, err = mk([]vk.DescriptorSetLayoutBinding{samplerBinding(0), samplerBinding(1)}); err != nil {
		return err
	}
	if ir.presentSetLayout, err = mk([]vk.DescriptorSetLayoutBinding{samplerBinding(0)}); err != nil {
		return err
	}
	return nil
}

// isoCreateRevealResources allocates the Reveal UBO's per-frame-in-flight
// buffers (host-visible, mapped for the renderer's lifetime — small and
// read-only by the shader, so no staging/device-local copy is worth it,
// unlike the sampled overlay image) and its descriptor set layout/pool/
// sets (set 1, bound alongside the per-texture set 0 — see
// isoCreatePipelines). Run before isoCreatePipelines, which needs
// revealSetLayout to build the world pipeline's layout.
func (r *Renderer) isoCreateRevealResources() error {
	ir := r.ir
	size := int(unsafe.Sizeof(isoRevealUBO{}))

	dslInfo := vk.DescriptorSetLayoutCreateInfo{
		SType: vk.StructureTypeDescriptorSetLayoutCreateInfo, BindingCount: 1,
		PBindings: []vk.DescriptorSetLayoutBinding{{
			Binding: 0, DescriptorType: vk.DescriptorTypeUniformBuffer, DescriptorCount: 1,
			StageFlags: vk.ShaderStageFlags(vk.ShaderStageFragmentBit),
		}},
	}
	var dsl vk.DescriptorSetLayout
	if res := vk.CreateDescriptorSetLayout(r.device, &dslInfo, nil, &dsl); res != vk.Success {
		return fmt.Errorf("vulkan: iso reveal dsl: %s", vk.Error(res))
	}
	ir.revealSetLayout = dsl

	poolInfo := vk.DescriptorPoolCreateInfo{
		SType: vk.StructureTypeDescriptorPoolCreateInfo, MaxSets: maxFramesInFlight, PoolSizeCount: 1,
		PPoolSizes: []vk.DescriptorPoolSize{{Type: vk.DescriptorTypeUniformBuffer, DescriptorCount: maxFramesInFlight}},
	}
	var pool vk.DescriptorPool
	if res := vk.CreateDescriptorPool(r.device, &poolInfo, nil, &pool); res != vk.Success {
		return fmt.Errorf("vulkan: iso reveal descriptor pool: %s", vk.Error(res))
	}
	ir.revealPool = pool

	for i := 0; i < maxFramesInFlight; i++ {
		buf, mem, mapped, err := r.wHostBuffer(size, vk.BufferUsageFlags(vk.BufferUsageUniformBufferBit))
		if err != nil {
			return fmt.Errorf("vulkan: iso reveal buffer %d: %w", i, err)
		}
		ir.revealBuf[i], ir.revealMem[i], ir.revealMapped[i] = buf, mem, mapped

		allocInfo := vk.DescriptorSetAllocateInfo{
			SType: vk.StructureTypeDescriptorSetAllocateInfo, DescriptorPool: pool, DescriptorSetCount: 1,
			PSetLayouts: []vk.DescriptorSetLayout{dsl},
		}
		var set vk.DescriptorSet
		if res := vk.AllocateDescriptorSets(r.device, &allocInfo, &set); res != vk.Success {
			return fmt.Errorf("vulkan: iso reveal descriptor set %d: %s", i, vk.Error(res))
		}
		ir.revealSet[i] = set

		write := vk.WriteDescriptorSet{
			SType: vk.StructureTypeWriteDescriptorSet, DstSet: set, DstBinding: 0, DescriptorCount: 1,
			DescriptorType:  vk.DescriptorTypeUniformBuffer,
			PBufferInfo:     []vk.DescriptorBufferInfo{{Buffer: buf, Offset: 0, Range: vk.DeviceSize(size)}},
		}
		vk.UpdateDescriptorSets(r.device, 1, []vk.WriteDescriptorSet{write}, 0, nil)
	}
	return nil
}

// isoCreateTexPool sizes the per-texture descriptor pool generously for a
// whole session's worth of unique wall/flat/sprite names — mirrors
// worldsetup.go's own texPool (see allocTexSet there for the pattern this
// file's isoAllocTexSet follows).
func (r *Renderer) isoCreateTexPool() error {
	const maxTextures = 1024
	info := vk.DescriptorPoolCreateInfo{
		SType:         vk.StructureTypeDescriptorPoolCreateInfo,
		MaxSets:       maxTextures,
		PoolSizeCount: 1,
		PPoolSizes:    []vk.DescriptorPoolSize{{Type: vk.DescriptorTypeCombinedImageSampler, DescriptorCount: 2 * maxTextures}},
	}
	var pool vk.DescriptorPool
	if res := vk.CreateDescriptorPool(r.device, &info, nil, &pool); res != vk.Success {
		return fmt.Errorf("vulkan: iso texture descriptor pool: %s", vk.Error(res))
	}
	r.ir.texPool = pool
	return nil
}

// isoCreatePresentDescriptor allocates the one descriptor set the present
// stage samples colorImg through.
func (r *Renderer) isoCreatePresentDescriptor() error {
	ir := r.ir
	info := vk.DescriptorPoolCreateInfo{
		SType:         vk.StructureTypeDescriptorPoolCreateInfo,
		MaxSets:       1,
		PoolSizeCount: 1,
		PPoolSizes:    []vk.DescriptorPoolSize{{Type: vk.DescriptorTypeCombinedImageSampler, DescriptorCount: 1}},
	}
	var pool vk.DescriptorPool
	if res := vk.CreateDescriptorPool(r.device, &info, nil, &pool); res != vk.Success {
		return fmt.Errorf("vulkan: iso present descriptor pool: %s", vk.Error(res))
	}
	ir.presentPool = pool
	allocInfo := vk.DescriptorSetAllocateInfo{
		SType:              vk.StructureTypeDescriptorSetAllocateInfo,
		DescriptorPool:     ir.presentPool,
		DescriptorSetCount: 1,
		PSetLayouts:        []vk.DescriptorSetLayout{ir.presentSetLayout},
	}
	var set vk.DescriptorSet
	if res := vk.AllocateDescriptorSets(r.device, &allocInfo, &set); res != vk.Success {
		return fmt.Errorf("vulkan: iso present descriptor set: %s", vk.Error(res))
	}
	ir.presentSet = set
	write := vk.WriteDescriptorSet{
		SType:           vk.StructureTypeWriteDescriptorSet,
		DstSet:          ir.presentSet,
		DstBinding:      0,
		DescriptorCount: 1,
		DescriptorType:  vk.DescriptorTypeCombinedImageSampler,
		PImageInfo: []vk.DescriptorImageInfo{{
			Sampler: ir.presentSampler, ImageView: ir.colorImg.view, ImageLayout: vk.ImageLayoutShaderReadOnlyOptimal,
		}},
	}
	vk.UpdateDescriptorSets(r.device, 1, []vk.WriteDescriptorSet{write}, 0, nil)
	return nil
}

// isoCreatePipelines builds the world-geometry pipeline (iso.vert/iso.frag,
// depth-tested, drawing into rpScene) and the present pipeline (the ordinary
// blit.vert/blit.frag shape, drawing colorImg into r.renderPass).
func (r *Renderer) isoCreatePipelines() error {
	ir := r.ir

	plInfo := vk.PipelineLayoutCreateInfo{
		SType:          vk.StructureTypePipelineLayoutCreateInfo,
		SetLayoutCount: 2,
		PSetLayouts:    []vk.DescriptorSetLayout{ir.dsl0, ir.revealSetLayout},
		PushConstantRangeCount: 1,
		PPushConstantRanges: []vk.PushConstantRange{{
			StageFlags: vk.ShaderStageFlags(vk.ShaderStageVertexBit) | vk.ShaderStageFlags(vk.ShaderStageFragmentBit),
			Offset:     0, Size: uint32(unsafe.Sizeof(isoPushConstants{})),
		}},
	}
	var plWorld vk.PipelineLayout
	if res := vk.CreatePipelineLayout(r.device, &plInfo, nil, &plWorld); res != vk.Success {
		return fmt.Errorf("vulkan: iso world pipeline layout: %s", vk.Error(res))
	}
	ir.plWorld = plWorld

	vert, err := r.createShaderModule(isoVertSPV)
	if err != nil {
		return fmt.Errorf("vulkan: iso vert: %w", err)
	}
	defer vk.DestroyShaderModule(r.device, vert, nil)
	frag, err := r.createShaderModule(isoFragSPV)
	if err != nil {
		return fmt.Errorf("vulkan: iso frag: %w", err)
	}
	defer vk.DestroyShaderModule(r.device, frag, nil)

	stages := []vk.PipelineShaderStageCreateInfo{
		{SType: vk.StructureTypePipelineShaderStageCreateInfo, Stage: vk.ShaderStageVertexBit, Module: vert, PName: nz("main")},
		{SType: vk.StructureTypePipelineShaderStageCreateInfo, Stage: vk.ShaderStageFragmentBit, Module: frag, PName: nz("main")},
	}
	attrs := worldgeo.VertexAttributes()
	vkAttrs := make([]vk.VertexInputAttributeDescription, len(attrs))
	for i, a := range attrs {
		vkAttrs[i] = vk.VertexInputAttributeDescription{
			Location: uint32(a.Location), Binding: 0, Format: vkFormatOf(a.Format), Offset: uint32(a.Offset),
		}
	}
	binding := vk.VertexInputBindingDescription{Binding: 0, Stride: uint32(worldgeo.PackedVertexSize), InputRate: vk.VertexInputRateVertex}
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
		CullMode:    vk.CullModeFlags(vk.CullModeNone), // winding isn't consistent across flats/walls (matches world.go)
		FrontFace:   vk.FrontFaceCounterClockwise,
		LineWidth:   1,
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
		PDynamicState:        &dynamic,
		Layout:              ir.plWorld,
		RenderPass:          ir.rpScene,
		Subpass:             0,
	}
	pipelines := make([]vk.Pipeline, 1)
	if res := vk.CreateGraphicsPipelines(r.device, nil, 1, []vk.GraphicsPipelineCreateInfo{info}, nil, pipelines); res != vk.Success {
		return fmt.Errorf("vulkan: iso world pipeline: %s", vk.Error(res))
	}
	ir.pipeWorld = pipelines[0]

	return r.isoCreatePresentPipeline()
}

// isoCreatePresentPipeline mirrors createPipeline (pipeline.go) exactly —
// same blit.vert/blit.frag shaders, same fixed-function state — just its
// own layout/set (pointed at colorImg instead of r.textureView) and its own
// vk.Pipeline field, so it doesn't alias the vanilla path's r.pipeline.
func (r *Renderer) isoCreatePresentPipeline() error {
	ir := r.ir

	vertModule, err := r.createShaderModule(blitVertSPV)
	if err != nil {
		return fmt.Errorf("vulkan: iso present vert: %w", err)
	}
	defer vk.DestroyShaderModule(r.device, vertModule, nil)
	fragModule, err := r.createShaderModule(blitFragSPV)
	if err != nil {
		return fmt.Errorf("vulkan: iso present frag: %w", err)
	}
	defer vk.DestroyShaderModule(r.device, fragModule, nil)

	stages := []vk.PipelineShaderStageCreateInfo{
		{SType: vk.StructureTypePipelineShaderStageCreateInfo, Stage: vk.ShaderStageVertexBit, Module: vertModule, PName: nz("main")},
		{SType: vk.StructureTypePipelineShaderStageCreateInfo, Stage: vk.ShaderStageFragmentBit, Module: fragModule, PName: nz("main")},
	}
	vertexInput := vk.PipelineVertexInputStateCreateInfo{SType: vk.StructureTypePipelineVertexInputStateCreateInfo}
	inputAssembly := vk.PipelineInputAssemblyStateCreateInfo{
		SType: vk.StructureTypePipelineInputAssemblyStateCreateInfo, Topology: vk.PrimitiveTopologyTriangleList,
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
	cba := vk.PipelineColorBlendAttachmentState{
		ColorWriteMask: vk.ColorComponentFlags(vk.ColorComponentRBit) | vk.ColorComponentFlags(vk.ColorComponentGBit) |
			vk.ColorComponentFlags(vk.ColorComponentBBit) | vk.ColorComponentFlags(vk.ColorComponentABit),
	}
	colorBlend := vk.PipelineColorBlendStateCreateInfo{
		SType: vk.StructureTypePipelineColorBlendStateCreateInfo, AttachmentCount: 1,
		PAttachments: []vk.PipelineColorBlendAttachmentState{cba},
	}
	dynamicState := vk.PipelineDynamicStateCreateInfo{
		SType: vk.StructureTypePipelineDynamicStateCreateInfo, DynamicStateCount: 2,
		PDynamicStates: []vk.DynamicState{vk.DynamicStateViewport, vk.DynamicStateScissor},
	}
	layoutInfo := vk.PipelineLayoutCreateInfo{
		SType: vk.StructureTypePipelineLayoutCreateInfo, SetLayoutCount: 1,
		PSetLayouts: []vk.DescriptorSetLayout{ir.presentSetLayout},
	}
	var presentLayout vk.PipelineLayout
	if res := vk.CreatePipelineLayout(r.device, &layoutInfo, nil, &presentLayout); res != vk.Success {
		return fmt.Errorf("vulkan: iso present pipeline layout: %s", vk.Error(res))
	}
	ir.presentLayout = presentLayout
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
		Layout:              ir.presentLayout,
		RenderPass:          r.renderPass,
		Subpass:             0,
	}
	pipelines := make([]vk.Pipeline, 1)
	if res := vk.CreateGraphicsPipelines(r.device, nil, 1, []vk.GraphicsPipelineCreateInfo{pipelineInfo}, nil, pipelines); res != vk.Success {
		return fmt.Errorf("vulkan: iso present pipeline: %s", vk.Error(res))
	}
	ir.presentPipeline = pipelines[0]
	return nil
}

// isoCreateVoxelPipeline builds the voxel-model pipeline (isovoxel.vert/
// frag) — same push-constant layout and vertex format as the FPS hardware
// path's voxel pipeline (worldCreateVoxelPipeline), just its own pipeline
// layout (no descriptor sets at all: isovoxel.frag needs none) and
// isoResources fields. Run as a NewIso step after isoCreatePipelines
// (needs rpScene).
func (r *Renderer) isoCreateVoxelPipeline() error {
	ir := r.ir

	layoutInfo := vk.PipelineLayoutCreateInfo{
		PushConstantRangeCount: 2,
		PPushConstantRanges: []vk.PushConstantRange{
			{StageFlags: vk.ShaderStageFlags(vk.ShaderStageVertexBit), Offset: 0, Size: voxelPushVert},
			{StageFlags: vk.ShaderStageFlags(vk.ShaderStageFragmentBit), Offset: voxelPushVert, Size: voxelPushFrag},
		},
		SType: vk.StructureTypePipelineLayoutCreateInfo,
	}
	var plVoxel vk.PipelineLayout
	if res := vk.CreatePipelineLayout(r.device, &layoutInfo, nil, &plVoxel); res != vk.Success {
		return fmt.Errorf("vulkan: iso voxel pipeline layout: %s", vk.Error(res))
	}
	ir.plVoxel = plVoxel

	vert, err := r.createShaderModule(isoVoxelVertSPV)
	if err != nil {
		return fmt.Errorf("vulkan: iso voxel vert: %w", err)
	}
	defer vk.DestroyShaderModule(r.device, vert, nil)
	frag, err := r.createShaderModule(isoVoxelFragSPV)
	if err != nil {
		return fmt.Errorf("vulkan: iso voxel frag: %w", err)
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
		// model space — matches worldCreateVoxelPipeline's own comment.
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
		Layout:              ir.plVoxel,
		RenderPass:          ir.rpScene,
		Subpass:             0,
	}
	out := make([]vk.Pipeline, 1)
	if res := vk.CreateGraphicsPipelines(r.device, nil, 1, []vk.GraphicsPipelineCreateInfo{info}, nil, out); res != vk.Success {
		return fmt.Errorf("vulkan: iso voxel pipeline: %s", vk.Error(res))
	}
	ir.pipeVoxel = out[0]
	return nil
}

// isoCreateOverlay sets up the HUD overlay's per-frame staging buffers, its
// device-local sampled image, sampler, descriptor set, and the alpha-
// blended pipeline (blit.vert/frag reused, like isoCreatePresentPipeline,
// just with blending turned on and its own pipeline/layout/set so it
// doesn't alias the opaque present draw). w/h is raster.Renderer's own
// resolution (see cmd/isoengine — sized to match the offscreen scene so the
// HUD composites 1:1, no separate scale step).
func (r *Renderer) isoCreateOverlay(w, h int) error {
	ir := r.ir
	ir.overlayW, ir.overlayH = w, h
	size := w * h * 4

	for i := 0; i < maxFramesInFlight; i++ {
		bufInfo := vk.BufferCreateInfo{
			SType: vk.StructureTypeBufferCreateInfo, Size: vk.DeviceSize(size),
			Usage: vk.BufferUsageFlags(vk.BufferUsageTransferSrcBit), SharingMode: vk.SharingModeExclusive,
		}
		var buf vk.Buffer
		if res := vk.CreateBuffer(r.device, &bufInfo, nil, &buf); res != vk.Success {
			return fmt.Errorf("vulkan: iso overlay staging buffer %d: %s", i, vk.Error(res))
		}
		var memReq vk.MemoryRequirements
		vk.GetBufferMemoryRequirements(r.device, buf, &memReq)
		memReq.Deref()
		memType, err := r.findMemoryType(memReq.MemoryTypeBits,
			vk.MemoryPropertyFlags(vk.MemoryPropertyHostVisibleBit)|vk.MemoryPropertyFlags(vk.MemoryPropertyHostCoherentBit))
		if err != nil {
			return fmt.Errorf("vulkan: iso overlay staging memory: %w", err)
		}
		allocInfo := vk.MemoryAllocateInfo{SType: vk.StructureTypeMemoryAllocateInfo, AllocationSize: memReq.Size, MemoryTypeIndex: memType}
		var mem vk.DeviceMemory
		if res := vk.AllocateMemory(r.device, &allocInfo, nil, &mem); res != vk.Success {
			return fmt.Errorf("vulkan: iso overlay staging alloc: %s", vk.Error(res))
		}
		if res := vk.BindBufferMemory(r.device, buf, mem, 0); res != vk.Success {
			return fmt.Errorf("vulkan: iso overlay staging bind: %s", vk.Error(res))
		}
		var mapped unsafe.Pointer
		if res := vk.MapMemory(r.device, mem, 0, vk.DeviceSize(size), 0, &mapped); res != vk.Success {
			return fmt.Errorf("vulkan: iso overlay staging map: %s", vk.Error(res))
		}
		ir.overlayStaging[i], ir.overlayStagingMem[i], ir.overlayStagingMapped[i] = buf, mem, mapped
	}

	var err error
	if ir.overlayImg, err = r.wMakeImage(w, h, vk.FormatR8g8b8a8Unorm,
		vk.ImageUsageFlags(vk.ImageUsageTransferDstBit)|vk.ImageUsageFlags(vk.ImageUsageSampledBit),
		vk.ImageAspectFlags(vk.ImageAspectColorBit), 1); err != nil {
		return err
	}
	ir.overlayImgLayout = vk.ImageLayoutUndefined

	sampInfo := vk.SamplerCreateInfo{
		SType: vk.StructureTypeSamplerCreateInfo,
		// Linear: the HUD is drawn at raster.Renderer's own resolution,
		// matched 1:1 to the offscreen scene here — bilinear only matters
		// on window resize/letterbox scaling, same reasoning as the
		// present pipeline's own sampler.
		MagFilter: vk.FilterLinear, MinFilter: vk.FilterLinear, MipmapMode: vk.SamplerMipmapModeNearest,
		AddressModeU: vk.SamplerAddressModeClampToEdge, AddressModeV: vk.SamplerAddressModeClampToEdge, AddressModeW: vk.SamplerAddressModeClampToEdge,
		MaxLod: 1,
	}
	var samp vk.Sampler
	if res := vk.CreateSampler(r.device, &sampInfo, nil, &samp); res != vk.Success {
		return fmt.Errorf("vulkan: iso overlay sampler: %s", vk.Error(res))
	}
	ir.overlaySampler = samp

	dslInfo := vk.DescriptorSetLayoutCreateInfo{
		SType: vk.StructureTypeDescriptorSetLayoutCreateInfo, BindingCount: 1,
		PBindings: []vk.DescriptorSetLayoutBinding{samplerBinding(0)},
	}
	var dsl vk.DescriptorSetLayout
	if res := vk.CreateDescriptorSetLayout(r.device, &dslInfo, nil, &dsl); res != vk.Success {
		return fmt.Errorf("vulkan: iso overlay dsl: %s", vk.Error(res))
	}
	ir.overlaySetLayout = dsl

	poolInfo := vk.DescriptorPoolCreateInfo{
		SType: vk.StructureTypeDescriptorPoolCreateInfo, MaxSets: 1, PoolSizeCount: 1,
		PPoolSizes: []vk.DescriptorPoolSize{{Type: vk.DescriptorTypeCombinedImageSampler, DescriptorCount: 1}},
	}
	var pool vk.DescriptorPool
	if res := vk.CreateDescriptorPool(r.device, &poolInfo, nil, &pool); res != vk.Success {
		return fmt.Errorf("vulkan: iso overlay descriptor pool: %s", vk.Error(res))
	}
	ir.overlayPool = pool

	allocInfo2 := vk.DescriptorSetAllocateInfo{
		SType: vk.StructureTypeDescriptorSetAllocateInfo, DescriptorPool: pool, DescriptorSetCount: 1,
		PSetLayouts: []vk.DescriptorSetLayout{dsl},
	}
	var set vk.DescriptorSet
	if res := vk.AllocateDescriptorSets(r.device, &allocInfo2, &set); res != vk.Success {
		return fmt.Errorf("vulkan: iso overlay descriptor set: %s", vk.Error(res))
	}
	ir.overlaySet = set
	// The image view never changes (overlayImg is a single persistent
	// image, only its contents are re-uploaded each frame), so this write
	// is a one-time thing, unlike the staging upload itself.
	write := vk.WriteDescriptorSet{
		SType: vk.StructureTypeWriteDescriptorSet, DstSet: set, DstBinding: 0, DescriptorCount: 1,
		DescriptorType: vk.DescriptorTypeCombinedImageSampler,
		PImageInfo: []vk.DescriptorImageInfo{{
			Sampler: samp, ImageView: ir.overlayImg.view, ImageLayout: vk.ImageLayoutShaderReadOnlyOptimal,
		}},
	}
	vk.UpdateDescriptorSets(r.device, 1, []vk.WriteDescriptorSet{write}, 0, nil)

	return r.isoCreateOverlayPipeline()
}

// isoCreateOverlayPipeline mirrors isoCreatePresentPipeline exactly (same
// blit.vert/blit.frag shaders, same fixed-function state) except for one
// difference: straight-alpha blending turned on, since the overlay (unlike
// the opaque scene) is meant to composite over whatever's already in the
// framebuffer rather than replace it.
func (r *Renderer) isoCreateOverlayPipeline() error {
	ir := r.ir

	vertModule, err := r.createShaderModule(blitVertSPV)
	if err != nil {
		return fmt.Errorf("vulkan: iso overlay vert: %w", err)
	}
	defer vk.DestroyShaderModule(r.device, vertModule, nil)
	fragModule, err := r.createShaderModule(blitFragSPV)
	if err != nil {
		return fmt.Errorf("vulkan: iso overlay frag: %w", err)
	}
	defer vk.DestroyShaderModule(r.device, fragModule, nil)

	stages := []vk.PipelineShaderStageCreateInfo{
		{SType: vk.StructureTypePipelineShaderStageCreateInfo, Stage: vk.ShaderStageVertexBit, Module: vertModule, PName: nz("main")},
		{SType: vk.StructureTypePipelineShaderStageCreateInfo, Stage: vk.ShaderStageFragmentBit, Module: fragModule, PName: nz("main")},
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
		BlendEnable:         vk.True,
		SrcColorBlendFactor: vk.BlendFactorSrcAlpha,
		DstColorBlendFactor: vk.BlendFactorOneMinusSrcAlpha,
		ColorBlendOp:        vk.BlendOpAdd,
		SrcAlphaBlendFactor: vk.BlendFactorOne,
		DstAlphaBlendFactor: vk.BlendFactorOneMinusSrcAlpha,
		AlphaBlendOp:        vk.BlendOpAdd,
		ColorWriteMask: vk.ColorComponentFlags(vk.ColorComponentRBit) | vk.ColorComponentFlags(vk.ColorComponentGBit) |
			vk.ColorComponentFlags(vk.ColorComponentBBit) | vk.ColorComponentFlags(vk.ColorComponentABit),
	}
	colorBlend := vk.PipelineColorBlendStateCreateInfo{
		SType: vk.StructureTypePipelineColorBlendStateCreateInfo, AttachmentCount: 1,
		PAttachments: []vk.PipelineColorBlendAttachmentState{cba},
	}
	dynamicState := vk.PipelineDynamicStateCreateInfo{
		SType: vk.StructureTypePipelineDynamicStateCreateInfo, DynamicStateCount: 2,
		PDynamicStates: []vk.DynamicState{vk.DynamicStateViewport, vk.DynamicStateScissor},
	}
	layoutInfo := vk.PipelineLayoutCreateInfo{
		SType: vk.StructureTypePipelineLayoutCreateInfo, SetLayoutCount: 1,
		PSetLayouts: []vk.DescriptorSetLayout{ir.overlaySetLayout},
	}
	var layout vk.PipelineLayout
	if res := vk.CreatePipelineLayout(r.device, &layoutInfo, nil, &layout); res != vk.Success {
		return fmt.Errorf("vulkan: iso overlay pipeline layout: %s", vk.Error(res))
	}
	ir.overlayPipeLayout = layout

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
		Layout:              ir.overlayPipeLayout,
		RenderPass:          r.renderPass,
		Subpass:             0,
	}
	pipelines := make([]vk.Pipeline, 1)
	if res := vk.CreateGraphicsPipelines(r.device, nil, 1, []vk.GraphicsPipelineCreateInfo{pipelineInfo}, nil, pipelines); res != vk.Success {
		return fmt.Errorf("vulkan: iso overlay pipeline: %s", vk.Error(res))
	}
	ir.overlayPipeline = pipelines[0]
	return nil
}
