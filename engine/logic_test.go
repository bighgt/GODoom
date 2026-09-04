package engine

import (
	"math"
	"testing"

	"twopointfive/raster"
)

func TestDistancePointToSegment(t *testing.T) {
	if d := distancePointToSegment(5, 0, 0, 0, 10, 0); d != 0 {
		t.Errorf("on-segment: got %v want 0", d)
	}
	if d := distancePointToSegment(5, 3, 0, 0, 10, 0); math.Abs(d-3) > 1e-9 {
		t.Errorf("perpendicular: got %v want 3", d)
	}
	if d := distancePointToSegment(-4, 0, 0, 0, 10, 0); math.Abs(d-4) > 1e-9 {
		t.Errorf("before start clamps to endpoint: got %v want 4", d)
	}
	if d := distancePointToSegment(3, 4, 0, 0, 0, 0); math.Abs(d-5) > 1e-9 {
		t.Errorf("degenerate (zero-length) segment: got %v want 5", d)
	}
}

func TestBoxStraddlesLine(t *testing.T) {
	// Vertical line x = 100. Box half-width 16.
	if boxStraddlesLine(80, 0, 16, 100, -50, 100, 50) {
		t.Error("box wholly left of the line reported as straddling")
	}
	if boxStraddlesLine(120, 0, 16, 100, -50, 100, 50) {
		t.Error("box wholly right of the line reported as straddling")
	}
	if !boxStraddlesLine(90, 0, 16, 100, -50, 100, 50) {
		t.Error("box edge (74..106) crossing x=100 not reported as straddling")
	}
	if !boxStraddlesLine(100, 0, 16, 100, -50, 100, 50) {
		t.Error("box centred on the line not reported as straddling")
	}
	// Just-touching (box edge exactly on the line) counts as not yet crossing.
	if boxStraddlesLine(84, 0, 16, 100, -50, 100, 50) {
		t.Error("box edge exactly on the line should not count as straddling")
	}
	// Diagonal line through the origin, direction (1,1).
	if !boxStraddlesLine(0, 0, 4, -10, -10, 10, 10) {
		t.Error("box on a diagonal line not reported as straddling")
	}
	if boxStraddlesLine(20, -20, 4, -10, -10, 10, 10) {
		t.Error("box well off the diagonal line reported as straddling")
	}
}

func TestRayIntersectsSegment(t *testing.T) {
	dist, hit := rayIntersectsSegment(0, 0, 1, 0, 10, -5, 10, 5)
	if !hit || math.Abs(dist-10) > 1e-9 {
		t.Errorf("frontal hit: hit=%v dist=%v want true,10", hit, dist)
	}
	if _, hit := rayIntersectsSegment(0, 0, -1, 0, 10, -5, 10, 5); hit {
		t.Error("segment behind the ray reported as a hit")
	}
	if _, hit := rayIntersectsSegment(0, 100, 1, 0, 10, -5, 10, 5); hit {
		t.Error("ray passing over the segment reported as a hit")
	}
	if _, hit := rayIntersectsSegment(0, 0, 0, 1, 10, -5, 10, 5); hit {
		t.Error("parallel ray reported as a hit")
	}
}

func TestAimDirection(t *testing.T) {
	for _, c := range []raster.Camera{
		{Angle: 0, Pitch: 0},
		{Angle: 1.2, Pitch: 0.3},
		{Angle: -2.5, Pitch: -0.5},
	} {
		dx, dy, dz := aimDirection(c)
		if l := math.Sqrt(dx*dx + dy*dy + dz*dz); math.Abs(l-1) > 1e-9 {
			t.Errorf("aimDirection(%+v) length %v, want unit", c, l)
		}
	}
	dx, dy, dz := aimDirection(raster.Camera{Angle: 0, Pitch: 0})
	if math.Abs(dx-1) > 1e-9 || math.Abs(dy) > 1e-9 || math.Abs(dz) > 1e-9 {
		t.Errorf("east/level = (%v,%v,%v), want (1,0,0)", dx, dy, dz)
	}
	if _, _, dz := aimDirection(raster.Camera{Angle: 0, Pitch: 0.5}); dz <= 0 {
		t.Errorf("positive pitch dz=%v, want > 0 (aims up)", dz)
	}
}

func TestFrameAt(t *testing.T) {
	frames := []WeaponFrame{{'A', 4}, {'B', 6}, {'C', 4}}
	cases := []struct {
		tics  float64
		frame byte
		ok    bool
	}{
		{0, 'A', true}, {3.9, 'A', true},
		{4, 'B', true}, {9.9, 'B', true},
		{10, 'C', true}, {13.9, 'C', true},
		{14, 0, false}, {100, 0, false},
	}
	for _, c := range cases {
		f, ok := frameAt(frames, c.tics)
		if ok != c.ok || (ok && f.Frame != c.frame) {
			t.Errorf("frameAt(%.1f) = (%q,%v), want (%q,%v)", c.tics, f.Frame, ok, c.frame, c.ok)
		}
	}
	if _, ok := frameAt(nil, 0); ok {
		t.Error("frameAt(nil) ok=true, want false")
	}
}

func TestClamp(t *testing.T) {
	if clamp(5, 0, 10) != 5 || clamp(-1, 0, 10) != 0 || clamp(11, 0, 10) != 10 {
		t.Error("clamp bounds wrong")
	}
}

func TestExplosionFrameProgression(t *testing.T) {
	p := &Projectile{def: &ProjectileDef{ExplodeFrames: []byte{'A', 'B', 'C'}, ExplodeTics: 4}}

	p.explodeElapsed = 0
	if f, ok := p.explosionFrame(); !ok || f != 'A' {
		t.Errorf("t=0: (%q,%v) want ('A',true)", f, ok)
	}
	p.explodeElapsed = 4.0/ticsPerSecond + 1e-4
	if f, ok := p.explosionFrame(); !ok || f != 'B' {
		t.Errorf("one frame in: (%q,%v) want ('B',true)", f, ok)
	}
	p.explodeElapsed = 12.0/ticsPerSecond + 1e-4
	if _, ok := p.explosionFrame(); ok {
		t.Error("past the last frame: ok=true, want false")
	}
}
