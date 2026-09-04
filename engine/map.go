package engine

import (
	"math"

	"twopointfive/wad"
)

// Movement clipping for any Mobj, ported from id's p_map.c (P_CheckPosition
// / P_TryMove) — the player's own move code in collision.go is a separate,
// older path that will fold into this later. Monsters walk by A_Chase ->
// P_Move -> P_TryMove; missiles (Phase 3) will move through here too.

// Linedef.Flags bits used by movement (Doom specs).
const (
	mlBlocking      = 0x0001 // blocks players and monsters
	mlBlockMonsters = 0x0002 // blocks monsters only
)

// Results of the last P_CheckPosition, id's tm* globals. Read by P_TryMove.
type checkPosResult struct {
	floorZ   float64 // highest floor under the destination footprint
	ceilingZ float64 // lowest ceiling over it
	dropoffZ float64 // lowest floor touching it (for monster ledge avoidance)
}

// pCheckPosition reports whether mo can occupy (x, y) without overlapping a
// solid line or solid thing, and fills res with the floor/ceiling/dropoff
// heights of the destination footprint. It does NOT apply step-up or
// dropoff rules — that's P_TryMove.
func (g *Game) pCheckPosition(mo *Mobj, x, y float64, res *checkPosResult) bool {
	res.floorZ, res.ceilingZ = g.sectorFloorCeil(x, y)
	res.dropoffZ = res.floorZ

	isMonster := mo.Info != nil && mo.Info.Flags&MF_COUNTKILL != 0

	blocked := false
	g.blockGrid.forEachNear(x, y, mo.Radius, func(i int32) bool {
		ld := &g.Level.Linedefs[i]
		v1 := g.Level.Vertexes[ld.StartVertex]
		v2 := g.Level.Vertexes[ld.EndVertex]
		if distancePointToSegment(x, y, float64(v1.X), float64(v1.Y), float64(v2.X), float64(v2.Y)) >= mo.Radius {
			return true
		}

		if ld.BackSidedef == wad.NoSidedef {
			blocked = true
			return false
		}
		if ld.Flags&mlBlocking != 0 || (isMonster && ld.Flags&mlBlockMonsters != 0) {
			blocked = true
			return false
		}

		open := g.wallOpeningAt(ld)
		if !open.twoSided {
			blocked = true
			return false
		}
		if open.top < res.ceilingZ {
			res.ceilingZ = open.top
		}
		if open.bottom > res.floorZ {
			res.floorZ = open.bottom
		}
		if open.lowFloor < res.dropoffZ {
			res.dropoffZ = open.lowFloor
		}
		return true
	})
	if blocked {
		return false
	}

	// Solid things — the live mobjs, plus the player's body. Linear scan;
	// the count is small and a spatial index can come later if it matters.
	solidThingHit := func(other *Mobj) bool {
		if other == nil || other == mo || other.removed || other.Flags&MF_SOLID == 0 {
			return false
		}
		block := other.Radius + mo.Radius
		return math.Abs(other.X-x) < block && math.Abs(other.Y-y) < block
	}
	if solidThingHit(g.playerMobj) {
		return false
	}
	for _, other := range g.mobjs {
		// (Phase 3: MF_MISSILE impact damage. Phase 4: MF_SPECIAL pickup.)
		if solidThingHit(other) {
			return false
		}
	}
	return true
}

// pTryMove attempts to move mo to (x, y), applying id's P_TryMove rules:
// the footprint must be clear, tall enough to stand in, no step up over
// maxStepUp, and (for a walking monster) no step off a ledge taller than
// maxStepUp. On success mo is repositioned and its floor/ceiling cached.
func (g *Game) pTryMove(mo *Mobj, x, y float64) bool {
	var res checkPosResult
	if !g.pCheckPosition(mo, x, y, &res) {
		return false
	}

	if mo.Flags&MF_NOCLIP == 0 {
		if res.ceilingZ-res.floorZ < mo.Height {
			return false // no room to stand
		}
		if mo.Flags&MF_TELEPORT == 0 && res.ceilingZ-mo.Z < mo.Height {
			return false // would bump its head (a floater moving up)
		}
		if mo.Flags&MF_TELEPORT == 0 && res.floorZ-mo.Z > maxStepUp {
			return false // step too high
		}
		if mo.Flags&(MF_DROPOFF|MF_FLOAT) == 0 && res.floorZ-res.dropoffZ > maxStepUp {
			return false // walking monster won't step off a ledge
		}
	}

	mo.X, mo.Y = x, y
	mo.FloorZ, mo.CeilingZ = res.floorZ, res.ceilingZ
	return true
}
