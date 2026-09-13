package raster

// DrawHereticStatusBar renders an approximation of Heretic's bottom status
// bar onto the 320x200 overlay using the real Heretic graphic lumps
// (BARBACK, the IN / SMALLIN number fonts, the gargoyle end-pieces, the
// health chain + life gem, the key icons, the artifact box). Positions are
// close to Heretic's sb_bar.c but not pixel-exact. Call in place of
// DrawStatusBar for a Heretic session (engine/hudstyle.go).
func (r *Renderer) DrawHereticStatusBar(ps HUDStats) {
	const barY = 158 // Heretic ST_Y (200 - 42)

	r.blitSprite("BARBACK", 0, barY)
	// the two chain-gargoyle end pieces
	r.blitSprite("LTFACE", 0, barY)
	r.blitSprite("RTFACE", 276, barY)

	// health chain + life gem: the gem slides along the chain with health
	h := ps.Health
	if h < 0 {
		h = 0
	}
	if h > 100 {
		h = 100
	}
	r.blitSprite("CHAIN", 0, 190)
	gemX := 2 + h*(270-2)/100
	if ps.GodMode {
		r.blitSprite("GOD1", gemX, 190)
		r.blitSprite("GOD2", gemX+2, 190)
	} else {
		r.blitSprite("LIFEGEM0", gemX, 190)
	}
	// mask the chain ends back over the gem where it would overrun
	r.blitSprite("LTFACE", 0, barY)
	r.blitSprite("RTFACE", 276, barY)

	// numbers: health (left), armour (right), ready ammo (upper mid-left)
	r.drawDigitsRightAligned(ps.Health, "IN", "", 88, 170)
	r.drawDigitsRightAligned(ps.Armor, "IN", "", 255, 170)
	if ps.CurrentAmmo >= 0 && ps.CurrentAmmo < len(ps.Ammo) {
		r.drawDigitsRightAligned(ps.Ammo[ps.CurrentAmmo], "IN", "", 135, 162)
	}

	// keys: Heretic has yellow / green / blue (indices 1 / 2 / 0 here)
	if ps.Keys[1] {
		r.blitSprite("YKEYICON", 153, 164)
	}
	if ps.Keys[2] {
		r.blitSprite("GKEYICON", 153, 172)
	}
	if ps.Keys[0] {
		r.blitSprite("BKEYICON", 153, 180)
	}

	// artifact box + current artifact icon and count
	r.blitSprite("ARTIBOX", 180, barY+3)
	if ps.Arti != "" {
		r.blitSprite(ps.Arti, 179, barY+2)
		if ps.ArtiCount > 1 {
			r.drawDigitsRightAligned(ps.ArtiCount, "SMALLIN", "", 209, barY+31)
		}
	}
}

// heretic key icon names, exposed for tests.
var hereticKeyIcons = [3]string{"BKEYICON", "YKEYICON", "GKEYICON"}

// hereticAmmoIcons is the pickup sprite shown next to the ready-ammo count,
// indexed by Heretic ammo type (see engine hgAmmo*): 0 wand crystals,
// 1 ethereal arrows, 2 claw orbs, 3 hellstaff runes, 4 flame orbs,
// 5 mace spheres.
var hereticAmmoIcons = [6]string{"AMG1A0", "AMC1A0", "AMB1A0", "AMS1A0", "AMP1A0", "AMM1A0"}

// DrawHereticQuakeHUD is the Quake II-style minimal corner HUD for a Heretic
// session — the same idea as DrawQuakeHUD (big health bottom-left, big ready
// ammo bottom-right, keys and the selected artifact clustered with it, no
// status-bar chrome) but drawn with Heretic's own graphic lumps: the "IN" /
// "SMALLIN" number fonts, the ammo pickup sprites, YKEYICON / GKEYICON /
// BKEYICON, and ARTIBOX. Selected by engine/hudstyle.go when hudStyle is
// quake2 and g.heretic. 320x200 authoring space, obeys hudScale.
func (r *Renderer) DrawHereticQuakeHUD(ps HUDStats) {
	// Health, bottom-left — big yellow "IN" digits.
	h := ps.Health
	if h < 0 {
		h = 0
	}
	r.drawDigitsRightAligned(h, "IN", "", hqhHealthNumRight, hqhBottomNumY)

	// Armour, one row above health, only when the player has some — the
	// shield icon sits just to its right.
	if ps.Armor > 0 {
		r.drawDigitsRightAligned(ps.Armor, "IN", "", hqhHealthNumRight, hqhArmorNumY)
		icon := "SHLDA0" // silver shield; enchanted (>100) shows the blue one
		if ps.Armor > 100 {
			icon = "SHD2A0"
		}
		r.blitSprite(icon, hqhArmorIconX, hqhArmorNumY+18)
	}

	// Ready ammo, bottom-right — big "IN" digits with the ammo-type icon.
	if ps.CurrentAmmo >= 0 && ps.CurrentAmmo < len(ps.Ammo) {
		r.drawDigitsRightAligned(ps.Ammo[ps.CurrentAmmo], "IN", "", hqhAmmoNumRight, hqhBottomNumY)
		if icon := hereticAmmoIcons[ps.CurrentAmmo]; icon != "" {
			r.blitSprite(icon, hqhAmmoIconX, hqhAmmoIconY)
		}
	}

	// Keys — yellow / green / blue, stacked just above the ammo readout.
	if ps.Keys[1] {
		r.blitSprite("YKEYICON", hqhKeysX, hqhKeysBottomY)
	}
	if ps.Keys[2] {
		r.blitSprite("GKEYICON", hqhKeysX, hqhKeysBottomY+hqhKeysRowGap)
	}
	if ps.Keys[0] {
		r.blitSprite("BKEYICON", hqhKeysX, hqhKeysBottomY+2*hqhKeysRowGap)
	}

	// Selected inventory artifact — the box + its icon + count, on the
	// right edge above the key column (Quake II has no inventory, and the
	// bottom centre is the weapon viewmodel's).
	if ps.Arti != "" {
		r.blitSprite("ARTIBOX", hqhArtiX, hqhArtiY)
		r.blitSprite(ps.Arti, hqhArtiX-1, hqhArtiY-1)
		if ps.ArtiCount > 1 {
			r.drawDigitsRightAligned(ps.ArtiCount, "SMALLIN", "", hqhArtiX+28, hqhArtiY+28)
		}
	}
}

// Layout for DrawHereticQuakeHUD, in the 320x200 HUD space (bottom = 200).
const (
	hqhBottomNumY = 178 // top of the big bottom-row "IN" digits
	hqhArmorNumY  = 156 // armour number, one row up from health

	hqhHealthNumRight = 60 // right edge of the health digits, bottom-left
	hqhArmorIconX     = 62 // shield icon, right of the armour number

	hqhAmmoNumRight = 268 // right edge of the ready-ammo digits, bottom-right
	hqhAmmoIconX    = 300 // ammo-type pickup sprite, right of the digits
	hqhAmmoIconY    = 196 // bottom-anchored

	hqhKeysX       = 302 // right edge, above the ammo readout
	hqhKeysBottomY = 143
	hqhKeysRowGap  = 11

	hqhArtiX = 286 // artifact box, right edge above the key column
	hqhArtiY = 108
)
