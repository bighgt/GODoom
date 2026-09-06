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

	drawTopBar(ui, m)
	drawSettingsPanel(ui, m)
	drawBottomBar(ui, m)

	if ui.In.Pressed(window.MenuKeyEnter) && !m.Done {
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
	ui.Label(Rect{16, y + chipH + 6, windowW - 32, 16}, label, true)

	mapOptions := m.Maps
	mapCurrent := m.MapName
	if len(mapOptions) == 0 {
		mapOptions = []string{"(choose a WAD first)"}
		mapCurrent = mapOptions[0]
	}
	mapRow := Rect{16, y + chipH + 26, windowW - 32, rowHeight}
	ui.Options("mapSelect", mapRow, "Level", mapCurrent, mapOptions, func(sel string) {
		if len(m.Maps) > 0 {
			m.MapName = sel
		}
	})
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

func drawSettingsPanel(ui *UI, m *Model) {
	viewport := Rect{0, topBarH, windowW, windowH - topBarH - bottomBarH}
	total := settingsContentHeight()

	maxScroll := float64(max(total-viewport.H, 0))
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
	y := viewport.Y - int(m.ScrollY)
	for _, sec := range sectionOrder {
		rows := fieldsInSection(sec)
		if len(rows) == 0 {
			continue
		}
		ui.SectionHeader(Rect{panelPad, y, viewport.W - 2*panelPad, 24}, sec)
		y += 24 + rowGap
		for _, f := range rows {
			r := Rect{panelPad, y, viewport.W - 2*panelPad - scrollbarW, rowHeight}
			drawField(ui, r, f, &m.Cfg)
			y += rowHeight + rowGap
		}
		y += sectionGap
	}
	ui.C.SetClip(prevClip)

	if maxScroll > 0 {
		trackH := viewport.H
		thumbH := max(int(float64(trackH)*float64(viewport.H)/float64(total)), 20)
		thumbY := viewport.Y + int(m.ScrollY/maxScroll*float64(trackH-thumbH))
		ui.C.FillRect(Rect{windowW - scrollbarW, viewport.Y, scrollbarW, trackH}, theme.Panel)
		ui.C.FillRect(Rect{windowW - scrollbarW + 2, thumbY, scrollbarW - 4, thumbH}, theme.ScrollThumb)
	}
}

// settingsContentHeight mirrors drawSettingsPanel's own layout math exactly
// (same header/row/gap constants) so scroll clamping matches what's
// actually drawn.
func settingsContentHeight() int {
	h := 0
	for _, sec := range sectionOrder {
		rows := fieldsInSection(sec)
		if len(rows) == 0 {
			continue
		}
		h += 24 + rowGap
		h += len(rows) * (rowHeight + rowGap)
		h += sectionGap
	}
	return h
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
