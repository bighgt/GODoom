package raster

import (
	"image"
	"image/color"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

// hudFace is a small, fixed bitmap font (7x13 pixels/glyph) from the Go
// standard extended library — no font file to ship, no license to track.
// Its glyphs are drawn sharp/unantialiased into the 320x200 overlay buffer;
// CompositeOverlay then scales that up onto the frame with nearest
// sampling, so the debug text stays crisp and chunky at any render
// resolution (matching the 3D view's deliberate pixel look).
var hudFace = basicfont.Face7x13

// DrawHUD renders each of lines into the top-left corner of the overlay
// buffer, one per row, skipping empty strings (a convenient way for a
// caller to omit a line some frames without renumbering the rest). Call
// after Render so the text draws on top of the 3D view.
func (r *Renderer) DrawHUD(lines []string) {
	const marginX, marginY = 4, 4
	lineHeight := hudFace.Metrics().Height.Ceil() + 2
	ascent := hudFace.Metrics().Ascent.Ceil()

	row := 0
	for _, line := range lines {
		if line == "" {
			continue
		}
		r.drawHUDLine(line, marginX, marginY+row*lineHeight+ascent)
		row++
	}
}

// drawHUDLine rasterizes one line of text into a just-large-enough
// temporary image (via golang.org/x/image/font's glyph-by-glyph drawer)
// then nearest-scales its opaque pixels into the overlay buffer. It maps
// (logicalX, logicalBaselineY) from the HUD's 320x200 space to render
// pixels at r.hudScale (so the debug overlay halves above 720p with the
// rest of the HUD) but anchors to the framebuffer's own top-left corner,
// not the bottom-anchored / centered plane the status bar and weapon use.
func (r *Renderer) drawHUDLine(line string, logicalX, logicalBaselineY int) {
	width := font.MeasureString(hudFace, line).Ceil()
	height := hudFace.Metrics().Height.Ceil()
	if width <= 0 || height <= 0 {
		return
	}

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	ascent := hudFace.Metrics().Ascent.Ceil()
	d := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(color.White),
		Face: hudFace,
		Dot:  fixed.P(0, ascent),
	}
	d.DrawString(line)

	s := r.hudScale
	dx0 := int(float64(logicalX)*s + 0.5)
	dy0 := int(float64(logicalBaselineY-ascent)*s + 0.5)
	dw := int(float64(width)*s + 0.5)
	dh := int(float64(height)*s + 0.5)
	if dw < 1 || dh < 1 {
		return
	}
	r.markOverlay(dy0, dy0+dh)
	for dy := 0; dy < dh; dy++ {
		py := dy0 + dy
		if py < 0 || py >= r.Height {
			continue
		}
		sy := dy * height / dh
		dstRow := py * r.Width * 4
		for dx := 0; dx < dw; dx++ {
			px := dx0 + dx
			if px < 0 || px >= r.Width {
				continue
			}
			c := img.RGBAAt(dx*width/dw, sy)
			if c.A == 0 {
				continue // transparent background pixel: leave the view showing
			}
			i := dstRow + px*4
			r.overlay[i], r.overlay[i+1], r.overlay[i+2], r.overlay[i+3] = c.R, c.G, c.B, c.A
		}
	}
}
