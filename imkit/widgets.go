package imkit

import (
	"fmt"
	"image/color"
	"strconv"
	"strings"
	"unicode/utf8"
)

// UI is the immediate-mode widget kit. Cross-frame state (which text field
// has focus, which dropdown/menu is open, an in-progress drag) lives here,
// keyed by each widget's caller-supplied id.
type UI struct {
	C  *Canvas
	In Input

	focusText  string
	textCursor map[string]int
	intDrafts  map[string]string
	dragSlider string

	openDropdown string
	deferDrop    *deferDrop
	dropScroll   map[string]int

	// menu bar + popups (menu.go). menuBlock is the open popup's rect;
	// hover() ignores widgets under it.
	menuBar     Rect
	menuX       int
	openMenu    string
	menuBlock   Rect
	ctxAt       Rect
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
	return &UI{
		C: c, textCursor: map[string]int{}, intDrafts: map[string]string{},
		dropScroll: map[string]int{},
	}
}

// Begin starts a frame. Call once before any widget.
func (ui *UI) Begin(in Input) {
	ui.In = in
	ui.dropHit, ui.dropOK, ui.blockInput = nil, false, false
	ui.menuBlock = Rect{}
}

// End draws deferred overlays (open dropdown list, drag ghost, tooltip) on
// top of the frame and resolves clicks on them. Call once after all widgets.
func (ui *UI) End() {
	if d := ui.deferDrop; d != nil {
		ui.drawDropdownList(d)
	}
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
	ui.deferDrop = nil
}

// hover reports whether the mouse is over r, unless an open dropdown/menu
// overlay covers that point (its rows sit on top of the widgets beneath).
func (ui *UI) hover(r Rect) bool {
	if ui.blockInput {
		return false
	}
	if d := ui.deferDrop; d != nil && d.list.Contains(int(ui.In.MouseX), int(ui.In.MouseY)) {
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

type ButtonStyle int

const (
	ButtonNormal ButtonStyle = iota
	ButtonPrimary
	ButtonDanger
	ButtonSelected
)

// Button draws a centred-label button filling r; reports a click.
func (ui *UI) Button(r Rect, label string, style ButtonStyle) bool {
	base, hi := Theme.Control, Theme.ControlHover
	switch style {
	case ButtonPrimary:
		base, hi = Theme.Primary, Theme.PrimaryHover
	case ButtonDanger:
		base, hi = Theme.Danger, Theme.Danger
	case ButtonSelected:
		base, hi = Theme.Accent, Theme.Accent
	}
	hot := ui.hover(r)
	col := base
	if hot {
		col = hi
	}
	ui.C.FillRect(r, col)
	ui.C.StrokeRect(r, Theme.Border, 1)
	ui.C.TextCenter(r, label, Theme.Text)
	return hot && ui.In.LeftClicked
}

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

// ---- slider ----------------------------------------------------------

func (ui *UI) Slider(id string, r Rect, label string, v *float64, lo, hi float64, format string) bool {
	if label != "" {
		ui.Label(Rect{r.X, r.Y, LabelWidth, r.H}, label, false)
		r = Rect{r.X + LabelWidth, r.Y, r.W - LabelWidth, r.H}
	}
	valueW := 56
	track := Rect{r.X, r.Y + r.H/2 - 3, r.W - valueW - 8, 6}
	ui.C.FillRect(track, Theme.Control)
	ui.C.StrokeRect(track, Theme.Border, 1)
	if hi <= lo {
		hi = lo + 1
	}
	frac := clamp01((*v - lo) / (hi - lo))
	hx := track.X + int(frac*float64(track.W)) - 4
	handle := Rect{hx, r.Y + r.H/2 - 8, 8, 16}
	hc := Theme.Accent
	if ui.hover(track) || ui.dragSlider == id {
		hc = Theme.AccentDim
	}
	ui.C.FillRect(handle, hc)

	changed := false
	switch {
	case ui.In.LeftClicked && (ui.hover(track) || ui.hover(handle)):
		ui.dragSlider = id
		fallthrough
	case ui.dragSlider == id && ui.In.LeftDown:
		tw := track.W
		if tw < 1 {
			tw = 1
		}
		f := clamp01(float64(int(ui.In.MouseX)-track.X) / float64(tw))
		if nv := lo + f*(hi-lo); nv != *v {
			*v = nv
			changed = true
		}
	}
	if ui.In.LeftUp && ui.dragSlider == id {
		ui.dragSlider = ""
	}
	ui.C.TextLeft(Rect{track.X + track.W + 8, r.Y, valueW, r.H}, fmt.Sprintf(format, *v), Theme.TextDim)
	return changed
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

// ---- dropdown ------------------------------------------------------

// Choice is one dropdown entry: Value is written back, Label is shown.
type Choice struct{ Value, Label string }

// Choices builds a []Choice with Label == Value.
func Choices(values []string) []Choice {
	out := make([]Choice, len(values))
	for i, v := range values {
		out[i] = Choice{v, v}
	}
	return out
}

const dropdownMaxRows = 10

type deferDrop struct {
	id            string
	control, list Rect
	options       []Choice
	current       string
	write         func(string)
	scroll        int
	visible       int
}

func choiceLabel(opts []Choice, val string) string {
	for _, o := range opts {
		if o.Value == val {
			return o.Label
		}
	}
	return val
}

// Options is a closed-box dropdown. write is called (from End) with the
// newly-selected value.
func (ui *UI) Options(id string, r Rect, label, current string, opts []Choice, write func(string)) {
	box := r
	if label != "" {
		ui.Label(Rect{r.X, r.Y, LabelWidth, r.H}, label, false)
		box = Rect{r.X + LabelWidth, r.Y, r.W - LabelWidth, r.H}
	}
	open := ui.openDropdown == id
	bg := Theme.Control
	if ui.hover(box) || open {
		bg = Theme.ControlHover
	}
	ui.C.FillRect(box, bg)
	ui.C.StrokeRect(box, Theme.Border, 1)
	prev := ui.C.Clip(box)
	ui.C.TextLeft(box, choiceLabel(opts, current), Theme.Text)
	ui.C.SetClip(prev)
	ui.C.Text(box.X+box.W-14, box.Y+box.H/2, "v", Theme.TextDim)

	if ui.hover(box) && ui.In.LeftClicked {
		if open {
			ui.openDropdown = ""
		} else {
			ui.openDropdown, ui.dropScroll[id] = id, 0
		}
	}
	if ui.openDropdown == id {
		visible := min(len(opts), dropdownMaxRows)
		maxScroll := max(len(opts)-visible, 0)
		rowH := box.H
		ui.deferDrop = &deferDrop{
			id: id, control: box, options: opts, current: current, write: write,
			list:    Rect{box.X, box.Y + box.H, box.W, visible * rowH},
			scroll:  max(0, min(maxScroll, ui.dropScroll[id])),
			visible: visible,
		}
	}
}

func (ui *UI) drawDropdownList(d *deferDrop) {
	rowH := d.control.H
	ui.C.FillRect(d.list, Theme.Panel)
	ui.C.StrokeRect(d.list, Theme.Border, 1)
	for row := 0; row < d.visible; row++ {
		idx := d.scroll + row
		if idx >= len(d.options) {
			break
		}
		rr := Rect{d.list.X, d.list.Y + row*rowH, d.list.W, rowH}
		bg := Theme.Panel
		switch {
		case d.options[idx].Value == d.current:
			bg = Theme.AccentDim
		case rr.Contains(int(ui.In.MouseX), int(ui.In.MouseY)):
			bg = Theme.ControlHover
		}
		ui.C.FillRect(rr, bg)
		ui.C.TextLeft(rr, d.options[idx].Label, Theme.Text)
	}
	scrollable := len(d.options) > d.visible
	if scrollable {
		maxScroll := len(d.options) - d.visible
		th := max(d.list.H*d.visible/len(d.options), 12)
		ty := d.list.Y
		if maxScroll > 0 {
			ty += (d.list.H - th) * d.scroll / maxScroll
		}
		ui.C.FillRect(Rect{d.list.X + d.list.W - 4, d.list.Y, 4, d.list.H}, Theme.Panel)
		ui.C.FillRect(Rect{d.list.X + d.list.W - 4, ty, 4, th}, Theme.ScrollThumb)
		if d.list.Contains(int(ui.In.MouseX), int(ui.In.MouseY)) && ui.In.ScrollY != 0 {
			d.scroll = max(0, min(maxScroll, d.scroll-int(ui.In.ScrollY)))
			ui.dropScroll[d.id] = d.scroll
		}
	}
	if !ui.In.LeftClicked {
		ui.openDropdown = d.id
		ui.deferDrop = d
		return
	}
	mx, my := int(ui.In.MouseX), int(ui.In.MouseY)
	if d.control.Contains(mx, my) {
		return // Options() toggled it this frame
	}
	if !d.list.Contains(mx, my) {
		ui.openDropdown = ""
		return
	}
	row := (my - d.list.Y) / rowH
	idx := d.scroll + row
	ui.openDropdown = ""
	if row < 0 || row >= d.visible || idx >= len(d.options) {
		return
	}
	if sel := d.options[idx]; sel.Value != d.current && d.write != nil {
		d.write(sel.Value)
	}
}

func clamp01(f float64) float64 {
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}

// fillA is FillRect with an explicit alpha over col.
func (ui *UI) fillA(r Rect, col color.RGBA) { ui.C.FillRect(r, col) }
