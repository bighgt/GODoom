package raster

import (
	"os"
	"testing"

	"twopointfive/assets"
	"twopointfive/wad"
)

// hereticTex loads heretic.wad's graphics for the Heretic HUD renderers,
// which reference Heretic-only lumps (IN0.., YKEYICON, ARTIBOX, AMG1A0).
func hereticTex(t *testing.T) *assets.Textures {
	t.Helper()
	const path = "../wad/heretic.wad"
	if _, err := os.Stat(path); err != nil {
		t.Skipf("no %s", path)
	}
	w, err := wad.Load(path)
	if err != nil {
		t.Fatalf("wad.Load: %v", err)
	}
	tex, err := assets.New(w)
	if err != nil {
		t.Fatalf("assets.New: %v", err)
	}
	return tex
}

// TestDrawHereticQuakeHUD: the minimal Heretic HUD puts health lower-left,
// ready ammo lower-right, and the key/artifact cluster clear of that bottom
// row (and of each other).
func TestDrawHereticQuakeHUD(t *testing.T) {
	r := New(hereticTex(t), 320, 200) // 1:1 with the hqh* layout constants

	r.DrawHereticQuakeHUD(HUDStats{
		Health: 87, Armor: 120,
		Ammo: [6]int{35}, MaxAmmo: [6]int{100}, CurrentAmmo: 0,
		Keys: [6]bool{true, true, true, false, false, false},
		Arti: "ARTIINVU", ArtiCount: 3,
	})

	x0, x1, any := changedBox(r, hqhBottomNumY-2, 200)
	if !any {
		t.Fatal("DrawHereticQuakeHUD drew nothing on the bottom row")
	}
	if x0 > 90 {
		t.Errorf("bottom row starts at x=%d — no lower-left health readout", x0)
	}
	if x1 < 230 {
		t.Errorf("bottom row ends at x=%d — no lower-right ammo readout", x1)
	}

	if _, _, arm := changedBox(r, hqhArmorNumY-2, hqhArmorNumY+12); !arm {
		t.Error("Armor > 0 but nothing on the armour row")
	}
	if _, _, keys := changedBox(r, hqhKeysBottomY-1, hqhKeysBottomY+3*hqhKeysRowGap); !keys {
		t.Error("held keys not drawn")
	}
	if _, _, arti := changedBox(r, hqhArtiY-2, hqhArtiY+32); !arti {
		t.Error("selected artifact not drawn")
	}
	if hqhKeysBottomY+3*hqhKeysRowGap >= hqhBottomNumY {
		t.Error("keys stack overlaps the bottom health/ammo row")
	}
}

// TestDrawHereticQuakeHUDMinimal: no armour row at 0, no ammo number for a
// melee weapon (CurrentAmmo < 0), no artifact box when none selected.
func TestDrawHereticQuakeHUDMinimal(t *testing.T) {
	r := New(hereticTex(t), 320, 200)

	r.DrawHereticQuakeHUD(HUDStats{Health: 100, Armor: 0, CurrentAmmo: -1})

	if _, _, arm := changedBox(r, hqhArmorNumY-2, hqhArmorNumY+12); arm {
		t.Error("armour row drawn with Armor == 0")
	}
	if x0, _, any := changedBox(r, hqhBottomNumY-2, 200); any && x0 > 200 {
		t.Error("ammo readout drawn for CurrentAmmo < 0")
	}
}
