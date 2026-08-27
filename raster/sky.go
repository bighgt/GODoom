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

// drawSkySpan fills screen rows [yTop, yBottom) of column x with the sky
// texture, using Doom's actual technique: rather than being positioned in
// the 3D world at all (there is no "sky sector" out past the level's
// walls), it's painted directly from the camera's view angle, so turning
// scrolls it but walking never does — the classic "infinitely far away"
// sky look. Vertically, since the sky has no real depth to
// perspective-project against, it's simply stretched to fill whatever
// range of the screen is asking for it.
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

	rangeH := maxInt(1, yBottom-yTop)
	for y := yTop; y < yBottom; y++ {
		ty := clampInt((y-yTop)*tex.Height/rangeH, 0, tex.Height-1)
		r.setPixel(x, y, tex.At(tx, ty))
	}
}
