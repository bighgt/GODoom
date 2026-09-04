package raster

import (
	"math"
	"testing"
)

// Viewer at (100,0), thing at the origin, so the angle from viewer to thing
// is 180 deg; the rotation then depends only on the thing's own facing.
// Expected values are id's R_ProjectSprite quantisation
// ((ang - thingangle + 202.5deg) / 45deg, low 3 bits): rotation 0 = the
// thing facing the viewer, 4 = facing away.
func TestSpriteRotation(t *testing.T) {
	const d2r = math.Pi / 180
	cases := []struct {
		facingDeg float64
		want      int
	}{
		{0, 0},   // faces the viewer
		{180, 4}, // faces away
		{90, 6},  // faces north -> viewer sees its right side
		{270, 2}, // faces south -> viewer sees its left side
		{45, 7},
		{315, 1},
		{135, 5},
		{225, 3},
	}
	for _, c := range cases {
		if got := spriteRotation(100, 0, 0, 0, c.facingDeg*d2r); got != c.want {
			t.Errorf("facing %g deg: rotation %d, want %d", c.facingDeg, got, c.want)
		}
	}
}
