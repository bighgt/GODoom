// Package window owns the OS window and raw input polling. It is the
// engine's one dependency on GLFW — every other package (including the
// renderer's public surface) talks to this package's small API, not to
// glfw directly, so a future non-GLFW backend only has to be reimplemented
// here.
package window

import (
	"fmt"
	"strings"

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

	// typed accumulates characters from GLFW's character callback (fired
	// during PollEvents) until TakeTyped drains them — the raw material the
	// engine's classic cheat-code matcher scans. Single-threaded: the
	// callback and TakeTyped both run on the main thread.
	typed []rune
}

// newWindow wraps a freshly created GLFW handle and installs the input
// callbacks the polling API needs — currently just the character callback
// that feeds TakeTyped (used for the classic cheat-code sequences).
func newWindow(handle *glfw.Window, title string) *Window {
	w := &Window{handle: handle, title: title}
	handle.SetCharCallback(func(_ *glfw.Window, r rune) {
		if len(w.typed) < 128 { // a cap so a key held at a broken repeat rate can't grow this unboundedly
			w.typed = append(w.typed, r)
		}
	})
	return w
}

// New creates and shows a window titled title. It must be called from the
// main OS thread (see cmd/engine/main.go's runtime.LockOSThread in init).
//
// When fullscreen is true the window covers the primary monitor at its
// current video mode as a borderless, undecorated window positioned at the
// monitor origin (windowed fullscreen — alt-tab friendly, no mode switch,
// and the compositor still hands it an unthrottled present path); width and
// height are ignored. Otherwise it's a normal resizable window of the given
// size.
func New(width, height int, title string, fullscreen bool) (*Window, error) {
	if err := glfw.Init(); err != nil {
		return nil, fmt.Errorf("window: glfw init: %w", err)
	}

	glfw.WindowHint(glfw.ClientAPI, glfw.NoAPI)

	if fullscreen {
		mon := glfw.GetPrimaryMonitor()
		mode := mon.GetVideoMode()
		// Match the monitor's mode so the borderless window maps 1:1 to the
		// screen with no rescale by the compositor.
		glfw.WindowHint(glfw.RedBits, mode.RedBits)
		glfw.WindowHint(glfw.GreenBits, mode.GreenBits)
		glfw.WindowHint(glfw.BlueBits, mode.BlueBits)
		glfw.WindowHint(glfw.RefreshRate, mode.RefreshRate)
		glfw.WindowHint(glfw.Decorated, glfw.False)
		glfw.WindowHint(glfw.Resizable, glfw.False)
		width, height = mode.Width, mode.Height

		handle, err := glfw.CreateWindow(width, height, title, nil, nil)
		if err != nil {
			glfw.Terminate()
			return nil, fmt.Errorf("window: create fullscreen window: %w", err)
		}
		mx, my := mon.GetPos()
		handle.SetPos(mx, my)
		return newWindow(handle, title), nil
	}

	glfw.WindowHint(glfw.Resizable, glfw.True)
	handle, err := glfw.CreateWindow(width, height, title, nil, nil)
	if err != nil {
		glfw.Terminate()
		return nil, fmt.Errorf("window: create window: %w", err)
	}
	return newWindow(handle, title), nil
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

// RunPressed reports whether either Shift is held — Doom's "run" modifier,
// which doubles movement speed while down.
func (w *Window) RunPressed() bool {
	return w.handle.GetKey(glfw.KeyLeftShift) == glfw.Press ||
		w.handle.GetKey(glfw.KeyRightShift) == glfw.Press
}

// UsePressed reports whether E — the "use" key, opening doors and
// activating switches — is currently held. (Doom's classic bind is Space;
// Space is the jump key here, see JumpPressed.)
func (w *Window) UsePressed() bool { return w.handle.GetKey(glfw.KeyE) == glfw.Press }

// JumpPressed reports whether Space — the jump key — is currently held.
// Edge-detected in engine.handleMovement: a press while the feet are on the
// ground launches the player upward.
func (w *Window) JumpPressed() bool { return w.handle.GetKey(glfw.KeySpace) == glfw.Press }

// HUDCyclePressed reports whether F1 — the HUD-style cycle key — is held.
// Edge-detected in engine.handleMovement: each press cycles the HUD
// doom -> quake2 -> none -> doom. F1 emits no character event, so it can't
// disturb the cheat-code matcher.
func (w *Window) HUDCyclePressed() bool { return w.handle.GetKey(glfw.KeyF1) == glfw.Press }

// FlashlightPressed reports whether F — the head-mounted flashlight toggle
// — is currently held. Edge-detected in engine.flashlightSystem so a held
// key doesn't retoggle every frame.
func (w *Window) FlashlightPressed() bool { return w.handle.GetKey(glfw.KeyF) == glfw.Press }

// WeaponKeyPressed reports whether the number key for weapon slot n (1-7,
// the classic Doom layout) is currently held.
func (w *Window) WeaponKeyPressed(n int) bool {
	if n < 1 || n > 7 {
		return false
	}
	return w.handle.GetKey(glfw.Key0+glfw.Key(n)) == glfw.Press
}

// FirePressed reports whether the left mouse button — Doom's fire button —
// is currently held.
func (w *Window) FirePressed() bool {
	return w.handle.GetMouseButton(glfw.MouseButtonLeft) == glfw.Press
}

// TakeTyped returns the characters typed since the last call and clears the
// buffer, lower-cased and filtered to ASCII letters and digits — enough to
// recognise the classic cheat-code sequences (see engine/cheats.go). GLFW
// delivers these through its character callback during PollEvents, so this
// must be called on the same (main) thread, once per frame.
func (w *Window) TakeTyped() string {
	if len(w.typed) == 0 {
		return ""
	}
	var b strings.Builder
	for _, r := range w.typed {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		}
	}
	w.typed = w.typed[:0]
	return b.String()
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
