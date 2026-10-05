package engine

import (
	"fmt"
	"strings"
	"testing"

	"twopointfive/assets"
	"twopointfive/raster"
	"twopointfive/wad"
)

// TestPlasmaBurstAnimation: holding fire on the plasma rifle cycles the HD
// pack's three firing frames shot by shot (PLGF A, B, C, A, ...), lights
// the room each shot with a light-only TNT1 flash (never the vanilla PLSF,
// which is a whole second gun), and releasing plays the PLGC vent
// cooldown before the rifle is ready again — the HD pack's own Fire1/2/3
// + release tail. Regression for "the plasma rifle isn't animated and a
// copy of it appears on the left while firing".
func TestPlasmaBurstAnimation(t *testing.T) {
	resetWeaponTables()
	g := &Game{Player: DefaultPlayerStats(), Weapon: NewWeaponState()}
	g.Player.Weapons[wpPlasma] = true
	g.Player.Ammo[Weapons[wpPlasma].AmmoType] = 100
	g.Weapon.Current, g.Weapon.phase, g.Weapon.Offset = wpPlasma, weaponReady, 0
	held := true
	g.fireHeldHook = func() bool { return held }

	var seq []string
	tick := func() {
		gp, gf, fp, _, hf := g.currentSprite()
		if hf && fp != raster.NoSpritePrefix {
			t.Fatalf("plasma flash drew sprite %q — its flash must be light-only (TNT1)", fp)
		}
		seq = append(seq, fmt.Sprintf("%s%c", gp, gf))
		g.updateWeapon(1.0 / 35)
	}

	g.FireWeapon()
	for i := 0; i < 9*3; i++ { // nine held shots, 3 tics each
		tick()
	}
	held = false
	for i := 0; i < 3+20+2; i++ { // the last shot's tail, the cooldown, then ready
		tick()
	}

	want := strings.Repeat("PLGFA ", 3) + strings.Repeat("PLGFB ", 3) + strings.Repeat("PLGFC ", 3)
	want = strings.Repeat(want, 3) + strings.Repeat("PLGFA ", 3) + // shot 10 (released during it)
		"PLGCB PLGCC PLGCD " + strings.Repeat("PLGCE ", 13) + "PLGCD PLGCC PLGCB PLGCA " +
		"PLGGA PLGGA"
	if got := strings.Join(seq, " "); got != strings.TrimSpace(want) {
		t.Errorf("gun frames:\n got %s\nwant %s", got, strings.TrimSpace(want))
	}
	if g.Weapon.phase != weaponReady {
		t.Errorf("phase after the cooldown = %v, want ready", g.Weapon.phase)
	}
	if shots := 100 - g.Player.Ammo[Weapons[wpPlasma].AmmoType]; shots != 10 {
		t.Errorf("fired %d shots, want 10", shots)
	}
}

// TestPlasmaAndBFGArtResolves: with Doom II and the on-disk HD weapon
// packs, every frame the plasma rifle and BFG draw — each fire-cycle
// frame, the plasma cooldown, the BFG windup/charge/flash, and both
// projectiles in flight and exploding — resolves to real pixels, and the
// plasma rifle draws exactly the same pixels with or without its (light-
// only) flash, so no second gun can appear.
func TestPlasmaAndBFGArtResolves(t *testing.T) {
	resetWeaponTables()
	w, err := wad.Load("../wad/Game WADs/doom2.wad")
	if err != nil {
		t.Skip("doom2.wad not available")
	}
	t.Chdir("..") // the HD weapon packs live under assets/weapons
	tex, err := assets.New(w)
	if err != nil {
		t.Fatal(err)
	}
	if len(tex.WeaponPackFrames()) == 0 {
		t.Skip("no HD weapon pack under assets/weapons")
	}
	need := map[string]bool{}
	for _, wp := range []int{wpPlasma, wpBFG} {
		d := &Weapons[wp]
		add := func(prefix string, fs []WeaponFrame) {
			for _, f := range fs {
				p := prefix
				if f.Prefix != "" {
					p = f.Prefix
				}
				if f.Frame != 0 && p != raster.NoSpritePrefix {
					need[fmt.Sprintf("%s%c0", p, f.Frame)] = true
				}
			}
		}
		add(d.SpritePrefix, d.FireFrames)
		for _, fs := range d.FireCycle {
			add(d.SpritePrefix, fs)
		}
		add(d.SpritePrefix, d.ReleaseFrames)
		add(d.FlashPrefix, d.FlashFrames)
		need[d.SpritePrefix+"A0"] = true
		need[d.Projectile.Sprite] = true
		for _, f := range d.Projectile.ExplodeFrames {
			need[fmt.Sprintf("%s%c0", d.Projectile.ExplodePrefix, f)] = true
		}
	}
	for n := range need {
		sp, ok := tex.Sprite(n)
		if !ok || sp == nil || sp.Width == 0 {
			t.Errorf("%s doesn't resolve", n)
			continue
		}
		opaque := 0
		for i := 3; i < len(sp.Pix); i += 4 {
			if sp.Pix[i] >= 128 {
				opaque++
			}
		}
		if opaque == 0 {
			t.Errorf("%s resolves but is fully transparent", n)
		}
	}

	draw := func(flash bool) []byte {
		r := raster.New(tex, 640, 400)
		r.BeginOverlayFrame()
		r.DrawWeapon("PLGF", 'A', Weapons[wpPlasma].FlashPrefix, 'A', flash, 0, 0, 0, 1, 255)
		return append([]byte(nil), r.Overlay()...)
	}
	if string(draw(true)) != string(draw(false)) {
		t.Error("the plasma rifle's flash drew extra pixels — it must be light-only")
	}
}
