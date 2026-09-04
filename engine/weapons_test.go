package engine

import "testing"

// TestChainsawIdleAndFiringFrames: the running chainsaw's viewmodel should
// vibrate on SAWG C/D while idle (id's S_SAW/S_SAWB) and grind on SAWG A/B
// while firing (S_SAW1/S_SAW2) — not sit on a static 'A'.
func TestChainsawIdleAndFiringFrames(t *testing.T) {
	g := &Game{Weapon: WeaponState{Current: wpChainsaw, phase: weaponReady, pending: -1}}

	for _, tc := range []struct {
		tics float64
		want byte
	}{
		{0, 'C'}, {3, 'C'}, {4, 'D'}, {7, 'D'}, {8, 'C'}, {12, 'D'}, {800, 'C'},
	} {
		g.Weapon.idleTics = tc.tics
		gf, _, hasFlash := g.currentSprite()
		if gf != tc.want || hasFlash {
			t.Errorf("idle @%.0ft: frame %q flash %v, want %q false", tc.tics, gf, hasFlash, tc.want)
		}
	}

	g.Weapon.phase = weaponFiring
	for _, tc := range []struct {
		tics float64
		want byte
	}{
		{0, 'A'}, {3, 'A'}, {4, 'B'}, {7, 'B'},
	} {
		g.Weapon.fireElapsedTics = tc.tics
		if gf, _, _ := g.currentSprite(); gf != tc.want {
			t.Errorf("firing @%.0ft: frame %q, want %q", tc.tics, gf, tc.want)
		}
	}
}

// Every other weapon still idles on frame 'A' regardless of the idle timer.
func TestNonChainsawIdlesOnFrameA(t *testing.T) {
	for _, wp := range []int{wpFist, wpPistol, wpShotgun, wpChaingun, wpMissile, wpPlasma, wpBFG, wpSuperShotgun} {
		g := &Game{Weapon: WeaponState{Current: wp, phase: weaponReady, pending: -1, idleTics: 777}}
		if gf, _, _ := g.currentSprite(); gf != 'A' {
			t.Errorf("%s idle frame %q, want 'A'", Weapons[wp].Name, gf)
		}
	}
}

// updateWeapon must end a finished firing sequence (and not panic) when
// there's no Window to ask about the fire button — the auto-refire path is
// guarded on g.Window != nil.
func TestUpdateWeaponFiringEndsWithoutWindow(t *testing.T) {
	g := &Game{Weapon: WeaponState{Current: wpChainsaw, phase: weaponFiring, pending: -1}}
	g.Weapon.fireElapsedTics = 100 // past the 8-tic A/B sequence
	g.updateWeapon(0.05)
	if g.Weapon.phase != weaponReady {
		t.Errorf("phase = %v, want weaponReady", g.Weapon.phase)
	}
	if g.Weapon.fireElapsedTics != 0 {
		t.Errorf("fireElapsedTics = %.1f, want 0 after returning to ready", g.Weapon.fireElapsedTics)
	}
}

// TestChainsawIdleWhir: while the chainsaw sits ready, updateWeapon
// re-triggers its DSSAWIDL loop; firing, switching, or a non-chainsaw
// weapon must not.
func TestChainsawIdleWhir(t *testing.T) {
	newG := func(wp int) (*Game, *[]string) {
		var played []string
		g := &Game{Weapon: WeaponState{Current: wp, phase: weaponReady, pending: -1}}
		g.sfxHook = func(name string) { played = append(played, name) }
		return g, &played
	}

	// Ready chainsaw: whirs on a loop. Step ~1s of 20ms frames.
	g, played := newG(wpChainsaw)
	for i := 0; i < 50; i++ {
		g.updateWeapon(0.02)
	}
	n := 0
	for _, s := range *played {
		if s == "DSSAWIDL" {
			n++
		}
	}
	if n < 3 {
		t.Errorf("ready chainsaw played DSSAWIDL %d times in ~1s, want a repeating whir", n)
	}

	// Firing (held, so it stays mid-sequence): no idle whir — the fire
	// sound is beginFire's job.
	g, played = newG(wpChainsaw)
	for i := 0; i < 50; i++ {
		g.Weapon.phase = weaponFiring
		g.Weapon.fireElapsedTics = 2 // mid-grind, never completes
		g.updateWeapon(0.02)
	}
	for _, s := range *played {
		if s == "DSSAWIDL" {
			t.Fatalf("chainsaw played its idle whir while firing")
		}
	}

	// Switching away (pending set): the whir stops.
	g, played = newG(wpChainsaw)
	g.Weapon.pending = wpFist
	for i := 0; i < 50; i++ {
		g.updateWeapon(0.02)
	}
	for _, s := range *played {
		if s == "DSSAWIDL" {
			t.Fatalf("chainsaw whirred while switching away")
		}
	}

	// A different weapon: silent.
	g, played = newG(wpChaingun)
	for i := 0; i < 50; i++ {
		g.updateWeapon(0.02)
	}
	if len(*played) != 0 {
		t.Errorf("non-chainsaw weapon played idle sounds: %v", *played)
	}
}

func TestIdleSoundIntervalClamped(t *testing.T) {
	g := &Game{} // no WAD -> soundDuration returns 0
	if d := g.idleSoundInterval("DSSAWIDL"); d != 0.229 {
		t.Errorf("missing lump -> interval %.3f, want the 0.229 fallback", d)
	}
}

func TestFramesTotalAndLoopedFrame(t *testing.T) {
	fs := []WeaponFrame{{'C', 4}, {'D', 4}}
	if got := framesTotalTics(fs); got != 8 {
		t.Errorf("framesTotalTics = %d, want 8", got)
	}
	for _, tc := range []struct {
		tics float64
		want byte
	}{
		{1, 'C'}, {5, 'D'}, {9, 'C'}, {20, 'D'}, // wraps every 8
	} {
		if f, ok := frameAtLooped(fs, tc.tics); !ok || f.Frame != tc.want {
			t.Errorf("frameAtLooped(%.0f) = %q,%v; want %q,true", tc.tics, f.Frame, ok, tc.want)
		}
	}
	if _, ok := frameAtLooped(nil, 5); ok {
		t.Error("frameAtLooped(nil) ok=true, want false")
	}
}
