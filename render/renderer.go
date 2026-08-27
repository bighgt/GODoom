// Package render defines the backend-agnostic contract every GPU renderer
// must satisfy. The engine core (package engine) only ever talks to the
// Renderer interface, never to a concrete backend — this is the
// interface-driven boundary that keeps game logic decoupled from Vulkan.
package render

// Renderer is implemented by every rendering backend. Phase 2 ships exactly
// one implementation, render/vulkan.Renderer, but the interface exists so a
// future backend (or a headless/null renderer for tests) can be substituted
// without touching engine code.
type Renderer interface {
	// DrawFrame uploads pix (an RGBA8, row-major frame from package raster,
	// raster.InternalWidth*raster.InternalHeight*4 bytes) to the GPU and
	// presents it, upscaled to the window's size.
	DrawFrame(pix []byte) error
	// WaitIdle blocks until the GPU has finished all outstanding work. Call
	// before Destroy, and before any operation that touches resources a
	// frame currently in flight might still be using.
	WaitIdle()
	// Destroy releases every GPU resource the renderer owns.
	Destroy()
}
