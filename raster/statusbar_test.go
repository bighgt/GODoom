package raster

import "testing"

// changedBox returns the horizontal bounds of non-transparent overlay
// pixels in the given row band.
func changedBox(r *Renderer, y0, y1 int) (x0, x1 int, any bool) {
	x0, x1 = r.Width, -1
	for y := y0; y < y1 && y < r.Height; y++ {
		for x := 0; x < r.Width; x++ {
			if r.overlay[(y*r.Width+x)*4+3] != 0 {
				if x < x0 {
					x0 = x
				}
				if x > x1 {
					x1 = x
				}
				any = true
			}
		}
	}
	return
}

// TestPercentFieldRightAligned guards the HUD position fix: drawPercentField
// right-aligns the number so its rightmost digit ends at the passed x (id's
// ST_HEALTHX = 90), with the '%' just after — it must NOT be shoved ~40px
// right (the old bug treated x as a left edge and added 3 digit widths,
// putting health/armour over the face at x=143).
func TestPercentFieldRightAligned(t *testing.T) {
	_, _, tex := loadTestLevel(t)
	r := New(tex, 320, 200) // 1:1 with the ST_* constants

	r.drawPercentField(100, healthX, healthY) // healthX == 90

	x0, x1, any := changedBox(r, healthY-4, healthY+16)
	if !any {
		t.Fatal("drawPercentField drew nothing")
	}
	// Vanilla layout: "100" spans roughly [48,90], the '%' patch [90,~104].
	if x1 >= faceX {
		t.Errorf("percent field ends at x=%d, into the face (x=%d) — mis-positioned", x1, faceX)
	}
	if x0 < 20 || x0 > healthX {
		t.Errorf("percent field starts at x=%d, want it right-aligned near x=%d", x0, healthX)
	}
	if x1 < healthX {
		t.Errorf("percent field ends at x=%d, before the '%%' at x=%d", x1, healthX)
	}
}

// TestSmallAmmoRowsMatchLabels: the four small current/max ammo counters
// line up with the STBAR labels BULL / SHEL / RCKT / CELL (top to bottom).
// The cell count (ammo type 2) is the BOTTOM row, the rocket count (type 3)
// the row above it — id's ST_AMMO2Y (191) / ST_AMMO3Y (185).
func TestSmallAmmoRowsMatchLabels(t *testing.T) {
	if smallAmmoY != [4]int{173, 179, 191, 185} {
		t.Errorf("smallAmmoY = %v, want {173,179,191,185} (BULL,SHEL,CELL@191,RCKT@185)", smallAmmoY)
	}
	if smallAmmoY[2] <= smallAmmoY[3] {
		t.Errorf("cell counter (y=%d) is not below the rocket counter (y=%d)", smallAmmoY[2], smallAmmoY[3])
	}

	_, _, tex := loadTestLevel(t)
	r := New(tex, 320, 200)
	r.DrawStatusBar(HUDStats{
		Ammo: [4]int{123, 123, 123, 123}, MaxAmmo: [4]int{456, 456, 456, 456}, CurrentAmmo: -1,
	})
	bar := New(tex, 320, 200)
	bar.blitSprite("STBAR", 0, stbarY)
	// Rows in the small-ammo column (x>=250) that differ from bare STBAR.
	rowChanged := func(y int) bool {
		for x := 250; x < r.Width; x++ {
			i := (y*r.Width + x) * 4
			if r.overlay[i] != bar.overlay[i] || r.overlay[i+1] != bar.overlay[i+1] || r.overlay[i+2] != bar.overlay[i+2] {
				return true
			}
		}
		return false
	}
	// All four 6px counter rows draw (BULL 173, SHEL 179, RCKT 185, CELL 191)
	// and nothing spills past the CELL row.
	for _, y := range []int{175, 181, 187, 193} {
		if !rowChanged(y) {
			t.Errorf("no small-ammo counter around row y=%d", y)
		}
	}
	if rowChanged(197) {
		t.Error("a small-ammo counter drew below the CELL row (y>=197)")
	}
}

// TestDeadFace: the death face shows once health hits 0.
func TestDeadFace(t *testing.T) {
	_, _, tex := loadTestLevel(t)
	r := New(tex, 320, 200)
	if _, ok := tex.Sprite("STFDEAD0"); !ok {
		t.Skip("WAD has no STFDEAD0")
	}
	r.drawFace(HUDStats{Health: 0})
	if _, x1, any := changedBox(r, faceY-2, faceY+34); !any || x1 < faceX {
		t.Errorf("dead face not drawn at the face slot (any=%v x1=%d)", any, x1)
	}
}
