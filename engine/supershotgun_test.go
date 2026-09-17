package engine

import (
	"testing"
)

func TestSuperShotgunPickupGivesWeaponAndShells(t *testing.T) {
	g := &Game{Player: DefaultPlayerStats(), Weapon: NewWeaponState(), SwitchWeaponOnPickup: true}
	g.Player.Ammo[1] = 0 // no shells
	if g.Player.Weapons[wpSuperShotgun] {
		t.Fatal("starts owning the SSG")
	}

	got := g.giveWeapon(wpSuperShotgun, false)
	if !got || !g.Player.Weapons[wpSuperShotgun] {
		t.Fatalf("giveWeapon(SSG) -> got=%v owned=%v", got, g.Player.Weapons[wpSuperShotgun])
	}
	if g.Player.Ammo[1] != 8 { // clipAmmo[shell]=4 * 2 clips
		t.Errorf("SSG pickup gave %d shells, want 8", g.Player.Ammo[1])
	}
	if g.Weapon.pending != wpSuperShotgun && g.Weapon.Current != wpSuperShotgun {
		t.Errorf("SwitchWeaponOnPickup did not select the SSG (current=%d pending=%d)", g.Weapon.Current, g.Weapon.pending)
	}
	// A dropped one gives half the shells.
	g2 := &Game{Player: DefaultPlayerStats(), Weapon: NewWeaponState()}
	g2.Player.Ammo[1] = 0
	g2.giveWeapon(wpSuperShotgun, true)
	if g2.Player.Ammo[1] != 4 {
		t.Errorf("dropped SSG gave %d shells, want 4", g2.Player.Ammo[1])
	}
}

func TestSuperShotgunCostsTwoShells(t *testing.T) {
	g := &Game{Player: DefaultPlayerStats(), Weapon: WeaponState{Current: wpSuperShotgun, phase: weaponReady, pending: -1}}
	g.playerMobj = &Mobj{Type: MT_PLAYER, Info: &mobjInfo[MT_PLAYER]}
	g.Player.Weapons[wpSuperShotgun] = true

	g.Player.Ammo[1] = 3
	if !g.beginFire() {
		t.Fatal("beginFire failed with 3 shells")
	}
	if g.Player.Ammo[1] != 1 {
		t.Errorf("after a shot: %d shells, want 1 (cost 2)", g.Player.Ammo[1])
	}
	// Only 1 shell left — can't fire.
	if g.beginFire() {
		t.Error("fired the SSG with only 1 shell")
	}
	if g.Player.Ammo[1] != 1 {
		t.Errorf("a failed fire still spent ammo: %d", g.Player.Ammo[1])
	}
}

func TestSuperShotgunReloadSoundSequence(t *testing.T) {
	g := &Game{
		Player: DefaultPlayerStats(),
		Weapon: WeaponState{Current: wpSuperShotgun, phase: weaponFiring, pending: -1},
	}
	g.playerMobj = &Mobj{Type: MT_PLAYER, Info: &mobjInfo[MT_PLAYER]}
	g.Player.Weapons[wpSuperShotgun] = true
	g.Player.Ammo[1] = 2

	var played []string
	g.sfxHook = func(n string) { played = append(played, n) }

	// Run the whole ~54-tic fire animation at 1 tic per step.
	for i := 0; i < 60 && g.Weapon.phase == weaponFiring; i++ {
		g.updateWeapon(1.0 / ticsPerSecond)
	}

	// The break-open reload triple, in order, once each.
	want := []string{"DSDBOPN", "DSDBLOAD", "DSDBCLS"}
	var seq []string
	for _, s := range played {
		for _, w := range want {
			if s == w {
				seq = append(seq, s)
			}
		}
	}
	if len(seq) != 3 || seq[0] != want[0] || seq[1] != want[1] || seq[2] != want[2] {
		t.Errorf("reload sounds = %v, want exactly %v in order (all sounds: %v)", seq, want, played)
	}
}

// TestSuperShotgunFireFrames checks the ported HD sequence (SmoothSuperShotgun
// — SH2N discharge, SH2R break-open reload, SH2S snap-shut, see
// Weapons[wpSuperShotgun]'s own doc comment for the exact tic breakdown)
// at a spread of points across all three sprite families.
func TestSuperShotgunFireFrames(t *testing.T) {
	g := &Game{Weapon: WeaponState{Current: wpSuperShotgun, phase: weaponFiring, pending: -1}}
	checks := []struct {
		atTic  float64
		frame  byte
		prefix string
	}{
		{1, 'A', "SH2N"}, {5, 'A', "SH2N"}, {12, 'B', "SH2R"}, {20, 'J', "SH2R"},
		{27, 'O', "SH2R"}, {34, 'X', "SH2R"}, {41, 'C', "SH2S"}, {47, 'G', "SH2S"}, {52, 'A', "SH2N"},
	}
	for _, c := range checks {
		g.Weapon.fireElapsedTics = c.atTic
		p, f, _, _, _ := g.currentSprite()
		if f != c.frame || p != c.prefix {
			t.Errorf("at tic %.0f: %s%c, want %s%c", c.atTic, p, f, c.prefix, c.frame)
		}
	}
	// Muzzle flash (A then B, 4+3 tics) only in the first 7 tics.
	g.Weapon.fireElapsedTics = 2
	if _, _, _, ff, has := g.currentSprite(); !has || ff != 'A' {
		t.Errorf("early flash = %c (has=%v), want A", ff, has)
	}
	g.Weapon.fireElapsedTics = 30
	if _, _, _, _, has := g.currentSprite(); has {
		t.Error("SSG still flashing 30 tics in")
	}
}

func TestSlot3TogglesShotgunAndSuperShotgun(t *testing.T) {
	g := &Game{Player: DefaultPlayerStats(), Weapon: WeaponState{Current: wpPistol, phase: weaponReady, pending: -1}}
	g.Player.Weapons[wpShotgun] = true
	g.Player.Weapons[wpSuperShotgun] = true

	g.SelectWeaponSlot(3) // -> first owned in slot 3
	first := g.Weapon.pending
	if first != wpShotgun {
		t.Fatalf("first slot-3 press selected %d, want shotgun", first)
	}
	g.Weapon.Current, g.Weapon.pending = wpShotgun, -1
	g.SelectWeaponSlot(3) // press again -> toggle
	if g.Weapon.pending != wpSuperShotgun {
		t.Errorf("second slot-3 press selected %d, want super shotgun", g.Weapon.pending)
	}
	g.Weapon.Current, g.Weapon.pending = wpSuperShotgun, -1
	g.SelectWeaponSlot(3) // again -> back to shotgun
	if g.Weapon.pending != wpShotgun {
		t.Errorf("third slot-3 press selected %d, want shotgun", g.Weapon.pending)
	}
}
