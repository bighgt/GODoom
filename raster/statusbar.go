package raster

import (
	"fmt"

	"twopointfive/assets"
)

// PlayerStats is the subset of player state Doom's status bar displays.
// There's no combat/pickup/ammo-consumption system yet (see
// game_design.txt's roadmap), so these currently start at a fixed loadout
// and stay static — but DrawStatusBar renders them through the exact same
// WAD graphics and screen layout the original engine used, so wiring up
// real values later is a data-plumbing change, not a rendering one.
type PlayerStats struct {
	Health, Armor int
	// Ammo/MaxAmmo are indexed by ammo type: 0 bullets, 1 shells, 2 cells, 3 rockets.
	Ammo, MaxAmmo [4]int
	// CurrentAmmo selects which of the 4 ammo types the big number next to
	// the face shows (mirrors the current weapon's ammo type); -1 shows
	// nothing there (e.g. fists).
	CurrentAmmo int
	// Weapons[n] reports whether weapon slot n (2..7; 1, the fist, is
	// always owned and isn't shown) is owned, for the arms widget.
	Weapons [8]bool
	// Keys[n]: 0 blue card, 1 yellow card, 2 red card, 3 blue skull, 4
	// yellow skull, 5 red skull.
	Keys [6]bool
}

// DefaultPlayerStats is a fresh-spawn loadout: full health, the starting
// pistol and its ammo, no armor or keys.
func DefaultPlayerStats() PlayerStats {
	ps := PlayerStats{Health: 100, CurrentAmmo: 0}
	ps.Ammo[0], ps.MaxAmmo[0] = 50, 200
	ps.MaxAmmo[1], ps.MaxAmmo[2], ps.MaxAmmo[3] = 50, 300, 50
	ps.Weapons[2] = true // pistol
	return ps
}

// Screen positions below are ported directly from id's own
// linuxdoom-1.10/st_stuff.c ST_* constants — this project's internal
// resolution is the same 320x200 vanilla Doom used, so no rescaling is
// needed to reuse them exactly.
const (
	stbarY = 168 // ST_Y: status bar top edge (200 - 32 tall)

	healthX, healthY = 90, 171
	armorX, armorY   = 221, 171
	ammoX, ammoY     = 44, 171

	faceX, faceY = 143, 168

	armsBGX, armsBGY = 104, 168
	armsColSpace     = 12
	armsRowSpace     = 10
	armsBaseX        = 111
	armsBaseY        = 172

	smallAmmoX, smallMaxAmmoX = 288, 314

	keysX = 239
)

var smallAmmoY = [4]int{173, 179, 185, 191}
var keyY = [3]int{171, 181, 191}

// DrawStatusBar draws Doom's actual heads-up display onto the bottom 32
// rows of r.Pix using the real graphic lumps from the loaded WAD (STBAR,
// STTNUM/STYSNUM/STGNUM digits, STFST faces, STARMS, STKEYS, ...) at the
// same positions the original engine drew them at. Call after Render (and
// DrawHUD, if used) so it draws on top.
func (r *Renderer) DrawStatusBar(ps PlayerStats) {
	r.blitSprite("STBAR", 0, stbarY)

	r.drawPercentField(ps.Health, healthX, healthY)
	r.drawPercentField(ps.Armor, armorX, armorY)
	if ps.CurrentAmmo >= 0 && ps.CurrentAmmo < len(ps.Ammo) {
		r.drawBigNumber(ps.Ammo[ps.CurrentAmmo], ammoX, ammoY)
	}

	r.drawFace(ps)

	r.blitSprite("STARMS", armsBGX, armsBGY)
	for slot := 2; slot <= 7; slot++ {
		col, row := (slot-2)%3, (slot-2)/3
		px := armsBaseX + col*armsColSpace
		py := armsBaseY + row*armsRowSpace
		if ps.Weapons[slot] {
			r.blitSprite(fmt.Sprintf("STYSNUM%d", slot), px, py)
		} else {
			r.blitSprite(fmt.Sprintf("STGNUM%d", slot), px, py)
		}
	}

	for i := 0; i < 4; i++ {
		r.drawSmallNumber(ps.Ammo[i], smallAmmoX, smallAmmoY[i])
		r.drawSmallNumber(ps.MaxAmmo[i], smallMaxAmmoX, smallAmmoY[i])
	}

	for i := 0; i < 3; i++ {
		switch {
		case ps.Keys[i]:
			r.blitSprite(fmt.Sprintf("STKEYS%d", i), keysX, keyY[i])
		case ps.Keys[i+3]:
			r.blitSprite(fmt.Sprintf("STKEYS%d", i+3), keysX, keyY[i])
		}
	}
}

// drawFace picks a health-tier face variant (STFST<tier><variant>, tier 0
// = healthiest .. 4 = near death) and draws it looking straight ahead
// (variant 1) — the original engine's pain/turn/god-mode/berserk variants
// need combat/item events this project doesn't simulate yet.
func (r *Renderer) drawFace(ps PlayerStats) {
	tier := 0
	switch {
	case ps.Health >= 80:
		tier = 0
	case ps.Health >= 60:
		tier = 1
	case ps.Health >= 40:
		tier = 2
	case ps.Health >= 20:
		tier = 3
	default:
		tier = 4
	}
	r.blitSprite(fmt.Sprintf("STFST%d1", tier), faceX, faceY)
}

// drawBigNumber draws value's decimal digits with STTNUM, right-aligned so
// the rightmost digit's right edge sits at rightX.
func (r *Renderer) drawBigNumber(value, rightX, y int) {
	r.drawDigitsRightAligned(value, "STTNUM", "STTMINUS", rightX, y)
}

// drawPercentField draws value as a big number in a fixed 3-digit-wide
// field starting at leftX, followed by a '%' — matching health/armor's
// fixed-width display in the original regardless of how many digits value
// actually has.
func (r *Renderer) drawPercentField(value, leftX, y int) {
	rightX := leftX + 3*r.digitWidth("STTNUM")
	r.drawBigNumber(value, rightX, y)
	r.blitSprite("STTPRCNT", rightX, y)
}

// drawSmallNumber draws value with STYSNUM, right-aligned so the rightmost
// digit's right edge sits at rightX — used for the small per-ammo-type counters.
func (r *Renderer) drawSmallNumber(value, rightX, y int) {
	r.drawDigitsRightAligned(value, "STYSNUM", "", rightX, y)
}

func (r *Renderer) drawDigitsRightAligned(value int, digitPrefix, minusName string, rightX, y int) {
	neg := value < 0
	if neg {
		value = -value
	}

	x := rightX
	if value == 0 {
		if sp, ok := r.textures.Sprite(digitPrefix + "0"); ok {
			x -= sp.Width
			r.blitSpriteImage(sp, x, y)
		}
	} else {
		for value > 0 {
			d := value % 10
			value /= 10
			if sp, ok := r.textures.Sprite(fmt.Sprintf("%s%d", digitPrefix, d)); ok {
				x -= sp.Width
				r.blitSpriteImage(sp, x, y)
			}
		}
	}
	if neg && minusName != "" {
		if sp, ok := r.textures.Sprite(minusName); ok {
			r.blitSpriteImage(sp, x-sp.Width, y)
		}
	}
}

func (r *Renderer) digitWidth(prefix string) int {
	if sp, ok := r.textures.Sprite(prefix + "0"); ok {
		return sp.Width
	}
	return 12 // fallback, matches vanilla STTNUM's actual glyph width
}

func (r *Renderer) blitSprite(name string, x, y int) {
	if sp, ok := r.textures.Sprite(name); ok {
		r.blitSpriteImage(sp, x, y)
	}
}

// blitSpriteImage draws sp with its own hotspot (OffsetX, OffsetY) aligned
// to (x, y) — the same convention the original engine's V_DrawPatch used.
// Thin wrapper around the shared blit (blit.go), which every sprite draw
// in this package (and DrawWeapon's own non-offset variant) goes through.
func (r *Renderer) blitSpriteImage(sp *assets.RGBA, x, y int) {
	r.blit(sp, x, y, true)
}
