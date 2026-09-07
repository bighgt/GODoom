package worldgeo

import (
	"math"
	"testing"
)

// applyMat4 transforms a world-space point by a column-major GLSL mat4 (the
// shape IsoViewProj/ViewProj return), returning clip-space (x, y, z, w).
func applyMat4(m [16]float32, x, y, z float64) (cx, cy, cz, cw float64) {
	fx, fy, fz := float32(x), float32(y), float32(z)
	// column-major: element [col*4+row]; clip[row] = sum_col m[col*4+row]*v[col]
	get := func(row int) float64 {
		return float64(m[0*4+row])*float64(fx) + float64(m[1*4+row])*float64(fy) +
			float64(m[2*4+row])*float64(fz) + float64(m[3*4+row])*1
	}
	return get(0), get(1), get(2), get(3)
}

const isoTestEps = 1e-4

func almostEqual(a, b float64) bool { return math.Abs(a-b) < isoTestEps }

// TestIsoViewProjZeroElevationMatchesPerspectiveBasis checks that at
// Elevation == 0 (no tilt), IsoViewProj's (right, up, forward) basis is
// exactly ViewProj's own — right = (sinA,-cosA,0), up = (0,0,1), forward =
// (cosA,sinA,0) — since an untilted iso camera is just that camera's
// horizontal aim, only orthographic instead of perspective.
func TestIsoViewProjZeroElevationMatchesPerspectiveBasis(t *testing.T) {
	cam := IsoCamera{Azimuth: 0, Elevation: 0, Zoom: 100}
	w, h := 200, 200 // square, so halfW == halfH == Zoom

	// Target itself must land at clip (0, 0, mid-depth, 1).
	m := IsoViewProj(cam, w, h)
	cx, cy, cz, cw := applyMat4(m, 0, 0, 0)
	if !almostEqual(cx, 0) || !almostEqual(cy, 0) || !almostEqual(cw, 1) {
		t.Fatalf("target point: got clip (%v,%v,%v,%v), want (0,0,*,1)", cx, cy, cz, cw)
	}
	if !almostEqual(cz, 0.5) {
		t.Errorf("target point clip z = %v, want 0.5 (isoNear/isoFar centred on 0)", cz)
	}

	// Moving +Y from the target: right vector at Azimuth 0 is (0,-1,0), so
	// eyeX = -deltaY -> after the projection's 1/halfW scale, clip x should
	// be negative (halfW == Zoom == 100 here, so a 50-unit +Y move is -0.5).
	cx, cy, _, _ = applyMat4(m, 0, 50, 0)
	if !almostEqual(cx, -0.5) {
		t.Errorf("world +Y=50: clip x = %v, want -0.5 (right = (0,-1,0) at Azimuth 0)", cx)
	}
	if !almostEqual(cy, 0) {
		t.Errorf("world +Y=50: clip y = %v, want 0 (Y offset must not move vertically at Elevation 0)", cy)
	}

	// Moving +Z (world up) from the target: up vector is (0,0,1), and the
	// projection flips Y (NDC y-down), so +Z should move clip y negative
	// (up on screen).
	_, cy, _, _ = applyMat4(m, 0, 0, 50)
	if cy >= 0 {
		t.Errorf("world +Z=50: clip y = %v, want < 0 (up in world should read as up/negative-y on screen)", cy)
	}

	// Moving along the view direction (+X at Azimuth 0) must not move the
	// point on screen at all (it's purely a depth change) — this is the
	// case an elevation tilt is specifically supposed to change.
	cx, cy, cz, _ = applyMat4(m, 500, 0, 0)
	if !almostEqual(cx, 0) || !almostEqual(cy, 0) {
		t.Errorf("world +X=500 (straight ahead): clip (x,y) = (%v,%v), want (0,0)", cx, cy)
	}
	if cz <= 0.5 {
		t.Errorf("world +X=500 (straight ahead, farther from camera): clip z = %v, want > 0.5", cz)
	}
}

// TestIsoTrueElevationTiltsDownward checks the "true isometric" elevation
// constant actually tilts the view direction downward (negative world Z
// component) rather than upward or sideways — a sign-flip bug here would
// point the camera up through the floor instead of down at the map. A
// camera elevated above the target and tilted down is, in true 3D terms,
// closer to a point *above* the target than to one *below* it (below is
// farther beneath the camera) — so above must read as nearer (smaller
// clip z) and below as farther (larger clip z).
// TestIsoUnprojectToGroundRoundTrips picks a world point, works out where
// IsoViewProj would put it on screen by hand (clip -> NDC -> pixel, the
// inverse of IsoUnprojectToGround's own screen -> NDC step), and checks
// IsoUnprojectToGround maps that screen position (at the point's own Z as
// groundZ) back to the same (X, Y) — for both a free orbit angle and the
// "true isometric" elevation.
func TestIsoUnprojectToGroundRoundTrips(t *testing.T) {
	w, h := 400, 300
	cases := []IsoCamera{
		{TargetX: 100, TargetY: -50, TargetZ: 0, Azimuth: 0.7, Elevation: IsoTrueElevation, Zoom: 300},
		{TargetX: -20, TargetY: 30, TargetZ: 16, Azimuth: -1.2, Elevation: 0.9, Zoom: 150},
	}
	worldPt := [3]float64{140, -10, 24}

	for i, cam := range cases {
		m := IsoViewProj(cam, w, h)
		cx, cy, cz, cw := applyMat4(m, worldPt[0], worldPt[1], worldPt[2])
		if cw == 0 {
			t.Fatalf("case %d: clip w == 0", i)
		}
		ndcX, ndcY := cx/cw, cy/cw
		screenX := (ndcX + 1) / 2 * float64(w)
		screenY := (ndcY + 1) / 2 * float64(h)

		gotX, gotY, ok := IsoUnprojectToGround(cam, w, h, screenX, screenY, worldPt[2])
		if !ok {
			t.Fatalf("case %d: IsoUnprojectToGround reported ok=false", i)
		}
		if !almostEqual(gotX, worldPt[0]) || !almostEqual(gotY, worldPt[1]) {
			t.Errorf("case %d: got (%v, %v), want (%v, %v)", i, gotX, gotY, worldPt[0], worldPt[1])
		}

		// IsoProjectToScreen (the forward direction, used for the crosshair-
		// snap aim assist) must agree with the same hand-derived screen
		// position IsoViewProj itself puts the point at.
		psx, psy := IsoProjectToScreen(cam, w, h, worldPt[0], worldPt[1], worldPt[2])
		if !almostEqual(psx, screenX) || !almostEqual(psy, screenY) {
			t.Errorf("case %d: IsoProjectToScreen got (%v, %v), want (%v, %v)", i, psx, psy, screenX, screenY)
		}

		// IsoProjectToScreenDepth's clipZ must match the same clip-space Z
		// (after the perspective^H^Horthographic divide) IsoViewProj itself
		// produces — the value iso.frag's per-fragment occluder check
		// compares gl_FragCoord.z against.
		dsx, dsy, dcz := IsoProjectToScreenDepth(cam, w, h, worldPt[0], worldPt[1], worldPt[2])
		wantCZ := cz / cw
		if !almostEqual(dsx, screenX) || !almostEqual(dsy, screenY) || !almostEqual(dcz, wantCZ) {
			t.Errorf("case %d: IsoProjectToScreenDepth got (%v, %v, %v), want (%v, %v, %v)", i, dsx, dsy, dcz, screenX, screenY, wantCZ)
		}
	}
}

func TestIsoTrueElevationTiltsDownward(t *testing.T) {
	cam := IsoCamera{Azimuth: 0, Elevation: IsoTrueElevation, Zoom: 100}
	m := IsoViewProj(cam, 200, 200)

	_, _, zBelow, _ := applyMat4(m, 0, 0, -50)
	_, _, zAbove, _ := applyMat4(m, 0, 0, 50)
	if zAbove >= zBelow {
		t.Errorf("with a downward tilt, a point above the target should be nearer (smaller clip z) than one below it; got above=%v below=%v", zAbove, zBelow)
	}
}
