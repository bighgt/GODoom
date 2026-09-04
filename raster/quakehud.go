package raster

// DrawQuakeHUD renders a minimal Quake II-style heads-up display: a big
// health number bottom-left and a big current-ammo number bottom-right,
// each next to a pickup-sprite icon; the armour count above health when
// the player has any; the held keys stacked top-right. No status-bar
// chrome — the rest of the screen is the view.
//
// It reuses the same 320x200 HUD authoring space and graphic lumps as
// DrawStatusBar (STTNUM/STYSNUM digits, the MEDI/ARM1/ARM2/CLIP/SHEL/CELL/
// ROCK pickup sprites, STKEYS), so it obeys hudScale and composites the
// same way. Call from renderFrame in place of DrawStatusBar.
func (r *Renderer) DrawQuakeHUD(ps HUDStats) {
	// Health, bottom-left: big red number with the medikit icon beside it.
	r.drawBigNumber(ps.Health, qhHealthNumRight, qhBottomNumY)
	r.blitSprite("MEDIA0", qhHealthIconX, qhIconBottomY)

	// Armour, just above health, only when the player has some — small
	// yellow number with the green/blue armour icon.
	if ps.Armor > 0 {
		r.drawSmallNumber(ps.Armor, qhArmorNumRight, qhArmorNumY)
		icon := "ARM1A0"
		if ps.Armor > 100 {
			icon = "ARM2A0"
		}
		r.blitSprite(icon, qhHealthIconX, qhArmorIconY)
	}

	// Current ammo, bottom-right: big red number with the ammo-type icon.
	if ps.CurrentAmmo >= 0 && ps.CurrentAmmo < len(ps.Ammo) {
		r.drawBigNumber(ps.Ammo[ps.CurrentAmmo], qhAmmoNumRight, qhBottomNumY)
		if icon := qhAmmoIcons[ps.CurrentAmmo]; icon != "" {
			r.blitSprite(icon, qhAmmoIconX, qhIconBottomY)
		}
	}

	// Keys, stacked top-right (blue / yellow / red — card or skull).
	for i := 0; i < 3; i++ {
		y := qhKeysTopY + i*qhKeysRowGap
		switch {
		case ps.Keys[i]:
			r.blitSprite(qhKeyName(i), qhKeysX, y)
		case ps.Keys[i+3]:
			r.blitSprite(qhKeyName(i+3), qhKeysX, y)
		}
	}
}

// qhAmmoIcons is the pickup sprite for each ammo type index
// (0 bullets, 1 shells, 2 cells, 3 rockets) — matches HUDStats.CurrentAmmo.
var qhAmmoIcons = [4]string{"CLIPA0", "SHELA0", "CELLA0", "ROCKA0"}

func qhKeyName(i int) string {
	// STKEYS0..2 cards (blue/yellow/red), STKEYS3..5 skulls.
	return "STKEYS" + string(rune('0'+i))
}

// Layout, in the 320x200 HUD authoring space (bottom edge = 200), chosen to
// read like Quake II's corners without colliding with the weapon viewmodel.
// The number's right edge and the icon's x are set so a full 3-digit value
// still leaves a clear gap between the digits and the icon.
const (
	qhBottomNumY  = 176 // top of the big bottom-row digits
	qhIconBottomY = 196 // pickup-sprite hotspot row (bottom-anchored sprites)

	qhHealthIconX    = 16 // medikit, hard against the bottom-left corner
	qhHealthNumRight = 98 // right edge of the health digits (clears the icon at 3 digits)

	qhAmmoNumRight = 282 // right edge of the ammo digits
	qhAmmoIconX    = 308 // ammo-type icon, right of the digits

	qhArmorNumY     = 158
	qhArmorIconY    = 169
	qhArmorNumRight = 86 // right edge of the (small) armour digits

	qhKeysX      = 308
	qhKeysTopY   = 8
	qhKeysRowGap = 11
)
