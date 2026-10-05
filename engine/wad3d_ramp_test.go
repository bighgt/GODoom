package engine_test

import (
	"math"
	"path/filepath"
	"testing"

	"twopointfive/engine"
	"twopointfive/wad"
	"twopointfive/wad3d"
)

// TestWad3dRampIsWalkable converts DOOM1.WAD's E1M1 with cmd/wad3d's
// stairs pass and walks the player up the first generated ramp (the
// 4-step run north of the start room, floor -136 -> -56) through the real
// movement code: the climb must reach the top landing, and rise
// continuously — no per-tic jump anywhere near a 16-unit step, which is
// what the original staircase would produce.
func TestWad3dRampIsWalkable(t *testing.T) {
	const path = "../testdata/DOOM1.WAD"
	w, err := wad.Load(path)
	if err != nil {
		t.Skipf("%s not available: %v", path, err)
	}
	lumps, _, err := wad3d.Convert(wad3d.Source{Name: "DOOM1.WAD", Picked: w, Merged: w},
		wad3d.Options{Maps: []string{"E1M1"}, NoVoxels: true})
	if err != nil {
		t.Fatal(err)
	}
	var slopesSrc []byte
	for _, l := range lumps {
		if l.Name == "SLOPES" {
			slopesSrc = l.Data
		}
	}
	if slopesSrc == nil {
		t.Fatal("conversion produced no SLOPES lump for E1M1")
	}

	g := engine.LoadRealLevelForTest(t, path, "E1M1")
	g.ApplySlopesForTest(t, slopesSrc)

	// Bottom riser midpoint is (2112, -3904); the ramp climbs +Y for 128
	// units. Start 32 below it on the lower landing, end 32 past the top.
	const x0, y0, speed = 2112.0, -3904.0 - 32, 4.0
	prevZ, maxJump := math.NaN(), 0.0
	var lastY, lastZ float64
	g.WalkForTest(x0, y0, 0, speed, (128+64)/int(speed), func(x, y, z float64) {
		if !math.IsNaN(prevZ) {
			maxJump = math.Max(maxJump, z-prevZ)
		}
		prevZ, lastY, lastZ = z, y, z
	})
	if lastY < -3904+128+16 {
		t.Fatalf("player stopped at y=%v — blocked before the top of the ramp", lastY)
	}
	if lastZ != -56 {
		t.Errorf("finished at z=%v, want the top landing's floor -56", lastZ)
	}
	if maxJump > speed*0.7 {
		t.Errorf("largest per-tic rise %v — the climb should be a smooth ramp (%.2f/tic), not steps", maxJump, speed*0.625)
	}
}

// TestWad3dWeaponSounds: a shareware Doom session on a converted WAD plays
// the Super Shotgun (Doom II only) and Heretic crossbow sounds from the
// WAD's embedded weapon set, with no other IWAD attached.
func TestWad3dWeaponSounds(t *testing.T) {
	d1, err := wad.Load("../testdata/DOOM1.WAD")
	if err != nil {
		t.Skip("DOOM1.WAD not available")
	}
	d2, err := wad.Load("../wad/Game WADs/doom2.wad")
	if err != nil {
		t.Skip("doom2.wad not available")
	}
	her, err := wad.Load("../wad/Game WADs/heretic.wad")
	if err != nil {
		t.Skip("heretic.wad not available")
	}
	if engine.SoundForTest(d1, "DSDSHTGN") {
		t.Fatal("test setup: shareware already has the Super Shotgun sound")
	}
	lumps, _, err := wad3d.Convert(
		wad3d.Source{Name: "DOOM1.WAD", Picked: d1, Merged: d1, Weapons: []wad3d.WeaponIWAD{
			{Game: "doom", WAD: d2}, {Game: "heretic", WAD: her}}},
		wad3d.Options{Maps: []string{"E1M1"}, Passes: []string{}, Weapons: true, NoVoxels: true})
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "out.wad")
	if err := wad.WritePWAD(out, lumps); err != nil {
		t.Fatal(err)
	}
	pw, err := wad.Load(out)
	if err != nil {
		t.Fatal(err)
	}
	merged := wad.Merge(d1, pw)
	for _, s := range []string{"DSDSHTGN", "DSDBOPN", "BOWSHT", "PHOHIT", "DSPISTOL"} {
		if !engine.SoundForTest(merged, s) {
			t.Errorf("sound %s doesn't resolve on the converted WAD", s)
		}
	}
}
