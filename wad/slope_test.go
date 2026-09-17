package wad

import "testing"

// TestFloorZAtDegeneratesToScalar: every vanilla sector (zero tilt, the
// zero value of the new fields) must read back exactly the flat scalar
// height at any (x, y) — the backward-compatibility guarantee the whole
// sloped-floor representation depends on.
func TestFloorZAtDegeneratesToScalar(t *testing.T) {
	s := &Sector{FloorHeight: 64, CeilingHeight: 192}
	for _, p := range [][2]float64{{0, 0}, {1000, -500}, {-99999, 42}} {
		if got := s.FloorZAt(p[0], p[1]); got != 64 {
			t.Errorf("FloorZAt%v = %v, want 64 (flat)", p, got)
		}
		if got := s.CeilZAt(p[0], p[1]); got != 192 {
			t.Errorf("CeilZAt%v = %v, want 192 (flat)", p, got)
		}
	}
}

// TestFloorZAtVariesLinearly: a tilted sector's height changes linearly
// with distance from its pivot along each tilt axis, and the pivot itself
// reads back exactly the untilted scalar (FloorHeight is "the height at
// the pivot," not "at the world origin" — see the Sector doc comment).
func TestFloorZAtVariesLinearly(t *testing.T) {
	s := &Sector{
		FloorHeight: 0, FloorTiltX: 0.5, FloorTiltY: -0.25,
		SlopePivotX: 100, SlopePivotY: 200,
	}
	if got := s.FloorZAt(100, 200); got != 0 {
		t.Errorf("FloorZAt(pivot) = %v, want 0 (== FloorHeight)", got)
	}
	if got := s.FloorZAt(120, 200); got != 10 { // +20 map units of run * 0.5 rise/run
		t.Errorf("FloorZAt(+20 run on X) = %v, want 10", got)
	}
	if got := s.FloorZAt(100, 240); got != -10 { // +40 run * -0.25 rise/run
		t.Errorf("FloorZAt(+40 run on Y) = %v, want -10", got)
	}
	if got := s.FloorZAt(120, 240); got != 0 { // +10 (X) -10 (Y) cancel
		t.Errorf("FloorZAt(both axes) = %v, want 0", got)
	}
}
