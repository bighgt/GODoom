package launcher

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"twopointfive/window"
)

// UI is the whole immediate-mode widget kit this package needs: buttons,
// checkboxes, sliders, a dropdown, and a single-line text field. State that
// must survive between frames (which text field has focus, its cursor
// position, which dropdown is open, which slider is being dragged) lives
// here, keyed by each widget's caller-supplied id — an id-keyed map instead
// of one Go struct field per widget, so the ~38-row settings table
// (fields.go) doesn't need a matching ~38 fields of UI state.
type UI struct {
	C  *Canvas
	In Input

	focusText  string
	textCursor map[string]int
	dragSlider string
	// intDrafts holds the free-typed text for an IntField between frames —
	// see IntField's doc comment for why it isn't just re-derived from the
	// int every frame.
	intDrafts map[string]string

	openDropdown string // id of the open dropdown, "" = none
	deferred     *deferredDropdown

	// tip is this frame's pending hover tooltip, drawn on top of everything
	// by End (before the dropdown overlay, so an open dropdown still wins).
	// Set by Hint; last setter this frame wins.
	tip *tooltip
	// dropdownScroll is, per dropdown id, the index of the first option
	// currently visible — a dropdown with more options than dropdownMaxRows
	// scrolls instead of squeezing every row into the same fixed-height box
	// (see Options/End's doc comments).
	dropdownScroll map[string]int
}

// dropdownMaxRows caps how many options an open dropdown shows at once,
// each at the full rowHeight — a WAD with many maps (a megawad can have
// several dozen) scrolls through the rest via the mouse wheel rather than
// this shrinking every row to fit them all in the same space.
const dropdownMaxRows = 8

// tooltip is a one-line hint box anchored under (or, near the panel bottom,
// over) a settings row.
type tooltip struct {
	anchor Rect
	text   string
}

type deferredDropdown struct {
	id            string
	control, list Rect
	options       []choice
	current       string // the selected VALUE (choice.value), not its label
	write         func(string)
	scroll        int // index of the first option drawn in list (see dropdownScroll)
	visible       int // how many rows list actually holds (<= dropdownMaxRows)
}

// NewUI creates a widget kit drawing into c.
func NewUI(c *Canvas) *UI {
	return &UI{
		C: c, textCursor: make(map[string]int), intDrafts: make(map[string]string),
		dropdownScroll: make(map[string]int),
	}
}

// Focused reports whether id currently holds text-field focus.
func (ui *UI) Focused(id string) bool { return ui.focusText == id }

// Begin starts a frame with in as this frame's input snapshot. Call once
// per frame before any widget calls.
func (ui *UI) Begin(in Input) { ui.In = in }

// End finishes a frame: draws the open dropdown's option list (if any) on
// top of everything else drawn this frame, and resolves a click on it.
// changed reports whether a selection was made; if so v already reflects it
// — screen.go doesn't need to do anything with the return value beyond
// knowing a redraw/validation pass is worth doing sooner rather than later.
func (ui *UI) End() (changed bool) {
	if ui.tip != nil {
		ui.drawTooltip(ui.tip)
		ui.tip = nil
	}

	d := ui.deferred
	ui.deferred = nil
	if d == nil {
		return false
	}
	ui.C.FillRect(d.list, theme.Panel)
	ui.C.StrokeRect(d.list, theme.Border, 1)
	for row := 0; row < d.visible; row++ {
		idx := d.scroll + row
		if idx >= len(d.options) {
			break
		}
		opt := d.options[idx]
		r := Rect{X: d.list.X, Y: d.list.Y + row*rowHeight, W: d.list.W, H: rowHeight}
		bg := theme.Panel
		if opt.value == d.current {
			bg = theme.AccentDim
		} else if r.Contains(int(ui.In.MouseX), int(ui.In.MouseY)) {
			bg = theme.ControlHover
		}
		ui.C.FillRect(r, bg)
		ui.C.TextLeft(r, opt.label, theme.Text)
	}
	scrollable := len(d.options) > d.visible
	if scrollable {
		trackH := d.list.H
		thumbH := max(trackH*d.visible/len(d.options), 12)
		maxScroll := len(d.options) - d.visible
		thumbY := d.list.Y
		if maxScroll > 0 {
			thumbY += (trackH - thumbH) * d.scroll / maxScroll
		}
		ui.C.FillRect(Rect{X: d.list.X + d.list.W - 4, Y: d.list.Y, W: 4, H: trackH}, theme.Panel)
		ui.C.FillRect(Rect{X: d.list.X + d.list.W - 4, Y: thumbY, W: 4, H: thumbH}, theme.ScrollThumb)
	}

	// Scrolling and closing both leave the dropdown open for another frame
	// (returning early re-arms ui.deferred); only an actual selection or an
	// outside click closes it.
	if scrollable && ui.hover(d.list) && ui.In.ScrollY != 0 {
		maxScroll := len(d.options) - d.visible
		d.scroll = max(0, min(maxScroll, d.scroll-int(ui.In.ScrollY)))
		ui.dropdownScroll[d.id] = d.scroll
	}
	if !ui.In.MouseLeftClicked {
		ui.openDropdown = d.id // still open, nothing clicked
		ui.deferred = d
		return false
	}
	mx, my := int(ui.In.MouseX), int(ui.In.MouseY)
	if d.control.Contains(mx, my) {
		return false // Options() itself already toggled this closed this frame
	}
	if !d.list.Contains(mx, my) {
		ui.openDropdown = "" // clicked well outside — just close
		return false
	}
	row := (my - d.list.Y) / rowHeight
	idx := d.scroll + row
	if row < 0 || row >= d.visible || idx >= len(d.options) {
		ui.openDropdown = ""
		return false
	}
	sel := d.options[idx]
	ui.openDropdown = ""
	if sel.value == d.current {
		return false
	}
	// The caller's *string was already captured by reference in Options();
	// deferredDropdown keeps only the value, so the write-back happens via
	// the pointer screen.go passed us — see Options' closure.
	if d.write != nil {
		d.write(sel.value)
	}
	return true
}

// hover reports whether the mouse is over r — every widget uses this for
// its rollover highlight AND its click test. It returns false whenever the
// pointer is inside an open dropdown's overlay list: that list is drawn on
// top by End and its rows sit directly over whatever widgets follow it in
// the layout (the Renderer list's first option lands exactly on the
// Lighting Mode box), so without this a click meant for an option would
// also toggle the widget beneath it — and, being processed first, that
// widget would steal the openDropdown slot and the option click would be
// lost. Options arms ui.deferred before any covered widget runs (dropdowns
// only ever open downward, onto later rows), so this is set in time.
func (ui *UI) hover(r Rect) bool {
	if ui.deferred != nil && ui.deferred.list.Contains(int(ui.In.MouseX), int(ui.In.MouseY)) {
		return false
	}
	return r.Contains(int(ui.In.MouseX), int(ui.In.MouseY))
}

// Hint queues text to be shown as a hover tooltip for anchor — a no-op
// unless the mouse is over anchor this frame. Called after a row's widget
// is drawn (screen.go), it's what turns each fields.go hint into an
// explanation on rollover. The last Hint call of the frame wins, so the
// row the mouse is actually over is the one that shows.
func (ui *UI) Hint(anchor Rect, text string) {
	if text == "" || !ui.hover(anchor) {
		return
	}
	ui.tip = &tooltip{anchor: anchor, text: text}
}

// drawTooltip paints t just below its anchor row, flipping above it near
// the panel bottom and clamping to the window horizontally.
func (ui *UI) drawTooltip(t *tooltip) {
	const pad = 6
	w := TextWidth(t.text) + pad*2
	h := glyphHeight + pad*2
	x := t.anchor.X + labelWidth
	y := t.anchor.Y + t.anchor.H + 4
	if y+h > windowH-bottomBarH {
		y = t.anchor.Y - h - 4
	}
	if x+w > windowW-panelPad {
		x = windowW - panelPad - w
	}
	if x < panelPad {
		x = panelPad
	}
	r := Rect{X: x, Y: y, W: w, H: h}
	ui.C.FillRect(r, theme.Panel)
	ui.C.StrokeRect(r, theme.Accent, 1)
	ui.C.TextLeft(r, t.text, theme.Text)
}

// Label draws plain text left-aligned in r, no interaction.
func (ui *UI) Label(r Rect, text string, dim bool) {
	col := theme.Text
	if dim {
		col = theme.TextDim
	}
	ui.C.TextLeft(r, text, col)
}

// ButtonStyle picks a button's fill colours.
type ButtonStyle int

const (
	ButtonNormal ButtonStyle = iota
	ButtonPrimary            // Play
	ButtonDanger             // not currently used, kept for a future Reset
	ButtonSelected           // the currently-chosen item in a quick-pick row (e.g. a WAD chip)
)

// Button draws a centred-label button filling r and reports whether it was
// clicked this frame.
func (ui *UI) Button(r Rect, label string, style ButtonStyle) bool {
	base, hoverCol := theme.Control, theme.ControlHover
	switch style {
	case ButtonPrimary:
		base, hoverCol = theme.Play, theme.PlayHover
	case ButtonDanger:
		base, hoverCol = theme.Error, theme.Error
	case ButtonSelected:
		base, hoverCol = theme.Accent, theme.Accent
	}
	col := base
	hot := ui.hover(r)
	if hot {
		col = hoverCol
	}
	ui.C.FillRect(r, col)
	ui.C.StrokeRect(r, theme.Border, 1)
	tw := TextWidth(label)
	ui.C.Text(r.X+(r.W-tw)/2, r.Y+r.H/2, label, theme.Text)
	return hot && ui.In.MouseLeftClicked
}

// Checkbox draws label:box in r (label in the left labelWidth column, a
// small box after it) and flips *v on click, reporting whether it changed.
func (ui *UI) Checkbox(id string, r Rect, label string, v *bool) bool {
	ui.Label(Rect{X: r.X, Y: r.Y, W: labelWidth, H: r.H}, label, false)
	box := Rect{X: r.X + labelWidth, Y: r.Y + (r.H-18)/2, W: 18, H: 18}
	bg := theme.Control
	if ui.hover(box) {
		bg = theme.ControlHover
	}
	ui.C.FillRect(box, bg)
	ui.C.StrokeRect(box, theme.Border, 1)
	if *v {
		ui.C.FillRect(box.Inset(4), theme.Accent)
	}
	if ui.hover(box) && ui.In.MouseLeftClicked {
		*v = !*v
		return true
	}
	return false
}

// Slider draws label:track:handle:value in r for a float64 in [min, max].
// Click-and-drag anywhere on the track jumps/moves the value; format is a
// fmt.Sprintf verb for the value readout (e.g. "%.2f", "%.0f%%").
func (ui *UI) Slider(id string, r Rect, label string, v *float64, min, max float64, format string) bool {
	ui.Label(Rect{X: r.X, Y: r.Y, W: labelWidth, H: r.H}, label, false)
	valueW := 64
	track := Rect{X: r.X + labelWidth, Y: r.Y + r.H/2 - 3, W: r.W - labelWidth - valueW - 8, H: 6}
	ui.C.FillRect(track, theme.Control)
	ui.C.StrokeRect(track, theme.Border, 1)

	if max <= min {
		max = min + 1
	}
	frac := (*v - min) / (max - min)
	frac = clamp01(frac)
	handleX := track.X + int(frac*float64(track.W)) - 4
	handle := Rect{X: handleX, Y: r.Y + r.H/2 - 8, W: 8, H: 16}
	handleCol := theme.Accent
	if ui.hover(track) || ui.dragSlider == id {
		handleCol = theme.AccentDim
	}
	ui.C.FillRect(handle, handleCol)

	changed := false
	switch {
	case ui.In.MouseLeftClicked && (ui.hover(track) || ui.hover(handle)):
		ui.dragSlider = id
		fallthrough
	case ui.dragSlider == id && ui.In.MouseLeftDown:
		// track.W is never <= 0 in practice, but guard the divide anyway;
		// min/max here are this Slider's own [min,max] value-range params,
		// shadowing the builtins, so this can't just be max(track.W, 1).
		trackW := track.W
		if trackW < 1 {
			trackW = 1
		}
		f := clamp01(float64(int(ui.In.MouseX)-track.X) / float64(trackW))
		nv := min + f*(max-min)
		if nv != *v {
			*v = nv
			changed = true
		}
	}
	if ui.In.MouseLeftUp && ui.dragSlider == id {
		ui.dragSlider = ""
	}

	valueRect := Rect{X: track.X + track.W + 8, Y: r.Y, W: valueW, H: r.H}
	ui.C.TextLeft(valueRect, fmt.Sprintf(format, *v), theme.TextDim)
	return changed
}

// Options is a closed-box dropdown: clicking it opens (or closes) an
// overlay list of options, drawn on top of everything else by (*UI).End —
// see deferredDropdown. write is called by End with the newly-selected
// option; screen.go's callers pass a closure over the actual *string so
// Options itself never needs a generic reflect-free "set string field"
// mechanism.
// labelFor is the display text for value — the matching choice's label, or
// value itself if it isn't one of options (a config.json value this table
// doesn't recognize — see fields_test.go's TestFieldAccessorsAreWired for
// why that shouldn't happen for a table-driven field, but the closed
// dropdown box still needs to show something rather than go blank).
func labelFor(options []choice, value string) string {
	for _, o := range options {
		if o.value == value {
			return o.label
		}
	}
	return value
}

func (ui *UI) Options(id string, r Rect, label string, current string, options []choice, write func(string)) {
	ui.Label(Rect{X: r.X, Y: r.Y, W: labelWidth, H: r.H}, label, false)
	box := Rect{X: r.X + labelWidth, Y: r.Y, W: r.W - labelWidth, H: r.H}
	open := ui.openDropdown == id
	bg := theme.Control
	if ui.hover(box) || open {
		bg = theme.ControlHover
	}
	ui.C.FillRect(box, bg)
	ui.C.StrokeRect(box, theme.Border, 1)
	ui.C.TextLeft(box, labelFor(options, current), theme.Text)
	ui.C.Text(box.X+box.W-16, box.Y+box.H/2, "v", theme.TextDim)

	if ui.hover(box) && ui.In.MouseLeftClicked {
		if open {
			ui.openDropdown = ""
		} else {
			ui.openDropdown = id
			ui.dropdownScroll[id] = 0 // always open scrolled to the top
		}
	}
	if ui.openDropdown == id {
		visible := min(len(options), dropdownMaxRows)
		maxScroll := max(len(options)-visible, 0)
		ui.deferred = &deferredDropdown{
			id: id, control: box, current: current, options: options, write: write,
			list:    Rect{X: box.X, Y: box.Y + box.H, W: box.W, H: visible * rowHeight},
			scroll:  max(0, min(maxScroll, ui.dropdownScroll[id])),
			visible: visible,
		}
	}
}

// TextField is a single-line editable string. Click to focus; typed
// characters insert at the cursor; Backspace/Delete/Left/Right/Home/End
// edit it; clicking elsewhere or Enter/Tab drops focus. Selection isn't
// supported — good enough for the paths/short strings this launcher edits
// (a WAD/IWAD path, a hex fog colour).
func (ui *UI) TextField(id string, r Rect, label string, v *string) bool {
	ui.Label(Rect{X: r.X, Y: r.Y, W: labelWidth, H: r.H}, label, false)
	box := Rect{X: r.X + labelWidth, Y: r.Y, W: r.W - labelWidth, H: r.H}
	focused := ui.focusText == id

	if ui.In.MouseLeftClicked {
		if ui.hover(box) {
			ui.focusText = id
			ui.textCursor[id] = utf8.RuneCountInString(*v)
			focused = true
		} else if focused {
			ui.focusText = ""
			focused = false
		}
	}

	changed := false
	if focused {
		runes := []rune(*v)
		cur := max(0, min(len(runes), ui.textCursor[id]))
		for _, r := range ui.In.Typed {
			runes = append(runes[:cur], append([]rune{r}, runes[cur:]...)...)
			cur++
			changed = true
		}
		if ui.In.Pressed(window.MenuKeyBackspace) && cur > 0 {
			runes = append(runes[:cur-1], runes[cur:]...)
			cur--
			changed = true
		}
		if ui.In.Pressed(window.MenuKeyDelete) && cur < len(runes) {
			runes = append(runes[:cur], runes[cur+1:]...)
			changed = true
		}
		if ui.In.Pressed(window.MenuKeyLeft) && cur > 0 {
			cur--
		}
		if ui.In.Pressed(window.MenuKeyRight) && cur < len(runes) {
			cur++
		}
		if ui.In.Pressed(window.MenuKeyHome) {
			cur = 0
		}
		if ui.In.Pressed(window.MenuKeyEnd) {
			cur = len(runes)
		}
		if ui.In.Pressed(window.MenuKeyEnter) || ui.In.Pressed(window.MenuKeyTab) {
			ui.focusText = ""
			focused = false
		}
		ui.textCursor[id] = cur
		if changed {
			*v = string(runes)
		}
	}

	bg := theme.Control
	if focused {
		bg = theme.ControlHover
	}
	ui.C.FillRect(box, bg)
	ui.C.StrokeRect(box, theme.Border, 1)
	if focused {
		ui.C.StrokeRect(box, theme.Accent, 1)
	}
	ui.C.TextLeft(box, *v, theme.Text)
	if focused {
		cx := box.X + 8 + ui.textCursor[id]*glyphAdvance
		ui.C.FillRect(Rect{X: cx, Y: box.Y + 4, W: 1, H: box.H - 8}, theme.Accent)
	}
	return changed
}

// IntSlider bridges Slider (float64) to an *int field with a small,
// bounded range (e.g. Skill 1..5) — the drag math works in float64, and the
// result is rounded back into *v.
func (ui *UI) IntSlider(id string, r Rect, label string, v *int, min, max int, format string) bool {
	f := float64(*v)
	if changed := ui.Slider(id, r, label, &f, float64(min), float64(max), format); changed {
		*v = int(f + 0.5)
		return true
	}
	return false
}

// IntField is a TextField for an *int with too wide a range for a slider to
// be usable (window dimensions, damage scale, an uncapped frame-rate cap).
// The typed text is kept in a per-id draft buffer (UI.intDrafts) rather
// than re-derived from *v every frame, and only re-synced from *v while the
// field isn't focused — otherwise clearing the box to type a new number
// would be immediately overwritten back to the old value by this same
// frame's redraw. A draft that doesn't parse as an integer simply doesn't
// write back yet (so "" or a stray letter mid-edit doesn't clobber *v);
// the field's own text keeps whatever the user typed regardless.
func (ui *UI) IntField(id string, r Rect, label string, v *int) bool {
	if !ui.Focused(id) {
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

func clamp01(f float64) float64 {
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}
