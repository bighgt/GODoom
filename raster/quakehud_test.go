package raster

import "testing"

// TestDrawQuakeHUDCorners: the minimal HUD draws a health readout in the
// lower-left and an ammo readout in the lower-right, and nothing spanning
// the middle (no status-bar chrome).
func TestDrawQuakeHUDCorners(t *testing.T) {
	_, _, tex := loadTestLevel(t)
	r := New(tex, 320, 200) // 1:1 with the qh* layout constants

	r.DrawQuakeHUD(HUDStats{
		Health: 87, Armor: 50,
		Ammo: [4]int{42, 0, 0, 0}, MaxAmmo: [4]int{200, 0, 0, 0}, CurrentAmmo: 0,
		Keys: [6]bool{true, false, false, false, false, true},
	})

	x0, x1, any := changedBox(r, qhBottomNumY-2, qhIconBottomY+2)
	if !any {
		t.Fatal("DrawQuakeHUD drew nothing on the bottom row")
	}
	if x0 > 100 {
		t.Errorf("bottom row starts at x=%d — no lower-left (health) readout", x0)
	}
	if x1 < 220 {
		t.Errorf("bottom row ends at x=%d — no lower-right (ammo) readout", x1)
	}

	// Armour readout sits above the health number.
	if _, _, arm := changedBox(r, qhArmorNumY-2, qhArmorNumY+10); !arm {
		t.Error("Armor > 0 but nothing drawn on the armour row")
	}

	// Keys stack top-right, well clear of the bottom readouts.
	if _, _, keys := changedBox(r, qhKeysTopY-1, qhKeysTopY+3*qhKeysRowGap); !keys {
		t.Error("held keys not drawn top-right")
	}
}

// TestDrawQuakeHUDNoArmorNoAmmo: armour row is blank when Armor == 0, and
// no ammo number for a weapon with CurrentAmmo < 0 (fist/chainsaw).
func TestDrawQuakeHUDNoArmorNoAmmo(t *testing.T) {
	_, _, tex := loadTestLevel(t)
	r := New(tex, 320, 200)

	r.DrawQuakeHUD(HUDStats{Health: 100, Armor: 0, CurrentAmmo: -1})

	if _, _, arm := changedBox(r, qhArmorNumY-2, qhArmorNumY+10); arm {
		t.Error("armour row drawn with Armor == 0")
	}
	// The lower-right ammo area stays clear.
	if x0, _, any := changedBox(r, qhBottomNumY-2, qhIconBottomY+2); any && x0 > 200 {
		t.Error("ammo readout drawn for CurrentAmmo < 0")
	}
}
