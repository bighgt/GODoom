package engine

import (
	"math"
	"testing"
)

// TestWeaponBobXScalesWithSpeedAndStaysBounded: standing still, no sway;
// moving, a small side-to-side sway whose amplitude grows with speed but
// never exceeds weaponBobMaxX even well past top speed.
func TestWeaponBobXScalesWithSpeedAndStaysBounded(t *testing.T) {
	g := &Game{Weapon: WeaponState{bobPhase: math.Pi / 2}} // sin(phase) = 1, so amplitude reads directly

	if bx := g.weaponBobX(); bx != 0 {
		t.Errorf("standing still: bobX = %.3f, want 0", bx)
	}

	g.VelX, g.VelY = moveTopSpeed/2, 0
	half := g.weaponBobX()
	if half <= 0 || half >= weaponBobMaxX {
		t.Errorf("half speed: bobX = %.3f, want strictly between 0 and %.3f", half, weaponBobMaxX)
	}

	g.VelX = moveTopSpeed
	full := g.weaponBobX()
	if math.Abs(full-weaponBobMaxX) > 1e-9 {
		t.Errorf("top speed: bobX = %.3f, want %.3f", full, weaponBobMaxX)
	}

	g.VelX = moveTopSpeed * 4 // well past top speed — must still clamp, not overshoot
	if over := g.weaponBobX(); math.Abs(over-weaponBobMaxX) > 1e-9 {
		t.Errorf("past top speed: bobX = %.3f, want clamped to %.3f", over, weaponBobMaxX)
	}
}

// TestWeaponBobPhaseSpeedTracksMovement: the sway's own cycle speed (not
// just its width) scales with how fast the player is moving — frozen at
// rest, full rate at top speed, roughly double at half speed vs. quarter.
func TestWeaponBobPhaseSpeedTracksMovement(t *testing.T) {
	g := &Game{Weapon: WeaponState{Current: wpPistol, phase: weaponReady, pending: -1}}

	// dt kept small enough that even top speed's increment stays well under
	// the 2*pi wrap — comparing the wrapped bobPhase values directly only
	// makes sense within one cycle.
	const dt = 0.1

	g.updateWeapon(dt)
	if g.Weapon.bobPhase != 0 {
		t.Errorf("standing still: bobPhase advanced to %.4f, want frozen at 0", g.Weapon.bobPhase)
	}

	g.VelX, g.VelY = moveTopSpeed/4, 0
	g.updateWeapon(dt)
	quarterSpeedPhase := g.Weapon.bobPhase
	if quarterSpeedPhase <= 0 {
		t.Fatalf("quarter speed: bobPhase = %.4f, want > 0", quarterSpeedPhase)
	}

	g2 := &Game{Weapon: WeaponState{Current: wpPistol, phase: weaponReady, pending: -1}}
	g2.VelX, g2.VelY = moveTopSpeed/2, 0
	g2.updateWeapon(dt)
	halfSpeedPhase := g2.Weapon.bobPhase

	if halfSpeedPhase <= quarterSpeedPhase {
		t.Errorf("half speed advanced the sway phase less than quarter speed: %.4f <= %.4f", halfSpeedPhase, quarterSpeedPhase)
	}
	if math.Abs(halfSpeedPhase-2*quarterSpeedPhase) > 1e-9 {
		t.Errorf("bobPhase should scale linearly with speed: half-speed %.4f, want ~2x quarter-speed %.4f", halfSpeedPhase, quarterSpeedPhase)
	}
}

// TestWeaponBobYRisesTwicePerSwayCycleAndNeverDips: the vertical bob is 0
// standing still, never negative (it only ever rises above rest), and
// completes two rise-and-settle cycles for every one full horizontal sway
// (a quarter horizontal sway is a half vertical cycle — its peak).
func TestWeaponBobYRisesTwicePerSwayCycleAndNeverDips(t *testing.T) {
	g := &Game{Weapon: WeaponState{bobPhase: 0}}
	if by := g.weaponBobY(); by != 0 {
		t.Errorf("phase 0: bobY = %.3f, want 0", by)
	}

	g.VelX, g.VelY = moveTopSpeed, 0

	for _, phase := range []float64{0, math.Pi / 2, math.Pi, 3 * math.Pi / 2} {
		g.Weapon.bobPhase = phase
		if by := g.weaponBobY(); by < 0 {
			t.Errorf("phase %.2f: bobY = %.3f, want >= 0 (never dips below rest)", phase, by)
		}
	}

	// A quarter horizontal cycle (phase = pi/4) is where sin(2*phase) peaks
	// at 1 — the vertical bob's first rise, at the top of its range.
	g.Weapon.bobPhase = math.Pi / 4
	if by := g.weaponBobY(); math.Abs(by-weaponBobMaxY) > 1e-9 {
		t.Errorf("phase pi/4 (moving at top speed): bobY = %.3f, want peak %.3f", by, weaponBobMaxY)
	}
}

// TestWeaponBobScaleClosestAtCentreFarthestAtExtremes: the toward/away-
// from-camera size swing is neutral (1) at rest, biggest (closest) at
// phase 0 (dead centre of the sway), and smallest (farthest) at phase
// pi/2 (the sway's side extreme) — the "half circular" arc: swinging out
// recedes, swinging back re-centres and grows close again.
func TestWeaponBobScaleClosestAtCentreFarthestAtExtremes(t *testing.T) {
	g := &Game{Weapon: WeaponState{bobPhase: 0}}
	if s := g.weaponBobScale(); s != 1 {
		t.Errorf("standing still: bobScale = %.4f, want 1 (no size change at rest)", s)
	}

	g.VelX, g.VelY = moveTopSpeed, 0

	g.Weapon.bobPhase = 0
	centre := g.weaponBobScale()
	if math.Abs(centre-(1+weaponBobDepth)) > 1e-9 {
		t.Errorf("centre of sway: bobScale = %.4f, want peak %.4f (closest)", centre, 1+weaponBobDepth)
	}

	g.Weapon.bobPhase = math.Pi / 2
	extreme := g.weaponBobScale()
	if math.Abs(extreme-(1-weaponBobDepth)) > 1e-9 {
		t.Errorf("side extreme: bobScale = %.4f, want trough %.4f (farthest)", extreme, 1-weaponBobDepth)
	}
}

// TestChainsawIdleAndFiringFrames: the running chainsaw's viewmodel should
// vibrate through SAWB B/C/D while idle and grind through SAWN (windup)
// then SAWF (the cut) while firing — not sit on a static 'A'. Also checks
// the sprite family actually switches at the SAWN->SAWF handoff (tic 3),
// since this is the weapon whose port most depends on WeaponFrame.Prefix.
func TestChainsawIdleAndFiringFrames(t *testing.T) {
	g := &Game{Weapon: WeaponState{Current: wpChainsaw, phase: weaponReady, pending: -1}}

	// IdleFrames: SAWB B,C,D,C at 2 tics each (loops every 8 tics).
	for _, tc := range []struct {
		tics float64
		want byte
	}{
		{0, 'B'}, {3, 'C'}, {4, 'D'}, {7, 'C'}, {8, 'B'}, {12, 'D'}, {800, 'B'},
	} {
		g.Weapon.idleTics = tc.tics
		gp, gf, _, _, hasFlash := g.currentSprite()
		if gf != tc.want || hasFlash {
			t.Errorf("idle @%.0ft: frame %q flash %v, want %q false", tc.tics, gf, hasFlash, tc.want)
		}
		if gp != "SAWB" {
			t.Errorf("idle @%.0ft: prefix %q, want SAWB", tc.tics, gp)
		}
	}

	g.Weapon.phase = weaponFiring
	for _, tc := range []struct {
		tics       float64
		wantFrame  byte
		wantPrefix string
	}{
		{0, 'A', "SAWN"}, {2, 'C', "SAWN"}, {3, 'A', "SAWF"}, {7, 'B', "SAWF"},
	} {
		g.Weapon.fireElapsedTics = tc.tics
		gp, gf, _, _, _ := g.currentSprite()
		if gf != tc.wantFrame {
			t.Errorf("firing @%.0ft: frame %q, want %q", tc.tics, gf, tc.wantFrame)
		}
		if gp != tc.wantPrefix {
			t.Errorf("firing @%.0ft: prefix %q, want %q", tc.tics, gp, tc.wantPrefix)
		}
	}
}

// Every other weapon still idles on frame 'A' regardless of the idle timer.
func TestNonChainsawIdlesOnFrameA(t *testing.T) {
	for _, wp := range []int{wpFist, wpPistol, wpShotgun, wpChaingun, wpMissile, wpPlasma, wpBFG, wpSuperShotgun} {
		g := &Game{Weapon: WeaponState{Current: wp, phase: weaponReady, pending: -1, idleTics: 777}}
		if _, gf, _, _, _ := g.currentSprite(); gf != 'A' {
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
	fs := []WeaponFrame{{Frame: 'C', Tics: 4}, {Frame: 'D', Tics: 4}}
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
