package engine

import "testing"

func TestBuildLiquidAmbientSectors(t *testing.T) {
	g := &Game{}
	g.Level = fakeLiquidLevel() // sector 0: LAVA1, sector 1: NUKAGE1 (liquidlights_test.go)
	g.ensureSectorIndex()

	g.buildLiquidAmbientSectors()

	if len(g.liquidAmbientSectors) != 2 {
		t.Fatalf("got %d liquid ambient sectors, want 2", len(g.liquidAmbientSectors))
	}
	var lava, acid bool
	for _, s := range g.liquidAmbientSectors {
		switch s.kind {
		case liqLava:
			lava = true
		case liqAcid:
			acid = true
		}
	}
	if !lava || !acid {
		t.Errorf("expected one lava and one acid sector, got lava=%v acid=%v", lava, acid)
	}
}

// TestUpdateLiquidAmbienceRespectsDistanceCull checks the cull itself
// (whether a sector's spawn timer gets touched at all), not whether a
// particle actually lands in g.particles — the underlying art is a
// gitignored, locally-supplied asset pack (see loadSmokeFrames), so
// asserting on actual spawned particles here would make the test's outcome
// depend on the machine it runs on.
func TestUpdateLiquidAmbienceRespectsDistanceCull(t *testing.T) {
	g := &Game{liquidAmbience: true}
	g.Level = fakeLiquidLevel()
	g.ensureSectorIndex()
	g.buildLiquidAmbientSectors()
	if len(g.liquidAmbientSectors) == 0 {
		t.Fatal("expected liquid ambient sectors to be built")
	}

	// Far from every liquid sector: culled before the timer is ever
	// touched, so it should still read its zero value.
	g.Camera.X, g.Camera.Y, g.Camera.Z = 1000000, 1000000, 0
	g.updateLiquidAmbience(1.0)
	for i, s := range g.liquidAmbientSectors {
		if s.timer != 0 {
			t.Errorf("sector %d timer = %v, want 0 (untouched — should have been culled)", i, s.timer)
		}
	}

	// Standing right in sector 0's pool (centre ~(128,128), see
	// fakeLiquidLevel): its timer should move off zero.
	g.Camera.X, g.Camera.Y, g.Camera.Z = 128, 128, 0
	g.updateLiquidAmbience(1.0)
	if g.liquidAmbientSectors[0].timer == 0 {
		t.Error("expected the nearby sector's timer to change after update, still 0")
	}
}

func TestUpdateLiquidAmbienceOffByConfig(t *testing.T) {
	g := &Game{liquidAmbience: false}
	g.Level = fakeLiquidLevel()
	g.ensureSectorIndex()
	g.buildLiquidAmbientSectors()
	g.Camera.X, g.Camera.Y, g.Camera.Z = 128, 128, 0

	g.updateLiquidAmbience(1.0)

	for i, s := range g.liquidAmbientSectors {
		if s.timer != 0 {
			t.Errorf("sector %d timer = %v, want 0 (liquidAmbience is off, update should no-op)", i, s.timer)
		}
	}
}
