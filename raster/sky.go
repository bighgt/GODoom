package raster

import "math"

// skyFlatName is Doom's one hardcoded special flat name: a sector whose
// ceiling texture is exactly this gets the sky rendered instead of a
// normal textured flat — see drawCeilingSpan (renderer.go) and PrBoom's
// r_bsp.c, which checks "sector->ceilingpic == skyflatnum" the same way.
const skyFlatName = "F_SKY1"

// skyTextureName is the wall-format texture actually painted for the sky
// — despite being used for a *ceiling*, Doom's sky is stored and drawn as
// an ordinary wall texture. SKY1 is the one shareware/Episode 1 IWADs
// carry; later episodes' SKY2/SKY3 aren't handled — see game_design.txt.
const skyTextureName = "SKY1"

// skyAngularRepeats is how many times the sky texture's full width tiles
// across one complete 360° turn — 4, in the original engine (its
// ANGLETOSKYSHIFT constant, applied to a 32-bit angle against a 256-wide
// sky texture, works out to exactly this ratio): turning 90° scrolls
// through the whole texture once, a well-known Doom trivia fact and not
// this project's own invention.
const skyAngularRepeats = 4.0

// skyVScale is the vertical texels-per-screen-row rate the sky is drawn
// at — a fixed 1:1, chosen (rather than ported; PrBoom's own skyiscale
// wasn't tracked down to its exact fixed-point value) to keep the sky at
// a stable, plausible size regardless of window/internal resolution.
const skyVScale = 1.0

// drawSkySpan fills screen rows [yTop, yBottom) of column x with the sky
// texture, using Doom's actual technique: rather than being positioned in
// the 3D world at all (there is no "sky sector" out past the level's
// walls), it's painted directly from the camera's view angle, so turning
// scrolls it but walking never does — the classic "infinitely far away"
// sky look.
//
// Critically, both axes here use a mapping that's the same for every
// column and every row — u depends only on cam.Angle and x (not on the
// wall silhouette in this particular column), and v depends only on y and
// the camera's pitch-adjusted horizon (not on yTop/yBottom, this
// particular call's own visible range). An earlier version stretched the
// texture to fit each column's own [yTop, yBottom) locally, which varies
// wildly column to column depending on how much sky nearby wall
// silhouettes happen to expose — different columns ended up sampling the
// texture at different effective scales, warping what should be a flat
// backdrop into a fisheye-like bulge (reported as a "goldfish bowl"
// effect). A real sky has no depth to locally fit anything to; it should
// look identical in overlapping rows/columns no matter what's drawn
// around it, which a single shared mapping guarantees.
func (r *Renderer) drawSkySpan(x, yTop, yBottom int, cam Camera) {
	if yBottom <= yTop {
		return
	}
	tex, ok := r.textures.WallTexture(skyTextureName)
	if !ok {
		return
	}

	// This column's own view angle: cam.Angle plus how far this column
	// sits from screen center, in the same tangent-based projection every
	// other column in this renderer uses — mapped to a sky-texture column
	// via skyAngularRepeats.
	colAngle := cam.Angle + math.Atan2(float64(x)+0.5-float64(r.Width)/2, r.focal)
	u := colAngle / (2 * math.Pi) * float64(tex.Width) * skyAngularRepeats
	tx := wrapInt(int(math.Floor(u)), tex.Width)

	// Anchored to the texture's vertical center at the (pitch-shifted)
	// horizon row, so looking up/down scrolls the sky vertically the same
	// consistent way in every column.
	horizon := r.horizonY()
	for y := yTop; y < yBottom; y++ {
		v := float64(tex.Height)/2 + (float64(y)-horizon)*skyVScale
		ty := wrapInt(int(math.Floor(v)), tex.Height)
		r.setPixel(x, y, tex.At(tx, ty))
	}
}
