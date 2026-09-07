package launcher

import "twopointfive/window"

// keyRepeatDelay/keyRepeatRate give a held navigation/editing key (arrow,
// Backspace, ...) the usual "press, pause, then repeat" typing feel —
// engine.handleMovement edge-detects its own held keys per-frame with no
// repeat because holding W just means "keep walking"; a text field holding
// Backspace needs to actually keep deleting, hence the timing here instead
// of in package window (see window.MenuKeyDown's doc comment).
const (
	keyRepeatDelay = 0.4 // seconds held before the first repeat
	keyRepeatRate  = 0.03
)

// Input is one frame's snapshot of everything the launcher's widgets read,
// built fresh each frame by (*Poller).Poll from a *window.Window. Widgets
// never touch window.Window directly.
type Input struct {
	MouseX, MouseY   float64
	MouseLeftDown    bool
	MouseLeftClicked bool // just went down this frame
	MouseLeftUp      bool // just went up this frame
	ScrollX, ScrollY float64
	Typed            string // raw characters typed this frame (TakeTypedRaw)

	// Keys is which MenuKeys count as "pressed" this frame, including
	// repeats from a held key — see keyRepeatDelay/keyRepeatRate.
	Keys map[window.MenuKey]bool
}

// Pressed reports whether k should be treated as pressed this frame.
func (in Input) Pressed(k window.MenuKey) bool { return in.Keys[k] }

// Poller turns window.Window's held-key/button state into per-frame edges
// and key-repeat, holding the "was it down last frame" state Input itself
// is too transient to carry.
type Poller struct {
	win           *window.Window
	prevLeftDown  bool
	keyDownSince  map[window.MenuKey]float64 // seconds of game-clock when the key went down, 0 = not down
	keyLastRepeat map[window.MenuKey]float64
	clock         float64
}

// NewPoller wraps win.
func NewPoller(win *window.Window) *Poller {
	return &Poller{
		win:           win,
		keyDownSince:  make(map[window.MenuKey]float64),
		keyLastRepeat: make(map[window.MenuKey]float64),
	}
}

var allMenuKeys = []window.MenuKey{
	window.MenuKeyBackspace, window.MenuKeyDelete, window.MenuKeyEnter, window.MenuKeyTab,
	window.MenuKeyLeft, window.MenuKeyRight, window.MenuKeyUp, window.MenuKeyDown,
	window.MenuKeyHome, window.MenuKeyEnd,
}

// Poll advances the poller's clock by dt (seconds) and returns this frame's
// Input.
func (p *Poller) Poll(dt float64) Input {
	p.clock += dt
	x, y := p.win.CursorPos()
	left := p.win.MouseLeftDown()
	in := Input{
		MouseX: x, MouseY: y,
		MouseLeftDown:    left,
		MouseLeftClicked: left && !p.prevLeftDown,
		MouseLeftUp:      !left && p.prevLeftDown,
		Typed:            p.win.TakeTypedRaw(),
		Keys:             make(map[window.MenuKey]bool, len(allMenuKeys)),
	}
	in.ScrollX, in.ScrollY = p.win.TakeScroll()
	p.prevLeftDown = left

	for _, k := range allMenuKeys {
		down := p.win.MenuKeyDown(k)
		if k == window.MenuKeyEnter && p.win.KeyEnterAlsoNumpad() {
			down = true // numpad Enter is just an alias for MenuKeyEnter
		}
		since, wasDown := p.keyDownSince[k], p.keyDownSince[k] != 0
		switch {
		case down && !wasDown:
			p.keyDownSince[k] = p.clock
			p.keyLastRepeat[k] = p.clock
			in.Keys[k] = true // the initial press
		case down && wasDown:
			held := p.clock - since
			if held >= keyRepeatDelay && p.clock-p.keyLastRepeat[k] >= keyRepeatRate {
				p.keyLastRepeat[k] = p.clock
				in.Keys[k] = true
			}
		case !down:
			p.keyDownSince[k] = 0
		}
	}

	return in
}
