package raster

// weaponDropPx is how far below its resting position the weapon sprite
// travels while fully lowered — comfortably more than any vanilla weapon
// sprite's height, so "fully lowered" reads as fully offscreen.
const weaponDropPx = 300

// weaponBottomOverlap pushes the gun's resting position slightly further
// down than its own bounding box strictly requires, so it hangs a little
// off the bottom of the screen instead of sitting exactly flush with it.
// Two things otherwise open a visible gap under the gun there: many
// weapon sprites (especially third-party HD packs, which don't all agree
// on how much transparent padding to leave below the drawn pixels) don't
// actually reach the bottom row of their own bounding box; and
// engine.Game.weaponBob's y component lifts the gun UP from its resting
// position while walking, which would crack a gap open on its own even
// with a perfectly tight sprite. Comfortably bigger than weaponBobMaxY so
// the bob's rise never uncovers it — the extra pixels are simply clipped
// by blit's normal off-screen check, the same as the lowered-weapon
// travel below.
const weaponBottomOverlap = 20

// DrawWeapon draws the current weapon's viewmodel sprite at gunFrame (e.g.
// 'A' -> spritePrefix+"A0") and — when hasFlash is true — the muzzle-flash
// overlay at flashFrame, positioned *relative to the gun*.
//
// Both frame letters come from engine.Game.currentSprite, which drives
// them off the real per-weapon animation timing ported from id's info.c
// (see engine/weapons.go) — this function only draws whatever frame it's
// told to. raiseOffset is 0 (fully raised/ready) to 1 (fully lowered/
// offscreen) — see engine.WeaponState.Offset. bobX/bobY/bobScale are the
// walking sway/bob/toward-camera-and-back motion, all in the same HUD
// pixel space — see engine.Game.weaponBob — applied to the gun only; the
// flash then rides along with it (in position;
// it stays its own native size) since its own position is computed
// relative to the gun's.
//
// Positioning, both the gun's own place on screen and the flash's relative
// to it, deliberately does NOT use the general hotspot-relative blit
// (blit.go's honorOffset) the status bar uses: Doom's weapon-viewmodel
// sprites carry huge LeftOffset/TopOffset values (e.g. -126 on a 57px-wide
// image) because the original engine draws them through a pseudo-3D
// projection formula, not a simple 2D blit — applying them as a simple
// hotspot offset threw the sprite badly off-center. What those offsets
// still encode correctly is each graphic's position *relative to the
// others for the same weapon* (every frame of a weapon — resting pose,
// each recoil frame, the flash overlay — is authored against one shared
// origin point, often off-canvas). So: the RESTING pose ('A') is centred
// horizontally and bottom-anchored by its own canvas size, same as always;
// every OTHER frame (a different fire-animation frame, or the flash) is
// placed at that same resting position PLUS the two frames' own offset
// delta, not independently re-centered by its own canvas. A hand-painted
// animation's frames don't all crop their canvas the same way frame to
// frame (a recoiling barrel needs a taller canvas at full extension than
// at rest, say — this project's "Smooth HD Weapons" rocket launcher pack
// varies noticeably) — self-centering each one by its own bounding box
// makes the gun visibly swell and drift through the animation instead of
// recoiling in place around one fixed point. For a pack whose frames DO
// share one canvas size and offset, this reduces to exactly the old
// independently-self-centered position (the delta is zero) — no visual
// change.
func (r *Renderer) DrawWeapon(spritePrefix string, gunFrame byte, flashPrefix string, flashFrame byte, hasFlash bool, raiseOffset, bobX, bobY, bobScale float64, sectorLight int16) {
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

	// refSp is the resting pose ('A') — reduces to gunSp itself (so the
	// math below is a no-op) when the active frame already IS 'A', which
	// is most of the time (every idle moment, and every fire sequence's
	// own first frame).
	refSp, refTPU := gunSp, gunTPU
	if gunFrame != 'A' {
		if rs, ok := r.textures.Sprite(spriteName(spritePrefix, 'A')); ok {
			refSp, refTPU = rs, tpu(rs)
		}
	}
	// bobScale (the scale component of engine.Game.weaponBob) is applied
	// to the resting footprint BEFORE centering it, so growing/shrinking
	// it for the toward/away-from-camera motion keeps it centered
	// horizontally and anchored to the same resting baseline rather than
	// drifting off one corner as it scales.
	refW := float64(refSp.Width) / refTPU * bobScale
	refH := float64(refSp.Height) / refTPU * bobScale
	// bottomPad converts refSp.BottomPad — transparent canvas rows below the
	// resting pose's actual drawn content (see assets.contentBottomPad) —
	// into this same HUD pixel space, so bottom-anchoring below reaches the
	// CONTENT's bottom edge rather than the padded canvas's. Zero for the
	// vast majority of sprites (every vanilla WAD patch, and any HD pack
	// frame cropped tight to its own art), so this is a no-op for them.
	bottomPad := float64(refSp.BottomPad) / refTPU * bobScale
	refX := (hudLogicalW-refW)/2 + bobX // bobX: the walking side-to-side sway (engine.Game.weaponBob's x)
	// bobY (the y component of engine.Game.weaponBob) is subtracted, not
	// added: positive bobY is "risen above rest", and Y grows downward in
	// this pixel space.
	refY := weaponRefY(refH, bottomPad, raiseOffset, bobY)

	gunX, gunY := weaponAnchorPos(refX, refY, refSp.OffsetX, refSp.OffsetY, refTPU, gunSp.OffsetX, gunSp.OffsetY, gunTPU, bobScale)
	// The held weapon dims with the room it's in — id shades a psprite with
	// spritelights[MAXLIGHTSCALE-1], the bright end of its sector's
	// scalelight (see pspriteShade). The muzzle flash overlay stays full
	// bright (it's an FF_FULLBRIGHT sprite that lights the gun, not lit by it).
	r.blit(gunSp, gunX, gunY, false, pspriteShade(sectorLight, false), true, bobScale) // weapon transform (ignores hudScale)

	if !hasFlash {
		return
	}
	flashSp, ok := r.textures.Sprite(spriteName(flashPrefix, flashFrame))
	if !ok {
		return
	}
	flashTPU := tpu(flashSp)
	flashX := gunX + int(float64(gunSp.OffsetX)/gunTPU*bobScale) - int(float64(flashSp.OffsetX)/flashTPU)
	flashY := gunY + int(float64(gunSp.OffsetY)/gunTPU*bobScale) - int(float64(flashSp.OffsetY)/flashTPU)
	r.blit(flashSp, flashX, flashY, false, 256, true, 1)
}

func spriteName(prefix string, frame byte) string {
	// Direct concatenation instead of fmt.Sprintf: this runs 2-3x every
	// frame for the held weapon/muzzle-flash sprite lookup, and frame is
	// always a single ASCII byte — fmt's reflection-based formatting is
	// pure overhead for something this simple.
	return prefix + string(frame) + "0"
}

// weaponAnchorPos is DrawWeapon's shared-origin math, factored out for
// testing without a live Textures/Sprite lookup: given the resting pose's
// own already-computed screen position (refX, refY) and offset/density,
// return where the CURRENTLY DRAWN frame's own top-left should land so
// that frame's own offset lines up on the resting pose's anchor point
// (refX,refY translated by its own offset) — the same "offset delta"
// trick flashX/flashY use to place the flash relative to the gun, applied
// here first to align the gun's own frames with each other. When the
// drawn frame IS the resting one (same offset/density), this is the
// identity: returns exactly (refX, refY).
func weaponAnchorPos(refX, refY float64, refOffX, refOffY int, refTPU float64, frameOffX, frameOffY int, frameTPU, bobScale float64) (x, y int) {
	anchorX := refX + float64(refOffX)/refTPU*bobScale
	anchorY := refY + float64(refOffY)/refTPU*bobScale
	x = int(anchorX - float64(frameOffX)/frameTPU*bobScale)
	y = int(anchorY - float64(frameOffY)/frameTPU*bobScale)
	return x, y
}

// weaponRefY is DrawWeapon's resting-pose Y placement, factored out for
// testing: bottom-anchor a canvas of height refH against the HUD's bottom
// edge, but treat its actual drawn content as ending bottomPad pixels
// (already in this same HUD pixel space — see the BottomPad doc comment
// above) above the canvas's own bottom row, so the CONTENT reaches the
// screen edge rather than leaving whatever transparent padding a given
// weapon-pack frame carries below its art visible as a gap underneath the
// gun. bottomPad == 0 (every vanilla WAD patch, and any HD frame already
// cropped tight to its own art) reduces this to exactly the previous
// "anchor the full canvas" placement.
func weaponRefY(refH, bottomPad, raiseOffset, bobY float64) float64 {
	// bobY (the y component of engine.Game.weaponBob) is subtracted, not
	// added: positive bobY is "risen above rest", and Y grows downward in
	// this pixel space.
	return hudLogicalH - (refH - bottomPad) + weaponBottomOverlap + raiseOffset*(refH+weaponDropPx) - bobY
}
