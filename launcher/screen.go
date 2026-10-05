package launcher

import (
	"path/filepath"
	"strings"

	"twopointfive/config"
	"twopointfive/window"
)

// scrollSpeed converts one wheel "click" (GLFW reports ~1.0 per notch on
// most mice) into pixels of settings-panel scroll.
const scrollSpeed = 24.0

// Draw renders one whole frame from m into ui.C and applies this frame's
// input to m — the launcher's entire UI in one function, called once per
// frame by run.go between ui.Begin and ui.End.
func Draw(ui *UI, m *Model) {
	c := ui.C
	c.Fill(theme.Background)

	// Captured before drawSettingsPanel runs: a focused text field consumes
	// this same Enter press itself (to commit the edit and drop focus, see
	// TextField), so "was a field focused when this frame started" is what
	// decides whether Enter also means Play — not ui.focusText's value
	// afterward, which TextField has by then already cleared.
	editingText := ui.focusText != ""

	drawTopBar(ui, m)
	drawSettingsPanel(ui, m)
	drawBottomBar(ui, m)

	if ui.In.Pressed(window.MenuKeyEnter) && !m.Done && !editingText {
		// Enter anywhere = Play, same convenience a "default button" gives
		// a dialog — but still goes through the same validation as clicking
		// PLAY (tryPlay), so it can't skip the WAD/map check.
		tryPlay(m)
	}
}

func tryPlay(m *Model) {
	switch {
	case m.WAD == nil:
		m.Status, m.StatusErr = "Choose a WAD file first", true
	case m.MapName == "":
		m.Status, m.StatusErr = "Choose a level", true
	default:
		m.Done, m.Ok = true, true
	}
}

const (
	chipW, chipH, chipGap = 150, 24, 8
	maxQuickChips         = 5
)

func drawTopBar(ui *UI, m *Model) {
	c := ui.C
	bar := Rect{X: 0, Y: 0, W: windowW, H: topBarH}
	c.FillRect(bar, theme.Panel)
	c.FillRect(Rect{X: 0, Y: topBarH - 1, W: windowW, H: 1}, theme.Border)
	c.Text(16, 16, "TWOPOINTFIVE", theme.Accent)

	// WAD quick-pick chips + Browse…
	x, y := 16, 28
	for i, path := range m.QuickWADs {
		if i >= maxQuickChips {
			break
		}
		style := ButtonNormal
		if path == m.WADPath {
			style = ButtonSelected
		}
		r := Rect{X: x, Y: y, W: chipW, H: chipH}
		if ui.Button(r, filepath.Base(path), style) {
			m.PickWAD(path)
		}
		x += chipW + chipGap
	}
	browseR := Rect{X: windowW - 140 - 16, Y: y, W: 140, H: chipH}
	if ui.Button(browseR, "Browse...", ButtonNormal) {
		m.BrowseRequested = true
	}

	// Selected WAD + level dropdown.
	label := "(no WAD chosen — pick one above)"
	if m.WADPath != "" {
		label = m.WADPath
		if m.IWADPath != "" {
			label += "  [+ " + filepath.Base(m.IWADPath) + "]"
		}
		if m.Is3D {
			label += "  [3D]"
		}
	}
	labelY := y + chipH + 6
	ui.Label(Rect{X: 16, Y: labelY, W: windowW - 32, H: 16}, label, true)

	mapOptions := m.Maps
	mapCurrent := m.MapName
	if len(mapOptions) == 0 {
		mapOptions = []string{"(choose a WAD first)"}
		mapCurrent = mapOptions[0]
	}
	mapY := labelY + 16 + 8
	mapRow := Rect{X: 16, Y: mapY, W: windowW - 32, H: rowHeight}
	ui.Options("mapSelect", mapRow, "Level", mapCurrent, choices(mapOptions), func(sel string) {
		if len(m.Maps) > 0 {
			m.MapName = sel
		}
	})

	// Cartoon shading gets its own row up here rather than a line buried in
	// the scrollable settings list below: it's a whole-scene visual style
	// switch (see render/vulkan/shaders/worldblit.frag's outline pass), the
	// kind of choice worth seeing before you scroll past it, not after.
	cartoonRow := Rect{X: 16, Y: mapY + rowHeight + 8, W: windowW - 32, H: rowHeight}
	ui.Checkbox("cartoonShading", cartoonRow, "Cartoon Shading", &m.Cfg.CartoonShading)
}

func drawBottomBar(ui *UI, m *Model) {
	c := ui.C
	bar := Rect{X: 0, Y: windowH - bottomBarH, W: windowW, H: bottomBarH}
	c.FillRect(bar, theme.Panel)
	c.FillRect(Rect{X: 0, Y: windowH - bottomBarH, W: windowW, H: 1}, theme.Border)

	statusCol := theme.TextDim
	if m.StatusErr {
		statusCol = theme.Error
	}
	// The status line gets everything left of the QUIT button, wrapped onto
	// up to statusMaxLines lines (and clipped there), so a long message —
	// a WAD load error, say — never runs underneath QUIT / PLAY.
	statusR := Rect{X: 16, Y: windowH - bottomBarH, W: windowW - 260 - 16 - 16, H: bottomBarH}
	lines := wrapStatus(m.Status, (statusR.W-16)/glyphAdvance)
	prev := c.Clip(statusR)
	lineH := glyphHeight + 3
	y0 := statusR.Y + statusR.H/2 - (len(lines)-1)*lineH/2
	for i, l := range lines {
		c.Text(statusR.X+8, y0+i*lineH, l, statusCol)
	}
	c.SetClip(prev)

	quitR := Rect{X: windowW - 260, Y: windowH - bottomBarH + 12, W: 100, H: 32}
	if ui.Button(quitR, "QUIT", ButtonNormal) {
		m.Done, m.Ok = true, false
	}
	playR := Rect{X: windowW - 150, Y: windowH - bottomBarH + 12, W: 134, H: 32}
	if ui.Button(playR, "PLAY", ButtonPrimary) {
		tryPlay(m)
	}
}

// layoutTab is the settings panel's one layout computation for the rows of
// a single tab: each field of section, in order, at the Y it would sit
// starting from y0, advancing by the same row/gap constants regardless of
// whether draw is given. drawSettingsPanel calls it once with draw == nil
// just to measure content height (for scroll clamping) and once more to
// actually draw, so the two can't drift apart — there is only one place
// this math is written.
func layoutTab(y0 int, section string, draw func(y int, f *field)) int {
	y := y0
	rows := fieldsInSection(section)
	for i := range rows {
		if draw != nil {
			draw(y, &rows[i])
		}
		y += rowHeight + rowGap
	}
	return y - y0
}

// drawTabStrip draws the row of tab buttons across r and applies a click:
// switching tab resets the per-tab scroll offset.
func drawTabStrip(ui *UI, m *Model, r Rect) {
	c := ui.C
	c.FillRect(r, theme.Panel)
	c.FillRect(Rect{X: r.X, Y: r.Y + r.H - 1, W: r.W, H: 1}, theme.Border)

	n := len(sectionOrder)
	const gap = 4
	tabW := (r.W - 2*panelPad - gap*(n-1)) / n
	x := r.X + panelPad
	y := r.Y + 5
	h := r.H - 10
	for i, sec := range sectionOrder {
		style := ButtonNormal
		if i == m.ActiveTab {
			style = ButtonSelected
		}
		if ui.Button(Rect{X: x, Y: y, W: tabW, H: h}, tabTitles[sec], style) && m.ActiveTab != i {
			m.ActiveTab = i
			m.ScrollY = 0
		}
		x += tabW + gap
	}
}

func drawSettingsPanel(ui *UI, m *Model) {
	panel := Rect{X: 0, Y: topBarH, W: windowW, H: windowH - topBarH - bottomBarH}

	drawTabStrip(ui, m, Rect{X: panel.X, Y: panel.Y, W: panel.W, H: tabBarH})

	if m.ActiveTab < 0 || m.ActiveTab >= len(sectionOrder) {
		m.ActiveTab = 0
	}
	section := sectionOrder[m.ActiveTab]

	viewport := Rect{X: panel.X, Y: panel.Y + tabBarH, W: panel.W, H: panel.H - tabBarH}
	total := layoutTab(0, section, nil)

	maxScroll := float64(max(total-(viewport.H-rowGap), 0))
	if viewport.Contains(int(ui.In.MouseX), int(ui.In.MouseY)) && ui.In.ScrollY != 0 {
		m.ScrollY -= ui.In.ScrollY * scrollSpeed
	}
	if m.ScrollY < 0 {
		m.ScrollY = 0
	}
	if m.ScrollY > maxScroll {
		m.ScrollY = maxScroll
	}

	prevClip := ui.C.Clip(viewport)
	layoutTab(viewport.Y+rowGap-int(m.ScrollY), section, func(y int, f *field) {
		r := Rect{X: panelPad, Y: y, W: viewport.W - 2*panelPad - scrollbarW, H: rowHeight}
		drawField(ui, r, *f, &m.Cfg)
		// Only offer the hint for a row actually inside the viewport — a row
		// scrolled up under the tab strip mustn't tooltip on a tab-strip hover.
		if f.hint != "" && y >= viewport.Y && y+rowHeight <= viewport.Y+viewport.H {
			ui.Hint(r, f.hint)
		}
	})
	ui.C.SetClip(prevClip)

	if maxScroll > 0 {
		trackH := viewport.H
		thumbH := max(int(float64(trackH)*float64(viewport.H)/float64(total)), 20)
		thumbY := viewport.Y + int(m.ScrollY/maxScroll*float64(trackH-thumbH))
		ui.C.FillRect(Rect{X: windowW - scrollbarW, Y: viewport.Y, W: scrollbarW, H: trackH}, theme.Panel)
		ui.C.FillRect(Rect{X: windowW - scrollbarW + 2, Y: thumbY, W: scrollbarW - 4, H: thumbH}, theme.ScrollThumb)
	}
}

func drawField(ui *UI, r Rect, f field, cfg *config.Config) {
	switch f.kind {
	case kindCheckbox:
		ui.Checkbox(f.id, r, f.label, f.boolAt(cfg))
	case kindSlider:
		ui.Slider(f.id, r, f.label, f.floatAt(cfg), f.min, f.max, f.format)
	case kindIntSlider:
		ui.IntSlider(f.id, r, f.label, f.intAt(cfg), int(f.min), int(f.max), f.format)
	case kindIntField:
		ui.IntField(f.id, r, f.label, f.intAt(cfg))
	case kindOptions:
		v := f.stringAt(cfg)
		ui.Options(f.id, r, f.label, *v, f.options, func(sel string) { *v = sel })
	case kindTextField:
		ui.TextField(f.id, r, f.label, f.stringAt(cfg))
	}
}

// statusMaxLines is how many wrapped lines of status text fit the bottom
// bar (bottomBarH) — anything longer ends in "...".
const statusMaxLines = 3

// wrapStatus word-wraps s to at most width characters per line (the font
// is monospace), at most statusMaxLines lines, the last one truncated with
// "..." if the text doesn't fit. A single word longer than width is broken.
func wrapStatus(s string, width int) []string {
	if s == "" || width < 4 {
		return nil
	}
	// The launcher's bitmap font has no em/en dash (it draws a placeholder
	// box), and several engine errors use them.
	s = strings.NewReplacer("—", "-", "–", "-").Replace(s)
	var lines []string
	line := ""
	for _, w := range strings.Fields(s) {
		for len([]rune(w)) > width { // an over-long word: hard-break it
			if line != "" {
				lines = append(lines, line)
				line = ""
			}
			r := []rune(w)
			lines = append(lines, string(r[:width]))
			w = string(r[width:])
		}
		switch {
		case line == "":
			line = w
		case len([]rune(line))+1+len([]rune(w)) <= width:
			line += " " + w
		default:
			lines = append(lines, line)
			line = w
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	if len(lines) > statusMaxLines {
		last := []rune(lines[statusMaxLines-1])
		if len(last) > width-3 {
			last = last[:width-3]
		}
		lines = append(lines[:statusMaxLines-1], string(last)+"...")
	}
	return lines
}
