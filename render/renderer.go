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
	// DrawWorld hands over the triangle list worldgeo.Builder collected from
	// the BSP walk and the camera, and the GPU rasterizes, textures,
	// depth-tests and shades it. w/h is the render-target size; overlay is
	// the un-composited 2D HUD/weapon layer (RGBA8, straight alpha) to blend
	// on top; overlayY0/overlayY1 is the row range of overlay that changed
	// since the previous call (raster.Renderer.OverlayDirty) — a backend may
	// use it to avoid re-uploading the whole plane every frame when most of
	// it (typically all but a HUD/weapon strip) is unchanged; lights is this
	// frame's dynamic emitters.
	DrawWorld(g *worldgeo.Geometry, cam worldgeo.Camera, w, h int, overlay []byte, overlayY0, overlayY1 int, lights []Light) error
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

