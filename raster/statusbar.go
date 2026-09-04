package raster

import (
	"fmt"

	"twopointfive/assets"
)

// HUDStats is a draw-only snapshot of the player state Doom's status bar
// displays — the renderer's input, produced fresh each frame by the engine
// (see engine.PlayerStats.toHUD) from its authoritative gameplay model.
// DrawStatusBar renders it through the exact same WAD graphics and screen
// layout the original engine used.
type HUDStats struct {
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
	// GodMode: draw the invulnerability face.
	GodMode bool
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

// smallAmmoY is the status-bar row for each ammo TYPE's small current/max
// counter, indexed [clip, shell, cell, rocket]. The STBAR graphic labels
// the four rows top-to-bottom BULL / SHEL / RCKT / CELL, so the cell count
// (type 2) belongs on the bottom row and the rocket count (type 3) above
// it — id's ST_AMMO2Y = ST_Y+23 (191), ST_AMMO3Y = ST_Y+17 (185). (This
// used to be sequential {173,179,185,191}, which put cells and rockets
// against the wrong labels.)
var smallAmmoY = [4]int{173, 179, 191, 185}
var keyY = [3]int{171, 181, 191}

// DrawStatusBar draws Doom's actual heads-up display onto the bottom 32
// rows of the 320x200 overlay buffer using the real graphic lumps from the
// loaded WAD (STBAR, STTNUM/STYSNUM/STGNUM digits, STFST faces, STARMS,
// STKEYS, ...) at the exact positions the original engine drew them at;
// CompositeOverlay scales the result up onto the frame. Call after Render
// (and DrawHUD, if used) so it draws on top.
func (r *Renderer) DrawStatusBar(ps HUDStats) {
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
func (r *Renderer) drawFace(ps HUDStats) {
	if ps.Health <= 0 {
		r.blitSprite("STFDEAD0", faceX, faceY)
		return
	}
	if ps.GodMode {
		r.blitSprite("STFGOD0", faceX, faceY)
		return
	}
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

// drawPercentField draws value as a right-aligned big number whose rightmost
// digit's right edge sits at rightX, then the '%' sign immediately after —
// exactly id's STlib_drawPercent, where ST_HEALTHX / ST_ARMORX is that
// boundary between the number and the '%'. (Passing it as a left edge, as
// this used to, shoved health and armour ~40px right, over the face.)
func (r *Renderer) drawPercentField(value, rightX, y int) {
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
			x -= logicalWidth(sp)
			r.blitSpriteImage(sp, x, y)
		}
	} else {
		for value > 0 {
			d := value % 10
			value /= 10
			if sp, ok := r.textures.Sprite(fmt.Sprintf("%s%d", digitPrefix, d)); ok {
				x -= logicalWidth(sp)
				r.blitSpriteImage(sp, x, y)
			}
		}
	}
	if neg && minusName != "" {
		if sp, ok := r.textures.Sprite(minusName); ok {
			r.blitSpriteImage(sp, x-logicalWidth(sp), y)
		}
	}
}

// logicalWidth is a sprite's width in HUD/map (1x) units — its pixel width
// divided by any hi-res upscale factor, so status-bar layout stays put
// whether or not an override is present.
func logicalWidth(sp *assets.RGBA) int {
	return int(float64(sp.Width) / tpu(sp))
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
	r.blit(sp, x, y, true, 256, false) // status-bar transform (obeys hudScale)
}
