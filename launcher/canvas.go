package launcher

import "twopointfive/imkit"

// The launcher draws onto imkit's canvas — a plain CPU RGBA buffer redrawn
// from scratch every frame and handed to render/vulkan's vanilla
// DrawFrame(pix), with clipping for the scrollable settings panel. Only the
// canvas is shared so far; the widgets (ui.go) are still the launcher's own.
type (
	Canvas = imkit.Canvas
	Rect   = imkit.Rect
)

const (
	glyphAdvance = imkit.GlyphAdvance
	glyphHeight  = imkit.GlyphHeight
)

var (
	NewCanvas = imkit.NewCanvas
	TextWidth = imkit.TextWidth
)
