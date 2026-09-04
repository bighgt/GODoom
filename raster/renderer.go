// Package raster is a software rasterizer that renders a Doom level the way
// id Software's own C engine did: a front-to-back BSP walk (package bsp)
// draws each subsector's walls as perspective-correct textured columns,
// narrowing a per-column "still open" vertical range exactly like Doom's
// ceilingclip/floorclip arrays, and floors/ceilings are filled into
// whatever range each wall newly exposes. See game_design.txt section 7 for
// the specific, deliberate ways this differs from id's fixed-point,
// tangent-table implementation, and how the RGBA frame it produces reaches
// the screen through the Vulkan renderer (package render/vulkan).
package raster

import (
	"log"
	"math"
	"runtime"
	"sync"

	"twopointfive/assets"
	"twopointfive/bsp"
	"twopointfive/wad"
)

// InternalWidth and InternalHeight are the reference render resolution —
// vanilla Doom's own 320x200 framebuffer, and the aspect (16:10) the FOV
// math is calibrated against (see Render). New takes an explicit size now;
// these are the fallback/default and the reference the horizontal FOV is
// defined at.
const (
	InternalWidth  = 320
	InternalHeight = 200

	// referenceAspect is the width:height the configured horizontal FOV is
	// specified at. Wider render targets keep the resulting vertical FOV
	// and widen horizontally (Hor+); narrower ones do the reverse.
	referenceAspect = float64(InternalWidth) / float64(InternalHeight)

	// hudLogicalW/hudLogicalH is the coordinate space the 2D overlay (weapon
	// viewmodel, status bar, debug HUD) is authored in — vanilla's 320x200,
	// the space every ST_* position constant is defined in. Overlay drawing
	// scales from here to the render resolution (uniform, horizontally
	// centered, bottom-anchored) so the HUD keeps its proportions at any
	// resolution, while a hi-res sprite override (assets, TexelsPerUnit=4)
	// still contributes its extra detail. SetHUDScale applies a user
	// multiplier on top of that fit (config.json's hudScale).
	hudLogicalW = InternalWidth
	hudLogicalH = InternalHeight
)

// nearPlane is the minimum camera-space depth (map units) a point must be
// at to be projectable; segs entirely closer than this are culled.
const nearPlane = 1.0

// Renderer owns the CPU-side RGBA framebuffer and the decoded texture cache,
// and renders one Level/BSP/Camera combination into that framebuffer per call.
type Renderer struct {
	Width, Height int
	Pix           []byte // RGBA8, row-major, len == Width*Height*4

	textures *assets.Textures
	// skybox is the external Mars sky panorama (see assets.LoadSkybox); when
	// present it overrides every level's F_SKY1 ceilings. nil (the asset is
	// missing / failed to decode) falls buildSkyTable back to the WAD's own
	// sky, picked per map the way PrBoom does — see skyTextureForMap.
	skybox *assets.RGBA

	// strips partitions the framebuffer into vertical column bands, one per
	// render worker. Each strip carries its own ceiling/floor occlusion
	// clips (full-width slices, but only [x0,x1] is ever touched by its
	// owner) so the BSP walk can run concurrently — see Render. With a
	// single worker there is exactly one strip spanning the whole width.
	strips []stripCtx

	// blank is a pre-built copy of a cleared framebuffer (opaque black) —
	// each frame starts by copy()ing it over r.Pix, a single memmove, rather
	// than looping every pixel.
	blank []byte

	// depth holds the camera-space depth of the wall/flat pixel drawn at
	// each framebuffer cell (Width*Height, row-major). Written by
	// drawWallSpan/drawFlatSpan; sprites test against it so things occlude
	// correctly behind level geometry. blankDepth resets it each frame.
	depth      []float32
	blankDepth []float32

	// overlay is a full-resolution (Width x Height) RGBA buffer the 2D HUD
	// (weapon, status bar, debug text) is drawn into each frame — full-res,
	// not 320x200, so a hi-res sprite override renders at its real detail.
	// Cleared to transparent at the start of Render, alpha-composited onto
	// r.Pix by CompositeOverlay just before the frame is presented.
	overlay []byte

	// overlay dirty-rect. The HUD only ever touches a fraction of the plane
	// (a bottom strip for the status bar, a centred block for the weapon, a
	// few lines top-left for the debug text). [ovY0,ovY1) is the row span
	// this frame's overlay draws touched (markOverlay); ovPrevY0/1 is last
	// frame's, which Render clears at the start of the next one so a moved or
	// vanished element leaves no stale pixels. Deferred() reports ovY0/ovY1
	// to the enhanced backend, which unions across the frames it buffers and
	// uploads only those rows instead of the whole plane. Empty == ovY1<=ovY0.
	ovY0, ovY1         int
	ovPrevY0, ovPrevY1 int

	// gbuf, when set (SetGBuffer), makes the wall/flat/sprite draws emit a
	// deferred-shading G-buffer instead of baking lighting into Pix: Pix
	// holds unlit albedo, Normal packs the surface normal (rgb, *0.5+0.5)
	// plus a material key (a: 0 world / 0.5 sprite / 1 emissive), and
	// LightParam.r is the pixel's sector light level. depth is written in
	// both modes. Consumed by render/vulkan's enhanced lighting pass (config
	// lightingMode "enhanced"); nil and unused in the default vanilla mode.
	Normal     []byte
	LightParam []byte
	gbuf       bool

	// texQ / maxAniso is the single global world-texture filter (config
	// textureQuality), set by SetTextureQuality — see texfilter.go. It drives
	// walls and flats directly (worldMipsCache holds their lazily-built,
	// wrap-addressed mip chains) and fans out to spriteFilter / voxelSmooth
	// for the sprite and voxel paths.
	texQ           texQuality
	maxAniso       int
	worldMipsCache map[*assets.RGBA]*texMips
	worldMipsMu    sync.Mutex

	// spriteFilter (filterNearest/Bilinear/Trilinear) is how blitBillboard
	// samples a world sprite when scaling it to the screen; spriteMips
	// caches the per-sprite box-filtered mip chain trilinear needs, keyed by
	// the (stable, assets-cached) *RGBA pointer. See spritemips.go.
	spriteFilter int
	spriteMips   map[*assets.RGBA]*mipChain
	spriteMipsMu sync.Mutex

	// voxelScale is map units per voxel for DrawVoxel (config voxelScale);
	// Cheello's Voxel Doom is authored at 1. voxelSmooth (config voxelFilter)
	// coverage-antialiases each voxel splat. See raster/voxel.go.
	voxelScale  float64
	voxelSmooth bool
	// groundShadows draws a soft contact shadow on the floor under every
	// thing and the player (config shadows, raster/shadow.go).
	groundShadows bool

	// animTime is a free-running wall-clock seconds value the engine pushes
	// each frame (SetAnimTime) for animated surface effects — currently the
	// water ripple in shadeLiquid/blendWater. 0 = static (tests leave it so,
	// which keeps serial and parallel renders bit-identical).
	animTime float64

	// Distance fog (config fogColor / fogDensity): fogRGB 0..1, fogDensity
	// the exponential strength. Applied by ApplyFog as a post-pass over
	// Pix+depth — the vanilla path only; the enhanced/hardware paths fog in
	// their shaders. fogDensity == 0 => ApplyFog is a no-op.
	fogR, fogG, fogB float32
	fogDensity       float64

	// hudScale / hudOffX / hudOffY map the HUD's 320x200 authoring space
	// (hudLogicalW/H) onto the render resolution: a uniform scale,
	// horizontally centered, with the plane's bottom edge on the
	// framebuffer's bottom edge so the status bar stays anchored there. The
	// debug text overlay ignores the offsets and pins itself to the
	// top-left (raster/hud.go). hudScale = hudFit * hudUserScale; hudFit
	// fills the framebuffer height, hudUserScale is the user multiplier
	// (SetHUDScale, config.json's hudScale). applyHUDScale recomputes these
	// whenever either changes.
	hudFit           float64
	hudUserScale     float64
	hudScale         float64
	hudOffX, hudOffY int

	// weaponScale / weaponOffX / weaponOffY are the same 320x200 -> render
	// mapping for the weapon viewmodel, independent of the status bar's
	// hudUserScale: weaponScale = hudFit * weaponUserScale, so the held
	// weapon fills the screen height by default and is resized only by its
	// own knob (config weaponScale, SetWeaponScale). weaponUserScale
	// defaults to 1.
	weaponUserScale        float64
	weaponScale            float64
	weaponOffX, weaponOffY int

	focal      float64 // pinhole-camera focal length in pixels, recomputed per frame from FOV
	pitchShear float64 // vertical screen-space shift implementing Camera.Pitch, recomputed per frame
	sinA, cosA float64 // sin/cos of Camera.Angle, recomputed once per frame and reused by every projection

	// Sky sampling tables, rebuilt by buildSkyTable each frame (see sky.go).
	// skyTex is the resolved sky bitmap (external skybox, or the WAD's own
	// per-map sky); skyScale converts id's 128-tall sky constants to it.
	// skyTx[x] is the texture column for screen column x (depends on x +
	// yaw); skyColAngle[x] is the per-column view angle, only recomputed
	// when focal changes (i.e. basically never after the first frame).
	// skyWADTex / skyWADFor cache the WAD sky resolved for a given map name,
	// so the per-map SKY1..4 lookup (PrBoom's rule) isn't repeated per frame.
	skyTex           *assets.RGBA
	skyScale         float64
	skyTx            []int
	skyColAngle      []float64
	skyColAngleFocal float64
	skyWADTex        *assets.RGBA
	skyWADFor        string
	skyNameOverride  string // UMAPINFO skytexture (SetSkyName); "" = per-episode default
}

// stripCtx is one render worker's slice of the frame: the inclusive screen
// column range [x0, x1] it owns, plus its private occlusion clips. The clip
// slices are allocated full-width so the renderer can keep indexing them by
// absolute column x; only [x0, x1] is read or written by the strip's owner,
// and no two strips' ranges overlap, so r.Pix / r.depth writes need no
// synchronisation either.
type stripCtx struct {
	x0, x1    int
	ceilClip  []int // rows [0, ceilClip[x]) already drawn from the top
	floorClip []int // rows [floorClip[x], Height) already drawn from the bottom
}

// minStripWidth is the narrowest screen strip worth handing to its own
// goroutine — below this the BSP-walk overhead (run once per strip) starts
// to outweigh the parallelism.
const minStripWidth = 32

// draw2DCtx is one worker's exclusive screen-column band [xlo, xhi) for the
// parallelised post-world 2D pass (sprites and voxel models). Those draws
// depth-test every pixel they write, so partitioning the work by column —
// the same disjoint bands the strip renderer uses — needs no locking. The
// full-frame value is {0, r.Width}.
type draw2DCtx struct{ xlo, xhi int }

// parallel2D runs fn once per render strip, each with that strip's column
// band, on the strip workers. Used for the 2D passes that follow the world
// render (DrawThings).
func (r *Renderer) parallel2D(fn func(c draw2DCtx)) {
	nw := len(r.strips)
	parallelFor(nw, func(i int) {
		s := &r.strips[i]
		fn(draw2DCtx{xlo: s.x0, xhi: s.x1 + 1})
	})
}

// fullBand is the whole-frame column range — what the public single-threaded
// draw entry points pass down.
func (r *Renderer) fullBand() draw2DCtx { return draw2DCtx{0, r.Width} }

// parallelFor runs fn(0..n-1), each on its own goroutine (fn(0) on the
// caller's), and returns once all have finished. n <= 1 just calls fn
// inline.
func parallelFor(n int, fn func(i int)) {
	if n <= 1 {
		if n == 1 {
			fn(0)
		}
		return
	}
	var wg sync.WaitGroup
	wg.Add(n - 1)
	for i := 1; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			fn(i)
		}(i)
	}
	fn(0)
	wg.Wait()
}

// New creates a renderer targeting a width x height framebuffer, resolving
// textures/flats through textures. Pass raster.InternalWidth/InternalHeight
// for the vanilla reference resolution, or any larger size for a
// higher-resolution render (the Vulkan blit stage letterboxes whatever
// size this produces into the window).
func New(textures *assets.Textures, width, height int) *Renderer {
	if width < 1 {
		width = InternalWidth
	}
	if height < 1 {
		height = InternalHeight
	}
	skybox, err := assets.LoadSkybox()
	if err != nil {
		// Non-fatal: drawSkySpan falls back to the WAD's own SKY1 lump.
		log.Printf("raster: %v (falling back to the WAD's own sky texture)", err)
	}
	blank := make([]byte, width*height*4)
	for i := 3; i < len(blank); i += 4 {
		blank[i] = 255 // opaque
	}
	blankDepth := make([]float32, width*height)
	for i := range blankDepth {
		blankDepth[i] = math.MaxFloat32
	}

	// One render worker per hardware thread, but never so many that a strip
	// falls below minStripWidth. The count is fixed for the renderer's life
	// so the per-strip clip buffers can be allocated once here.
	nw := runtime.GOMAXPROCS(0)
	if maxByWidth := width / minStripWidth; nw > maxByWidth {
		nw = maxByWidth
	}
	if nw < 1 {
		nw = 1
	}
	strips := make([]stripCtx, nw)
	for i := range strips {
		strips[i] = stripCtx{
			x0:        i * width / nw,
			x1:        (i+1)*width/nw - 1,
			ceilClip:  make([]int, width),
			floorClip: make([]int, width),
		}
	}

	r := &Renderer{
		Width: width, Height: height,
		Pix:        make([]byte, width*height*4),
		textures:   textures,
		skybox:     skybox,
		strips:     strips,
		blank:      blank,
		blankDepth: blankDepth,
		depth:      make([]float32, width*height),
		overlay:    make([]byte, width*height*4),
		spriteMips: make(map[*assets.RGBA]*mipChain),

		worldMipsCache: make(map[*assets.RGBA]*texMips),

		voxelScale:    1,
		voxelSmooth:   true,
		groundShadows: true,

		skyTx:            make([]int, width),
		skyColAngle:      make([]float64, width),
		skyColAngleFocal: -1, // force buildSkyTable's per-column trig on frame 1
	}
	// HUD transform: the 320x200 authoring space scaled to fill the
	// framebuffer height, times a user multiplier (default 1) — one for the
	// status bar, a separate one for the weapon viewmodel. Both planes are
	// bottom-anchored; the debug text overlay top-anchors itself separately
	// (hud.go).
	r.hudFit = math.Min(float64(width)/hudLogicalW, float64(height)/hudLogicalH)
	r.hudUserScale = 1
	r.weaponUserScale = 1
	r.applyHUDScale()
	return r
}

// Deferred is the G-buffer plus the camera parameters a GPU deferred-
// lighting pass needs to rebuild each pixel's world position from Depth —
// the exact values the most recent Render used. Only meaningful when
// SetGBuffer(true) was called; the slices alias the renderer's own buffers
// (valid until the next Render), they are not copies.
type Deferred struct {
	W, H                                int
	Albedo, Normal, LightParam, Overlay []byte
	Depth                               []float32
	Focal, SinA, CosA, HorizonY         float64

	// OverlayY/OverlayH is the row span of Overlay that changed this frame
	// (unioned with last frame's). The enhanced backend clears + uploads
	// only these rows instead of the whole plane. OverlayH == 0 means the
	// overlay is unchanged and need not be re-uploaded.
	OverlayY, OverlayH int
}

// Deferred returns the current G-buffer view (see the Deferred type). Call
// it after the 2D overlay draws for this frame are done — OverlayY/H report
// the rows those draws touched this frame (the backend is responsible for
// carrying that forward across however many frames it buffers).
func (r *Renderer) Deferred() Deferred {
	oy, oh := r.ovY0, r.ovY1-r.ovY0
	if oh < 0 {
		oh = 0
	}
	return Deferred{
		W: r.Width, H: r.Height,
		Albedo: r.Pix, Normal: r.Normal, LightParam: r.LightParam, Overlay: r.overlay,
		Depth:    r.depth,
		Focal:    r.focal,
		SinA:     r.sinA,
		CosA:     r.cosA,
		HorizonY: r.horizonY(),
		OverlayY: oy,
		OverlayH: oh,
	}
}

// BeginOverlayFrame runs just the 2D overlay plane's per-frame lifecycle —
// clear last frame's dirty rows and roll the dirty rect forward — without
// rendering the 3D world. The hardware-geometry path (Game.HardwareMode)
// calls this instead of Render each frame: the GPU draws the world, and only
// the overlay draws (DrawWeapon / DrawHUD / DrawCrosshair / DrawStatusBar)
// still run on the CPU, into r.overlay, for DrawWorld to blend on top.
func (r *Renderer) BeginOverlayFrame() {
	r.ovPrevY0, r.ovPrevY1 = r.ovY0, r.ovY1
	if r.ovY1 > r.ovY0 {
		rb := r.Width * 4
		clear(r.overlay[r.ovY0*rb : r.ovY1*rb])
	}
	r.ovY0, r.ovY1 = 0, 0 // empty; markOverlay grows it
}

// Textures returns the shared texture/flat resolver this renderer was
// created with — so the hardware-geometry path (render/worldgeo) can build
// its triangle list from the same *assets.RGBA bitmaps.
func (r *Renderer) Textures() *assets.Textures { return r.textures }

// Overlay returns the un-composited 2D overlay plane (full render
// resolution, straight-alpha RGBA8). The hardware-geometry backend
// (render/vulkan DrawWorld) blends this over the GPU-rendered world instead
// of CompositeOverlay merging it into Pix. Valid until the next frame's
// overlay draws; not a copy.
func (r *Renderer) Overlay() []byte { return r.overlay }

// SetAnimTime sets the wall-clock seconds value used by time-animated
// surface effects (the water ripple). Call once per frame before Render.
func (r *Renderer) SetAnimTime(t float64) { r.animTime = t }

// SetFog sets the distance-fog colour (0..1) and exponential density for
// ApplyFog. density <= 0 disables it. Set once at startup (config).
func (r *Renderer) SetFog(fr, fg, fb float32, density float64) {
	r.fogR, r.fogG, r.fogB, r.fogDensity = fr, fg, fb, density
}

// SetSkyName forces the WAD sky texture buildSkyTable uses, overriding the
// per-episode default (skyTextureForMap) — for a UMAPINFO `skytexture` key.
// "" restores the default. An external skybox panorama (assets/skybox) still
// wins over both. Call on level load.
func (r *Renderer) SetSkyName(name string) {
	if name == r.skyNameOverride {
		return
	}
	r.skyNameOverride = name
	r.skyWADTex, r.skyWADFor = nil, "" // force re-resolve next buildSkyTable
}

// ApplyFog blends every rasterised world pixel toward the fog colour by
// 1 - exp(-density * cameraDistance), reading the per-pixel depth plane.
// Background pixels (depth left at +Inf/MaxFloat32 — the sky) are skipped.
// A no-op when fog is disabled. Call after the world + sprites are drawn
// and before the 2D overlay (vanilla path only — the enhanced and hardware
// paths fog in their shaders).
func (r *Renderer) ApplyFog() {
	if r.fogDensity <= 0 {
		return
	}
	fr, fg, fb := float64(r.fogR)*255, float64(r.fogG)*255, float64(r.fogB)*255
	dens := r.fogDensity
	dst, dep := r.Pix, r.depth
	nw := len(r.strips)
	if nw < 1 {
		nw = 1
	}
	n := len(dep)
	parallelFor(nw, func(k int) {
		lo, hi := k*n/nw, (k+1)*n/nw
		for cell := lo; cell < hi; cell++ {
			d := dep[cell]
			if d >= math.MaxFloat32 || d <= 0 {
				continue
			}
			f := 1 - math.Exp(-dens*float64(d))
			if f <= 0 {
				continue
			}
			if f > 1 {
				f = 1
			}
			i := cell * 4
			g := 1 - f
			dst[i] = byte(float64(dst[i])*g + fr*f)
			dst[i+1] = byte(float64(dst[i+1])*g + fg*f)
			dst[i+2] = byte(float64(dst[i+2])*g + fb*f)
		}
	})
}

// SetGBuffer turns deferred-shading output on or off (see the Normal /
// LightParam fields). The two extra planes are allocated on the first
// enable and then kept.
func (r *Renderer) SetGBuffer(on bool) {
	r.gbuf = on
	if on && r.Normal == nil {
		r.Normal = make([]byte, r.Width*r.Height*4)
		r.LightParam = make([]byte, r.Width*r.Height*4)
	}
}

// markOverlay records that overlay rows [y0, y1) were written this frame, so
// Render's next clear and the enhanced path's upload can be limited to the
// touched span. Callers pass their destination pixel bounds; the span is
// clamped and only ever grows within a frame.
func (r *Renderer) markOverlay(y0, y1 int) {
	if y0 < 0 {
		y0 = 0
	}
	if y1 > r.Height {
		y1 = r.Height
	}
	if y0 >= y1 {
		return
	}
	if r.ovY1 <= r.ovY0 { // empty -> set
		r.ovY0, r.ovY1 = y0, y1
		return
	}
	if y0 < r.ovY0 {
		r.ovY0 = y0
	}
	if y1 > r.ovY1 {
		r.ovY1 = y1
	}
}

// SetHUDScale sets the user multiplier applied on top of the automatic
// 320x200->framebuffer fit for the status bar (config.json's hudScale): 1
// keeps the authored proportions, less than 1 shrinks it toward the
// bottom-centre. A non-positive value resets to 1.
func (r *Renderer) SetHUDScale(mul float64) {
	if mul <= 0 {
		mul = 1
	}
	r.hudUserScale = mul
	r.applyHUDScale()
}

// SetWeaponScale sets the weapon viewmodel's user multiplier (config.json's
// weaponScale), independent of SetHUDScale: 1 fills the screen height,
// less than 1 draws a smaller gun. A non-positive value resets to 1.
func (r *Renderer) SetWeaponScale(mul float64) {
	if mul <= 0 {
		mul = 1
	}
	r.weaponUserScale = mul
	r.applyHUDScale()
}

// applyHUDScale recomputes the status-bar transform (hudFit * hudUserScale)
// and the weapon transform (hudFit * weaponUserScale), each with its own
// centre / bottom-anchor offsets.
func (r *Renderer) applyHUDScale() {
	r.hudScale = r.hudFit * r.hudUserScale
	r.hudOffX = (r.Width - int(hudLogicalW*r.hudScale)) / 2
	r.hudOffY = r.Height - int(hudLogicalH*r.hudScale) // bottom-anchored

	r.weaponScale = r.hudFit * r.weaponUserScale
	r.weaponOffX = (r.Width - int(hudLogicalW*r.weaponScale)) / 2
	r.weaponOffY = r.Height - int(hudLogicalH*r.weaponScale)
}

// Render draws one frame of level, viewed from cam, into r.Pix.
func (r *Renderer) Render(level *wad.Level, tree bsp.Node, cam Camera) {
	fov := cam.FOVDeg
	if fov <= 0 {
		fov = 90
	}
	// cam.FOVDeg is the *horizontal* FOV at referenceAspect. Convert it to a
	// vertical FOV (held constant across render aspects) and derive the
	// isotropic focal length from the actual render height — so a wider
	// render target shows more to the sides (Hor+) rather than squashing.
	hRef := fov * math.Pi / 180
	vFOV := 2 * math.Atan(math.Tan(hRef/2)/referenceAspect)
	r.focal = float64(r.Height) / 2 / math.Tan(vFOV/2)
	// tan(pitch)*focal is the standard software-renderer freelook
	// approximation: shift the whole projection vertically rather than
	// truly rotating the camera in 3D (see Camera.Pitch's doc comment).
	r.pitchShear = math.Tan(cam.Pitch) * r.focal
	// Hoisted out of the per-seg / per-flat-span projection math below.
	r.sinA, r.cosA = math.Sin(cam.Angle), math.Cos(cam.Angle)
	// Per-column sky sample tables, so drawSkySpan is a straight texel copy
	// with no trig (see sky.go). level supplies the map name for the
	// per-episode / per-map-range WAD sky pick.
	r.buildSkyTable(&cam, level)

	camp := &cam
	cx, cy := float32(cam.X), float32(cam.Y)
	nw := len(r.strips)

	// Phase 1 (parallel): clear r.Pix and r.depth and reset every strip's
	// occlusion clips. The buffer clears partition r.Pix / r.depth into
	// contiguous byte ranges (fast memmove-friendly copies), which is
	// independent of the column-strip partitioning used for drawing — any
	// split works for a clear. A full barrier follows so no worker starts
	// drawing into a region another is still clearing.
	//
	//   r.blank      — void backdrop (opaque black) for pixels no wall covers
	//   r.blankDepth — every cell at +inf so the first surface always wins
	//
	// r.overlay is cleared separately, just below, over only last frame's
	// dirty rows (the 2D HUD touches a small fraction of the plane).
	//
	// Normal / LightParam are NOT cleared. Every G-buffer draw path writes
	// them together with r.depth for each pixel it covers (drawWallSpan /
	// drawFlatSpan / blitBillboard / DrawVoxel), and
	// light.frag reads them only after checking depth != +Inf — so a pixel
	// nothing drew this frame keeps stale bytes that are never sampled.
	// Skipping the two full-plane memsets is ~2*W*H*4 bytes of memory
	// traffic saved every frame, which matters most at the high render
	// resolutions where the software rasterizer is already memory-bound.
	parallelFor(nw, func(i int) {
		plo, phi := i*len(r.Pix)/nw, (i+1)*len(r.Pix)/nw
		copy(r.Pix[plo:phi], r.blank[plo:phi])
		dlo, dhi := i*len(r.depth)/nw, (i+1)*len(r.depth)/nw
		copy(r.depth[dlo:dhi], r.blankDepth[dlo:dhi])

		s := &r.strips[i]
		for x := s.x0; x <= s.x1; x++ {
			s.ceilClip[x] = 0
			s.floorClip[x] = r.Height
		}
	})

	// r.overlay: clear only the row span last frame's HUD touched (the 2D
	// draws that follow re-dirty their own rows via markOverlay), and roll
	// the rect forward so Deferred() can union it with this frame's.
	r.ovPrevY0, r.ovPrevY1 = r.ovY0, r.ovY1
	if r.ovY1 > r.ovY0 {
		rb := r.Width * 4
		clear(r.overlay[r.ovY0*rb : r.ovY1*rb])
	}
	r.ovY0, r.ovY1 = 0, 0 // empty; markOverlay grows it

	// Phase 2 (parallel): each worker walks the whole BSP front-to-back but
	// only draws the columns inside its own strip, into its private clips
	// and a disjoint set of r.Pix / r.depth columns. The tree walk itself is
	// a read-only recursion, so running it once per worker is safe and
	// cheap next to the per-pixel fill it drives.
	parallelFor(nw, func(i int) {
		s := &r.strips[i]
		bsp.Traverse(tree, cx, cy, func(leaf *bsp.Leaf) {
			r.renderSubsector(level, leaf, camp, s)
		})
	})
}

func (r *Renderer) renderSubsector(level *wad.Level, leaf *bsp.Leaf, cam *Camera, ctx *stripCtx) {
	ss := leaf.Subsector
	for i := uint16(0); i < ss.SegCount; i++ {
		segIdx := int(ss.FirstSeg) + int(i)
		if segIdx >= len(level.Segs) {
			continue
		}
		r.renderSeg(level, level.Segs[segIdx], cam, ctx)
	}
}

// toCameraSpace rotates a world-space point into camera space: depth is the
// distance along the camera's forward axis, horiz the signed offset along
// its right axis (positive = to the right of center). Uses r.sinA/r.cosA,
// which Render recomputes once per frame — so callers outside Render (e.g.
// DrawWorldSprite) rely on the most recent Render having set them.
func (r *Renderer) toCameraSpace(cam *Camera, wx, wy float64) (depth, horiz float64) {
	relX := wx - cam.X
	relY := wy - cam.Y
	depth = relX*r.cosA + relY*r.sinA
	horiz = relX*r.sinA - relY*r.cosA
	return
}

// segDraw carries everything renderColumn needs that stays constant across
// a seg's columns: the resolved sidedefs/sectors, the linedef flags and
// fake-contrast light level, and — the reason it exists — the wall textures
// and floor/ceiling flats resolved once here instead of via a string-keyed
// map lookup for every column the seg covers.
type segDraw struct {
	frontSD           *wad.Sidedef
	frontSec, backSec *wad.Sector
	flags             uint16
	wallLight         int16

	// nx, ny is the seg's front-facing unit normal in world space (z is 0 —
	// Doom walls are vertical). Only set/used when r.gbuf is on.
	nx, ny float32

	midTex, upperTex, lowerTex *assets.RGBA // nil == "no texture here"
	haveUpper                  bool         // upper texture names a real lump (vs "-"/empty)
	floorFlat                  *assets.RGBA
	floorLiquid                uint8 // liquidNone / liquidWater — drawFlatSpan surface effect
	ceilFlat                   *assets.RGBA // nil when ceilIsSky, or an unnamed ceiling
	ceilIsSky                  bool

	// brightmap masks (assets.Textures.Brightmap), each resized to its base
	// texture's pixel dims, or nil when the loaded packs define none for
	// that name. drawWallSpan/drawFlatSpan draw a masked texel at full
	// brightness (vanilla) / stamp LightParam.g for light.frag (enhanced).
	midBright, upperBright, lowerBright *assets.RGBA
	floorBright, ceilBright             *assets.RGBA
}

// Liquid-floor kinds for drawFlatSpan's surface effects: water gets a
// mirrored-sky reflection, lava a warm emissive glow, nukage/slime a green
// emissive tint. The same three are recognised (and shaded to match) in the
// hardware path — worldgeo.liquidKindOfFlat + world.frag. Spill light onto
// nearby geometry is separate (real dynamic lights, engine/liquidlights.go).
// Classified from the floor flat's family prefix so it still matches
// whichever animation frame is showing.
const (
	liquidNone uint8 = iota
	liquidWater
	liquidLava
	liquidNukage
)

// matWater is the G-buffer material key (Normal.a byte) drawFlatSpan stamps
// on water pixels; light.frag recognises it (0.10..0.22) and damps the
// dynamic-light contribution so a muzzle flash over water reads as a glint,
// not a flood. Stays clear of the sprite (0.25..0.75) and emissive (>0.75)
// buckets.
const matWater = 40 // 40/255 ≈ 0.157

func classifyFloorLiquid(name string) uint8 {
	switch {
	case len(name) >= 6 && name[:6] == "FWATER":
		return liquidWater
	case (len(name) >= 4 && name[:4] == "LAVA") || name == "FIRELAVA":
		return liquidLava
	case (len(name) >= 6 && name[:6] == "NUKAGE") || (len(name) >= 5 && name[:5] == "SLIME"):
		return liquidNukage
	}
	return liquidNone
}

func (r *Renderer) renderSeg(level *wad.Level, seg wad.Seg, cam *Camera, ctx *stripCtx) {
	if int(seg.StartVertex) >= len(level.Vertexes) || int(seg.EndVertex) >= len(level.Vertexes) {
		return
	}
	v1 := level.Vertexes[seg.StartVertex]
	v2 := level.Vertexes[seg.EndVertex]
	x1, y1 := float64(v1.X), float64(v1.Y)
	x2, y2 := float64(v2.X), float64(v2.Y)

	// Backface cull in world space. `cross` below is the 2D cross product
	// (v1-cam) x (v2-cam); it's < 0 exactly when the seg is front-facing —
	// the same decision the old `sx1 >= sx2` screen-space test made for a
	// seg fully in front of the camera (h1/d1 < h2/d2  <=>  cross < 0,
	// after the sign flip from the reflection in toCameraSpace's rotation).
	// The point of doing it here, before projecting, is a seg with one
	// endpoint *behind* the camera: near-plane clipping its projection can
	// flip sx1/sx2 and make a genuinely visible wall look backfacing, so it
	// gets dropped — a whole room going black and see-through, as reported
	// on Doom II MAP01's exit.
	if cross := (x2-x1)*(cam.Y-y1) - (y2-y1)*(cam.X-x1); cross >= 0 {
		return
	}

	d1, h1 := r.toCameraSpace(cam, x1, y1)
	d2, h2 := r.toCameraSpace(cam, x2, y2)

	// t is the distance along the seg (map units, from its start vertex) at
	// each endpoint — carried alongside depth/horiz so it can be clipped
	// and perspective-interpolated the same way, giving correct wall
	// texture-space X per column.
	t1, t2 := 0.0, math.Hypot(x2-x1, y2-y1)

	if d1 < nearPlane && d2 < nearPlane {
		return // entirely behind the near plane
	}
	if d1 < nearPlane {
		f := (nearPlane - d1) / (d2 - d1)
		h1 += (h2 - h1) * f
		t1 += (t2 - t1) * f
		d1 = nearPlane
	} else if d2 < nearPlane {
		f := (nearPlane - d2) / (d1 - d2)
		h2 += (h1 - h2) * f
		t2 += (t1 - t2) * f
		d2 = nearPlane
	}

	halfW := float64(r.Width) / 2
	sx1 := halfW + h1/d1*r.focal
	sx2 := halfW + h2/d2*r.focal
	if sx1 >= sx2 {
		// Front-facing per the world-space cull above, but the projection
		// still came out degenerate (zero-width, or an endpoint sitting on
		// the near plane after clipping). Nothing to draw, and it would
		// divide by zero in the column stepping below.
		return
	}

	xStart := int(math.Ceil(sx1 - 0.5))
	xEnd := int(math.Floor(sx2 - 0.5))
	if xStart < 0 {
		xStart = 0
	}
	if xEnd > r.Width-1 {
		xEnd = r.Width - 1
	}
	// Clamp to this worker's screen strip — the seg is projected in full
	// (its geometry is global) but only the columns this goroutine owns are
	// drawn. Doing it here, before SegSides / texture resolution, means a
	// seg outside the strip costs almost nothing.
	if xStart < ctx.x0 {
		xStart = ctx.x0
	}
	if xEnd > ctx.x1 {
		xEnd = ctx.x1
	}
	if xStart > xEnd {
		return
	}

	frontSD, frontSec, backSec := bsp.SegSides(level, seg)
	if frontSD == nil || frontSec == nil {
		return
	}
	var flags uint16
	if int(seg.Linedef) < len(level.Linedefs) {
		flags = level.Linedefs[seg.Linedef].Flags
	}

	// Fake contrast: vanilla nudges a perfectly axis-aligned wall's light
	// level so N-S faces read a touch brighter than E-W ones (r_segs.c).
	wallLight := int(frontSec.LightLevel)
	switch {
	case y1 == y2:
		wallLight -= fakeContrast
	case x1 == x2:
		wallLight += fakeContrast
	}
	wallLight = clampInt(wallLight, 0, 255)

	// Resolve textures/flats once for the whole seg. WallTexture/Flat are
	// string-keyed map lookups; doing them here instead of per column is the
	// single biggest saving in this file for a wide wall.
	sd := segDraw{
		frontSD: frontSD, frontSec: frontSec, backSec: backSec,
		flags: flags, wallLight: int16(wallLight),
	}
	if r.gbuf {
		// The seg's front-facing wall normal (into the room). Doom winds a
		// seg so solid space is to its right, so the outward normal is the
		// right-hand perpendicular of the v1->v2 direction.
		dx, dy := x2-x1, y2-y1
		if l := math.Hypot(dx, dy); l > 0 {
			sd.nx, sd.ny = float32(dy/l), float32(-dx/l)
		}
	}
	sd.midTex, _ = r.textures.WallTexture(frontSD.MiddleTexture)
	sd.upperTex, sd.haveUpper = r.textures.WallTexture(frontSD.UpperTexture)
	sd.lowerTex, _ = r.textures.WallTexture(frontSD.LowerTexture)
	sd.floorFlat, _ = r.textures.Flat(frontSec.FloorTexture)
	sd.floorLiquid = classifyFloorLiquid(frontSec.FloorTexture)
	sd.midBright, _ = r.textures.Brightmap(frontSD.MiddleTexture, sd.midTex)
	sd.upperBright, _ = r.textures.Brightmap(frontSD.UpperTexture, sd.upperTex)
	sd.lowerBright, _ = r.textures.Brightmap(frontSD.LowerTexture, sd.lowerTex)
	sd.floorBright, _ = r.textures.Brightmap(frontSec.FloorTexture, sd.floorFlat)
	if frontSec.CeilingTexture == skyFlatName {
		sd.ceilIsSky = true
	} else {
		sd.ceilFlat, _ = r.textures.Flat(frontSec.CeilingTexture)
		sd.ceilBright, _ = r.textures.Brightmap(frontSec.CeilingTexture, sd.ceilFlat)
	}

	// 1/depth and t/depth are both linear in screen x, so each column's
	// values come from a lerp on x. This is written as a direct function of
	// the absolute column (not an accumulator seeded at the loop start) on
	// purpose: strip workers start their loop at different x, and an
	// accumulator would drift by a few ULPs between them and occasionally
	// flip a floor()/round() at a strip seam. The two extra multiplies per
	// column are nothing next to the per-pixel fill they feed.
	invD1, invD2 := 1/d1, 1/d2
	tOverD1, tOverD2 := t1*invD1, t2*invD2
	invSpan := 1.0 / (sx2 - sx1)
	dInvD := invD2 - invD1
	dTOverD := tOverD2 - tOverD1
	xBase := float64(frontSD.XOffset) + float64(seg.Offset)

	filtering := r.worldFiltering()
	for x := xStart; x <= xEnd; x++ {
		if ctx.ceilClip[x] < ctx.floorClip[x] { // else fully occluded by something nearer
			alpha := (float64(x) + 0.5 - sx1) * invSpan
			depth := 1 / (invD1 + dInvD*alpha)
			segT := (tOverD1 + dTOverD*alpha) * depth
			segU := xBase + segT
			texX := int(math.Floor(segU))
			// Perspective-correct d(texture-u)/d(screen-x), for the filtered
			// wall path's footprint. One column over, same lerp.
			var dUdx float64
			if filtering {
				aN := alpha + invSpan
				dN := 1 / (invD1 + dInvD*aN)
				dUdx = (tOverD1+dTOverD*aN)*dN - segT
			}
			r.renderColumn(x, depth, texX, segU, dUdx, &sd, cam, ctx)
		}
	}
}

func (r *Renderer) worldZToScreenY(depth, worldZ, camZ float64) int {
	return int(r.horizonY() - (worldZ-camZ)/depth*r.focal + 0.5)
}

// horizonY is the screen row the horizon (eye-level, zero pitch) sits at —
// ordinarily the exact vertical center, shifted by r.pitchShear when the
// camera is looking up or down.
func (r *Renderer) horizonY() float64 {
	return float64(r.Height)/2 + r.pitchShear
}

// renderColumn draws one screen column's contribution from a single seg:
// the wall piece(s) it defines, and the floor/ceiling flat spans the wall
// newly exposes — then narrows ctx.ceilClip/ctx.floorClip to reflect what's
// now been drawn, exactly mirroring Doom's own per-column occlusion
// tracking. ctx is the calling worker's screen strip (see stripCtx).
// flags is the seg's parent Linedef's flag bits, for texture pegging (see
// drawWallSpan). wallLight is the seg's front-sector light level with fake
// contrast already folded in (see renderSeg) — used for the wall pieces;
// flats use their own sector's unadjusted light level.
func (r *Renderer) renderColumn(x int, depth float64, texX int, segU, dUdx float64, sd *segDraw, cam *Camera, ctx *stripCtx) {
	top := ctx.ceilClip[x]
	bottom := ctx.floorClip[x]
	frontSec, backSec := sd.frontSec, sd.backSec

	ceilY := clampInt(r.worldZToScreenY(depth, float64(frontSec.CeilingHeight), cam.Z), top, bottom)
	floorY := clampInt(r.worldZToScreenY(depth, float64(frontSec.FloorHeight), cam.Z), top, bottom)

	if backSec == nil {
		// One-sided (solid) wall: the middle texture fills the entire
		// floor-to-ceiling gap and nothing behind this seg can ever be
		// seen through it — close the column completely.
		r.drawCeilingSpan(x, top, ceilY, sd, depth, cam)
		r.drawWallSpan(x, ceilY, floorY, texX, segU, dUdx, sd.midTex, sd.midBright, sd.frontSD,
			pieceMiddle, sd.flags&wad.LinedefLowerUnpegged != 0, frontSec, backSec, depth, sd.wallLight, sd.nx, sd.ny, cam)
		r.drawFlatSpan(x, floorY, bottom, sd.floorFlat, sd.floorBright, float64(frontSec.FloorHeight), frontSec.LightLevel, true, sd.floorLiquid, cam)
		ctx.ceilClip[x] = bottom
		ctx.floorClip[x] = bottom
		return
	}

	backCeilY := clampInt(r.worldZToScreenY(depth, float64(backSec.CeilingHeight), cam.Z), top, bottom)
	backFloorY := clampInt(r.worldZToScreenY(depth, float64(backSec.FloorHeight), cam.Z), top, bottom)

	// Ceiling flat down to the (possibly stepped) upper wall piece.
	r.drawCeilingSpan(x, top, ceilY, sd, depth, cam)
	if backSec.CeilingHeight < frontSec.CeilingHeight {
		r.drawWallSpan(x, ceilY, backCeilY, texX, segU, dUdx, sd.upperTex, sd.upperBright, sd.frontSD,
			pieceUpper, sd.flags&wad.LinedefUpperUnpegged != 0, frontSec, backSec, depth, sd.wallLight, sd.nx, sd.ny, cam)
		// A stepped-down ceiling with no upper texture, below a sky ceiling,
		// would otherwise leave the void backdrop showing through the step.
		// Vanilla shows sky there (the sky "wraps" over the drop); match it.
		if sd.ceilIsSky && !sd.haveUpper {
			r.drawSkySpan(x, ceilY, backCeilY)
		}
	}
	ctx.ceilClip[x] = maxInt(ceilY, backCeilY)

	// Floor flat up to the (possibly stepped) lower wall piece.
	r.drawFlatSpan(x, floorY, bottom, sd.floorFlat, sd.floorBright, float64(frontSec.FloorHeight), frontSec.LightLevel, true, sd.floorLiquid, cam)
	if backSec.FloorHeight > frontSec.FloorHeight {
		r.drawWallSpan(x, backFloorY, floorY, texX, segU, dUdx, sd.lowerTex, sd.lowerBright, sd.frontSD,
			pieceLower, sd.flags&wad.LinedefLowerUnpegged != 0, frontSec, backSec, depth, sd.wallLight, sd.nx, sd.ny, cam)
	}
	ctx.floorClip[x] = minInt(floorY, backFloorY)

	// The gap between backCeilY and backFloorY is the open "portal" into
	// the next area — deliberately left undrawn/unclipped so subsectors
	// behind it, visited later in this same front-to-back traversal, are
	// still free to draw into it.
}

// drawCeilingSpan draws a sector's ceiling, dispatching to the sky
// renderer (sky.go) instead of a normal textured flat when the ceiling is
// the special sky flat (F_SKY1) — exactly the check PrBoom's r_bsp.c makes
// (sector->ceilingpic == skyflatnum) before building a normal visplane.
func (r *Renderer) drawCeilingSpan(x, yTop, yBottom int, sd *segDraw, depth float64, cam *Camera) {
	if sd.ceilIsSky {
		r.drawSkySpan(x, yTop, yBottom)
		return
	}
	sec := sd.frontSec
	r.drawFlatSpan(x, yTop, yBottom, sd.ceilFlat, sd.ceilBright, float64(sec.CeilingHeight), sec.LightLevel, false, liquidNone, cam)
}

// wallPiece identifies which of a seg's up-to-three wall textures
// drawWallSpan is drawing — each anchors its texture differently, and two
// of the three change behavior with the linedef's (un)pegged flags. See
// drawWallSpan's doc comment.
type wallPiece int

const (
	pieceMiddle wallPiece = iota // one-sided line's only texture, floor to ceiling
	pieceUpper                   // two-sided line's upper piece, front ceiling down to back ceiling
	pieceLower                   // two-sided line's lower piece, back floor up to front floor
)

// drawWallSpan draws one wall piece's texture into screen rows [yTop,
// yBottom) of column x. Which world-Z the texture's row 0 anchors to
// (anchorZ) depends on piece and unpegged — ported directly from PrBoom's
// r_segs.c (R_StoreWallRange's rw_*texturemid setup), not invented:
//
//   - pieceMiddle (one-sided): unpegged anchors the texture's *bottom* to
//     the floor (anchorZ = floor + texture height); the default anchors
//     the top to the ceiling (anchorZ = ceiling).
//   - pieceUpper: unpegged (ML_DONTPEGTOP) anchors the top to the front
//     ceiling (anchorZ = frontCeiling) — a fixed point that doesn't move
//     as a door's back ceiling rises/falls, so the texture appears to be
//     "revealed" from the bottom as the door opens. The default anchors
//     the *bottom* to the back ceiling (anchorZ = backCeiling + texture
//     height), which — recomputed fresh every frame from the live,
//     possibly-animating backCeiling — keeps the texture's bottom edge
//     flush with a moving door's ceiling instead of stretching.
//   - pieceLower: the default anchors the top to the back floor (anchorZ
//     = backFloor, matching the piece's own top edge). Unpegged
//     (ML_DONTPEGBOTTOM) anchors to the *front ceiling* instead
//     (anchorZ = frontCeiling) — a well-known original-engine quirk, not
//     a bug in this port: lower-unpegged textures reference the far-away
//     front ceiling rather than anything on the lower piece itself.
//
// Doom's texture formats store 1 texel per map unit, so the texture row at
// screen row y is just anchorZ minus that row's own world Z. Because depth
// (this column's distance to the wall plane) is constant across a column,
// world Z is a *linear* function of screen row here — unlike drawFlatSpan,
// which has to recompute a fresh depth every row — so this reduces to
// stepping v by a constant `depth/r.focal` texels per row. That step is
// exactly what makes a texture visibly stretch as a wall gets closer
// (small depth → small step) and compress as it recedes (large depth →
// large step) instead of just showing a fixed number of texels regardless
// of distance.
func (r *Renderer) drawWallSpan(x, yTop, yBottom, texX int, segU, dUdx float64, tex, bright *assets.RGBA, sd *wad.Sidedef, piece wallPiece, unpegged bool, frontSec, backSec *wad.Sector, depth float64, light int16, nx, ny float32, cam *Camera) {
	if yBottom <= yTop || tex == nil {
		return
	}

	// texHeightUnits is tex.Height converted back to map units (identical
	// to tex.Height for a normal 1:1 WAD texture; a quarter of it for a
	// HiresTextures override at TexelsPerUnit=4) — every anchor below is
	// computed in world/map-unit space, same as frontSec/backSec's own
	// heights, so a texture's *pixel* height must never leak in directly.
	texHeightUnits := float64(tex.Height) / tex.TexelsPerUnit

	var anchorZ float64
	switch piece {
	case pieceMiddle:
		if unpegged {
			anchorZ = float64(frontSec.FloorHeight) + texHeightUnits
		} else {
			anchorZ = float64(frontSec.CeilingHeight)
		}
	case pieceUpper:
		if unpegged {
			anchorZ = float64(frontSec.CeilingHeight)
		} else {
			anchorZ = float64(backSec.CeilingHeight) + texHeightUnits
		}
	case pieceLower:
		if unpegged {
			anchorZ = float64(frontSec.CeilingHeight)
		} else {
			anchorZ = float64(backSec.FloorHeight)
		}
	}

	// texX is a map-unit coordinate (see renderSeg); scale to pixels by
	// TexelsPerUnit before wrapping by the texture's actual pixel width —
	// for a normal 1:1 texture this is a no-op, but it's what makes a
	// HiresTextures override sample its full extra resolution instead of
	// effectively showing the pattern 4x too large (or, wrapped the other
	// way, only ever showing one quarter of the image).
	tx := wrapInt(int(math.Floor(float64(texX)*tex.TexelsPerUnit)), tex.Width)

	step := depth / r.focal
	base := anchorZ - cam.Z + float64(sd.YOffset) - r.horizonY()*step

	// World Z is linear in screen row (column depth is constant) and the
	// texture row is a fixed multiple of world Z, so the sample coordinate
	// reduces to a constant add per row instead of a multiply. vt tracks
	// v*TexelsPerUnit directly.
	tpu := tex.TexelsPerUnit
	texH := tex.Height
	texW := tex.Width
	src := tex.Pix
	depth32 := float32(depth)
	vt := (base + (float64(yTop)+0.5)*step) * tpu
	vtStep := step * tpu

	// Composite-texture heights aren't always powers of two (72, 128, ...),
	// so keep a modulo fallback; the pow2 ones (the common case) get a mask.
	texHMask := texH - 1
	texHPow2 := texH&texHMask == 0

	// Depth is constant across a wall column, so the brightness multiplier is
	// too — one table lookup for the whole span, applied as an integer
	// multiply-shift per texel. yTop/yBottom are already clamped to
	// [0, Height] and x to [0, Width) by renderColumn, so the writes below
	// need no per-texel bounds check.
	//
	// In G-buffer mode the shade is skipped entirely (Pix gets raw albedo)
	// and each covered texel also stamps the seg's constant normal + sector
	// light for the GPU lighting pass.
	gb := r.gbuf
	var sm uint32
	var full bool
	var nrm, lp []byte
	var pnx, pny, plight byte
	if gb {
		nrm, lp = r.Normal, r.LightParam
		pnx = byte((nx*0.5 + 0.5) * 255)
		pny = byte((ny*0.5 + 0.5) * 255)
		plight = byte(clampInt(int(light), 0, 255))
	} else {
		sm = shadeMul(light, depth)
		full = sm >= 256
	}
	dst := r.Pix
	i := (yTop*r.Width + x) * 4
	cell := yTop*r.Width + x
	stride := r.Width * 4

	if r.texQ != tqNearest {
		// Filtered path: mip/trilinear/anisotropic. u is constant down the
		// column; the footprint is texel-axis-aligned (dudx across screen x,
		// dvdy down screen y), so its major/minor axes and tap direction are
		// resolved once here, not per texel.
		mm := r.worldMips(tex)
		uTex := segU * tpu
		dudx := math.Abs(dUdx) * tpu
		dvdy := math.Abs(vtStep)
		major, minor, mdu, mdv := dvdy, dudx, 0.0, 1.0
		if dudx >= dvdy {
			major, minor, mdu, mdv = dudx, dvdy, 1.0, 0.0
		}
		maxT := 1
		if r.texQ == tqAniso {
			maxT = r.maxAniso
		}
		bilinearOnly := r.texQ == tqBilinear
		// The footprint is constant down a wall column, so resolve the tap
		// count + mip LOD once here rather than per pixel.
		plan := planAniso(major, minor, mdu, mdv, maxT)
		var fscale float64
		if !gb {
			fscale = float64(sm) / 256
		}
		for y := yTop; y < yBottom; y++ {
			var fr, fg, fb, fa float64
			if bilinearOnly {
				fr, fg, fb, fa = mm.bilerpLevel(0, uTex, vt)
			} else {
				fr, fg, fb, fa = mm.sampleAnisoPlan(uTex, vt, plan)
			}
			vt += vtStep
			if fa >= 128 { // an opaque texture is always ~255; a composite hole fades out
				if gb {
					dst[i], dst[i+1], dst[i+2] = clampByte(fr), clampByte(fg), clampByte(fb)
					nrm[i], nrm[i+1], nrm[i+2], nrm[i+3] = pnx, pny, 128, 0
					lp[i], lp[i+1], lp[i+2], lp[i+3] = plight, 0, 0, 255
				} else if full {
					dst[i], dst[i+1], dst[i+2] = clampByte(fr), clampByte(fg), clampByte(fb)
				} else {
					dst[i], dst[i+1], dst[i+2] = clampByte(fr*fscale), clampByte(fg*fscale), clampByte(fb*fscale)
				}
				if bright != nil {
					r.applyBright(bright, int(uTex), int(vt), i, gb)
				}
				dst[i+3] = 255
				r.depth[cell] = depth32
			}
			cell += r.Width
			i += stride
		}
		return
	}

	for y := yTop; y < yBottom; y++ {
		var ty int
		if texHPow2 {
			ty = int(math.Floor(vt)) & texHMask
		} else {
			ty = wrapInt(int(math.Floor(vt)), texH)
		}
		vt += vtStep
		s := (ty*texW + tx) * 4
		if src[s+3] != 0 { // skip a transparent texel in a composite texture
			if gb {
				dst[i] = src[s]
				dst[i+1] = src[s+1]
				dst[i+2] = src[s+2]
				nrm[i], nrm[i+1], nrm[i+2], nrm[i+3] = pnx, pny, 128, 0 // nz=0, material 0 (world)
				lp[i], lp[i+1], lp[i+2], lp[i+3] = plight, 0, 0, 255
			} else if full {
				dst[i] = src[s]
				dst[i+1] = src[s+1]
				dst[i+2] = src[s+2]
			} else {
				dst[i] = byte(uint32(src[s]) * sm >> 8)
				dst[i+1] = byte(uint32(src[s+1]) * sm >> 8)
				dst[i+2] = byte(uint32(src[s+2]) * sm >> 8)
			}
			if bright != nil {
				r.applyBright(bright, tx, ty, i, gb)
			}
			dst[i+3] = 255
			r.depth[cell] = depth32
		}
		cell += r.Width
		i += stride
	}
}

// drawFlatSpan fills screen rows [yTop, yBottom) of column x with a
// floor/ceiling flat, computing each row's own world-space distance and
// sample point the way a raycaster's floor-casting pass does — the
// per-column equivalent of Doom's own per-row visplane spans, chosen
// because it fits this renderer's column-major structure far more simply
// than reintroducing horizontal span buffers would.
func (r *Renderer) drawFlatSpan(x, yTop, yBottom int, flat, bright *assets.RGBA, planeZ float64, light int16, faceUp bool, liquid uint8, cam *Camera) {
	if yBottom <= yTop || flat == nil {
		return
	}

	zf := (planeZ - cam.Z) * r.focal
	s, c := r.sinA, r.cosA
	horizon := r.horizonY()
	halfW := float64(r.Width) / 2

	// horiz = colFactor*depth, so relX/relY collapse to depth*kx / depth*ky
	// with kx,ky constant down the column — see toCameraSpace's inverse below.
	colFactor := (float64(x) + 0.5 - halfW) / r.focal
	kx := c + colFactor*s
	ky := s - colFactor*c
	tpu := flat.TexelsPerUnit
	fw, fh := flat.Width, flat.Height
	src := flat.Pix
	camX, camY := cam.X, cam.Y

	// Flats are square powers of two by format (64, or 256 for a hires
	// override), so mask instead of a per-pixel modulo — with a wrapInt
	// fallback in case a future asset path ever breaks that.
	fwMask, fhMask := fw-1, fh-1
	fwPow2, fhPow2 := fw&fwMask == 0, fh&fhMask == 0

	// Depth changes every row here (unlike a wall column), so brightness is a
	// per-row lookup: hold this light level's row, index it by distIdx.
	// G-buffer mode skips shading (Pix gets raw albedo) and stamps a
	// constant up/down normal + sector light instead.
	gb := r.gbuf
	var lrow *[maxLightZ]uint16
	var nrm, lp []byte
	var pnz, plight byte
	if gb {
		nrm, lp = r.Normal, r.LightParam
		pnz = 0 // ceiling: nz = -1
		if faceUp {
			pnz = 255 // floor: nz = +1
		}
		plight = byte(clampInt(int(light), 0, 255))
	} else {
		lrow = lightRow(light)
	}

	dst := r.Pix
	i := (yTop*r.Width + x) * 4
	cell := yTop*r.Width + x
	stride := r.Width * 4
	denom := horizon - (float64(yTop) + 0.5)

	if r.texQ != tqNearest {
		// Filtered path. Unlike a wall column the footprint changes every row
		// (depth changes every row), and it is NOT texel-axis-aligned — the
		// screen-x step maps to (s,-c) in flat space and the screen-y step to
		// the view direction (kx,ky). Near the horizon the along-view axis
		// blows up, which is exactly the floor-shimmer anisotropic sampling
		// removes.
		mm := r.worldMips(flat)
		zfAbs := math.Abs(zf)
		focalInv := 1 / r.focal
		hk := math.Hypot(kx, ky)
		maxT := 1
		if r.texQ == tqAniso {
			maxT = r.maxAniso
		}
		bilinearOnly := r.texQ == tqBilinear
		for y := yTop; y < yBottom; y++ {
			dn := denom
			denom--
			depth := zf / dn
			if dn == 0 || depth <= 0 {
				cell += r.Width
				i += stride
				continue
			}
			ddd := zfAbs / (dn * dn) // |d(depth)/d(screen-y)|
			fu := (camX + depth*kx) * tpu
			fv := (camY + depth*ky) * tpu
			var wgx, wgy, wcrest float64
			if liquid == liquidWater {
				wgx, wgy, wcrest = waterWave(camX+depth*kx, camY+depth*ky, r.animTime)
				fu += wgx * 1.5 // refraction: the bottom shimmers through moving water
				fv += wgy * 1.5
			}
			absVx := depth * tpu * focalInv // |footprint along screen-x|, texels
			absVy := ddd * tpu * hk         // |footprint along screen-y|, texels
			major, minor, mdu, mdv := absVx, absVy, s, -c
			if absVy > absVx {
				major, minor, mdu, mdv = absVy, absVx, kx/hk, ky/hk
			}
			var fr, fg, fb float64
			if bilinearOnly {
				fr, fg, fb, _ = mm.bilerpLevel(0, fu, fv)
			} else {
				fr, fg, fb, _ = mm.aniso(fu, fv, major, minor, mdu, mdv, maxT)
			}
			if gb {
				dst[i], dst[i+1], dst[i+2] = clampByte(fr), clampByte(fg), clampByte(fb)
				nrm[i], nrm[i+1], nrm[i+2], nrm[i+3] = 128, 128, pnz, 0
				lp[i], lp[i+1], lp[i+2], lp[i+3] = plight, 0, 0, 255
			} else if sm := uint32(lrow[distIdx(depth)]); sm >= 256 {
				dst[i], dst[i+1], dst[i+2] = clampByte(fr), clampByte(fg), clampByte(fb)
			} else {
				f := float64(sm) / 256
				dst[i], dst[i+1], dst[i+2] = clampByte(fr*f), clampByte(fg*f), clampByte(fb*f)
			}
			if bright != nil {
				r.applyBright(bright, int(math.Floor(fu)), int(math.Floor(fv)), i, gb)
			}
			if liquid != liquidNone {
				r.shadeLiquid(liquid, x, y, i, gb, camX+depth*kx, camY+depth*ky, wgx, wgy, wcrest, depth)
			}
			dst[i+3] = 255
			r.depth[cell] = float32(depth)
			cell += r.Width
			i += stride
		}
		return
	}

	for y := yTop; y < yBottom; y++ {
		dn := denom
		denom--
		depth := zf / dn
		if dn == 0 || depth <= 0 {
			cell += r.Width
			i += stride
			continue
		}

		// World point this pixel's ray hits the floor/ceiling plane at,
		// scaled straight into flat-texel space.
		var fx, fy int
		if fwPow2 {
			fx = int(math.Floor((camX+depth*kx)*tpu)) & fwMask
		} else {
			fx = wrapInt(int(math.Floor((camX+depth*kx)*tpu)), fw)
		}
		if fhPow2 {
			fy = int(math.Floor((camY+depth*ky)*tpu)) & fhMask
		} else {
			fy = wrapInt(int(math.Floor((camY+depth*ky)*tpu)), fh)
		}

		var wgx, wgy, wcrest float64
		if liquid == liquidWater {
			wgx, wgy, wcrest = waterWave(camX+depth*kx, camY+depth*ky, r.animTime)
			fx = wrapInt(fx+int(math.Round(wgx*1.5)), fw) // refraction shimmer
			fy = wrapInt(fy+int(math.Round(wgy*1.5)), fh)
		}

		sp := (fy*fw + fx) * 4
		if gb {
			dst[i] = src[sp]
			dst[i+1] = src[sp+1]
			dst[i+2] = src[sp+2]
			nrm[i], nrm[i+1], nrm[i+2], nrm[i+3] = 128, 128, pnz, 0 // material 0 (world)
			lp[i], lp[i+1], lp[i+2], lp[i+3] = plight, 0, 0, 255
		} else if sm := uint32(lrow[distIdx(depth)]); sm >= 256 {
			dst[i] = src[sp]
			dst[i+1] = src[sp+1]
			dst[i+2] = src[sp+2]
		} else {
			dst[i] = byte(uint32(src[sp]) * sm >> 8)
			dst[i+1] = byte(uint32(src[sp+1]) * sm >> 8)
			dst[i+2] = byte(uint32(src[sp+2]) * sm >> 8)
		}
		if bright != nil {
			r.applyBright(bright, fx, fy, i, gb)
		}
		if liquid != liquidNone {
			r.shadeLiquid(liquid, x, y, i, gb, camX+depth*kx, camY+depth*ky, wgx, wgy, wcrest, depth)
		}
		dst[i+3] = 255
		r.depth[cell] = float32(depth)
		cell += r.Width
		i += stride
	}
}

// brightBoost is how hard a fully-masked brightmap texel is pushed above
// its sector-shaded value in the vanilla path (hue preserved, then
// clamped): a gentle lift so a lit panel reads as self-illuminated without
// blowing out. (The enhanced and hardware paths use the tighter
// max(sectorFade, mask) model in light.frag / world.frag.)
const brightBoost = 1.0

// applyBright post-processes one just-written wall/flat pixel per its
// brightmap mask (bright, already resized to the base texture's pixel dims;
// mtx/mty are the base texel coords; i is the r.Pix byte offset). In
// G-buffer mode it stamps the mask value into LightParam.g for light.frag's
// emissive term; otherwise it scales the shaded pixel up toward full
// brightness. A near-zero mask value is a no-op.
func (r *Renderer) applyBright(bright *assets.RGBA, mtx, mty, i int, gb bool) {
	if mtx < 0 || mtx >= bright.Width {
		mtx = wrapInt(mtx, bright.Width)
	}
	if mty < 0 || mty >= bright.Height {
		mty = wrapInt(mty, bright.Height)
	}
	b := bright.Pix[(mty*bright.Width+mtx)*4] // luma, replicated across channels
	if b < 6 {
		return
	}
	if gb {
		r.LightParam[i+1] = b
		return
	}
	f := 1 + float64(b)/255*brightBoost
	dst := r.Pix
	dst[i] = clampByte(float64(dst[i]) * f)
	dst[i+1] = clampByte(float64(dst[i+1]) * f)
	dst[i+2] = clampByte(float64(dst[i+2]) * f)
}

// waterWave is a small multi-octave sum-of-sines height field over world XY
// at time t — the same constants as world.frag's waterWave so the software
// and hardware water ripple alike. Three fast short-wavelength ripples plus
// one slow large swell (so a big pool isn't uniformly busy). Returns a
// slope vector (gx, gy) for distorting the reflection and a 0..1 crest
// factor for the glint.
func waterWave(wx, wy, t float64) (gx, gy, crest float64) {
	px, py := wx*0.030, wy*0.030
	a := math.Sin(px*1.00 + py*0.60 + t*1.6)
	b := math.Sin(px*-0.70 + py*1.30 + t*2.1)
	c := math.Sin(px*1.70 + py*-0.40 + t*1.1)
	qx, qy := wx*0.008, wy*0.008
	e := math.Sin(qx*1.00 + qy*0.70 + t*0.5) // slow swell
	hgt := (a+0.6*b+0.4*c)/2.0 + 0.35*e
	gx, gy = a-c+0.4*e, b-0.5*c+0.3*e
	crest = hgt*0.4 + 0.5
	if crest < 0 {
		crest = 0
	} else if crest > 1 {
		crest = 1
	}
	return gx, gy, crest
}

// shadeLiquid post-processes one just-written liquid-floor pixel (column x,
// row y, byte offset i in r.Pix; gb == G-buffer mode; wx,wy = the world
// point the pixel's ray hit the floor at, for the animated ripple). Water
// gets a rippling mirrored-sky reflection; lava a warm self-lit glow (and,
// in G-buffer mode, the emissive material key so the enhanced lighting pass
// passes it through unlit); nukage a green emissive tint. Matches
// world.frag's KIND_LIQUID_* branches so the two renderers agree.
func (r *Renderer) shadeLiquid(kind uint8, x, y, i int, gb bool, wx, wy, gx, gy, crest, dist float64) {
	dst := r.Pix
	switch kind {
	case liquidWater:
		r.blendWater(x, y, i, gx, gy, crest, dist)
		if gb {
			// Water material key (~0.16): light.frag damps the dynamic-light
			// wash so firing a weapon over water doesn't blow the reflection
			// out — a flash shows as a faint moving glint instead.
			r.Normal[i+3] = matWater
		}
	case liquidLava:
		fr, fg, fb := float64(dst[i]), float64(dst[i+1]), float64(dst[i+2])
		// Warm emissive: keep most of the texel, add a strong orange lift so
		// lava reads hot even in an unlit corner (and blooms in enhanced
		// mode). A slow per-patch pulse (world position + time) makes the
		// glow breathe — matches world.frag's KIND_LIQUID_LAVA.
		pulse := 0.92 + 0.13*math.Sin(wx*0.018+wy*0.021+r.animTime*2.3)
		dst[i] = clampByte(fr*0.55 + 150*pulse)
		dst[i+1] = clampByte(fg*0.55 + 55*pulse)
		dst[i+2] = clampByte(fb*0.55 + 14*pulse)
		if gb {
			r.Normal[i+3] = 255 // material key: emissive -> not shaded by light.frag
		}
	case liquidNukage:
		fr, fg, fb := float64(dst[i]), float64(dst[i+1]), float64(dst[i+2])
		dst[i] = clampByte(fr * 0.80)
		dst[i+1] = clampByte(fg*0.88 + 34)
		dst[i+2] = clampByte(fb*0.85 + 10)
	}
}

// skyReflectTexel samples the current sky bitmap as if seen in a mirror at
// the horizon: for a floor pixel on screen row y (below the horizon), the
// reflected ray points to elevation -(y-horizon), so the sky's vertical
// mapping (see drawSkySpan) is evaluated at that mirrored row. Returns the
// sky texel and ok, or ok=false when there is no sky this frame.
func (r *Renderer) skyReflectTexel(x, y int) (rr, gg, bb byte, ok bool) {
	tex := r.skyTex
	if tex == nil {
		return 0, 0, 0, false
	}
	if x < 0 {
		x = 0
	} else if x >= r.Width {
		x = r.Width - 1
	}
	tx := r.skyTx[x]
	vStep := r.skyScale * (skyReferenceScreenHeight / float64(r.Height))
	v := skyTextureMidRow*r.skyScale - (float64(y)+0.5-r.horizonY())*vStep
	ty := wrapInt(int(math.Floor(v)), tex.Height)
	s := (ty*tex.Width + tx) * 4
	p := tex.Pix
	return p[s], p[s+1], p[s+2], true
}

// blendWater mixes a rippling mirrored-sky reflection into an
// already-shaded water floor pixel (column x, row y, byte offset i in
// r.Pix; wx,wy = the world point it sits at). The reflection sample is
// nudged by the wave slope (so the mirrored sky shimmers), fresnel grows
// sharply toward the horizon, and wave crests near grazing get a white
// sun-glint. Cool-biased so it still reads as water under a grey/absent
// sky. Software renderer only — the hardware path does the equivalent in
// world.frag.
// blendWater shades one water pixel. gx,gy,crest come from waterWave (also
// used upstream to refract the bottom texel already in dst); dist is the
// pixel's camera-forward distance in map units, a "how much water am I
// looking across" cue on top of the view angle.
func (r *Renderer) blendWater(x, y, i int, gx, gy, crest, dist float64) {
	dst := r.Pix
	// The FWATER texel already shaded/albedo'd into dst is "the bottom, seen
	// THROUGH the water" — keep it as the dominant term so the surface reads
	// as translucent, not painted-on.
	fr, fg, fb := float64(dst[i]), float64(dst[i+1]), float64(dst[i+2])

	horizon := r.horizonY()
	graz := 0.0
	if d := float64(r.Height) - horizon; d > 1 {
		graz = 1 - (float64(y)+0.5-horizon)/d
	}
	if graz < 0 {
		graz = 0
	} else if graz > 1 {
		graz = 1
	}

	// Depth cue: view angle PLUS actual view distance — a wide lake reads
	// deeper toward its far side, a puddle underfoot stays shallow. The
	// further the ray travels through water the more light is absorbed (red
	// first, blue least), so the bottom fades to dark blue-green. Wave
	// crests focus a little light back (caustics).
	distFrac := dist / 620
	if distFrac > 1 {
		distFrac = 1
	}
	depth := 0.14 + 0.55*graz + 0.34*distFrac
	caustic := 0.86 + 0.5*(crest-0.5)
	ar := math.Exp(-2.3*depth) * caustic
	ag := math.Exp(-1.15*depth) * caustic
	ab := math.Exp(-0.5*depth) * caustic
	fr = fr*ar + 5*depth  // + a faint deep-water colour so a black bottom
	fg = fg*ag + 15*depth //   still reads as water
	fb = fb*ab + 22*depth

	// Reflection: a rippled, desaturated hint of sky — only really present
	// at a glancing angle (sharp fresnel), never a mirror.
	fres := 0.02 + 0.18*math.Pow(graz, 5)
	off := 1.5 + 3.0*graz
	sx := x + int(math.Round(gx*off))
	sy := y + int(math.Round(gy*off*0.5))
	if sr, sg, sb, ok := r.skyReflectTexel(sx, sy); ok {
		lum := 0.30*float64(sr) + 0.59*float64(sg) + 0.11*float64(sb)
		fr = fr*(1-fres) + (float64(sr)*0.7+lum*0.3)*fres
		fg = fg*(1-fres) + (float64(sg)*0.7+lum*0.3)*fres
		fb = fb*(1-fres) + (float64(sb)*0.7+lum*0.3)*fres
	}

	// Sun glint: one tight bright spot near the horizon whose column drifts
	// slowly (time + camera yaw) and is shattered into glitter by the wave
	// crests — a coherent highlight, not a uniform sheen.
	yaw := math.Atan2(r.sinA, r.cosA)
	sunX := float64(r.Width)*0.5 + math.Sin(r.animTime*0.06-yaw*1.5)*float64(r.Width)*0.32
	ddx := (float64(x) - sunX) / (float64(r.Width) * 0.05)
	ddy := (float64(y) - horizon - 6) / 7.0
	sun := math.Exp(-(ddx*ddx+ddy*ddy)) * math.Pow(crest, 5) * (0.25 + 0.75*graz) * 200
	dst[i] = clampByte(fr + sun)
	dst[i+1] = clampByte(fg + sun*0.97)
	dst[i+2] = clampByte(fb + sun*0.9)
}

// CompositeOverlay alpha-composites the 2D overlay buffer (full render
// resolution, drawn this frame by DrawWeapon/DrawStatusBar/DrawHUD with
// their own HUD-space scaling) onto r.Pix. Overlay pixels are
// opaque-or-transparent (vanilla graphics have no partial alpha), so this
// is a masked copy, partitioned into the same contiguous byte ranges the
// frame clears use. Call once per frame, after the overlay draws and
// before handing r.Pix to the GPU backend (vanilla path only — the
// enhanced path uploads the overlay separately, uncomposited).
func (r *Renderer) CompositeOverlay() {
	nw := len(r.strips)
	if nw < 1 {
		nw = 1
	}
	n := len(r.overlay)
	parallelFor(nw, func(k int) {
		lo := k * n / nw / 4 * 4
		hi := (k + 1) * n / nw / 4 * 4
		for i := lo; i+3 < hi; i += 4 {
			if r.overlay[i+3] == 0 {
				continue
			}
			r.Pix[i], r.Pix[i+1], r.Pix[i+2], r.Pix[i+3] = r.overlay[i], r.overlay[i+1], r.overlay[i+2], 255
		}
	})
}

// hudRect maps a rectangle given in HUD authoring space (320x200, top-left
// at logical x,y with logical width/height lw,lh) to render-space pixel
// bounds. weaponSpace picks the weapon transform (hudFit only) over the
// status-bar transform (hudFit * the user's hudScale).
func (r *Renderer) hudRect(x, y, lw, lh float64, weaponSpace bool) (px0, py0, pw, ph int) {
	s, ox, oy := r.hudScale, r.hudOffX, r.hudOffY
	if weaponSpace {
		s, ox, oy = r.weaponScale, r.weaponOffX, r.weaponOffY
	}
	px0 = ox + int(math.Round(x*s))
	py0 = oy + int(math.Round(y*s))
	pw = int(math.Round(lw * s))
	ph = int(math.Round(lh * s))
	return
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func wrapInt(v, m int) int {
	v %= m
	if v < 0 {
		v += m
	}
	return v
}
