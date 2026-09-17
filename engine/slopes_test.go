package engine

import (
	"testing"

	"twopointfive/bsp"
)

// TestResolvePlayerZTracksSlope: standing on a tilted sector, resolvePlayerZ
// must settle the player's feet onto the plane's own FloorZAt(x, y) at
// wherever the camera currently is — not the sector's flat scalar height —
// so walking across a sloped floor rises/falls continuously instead of
// snapping to one height or stair-stepping. Regression for the sector
// height reads specials.go/game.go/collision.go/mobj.go were converted to
// FloorZAt/CeilZAt for (sloped-floor support).
//
// Reuses sector 55 from DOOM1.WAD E1M1 (a ~192x224 unit NUKAGE3 pool,
// floor -48) — the same sector TestDamageFloorOnlyHurtsWhileOnGround
// (lavadamage_test.go) already confirmed resolves at its center via
// bsp.PointSector, so the walk below stays comfortably inside its
// footprint rather than guessing at unfamiliar map geometry.
func TestResolvePlayerZTracksSlope(t *testing.T) {
	g := loadRealLevel(t, "../testdata/DOOM1.WAD", "E1M1")

	const nukageSector = 55
	const cx, cy = 2848.0, -3472.0
	sec := &g.Level.Sectors[nukageSector]
	if got := bsp.PointSector(g.BSP, g.Level, float32(cx), float32(cy)); got != sec {
		t.Fatalf("test point (%v,%v) not over sector %d: got %+v", cx, cy, nukageSector, got)
	}

	sec.FloorTiltX, sec.FloorTiltY = 0.5, 0
	sec.SlopePivotX, sec.SlopePivotY = cx, cy
	sec.CeilingHeight = sec.FloorHeight + 200 // generous headroom for the whole walk

	g.Camera.X, g.Camera.Y = cx, cy
	g.groundPlayer(sec.FloorZAt(cx, cy))

	// Walk 40 map units along the tilted (X) axis — well inside the
	// sector's ~192-unit width — re-settling each step. fallStep snaps
	// feet-at-or-under-floor straight to the floor, so a handful of tics
	// comfortably converges even from a standing start with no initial
	// downward velocity.
	for dx := 0.0; dx <= 20; dx += 5 {
		g.Camera.X = cx + dx
		if got := bsp.PointSector(g.BSP, g.Level, float32(g.Camera.X), float32(g.Camera.Y)); got != sec {
			t.Fatalf("dx=%v: point (%v,%v) left sector %d's actual polygon (its bbox is looser than its shape) — got %+v",
				dx, g.Camera.X, g.Camera.Y, nukageSector, got)
		}
		for i := 0; i < 5; i++ {
			g.resolvePlayerZ(1.0 / 35)
		}
		want := sec.FloorZAt(g.Camera.X, g.Camera.Y)
		if got := g.PlayerZ; got < want-0.01 || got > want+0.01 {
			t.Errorf("dx=%v: PlayerZ = %v, want %v (the slope's own height there)", dx, got, want)
		}
	}

	// The slope actually changed something: the far end must differ from
	// the pivot's height, or this test would pass vacuously against the
	// pre-slope flat-scalar code too.
	if sec.FloorZAt(cx+20, cy) == sec.FloorZAt(cx, cy) {
		t.Fatal("test setup: slope produced no height change across the walk")
	}
}
