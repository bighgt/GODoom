// Package launcher is the pre-game menu: pick a WAD and a level, edit every
// config.json setting, then hand off to cmd/engine's normal load path. It's
// drawn with the engine's own window + presentation stack (see run.go) but
// is otherwise fully decoupled from Doom — no WAD assets, no game fonts, no
// game colours. Deliberately plain: flat rectangles, one monospace bitmap
// font, a dark neutral palette. See the "vivid-gliding-river" plan doc.
package launcher

import "image/color"

// hex is a small color.RGBA literal helper — 0xRRGGBB, always opaque.
func hex(rgb uint32) color.RGBA {
	return color.RGBA{R: byte(rgb >> 16), G: byte(rgb >> 8), B: byte(rgb), A: 255}
}

// hexA is hex with an explicit alpha (0-255).
func hexA(rgb uint32, a byte) color.RGBA {
	return color.RGBA{R: byte(rgb >> 16), G: byte(rgb >> 8), B: byte(rgb), A: a}
}

// Theme is a flat, dark, neutral palette — deliberately nothing like Doom's
// browns/reds/gold: a slate background, a single blue accent for focus/
// selection, plain text. Every widget in ui.go draws from this table, so
// re-theming later is a one-file change.
var theme = struct {
	Background   color.RGBA // window background
	Panel        color.RGBA // section/card background
	PanelAlt     color.RGBA // alternating row background in the settings list
	Border       color.RGBA
	Text         color.RGBA
	TextDim      color.RGBA // labels, hints
	Accent       color.RGBA // focus ring, active slider fill, selected row
	AccentDim    color.RGBA // hover
	Control      color.RGBA // checkbox/slider/dropdown/textfield body
	ControlHover color.RGBA
	Play         color.RGBA // Play button
	PlayHover    color.RGBA
	Quit         color.RGBA // Quit / secondary button
	QuitHover    color.RGBA
	Error        color.RGBA // validation text
	ScrollThumb  color.RGBA
}{
	Background:   hex(0x1e1e24),
	Panel:        hex(0x262630),
	PanelAlt:     hex(0x2b2b36),
	Border:       hex(0x3a3a46),
	Text:         hex(0xe8e8ec),
	TextDim:      hex(0x9a9aa6),
	Accent:       hex(0x4a90d9),
	AccentDim:    hex(0x3a6a99),
	Control:      hex(0x33333f),
	ControlHover: hex(0x3d3d4a),
	Play:         hex(0x3ea86e),
	PlayHover:    hex(0x49c47f),
	Quit:         hex(0x4a4a56),
	QuitHover:    hex(0x585865),
	Error:        hex(0xe0665a),
	ScrollThumb:  hex(0x50505e),
}

// Layout constants shared across screen.go/ui.go.
const (
	rowHeight     = 28
	rowGap        = 6
	sectionGap    = 18
	panelPad      = 14
	labelWidth    = 220 // fixed left column for a row's label, control fills the rest
	scrollbarW    = 10
	windowW       = 900
	windowH       = 700
	topBarH       = 96
	bottomBarH    = 56
)
