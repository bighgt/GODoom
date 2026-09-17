package engine

import (
	"testing"

	"twopointfive/bsp"
)

// TestRoomOverRoomShieldsFromBaseSectorDamage: standing on a room-over-room
// platform puts the player logically inside the platform's CONTROL
// sector's airspace (planeAt's activeIdx) — so a damaging pool with a
// platform floating over it should NOT hurt someone standing on the
// platform, even though their (x, y) is still over the pool. Reuses
// sector 55 from DOOM1.WAD E1M1 (a NUKAGE3 pool, special 7, floor -48,
// ceiling 176 — the same sector TestDamageFloorOnlyHurtsWhileOnGround
// already confirmed resolves at its center via bsp.PointSector).
func TestRoomOverRoomShieldsFromBaseSectorDamage(t *testing.T) {
	g := loadRealLevel(t, "../testdata/DOOM1.WAD", "E1M1")
	g.mobjs = nil // only sector specials should touch the player, not monster AI

	const poolSector = 55
	const platformSector = 0
	const cx, cy = 2848.0, -3472.0
	pool := &g.Level.Sectors[poolSector]
	if got := bsp.PointSector(g.BSP, g.Level, float32(cx), float32(cy)); got != pool {
		t.Fatalf("test point (%v,%v) not over sector %d: got %+v", cx, cy, poolSector, got)
	}
	if pool.SpecialType != 7 {
		t.Fatalf("sector %d SpecialType = %d, want 7 (damaging) — test assumption stale", poolSector, pool.SpecialType)
	}

	// An 8-unit-thick, non-damaging platform floating well inside the
	// pool's own [-48, 176] vertical span.
	platform := &g.Level.Sectors[platformSector]
	platform.SpecialType = 0
	platform.FloorHeight, platform.CeilingHeight = 50, 58

	g.extraFloors = map[int][]ExtraFloor{poolSector: {{ControlSector: platformSector}}}
	g.Camera.X, g.Camera.Y = cx, cy

	run := func(z float64) (before, after int) {
		g.Player = DefaultPlayerStats()
		before = g.Player.Health
		g.playerMobj.Health = g.Player.Health
		g.playerMobj.Flags |= MF_SHOOTABLE
		g.playerDead = false
		g.groundPlayer(z)
		g.Camera.Z = z + EyeHeight
		for i := 0; i < 64; i++ { // >32 tics so the damage-floor throttle fires at least once
			g.runTic()
		}
		return before, g.Player.Health
	}

	if before, after := run(float64(pool.FloorHeight)); after >= before {
		t.Errorf("standing in the pool (no platform underfoot): health %d -> %d, want a decrease", before, after)
	}
	if before, after := run(float64(platform.CeilingHeight)); after != before {
		t.Errorf("standing ON the platform above the pool: health %d -> %d, want unchanged (shielded by the platform's own non-damaging sector)", before, after)
	}
}
