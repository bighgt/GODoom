package vulkan

import (
	_ "embed"
	"fmt"
	"log"
	"math"
	"os"
	"unsafe"

	vk "github.com/goki/vulkan"

	"twopointfive/assets"
	"twopointfive/assets/models"
	"twopointfive/raster"
	"twopointfive/render"
	"twopointfive/render/worldgeo"
	"twopointfive/window"
)

// Hardware (GPU-geometry) renderer — the game's only rendering path. The CPU
// walks the BSP into a per-texture-grouped triangle list (render/worldgeo)
// and the GPU does rasterisation, perspective-correct texturing, mip/
// anisotropic filtering, the depth test, and a continuous vanilla-style
// sector/distance fade (world.frag's fade()). One draw call per texture —
// plain Vulkan 1.0, no bindless.
//
// Per frame DrawWorld records into one command buffer:
//
//	A  world.vert/frag    packed triangle list -> offscreen RGBA16F HDR scene
//	                      colour (render res) + a sampleable D32 depth buffer
//	B  worldambient.frag  quarter-res dynamic-light shadow + SSAO + god-ray
//	                      term — the low res + the present pass's bilinear
//	                      read of it is both a real perf win (two of the
//	                      most per-pixel-expensive effects now run at
//	                      1/16th the pixel count) and what makes the shadow
//	                      edge soft
//	C  worldbloom.go      scene bright part -> a mip-pyramid glow buffer
//	D  worldblit.frag     SSR / contact shadow, ACES tonemap, FXAA, CAS,
//	                      screen tint, then upscale letterboxed to the
//	                      swapchain with the crisp un-composited 2D overlay
//	                      (HUD/weapon) on top
//
// Pass C reuses r.renderPass / r.framebuffers (the vanilla blit's), so a
// window resize only rebuilds the swapchain-sized resources; the offscreen
// targets are render-resolution and fixed.

//go:embed shaders/world.vert.spv
var worldVertSPV []byte

//go:embed shaders/world.frag.spv
var worldFragSPV []byte

//go:embed shaders/worldblit.frag.spv
var worldBlitFragSPV []byte

//go:embed shaders/waterrefract.frag.spv
var waterRefractFragSPV []byte

//go:embed shaders/depthonly.vert.spv
var depthOnlyVertSPV []byte

//go:embed shaders/depth.frag.spv
var depthFragSPV []byte

//go:embed shaders/shadow.vert.spv
var shadowVertSPV []byte

//go:embed shaders/shadow.frag.spv
var shadowFragSPV []byte

// testLightEnabled forces a magenta light at the camera (uploadWorldFrame) —
// a bisection aid for "dynamic lights don't show". Set TPF_TESTLIGHT=1.
var testLightEnabled = os.Getenv("TPF_TESTLIGHT") != ""

// worldMaxLights is the dynamic point-light cap the World UBO carries —
// keep == render.MaxLights and MAX_LIGHTS in world.frag.
const worldMaxLights = 64

// lightReachStride is how many uint32 words of sector-reach bitset each
// light slot has in worldSlot.reach: 8192 sectors' worth. A sector past
// that is always lit (the shaders skip the test). Keep == REACH_STRIDE in
// world.frag / voxel.frag / waterrefract.frag.
const lightReachStride = 256

// shadowCasterMax is the ground-shadow caster cap the World UBO carries —
// keep == SHADOW_MAX in world.frag and hwGroundShadowCap in engine.
const shadowCasterMax = 24

// World UBO layout (std140), in float32s:
//
//	[0:24]     header      — 6 vec4: cam, screen, light, nLights, fog, amb
//	[24:40]    spot        — 4 vec4: the flashlight (see worldUBO doc)
//	[40:296]   lightPos    — worldMaxLights vec4: xyz world pos, w = 1/radius^2
//	[296:552]  lightColor  — worldMaxLights vec4: rgb = colour * intensity
//	[552:648]  shadowCaster— shadowCasterMax vec4: xyz floor pos, w = radius (0 = end)
const (
	worldUBOHead   = 40
	worldUBOFloats = worldUBOHead + worldMaxLights*4*2 + shadowCasterMax*4
)

// cartoonLightBands/cartoonOutlineStrength tune config.CartoonShading's
// look: how many flat lighting steps world.frag's fade() posterizes to, and
// how dark worldblit.frag's depth-edge outline pass draws (0..1, applied as
// a mix toward black — see recordWorldCommandBuffer's blitPC comment).
// Fixed constants rather than config knobs — like ambGodrayStrength and
// friends, a style choice more than something a player needs to dial in.
const (
	cartoonLightBands      = 4.0
	cartoonOutlineStrength = 0.85
)

// worldUBO mirrors world.frag's `World` block header field-for-field. The
// per-frame upload writes floats() then appends the light arrays.
type worldUBO struct {
	// cam: sinA, cosA, focal, camX
	SinA, CosA, Focal, CamX float32
	// screen: width, height, skyPixPerTurn, pitchShear
	Width, Height, SkyPixPerTurn, PitchShear float32
	// light: lightScale, extraLight, scaleRef, ambientLight
	LightScale, ExtraLight, ScaleRef, AmbientLight float32
	// nLights: x = active dynamic light count, y = water clock, z = camY, w = camZ
	NLights, WaterClock, CamY, CamZ float32
	// fog: rgb colour, w = density (0 = off)
	FogR, FogG, FogB, FogDensity float32
	// amb: rgb = sky-tinted hemisphere ambient (the faint fill a fully
	// shadowed pixel settles to, derived from the level's own sky average),
	// w = ambient strength (0 disables the hemisphere model -> flat fallback)
	AmbR, AmbG, AmbB, AmbStrength float32

	// The player's flashlight — a single forward cone light, kept apart
	// from lightPos/lightColor (isotropic point lights) because a beam
	// needs a direction and cone angle neither carries. SpotIntensity <= 0
	// means off.
	SpotX, SpotY, SpotZ    float32
	SpotInvR2              float32 // 1/radius^2
	SpotDX, SpotDY, SpotDZ float32 // normalized
	SpotCosOuter           float32
	SpotR, SpotG, SpotB    float32
	SpotIntensity          float32
	SpotCosInner           float32
	// CartoonBands is cartoon-shading's band count (world.frag's fade()
	// posterizes its 0..1 brightness to the nearest multiple of 1/N when
	// this is > 0; 0 = off, the smooth vanilla gradient) — repurposes what
	// was a spare pad float alongside the flashlight's own vec4, rather
	// than growing the UBO for one more scalar.
	CartoonBands float32
	// ShadowCount/ShadowFar (config realShadows): how many of the leading
	// lights in lightPos/lightColor (0, 1, or 2 — always index 0 then 1,
	// same "leading lights" order collectLights already produces for the
	// screen-space shadow term) have a real cube shadow map this frame —
	// world.frag's uShadowCube0/uShadowCube1 — and the shared far plane
	// (world units) those maps were rendered with. These were a spare pad
	// pair alongside CartoonBands, same reasoning.
	ShadowCount, ShadowFar float32
}

func (u worldUBO) floats() [worldUBOHead]float32 {
	return [worldUBOHead]float32{
		u.SinA, u.CosA, u.Focal, u.CamX,
		u.Width, u.Height, u.SkyPixPerTurn, u.PitchShear,
		u.LightScale, u.ExtraLight, u.ScaleRef, u.AmbientLight,
		u.NLights, u.WaterClock, u.CamY, u.CamZ,
		u.FogR, u.FogG, u.FogB, u.FogDensity,
		u.AmbR, u.AmbG, u.AmbB, u.AmbStrength,
		u.SpotX, u.SpotY, u.SpotZ, u.SpotInvR2,
		u.SpotDX, u.SpotDY, u.SpotDZ, u.SpotCosOuter,
		u.SpotR, u.SpotG, u.SpotB, u.SpotIntensity,
		u.SpotCosInner, u.CartoonBands, u.ShadowCount, u.ShadowFar,
	}
}

// worldSlot is one frame-in-flight slot's mutable per-frame GPU state:
// double-buffered so the CPU can build slot N+1 while the GPU still reads
// slot N. The vertex buffer / UBO / overlay staging are host-visible and
// kept mapped; the scene + depth + overlay images are device-local.
type worldSlot struct {
	ubo       vk.Buffer
	uboMem    vk.DeviceMemory
	uboMapped unsafe.Pointer

	// reach is the per-light sector-reach bitsets (render.Light.Reach),
	// lightReachStride words per light slot — set 1 binding 3, a storage
	// buffer the light loops in world.frag / voxel.frag / waterrefract.frag
	// test before lighting a surface.
	reach       vk.Buffer
	reachMem    vk.DeviceMemory
	reachMapped unsafe.Pointer

	vbuf       vk.Buffer
	vbufMem    vk.DeviceMemory
	vbufMapped unsafe.Pointer
	vbufSize   int

	ovStage       vk.Buffer
	ovStageMem    vk.DeviceMemory
	ovStageMapped unsafe.Pointer
	ovImg         litImage
	ovLayout      vk.ImageLayout
	// ovUploadY0/Y1 is the row range uploadWorldFrame actually wrote into
	// ovStage this frame (see its own comment on why it's not simply
	// overlayY0/Y1) — recordWorldCommandBuffer's overlay copy step reads
	// this instead of re-deriving it, and skips the copy entirely when
	// empty.
	ovUploadY0, ovUploadY1 int

	scene litImage // offscreen colour (world pass target, blit pass source)
	depth litImage // D32 depth for the world pass
	fb    vk.Framebuffer

	// preWaterColor/preWaterDepth are a snapshot of scene/depth taken right
	// after the opaque (water-excluded) pass and right before the water
	// pass — see recordWorldCommandBuffer and waterrefract.frag's own doc
	// comment. Only allocated/used when wr.waterRefraction is on.
	// preWaterColorLayout/preWaterDepthLayout persist each image's current
	// layout across frames the same way ovLayout does for the overlay
	// image, so the first-ever transition (Undefined -> TransferDst) can
	// use the right stage/access masks instead of a bogus prior-write wait.
	preWaterColor       litImage
	preWaterDepth       litImage
	preWaterColorLayout vk.ImageLayout
	preWaterDepthLayout vk.ImageLayout

	// shadowCube[0]/[1] are this slot's real cube shadow maps (config
	// realShadows) for the frame's leading light(s) — see worldshadow.go.
	// Zero value (unbuilt) when !wr.realShadows.
	shadowCube [shadowLightSlots]shadowCubeTarget

	set1            vk.DescriptorSet // world.frag set 1: World UBO, uShadowCube0/1
	setBlit         vk.DescriptorSet // worldblit.frag: uScene, uOverlay
	setWaterCapture vk.DescriptorSet // waterrefract.frag set 2: uPreWaterColor, uPreWaterDepth — only when wr.waterRefraction
}

// worldTex is one resident GPU texture (a wall texture, flat, or the sky),
// its mip chain, and the set-0 descriptor world.frag samples it through.
type worldTex struct {
	img    litImage
	bright litImage // brightmap mask image (zero value = none; dummy bound instead)
	set    vk.DescriptorSet
	mips   uint32
}

type worldResources struct {
	w, h int // render resolution (offscreen size)

	// waterRefraction is a construction-time-only gate (config
	// waterRefraction, threaded through NewWorld): when true, rpWater/the
	// capture images/dslWaterCapture/plWorldLiquid/pipeWorldLiquid are all
	// built and recordWorldCommandBuffer captures the opaque scene and
	// draws water with real depth/refraction; when false, none of that is
	// built at all and water draws through the plain pipeWorld pipeline
	// inside rpOpaque instead, exactly like every other opaque flat.
	waterRefraction bool

	// realShadows is a construction-time-only gate (config realShadows,
	// threaded through NewWorld) — see worldshadow.go's own doc comment.
	// When true, rpShadow/plShadow/pipeShadow and each slot's shadowCube[]
	// are built and recordWorldCommandBuffer renders real cube shadow maps
	// for up to shadowLightSlots leading lights each frame; when false,
	// only the always-built 1x1 dummyShadowCube exists, so world.frag's two
	// samplerCube bindings stay legal but are never actually sampled
	// (W.spotCosInner.z, the shadow count, is always 0).
	realShadows bool

	rpOpaque vk.RenderPass // opaque static geometry (water excluded when waterRefraction), always built
	rpWater  vk.RenderPass // water (real refraction) + sprites/voxels/particles — only built when waterRefraction
	rpShadow vk.RenderPass // depth-only cube shadow-map face pass — only built when realShadows

	texResidency // texSampler, dummyTex, dsl0, texPool, texCache, voxCache (worldtex.go)

	clampSampler vk.Sampler // LUT / scene / overlay: linear, clamp-to-edge
	depthSampler vk.Sampler // world-pass depth for the blit's water SSR, and shadow cube sampling: nearest, clamp

	dummyShadowCube shadowCubeTarget // always-built 1x1 cube — set-1 bindings 0/1 fallback when unused

	dsl1            vk.DescriptorSetLayout // set 1: uShadowCube0/1, World UBO
	dslBlit         vk.DescriptorSetLayout // present: uScene, uOverlay
	dslWaterCapture vk.DescriptorSetLayout // waterrefract.frag set 2: uPreWaterColor, uPreWaterDepth — only when waterRefraction

	plWorld           vk.PipelineLayout
	plBlit            vk.PipelineLayout
	plWorldLiquid     vk.PipelineLayout // [dsl0, dsl1, dslWaterCapture] — only when waterRefraction
	plShadow          vk.PipelineLayout // push-constant only (viewProj + lightPos/far) — only when realShadows
	pipeWorld         vk.Pipeline
	pipeDepth         vk.Pipeline // opaque static depth pre-pass (reuses plWorld)
	pipeParticleAdd   vk.Pipeline // additive, no depth-write particle billboards (reuses plWorld) — see worldCreatePipelines
	pipeParticleTrans vk.Pipeline // real alpha-blended, no depth-write particle billboards (reuses plWorld) — see worldCreatePipelines
	pipeWorldLiquid   vk.Pipeline // real depth/refraction water (waterrefract.frag) — only when waterRefraction
	pipeShadow        vk.Pipeline // cube shadow-map face pass (shadow.vert/frag) — only when realShadows
	pipeBlit          vk.Pipeline

	framePool vk.DescriptorPool // set1 + setBlit, one pair per slot

	// Voxel models: a dedicated pipeline (vertex colour, no texture) reusing
	// set 1 (LUT + World UBO), and one static device-local mesh per KVX model.
	plVoxel        vk.PipelineLayout
	pipeVoxel      vk.Pipeline
	pipeVoxelDepth vk.Pipeline // translucent-glass depth prepass (no colour writes) — see worldCreateVoxelPipeline
	pipeVoxelGlass vk.Pipeline // translucent-glass blended pass
	glassScratch   []int       // reused recordVoxels glass-instance list
	voxDbg         int         // 0 until recordVoxels has logged its one-shot "drew N models" line

	// Textured 3D models (worldmodel.go): pipeline, per-frame-mesh vertex
	// buffers (keyed by the models.FrameMesh), one-shot debug-log flag.
	plModel           vk.PipelineLayout
	pipeModel         vk.Pipeline
	pipeModelAdd      vk.Pipeline // additive variant (ModelInstance.Additive) — see worldCreateModelPipeline
	pipeModelGlass    vk.Pipeline // translucent-glass variant (ModelInstance.Alpha)
	modelOrderScratch []int       // reused recordModels draw order
	modelCache        map[*models.FrameMesh]*voxelMesh
	modelDbg          int

	slots [maxFramesInFlight]worldSlot

	bloom   worldBloom   // HDR bright-pass + blur (worldbloom.go)
	ambient worldAmbient // quarter-res shadow/AO/god-ray term (worldambient.go)
	resolve worldResolve // full-res SSR+ambient+tonemap resolve (worldresolve.go)

	// Static world geometry (all walls + flats) — one device-local vertex
	// buffer, re-uploaded only when the builder bumps StaticVersion (a door
	// / lift moved). The per-frame path only streams the small dynamic
	// (sprite / voxel-billboard) list into slots[*].vbuf.
	staticGeom

	packBuf      []byte         // reused dynamic Geometry.Pack scratch
	lightCullBuf []render.Light // reused cullLightsToView scratch

	dbgLightLog int // rate-limits the one-off dynamic-light diagnostic

	lightScaleRef float32

	// prevOverlayRanges is a ring buffer of the overlay dirty range
	// (DrawWorld's overlayY0/Y1) from each of the last maxFramesInFlight-1
	// calls — see uploadWorldFrame's own comment on why a slot's overlay
	// upload needs the union of ALL of these with this call's own range,
	// not just the single most recent call's. Sized maxFramesInFlight-1
	// because that's exactly how many OTHER-slot writes land between two
	// visits to the same slot (0 when maxFramesInFlight is 1 — no other
	// slot ever writes in between, so nothing to union).
	prevOverlayRanges [maxFramesInFlight - 1][2]int
	prevOverlayNext   int // next prevOverlayRanges index to overwrite (round-robin)

	// Hemisphere ambient tint, averaged from the level's sky bitmap once and
	// cached (keyed by the bitmap pointer) — world.frag's shadowed-pixel fill
	// takes on each map's own sky palette instead of a fixed blue.
	skyAmb    [3]float32
	skyAmbSrc *assets.RGBA
}

// NewWorld stands up the Vulkan backend on the hardware-geometry path.
// srcW/srcH is the render resolution (the offscreen scene size); vsync
// prefers FIFO; debug enables the validation layer when present.
// waterRefraction gates the real depth/refraction water pipeline (config
// waterRefraction — see worldResources.waterRefraction's own doc comment
// for what building with this on vs. off actually changes); it's a
// construction-time-only choice, not a per-frame toggle. realShadows gates
// real cube shadow maps for a small budget of the leading dynamic lights
// (config realShadows — see worldResources.realShadows's own doc comment).
// Shares the vanilla bring-up (instance … sync objects) and adds the world
// resources.
func NewWorld(win *window.Window, srcW, srcH int, vsync, debug, waterRefraction, realShadows bool) (*Renderer, error) {
	r := &Renderer{win: win, vsync: vsync, debug: debug, hw: true}
	r.srcWidth, r.srcHeight = srcW, srcH // letterboxViewport reads these

	r.wr = &worldResources{
		w: srcW, h: srcH,
		waterRefraction: waterRefraction,
		realShadows:     realShadows,
		modelCache:      make(map[*models.FrameMesh]*voxelMesh),
		lightScaleRef:   float32(raster.LightScaleRef),
	}

	steps := []func() error{
		r.createInstance,
		r.createSurface,
		r.pickPhysicalDevice,
		r.createLogicalDevice,
		r.createSwapchain,
		r.createImageViews,
		r.createRenderPass,
		r.createFramebuffers,
		r.createCommandPool,
		r.createCommandBuffers,
		r.createSyncObjects,
		r.worldCreateSamplers,
		r.worldCreateOpaqueRenderPass,
		r.worldCreateWaterRenderPass,  // no-ops when !waterRefraction
		r.worldCreateShadowRenderPass, // no-op when !realShadows
		r.worldCreateOffscreen,
		r.worldCreateBloom, // before descriptors — setBlit samples bloom mip 0
		func() error { return r.initTexResidency(&r.wr.texResidency, "world") },
		r.worldCreateShadowTargets, // dummy cube always; per-slot real cubes when realShadows — before descriptors (their views get bound)
		r.worldCreateBuffers,
		r.worldCreateAmbientPass, // needs sl.depth/sl.scene (offscreen) + sl.ubo (buffers); before worldCreateResolvePass (samples wr.ambient.imgBlur)
		r.worldCreateResolvePass, // needs sl.scene/sl.depth, wr.bloom.mip[0], wr.ambient.imgBlur; before worldCreatePipelines (needs wr.resolve.dslSample)
		r.worldCreateDescriptorLayouts,
		r.worldCreateDescriptors,
		r.worldCreatePipelines,
		r.worldCreateShadowPipeline, // no-op when !realShadows
		r.worldCreateVoxelPipeline,
		r.worldCreateModelPipeline,
	}
	for _, step := range steps {
		if err := step(); err != nil {
			r.Destroy()
			return nil, err
		}
	}
	return r, nil
}

// DrawWorld implements render.Renderer for the hardware path: upload any new
// textures, pack + upload this frame's triangle list, fill the World UBO
// (camera + fade params + dynamic point lights) and overlay, then record the
// world pass + present blit and submit. Mirrors DrawFrame's
// acquire/submit/present bookkeeping.
func (r *Renderer) DrawWorld(g *worldgeo.Geometry, cam worldgeo.Camera, w, h int, overlay []byte, overlayY0, overlayY1 int, lights []render.Light) error {
	if !r.hw {
		return fmt.Errorf("vulkan: DrawWorld called on a renderer that isn't the hardware-geometry backend")
	}
	wr := r.wr
	if w != wr.w || h != wr.h {
		return fmt.Errorf("vulkan: DrawWorld got %dx%d, renderer built for %dx%d", w, h, wr.w, wr.h)
	}
	if fw, fh := r.win.FramebufferSize(); fw == 0 || fh == 0 {
		return nil
	}
	if r.needRecreate {
		if err := r.recreateSwapchain(); err != nil {
			if err == errSwapchainZeroExtent {
				return nil
			}
			return err
		}
		r.needRecreate = false
	}

	// New wall/flat/sky bitmaps this level — one-time GPU upload + mipmaps.
	// Blocks (drains the device) only when there's actually something new.
	if err := r.syncTextures(&r.wr.texResidency, g); err != nil {
		return err
	}
	if err := r.syncVoxelMeshes(&r.wr.texResidency, g); err != nil {
		return err
	}
	if err := r.syncModels(g); err != nil {
		return err
	}
	if err := r.uploadStatic(&r.wr.staticGeom, g); err != nil {
		return err
	}

	// Drop lights that can't possibly light anything on screen before they
	// cost a UBO slot or a dynamicDiffuse() loop iteration on every fragment
	// this frame — collectLights only distance-culls to the camera, so a
	// torch just out of view (behind the player, around a corner) still paid
	// full per-pixel shading cost every frame without this. Same generous
	// cone test UpdateVisibility already uses to cull whole geometry cells;
	// cam.ShadowCasterN is clamped to match since it indexes lightPos[] by
	// the SAME count worldambient.frag's dynShadow() trusts, and that count
	// was computed engine-side against the unculled list.
	lights = r.cullLightsToView(cam, lights)
	if cam.ShadowCasterN > len(lights) {
		cam.ShadowCasterN = len(lights)
	}

	frame := r.currentFrame
	vk.WaitForFences(r.device, 1, []vk.Fence{r.inFlight[frame]}, vk.True, vk.MaxUint64)

	sl := &wr.slots[frame]
	if err := r.uploadWorldFrame(sl, g, cam, overlay, overlayY0, overlayY1, lights); err != nil {
		return err
	}

	var imageIndex uint32
	acq := vk.AcquireNextImage(r.device, r.swapchain, vk.MaxUint64, r.imageAvailable[frame], nil, &imageIndex)
	if acq == vk.ErrorOutOfDate {
		r.needRecreate = true
		return nil
	} else if acq != vk.Success && acq != vk.Suboptimal {
		return fmt.Errorf("vulkan: acquire next image: %s", vk.Error(acq))
	}

	if f := r.imagesInFlight[imageIndex]; f != nil {
		vk.WaitForFences(r.device, 1, []vk.Fence{f}, vk.True, vk.MaxUint64)
	}
	r.imagesInFlight[imageIndex] = r.inFlight[frame]
	vk.ResetFences(r.device, 1, []vk.Fence{r.inFlight[frame]})

	cmd := r.commandBuffers[imageIndex]
	vk.ResetCommandBuffer(cmd, 0)
	if err := r.recordWorldCommandBuffer(cmd, imageIndex, frame, g, cam, lights); err != nil {
		return err
	}

	signal := []vk.Semaphore{r.renderFinished[imageIndex]}
	submit := vk.SubmitInfo{
		SType:                vk.StructureTypeSubmitInfo,
		WaitSemaphoreCount:   1,
		PWaitSemaphores:      []vk.Semaphore{r.imageAvailable[frame]},
		PWaitDstStageMask:    []vk.PipelineStageFlags{vk.PipelineStageFlags(vk.PipelineStageColorAttachmentOutputBit)},
		CommandBufferCount:   1,
		PCommandBuffers:      []vk.CommandBuffer{cmd},
		SignalSemaphoreCount: 1,
		PSignalSemaphores:    signal,
	}
	if res := vk.QueueSubmit(r.graphicsQueue, 1, []vk.SubmitInfo{submit}, r.inFlight[frame]); res != vk.Success {
		return fmt.Errorf("vulkan: queue submit: %s", vk.Error(res))
	}

	present := vk.PresentInfo{
		SType:              vk.StructureTypePresentInfo,
		WaitSemaphoreCount: 1,
		PWaitSemaphores:    signal,
		SwapchainCount:     1,
		PSwapchains:        []vk.Swapchain{r.swapchain},
		PImageIndices:      []uint32{imageIndex},
	}
	pr := vk.QueuePresent(r.presentQueue, &present)
	if pr == vk.ErrorOutOfDate || pr == vk.Suboptimal {
		r.needRecreate = true
	} else if pr != vk.Success {
		return fmt.Errorf("vulkan: queue present: %s", vk.Error(pr))
	}

	r.currentFrame = (frame + 1) % maxFramesInFlight
	return nil
}

// staticGeom is the static level geometry (all walls + flats) both the hw
// and iso paths keep in one device-local vertex buffer, re-uploaded only
// when the builder bumps StaticVersion (a door / lift moved). Embedded in
// worldResources / isoResources. The per-frame path only streams the small
// dynamic (sprite / voxel-billboard) list separately.
type staticGeom struct {
	staticVBO     vk.Buffer
	staticVBOMem  vk.DeviceMemory
	staticVBOCap  int
	staticVersion uint32
	staticPackBuf []byte // reused static-geometry pack scratch
}

// uploadStatic packs g.StaticTris and copies it into sg's device-local
// vertex buffer (grown / created as needed) when g.StaticVersion moved on
// since the last upload — at level load and after a door/lift moves — so
// the blocking one-shot copy is off the per-frame path.
func (r *Renderer) uploadStatic(sg *staticGeom, g *worldgeo.Geometry) error {
	if g.StaticVersion == sg.staticVersion && sg.staticVBO != nil {
		return nil
	}
	sg.staticPackBuf = worldgeo.PackVerts(g.StaticTris, sg.staticPackBuf)
	n := len(sg.staticPackBuf)
	if n == 0 {
		sg.staticVersion = g.StaticVersion
		return nil
	}

	if n > sg.staticVBOCap {
		grown := max(sg.staticVBOCap, 1<<16)
		for grown < n {
			grown *= 2
		}
		vk.DeviceWaitIdle(r.device) // no frame may still reference the old buffer
		sg.destroy(r.device)
		b, m, err := r.deviceLocalVertexBuffer(grown)
		if err != nil {
			return err
		}
		sg.staticVBO, sg.staticVBOMem, sg.staticVBOCap = b, m, grown
	}
	if err := r.copyToDeviceBuffer(sg.staticVBO, sg.staticPackBuf); err != nil {
		return err
	}
	sg.staticVersion = g.StaticVersion
	return nil
}

func (sg *staticGeom) destroy(dev vk.Device) {
	if sg.staticVBO != nil {
		vk.DestroyBuffer(dev, sg.staticVBO, nil)
		vk.FreeMemory(dev, sg.staticVBOMem, nil)
	}
	sg.staticVBO, sg.staticVBOMem, sg.staticVBOCap = nil, nil, 0
}

// hemisphereAmbient returns the faint fill world.frag settles a fully
// shadowed pixel to (rgb) plus a strength scalar. It's the level's sky
// bitmap averaged, nudged toward its dominant hue (a sky reads greyer
// averaged than it looks) and scaled right down, cached by bitmap pointer.
// A fully enclosed level (no sky) gets a fixed cool neutral — close to the
// old flat AMBIENT constant.
func (r *Renderer) hemisphereAmbient(sky *assets.RGBA) (rr, gg, bb, strength float32) {
	wr := r.wr
	if sky == nil || len(sky.Pix) < 4 {
		return 0.022, 0.026, 0.036, 1.0
	}
	if sky == wr.skyAmbSrc {
		return wr.skyAmb[0], wr.skyAmb[1], wr.skyAmb[2], 1.0
	}
	step := 4
	if n := len(sky.Pix) / 4; n > 4096 {
		step = (n / 4096) * 4
	}
	var sr, sg, sb, cnt float64
	for i := 0; i+2 < len(sky.Pix); i += step {
		sr += float64(sky.Pix[i])
		sg += float64(sky.Pix[i+1])
		sb += float64(sky.Pix[i+2])
		cnt++
	}
	if cnt == 0 {
		return 0.022, 0.026, 0.036, 1.0
	}
	mr, mg, mb := sr/cnt/255, sg/cnt/255, sb/cnt/255
	lum := 0.299*mr + 0.587*mg + 0.114*mb
	const sat, scale = 1.35, 0.09
	mr = max(0.0, min(1.0, lum+(mr-lum)*sat)) * scale
	mg = max(0.0, min(1.0, lum+(mg-lum)*sat)) * scale
	mb = max(0.0, min(1.0, lum+(mb-lum)*sat)) * scale
	wr.skyAmb = [3]float32{float32(mr), float32(mg), float32(mb)}
	wr.skyAmbSrc = sky
	return wr.skyAmb[0], wr.skyAmb[1], wr.skyAmb[2], 1.0
}

// cullLightsToView drops lights whose reach can't overlap anything on
// screen from cam (worldgeo.ViewCone — the same cone test
// UpdateVisibility uses for geometry cells), compacting the survivors into
// a reused buffer so this costs no allocation on a warm frame. Order is
// preserved: shadowBudget's "the leading survivors get a real shadow map"
// and the ambient pass's "the leading survivors get a screen-space one"
// both still pick the most relevant lights first, just among ones that can
// actually matter this frame.
func (r *Renderer) cullLightsToView(cam worldgeo.Camera, lights []render.Light) []render.Light {
	wr := r.wr
	out := wr.lightCullBuf[:0]
	cone := worldgeo.NewViewCone(cam)
	for _, l := range lights {
		if cone.Visible(float64(l.X), float64(l.Y), float64(l.Radius)) {
			out = append(out, l)
		}
	}
	wr.lightCullBuf = out
	return out
}

// uploadWorldFrame packs g into slot sl's (host-visible) vertex buffer,
// growing it if the frame outgrew it, and fills the World UBO (camera + fade
// params + dynamic lights) and overlay staging. Called after sl's in-flight
// fence has been waited on.
func (r *Renderer) uploadWorldFrame(sl *worldSlot, g *worldgeo.Geometry, cam worldgeo.Camera, overlay []byte, overlayY0, overlayY1 int, lights []render.Light) error {
	wr := r.wr

	wr.packBuf = g.Pack(wr.packBuf)
	need := len(wr.packBuf)
	if need > sl.vbufSize {
		grown := sl.vbufSize
		for grown < need {
			grown *= 2
		}
		vk.UnmapMemory(r.device, sl.vbufMem)
		vk.DestroyBuffer(r.device, sl.vbuf, nil)
		vk.FreeMemory(r.device, sl.vbufMem, nil)
		b, m, p, err := r.wHostBuffer(grown, vk.BufferUsageFlags(vk.BufferUsageVertexBufferBit))
		if err != nil {
			return err
		}
		sl.vbuf, sl.vbufMem, sl.vbufMapped, sl.vbufSize = b, m, p, grown
		r.rewriteSlotVertexBinding(sl)
	}
	if need > 0 {
		vk.Memcopy(sl.vbufMapped, wr.packBuf)
	}

	skyPPT := float32(1024) // id's ANGLETOSKYSHIFT: a 256-wide sky wraps 4x
	if g.Sky != nil && float32(g.Sky.Width) > skyPPT {
		skyPPT = float32(g.Sky.Width)
	}
	n := len(lights)
	if n > worldMaxLights {
		n = worldMaxLights
	}
	if n > 0 && wr.dbgLightLog < 4 {
		l := lights[0]
		log.Printf("vulkan: DrawWorld — %d dynamic light(s); [0] pos=(%.0f,%.0f,%.0f) radius=%.0f colour=(%.2f,%.2f,%.2f) intensity=%.2f",
			n, l.X, l.Y, l.Z, l.Radius, l.R, l.G, l.B, l.Intensity)
		wr.dbgLightLog++
	}
	focal := worldgeo.FocalFor(cam, wr.h)
	ambR, ambG, ambB, ambStr := r.hemisphereAmbient(g.Sky)
	u := worldUBO{
		SinA:          float32(math.Sin(cam.Angle)),
		CosA:          float32(math.Cos(cam.Angle)),
		Focal:         float32(focal),
		Width:         float32(wr.w),
		Height:        float32(wr.h),
		SkyPixPerTurn: skyPPT,
		// raster.horizonY = h/2 + tan(pitch)*focal — the sky's vertical anchor.
		PitchShear:   float32(math.Tan(cam.Pitch) * focal),
		LightScale:   float32(raster.LightScaleValue()),
		ExtraLight:   float32(raster.ExtraLight),
		ScaleRef:     wr.lightScaleRef,
		AmbientLight: float32(raster.AmbientLightValue()),
		CamX:         float32(cam.X),
		CamY:         float32(cam.Y),
		CamZ:         float32(cam.Z),
		NLights:      float32(n),
		WaterClock:   float32(cam.Time), // world.frag: W.nLights.y — animated-water clock (s)
		FogR:         cam.FogR,
		FogG:         cam.FogG,
		FogB:         cam.FogB,
		FogDensity:   cam.FogDensity,
		AmbR:         ambR,
		AmbG:         ambG,
		AmbB:         ambB,
		AmbStrength:  ambStr,
	}
	if cam.CartoonShading {
		u.CartoonBands = cartoonLightBands
	}
	if shN, shFar := shadowBudget(wr, lights); shN > 0 {
		u.ShadowCount, u.ShadowFar = float32(shN), shFar
	}
	if cam.SpotIntensity > 0 {
		rad := cam.SpotRadius
		if rad < 0.001 {
			rad = 0.001
		}
		u.SpotX, u.SpotY, u.SpotZ, u.SpotInvR2 = cam.SpotX, cam.SpotY, cam.SpotZ, 1.0/(rad*rad)
		u.SpotDX, u.SpotDY, u.SpotDZ, u.SpotCosOuter = cam.SpotDX, cam.SpotDY, cam.SpotDZ, cam.SpotCosOuter
		// rgb premultiplied by intensity (matches lightColor[]'s convention);
		// alpha carries the raw intensity too, purely as the shader's on/off
		// gate (spotColor.a > 0).
		u.SpotR = cam.SpotR * cam.SpotIntensity
		u.SpotG = cam.SpotG * cam.SpotIntensity
		u.SpotB = cam.SpotB * cam.SpotIntensity
		u.SpotIntensity = cam.SpotIntensity
		u.SpotCosInner = cam.SpotCosInner
	}

	var ubo [worldUBOFloats]float32
	head := u.floats()
	copy(ubo[:worldUBOHead], head[:])
	const posBase = worldUBOHead
	colBase := worldUBOHead + worldMaxLights*4
	for i := 0; i < n; i++ {
		l := lights[i]
		rad := l.Radius
		if rad < 0.001 {
			rad = 0.001
		}
		o := posBase + i*4
		// world.frag's falloff and cull are both in squared distance.
		ubo[o], ubo[o+1], ubo[o+2], ubo[o+3] = l.X, l.Y, l.Z, 1.0/(rad*rad)
		c := colBase + i*4
		ubo[c], ubo[c+1], ubo[c+2], ubo[c+3] = l.R*l.Intensity, l.G*l.Intensity, l.B*l.Intensity, 0
		if len(l.Reach) > 0 {
			// lightColor.w = 1 flags "test reach"; the words go to this
			// light's slot in the reach buffer (only the level's own
			// sectors' words — stale words past them are never indexed).
			ubo[c+3] = 1
			dst := unsafe.Slice((*uint32)(unsafe.Add(sl.reachMapped, i*lightReachStride*4)), lightReachStride)
			copy(dst, l.Reach)
		}
	}
	// Diagnostic: TPF_TESTLIGHT forces one bright magenta light at the
	// camera. If the world near you turns magenta, the shader / UBO / light
	// pipeline is fine and the real issue is upstream (collectLights /
	// positions); if not, it's in here.
	if testLightEnabled {
		ubo[12] = 1 // NLights.x
		ubo[posBase], ubo[posBase+1], ubo[posBase+2], ubo[posBase+3] =
			float32(cam.X), float32(cam.Y), float32(cam.Z), 1.0/(500.0*500.0)
		ubo[colBase], ubo[colBase+1], ubo[colBase+2], ubo[colBase+3] = 4, 0, 4, 0
	}
	// Ground-shadow casters (world.frag shadowCaster[]). Unused entries stay
	// zero — the shader loop breaks on the first radius (.w) <= 0.
	shadowBase := worldUBOHead + worldMaxLights*4*2
	for i := 0; i < len(cam.GroundShadows) && i < shadowCasterMax; i++ {
		s := cam.GroundShadows[i]
		o := shadowBase + i*4
		ubo[o], ubo[o+1], ubo[o+2], ubo[o+3] = s[0], s[1], s[2], s[3]
	}
	vk.Memcopy(sl.uboMapped, unsafe.Slice((*byte)(unsafe.Pointer(&ubo[0])), worldUBOFloats*4))

	// Overlay: only re-copy the rows that actually changed. overlayY0/Y1 is
	// this call's own dirty range (raster.Renderer.OverlayDirty), but sl's
	// image is one of maxFramesInFlight alternating buffers — every OTHER
	// slot was written at least once since sl's own image was last updated
	// (maxFramesInFlight calls ago), so sl's own image is missing each of
	// those calls' dirty rows, not just the single most recent call's.
	// Union in every entry of wr.prevOverlayRanges (the last
	// maxFramesInFlight-1 calls' ranges — see its own doc comment) so the
	// rows copied here cover everything that's actually different from
	// what sl's image currently holds. A slot used for the first time
	// (still its initial Undefined layout) has no valid prior contents at
	// all, so that case forces a full-plane copy regardless.
	upY0, upY1 := overlayY0, overlayY1
	for _, rng := range wr.prevOverlayRanges {
		ry0, ry1 := rng[0], rng[1]
		if ry1 <= ry0 {
			continue
		}
		if upY1 <= upY0 {
			upY0, upY1 = ry0, ry1
			continue
		}
		if ry0 < upY0 {
			upY0 = ry0
		}
		if ry1 > upY1 {
			upY1 = ry1
		}
	}
	if n := len(wr.prevOverlayRanges); n > 0 {
		wr.prevOverlayRanges[wr.prevOverlayNext] = [2]int{overlayY0, overlayY1}
		wr.prevOverlayNext = (wr.prevOverlayNext + 1) % n
	}

	dst := unsafe.Slice((*byte)(sl.ovStageMapped), wr.w*wr.h*4)
	if len(overlay) != len(dst) {
		for i := range dst {
			dst[i] = 0
		}
		sl.ovUploadY0, sl.ovUploadY1 = 0, wr.h
		return nil
	}
	if sl.ovLayout == vk.ImageLayoutUndefined {
		upY0, upY1 = 0, wr.h
	}
	if upY0 < 0 {
		upY0 = 0
	}
	if upY1 > wr.h {
		upY1 = wr.h
	}
	if upY1 > upY0 {
		rb := wr.w * 4
		copy(dst[upY0*rb:upY1*rb], overlay[upY0*rb:upY1*rb])
	}
	sl.ovUploadY0, sl.ovUploadY1 = upY0, upY1
	return nil
}

// rewriteSlotVertexBinding is a no-op hook: the vertex buffer is bound fresh
// every frame in recordWorldCommandBuffer, so a reallocated buffer needs no
// descriptor rewrite. Kept as a named seam in case the buffer ever moves to
// a descriptor.
func (r *Renderer) rewriteSlotVertexBinding(*worldSlot) {}

// camBasis is the camera-space projection basis both the ambient pass
// (worldambient.go's recordAmbientTerm) and the present blit's push
// constants need to reconstruct a world point from (screen pixel, sampled
// depth) — computed once per frame in recordWorldCommandBuffer and passed
// into both instead of each independently re-deriving the same
// FocalFor/Sin/Cos/Tan calls from the same cam.
type camBasis struct {
	focal, sin, cos, horizon float64
}

func computeCamBasis(cam worldgeo.Camera, h int) camBasis {
	focal := worldgeo.FocalFor(cam, h)
	return camBasis{
		focal:   focal,
		sin:     math.Sin(cam.Angle),
		cos:     math.Cos(cam.Angle),
		horizon: float64(h)*0.5 + math.Tan(cam.Pitch)*focal,
	}
}

// drawTextureRuns binds buf and issues one CmdDraw per run in runs,
// skipping empty and (per visible, when non-nil) culled runs, and rebinding
// the set-0 texture descriptor only when the resolved texture actually
// changes from the previous drawn run. Shared by the main world renderer
// (recordWorldCommandBuffer, with StaticDrawVisible as visible for the
// static-geometry pass and nil for everything else) and the isometric
// renderer (recordIsoWorld, always nil — it has no visibility grid).
//
// lastTex/lastWT track the texture (and its resolved descriptor) bound by
// the PREVIOUS drawn (not skipped) run: since the static-geometry
// visibility grid splits what used to be one Draw per texture into several
// — one per (texture, cell) — consecutive runs often share a texture, and
// both rebinding the identical descriptor set AND re-doing the texCache map
// lookup for it are pure waste. haveLast (rather than comparing lastTex to
// nil) is what forces the very first run to always do both: tex itself can
// legitimately be nil (an unresolved sky run, sky == nil), and that must
// still miss the cache instead of matching lastTex's own zero value and
// skipping straight to an unguarded CmdDraw with whatever descriptor set
// happened to be bound before this pass.
func drawTextureRuns(cmd vk.CommandBuffer, plWorld vk.PipelineLayout, texCache map[*assets.RGBA]*worldTex, sky *assets.RGBA, buf vk.Buffer, runs []worldgeo.Draw, visible []bool) {
	if buf == nil || len(runs) == 0 {
		return
	}
	vk.CmdBindVertexBuffers(cmd, 0, 1, []vk.Buffer{buf}, []vk.DeviceSize{0})
	var lastTex *assets.RGBA
	haveLast := false
	for i, d := range runs {
		if d.Count == 0 {
			continue
		}
		if visible != nil && i < len(visible) && !visible[i] {
			continue
		}
		tex := d.Tex
		if tex == nil { // sky run
			tex = sky
		}
		if !haveLast || tex != lastTex {
			wt := texCache[tex]
			if wt == nil {
				continue // texture unresolved / not uploaded — skip the run
			}
			vk.CmdBindDescriptorSets(cmd, vk.PipelineBindPointGraphics, plWorld, 0, 1, []vk.DescriptorSet{wt.set}, 0, nil)
			lastTex, haveLast = tex, true
		}
		vk.CmdDraw(cmd, uint32(d.Count), 1, uint32(d.First), 0)
	}
}

// recordWaterCapture hands rpOpaque's just-drawn (water-excluded) scene
// colour/depth to the water pass as a sampled snapshot — see
// waterrefract.frag's own doc comment for why: a render pass can't sample
// the attachment it's still writing to, so the water pass needs a separate
// copy of "the scene so far" to read from. Only called when
// wr.waterRefraction is on (rpOpaque's own FinalLayout already differs by
// that same toggle — see worldCreateOpaqueRenderPass).
//
// Sequence: rpOpaque's FinalLayout already left sl.scene/sl.depth in
// ColorAttachmentOptimal/DepthStencilAttachmentOptimal (not shader-read —
// this capture, not a sampler, is their very next consumer); transition
// both to TransferSrcOptimal, transition the capture images to
// TransferDstOptimal (respecting each one's own first-use Undefined case,
// same as sl.ovLayout's dance above), copy, transition the captures to
// ShaderReadOnlyOptimal for the water shader to sample, then transition
// sl.scene/sl.depth back to attachment-writable so rpWater's own
// LoadOpLoad has the layout it expects.
func (r *Renderer) recordWaterCapture(cmd vk.CommandBuffer, sl *worldSlot) {
	wr := r.wr
	fullColor := vk.ImageSubresourceRange{AspectMask: vk.ImageAspectFlags(vk.ImageAspectColorBit), LevelCount: 1, LayerCount: 1}
	fullDepth := vk.ImageSubresourceRange{AspectMask: vk.ImageAspectFlags(vk.ImageAspectDepthBit), LevelCount: 1, LayerCount: 1}

	barrier(cmd, sl.scene.image, fullColor, vk.ImageLayoutColorAttachmentOptimal, vk.ImageLayoutTransferSrcOptimal,
		vk.AccessFlags(vk.AccessColorAttachmentWriteBit), vk.AccessFlags(vk.AccessTransferReadBit),
		vk.PipelineStageFlags(vk.PipelineStageColorAttachmentOutputBit), vk.PipelineStageFlags(vk.PipelineStageTransferBit))
	barrier(cmd, sl.depth.image, fullDepth, vk.ImageLayoutDepthStencilAttachmentOptimal, vk.ImageLayoutTransferSrcOptimal,
		vk.AccessFlags(vk.AccessDepthStencilAttachmentWriteBit), vk.AccessFlags(vk.AccessTransferReadBit),
		vk.PipelineStageFlags(vk.PipelineStageLateFragmentTestsBit), vk.PipelineStageFlags(vk.PipelineStageTransferBit))

	pwcSrcStage, pwcSrcAccess := vk.PipelineStageFlags(vk.PipelineStageFragmentShaderBit), vk.AccessFlags(vk.AccessShaderReadBit)
	if sl.preWaterColorLayout == vk.ImageLayoutUndefined {
		pwcSrcStage, pwcSrcAccess = vk.PipelineStageFlags(vk.PipelineStageTopOfPipeBit), 0
	}
	barrier(cmd, sl.preWaterColor.image, fullColor, sl.preWaterColorLayout, vk.ImageLayoutTransferDstOptimal,
		pwcSrcAccess, vk.AccessFlags(vk.AccessTransferWriteBit), pwcSrcStage, vk.PipelineStageFlags(vk.PipelineStageTransferBit))

	pwdSrcStage, pwdSrcAccess := vk.PipelineStageFlags(vk.PipelineStageFragmentShaderBit), vk.AccessFlags(vk.AccessShaderReadBit)
	if sl.preWaterDepthLayout == vk.ImageLayoutUndefined {
		pwdSrcStage, pwdSrcAccess = vk.PipelineStageFlags(vk.PipelineStageTopOfPipeBit), 0
	}
	barrier(cmd, sl.preWaterDepth.image, fullDepth, sl.preWaterDepthLayout, vk.ImageLayoutTransferDstOptimal,
		pwdSrcAccess, vk.AccessFlags(vk.AccessTransferWriteBit), pwdSrcStage, vk.PipelineStageFlags(vk.PipelineStageTransferBit))

	extent := vk.Extent3D{Width: uint32(wr.w), Height: uint32(wr.h), Depth: 1}
	vk.CmdCopyImage(cmd, sl.scene.image, vk.ImageLayoutTransferSrcOptimal, sl.preWaterColor.image, vk.ImageLayoutTransferDstOptimal, 1,
		[]vk.ImageCopy{{
			SrcSubresource: vk.ImageSubresourceLayers{AspectMask: vk.ImageAspectFlags(vk.ImageAspectColorBit), LayerCount: 1},
			DstSubresource: vk.ImageSubresourceLayers{AspectMask: vk.ImageAspectFlags(vk.ImageAspectColorBit), LayerCount: 1},
			Extent:         extent,
		}})
	vk.CmdCopyImage(cmd, sl.depth.image, vk.ImageLayoutTransferSrcOptimal, sl.preWaterDepth.image, vk.ImageLayoutTransferDstOptimal, 1,
		[]vk.ImageCopy{{
			SrcSubresource: vk.ImageSubresourceLayers{AspectMask: vk.ImageAspectFlags(vk.ImageAspectDepthBit), LayerCount: 1},
			DstSubresource: vk.ImageSubresourceLayers{AspectMask: vk.ImageAspectFlags(vk.ImageAspectDepthBit), LayerCount: 1},
			Extent:         extent,
		}})

	barrier(cmd, sl.preWaterColor.image, fullColor, vk.ImageLayoutTransferDstOptimal, vk.ImageLayoutShaderReadOnlyOptimal,
		vk.AccessFlags(vk.AccessTransferWriteBit), vk.AccessFlags(vk.AccessShaderReadBit),
		vk.PipelineStageFlags(vk.PipelineStageTransferBit), vk.PipelineStageFlags(vk.PipelineStageFragmentShaderBit))
	sl.preWaterColorLayout = vk.ImageLayoutShaderReadOnlyOptimal
	barrier(cmd, sl.preWaterDepth.image, fullDepth, vk.ImageLayoutTransferDstOptimal, vk.ImageLayoutShaderReadOnlyOptimal,
		vk.AccessFlags(vk.AccessTransferWriteBit), vk.AccessFlags(vk.AccessShaderReadBit),
		vk.PipelineStageFlags(vk.PipelineStageTransferBit), vk.PipelineStageFlags(vk.PipelineStageFragmentShaderBit))
	sl.preWaterDepthLayout = vk.ImageLayoutShaderReadOnlyOptimal

	barrier(cmd, sl.scene.image, fullColor, vk.ImageLayoutTransferSrcOptimal, vk.ImageLayoutColorAttachmentOptimal,
		vk.AccessFlags(vk.AccessTransferReadBit), vk.AccessFlags(vk.AccessColorAttachmentWriteBit),
		vk.PipelineStageFlags(vk.PipelineStageTransferBit), vk.PipelineStageFlags(vk.PipelineStageColorAttachmentOutputBit))
	barrier(cmd, sl.depth.image, fullDepth, vk.ImageLayoutTransferSrcOptimal, vk.ImageLayoutDepthStencilAttachmentOptimal,
		vk.AccessFlags(vk.AccessTransferReadBit), vk.AccessFlags(vk.AccessDepthStencilAttachmentWriteBit)|vk.AccessFlags(vk.AccessDepthStencilAttachmentReadBit),
		vk.PipelineStageFlags(vk.PipelineStageTransferBit), vk.PipelineStageFlags(vk.PipelineStageEarlyFragmentTestsBit)|vk.PipelineStageFlags(vk.PipelineStageLateFragmentTestsBit))
}

func (r *Renderer) recordWorldCommandBuffer(cmd vk.CommandBuffer, imageIndex uint32, frame int, g *worldgeo.Geometry, cam worldgeo.Camera, lights []render.Light) error {
	wr := r.wr
	sl := &wr.slots[frame]
	if res := vk.BeginCommandBuffer(cmd, &vk.CommandBufferBeginInfo{SType: vk.StructureTypeCommandBufferBeginInfo}); res != vk.Success {
		return fmt.Errorf("vulkan: begin command buffer: %s", vk.Error(res))
	}

	// (1) overlay staging -> this slot's overlay image — only the rows
	// uploadWorldFrame actually copied into staging (sl.ovUploadY0/Y1),
	// skipped entirely when nothing changed: the HUD/weapon overlay is
	// mostly blank/unchanged most frames (see raster.Renderer.OverlayDirty),
	// so re-copying and re-barriering the whole render-resolution plane
	// every frame regardless was pure waste on all but the rows that
	// actually moved.
	if sl.ovUploadY1 > sl.ovUploadY0 {
		rng := vk.ImageSubresourceRange{AspectMask: vk.ImageAspectFlags(vk.ImageAspectColorBit), LevelCount: 1, LayerCount: 1}
		srcStage := vk.PipelineStageFlags(vk.PipelineStageFragmentShaderBit)
		srcAccess := vk.AccessFlags(vk.AccessShaderReadBit)
		if sl.ovLayout == vk.ImageLayoutUndefined {
			srcStage = vk.PipelineStageFlags(vk.PipelineStageTopOfPipeBit)
			srcAccess = 0
		}
		barrier(cmd, sl.ovImg.image, rng, sl.ovLayout, vk.ImageLayoutTransferDstOptimal,
			srcAccess, vk.AccessFlags(vk.AccessTransferWriteBit),
			srcStage, vk.PipelineStageFlags(vk.PipelineStageTransferBit))
		rows := uint32(sl.ovUploadY1 - sl.ovUploadY0)
		cp := vk.BufferImageCopy{
			BufferOffset:     vk.DeviceSize(sl.ovUploadY0 * wr.w * 4),
			ImageSubresource: vk.ImageSubresourceLayers{AspectMask: vk.ImageAspectFlags(vk.ImageAspectColorBit), LayerCount: 1},
			ImageOffset:      vk.Offset3D{Y: int32(sl.ovUploadY0)},
			ImageExtent:      vk.Extent3D{Width: uint32(wr.w), Height: rows, Depth: 1},
		}
		vk.CmdCopyBufferToImage(cmd, sl.ovStage, sl.ovImg.image, vk.ImageLayoutTransferDstOptimal, 1, []vk.BufferImageCopy{cp})
		barrier(cmd, sl.ovImg.image, rng, vk.ImageLayoutTransferDstOptimal, vk.ImageLayoutShaderReadOnlyOptimal,
			vk.AccessFlags(vk.AccessTransferWriteBit), vk.AccessFlags(vk.AccessShaderReadBit),
			vk.PipelineStageFlags(vk.PipelineStageTransferBit), vk.PipelineStageFlags(vk.PipelineStageFragmentShaderBit))
		sl.ovLayout = vk.ImageLayoutShaderReadOnlyOptimal
	}

	// (1.5) real shadow maps (config realShadows): render up to
	// shadowLightSlots cube depth maps from the leading lights' own
	// positions into sl.shadowCube[], before the colour pass below samples
	// them — see worldshadow.go. No-op (returns immediately) when
	// !wr.realShadows.
	r.recordShadowMaps(cmd, frame, g, lights)

	// (2) opaque pass -> offscreen scene colour (+ depth). Water is excluded
	// from g.StaticDraws (see worldgeo.Geometry.WaterDraws) when
	// waterRefraction is on — it draws in its own step below instead, once
	// this pass's result has been captured for it to sample.
	clears := []vk.ClearValue{vk.NewClearValue([]float32{0, 0, 0, 1}), vk.NewClearDepthStencil(1, 0)}
	vk.CmdBeginRenderPass(cmd, &vk.RenderPassBeginInfo{
		SType:           vk.StructureTypeRenderPassBeginInfo,
		RenderPass:      wr.rpOpaque,
		Framebuffer:     sl.fb,
		RenderArea:      vk.Rect2D{Extent: vk.Extent2D{Width: uint32(wr.w), Height: uint32(wr.h)}},
		ClearValueCount: 2,
		PClearValues:    clears,
	}, vk.SubpassContentsInline)

	vk.CmdSetViewport(cmd, 0, 1, []vk.Viewport{fullViewport(wr.w, wr.h)})
	vk.CmdSetScissor(cmd, 0, 1, []vk.Rect2D{fullScissor(wr.w, wr.h)})

	vp := worldgeo.ViewProj(cam, wr.w, wr.h)
	basis := computeCamBasis(cam, wr.h)

	// Depth pre-pass: rasterise the opaque static geometry to the depth
	// buffer only (no fragment shading), so the colour pass's early-Z
	// rejects every occluded wall/flat pixel before it runs the fade + light
	// loop. Masked 2-sided middles (StaticTris[StaticOpaqueCount:]) are
	// excluded — their holes must not write depth.
	//
	// Measured (2026-09): disabling this cost ~100 FPS on a real test map —
	// the per-pixel dynamic-light loop it protects (world.frag's
	// dynamicDiffuse) costs more than the doubled vertex/draw submission
	// below saves, so it stays on. Kept as a data point for anyone touching
	// this again: a bigger/more-occluded custom level could tip the other
	// way, but this codebase's typical geometry does not.
	//
	// This walks g.StaticDraws' opaque prefix (every entry's First is <
	// StaticOpaqueCount, in the order Invalidate appended them — masked
	// middles only start after it) one CmdDraw per run instead of the single
	// CmdDraw over the whole StaticTris[:StaticOpaqueCount] range this used
	// to be, so it can skip runs g.StaticDrawVisible has culled this frame
	// exactly like the colour pass's drawRuns does just below. Without this,
	// depth got written (and the colour pass's early-Z got to reject
	// against) geometry the colour pass itself no longer even submits,
	// undercutting part of the point of culling it there.
	if wr.pipeDepth != nil && wr.staticVBO != nil && g.StaticOpaqueCount > 0 {
		vk.CmdBindPipeline(cmd, vk.PipelineBindPointGraphics, wr.pipeDepth)
		vk.CmdPushConstants(cmd, wr.plWorld, vk.ShaderStageFlags(vk.ShaderStageVertexBit), 0, 64, unsafe.Pointer(&vp[0]))
		vk.CmdBindVertexBuffers(cmd, 0, 1, []vk.Buffer{wr.staticVBO}, []vk.DeviceSize{0})
		visible := g.StaticDrawVisible
		for i, d := range g.StaticDraws {
			if d.First >= g.StaticOpaqueCount || d.Count == 0 {
				continue
			}
			if visible != nil && i < len(visible) && !visible[i] {
				continue
			}
			vk.CmdDraw(cmd, uint32(d.Count), 1, uint32(d.First), 0)
		}
	}

	vk.CmdBindPipeline(cmd, vk.PipelineBindPointGraphics, wr.pipeWorld)
	vk.CmdPushConstants(cmd, wr.plWorld, vk.ShaderStageFlags(vk.ShaderStageVertexBit), 0, 64, unsafe.Pointer(&vp[0]))
	vk.CmdBindDescriptorSets(cmd, vk.PipelineBindPointGraphics, wr.plWorld, 1, 1, []vk.DescriptorSet{sl.set1}, 0, nil)

	// visible, when non-nil, parallels runs: a false entry is this frame's
	// static-geometry visibility cull skipping a Draw outside the camera's
	// view (worldgeo.Geometry.StaticDrawVisible, from Builder.UpdateVisibility
	// — see render/worldgeo/visgrid.go). Only g.StaticDraws carries this;
	// the per-frame sprite/voxel Draws list (g.Draws) is passed nil, i.e.
	// unfiltered, same as before. See drawTextureRuns for the shared body
	// (also used by isoworld.go's recorder, with visible always nil).
	drawRuns := func(buf vk.Buffer, runs []worldgeo.Draw, visible []bool) {
		drawTextureRuns(cmd, wr.plWorld, wr.texCache, g.Sky, buf, runs, visible)
	}
	drawRuns(wr.staticVBO, g.StaticDraws, g.StaticDrawVisible) // level walls + flats (device-local), visibility-culled
	drawRuns(sl.vbuf, g.LiveDraws, nil)                        // live sectors: moving doors / lifts, flickering lights, scrollers

	if !wr.waterRefraction {
		// Fallback: water draws right here with the plain pipeline, same as
		// every other opaque flat, then falls straight into the shared
		// sprite/voxel/particle tail below inside this one pass — exactly
		// how this worked before water became its own Draw list.
		drawRuns(wr.staticVBO, g.WaterDraws, g.WaterDrawVisible)
	} else {
		r.recordWaterCapture(cmd, sl)

		vk.CmdBeginRenderPass(cmd, &vk.RenderPassBeginInfo{
			SType:       vk.StructureTypeRenderPassBeginInfo,
			RenderPass:  wr.rpWater,
			Framebuffer: sl.fb,
			RenderArea:  vk.Rect2D{Extent: vk.Extent2D{Width: uint32(wr.w), Height: uint32(wr.h)}},
			// LoadOpLoad on both attachments — nothing to clear.
		}, vk.SubpassContentsInline)
		vk.CmdSetViewport(cmd, 0, 1, []vk.Viewport{fullViewport(wr.w, wr.h)})
		vk.CmdSetScissor(cmd, 0, 1, []vk.Rect2D{fullScissor(wr.w, wr.h)})

		if wr.pipeWorldLiquid != nil && len(g.WaterDraws) > 0 {
			vk.CmdBindPipeline(cmd, vk.PipelineBindPointGraphics, wr.pipeWorldLiquid)
			vk.CmdPushConstants(cmd, wr.plWorldLiquid, vk.ShaderStageFlags(vk.ShaderStageVertexBit), 0, 64, unsafe.Pointer(&vp[0]))
			vk.CmdBindDescriptorSets(cmd, vk.PipelineBindPointGraphics, wr.plWorldLiquid, 1, 1, []vk.DescriptorSet{sl.set1}, 0, nil)
			vk.CmdBindDescriptorSets(cmd, vk.PipelineBindPointGraphics, wr.plWorldLiquid, 2, 1, []vk.DescriptorSet{sl.setWaterCapture}, 0, nil)
			drawTextureRuns(cmd, wr.plWorldLiquid, wr.texCache, nil, wr.staticVBO, g.WaterDraws, g.WaterDrawVisible)
		}

		// Re-bind pipeWorld/plWorld before the shared sprite/voxel/particle
		// tail below: pipeWorldLiquid uses a different pipeline LAYOUT
		// object (plWorldLiquid) than pipeWorld's plWorld, even though set 1
		// binds the identical descriptor set both times — Vulkan tracks
		// bindings per pipeline-layout object, not just by set index, so
		// both the pipeline and set 1 need re-establishing here. Same class
		// of gotcha recordVoxels' own comment below already flags for its
		// own pipeline-layout swap.
		vk.CmdBindPipeline(cmd, vk.PipelineBindPointGraphics, wr.pipeWorld)
		vk.CmdPushConstants(cmd, wr.plWorld, vk.ShaderStageFlags(vk.ShaderStageVertexBit), 0, 64, unsafe.Pointer(&vp[0]))
		vk.CmdBindDescriptorSets(cmd, vk.PipelineBindPointGraphics, wr.plWorld, 1, 1, []vk.DescriptorSet{sl.set1}, 0, nil)
	}

	drawRuns(sl.vbuf, g.Draws, nil) // this frame's sprite / voxel-billboard quads, unfiltered
	// Voxel models — own pipeline, still inside the world pass so the depth
	// buffer sorts them against the level and the textured geometry. Must
	// come before the particle passes below: voxels are opaque replacements
	// for monster/item sprites, and smoke/fire's real alpha blend needs
	// them already in the depth+color buffers to draw over, the same way it
	// already draws over ordinary (non-voxel) sprites from g.Draws above —
	// recording them after the particle passes instead (as this used to)
	// painted every voxel monster back over the smoke/fire meant to be
	// hiding it, regardless of how opaque the smoke actually was.
	r.recordVoxels(cmd, sl, g, cam)
	r.recordModels(cmd, sl, g, cam) // same layer as voxels; the rebinds below restore what both overwrite
	// recordVoxels binds sl.set1 at set 0 under plVoxel's layout, which
	// isn't compatible with plWorld's own completely different set-0 schema
	// (a per-texture sampler, not the world UBO) — per Vulkan's pipeline
	// layout compatibility rules, that invalidates set 1 too (the world UBO
	// itself: lights/camera/fog/water-clock), even though voxels never
	// touch set 1 directly. drawRuns below only ever rebinds set 0 (the
	// per-texture descriptor, on its first run each call), so without
	// this, the particle passes' fragment shaders would read undefined
	// set-1 data — which is exactly why moving recordVoxels earlier (see
	// above) silently broke every particle effect instead of just fixing
	// the occlusion this was meant to fix.
	vk.CmdBindDescriptorSets(cmd, vk.PipelineBindPointGraphics, wr.plWorld, 1, 1, []vk.DescriptorSet{sl.set1}, 0, nil)
	// recordVoxels ALSO overwrites push-constant bytes [0:64) with each
	// voxel instance's own per-model MVP matrix (see its CmdPushConstants
	// calls) - the SAME byte range/stage the vertex shader above reads its
	// camera view-projection matrix from. Left un-restored, every particle
	// drawn below would be transformed by whichever voxel model happened
	// to be pushed last this frame instead of the camera, scattering its
	// geometry off-screen/degenerate - a far more complete breakage than
	// the descriptor-set issue above, and the real reason moving
	// recordVoxels earlier made every particle effect (explosion fire and
	// smoke, the rocket exhaust trail) disappear outright.
	vk.CmdPushConstants(cmd, wr.plWorld, vk.ShaderStageFlags(vk.ShaderStageVertexBit), 0, 64, unsafe.Pointer(&vp[0]))
	if wr.pipeParticleAdd != nil && len(g.ParticleDraws) > 0 {
		// Same vertex buffer (g.Pack wrote all of g.Tris, Draws' and
		// ParticleDraws' vertices both), same descriptor-set-1 binding
		// (still bound from above) — only the pipeline changes, so
		// overlapping billboards additively glow instead of alpha-cutout
		// occluding each other. See pipeParticleAdd's creation in
		// worldsetup.go (worldCreatePipelines) for the blend/depth state.
		vk.CmdBindPipeline(cmd, vk.PipelineBindPointGraphics, wr.pipeParticleAdd)
		drawRuns(sl.vbuf, g.ParticleDraws, nil)
	}
	if wr.pipeParticleTrans != nil && len(g.TransparentDraws) > 0 {
		// Same vertex buffer, same descriptor-set-1 binding — just the
		// blend pipeline swapped again. g.TransparentDraws is already
		// sorted back-to-front by AddTransparentSprites, one Draw per
		// sprite (not batched by texture — see its own doc comment), so
		// drawRuns's texture-change tracking still does the right thing,
		// it just rebinds more often than the batched passes above.
		vk.CmdBindPipeline(cmd, vk.PipelineBindPointGraphics, wr.pipeParticleTrans)
		drawRuns(sl.vbuf, g.TransparentDraws, nil)
	}
	vk.CmdEndRenderPass(cmd)
	// Whichever pass just ended (rpWater when waterRefraction is on, the
	// single rpOpaque otherwise)'s finalLayout transitions the scene image
	// to shader-read for us.

	// (3) ambient term: quarter-res shadow/AO/god-ray buffer (worldambient.go)
	// — its low resolution plus the resolve pass's bilinear read of it is
	// both a real perf win (SSAO's disc gather and god rays' march run at
	// 1/16th the pixel count) and what makes the shadow edge soft, not any
	// math in the shader itself.
	r.recordAmbientTerm(cmd, frame, cam, basis)

	// (4) bloom: build the HDR mip pyramid -> bloom mip 0.
	r.recordBloom(cmd, frame)

	// blitPC/PC prep, hoisted above (5)/(6) so both the resolve pass and the
	// present blit can use it: worldblit.frag PC: tonemap params + the
	// camera basis needed to reconstruct a world point from (screen pixel,
	// sampled depth) and march the depth buffer for the water SSR
	// reflection. horizonY / focal match raster.horizonY and
	// world.frag skyColorAt. basis (computed once above, shared with
	// recordAmbientTerm) is the same derivation. p0..p3 (the first 64
	// bytes/16 floats) are exactly what worldresolve.frag's own PC needs —
	// see recordResolve's doc comment on why it reads this same array
	// instead of rebuilding its own copy.
	bFocal := basis.focal
	bSin := float32(basis.sin)
	bCos := float32(basis.cos)
	bHorizon := float32(basis.horizon)
	// worldblit.frag post-chain tuning still owned here (SSR/CAS/dither).
	// aoStrength, aoRadius/aoBias, sunDir, and the dyn-light shadow march
	// constants used to be threaded through here too, back when this
	// shader ran SSAO/god-rays/shadows itself — now that they've moved to
	// worldambient.go's own pass (and SSR/the ambient combine/tonemap into
	// worldresolve.go's), this array carries only what worldblit.frag
	// itself still actually reads, plus the p0-p3 subset worldresolve.frag
	// needs.
	const (
		ssrStrength     = 0.9
		sharpenStrength = 0.35 // CAS 0..1
		ditherStrength  = 1.0  // +/- 1 LSB triangular-PDF, kills 8-bit banding
	)
	outlineStrength := float32(0)
	if cam.CartoonShading {
		outlineStrength = cartoonOutlineStrength
	}
	blitPC := [24]float32{
		wr.bloom.exposure, wr.bloom.strength, float32(cam.Time), float32(worldgeo.NearPlane),
		bSin, bCos, float32(bFocal), float32(worldgeo.FarPlane),
		float32(cam.X), float32(cam.Y), float32(cam.Z), bHorizon,
		// p3.w (godrayStrength) scales+tints the pre-marched god-ray luma
		// worldresolve.frag samples from worldambient.go's ambient buffer.
		float32(wr.w), float32(wr.h), ssrStrength, ambGodrayStrength,
		// p4.w was the last unused pad; now cartoon shading's depth-edge
		// outline strength (0 = off — see cartoonOutlineStrength's doc).
		sharpenStrength, ditherStrength, 0, outlineStrength,
		// p5: full-frame sector-effect tint (rgb, amount; A<0 also = heat wobble)
		cam.TintR, cam.TintG, cam.TintB, cam.TintA,
	}

	// (5) resolve: water SSR + the ambient combine + the ACES tonemap, once
	// per pixel, into wr.resolve.img — see worldresolve.go's own doc
	// comment for why this is its own pass now.
	r.recordResolve(cmd, frame, (*[16]float32)(blitPC[:16]))

	// (6) present blit: FXAA/CAS/cartoon-outline/tint the resolved image,
	// upscaled letterboxed, with the crisp overlay composited on top.
	vpLB := r.letterboxViewport()
	vk.CmdBeginRenderPass(cmd, &vk.RenderPassBeginInfo{
		SType:           vk.StructureTypeRenderPassBeginInfo,
		RenderPass:      r.renderPass,
		Framebuffer:     r.framebuffers[imageIndex],
		RenderArea:      vk.Rect2D{Extent: r.swapchainExtent},
		ClearValueCount: 1,
		PClearValues:    []vk.ClearValue{vk.NewClearValue([]float32{0, 0, 0, 1})},
	}, vk.SubpassContentsInline)
	vk.CmdBindPipeline(cmd, vk.PipelineBindPointGraphics, wr.pipeBlit)
	vk.CmdSetViewport(cmd, 0, 1, []vk.Viewport{vpLB})
	vk.CmdSetScissor(cmd, 0, 1, []vk.Rect2D{{
		Offset: vk.Offset2D{X: int32(vpLB.X), Y: int32(vpLB.Y)},
		Extent: vk.Extent2D{Width: uint32(vpLB.Width), Height: uint32(vpLB.Height)},
	}})
	vk.CmdBindDescriptorSets(cmd, vk.PipelineBindPointGraphics, wr.plBlit, 0, 2,
		[]vk.DescriptorSet{sl.setBlit, wr.resolve.setSample}, 0, nil)
	vk.CmdPushConstants(cmd, wr.plBlit, vk.ShaderStageFlags(vk.ShaderStageFragmentBit), 0, 96, unsafe.Pointer(&blitPC[0]))
	vk.CmdDraw(cmd, 3, 1, 0, 0)
	vk.CmdEndRenderPass(cmd)

	if res := vk.EndCommandBuffer(cmd); res != vk.Success {
		return fmt.Errorf("vulkan: end command buffer: %s", vk.Error(res))
	}
	return nil
}

func (r *Renderer) destroyWorldResources() {
	wr := r.wr
	if wr == nil {
		return
	}
	dev := r.device

	r.destroyBloom()
	r.destroyAmbientPass()
	r.destroyResolvePass()
	r.destroyVoxelResources()
	r.destroyModelResources()

	for _, p := range []vk.Pipeline{wr.pipeWorld, wr.pipeDepth, wr.pipeParticleAdd, wr.pipeParticleTrans, wr.pipeWorldLiquid, wr.pipeShadow, wr.pipeBlit} {
		if p != nil {
			vk.DestroyPipeline(dev, p, nil)
		}
	}
	for _, l := range []vk.PipelineLayout{wr.plWorld, wr.plBlit, wr.plWorldLiquid, wr.plShadow} {
		if l != nil {
			vk.DestroyPipelineLayout(dev, l, nil)
		}
	}
	wr.texResidency.destroy(dev)
	if wr.framePool != nil {
		vk.DestroyDescriptorPool(dev, wr.framePool, nil)
	}
	for _, l := range []vk.DescriptorSetLayout{wr.dsl1, wr.dslBlit, wr.dslWaterCapture} {
		if l != nil {
			vk.DestroyDescriptorSetLayout(dev, l, nil)
		}
	}
	freeShadowCube(dev, wr.dummyShadowCube)
	for i := range wr.slots {
		sl := &wr.slots[i]
		if sl.fb != nil {
			vk.DestroyFramebuffer(dev, sl.fb, nil)
		}
		freeLitImage(dev, sl.scene)
		freeLitImage(dev, sl.depth)
		freeLitImage(dev, sl.preWaterColor)
		freeLitImage(dev, sl.preWaterDepth)
		freeLitImage(dev, sl.ovImg)
		for s := range sl.shadowCube {
			freeShadowCube(dev, sl.shadowCube[s])
		}
		freeHostBuffer(dev, sl.ubo, sl.uboMem, sl.uboMapped)
		freeHostBuffer(dev, sl.reach, sl.reachMem, sl.reachMapped)
		freeHostBuffer(dev, sl.vbuf, sl.vbufMem, sl.vbufMapped)
		freeHostBuffer(dev, sl.ovStage, sl.ovStageMem, sl.ovStageMapped)
	}
	wr.staticGeom.destroy(dev)
	if wr.rpOpaque != nil {
		vk.DestroyRenderPass(dev, wr.rpOpaque, nil)
	}
	if wr.rpWater != nil {
		vk.DestroyRenderPass(dev, wr.rpWater, nil)
	}
	if wr.rpShadow != nil {
		vk.DestroyRenderPass(dev, wr.rpShadow, nil)
	}
	for _, s := range []vk.Sampler{wr.clampSampler, wr.depthSampler} {
		if s != nil {
			vk.DestroySampler(dev, s, nil)
		}
	}
	r.wr = nil
}

func freeLitImage(dev vk.Device, li litImage) {
	if li.view != nil {
		vk.DestroyImageView(dev, li.view, nil)
	}
	if li.image != nil {
		vk.DestroyImage(dev, li.image, nil)
	}
	if li.memory != nil {
		vk.FreeMemory(dev, li.memory, nil)
	}
}

func freeHostBuffer(dev vk.Device, buf vk.Buffer, mem vk.DeviceMemory, mapped unsafe.Pointer) {
	if mapped != nil {
		vk.UnmapMemory(dev, mem)
	}
	if buf != nil {
		vk.DestroyBuffer(dev, buf, nil)
	}
	if mem != nil {
		vk.FreeMemory(dev, mem, nil)
	}
}

// vkFormatOf maps a worldgeo.VertexAttribute format name to a vk.Format.
func vkFormatOf(name string) vk.Format {
	switch name {
	case "R32G32B32A32_SFLOAT":
		return vk.FormatR32g32b32a32Sfloat
	case "R32G32B32_SFLOAT":
		return vk.FormatR32g32b32Sfloat
	case "R32G32_SFLOAT":
		return vk.FormatR32g32Sfloat
	case "R32_SFLOAT":
		return vk.FormatR32Sfloat
	case "R32_UINT":
		return vk.FormatR32Uint
	case "R8G8B8A8_UNORM":
		return vk.FormatR8g8b8a8Unorm
	default:
		return vk.FormatUndefined
	}
}
