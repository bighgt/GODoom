package raster

import (
	"fmt"

	"twopointfive/assets"
)

// weaponDropPx is how far below its resting position the weapon sprite
// travels while fully lowered — comfortably more than any vanilla weapon
// sprite's height, so "fully lowered" reads as fully offscreen.
const weaponDropPx = 300

// DrawWeapon draws the current weapon's viewmodel sprite at gunFrame
// (e.g. 'A' -> spritePrefix+"A0"), and — when hasFlash is true — the
// muzzle-flash overlay at flashFrame, bottom-anchored and horizontally
// centered. Both frame letters come from engine.Game.currentSprite, which
// drives them off the real per-weapon animation timing ported from id's
// info.c (see engine/weapons.go) — this function only draws whatever
// frame it's told to. raiseOffset is 0 (fully raised/resting, its bottom
// edge flush with the screen's bottom row, so the status bar drawn
// afterward naturally covers its lowest few rows the way vanilla Doom's
// status bar does) to 1 (fully lowered, offscreen) — see
// engine.WeaponState.Offset.
func (r *Renderer) DrawWeapon(spritePrefix string, gunFrame byte, flashPrefix string, flashFrame byte, hasFlash bool, raiseOffset float64) {
	if sp, ok := r.textures.Sprite(spriteName(spritePrefix, gunFrame)); ok {
		r.drawWeaponSprite(sp, raiseOffset)
	}
	if hasFlash {
		if sp, ok := r.textures.Sprite(spriteName(flashPrefix, flashFrame)); ok {
			r.drawWeaponSprite(sp, raiseOffset)
		}
	}
}

func spriteName(prefix string, frame byte) string {
	return fmt.Sprintf("%s%c0", prefix, frame)
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
