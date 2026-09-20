package engine

import (
	"testing"

	"twopointfive/bsp"
)

// TestPlayerLiquidCrossingFiresOnce mirrors TestDamageFloorOnlyHurtsWhileOnGround's
// setup (lavadamage_test.go). Height-based, not g.onGround-based (see
// liquidContactTolerance's doc comment for why reusing onGround was wrong):
// flying well above the pool must not trigger a splash, an ordinary in-place
// jump over it must not spuriously "exit" mid-hop, and landing/leaving must
// each fire exactly one splash.
func TestPlayerLiquidCrossingFiresOnce(t *testing.T) {
	g := loadRealLevel(t, "../testdata/DOOM1.WAD", "E1M1")
	g.mobjs = nil
	g.liquidAmbience = true

	// Sector 55 in E1M1: a NUKAGE3 pool, floor -48 (see lavadamage_test.go).
	const nukageSector = 55
	g.Camera.X, g.Camera.Y = 2848, -3472
	sec := bsp.PointSector(g.BSP, g.Level, float32(g.Camera.X), float32(g.Camera.Y))
	if sec == nil || sec != &g.Level.Sectors[nukageSector] {
		t.Fatalf("test point not over sector %d: got %+v", nukageSector, sec)
	}

	var splashes int
	g.liquidCrossingHook = func(x, y, z float64, kind int) { splashes++ }

	// Well above the pool (clearing it in a long jump, not touching down):
	// no splash.
	g.playerLiquidKind = liqNone
	g.PlayerZ = float64(sec.FloorHeight) + 200
	for i := 0; i < 8; i++ {
		g.checkPlayerLiquidCrossing()
	}
	if splashes != 0 {
		t.Errorf("well above the nukage pool: got %d splashes, want 0", splashes)
	}

	// Standing in it, then an ordinary in-place jump (apex ~46 units, see
	// jumpImpulse): must not read as leaving mid-hop.
	g.PlayerZ = float64(sec.FloorHeight)
	g.checkPlayerLiquidCrossing()
	if splashes != 1 {
		t.Fatalf("landing in the nukage pool: got %d splashes, want 1", splashes)
	}
	if g.playerLiquidKind != liqAcid {
		t.Fatalf("playerLiquidKind = %d, want liqAcid (%d)", g.playerLiquidKind, liqAcid)
	}
	for _, dz := range []float64{10, 30, 46, 30, 10, 0} { // one hop's worth of arc
		g.PlayerZ = float64(sec.FloorHeight) + dz
		g.checkPlayerLiquidCrossing()
	}
	if splashes != 1 {
		t.Errorf("in-place jump while standing in the pool: got %d splashes, want still 1 (no false exit/enter)", splashes)
	}

	// Leaving it for dry land: exactly one more splash, at the kind just left.
	g.Camera.X, g.Camera.Y = 0, 0 // wherever E1M1's start sector is — definitely not liquid
	sec2 := bsp.PointSector(g.BSP, g.Level, float32(g.Camera.X), float32(g.Camera.Y))
	if sec2 == nil {
		t.Fatal("test point (0,0) has no sector — pick a different non-liquid point")
	}
	g.PlayerZ = float64(sec2.FloorHeight)
	g.checkPlayerLiquidCrossing()
	if splashes != 2 {
		t.Errorf("exiting the nukage pool: got %d total splashes, want 2", splashes)
	}
	if g.playerLiquidKind != liqNone {
		t.Errorf("playerLiquidKind after exiting = %d, want liqNone", g.playerLiquidKind)
	}
}
