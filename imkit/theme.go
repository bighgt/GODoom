package imkit

import "image/color"

func hex(rgb uint32) color.RGBA {
	return color.RGBA{R: byte(rgb >> 16), G: byte(rgb >> 8), B: byte(rgb), A: 255}
}

func hexA(rgb uint32, a byte) color.RGBA {
	return color.RGBA{R: byte(rgb >> 16), G: byte(rgb >> 8), B: byte(rgb), A: a}
}

// Theme is the kit's flat dark palette. Every widget draws from it, so a
// re-skin is a one-place change. Exported and mutable so an app can tweak
// it before the first frame.
var Theme = struct {
	Background   color.RGBA
	Panel        color.RGBA // toolbar / side-panel background
	PanelAlt     color.RGBA // alternating list rows
	Border       color.RGBA
	Text         color.RGBA
	TextDim      color.RGBA
	Accent       color.RGBA // focus ring, selection, active fill
	AccentDim    color.RGBA // hover on an accent element
	Control      color.RGBA // widget body
	ControlHover color.RGBA
	Primary      color.RGBA // primary button (Save / Play)
	PrimaryHover color.RGBA
	Danger       color.RGBA
	Warn         color.RGBA
	ScrollThumb  color.RGBA
	Overlay      color.RGBA // dim behind a modal / menu
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
	Primary:      hex(0x3ea86e),
	PrimaryHover: hex(0x49c47f),
	Danger:       hex(0xe0665a),
	Warn:         hex(0xd9a54a),
	ScrollThumb:  hex(0x50505e),
	Overlay:      hexA(0x000000, 140),
}

// LabelWidth is the fixed left column a labelled widget (Checkbox, Slider,
// TextField, Options) reserves for its label; the control fills the rest of
// the row rect. An app sets it once to fit its inspector panel width.
var LabelWidth = 116
