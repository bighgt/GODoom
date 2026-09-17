package raster

import "testing"

// TestWeaponAnchorPosIdentityForRestingFrame: when the drawn frame IS the
// resting one (same offset/density passed for both ref and frame), the
// result must be exactly (refX, refY) regardless of what the offset/tpu
// values themselves are — this is what keeps every weapon whose pack
// frames share one canvas/offset looking exactly as before this fix.
func TestWeaponAnchorPosIdentityForRestingFrame(t *testing.T) {
	cases := []struct {
		refX, refY   float64
		offX, offY   int
		tpu, bobScale float64
	}{
		{100, 150, 0, 0, 2.0, 1.0},
		{50, 80, -524, -519, 4.5, 1.0},
		{60, 90, -524, -519, 4.5, 1.06}, // bobScale != 1
	}
	for _, c := range cases {
		x, y := weaponAnchorPos(c.refX, c.refY, c.offX, c.offY, c.tpu, c.offX, c.offY, c.tpu, c.bobScale)
		if x != int(c.refX) || y != int(c.refY) {
			t.Errorf("case %+v: got (%d,%d), want (%d,%d)", c, x, y, int(c.refX), int(c.refY))
		}
	}
}

// TestWeaponAnchorPosTracksSharedOrigin: a frame whose own canvas is
// cropped differently from the resting pose (the rocket launcher's own
// recoil frames — see DrawWeapon's doc comment) must still land with its
// authored offset point exactly on the resting pose's anchor, not get
// independently re-centered by its own (different) bounding box.
func TestWeaponAnchorPosTracksSharedOrigin(t *testing.T) {
	const tpu = 4.5
	// Resting pose 'A': drawn at (50, 80), offset (-524, -519) — matches
	// this project's actual ported RLANA0 data.
	refX, refY := 50.0, 80.0
	refOffX, refOffY := -524, -519
	anchorX := refX + float64(refOffX)/tpu
	anchorY := refY + float64(refOffY)/tpu

	// A later recoil frame ('D'): a taller/wider canvas, different offset
	// (RLAND0's actual data) — its own top-left must differ from refX/refY
	// (it's NOT just re-centered the same way) but its OWN offset point
	// must land on the exact same anchor computed above.
	frameOffX, frameOffY := -473, -558
	x, y := weaponAnchorPos(refX, refY, refOffX, refOffY, tpu, frameOffX, frameOffY, tpu, 1.0)

	gotAnchorX := float64(x) + float64(frameOffX)/tpu
	gotAnchorY := float64(y) + float64(frameOffY)/tpu
	if d := gotAnchorX - anchorX; d > 1 || d < -1 { // int() truncation tolerance
		t.Errorf("frame's own anchor X = %.2f, want %.2f (the resting pose's)", gotAnchorX, anchorX)
	}
	if d := gotAnchorY - anchorY; d > 1 || d < -1 {
		t.Errorf("frame's own anchor Y = %.2f, want %.2f (the resting pose's)", gotAnchorY, anchorY)
	}
	if x == int(refX) && y == int(refY) {
		t.Error("frame with a different offset landed at exactly the resting position — the offset delta wasn't applied")
	}
}

// TestWeaponRefYZeroPadUnchanged: bottomPad == 0 (every vanilla WAD patch,
// and any HD frame already cropped tight to its own art — e.g. this
// project's "Smooth HD Weapons" shotgun) must reduce to exactly the old
// "anchor the full canvas" position, so this fix is a no-op for every
// weapon that isn't padded.
func TestWeaponRefYZeroPadUnchanged(t *testing.T) {
	got := weaponRefY(150, 0, 0, 0)
	want := float64(hudLogicalH) - 150 + weaponBottomOverlap
	if got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestWeaponRefYBottomPadPushesDown: this project's actual RLANA0 case —
// a canvas with real transparent padding below the drawn gun art (see
// assets.contentBottomPad) — must push the resting position DOWN by
// exactly the padding amount, so the drawn CONTENT (not the padded canvas)
// reaches the same bottom-anchored line a zero-pad sprite would.
func TestWeaponRefYBottomPadPushesDown(t *testing.T) {
	const refH = 150.0
	const pad = 21.6 // ~97px/4.5 tpu, this project's actual RLANA0 measurement
	zeroPad := weaponRefY(refH, 0, 0, 0)
	padded := weaponRefY(refH, pad, 0, 0)
	if d := padded - (zeroPad + pad); d > 1e-9 || d < -1e-9 {
		t.Errorf("padded refY = %v, want exactly %v (zeroPad %v + pad %v)", padded, zeroPad+pad, zeroPad, pad)
	}
}
