package raster

import "fmt"

// weaponDropPx is how far below its resting position the weapon sprite
// travels while fully lowered — comfortably more than any vanilla weapon
// sprite's height, so "fully lowered" reads as fully offscreen.
const weaponDropPx = 300

// DrawWeapon draws the current weapon's viewmodel sprite at gunFrame
// (e.g. 'A' -> spritePrefix+"A0"), bottom-anchored and horizontally
// centered by its own pixel dimensions, and — when hasFlash is true — the
// muzzle-flash overlay at flashFrame, positioned *relative to the gun*.
//
// Both frame letters come from engine.Game.currentSprite, which drives
// them off the real per-weapon animation timing ported from id's info.c
// (see engine/weapons.go) — this function only draws whatever frame it's
// told to. raiseOffset is 0 (fully raised/ready) to 1 (fully lowered/
// offscreen) — see engine.WeaponState.Offset.
//
// The flash's placement deliberately does NOT use the general
// hotspot-relative blit (blit.go's honorOffset) the status bar uses:
// Doom's weapon-viewmodel sprites carry huge LeftOffset/TopOffset values
// (e.g. -126 on a 57px-wide image) because the original engine draws them
// through a pseudo-3D projection formula, not a simple 2D blit — applying
// them as a simple hotspot offset threw the sprite badly off-center.
// What those offsets still encode correctly is each graphic's position
// *relative to the others for the same weapon* (they're all authored
// against one shared origin), so the flash is placed at the gun's own
// position plus the two sprites' offset delta — the same relative
// alignment the original data intends, without needing to reproduce its
// absolute projection math.
func (r *Renderer) DrawWeapon(spritePrefix string, gunFrame byte, flashPrefix string, flashFrame byte, hasFlash bool, raiseOffset float64, sectorLight int16) {
	gunSp, ok := r.textures.Sprite(spriteName(spritePrefix, gunFrame))
	if !ok {
		return
	}
	// All layout is in the HUD's 320x200 logical space; a hi-res override
	// (tpu==4) has the same logical footprint as its 1x original. The blit
	// calls pass weaponSpace=true, so the viewmodel is sized by hudFit alone
	// (fills the screen height) and the config hudScale — which sizes the
	// status bar — does not shrink or grow it.
	gunTPU := tpu(gunSp)
	gunW := float64(gunSp.Width) / gunTPU
	gunH := float64(gunSp.Height) / gunTPU
	gunX := int((hudLogicalW - gunW) / 2)
	gunY := int(hudLogicalH - gunH + raiseOffset*(gunH+weaponDropPx))
	// The held weapon dims with the room it's in — id shades a psprite with
	// spritelights[MAXLIGHTSCALE-1], the bright end of its sector's
	// scalelight (see pspriteShade). The muzzle flash overlay stays full
	// bright (it's an FF_FULLBRIGHT sprite that lights the gun, not lit by it).
	r.blit(gunSp, gunX, gunY, false, pspriteShade(sectorLight, false), true) // weapon transform (ignores hudScale)

	if !hasFlash {
		return
	}
	flashSp, ok := r.textures.Sprite(spriteName(flashPrefix, flashFrame))
	if !ok {
		return
	}
	flashTPU := tpu(flashSp)
	flashX := gunX + int(float64(gunSp.OffsetX)/gunTPU) - int(float64(flashSp.OffsetX)/flashTPU)
	flashY := gunY + int(float64(gunSp.OffsetY)/gunTPU) - int(float64(flashSp.OffsetY)/flashTPU)
	r.blit(flashSp, flashX, flashY, false, 256, true)
}

func spriteName(prefix string, frame byte) string {
	return fmt.Sprintf("%s%c0", prefix, frame)
}
