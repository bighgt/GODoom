// Package imkit is a small immediate-mode GUI kit: a CPU RGBA canvas, a
// per-frame input snapshot, and a set of widgets (buttons, checkboxes,
// sliders, text fields, dropdowns, tabs, trees, a draggable splitter,
// menus, a thumbnail grid, drag-and-drop). It is drawn into a plain
// image.RGBA and presented through render/vulkan's vanilla blit path — the
// same "just show this RGBA8 buffer" route the launcher and the game's
// vanilla lighting mode use, so the kit adds no new rendering code.
//
// It is deliberately close in spirit to the pre-game launcher's own kit
// (launcher/ui.go): flat rectangles, one monospace bitmap font, a dark
// neutral palette. cmd/editor is the first consumer; the launcher may be
// migrated onto it later.
package imkit

import (
	"image"
	"image/color"
	"image/draw"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

// glyph is the one font the kit draws with — a public-domain pre-rasterized
// 7x13 monospace face. GlyphAdvance/GlyphHeight mirror its metrics so
// layout code needn't import basicfont.
var glyph = basicfont.Face7x13

const (
	GlyphAdvance = 7
	GlyphHeight  = 13
)

// Rect is an integer screen-space rectangle, (X, Y) at the top-left.
type Rect struct{ X, Y, W, H int }

// Contains reports whether (px, py) falls inside r.
func (r Rect) Contains(px, py int) bool {
	return px >= r.X && px < r.X+r.W && py >= r.Y && py < r.Y+r.H
}

// Inset shrinks r by n on every side (negative n grows it).
func (r Rect) Inset(n int) Rect {
	return Rect{X: r.X + n, Y: r.Y + n, W: r.W - 2*n, H: r.H - 2*n}
}

// TextWidth is the pixel width of s in the kit's one font.
func TextWidth(s string) int { return len([]rune(s)) * GlyphAdvance }

// Canvas is one editor frame: a CPU RGBA buffer redrawn from scratch every
// frame and handed to render/vulkan's DrawFrame(pix). clip is the current
// clipping rect — every draw call is silently no-op'd outside it.
type Canvas struct {
	Img  *image.RGBA
	W, H int
	clip Rect
}

// NewCanvas allocates a w x h buffer.
func NewCanvas(w, h int) *Canvas {
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	return &Canvas{Img: image.NewRGBA(image.Rect(0, 0, w, h)), W: w, H: h, clip: Rect{0, 0, w, h}}
}

// Resize reallocates the buffer when w x h changes (the editor window is
// resizable, unlike the launcher's).
func (c *Canvas) Resize(w, h int) {
	if w == c.W && h == c.H {
		return
	}
	*c = *NewCanvas(w, h)
}

// Clip narrows further drawing to r intersected with the current clip and
// returns the previous clip, to restore via SetClip.
func (c *Canvas) Clip(r Rect) Rect {
	prev := c.clip
	c.clip = intersect(c.clip, r)
	return prev
}

// SetClip restores a clip rect previously returned by Clip.
func (c *Canvas) SetClip(r Rect) { c.clip = r }

// ClipRect reports the current clip rectangle.
func (c *Canvas) ClipRect() Rect { return c.clip }

func intersect(a, b Rect) Rect {
	x0, y0 := max(a.X, b.X), max(a.Y, b.Y)
	x1, y1 := min(a.X+a.W, b.X+b.W), min(a.Y+a.H, b.Y+b.H)
	if x1 < x0 {
		x1 = x0
	}
	if y1 < y0 {
		y1 = y0
	}
	return Rect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}
}

// Fill paints the whole canvas — the first call every frame.
func (c *Canvas) Fill(col color.RGBA) {
	draw.Draw(c.Img, c.Img.Bounds(), &image.Uniform{C: col}, image.Point{}, draw.Src)
}

// FillRect paints r (clipped) with col, alpha-blended.
func (c *Canvas) FillRect(r Rect, col color.RGBA) {
	cr := intersect(r, c.clip)
	if cr.W <= 0 || cr.H <= 0 {
		return
	}
	op := draw.Over
	if col.A == 255 {
		op = draw.Src
	}
	draw.Draw(c.Img, image.Rect(cr.X, cr.Y, cr.X+cr.W, cr.Y+cr.H), &image.Uniform{C: col}, image.Point{}, op)
}

// StrokeRect draws a t-pixel border around r.
func (c *Canvas) StrokeRect(r Rect, col color.RGBA, t int) {
	if t < 1 {
		t = 1
	}
	c.FillRect(Rect{r.X, r.Y, r.W, t}, col)
	c.FillRect(Rect{r.X, r.Y + r.H - t, r.W, t}, col)
	c.FillRect(Rect{r.X, r.Y, t, r.H}, col)
	c.FillRect(Rect{r.X + r.W - t, r.Y, t, r.H}, col)
}

// Pixel sets one clipped pixel (used by the 2D map view for thin lines).
func (c *Canvas) Pixel(x, y int, col color.RGBA) {
	if c.clip.Contains(x, y) {
		c.Img.SetRGBA(x, y, col)
	}
}

// Line draws a 1px Bresenham line, clipped.
func (c *Canvas) Line(x0, y0, x1, y1 int, col color.RGBA) {
	dx := abs(x1 - x0)
	dy := -abs(y1 - y0)
	sx, sy := 1, 1
	if x0 > x1 {
		sx = -1
	}
	if y0 > y1 {
		sy = -1
	}
	err := dx + dy
	for {
		c.Pixel(x0, y0, col)
		if x0 == x1 && y0 == y1 {
			return
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x0 += sx
		}
		if e2 <= dx {
			err += dx
			y0 += sy
		}
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// Blit copies src (a full RGBA image, e.g. the 3D pane's framebuffer or a
// texture thumbnail) into the canvas at dst, scaling with nearest-neighbour
// when the sizes differ. Clipped.
func (c *Canvas) Blit(dst Rect, src *image.RGBA) {
	if src == nil || dst.W <= 0 || dst.H <= 0 {
		return
	}
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	if sw == 0 || sh == 0 {
		return
	}
	cr := intersect(dst, c.clip)
	for y := cr.Y; y < cr.Y+cr.H; y++ {
		sy := sb.Min.Y + (y-dst.Y)*sh/dst.H
		if sy < sb.Min.Y || sy >= sb.Max.Y {
			continue
		}
		for x := cr.X; x < cr.X+cr.W; x++ {
			sx := sb.Min.X + (x-dst.X)*sw/dst.W
			if sx < sb.Min.X || sx >= sb.Max.X {
				continue
			}
			c.Img.SetRGBA(x, y, src.RGBAAt(sx, sy))
		}
	}
}

// Text draws s with (x, y) as the vertical centre of the line (baseline is
// y + GlyphHeight/2 - 3, tuned for Face7x13).
func (c *Canvas) Text(x, y int, s string, col color.RGBA) {
	if s == "" {
		return
	}
	d := &font.Drawer{
		Dst:  &clippedRGBA{img: c.Img, clip: c.clip},
		Src:  &image.Uniform{C: col},
		Face: glyph,
		Dot:  fixed.P(x, y+GlyphHeight/2-3),
	}
	d.DrawString(s)
}

// TextLeft draws s left-aligned in r with an 8px inset, vertically centred.
func (c *Canvas) TextLeft(r Rect, s string, col color.RGBA) {
	c.Text(r.X+8, r.Y+r.H/2, s, col)
}

// TextCenter draws s horizontally centred in r.
func (c *Canvas) TextCenter(r Rect, s string, col color.RGBA) {
	c.Text(r.X+(r.W-TextWidth(s))/2, r.Y+r.H/2, s, col)
}

// clippedRGBA bounds font.Drawer's Set calls to the clip rect.
type clippedRGBA struct {
	img  *image.RGBA
	clip Rect
}

func (c *clippedRGBA) ColorModel() color.Model { return c.img.ColorModel() }
func (c *clippedRGBA) Bounds() image.Rectangle { return c.img.Bounds() }
func (c *clippedRGBA) At(x, y int) color.Color { return c.img.At(x, y) }
func (c *clippedRGBA) Set(x, y int, col color.Color) {
	if c.clip.Contains(x, y) {
		c.img.Set(x, y, col)
	}
}
