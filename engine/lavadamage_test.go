package engine

import (
	"testing"

	"twopointfive/bsp"
)

// Regression for "jumping over lava/acid still deals damage": the damage
// floors in playerInSpecialSector must only fire while the player's feet
// are actually resting on the floor (onGround), not merely because the
// player's (x, y) is over a damaging sector. See specials.go.
func TestDamageFloorOnlyHurtsWhileOnGround(t *testing.T) {
	g := loadRealLevel(t, "../testdata/DOOM1.WAD", "E1M1")
	g.mobjs = nil // only the sector's own damage floor should touch the player, not monster AI

	// Sector 55 in E1M1: a NUKAGE3 pool, special 7 (5% damage every 32
	// tics), floor -48.
	const nukageSector = 55
	g.Camera.X, g.Camera.Y = 2848, -3472
	sec := bsp.PointSector(g.BSP, g.Level, float32(g.Camera.X), float32(g.Camera.Y))
	if sec == nil || sec.SpecialType != 7 || sec != &g.Level.Sectors[nukageSector] {
		t.Fatalf("test point not over sector %d (special 7): got %+v", nukageSector, sec)
	}

	// before/after rather than against a fresh DefaultPlayerStats() each
	// time, since armor/starting stats can carry config defaults that
	// aren't necessarily 0.
	run := func(onGround bool) (before, after int) {
		g.Player = DefaultPlayerStats()
		before = g.Player.Health
		g.playerMobj.Health = g.Player.Health
		g.playerMobj.Flags |= MF_SHOOTABLE
		g.playerDead = false
		g.groundPlayer(float64(sec.FloorHeight))
		g.onGround = onGround
		if !onGround {
			// Airborne but still low — just off the ground at the start of
			// a jump — with a landing-squat viewDip left over from an
			// unrelated hard landing just before this jump. That combination
			// is what fooled the old Camera.Z-based check into still
			// "seeing" the floor even though the feet were genuinely up.
			g.PlayerZ = float64(sec.FloorHeight) + 2
			g.viewDip = -12
		}
		g.Camera.Z = g.PlayerZ + EyeHeight + g.viewDip
		for i := 0; i < 64; i++ { // >32 tics so the damage-floor throttle fires at least once
			g.runTic()
		}
		return before, g.Player.Health
	}

	if before, after := run(false); after != before {
		t.Errorf("airborne over the nukage pool: health %d -> %d, want unchanged", before, after)
	}
	if before, after := run(true); after >= before {
		t.Errorf("standing in the nukage pool: health %d -> %d, want a decrease", before, after)
	}
}
