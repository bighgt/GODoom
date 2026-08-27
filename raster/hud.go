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
// Its glyphs are drawn sharp/unantialiased; smoothing comes entirely from
// the GPU sampler that later upscales this whole internal frame to the
// window's real size (see render/vulkan/texture.go), the same "hardware
// text smoothing" applied to the 3D view itself. Drawing the HUD directly
// into the low-resolution raster.Renderer.Pix buffer, rather than as a
// separate high-resolution overlay, is what makes one sampler enough for
// both — see game_design.txt section 7.
var hudFace = basicfont.Face7x13

// DrawHUD renders each of lines into the top-left corner of r.Pix, one per
// row, skipping empty strings (a convenient way for a caller to omit a
// line some frames without renumbering the rest). Call after Render so the
// text draws on top of the 3D view.
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
// and blits its opaque pixels onto r.Pix at (x, baselineY).
func (r *Renderer) drawHUDLine(line string, x, baselineY int) {
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

	oy := baselineY - ascent
	b := img.Bounds()
	for sy := b.Min.Y; sy < b.Max.Y; sy++ {
		dy := oy + sy
		if dy < 0 || dy >= r.Height {
			continue
		}
		for sx := b.Min.X; sx < b.Max.X; sx++ {
			c := img.RGBAAt(sx, sy)
			if c.A == 0 {
				continue // transparent background pixel: leave the 3D view showing
			}
			dx := x + sx
			if dx < 0 || dx >= r.Width {
				continue
			}
			i := (dy*r.Width + dx) * 4
			r.Pix[i], r.Pix[i+1], r.Pix[i+2], r.Pix[i+3] = c.R, c.G, c.B, c.A
		}
	}
}
