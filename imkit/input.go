package imkit

import (
	"github.com/go-gl/glfw/v3.3/glfw"

	"twopointfive/window"
)

// Key is a keyboard key the editor cares about. The kit keeps its own enum
// (rather than exposing glfw) so callers depend only on imkit.
type Key int

const (
	KeyNone Key = iota
	KeyEscape
	KeyEnter
	KeyTab
	KeySpace
	KeyBackspace
	KeyDelete
	KeyLeft
	KeyRight
	KeyUp
	KeyDown
	KeyHome
	KeyEnd
	KeyShift
	KeyCtrl
	KeyAlt
	KeyMinus
	KeyEqual
	KeyLBracket
	KeyRBracket
	KeyA
	KeyB
	KeyC
	KeyD
	KeyE
	KeyF
	KeyG
	KeyH
	KeyL
	KeyN
	KeyO
	KeyP
	KeyQ
	KeyR
	KeyS
	KeyT
	KeyV
	KeyW
	KeyX
	KeyY
	KeyZ
	Key0
	Key1
	Key2
	Key3
	Key4
	Key5
	Key6
	Key7
	Key8
	Key9
	KeyF1
	KeyF2
	KeyF3
	KeyF5
	KeyF11
	keyCount
)

var keyGLFW = map[Key]glfw.Key{
	KeyEscape: glfw.KeyEscape, KeyEnter: glfw.KeyEnter, KeyTab: glfw.KeyTab,
	KeySpace: glfw.KeySpace, KeyBackspace: glfw.KeyBackspace, KeyDelete: glfw.KeyDelete,
	KeyLeft: glfw.KeyLeft, KeyRight: glfw.KeyRight, KeyUp: glfw.KeyUp, KeyDown: glfw.KeyDown,
	KeyHome: glfw.KeyHome, KeyEnd: glfw.KeyEnd,
	KeyShift: glfw.KeyLeftShift, KeyCtrl: glfw.KeyLeftControl, KeyAlt: glfw.KeyLeftAlt,
	KeyMinus: glfw.KeyMinus, KeyEqual: glfw.KeyEqual,
	KeyLBracket: glfw.KeyLeftBracket, KeyRBracket: glfw.KeyRightBracket,
	KeyA: glfw.KeyA, KeyB: glfw.KeyB, KeyC: glfw.KeyC, KeyD: glfw.KeyD, KeyE: glfw.KeyE,
	KeyF: glfw.KeyF, KeyG: glfw.KeyG, KeyH: glfw.KeyH, KeyL: glfw.KeyL, KeyN: glfw.KeyN,
	KeyO: glfw.KeyO, KeyP: glfw.KeyP, KeyQ: glfw.KeyQ, KeyR: glfw.KeyR, KeyS: glfw.KeyS, KeyT: glfw.KeyT,
	KeyV: glfw.KeyV, KeyW: glfw.KeyW, KeyX: glfw.KeyX, KeyY: glfw.KeyY, KeyZ: glfw.KeyZ,
	Key0: glfw.Key0, Key1: glfw.Key1, Key2: glfw.Key2, Key3: glfw.Key3, Key4: glfw.Key4,
	Key5: glfw.Key5, Key6: glfw.Key6, Key7: glfw.Key7, Key8: glfw.Key8, Key9: glfw.Key9,
	KeyF1: glfw.KeyF1, KeyF2: glfw.KeyF2, KeyF3: glfw.KeyF3, KeyF5: glfw.KeyF5,
	KeyF11: glfw.KeyF11,
}

// Input is one frame's snapshot of everything widgets read. Built by
// (*Poller).Poll. Widgets never touch window.Window / glfw directly.
type Input struct {
	MouseX, MouseY float64
	DragDX, DragDY float64 // mouse movement since last frame (for 3D look / pans)

	LeftDown, LeftClicked, LeftUp    bool
	RightDown, RightClicked, RightUp bool
	MidDown, MidClicked, MidUp       bool

	ScrollX, ScrollY float64
	Typed            string

	down    [keyCount]bool // held this frame
	pressed [keyCount]bool // went down this frame, or a repeat fired
	edge    [keyCount]bool // went down this frame (no repeat)
}

// Down reports whether k is currently held.
func (in *Input) Down(k Key) bool { return k > KeyNone && k < keyCount && in.down[k] }

// Pressed reports a fresh press or an auto-repeat of k this frame (for
// text-editing / list navigation).
func (in *Input) Pressed(k Key) bool { return k > KeyNone && k < keyCount && in.pressed[k] }

// Edge reports only a fresh press of k this frame (for one-shot shortcuts).
func (in *Input) Edge(k Key) bool { return k > KeyNone && k < keyCount && in.edge[k] }

// Ctrl / Shift are convenience modifier reads.
func (in *Input) Ctrl() bool  { return in.down[KeyCtrl] }
func (in *Input) Shift() bool { return in.down[KeyShift] }

const (
	keyRepeatDelay = 0.40
	keyRepeatRate  = 0.03
)

// Poller turns a *window.Window's held state into per-frame edges and
// key-repeat. It reads scroll and typed text through window's callbacks and
// everything else off the raw glfw handle.
type Poller struct {
	win  *window.Window
	gw   *glfw.Window
	prev struct {
		left, right, mid bool
		mx, my           float64
		haveMouse        bool
	}
	downSince  [keyCount]float64
	lastRepeat [keyCount]float64
	clock      float64
}

// NewPoller wraps win.
func NewPoller(win *window.Window) *Poller {
	return &Poller{win: win, gw: win.Handle()}
}

// Poll advances the clock by dt seconds and returns this frame's Input.
func (p *Poller) Poll(dt float64) Input {
	p.clock += dt
	mx, my := p.win.CursorPos()
	left := p.gw.GetMouseButton(glfw.MouseButtonLeft) == glfw.Press
	right := p.gw.GetMouseButton(glfw.MouseButtonRight) == glfw.Press
	mid := p.gw.GetMouseButton(glfw.MouseButtonMiddle) == glfw.Press

	in := Input{
		MouseX: mx, MouseY: my,
		LeftDown: left, LeftClicked: left && !p.prev.left, LeftUp: !left && p.prev.left,
		RightDown: right, RightClicked: right && !p.prev.right, RightUp: !right && p.prev.right,
		MidDown: mid, MidClicked: mid && !p.prev.mid, MidUp: !mid && p.prev.mid,
		Typed: p.win.TakeTypedRaw(),
	}
	in.ScrollX, in.ScrollY = p.win.TakeScroll()
	if p.prev.haveMouse {
		in.DragDX, in.DragDY = mx-p.prev.mx, my-p.prev.my
	}
	p.prev.left, p.prev.right, p.prev.mid = left, right, mid
	p.prev.mx, p.prev.my, p.prev.haveMouse = mx, my, true

	for k := KeyNone + 1; k < keyCount; k++ {
		gk, ok := keyGLFW[k]
		if !ok {
			continue
		}
		down := p.gw.GetKey(gk) == glfw.Press
		if k == KeyShift {
			down = down || p.gw.GetKey(glfw.KeyRightShift) == glfw.Press
		}
		if k == KeyCtrl {
			down = down || p.gw.GetKey(glfw.KeyRightControl) == glfw.Press
		}
		if k == KeyEnter {
			down = down || p.gw.GetKey(glfw.KeyKPEnter) == glfw.Press
		}
		in.down[k] = down
		was := p.downSince[k] != 0
		switch {
		case down && !was:
			p.downSince[k] = p.clock
			p.lastRepeat[k] = p.clock
			in.pressed[k], in.edge[k] = true, true
		case down && was:
			if p.clock-p.downSince[k] >= keyRepeatDelay && p.clock-p.lastRepeat[k] >= keyRepeatRate {
				p.lastRepeat[k] = p.clock
				in.pressed[k] = true
			}
		default:
			p.downSince[k] = 0
		}
	}
	return in
}
