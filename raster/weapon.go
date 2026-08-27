package raster

import "twopointfive/assets"

// weaponDropPx is how far below its resting position the weapon sprite
// travels while fully lowered — comfortably more than any vanilla weapon
// sprite's height, so "fully lowered" reads as fully offscreen.
const weaponDropPx = 300

// DrawWeapon draws the current weapon's viewmodel sprite, and — while
// firing — its recoil frame and muzzle flash, bottom-anchored and
// horizontally centered. raiseOffset is 0 (fully raised/resting, its
// bottom edge flush with the screen's bottom row, so the status bar drawn
// afterward naturally covers its lowest few rows the way vanilla Doom's
// status bar does) to 1 (fully lowered, offscreen) — see
// engine.WeaponState.Offset, driven by the raise/lower/fire animation in
// engine/weapons.go. Missing recoil-frame lumps fall back to the ready
// frame, since not every weapon's WAD data has a distinct one.
func (r *Renderer) DrawWeapon(spritePrefix, flashPrefix string, firing bool, raiseOffset float64) {
	readyName := spritePrefix + "A0"

	if !firing {
		if sp, ok := r.textures.Sprite(readyName); ok {
			r.drawWeaponSprite(sp, raiseOffset)
		}
		return
	}

	if sp, ok := r.textures.Sprite(spritePrefix + "B0"); ok {
		r.drawWeaponSprite(sp, raiseOffset)
	} else if sp, ok := r.textures.Sprite(readyName); ok {
		r.drawWeaponSprite(sp, raiseOffset)
	}
	if flashPrefix != "" {
		if sp, ok := r.textures.Sprite(flashPrefix + "A0"); ok {
			r.drawWeaponSprite(sp, raiseOffset)
		}
	}
}

func (r *Renderer) drawWeaponSprite(sp *assets.RGBA, raiseOffset float64) {
	x := (r.Width - sp.Width) / 2
	y := r.Height - sp.Height + int(raiseOffset*float64(sp.Height+weaponDropPx))
	r.blitRaw(sp, x, y)
}

// blitRaw copies sp's opaque pixels onto r.Pix at (x, y) directly — unlike
// blitSpriteImage, it ignores the source patch's own offset metadata,
// which is what lets DrawWeapon use a simple, robust bottom-anchor
// convention instead of depending on exactly how each weapon graphic's
// hotspot was authored.
func (r *Renderer) blitRaw(sp *assets.RGBA, x, y int) {
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
