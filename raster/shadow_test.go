package raster

import (
	"bytes"
	"math"
	"testing"
)

// DrawGroundShadow must darken (never brighten) the floor pixels it covers,
// and be a no-op when SetGroundShadows(false).
func TestGroundShadowDarkensFloorOnly(t *testing.T) {
	lvl, tree, tex := loadLevelFrom(t, "../testdata/DOOM1.WAD", "E1M1")
	const w, h = 640, 400
	r := New(tex, w, h)
	cam := Camera{X: 1056, Y: -3616, Z: 41, Angle: math.Pi / 2, Pitch: -0.2, FOVDeg: 90}
	r.Render(lvl, tree, cam)

	// A floor pixel somewhere in the lower half: finite depth, and its
	// unprojected world point sits below the camera.
	px, py, depth := -1, -1, 0.0
	horizon := r.horizonY()
	for _, col := range []int{w / 2, w/2 - 80, w/2 + 80} {
		for y := h - 3; y > h/2; y-- {
			d := float64(r.depth[y*w+col])
			if d >= math.MaxFloat32 || d <= 4 {
				continue
			}
			wz := cam.Z - (float64(y)+0.5-horizon)/r.focal*d
			if wz < cam.Z-8 { // genuinely a floor below the eye
				px, py, depth = col, y, d
				break
			}
		}
		if px >= 0 {
			break
		}
	}
	if px < 0 {
		t.Skip("no floor pixel visible from this E1M1 vantage")
	}

	halfW := float64(w) / 2
	colF := (float64(px) + 0.5 - halfW) / r.focal
	wx := cam.X + depth*(r.cosA+colF*r.sinA)
	wy := cam.Y + depth*(r.sinA-colF*r.cosA)
	floorZ := cam.Z - (float64(py)+0.5-horizon)/r.focal*depth

	before := append([]byte(nil), r.Pix...)

	r.SetGroundShadows(false)
	r.DrawGroundShadow(cam, wx, wy, floorZ, 56, 0.6)
	if !bytes.Equal(before, r.Pix) {
		t.Fatal("DrawGroundShadow touched Pix with shadows disabled")
	}

	r.SetGroundShadows(true)
	r.DrawGroundShadow(cam, wx, wy, floorZ, 56, 0.6)

	changed := 0
	for i := 0; i < len(before); i += 4 {
		if before[i] == r.Pix[i] && before[i+1] == r.Pix[i+1] && before[i+2] == r.Pix[i+2] {
			continue
		}
		changed++
		if r.Pix[i] > before[i] || r.Pix[i+1] > before[i+1] || r.Pix[i+2] > before[i+2] {
			t.Fatalf("pixel %d brightened by the shadow: %v -> %v", i/4, before[i:i+3], r.Pix[i:i+3])
		}
	}
	if changed < 20 {
		t.Errorf("ground shadow changed only %d pixels, expected a patch", changed)
	}
	ci := (py*w + px) * 4
	if before[ci] > 4 && r.Pix[ci] >= before[ci] {
		t.Errorf("the sampled centre floor pixel was not darkened: %d -> %d", before[ci], r.Pix[ci])
	}
}

// A shadow centred behind the camera projects nowhere and must be a no-op.
func TestGroundShadowBehindCameraNoOp(t *testing.T) {
	lvl, tree, tex := loadLevelFrom(t, "../testdata/DOOM1.WAD", "E1M1")
	const w, h = 320, 200
	r := New(tex, w, h)
	cam := Camera{X: 1056, Y: -3616, Z: 41, Angle: math.Pi / 2, FOVDeg: 90}
	r.Render(lvl, tree, cam)
	before := append([]byte(nil), r.Pix...)
	r.DrawGroundShadow(cam, cam.X, cam.Y-256, 0, 48, 0.6) // 256u south = behind
	if !bytes.Equal(before, r.Pix) {
		t.Error("a shadow behind the camera changed the frame")
	}
}
