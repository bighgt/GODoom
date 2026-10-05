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
// Its glyphs are drawn sharp/unantialiased into the overlay buffer at a
// small whole-number scale (debugTextScale), so the debug text stays crisp
// at any render resolution (matching the 3D view's deliberate pixel look).
var hudFace = basicfont.Face7x13

// DrawHUD renders each of lines into the top-left corner of the overlay
// buffer, one per row, skipping empty strings (a convenient way for a
// caller to omit a line some frames without renumbering the rest). Call
// after Render so the text draws on top of the 3D view.
func (r *Renderer) DrawHUD(lines []string) {
	k := r.debugTextScale()
	marginX, marginY := 4*k, 4*k
	lineHeight := (hudFace.Metrics().Height.Ceil() + 1) * k

	row := 0
	for _, line := range lines {
		if line == "" {
			continue
		}
		r.drawHUDLine(line, marginX, marginY+row*lineHeight, k)
		row++
	}
}

// debugTextScale is the whole-number pixel scale the debug text is drawn
// at: 1 below 1080 rows, then one more step per 540 rows (2 at 1080p and
// 1440p, 4 at 2160p). The text used to ride the status bar's 320x200 fit
// (~5x at 1080p), which made the overlay's many rows huge; its own small
// integer scale keeps it compact and the bitmap font crisp.
func (r *Renderer) debugTextScale() int {
	return max(1, r.Height/540)
}

// drawHUDLine rasterizes one line of text into a just-large-enough
// temporary image (via golang.org/x/image/font's glyph-by-glyph drawer)
// then scales its opaque pixels by the integer k into the overlay buffer,
// with the line's top-left at render pixel (x, y) — anchored to the
// framebuffer's own top-left corner, not the bottom-anchored / centered
// plane the status bar and weapon use.
func (r *Renderer) drawHUDLine(line string, x, y, k int) {
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

	dx0, dy0 := x, y
	dw, dh := width*k, height*k
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
