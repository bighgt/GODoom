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
	"twopointfive/raster"
	"twopointfive/render"
	"twopointfive/render/worldgeo"
	"twopointfive/window"
)

// Hardware (GPU-geometry) renderer — the "Tier 2" path. The CPU walks the
// BSP into a per-texture-grouped triangle list (render/worldgeo) and the GPU
// does rasterisation, perspective-correct texturing, mip/anisotropic
// filtering, the depth test, and the exact vanilla sector/distance fade
// (world.frag samples the LUT raster.BuildLightLUT bakes). One draw call per
// texture — plain Vulkan 1.0, no bindless. Selected by config
// "renderer": "hardware"; the CPU software rasterizer stays the default and
// the fallback.
//
// Per frame DrawWorld records into one command buffer:
//
//	A  world.vert/frag   packed triangle list -> offscreen RGBA16F HDR scene
//	                     colour (render res) + a sampleable D32 depth buffer
//	B  worldbloom.go     scene bright part -> a mip-pyramid glow buffer
//	C  worldblit.frag    SSR / SSAO / god rays / contact shadow, ACES
//	                     tonemap, FXAA, CAS, screen tint, then upscale
//	                     letterboxed to the swapchain with the crisp
//	                     un-composited 2D overlay (HUD/weapon) on top
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

//go:embed shaders/depthonly.vert.spv
var depthOnlyVertSPV []byte

//go:embed shaders/depth.frag.spv
var depthFragSPV []byte

// testLightEnabled forces a magenta light at the camera (uploadWorldFrame) —
// a bisection aid for "dynamic lights don't show". Set TPF_TESTLIGHT=1.
var testLightEnabled = os.Getenv("TPF_TESTLIGHT") != ""

// worldMaxLights is the dynamic point-light cap the World UBO carries —
// keep == render.MaxLights and MAX_LIGHTS in world.frag.
const worldMaxLights = 64

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
	SpotX, SpotY, SpotZ          float32
	SpotInvR2                    float32 // 1/radius^2
	SpotDX, SpotDY, SpotDZ       float32 // normalized
	SpotCosOuter                 float32
	SpotR, SpotG, SpotB          float32
	SpotIntensity                float32
	SpotCosInner                 float32
	spotPad0, spotPad1, spotPad2 float32
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
		u.SpotCosInner, u.spotPad0, u.spotPad1, u.spotPad2,
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

	vbuf       vk.Buffer
	vbufMem    vk.DeviceMemory
	vbufMapped unsafe.Pointer
	vbufSize   int

	ovStage       vk.Buffer
	ovStageMem    vk.DeviceMemory
	ovStageMapped unsafe.Pointer
	ovImg         litImage
	ovLayout      vk.ImageLayout

	scene litImage // offscreen colour (world pass target, blit pass source)
	depth litImage // D32 depth for the world pass
	fb    vk.Framebuffer

	set1    vk.DescriptorSet // world.frag set 1: uScaleLUT, uZLUT, World UBO
	setBlit vk.DescriptorSet // worldblit.frag: uScene, uOverlay
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

	rpScene vk.RenderPass

	texSampler   vk.Sampler // wall/flat: linear + mip + anisotropic, REPEAT
	clampSampler vk.Sampler // LUT / scene / overlay: linear, clamp-to-edge
	depthSampler vk.Sampler // world-pass depth for the blit's water SSR: nearest, clamp

	scaleLUT litImage // R16_UNORM, ScaleCols x Levels (walls/sprites fade)
	zLUT     litImage // R16_UNORM, ZCols x Levels     (flats fade)
	dummyTex litImage // 1x1 black — set-0 binding 1 for textures with no brightmap

	dsl0    vk.DescriptorSetLayout // set 0: per-texture sampler2D
	dsl1    vk.DescriptorSetLayout // set 1: uScaleLUT, uZLUT, World UBO
	dslBlit vk.DescriptorSetLayout // present: uScene, uOverlay

	plWorld   vk.PipelineLayout
	plBlit    vk.PipelineLayout
	pipeWorld vk.Pipeline
	pipeDepth vk.Pipeline // opaque static depth pre-pass (reuses plWorld)
	pipeBlit  vk.Pipeline

	framePool vk.DescriptorPool // set1 + setBlit, one pair per slot
	texPool   vk.DescriptorPool // per-texture set0, grows over a session

	// Voxel models: a dedicated pipeline (vertex colour, no texture) reusing
	// set 1 (LUT + World UBO), and one static device-local mesh per KVX model.
	plVoxel   vk.PipelineLayout
	pipeVoxel vk.Pipeline
	voxCache  map[*assets.VoxelModel]*voxelMesh

	slots [maxFramesInFlight]worldSlot

	texCache map[*assets.RGBA]*worldTex // keyed by the shared bitmap pointer

	bloom worldBloom // HDR bright-pass + blur (worldbloom.go)

	// Static world geometry (all walls + flats) — one device-local vertex
	// buffer, re-uploaded only when the builder bumps StaticVersion (a door
	// / lift moved). The per-frame path only streams the small dynamic
	// (sprite / voxel-billboard) list into slots[*].vbuf.
	staticVBO     vk.Buffer
	staticVBOMem  vk.DeviceMemory
	staticVBOCap  int
	staticVersion uint32

	packBuf       []byte // reused dynamic Geometry.Pack scratch
	staticPackBuf []byte // reused static-geometry pack scratch

	dbgLightLog int // rate-limits the one-off dynamic-light diagnostic

	lightScaleRef float32

	// Hemisphere ambient tint, averaged from the level's sky bitmap once and
	// cached (keyed by the bitmap pointer) — world.frag's shadowed-pixel fill
	// takes on each map's own sky palette instead of a fixed blue.
	skyAmb    [3]float32
	skyAmbSrc *assets.RGBA
}

// NewWorld stands up the Vulkan backend on the hardware-geometry path.
// srcW/srcH is the render resolution (the offscreen scene size); vsync
// prefers FIFO; debug enables the validation layer when present. Shares the
// vanilla bring-up (instance … sync objects) and adds the world resources.
func NewWorld(win *window.Window, srcW, srcH int, vsync, debug bool) (*Renderer, error) {
	r := &Renderer{win: win, vsync: vsync, debug: debug, hw: true}
	r.srcWidth, r.srcHeight = srcW, srcH // letterboxViewport reads these

	r.wr = &worldResources{
		w: srcW, h: srcH,
		texCache:      make(map[*assets.RGBA]*worldTex),
		voxCache:      make(map[*assets.VoxelModel]*voxelMesh),
		lightScaleRef: float32(raster.LightScaleRef),
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
		r.worldCreateSceneRenderPass,
		r.worldCreateOffscreen,
		r.worldCreateBloom, // before descriptors — setBlit samples bloom mip 0
		r.worldCreateLUTs,
		r.worldCreateBuffers,
		r.worldCreateDescriptorLayouts,
		r.worldCreateDescriptors,
		r.worldCreatePipelines,
		r.worldCreateVoxelPipeline,
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
func (r *Renderer) DrawWorld(g *worldgeo.Geometry, cam worldgeo.Camera, w, h int, overlay []byte, lights []render.Light) error {
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
	if err := r.syncWorldTextures(g); err != nil {
		return err
	}
	if err := r.syncVoxelMeshes(g); err != nil {
		return err
	}
	if g.StaticVersion != r.wr.staticVersion {
		if err := r.uploadStaticWorld(g); err != nil {
			return err
		}
	}

	frame := r.currentFrame
	vk.WaitForFences(r.device, 1, []vk.Fence{r.inFlight[frame]}, vk.True, vk.MaxUint64)

	sl := &wr.slots[frame]
	if err := r.uploadWorldFrame(sl, g, cam, overlay, lights); err != nil {
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
	if err := r.recordWorldCommandBuffer(cmd, imageIndex, frame, g, cam); err != nil {
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

// uploadStaticWorld packs g.StaticTris and copies it into the device-local
// static vertex buffer (grown / created as needed). Runs only when the
// builder bumps StaticVersion — at level load and after a door/lift moves —
// so the blocking one-shot copy is off the per-frame path.
func (r *Renderer) uploadStaticWorld(g *worldgeo.Geometry) error {
	wr := r.wr
	wr.staticPackBuf = worldgeo.PackVerts(g.StaticTris, wr.staticPackBuf)
	n := len(wr.staticPackBuf)
	if n == 0 {
		wr.staticVersion = g.StaticVersion
		return nil
	}

	if n > wr.staticVBOCap {
		grown := wr.staticVBOCap
		if grown == 0 {
			grown = 1 << 16
		}
		for grown < n {
			grown *= 2
		}
		vk.DeviceWaitIdle(r.device) // no frame may still reference the old buffer
		if wr.staticVBO != nil {
			vk.DestroyBuffer(r.device, wr.staticVBO, nil)
			vk.FreeMemory(r.device, wr.staticVBOMem, nil)
		}
		b, m, err := r.deviceLocalVertexBuffer(grown)
		if err != nil {
			wr.staticVBO, wr.staticVBOMem, wr.staticVBOCap = nil, nil, 0
			return err
		}
		wr.staticVBO, wr.staticVBOMem, wr.staticVBOCap = b, m, grown
	}

	stage, stageMem, mapped, err := r.wHostBuffer(n, vk.BufferUsageFlags(vk.BufferUsageTransferSrcBit))
	if err != nil {
		return err
	}
	vk.Memcopy(mapped, wr.staticPackBuf)
	defer func() {
		vk.UnmapMemory(r.device, stageMem)
		vk.DestroyBuffer(r.device, stage, nil)
		vk.FreeMemory(r.device, stageMem, nil)
	}()
	if err := r.submitOneShot(func(cmd vk.CommandBuffer) {
		vk.CmdCopyBuffer(cmd, stage, wr.staticVBO, 1, []vk.BufferCopy{{Size: vk.DeviceSize(n)}})
	}); err != nil {
		return err
	}
	wr.staticVersion = g.StaticVersion
	return nil
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

// uploadWorldFrame packs g into slot sl's (host-visible) vertex buffer,
// growing it if the frame outgrew it, and fills the World UBO (camera + fade
// params + dynamic lights) and overlay staging. Called after sl's in-flight
// fence has been waited on.
func (r *Renderer) uploadWorldFrame(sl *worldSlot, g *worldgeo.Geometry, cam worldgeo.Camera, overlay []byte, lights []render.Light) error {
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

	dst := unsafe.Slice((*byte)(sl.ovStageMapped), wr.w*wr.h*4)
	if len(overlay) == len(dst) {
		copy(dst, overlay)
	} else {
		for i := range dst {
			dst[i] = 0
		}
	}
	return nil
}

// rewriteSlotVertexBinding is a no-op hook: the vertex buffer is bound fresh
// every frame in recordWorldCommandBuffer, so a reallocated buffer needs no
// descriptor rewrite. Kept as a named seam in case the buffer ever moves to
// a descriptor.
func (r *Renderer) rewriteSlotVertexBinding(*worldSlot) {}

func (r *Renderer) recordWorldCommandBuffer(cmd vk.CommandBuffer, imageIndex uint32, frame int, g *worldgeo.Geometry, cam worldgeo.Camera) error {
	wr := r.wr
	sl := &wr.slots[frame]
	if res := vk.BeginCommandBuffer(cmd, &vk.CommandBufferBeginInfo{SType: vk.StructureTypeCommandBufferBeginInfo}); res != vk.Success {
		return fmt.Errorf("vulkan: begin command buffer: %s", vk.Error(res))
	}

	// (1) overlay staging -> this slot's overlay image.
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
	cp := vk.BufferImageCopy{
		ImageSubresource: vk.ImageSubresourceLayers{AspectMask: vk.ImageAspectFlags(vk.ImageAspectColorBit), LayerCount: 1},
		ImageExtent:      vk.Extent3D{Width: uint32(wr.w), Height: uint32(wr.h), Depth: 1},
	}
	vk.CmdCopyBufferToImage(cmd, sl.ovStage, sl.ovImg.image, vk.ImageLayoutTransferDstOptimal, 1, []vk.BufferImageCopy{cp})
	barrier(cmd, sl.ovImg.image, rng, vk.ImageLayoutTransferDstOptimal, vk.ImageLayoutShaderReadOnlyOptimal,
		vk.AccessFlags(vk.AccessTransferWriteBit), vk.AccessFlags(vk.AccessShaderReadBit),
		vk.PipelineStageFlags(vk.PipelineStageTransferBit), vk.PipelineStageFlags(vk.PipelineStageFragmentShaderBit))
	sl.ovLayout = vk.ImageLayoutShaderReadOnlyOptimal

	// (2) world pass -> offscreen scene colour (+ depth).
	clears := []vk.ClearValue{vk.NewClearValue([]float32{0, 0, 0, 1}), vk.NewClearDepthStencil(1, 0)}
	vk.CmdBeginRenderPass(cmd, &vk.RenderPassBeginInfo{
		SType:           vk.StructureTypeRenderPassBeginInfo,
		RenderPass:      wr.rpScene,
		Framebuffer:     sl.fb,
		RenderArea:      vk.Rect2D{Extent: vk.Extent2D{Width: uint32(wr.w), Height: uint32(wr.h)}},
		ClearValueCount: 2,
		PClearValues:    clears,
	}, vk.SubpassContentsInline)

	vk.CmdSetViewport(cmd, 0, 1, []vk.Viewport{fullViewport(wr.w, wr.h)})
	vk.CmdSetScissor(cmd, 0, 1, []vk.Rect2D{fullScissor(wr.w, wr.h)})

	vp := worldgeo.ViewProj(cam, wr.w, wr.h)

	// Depth pre-pass: rasterise the opaque static geometry to the depth
	// buffer only (no fragment shading), so the colour pass's early-Z
	// rejects every occluded wall/flat pixel before it runs the fade + light
	// loop. Masked 2-sided middles (StaticTris[StaticOpaqueCount:]) are
	// excluded — their holes must not write depth.
	if wr.pipeDepth != nil && wr.staticVBO != nil && g.StaticOpaqueCount > 0 {
		vk.CmdBindPipeline(cmd, vk.PipelineBindPointGraphics, wr.pipeDepth)
		vk.CmdPushConstants(cmd, wr.plWorld, vk.ShaderStageFlags(vk.ShaderStageVertexBit), 0, 64, unsafe.Pointer(&vp[0]))
		vk.CmdBindVertexBuffers(cmd, 0, 1, []vk.Buffer{wr.staticVBO}, []vk.DeviceSize{0})
		vk.CmdDraw(cmd, uint32(g.StaticOpaqueCount), 1, 0, 0)
	}

	vk.CmdBindPipeline(cmd, vk.PipelineBindPointGraphics, wr.pipeWorld)
	vk.CmdPushConstants(cmd, wr.plWorld, vk.ShaderStageFlags(vk.ShaderStageVertexBit), 0, 64, unsafe.Pointer(&vp[0]))
	vk.CmdBindDescriptorSets(cmd, vk.PipelineBindPointGraphics, wr.plWorld, 1, 1, []vk.DescriptorSet{sl.set1}, 0, nil)

	drawRuns := func(buf vk.Buffer, runs []worldgeo.Draw) {
		if buf == nil || len(runs) == 0 {
			return
		}
		vk.CmdBindVertexBuffers(cmd, 0, 1, []vk.Buffer{buf}, []vk.DeviceSize{0})
		for _, d := range runs {
			if d.Count == 0 {
				continue
			}
			tex := d.Tex
			if tex == nil { // sky run
				tex = g.Sky
			}
			wt := wr.texCache[tex]
			if wt == nil {
				continue // texture unresolved / not uploaded — skip the run
			}
			vk.CmdBindDescriptorSets(cmd, vk.PipelineBindPointGraphics, wr.plWorld, 0, 1, []vk.DescriptorSet{wt.set}, 0, nil)
			vk.CmdDraw(cmd, uint32(d.Count), 1, uint32(d.First), 0)
		}
	}
	drawRuns(wr.staticVBO, g.StaticDraws) // level walls + flats (device-local)
	drawRuns(sl.vbuf, g.Draws)            // this frame's sprite / voxel-billboard quads
	// Voxel models — own pipeline, still inside the world pass so the depth
	// buffer sorts them against the level and the textured geometry.
	r.recordVoxels(cmd, sl, g, cam)
	vk.CmdEndRenderPass(cmd)
	// rpScene's finalLayout transitions the scene image to shader-read for us.

	// (3) bloom: build the HDR mip pyramid -> bloom mip 0.
	r.recordBloom(cmd, frame)

	// (4) present blit: tonemap(scene*exposure + bloom*strength), upscaled
	// letterboxed, with the crisp overlay composited on top.
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
	vk.CmdBindDescriptorSets(cmd, vk.PipelineBindPointGraphics, wr.plBlit, 0, 1, []vk.DescriptorSet{sl.setBlit}, 0, nil)
	// worldblit.frag PC: tonemap params + the camera basis it needs to
	// reconstruct a world point from (screen pixel, sampled depth) and march
	// the depth buffer for the water SSR reflection. horizonY / focal match
	// raster.horizonY and world.frag skyColorAt.
	bFocal := worldgeo.FocalFor(cam, wr.h)
	bSin := float32(math.Sin(cam.Angle))
	bCos := float32(math.Cos(cam.Angle))
	bHorizon := float32(wr.h)*0.5 + float32(math.Tan(cam.Pitch)*bFocal)
	// worldblit.frag post-chain tuning. Any *Strength at 0 disables that
	// stage. sunDir is a fixed, normalised world-space direction (high,
	// toward +X/+Y) so god rays streak from a bright sky opening the player
	// is roughly facing.
	const (
		ssrStrength      = 0.9
		aoStrength       = 0.40 // deepest crease darkens ~40%; AO multiplies final light
		aoRadius         = 48.0
		aoBias           = 1.5
		godrayStrength   = 0.35
		sharpenStrength  = 0.35              // CAS 0..1
		ditherStrength   = 1.0               // +/- 1 LSB triangular-PDF, kills 8-bit banding
		sunX, sunY, sunZ = 0.40, 0.30, 0.866 // |sun| == 1, ~60 deg elevation
		// Screen-space dynamic-light shadows (worldblit.frag dynShadow):
		// strength 0 disables. march = how far along the ray-to-light to
		// search for an occluder; bias in camera-forward units.
		dynShadowStrength = 0.6
		dynShadowMarch    = 320.0
		dynShadowBias     = 2.0
	)
	blitPC := [32]float32{
		wr.bloom.exposure, wr.bloom.strength, float32(cam.Time), float32(worldgeo.NearPlane),
		bSin, bCos, float32(bFocal), float32(worldgeo.FarPlane),
		float32(cam.X), float32(cam.Y), float32(cam.Z), bHorizon,
		float32(wr.w), float32(wr.h), ssrStrength, aoStrength,
		sunX, sunY, sunZ, godrayStrength,
		aoRadius, sharpenStrength, ditherStrength, aoBias,
		// p6: screen-space dynamic-light shadows — count (reads lightPos[0..N)
		// from the world UBO), strength, march distance, depth bias.
		float32(cam.ShadowCasterN), dynShadowStrength, dynShadowMarch, dynShadowBias,
		// p7: full-frame sector-effect tint (rgb, amount; A<0 also = heat wobble)
		cam.TintR, cam.TintG, cam.TintB, cam.TintA,
	}
	vk.CmdPushConstants(cmd, wr.plBlit, vk.ShaderStageFlags(vk.ShaderStageFragmentBit), 0, 128, unsafe.Pointer(&blitPC[0]))
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
	r.destroyVoxelResources()

	for _, p := range []vk.Pipeline{wr.pipeWorld, wr.pipeDepth, wr.pipeBlit} {
		if p != nil {
			vk.DestroyPipeline(dev, p, nil)
		}
	}
	for _, l := range []vk.PipelineLayout{wr.plWorld, wr.plBlit} {
		if l != nil {
			vk.DestroyPipelineLayout(dev, l, nil)
		}
	}
	if wr.texPool != nil {
		vk.DestroyDescriptorPool(dev, wr.texPool, nil)
	}
	if wr.framePool != nil {
		vk.DestroyDescriptorPool(dev, wr.framePool, nil)
	}
	for _, l := range []vk.DescriptorSetLayout{wr.dsl0, wr.dsl1, wr.dslBlit} {
		if l != nil {
			vk.DestroyDescriptorSetLayout(dev, l, nil)
		}
	}
	for _, wt := range wr.texCache {
		freeLitImage(dev, wt.img)
		freeLitImage(dev, wt.bright)
	}
	wr.texCache = nil
	freeLitImage(dev, wr.scaleLUT)
	freeLitImage(dev, wr.zLUT)
	freeLitImage(dev, wr.dummyTex)
	for i := range wr.slots {
		sl := &wr.slots[i]
		if sl.fb != nil {
			vk.DestroyFramebuffer(dev, sl.fb, nil)
		}
		freeLitImage(dev, sl.scene)
		freeLitImage(dev, sl.depth)
		freeLitImage(dev, sl.ovImg)
		freeHostBuffer(dev, sl.ubo, sl.uboMem, sl.uboMapped)
		freeHostBuffer(dev, sl.vbuf, sl.vbufMem, sl.vbufMapped)
		freeHostBuffer(dev, sl.ovStage, sl.ovStageMem, sl.ovStageMapped)
	}
	if wr.staticVBO != nil {
		vk.DestroyBuffer(dev, wr.staticVBO, nil)
		vk.FreeMemory(dev, wr.staticVBOMem, nil)
		wr.staticVBO, wr.staticVBOMem = nil, nil
	}
	if wr.rpScene != nil {
		vk.DestroyRenderPass(dev, wr.rpScene, nil)
	}
	for _, s := range []vk.Sampler{wr.texSampler, wr.clampSampler, wr.depthSampler} {
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
