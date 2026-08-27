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

// drawWeaponSprite bottom-anchors and horizontally centers sp, ignoring its
// own hotspot metadata (unlike blitSpriteImage) — a simple, robust
// convention that doesn't depend on exactly how each weapon graphic's
// offset was authored.
func (r *Renderer) drawWeaponSprite(sp *assets.RGBA, raiseOffset float64) {
	x := (r.Width - sp.Width) / 2
	y := r.Height - sp.Height + int(raiseOffset*float64(sp.Height+weaponDropPx))
	r.blit(sp, x, y, false)
}
