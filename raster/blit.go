package raster

import "twopointfive/assets"

// blit copies sp's opaque pixels onto r.Pix at (x, y), skipping fully
// transparent source pixels and anything out of bounds. If honorOffset is
// true, sp's own hotspot (OffsetX, OffsetY — see wad.Patch) is subtracted
// from (x, y) first, matching the original engine's V_DrawPatch convention
// (used for every HUD/status-bar graphic, see DrawStatusBar); DrawWeapon
// passes false since it uses its own simpler bottom-anchored/centered
// placement instead of each weapon sprite's individual hotspot — see its
// own doc comment for why. This is the one shared pixel-copy loop every
// sprite-drawing method in this package (status bar, weapon viewmodel)
// is built on.
func (r *Renderer) blit(sp *assets.RGBA, x, y int, honorOffset bool) {
	if honorOffset {
		x -= sp.OffsetX
		y -= sp.OffsetY
	}
	for sy := 0; sy < sp.Height; sy++ {
		dy := y + sy
		if dy < 0 || dy >= r.Height {
			continue
		}
		for sx := 0; sx < sp.Width; sx++ {
			dx := x + sx
			if dx < 0 || dx >= r.Width {
				continue
			}
			c := sp.At(sx, sy)
			if c[3] == 0 {
				continue
			}
			i := (dy*r.Width + dx) * 4
			r.Pix[i], r.Pix[i+1], r.Pix[i+2], r.Pix[i+3] = c[0], c[1], c[2], c[3]
		}
	}
}
