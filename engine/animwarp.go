package engine

import (
	"math"

	"twopointfive/assets"
)

// ANIMDEFS `warp` / `warp2` support.
//
// A true swimming-texture warp is a per-texel sample offset applied while
// the surface is sampled. This engine has two renderers (a CPU rasteriser
// and a GPU forward path) that sample textures in quite different places,
// so instead of threading a "warp this one" flag and the anim clock into
// both hot sampling loops, we bake the effect: at level load each warped
// texture is expanded into warpPhases pre-distorted copies, registered as a
// normal animation group, and the surface's live texture name is swapped
// between them each tic — the same "rewrite the name the renderer reads"
// mechanism flats/doors already use. Costs warpPhases x the texture's size
// in RAM per warped texture (a 64x64 flat -> 512 KB); warpDefLimit caps how
// many a hostile lump can demand.
const (
	warpPhases   = 32
	warpDefLimit = 24
)

// warpImage returns a sine-distorted copy of src for the given phase in
// [0,warpPhases). wide selects the larger warp2 displacement. Sampling is
// nearest-neighbour with wraparound, which keeps the result crisp and can't
// bleed colours across a palette the way bilinear would.
func warpImage(src *assets.RGBA, phase int, wide bool) *assets.RGBA {
	w, h := src.Width, src.Height
	if w <= 0 || h <= 0 || len(src.Pix) < w*h*4 {
		return src
	}
	ampX := float64(w) / 16
	ampY := float64(h) / 16
	perX := float64(w)
	perY := float64(h)
	if wide {
		ampX *= 2
		ampY *= 2
		perX *= 0.5
		perY *= 0.5
	}
	ampX = clampF(ampX, 1, 16)
	ampY = clampF(ampY, 1, 16)
	ph := float64(phase) / float64(warpPhases) * 2 * math.Pi

	dst := &assets.RGBA{
		Width: w, Height: h,
		OffsetX: src.OffsetX, OffsetY: src.OffsetY,
		TexelsPerUnit: src.TexelsPerUnit,
		Pix:           make([]byte, w*h*4),
	}
	if dst.TexelsPerUnit == 0 {
		dst.TexelsPerUnit = 1
	}
	for y := 0; y < h; y++ {
		soy := int(math.Round(ampY * math.Sin(2*math.Pi*float64(y)/perY+ph)))
		for x := 0; x < w; x++ {
			sox := int(math.Round(ampX * math.Sin(2*math.Pi*float64(x)/perX+ph)))
			sx := wrap(x+sox, w)
			sy := wrap(y+soy, h)
			si := (sy*w + sx) * 4
			di := (y*w + x) * 4
			copy(dst.Pix[di:di+4], src.Pix[si:si+4])
		}
	}
	return dst
}

func wrap(v, n int) int {
	v %= n
	if v < 0 {
		v += n
	}
	return v
}

func clampF(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
