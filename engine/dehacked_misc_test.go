package engine

import (
	"testing"

	"twopointfive/engine/dehacked"
)

func TestApplyDehMiscWeaponAmmoCheat(t *testing.T) {
	// mobjInfo / Weapons / clipAmmo / dehMaxAmmo are package state.
	savedBFG := Weapons[wpBFG]
	savedShot := Weapons[wpShotgun]
	savedClip := clipAmmo
	savedMax := dehMaxAmmo
	defer func() {
		Weapons[wpBFG] = savedBFG
		Weapons[wpShotgun] = savedShot
		clipAmmo = savedClip
		dehMaxAmmo = savedMax
	}()

	g := &Game{Player: DefaultPlayerStats()}
	patch, errs := dehacked.Parse([]byte(`Misc
Initial Health = 150
Initial Bullets = 25
Max Health = 175
Soulsphere Health = 50
BFG Cells/Shot = 30
Max Soulsphere = 250

Weapon 2 (Shotgun)
Ammo type = 2
Ammo per shot = 3

Ammo 0 (Bullets)
Max ammo = 400
Per ammo = 15

Cheat 0
God mode = betuff
No Clipping 2 =
`))
	if len(errs) != 0 {
		t.Fatalf("parse: %v", errs)
	}
	g.applyDehacked(patch)

	if g.dm().maxHealth != 175 || g.dm().soulsphereHealth != 50 || g.dm().maxSoulsphere != 250 {
		t.Errorf("misc = %+v", *g.dm())
	}
	if Weapons[wpBFG].AmmoCost != 30 {
		t.Errorf("BFG cells/shot = %d, want 30", Weapons[wpBFG].AmmoCost)
	}
	if Weapons[wpShotgun].AmmoType != 2 || Weapons[wpShotgun].AmmoCost != 3 {
		t.Errorf("shotgun ammo type/cost = %d/%d", Weapons[wpShotgun].AmmoType, Weapons[wpShotgun].AmmoCost)
	}
	if clipAmmo[0] != 15 || dehMaxAmmo[0] != 400 {
		t.Errorf("bullets clip/max = %d/%d, want 15/400", clipAmmo[0], dehMaxAmmo[0])
	}
	// Player was fresh (100 hp / 50 bullets) so the retro-patch applies.
	if g.Player.Health != 150 || g.Player.Ammo[0] != 25 || g.Player.MaxAmmo[0] != 400 {
		t.Errorf("player = hp %d, bullets %d, maxbullets %d", g.Player.Health, g.Player.Ammo[0], g.Player.MaxAmmo[0])
	}

	// Cheat remap + disable.
	if g.cheat("iddqd") != "betuff" {
		t.Errorf("iddqd remapped to %q, want betuff", g.cheat("iddqd"))
	}
	if c := g.cheat("idclip"); c != "" || hasCheat("xxidclip", c) {
		t.Errorf("idclip should be disabled, got %q", c)
	}
}

// The DEH-tuned Max Health flows through giveBody (stimpack/medikit).
func TestDehMaxHealthGovernsGiveBody(t *testing.T) {
	g := &Game{Player: DefaultPlayerStats()}
	m := defaultDehMisc()
	m.maxHealth = 80
	g.misc = &m

	g.Player.Health = 75
	if !g.giveBody(25) || g.Player.Health != 80 {
		t.Errorf("giveBody capped at %d, want 80", g.Player.Health)
	}
	if g.giveBody(10) { // already at the (lowered) cap
		t.Error("giveBody returned true at the cap")
	}
}
