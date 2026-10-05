package engine

import (
	"testing"

	"twopointfive/engine/slopes"
)

func TestPlaneTilt(t *testing.T) {
	// A floor rising 0.5 units per unit of +X, flat in Y: through (0,0,64),
	// (128,0,128), (0,64,64).
	tiltX, tiltY, ok := slopes.PlaneTilt([3]float64{0, 0, 64}, [3]float64{128, 0, 128}, [3]float64{0, 64, 64})
	if !ok {
		t.Fatal("expected a valid plane")
	}
	if tiltX != 0.5 || tiltY != 0 {
		t.Errorf("tiltX=%v tiltY=%v, want 0.5, 0", tiltX, tiltY)
	}

	// Collinear points: no unique plane.
	if _, _, ok := slopes.PlaneTilt([3]float64{0, 0, 0}, [3]float64{1, 0, 0}, [3]float64{2, 0, 0}); ok {
		t.Error("collinear points should not resolve to a plane")
	}
}

// TestApplySlopesTiltsTheNamedSector: an end-to-end check of the WAD-glue
// path (applySlopes), not just slopes.PlaneTilt's math — g.slopeDefs, keyed by
// upper-cased map name, must land on the right sector's FloorTiltX/Y,
// SlopePivotX/Y and FloorHeight (P1's height, since P1 is the pivot).
func TestApplySlopesTiltsTheNamedSector(t *testing.T) {
	g := loadRealLevel(t, "../testdata/DOOM1.WAD", "E1M1")
	const sec = 5
	before := g.Level.Sectors[sec]

	g.slopeDefs = map[string][]slopes.Def{
		"E1M1": {
			{SectorIndex: sec, Ceiling: false,
				P1: [3]float64{100, 200, 64}, P2: [3]float64{228, 200, 128}, P3: [3]float64{100, 264, 64}},
		},
	}
	g.applySlopes()

	got := g.Level.Sectors[sec]
	if got.FloorHeight != 64 {
		t.Errorf("FloorHeight = %v, want 64 (P1's height)", got.FloorHeight)
	}
	if got.SlopePivotX != 100 || got.SlopePivotY != 200 {
		t.Errorf("pivot = (%v,%v), want (100,200)", got.SlopePivotX, got.SlopePivotY)
	}
	if got.FloorTiltX != 0.5 || got.FloorTiltY != 0 {
		t.Errorf("tilt = (%v,%v), want (0.5,0)", got.FloorTiltX, got.FloorTiltY)
	}
	// Nothing else about the sector (ceiling, textures, special) changed.
	if got.CeilingHeight != before.CeilingHeight || got.FloorTexture != before.FloorTexture {
		t.Errorf("unrelated sector fields changed: got %+v, before %+v", got, before)
	}

	// A different map name must not touch this sector.
	g.Level.Sectors[sec] = before
	g.slopeDefs = map[string][]slopes.Def{"MAP01": {{SectorIndex: sec, P1: [3]float64{0, 0, 999}, P2: [3]float64{1, 0, 0}, P3: [3]float64{0, 1, 0}}}}
	g.applySlopes()
	if g.Level.Sectors[sec] != before {
		t.Errorf("a def for a different map name changed sector %d anyway", sec)
	}
}
