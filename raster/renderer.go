// Package raster draws TwoPointFive's 2D overlay plane — the weapon
// viewmodel, status bar, crosshair, and debug HUD text — into an RGBA8
// buffer every frame. The 3D world itself is the Vulkan hardware renderer's
// job (render/vulkan, driven by render/worldgeo's BSP-to-triangle-list
// build): Renderer.Overlay() hands this package's buffer to
// render.Renderer.DrawWorld, which blends it on top after shading the
// world. See game_design.txt section 7.
package raster

import (
	"math"

	"twopointfive/assets"
)

// hudLogicalW/hudLogicalH is the coordinate space the 2D overlay (weapon
// viewmodel, status bar, debug HUD) is authored in — vanilla Doom's
// 320x200, the space every ST_* position constant is defined in. Overlay
// drawing scales from here to the render resolution (uniform, horizontally
// centered, bottom-anchored) so the HUD keeps its proportions at any
// resolution, while a hi-res sprite override (assets, TexelsPerUnit=4)
// still contributes its extra detail. SetHUDScale applies a user multiplier
// on top of that fit (config.json's hudScale).
const (
	hudLogicalW = 320
	hudLogicalH = 200
)

// Renderer owns the 2D overlay plane (weapon/HUD/crosshair/status bar) and
// the decoded texture cache every draw call resolves sprites/fonts through.
type Renderer struct {
	Width, Height int

	textures *assets.Textures

	// overlay is a full-resolution (Width x Height) RGBA buffer the 2D HUD
	// (weapon, status bar, debug text) is drawn into each frame — full-res,
	// not 320x200, so a hi-res sprite override renders at its real detail.
	// render.Renderer.DrawWorld blends it over the GPU-rendered world.
	overlay []byte

	// overlay dirty-rect. The HUD only ever touches a fraction of the plane
	// (a bottom strip for the status bar, a centred block for the weapon, a
	// few lines top-left for the debug text). [ovY0,ovY1) is the row span
	// this frame's overlay draws touched (markOverlay); ovPrevY0/1 is last
	// frame's, which BeginOverlayFrame clears at the start of the next one
	// so a moved or vanished element leaves no stale pixels.
	ovY0, ovY1         int
	ovPrevY0, ovPrevY1 int

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
}

// New creates a renderer targeting a width x height overlay plane,
// resolving sprites/fonts through textures.
func New(textures *assets.Textures, width, height int) *Renderer {
	if width < 1 {
		width = InternalWidth
	}
	if height < 1 {
		height = InternalHeight
	}
	r := &Renderer{
		Width: width, Height: height,
		textures: textures,
		overlay:  make([]byte, width*height*4),
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

// InternalWidth and InternalHeight are the fallback overlay-plane size when
// New is given a non-positive width/height — vanilla Doom's own 320x200
// framebuffer.
const (
	InternalWidth  = 320
	InternalHeight = 200
)

// BeginOverlayFrame runs the 2D overlay plane's per-frame lifecycle — clear
// last frame's dirty rows and roll the dirty rect forward. Call once per
// frame before the overlay draws (DrawWeapon / DrawHUD / DrawCrosshair /
// DrawStatusBar); render.Renderer.DrawWorld reads the result via Overlay().
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
// resolution, straight-alpha RGBA8) for render.Renderer.DrawWorld to blend
// over the GPU-rendered world. Valid until the next frame's overlay draws;
// not a copy.
func (r *Renderer) Overlay() []byte { return r.overlay }

// markOverlay records that overlay rows [y0, y1) were written this frame,
// so BeginOverlayFrame's next clear can be limited to the touched span.
// Callers pass their destination pixel bounds; the span is clamped and only
// ever grows within a frame.
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
