package raster

import "twopointfive/assets"

// tpu is sp.TexelsPerUnit with a safe default of 1 (a WAD-native sprite
// with the field left unset).
func tpu(sp *assets.RGBA) float64 {
	if sp.TexelsPerUnit > 0 {
		return sp.TexelsPerUnit
	}
	return 1
}

// blit draws sp into the overlay buffer at (logicalX, logicalY) — a
// position in the HUD's 320x200 authoring space — scaled to the render
// resolution and centered. weaponSpace picks the weapon transform (fills
// the screen height, ignoring the user's hudScale) over the status-bar
// transform (hudScale-sized); see hudRect. The sprite's own extra
// resolution is respected: a hi-res override (sp.TexelsPerUnit == 4)
// occupies the same *logical* footprint as its 1x original but is sampled
// at full detail, so the HUD reads sharper without changing layout.
//
// If honorOffset is true, sp's hotspot (OffsetX/OffsetY, in the sprite's
// own pixel space) is subtracted first, in logical units — the V_DrawPatch
// convention every HUD/status-bar graphic uses. DrawWeapon passes false: it
// places the viewmodel by its own bottom-anchored/centered rule instead.
//
// shade is a 0..256 brightness multiplier applied per texel (c*shade>>8);
// 256 leaves the sprite untouched. DrawWeapon uses it to dim the held
// weapon by the player's sector light (the status bar and HUD always pass
// 256 — they're pure UI).
//
// Vanilla graphics are opaque-or-transparent, so transparent source texels
// are simply skipped. Nearest sampling keeps the pixel-art look.
func (r *Renderer) blit(sp *assets.RGBA, logicalX, logicalY int, honorOffset bool, shade uint32, weaponSpace bool) {
	tpu := sp.TexelsPerUnit
	if tpu <= 0 {
		tpu = 1
	}
	dim := shade < 256

	lx, ly := float64(logicalX), float64(logicalY)
	if honorOffset {
		lx -= float64(sp.OffsetX) / tpu
		ly -= float64(sp.OffsetY) / tpu
	}

	// Logical footprint = the sprite's size in 1x (map/HUD) units.
	dx0, dy0, dw, dh := r.hudRect(lx, ly, float64(sp.Width)/tpu, float64(sp.Height)/tpu, weaponSpace)
	if dw < 1 || dh < 1 {
		return
	}
	r.markOverlay(dy0, dy0+dh)

	for dy := 0; dy < dh; dy++ {
		py := dy0 + dy
		if py < 0 || py >= r.Height {
			continue
		}
		sy := dy * sp.Height / dh
		dstRow := py * r.Width * 4
		for dx := 0; dx < dw; dx++ {
			px := dx0 + dx
			if px < 0 || px >= r.Width {
				continue
			}
			c := sp.At(dx*sp.Width/dw, sy)
			if c[3] == 0 {
				continue
			}
			i := dstRow + px*4
			if dim {
				r.overlay[i] = byte(uint32(c[0]) * shade >> 8)
				r.overlay[i+1] = byte(uint32(c[1]) * shade >> 8)
				r.overlay[i+2] = byte(uint32(c[2]) * shade >> 8)
				r.overlay[i+3] = c[3]
			} else {
				r.overlay[i], r.overlay[i+1], r.overlay[i+2], r.overlay[i+3] = c[0], c[1], c[2], c[3]
			}
		}
	}
}
