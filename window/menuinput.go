package window

import "github.com/go-gl/glfw/v3.3/glfw"

// Input the pre-game launcher (package launcher) needs on top of the
// gameplay-oriented methods in window.go: a visible, absolute cursor
// (EnableMouseLook's hidden/relative mode is never turned on for this
// window), left/right mouse buttons, scroll, raw text entry, and a handful
// of navigation keys for a text field or a scrollable list. Kept in its own
// file since it's a distinct, menu-only surface on top of Window.

// CursorPos returns the mouse position in window coordinates (pixels from
// the top-left), for hit-testing widgets. Meaningless while mouse-look is
// enabled (EnableMouseLook switches GetCursorPos to an unbounded virtual
// position) — the launcher never calls EnableMouseLook, so this is always
// the real, visible-cursor position there.
func (w *Window) CursorPos() (x, y float64) {
	return w.handle.GetCursorPos()
}

// MouseLeftDown / MouseRightDown report whether the given mouse button is
// currently held. (FirePressed in window.go is the same left-button query
// under gameplay's name; this is the menu-context name.)
func (w *Window) MouseLeftDown() bool {
	return w.handle.GetMouseButton(glfw.MouseButtonLeft) == glfw.Press
}
func (w *Window) MouseRightDown() bool {
	return w.handle.GetMouseButton(glfw.MouseButtonRight) == glfw.Press
}

// TakeScroll returns the scroll-wheel motion accumulated since the last
// call and clears it — horizontal (rare; a trackpad or a tilt-wheel) then
// vertical, matching GLFW's callback argument order. +Y is scroll-up.
func (w *Window) TakeScroll() (dx, dy float64) {
	dx, dy = w.scrollX, w.scrollY
	w.scrollX, w.scrollY = 0, 0
	return dx, dy
}

// TakeTypedRaw returns the characters typed since the last call and clears
// the buffer, same accumulation as TakeTyped but WITHOUT lower-casing or
// filtering to alphanumerics — a text field needs real case, spaces,
// punctuation and path separators (an IWAD path, a hex fog colour). Control
// characters (which GLFW's character callback shouldn't deliver anyway) are
// dropped defensively.
func (w *Window) TakeTypedRaw() string {
	if len(w.typed) == 0 {
		return ""
	}
	out := make([]rune, 0, len(w.typed))
	for _, r := range w.typed {
		if r >= 0x20 && r != 0x7f {
			out = append(out, r)
		}
	}
	w.typed = w.typed[:0]
	return string(out)
}

// MenuKey names a navigation/editing key the launcher's text fields and
// lists need, kept as a small enum (rather than one exported method per
// key) so this file doesn't grow ten near-identical one-liners; MenuKeyDown
// is still the only thing outside this package that ever names a glfw key.
type MenuKey int

const (
	MenuKeyBackspace MenuKey = iota
	MenuKeyDelete
	MenuKeyEnter
	MenuKeyTab
	MenuKeyLeft
	MenuKeyRight
	MenuKeyUp
	MenuKeyDown
	MenuKeyHome
	MenuKeyEnd
)

var menuKeyGLFW = map[MenuKey]glfw.Key{
	MenuKeyBackspace: glfw.KeyBackspace,
	MenuKeyDelete:    glfw.KeyDelete,
	MenuKeyEnter:     glfw.KeyEnter,
	MenuKeyTab:       glfw.KeyTab,
	MenuKeyLeft:      glfw.KeyLeft,
	MenuKeyRight:     glfw.KeyRight,
	MenuKeyUp:        glfw.KeyUp,
	MenuKeyDown:      glfw.KeyDown,
	MenuKeyHome:      glfw.KeyHome,
	MenuKeyEnd:       glfw.KeyEnd,
}

// MenuKeyDown reports whether k is currently held. The caller (package
// launcher's Input snapshot) does its own edge-detection and key-repeat
// timing against this, the same way engine.handleMovement edge-detects
// HUDCyclePressed/JumpPressed itself rather than pushing that logic into
// package window.
func (w *Window) MenuKeyDown(k MenuKey) bool {
	gk, ok := menuKeyGLFW[k]
	if !ok {
		return false
	}
	return w.handle.GetKey(gk) == glfw.Press
}

// KeyEnterAlsoNumpad reports whether either Enter key (main or numpad) is
// held — a couple of platforms/keyboards only send KeyKPEnter for the
// numpad key, which GetKey(glfw.KeyEnter) alone would miss.
func (w *Window) KeyEnterAlsoNumpad() bool {
	return w.handle.GetKey(glfw.KeyEnter) == glfw.Press || w.handle.GetKey(glfw.KeyKPEnter) == glfw.Press
}
