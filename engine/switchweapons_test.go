package engine

import (
	"os"
	"testing"

	"twopointfive/bsp"
	"twopointfive/config"
	"twopointfive/raster"
	"twopointfive/wad"
)

// loadGameSwap builds a Game for wadPath / mapName with config.SwitchWeapons
// on, so the arsenal and the on-map pickup THINGS are the other game's.
func loadGameSwap(t *testing.T, wadPath, mapName string) *Game {
	t.Helper()
	if _, err := os.Stat(wadPath); err != nil {
		t.Skipf("no %s", wadPath)
	}
	w, err := wad.Load(wadPath)
	if err != nil {
		t.Fatalf("wad.Load: %v", err)
	}
	lvl, err := w.LoadLevel(mapName)
	if err != nil {
		t.Fatalf("LoadLevel(%s): %v", mapName, err)
	}
	tree, err := bsp.Build(lvl)
	if err != nil {
		t.Fatalf("bsp.Build: %v", err)
	}
	return NewGame(nil, nil, nil, w, lvl, tree, raster.Camera{}, config.Config{Skill: 3, SwitchWeapons: true})
}

func spritesOnMap(g *Game) map[string]int {
	m := map[string]int{}
	for _, mo := range g.mobjs {
		if mo.spriteOverride != "" {
			m[mo.spriteOverride]++
		}
	}
	return m
}

// TestSwitchWeaponsReskinsHereticPickups: a Heretic map played with the Doom
// arsenal shows Doom weapon/ammo pickups on the floor, not Heretic ones.
func TestSwitchWeaponsReskinsHereticPickups(t *testing.T) {
	g := loadGameSwap(t, "../wad/heretic.wad", "E1M1")
	if !g.heretic || !g.swapWeapons {
		t.Fatalf("heretic=%v swapWeapons=%v", g.heretic, g.swapWeapons)
	}
	got := spritesOnMap(g)
	// E1M1's single-player weapons are 2001 Ethereal Crossbow and 2005
	// Gauntlets (the rest are notSingle) — now Doom sprites.
	if got["SHOT"] == 0 {
		t.Errorf("crossbow not re-skinned to SHOT — got %v", got)
	}
	if got["CSAW"] == 0 {
		t.Errorf("gauntlets not re-skinned to CSAW — got %v", got)
	}
	// No Heretic weapon sprite should remain.
	for _, gone := range []string{"WBOW", "WBLS", "WSKL", "WPHX", "WGNT", "WMCE"} {
		if got[gone] != 0 {
			t.Errorf("%s (Heretic weapon sprite) still on the map", gone)
		}
	}
	// Wand-crystal ammo (doomednum 10, x46) -> Doom clips.
	if got["CLIP"] == 0 {
		t.Errorf("wand-crystal ammo not re-skinned to CLIP — got %v", got)
	}
	// Bag of Holding -> backpack.
	if got["BPAK"] == 0 {
		t.Errorf("Bag of Holding not re-skinned to BPAK — got %v", got)
	}
}

// TestSwitchWeaponsReskinsDoomPickups: the mirror — a Doom map played with
// the Heretic arsenal shows Heretic pickups.
func TestSwitchWeaponsReskinsDoomPickups(t *testing.T) {
	g := loadGameSwap(t, "../testdata/DOOM1.WAD", "E1M2")
	if g.heretic || !g.swapWeapons {
		t.Fatalf("heretic=%v swapWeapons=%v", g.heretic, g.swapWeapons)
	}
	got := spritesOnMap(g)
	// E1M2 single-player weapons: 2001 shotgun -> WBOW, 2002 chaingun ->
	// WBLS, 2005 chainsaw -> WGNT.
	for _, want := range []string{"WBOW", "WBLS", "WGNT"} {
		if got[want] == 0 {
			t.Errorf("no %s pickup on the map — got %v", want, got)
		}
	}
	for _, gone := range []string{"SHOT", "MGUN", "CSAW"} {
		if got[gone] != 0 {
			t.Errorf("%s (Doom weapon sprite) still on the map", gone)
		}
	}
	// Doom ammo / backpack -> Heretic equivalents.
	if got["AMG1"] == 0 && got["AMG2"] == 0 {
		t.Errorf("bullets not re-skinned to wand crystals — got %v", got)
	}
}

// TestSwitchWeaponsOffLeavesPickups: without the switch, nothing is re-skinned.
func TestSwitchWeaponsOffLeavesPickups(t *testing.T) {
	if _, err := os.Stat("../testdata/DOOM1.WAD"); err != nil {
		t.Skip("no DOOM1.WAD")
	}
	w, _ := wad.Load("../testdata/DOOM1.WAD")
	lvl, _ := w.LoadLevel("E1M1")
	tree, _ := bsp.Build(lvl)
	g := NewGame(nil, nil, nil, w, lvl, tree, raster.Camera{}, config.Config{Skill: 3})
	if g.swapWeapons {
		t.Fatal("swapWeapons set without the config")
	}
	if s := spritesOnMap(g); s["WBOW"] != 0 {
		t.Errorf("Doom map has a Heretic pickup sprite without switchWeapons: %v", s)
	}
}
