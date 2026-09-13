package launcher

import (
	"path/filepath"

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
	bar := Rect{0, 0, windowW, topBarH}
	c.FillRect(bar, theme.Panel)
	c.FillRect(Rect{0, topBarH - 1, windowW, 1}, theme.Border)
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
		r := Rect{x, y, chipW, chipH}
		if ui.Button(r, filepath.Base(path), style) {
			m.PickWAD(path)
		}
		x += chipW + chipGap
	}
	browseR := Rect{windowW - 140 - 16, y, 140, chipH}
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
	}
	labelY := y + chipH + 6
	ui.Label(Rect{16, labelY, windowW - 32, 16}, label, true)

	mapOptions := m.Maps
	mapCurrent := m.MapName
	if len(mapOptions) == 0 {
		mapOptions = []string{"(choose a WAD first)"}
		mapCurrent = mapOptions[0]
	}
	mapY := labelY + 16 + 8
	mapRow := Rect{16, mapY, windowW - 32, rowHeight}
	ui.Options("mapSelect", mapRow, "Level", mapCurrent, choices(mapOptions), func(sel string) {
		if len(m.Maps) > 0 {
			m.MapName = sel
		}
	})

	// Cartoon shading gets its own row up here rather than a line buried in
	// the scrollable settings list below: it's a whole-scene visual style
	// switch (see render/vulkan/shaders/worldblit.frag's outline pass), the
	// kind of choice worth seeing before you scroll past it, not after.
	cartoonRow := Rect{16, mapY + rowHeight + 8, windowW - 32, rowHeight}
	ui.Checkbox("cartoonShading", cartoonRow, "Cartoon Shading", &m.Cfg.CartoonShading)
	if m.Cfg.Renderer != config.RendererHardware {
		ui.Label(Rect{16 + labelWidth + 28, cartoonRow.Y, windowW - 32 - labelWidth - 28, rowHeight}, "(needs Renderer: Hardware, below)", true)
	}
}

func drawBottomBar(ui *UI, m *Model) {
	c := ui.C
	bar := Rect{0, windowH - bottomBarH, windowW, bottomBarH}
	c.FillRect(bar, theme.Panel)
	c.FillRect(Rect{0, windowH - bottomBarH, windowW, 1}, theme.Border)

	statusCol := theme.TextDim
	if m.StatusErr {
		statusCol = theme.Error
	}
	c.TextLeft(Rect{16, windowH - bottomBarH, windowW - 320, bottomBarH}, m.Status, statusCol)

	quitR := Rect{windowW - 260, windowH - bottomBarH + 12, 100, 32}
	if ui.Button(quitR, "QUIT", ButtonNormal) {
		m.Done, m.Ok = true, false
	}
	playR := Rect{windowW - 150, windowH - bottomBarH + 12, 134, 32}
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
	c.FillRect(Rect{r.X, r.Y + r.H - 1, r.W, 1}, theme.Border)

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
		if ui.Button(Rect{x, y, tabW, h}, tabTitles[sec], style) && m.ActiveTab != i {
			m.ActiveTab = i
			m.ScrollY = 0
		}
		x += tabW + gap
	}
}

func drawSettingsPanel(ui *UI, m *Model) {
	panel := Rect{0, topBarH, windowW, windowH - topBarH - bottomBarH}

	drawTabStrip(ui, m, Rect{panel.X, panel.Y, panel.W, tabBarH})

	if m.ActiveTab < 0 || m.ActiveTab >= len(sectionOrder) {
		m.ActiveTab = 0
	}
	section := sectionOrder[m.ActiveTab]

	viewport := Rect{panel.X, panel.Y + tabBarH, panel.W, panel.H - tabBarH}
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
		r := Rect{panelPad, y, viewport.W - 2*panelPad - scrollbarW, rowHeight}
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
		ui.C.FillRect(Rect{windowW - scrollbarW, viewport.Y, scrollbarW, trackH}, theme.Panel)
		ui.C.FillRect(Rect{windowW - scrollbarW + 2, thumbY, scrollbarW - 4, thumbH}, theme.ScrollThumb)
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
