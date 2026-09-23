package launcher

import (
	"image"
	"image/color"
	"image/draw"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

// glyph is the one font this whole package ever draws with — a public-
// domain, pre-rasterized monospace face (see theme.go's doc comment on
// staying visually unrelated to Doom). Advance/Height below mirror its
// metrics so layout code doesn't have to import basicfont itself.
var glyph = basicfont.Face7x13

const (
	glyphAdvance = 7
	glyphHeight  = 13
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

// Canvas is the whole launcher frame: a plain CPU RGBA buffer redrawn from
// scratch every frame and handed to render/vulkan's vanilla DrawFrame(pix)
// — the same "just blit an RGBA8 image" path the game's own vanilla
// lighting mode presents through, reused here instead of any new rendering
// code. clip is the current clipping rect (see BeginClip/EndClip in ui.go's
// scroll region) — every draw call is silently no-op'd outside it.
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

// Clip narrows all further drawing to r intersected with the current clip —
// used for the scrollable settings panel so a row scrolled half off the top
// doesn't paint over the fixed header above it. Returns the previous clip,
// to restore via SetClip.
func (c *Canvas) Clip(r Rect) Rect {
	prev := c.clip
	c.clip = intersect(c.clip, r)
	return prev
}

// SetClip restores a clip rect previously returned by Clip.
func (c *Canvas) SetClip(r Rect) { c.clip = r }

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

// FillRect paints r (clipped) with col.
func (c *Canvas) FillRect(r Rect, col color.RGBA) {
	cr := intersect(r, c.clip)
	if cr.W <= 0 || cr.H <= 0 {
		return
	}
	draw.Draw(c.Img, image.Rect(cr.X, cr.Y, cr.X+cr.W, cr.Y+cr.H), &image.Uniform{C: col}, image.Point{}, draw.Over)
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

// TextWidth is the pixel width of s set in the launcher's one font.
func TextWidth(s string) int { return len([]rune(s)) * glyphAdvance }

// Text draws s with its baseline at (x, y) — y is the text's vertical
// center, matching how ui.go centers a label inside a row; the actual
// baseline used is y + glyphHeight/2 - 3, tuned for Face7x13's ascent so a
// label visually centers in a rowHeight-tall row.
func (c *Canvas) Text(x, y int, s string, col color.RGBA) {
	if s == "" {
		return
	}
	d := &font.Drawer{
		Dst:  &clippedRGBA{img: c.Img, clip: c.clip},
		Src:  &image.Uniform{C: col},
		Face: glyph,
		Dot:  fixed.P(x, y+glyphHeight/2-3),
	}
	d.DrawString(s)
}

// TextLeft is Text at a row's standard left inset (panelPad/2), vertically
// centered in r.
func (c *Canvas) TextLeft(r Rect, s string, col color.RGBA) {
	c.Text(r.X+8, r.Y+r.H/2, s, col)
}

// clippedRGBA wraps an *image.RGBA so font.Drawer's Set calls (via its
// internal rasterizer) never paint outside clip — image/draw and
// golang.org/x/image/font both only need draw.Image's minimal interface
// (ColorModel, Bounds, At, Set).
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
