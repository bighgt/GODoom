// Package render defines the backend-agnostic contract every GPU renderer
// must satisfy. The engine core (package engine) only ever talks to the
// Renderer interface, never to a concrete backend — this is the
// interface-driven boundary that keeps game logic decoupled from Vulkan.
package render

import "twopointfive/render/worldgeo"

// Renderer is implemented by every rendering backend. Phase 2 ships exactly
// one implementation, render/vulkan.Renderer, but the interface exists so a
// future backend (or a headless/null renderer for tests) can be substituted
// without touching engine code.
type Renderer interface {
	// DrawFrame uploads pix (an RGBA8, row-major frame from package raster,
	// sized to the render resolution the backend was created with) to the
	// GPU and presents it, letterboxed to the window's size. This is the
	// "vanilla" path — the CPU has already baked lighting into pix.
	DrawFrame(pix []byte) error
	// DrawFrameLit is the "enhanced" path (config lightingMode "enhanced"):
	// the CPU hands over a G-buffer (unlit albedo + normals + per-pixel
	// sector light + camera-space depth), the un-composited 2D overlay, and
	// the frame's dynamic lights, and the backend does deferred lighting,
	// bloom, and tonemapping on the GPU. A backend built without the
	// enhanced resources returns an error here (and vice-versa for
	// DrawFrame on an enhanced-only backend).
	DrawFrameLit(f *Frame) error
	// DrawWorld is the "hardware" path (config renderer "hardware"): the CPU
	// hands over the triangle list worldgeo.Builder collected from the BSP
	// walk and the camera, and the GPU rasterizes, textures, depth-tests and
	// shades it — the software rasterizer's per-pixel work moved onto the
	// GPU. w/h is the render-target size; overlay is the un-composited 2D
	// HUD/weapon layer (RGBA8, straight alpha) to blend on top; lights is
	// this frame's dynamic emitters. A backend not built for it returns an
	// error (like DrawFrameLit on a vanilla backend).
	DrawWorld(g *worldgeo.Geometry, cam worldgeo.Camera, w, h int, overlay []byte, lights []Light) error
	// Destroy releases every GPU resource the renderer owns. It blocks until
	// the GPU has finished all outstanding work first, so callers can invoke
	// it directly on shutdown.
	Destroy()
}

// Light is one dynamic point light for a single frame, in world space and
// map units. Colour components are 0..1 (linear); Intensity scales the
// whole contribution; Radius is the distance at which it falls to zero
// (smooth (1 - d/Radius)^2 attenuation, no light beyond it).
type Light struct {
	X, Y, Z   float32
	Radius    float32
	R, G, B   float32
	Intensity float32
}

// Frame is one frame's worth of G-buffer plus the state the GPU lighting
// pass needs to reconstruct world position from depth. All the image
// planes are row-major, W*H, sized to the render resolution; the RGBA8
// ones are 4 bytes/pixel. It carries no raster-package types on purpose —
// package render must not import package raster (see game_design.txt §6).
type Frame struct {
	W, H int

	// Albedo is the unlit, texture-only colour (RGBA8). Normal packs the
	// view-independent surface normal into rgb (*0.5+0.5) and a material
	// key into a: 0 = lit world surface, ~0.5 = sprite (wrap-lambert +
	// fixed ambient), >=0.75 = emissive (passed through unlit — sky, muzzle
	// flash, explosions). LightParam.r is the pixel's sector light level
	// 0..255 (already fake-contrast-adjusted for walls). Overlay is the
	// raw, un-composited 2D HUD/weapon layer (RGBA8, straight-alpha) that
	// the composite pass blends on top after tonemapping so it stays crisp.
	Albedo     []byte
	Normal     []byte
	LightParam []byte
	Overlay    []byte

	// Depth is camera-forward distance in map units for the surface drawn
	// at each pixel (+Inf where nothing was drawn).
	Depth []float32

	// OverlayY/OverlayH bound the rows of Overlay that changed this frame
	// (this frame's HUD footprint unioned with last frame's). The backend
	// uploads only these rows of the overlay plane; OverlayH == 0 means the
	// overlay is unchanged since the last frame and is not re-uploaded. The
	// first upload to each in-flight slot is promoted to the full plane
	// regardless, so the untouched rows start defined.
	OverlayY, OverlayH int

	// Camera reconstruction — the exact values raster.Renderer used this
	// frame (see raster.Renderer.toCameraSpace / horizonY). Camera pitch is
	// already folded into HorizonY, so the backend needs nothing else for it.
	CamX, CamY, CamZ float32
	SinA, CosA       float32
	Focal            float32
	HorizonY         float32
	ExtraLight       float32

	// LightScale multiplies the per-pixel sector light level in the base
	// ambient term (config lightScale); Exposure is a final multiply on the
	// HDR colour before the tonemap (config exposure). Both default to 1.
	// ShadowStrength (0..1, config shadows -> 0 or 1) blends in the
	// screen-space occlusion march; ShadowLights is how many of the LEADING
	// Lights entries actually get that march — the engine puts the few
	// gameplay-critical dynamic lights (muzzle flash, in-flight shots)
	// first, so a normal frame with none firing marches nothing. The
	// per-pixel-per-light-per-step cost of the march is why the static map
	// emitters are excluded from it.
	LightScale     float32
	Exposure       float32
	ShadowStrength float32
	ShadowLights   int

	// Distance fog (config fogColor / fogDensity). FogRGB is the fog colour
	// 0..1; FogDensity is the exponential strength (fogFactor =
	// 1-exp(-density*cameraDistance)). FogDensity == 0 disables it.
	FogR, FogG, FogB float32
	FogDensity       float32

	// AnimTime is a free-running wall-clock seconds value for time-animated
	// effects in the lighting shader (the water ripple / reflected-light
	// streaks). Presentation only.
	AnimTime float32

	// Lights is this frame's lights: the leading ShadowLights-worth are the
	// gameplay-critical dynamic emitters (muzzle flash, in-flight shots —
	// the shadow-casters), followed by the map's static emitters, all
	// clamped to the backend maximum and roughly nearest-first.
	Lights []Light

	// Spot is the player's head-mounted flashlight: a single forward-facing
	// cone light, kept separate from Lights (which are isotropic point
	// lights with no notion of direction or cone angle) rather than growing
	// every entry in that array for the one caller that needs a beam.
	// SpotIntensity <= 0 means off — every other Spot field is then unread.
	SpotX, SpotY, SpotZ    float32
	SpotDX, SpotDY, SpotDZ float32 // normalized direction the cone points
	SpotR, SpotG, SpotB    float32
	SpotIntensity          float32
	SpotRadius             float32
	// SpotCosOuter/SpotCosInner are cos(halfAngle) of the cone's outer edge
	// (spill falls to zero here) and inner hotspot (full bright inside
	// here); Inner > Outer, and the shader smoothsteps between them.
	SpotCosOuter, SpotCosInner float32
}

// MaxLights is the hard cap on Frame.Lights the enhanced backend honours
// (the lighting shader's fixed-size UBO array). The engine sorts by
// distance and truncates to this.
const MaxLights = 64
