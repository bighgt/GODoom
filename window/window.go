// Package window owns the OS window and raw input polling. It is the
// engine's one dependency on GLFW — every other package (including the
// renderer's public surface) talks to this package's small API, not to
// glfw directly, so a future non-GLFW backend only has to be reimplemented
// here.
package window

import (
	"fmt"

	"github.com/go-gl/glfw/v3.3/glfw"
)

// Window wraps a single GLFW window configured for Vulkan rendering (GLFW
// itself is told not to create a client API context — Vulkan owns the GPU).
type Window struct {
	handle *glfw.Window
	title  string

	mouseLook          bool
	lastMouseX         float64
	lastMouseY         float64
	mousePositionKnown bool
}

// New creates and shows a window sized width x height, titled title. It must
// be called from the main OS thread (see cmd/engine/main.go's
// runtime.LockOSThread in init).
func New(width, height int, title string) (*Window, error) {
	if err := glfw.Init(); err != nil {
		return nil, fmt.Errorf("window: glfw init: %w", err)
	}

	glfw.WindowHint(glfw.ClientAPI, glfw.NoAPI)
	glfw.WindowHint(glfw.Resizable, glfw.True)

	handle, err := glfw.CreateWindow(width, height, title, nil, nil)
	if err != nil {
		glfw.Terminate()
		return nil, fmt.Errorf("window: create window: %w", err)
	}

	return &Window{handle: handle, title: title}, nil
}

// Handle exposes the underlying *glfw.Window for the one place that
// genuinely needs it: Vulkan surface creation, which is inherently tied to
// GLFW's C window handle (render/vulkan is the only caller of this).
func (w *Window) Handle() *glfw.Window {
	return w.handle
}

// ShouldClose reports whether the window should close — the user clicked
// close/Alt+F4, or the engine itself called RequestClose.
func (w *Window) ShouldClose() bool {
	return w.handle.ShouldClose()
}

// RequestClose marks the window to close on the next frame.
func (w *Window) RequestClose() {
	w.handle.SetShouldClose(true)
}

// PollEvents processes pending OS input/window events. Call once per frame.
func (w *Window) PollEvents() {
	glfw.PollEvents()
}

// EscapePressed reports whether Escape is currently held down — used to
// quit the demo cleanly.
func (w *Window) EscapePressed() bool {
	return w.handle.GetKey(glfw.KeyEscape) == glfw.Press
}

// The following key queries are Phase 2's placeholder input scheme (WASD to
// move, Left/Right arrows to turn) — a real input system with configurable
// bindings is Phase 3 work; see game_design.txt.
func (w *Window) ForwardPressed() bool     { return w.handle.GetKey(glfw.KeyW) == glfw.Press }
func (w *Window) BackPressed() bool        { return w.handle.GetKey(glfw.KeyS) == glfw.Press }
func (w *Window) StrafeLeftPressed() bool  { return w.handle.GetKey(glfw.KeyA) == glfw.Press }
func (w *Window) StrafeRightPressed() bool { return w.handle.GetKey(glfw.KeyD) == glfw.Press }
func (w *Window) TurnLeftPressed() bool    { return w.handle.GetKey(glfw.KeyLeft) == glfw.Press }
func (w *Window) TurnRightPressed() bool   { return w.handle.GetKey(glfw.KeyRight) == glfw.Press }

// UsePressed reports whether Space — Doom's classic "use" key, opening
// doors and activating switches — is currently held.
func (w *Window) UsePressed() bool { return w.handle.GetKey(glfw.KeySpace) == glfw.Press }

// WeaponKeyPressed reports whether the number key for weapon slot n (1-6)
// is currently held.
func (w *Window) WeaponKeyPressed(n int) bool {
	if n < 1 || n > 6 {
		return false
	}
	return w.handle.GetKey(glfw.Key0+glfw.Key(n)) == glfw.Press
}

// FirePressed reports whether the left mouse button — Doom's fire button —
// is currently held.
func (w *Window) FirePressed() bool {
	return w.handle.GetMouseButton(glfw.MouseButtonLeft) == glfw.Press
}

// EnableMouseLook hides the OS cursor and confines it to the window,
// switching GLFW into its "disabled cursor" mode: GetCursorPos then reports
// an unbounded virtual position that keeps moving with the mouse instead of
// clamping at the screen edge, which is what MouseDelta needs to give
// smooth, unbounded look input. It also turns on raw (OS-acceleration-free,
// un-scaled) motion when the platform supports it, for the same 1:1 feel
// most first-person games use.
func (w *Window) EnableMouseLook() {
	w.handle.SetInputMode(glfw.CursorMode, glfw.CursorDisabled)
	if glfw.RawMouseMotionSupported() {
		w.handle.SetInputMode(glfw.RawMouseMotion, glfw.True)
	}
	w.mouseLook = true
	w.mousePositionKnown = false
}

// MouseDelta returns how far the mouse has moved since the last call to
// MouseDelta (in pixels; +X right, +Y down, matching GLFW's window
// coordinate convention). Returns (0, 0) until EnableMouseLook has been
// called, and on the first call afterward (nothing to diff against yet).
func (w *Window) MouseDelta() (dx, dy float64) {
	if !w.mouseLook {
		return 0, 0
	}
	x, y := w.handle.GetCursorPos()
	if !w.mousePositionKnown {
		w.lastMouseX, w.lastMouseY = x, y
		w.mousePositionKnown = true
		return 0, 0
	}
	dx, dy = x-w.lastMouseX, y-w.lastMouseY
	w.lastMouseX, w.lastMouseY = x, y
	return dx, dy
}

// FramebufferSize returns the drawable size in pixels, which can differ from
// the window size on HiDPI displays. The renderer sizes its swapchain to this.
func (w *Window) FramebufferSize() (int, int) {
	return w.handle.GetFramebufferSize()
}

// RequiredInstanceExtensions returns the Vulkan instance extensions GLFW
// needs enabled in order to create a surface for this window.
func (w *Window) RequiredInstanceExtensions() []string {
	return w.handle.GetRequiredInstanceExtensions()
}

// Destroy tears down the window and terminates GLFW. Call exactly once,
// after the renderer has released every resource tied to this window.
func (w *Window) Destroy() {
	w.handle.Destroy()
	glfw.Terminate()
}
