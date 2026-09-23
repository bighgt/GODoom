package imkit

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// UI is the immediate-mode widget kit. Cross-frame state (which text field
// has focus, which menu is open, an in-progress drag) lives here, keyed by
// each widget's caller-supplied id.
type UI struct {
	C  *Canvas
	In Input

	focusText  string
	textCursor map[string]int
	intDrafts  map[string]string

	// menu bar + popups (menu.go). menuBlock is the open popup's rect;
	// hover() ignores widgets under it.
	menuBar     Rect
	menuX       int
	openMenu    string
	menuBlock   Rect
	pendingMenu *pendingMenu

	// overlays drawn last by End
	tip        *tooltip
	drag       *dragState
	dropHit    any  // payload dropped onto a DropTarget this frame
	dropOK     bool // a DropTarget accepted this frame
	blockInput bool // a modal is up: swallow clicks/keys from widgets beneath
}

// New creates a kit drawing into c.
func New(c *Canvas) *UI {
	return &UI{C: c, textCursor: map[string]int{}, intDrafts: map[string]string{}}
}

// Begin starts a frame. Call once before any widget.
func (ui *UI) Begin(in Input) {
	ui.In = in
	ui.dropHit, ui.dropOK, ui.blockInput = nil, false, false
	ui.menuBlock = Rect{}
}

// End draws deferred overlays (drag ghost, tooltip) on top of the frame and
// resolves clicks on them. Call once after all widgets.
func (ui *UI) End() {
	ui.drawPendingMenu()
	if ui.drag != nil {
		ui.drawDragGhost()
	}
	if ui.tip != nil {
		ui.drawTooltip(ui.tip)
		ui.tip = nil
	}
	// A left-release always ends a drag (any DropTarget under the cursor got
	// its chance this frame).
	if ui.drag != nil && ui.In.LeftUp {
		ui.drag = nil
	}
}

// hover reports whether the mouse is over r, unless an open menu popup
// overlay covers that point (its rows sit on top of the widgets beneath).
func (ui *UI) hover(r Rect) bool {
	if ui.blockInput {
		return false
	}
	if ui.menuBlock.W > 0 && ui.menuBlock.Contains(int(ui.In.MouseX), int(ui.In.MouseY)) {
		return false
	}
	return r.Contains(int(ui.In.MouseX), int(ui.In.MouseY))
}

// MouseIn is hover without the overlay guard — for panes that want the raw
// "is the cursor over me" answer.
func (ui *UI) MouseIn(r Rect) bool {
	return r.Contains(int(ui.In.MouseX), int(ui.In.MouseY))
}

// ---- text ----------------------------------------------------------------

// Label draws plain text left-aligned in r.
func (ui *UI) Label(r Rect, text string, dim bool) {
	col := Theme.Text
	if dim {
		col = Theme.TextDim
	}
	ui.C.TextLeft(r, text, col)
}

type tooltip struct {
	at   Rect
	text string
}

// Tooltip queues a one-line hint under anchor when the mouse is over it.
func (ui *UI) Tooltip(anchor Rect, text string) {
	if text == "" || !ui.hover(anchor) {
		return
	}
	ui.tip = &tooltip{at: anchor, text: text}
}

func (ui *UI) drawTooltip(t *tooltip) {
	const pad = 6
	w := TextWidth(t.text) + pad*2
	h := GlyphHeight + pad*2
	x := t.at.X
	y := t.at.Y + t.at.H + 4
	if y+h > ui.C.H {
		y = t.at.Y - h - 4
	}
	if x+w > ui.C.W-2 {
		x = ui.C.W - 2 - w
	}
	if x < 2 {
		x = 2
	}
	r := Rect{x, y, w, h}
	ui.C.FillRect(r, Theme.Panel)
	ui.C.StrokeRect(r, Theme.Accent, 1)
	ui.C.TextLeft(r, t.text, Theme.Text)
}

// ---- button ------------------------------------------------------------

// IconButton is a compact square button showing a 1-3 char glyph string.
func (ui *UI) IconButton(r Rect, glyph string, active bool) bool {
	hot := ui.hover(r)
	col := Theme.Control
	switch {
	case active:
		col = Theme.Accent
	case hot:
		col = Theme.ControlHover
	}
	ui.C.FillRect(r, col)
	ui.C.StrokeRect(r, Theme.Border, 1)
	ui.C.TextCenter(r, glyph, Theme.Text)
	return hot && ui.In.LeftClicked
}

// ---- checkbox --------------------------------------------------------

// Checkbox draws "label [x]" across r (label in the LabelWidth column).
func (ui *UI) Checkbox(r Rect, label string, v *bool) bool {
	ui.Label(Rect{r.X, r.Y, LabelWidth, r.H}, label, false)
	box := Rect{r.X + LabelWidth, r.Y + (r.H-16)/2, 16, 16}
	bg := Theme.Control
	if ui.hover(box) {
		bg = Theme.ControlHover
	}
	ui.C.FillRect(box, bg)
	ui.C.StrokeRect(box, Theme.Border, 1)
	if *v {
		ui.C.FillRect(box.Inset(4), Theme.Accent)
	}
	if ui.hover(box) && ui.In.LeftClicked {
		*v = !*v
		return true
	}
	return false
}

// ---- text field ----------------------------------------------------

// TextField is a single-line editable string. Returns true on any change.
func (ui *UI) TextField(id string, r Rect, label string, v *string) bool {
	box := r
	if label != "" {
		ui.Label(Rect{r.X, r.Y, LabelWidth, r.H}, label, false)
		box = Rect{r.X + LabelWidth, r.Y, r.W - LabelWidth, r.H}
	}
	focused := ui.focusText == id
	if ui.In.LeftClicked {
		if ui.hover(box) {
			ui.focusText, focused = id, true
			ui.textCursor[id] = utf8.RuneCountInString(*v)
		} else if focused {
			ui.focusText, focused = "", false
		}
	}
	changed := false
	if focused {
		runes := []rune(*v)
		cur := max(0, min(len(runes), ui.textCursor[id]))
		for _, c := range ui.In.Typed {
			runes = append(runes[:cur], append([]rune{c}, runes[cur:]...)...)
			cur++
			changed = true
		}
		if ui.In.Pressed(KeyBackspace) && cur > 0 {
			runes = append(runes[:cur-1], runes[cur:]...)
			cur--
			changed = true
		}
		if ui.In.Pressed(KeyDelete) && cur < len(runes) {
			runes = append(runes[:cur], runes[cur+1:]...)
			changed = true
		}
		if ui.In.Pressed(KeyLeft) && cur > 0 {
			cur--
		}
		if ui.In.Pressed(KeyRight) && cur < len(runes) {
			cur++
		}
		if ui.In.Pressed(KeyHome) {
			cur = 0
		}
		if ui.In.Pressed(KeyEnd) {
			cur = len(runes)
		}
		if ui.In.Edge(KeyEnter) || ui.In.Edge(KeyTab) || ui.In.Edge(KeyEscape) {
			ui.focusText, focused = "", false
		}
		ui.textCursor[id] = cur
		if changed {
			*v = string(runes)
		}
	}
	bg := Theme.Control
	if focused {
		bg = Theme.ControlHover
	}
	ui.C.FillRect(box, bg)
	ui.C.StrokeRect(box, Theme.Border, 1)
	if focused {
		ui.C.StrokeRect(box, Theme.Accent, 1)
	}
	prev := ui.C.Clip(box)
	ui.C.TextLeft(box, *v, Theme.Text)
	if focused {
		cx := box.X + 8 + ui.textCursor[id]*GlyphAdvance
		ui.C.FillRect(Rect{cx, box.Y + 3, 1, box.H - 6}, Theme.Accent)
	}
	ui.C.SetClip(prev)
	return changed
}

// Focused reports whether id is the currently-focused text-entry field
// (TextField/IntField and anything built the same way, e.g. a caller's own
// float field) — exported so a caller outside this package can implement
// its own draft-buffer field type using the exact same "only re-format
// from the canonical value while NOT focused" rule IntField uses below,
// without needing access to this package's own private focus state.
func (ui *UI) Focused(id string) bool { return ui.focusText == id }

// IntField edits an *int through a draft buffer so mid-edit non-numbers
// don't clobber the value.
func (ui *UI) IntField(id string, r Rect, label string, v *int) bool {
	if ui.focusText != id {
		ui.intDrafts[id] = strconv.Itoa(*v)
	}
	s := ui.intDrafts[id]
	ui.TextField(id, r, label, &s)
	ui.intDrafts[id] = s
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && n != *v {
		*v = n
		return true
	}
	return false
}
