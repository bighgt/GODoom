package engine

import (
	"math"

	"twopointfive/bsp"
	"twopointfive/wad"
)

// Door timing, ported from id's original p_doors.c: VDOORSPEED (2 map
// units per 1/35s tic = 70 units/sec) and VDOORWAIT (150 tics ≈ 4.29s).
const (
	doorSpeed       = 70.0
	doorWaitSeconds = 150.0 / 35.0
	useRange        = 64.0 // USERANGE: how far the space-bar "use" reaches
)

// manualDoorSpecials maps a Linedef.SpecialType to whether the door it
// operates opens and then stays open (true, the "D1" family) or opens,
// waits, and closes again (false, the "DR" family) — the vanilla
// manual/use-activated door specials. Keycard-locked variants (26-28,
// 32-34) are treated the same as their unlocked counterparts for now:
// there's no key inventory system yet (see game_design.txt).
var manualDoorSpecials = map[uint16]bool{
	1: false, 26: false, 27: false, 28: false, 117: false,
	31: true, 32: true, 33: true, 34: true, 118: true,
}

type doorPhase int

const (
	doorOpening doorPhase = iota
	doorWaitingOpen
	doorClosing
)

// doorThinker animates one Sector's ceiling height between its closed and
// open positions — Doom's own term for this kind of small, independent
// per-frame update is a "thinker" (p_tick.c's thinker_t list); this
// project's version is deliberately just a map of them rather than a
// generic linked-list/thinker framework, since doors are the only kind so far.
type doorThinker struct {
	stayOpen      bool
	phase         doorPhase
	current       float64 // the sector's animated ceiling height
	closedHeight  float64
	openHeight    float64
	waitRemaining float64
}

// tryUseDoor is the space-bar "use" action: cast a ray from the camera
// along its facing direction, find the nearest door-special linedef within
// useRange, and activate it. Mirrors P_UseLines' role in the original,
// simplified to only the "manual" door family bound directly to the used
// line (not the tag-based remote-trigger families — see game_design.txt).
func (g *Game) tryUseDoor() {
	ux, uy := math.Cos(g.Camera.Angle), math.Sin(g.Camera.Angle)

	bestDist := math.Inf(1)
	bestLine := -1
	for i := range g.Level.Linedefs {
		ld := &g.Level.Linedefs[i]
		if _, ok := manualDoorSpecials[ld.SpecialType]; !ok {
			continue
		}
		v1 := g.Level.Vertexes[ld.StartVertex]
		v2 := g.Level.Vertexes[ld.EndVertex]
		dist, hit := rayIntersectsSegment(g.Camera.X, g.Camera.Y, ux, uy,
			float64(v1.X), float64(v1.Y), float64(v2.X), float64(v2.Y))
		if hit && dist <= useRange && dist < bestDist {
			bestDist, bestLine = dist, i
		}
	}
	if bestLine >= 0 {
		g.activateDoor(bestLine)
	}
}

func (g *Game) activateDoor(lineIdx int) {
	ld := &g.Level.Linedefs[lineIdx]
	if ld.BackSidedef == wad.NoSidedef {
		return
	}
	stayOpen := manualDoorSpecials[ld.SpecialType]

	sectorIdx := g.resolveDoorSector(ld)
	if sectorIdx < 0 {
		return
	}

	if d, exists := g.doorThinkers[sectorIdx]; exists {
		switch d.phase {
		case doorClosing:
			d.phase = doorOpening
			g.playSound("DSDOROPN")
		case doorWaitingOpen:
			d.waitRemaining = doorWaitSeconds
		}
		return
	}

	g.playSound("DSDOROPN")
	closedHeight := float64(g.Level.Sectors[sectorIdx].CeilingHeight)
	if g.doorThinkers == nil {
		g.doorThinkers = make(map[int]*doorThinker)
	}
	g.doorThinkers[sectorIdx] = &doorThinker{
		stayOpen:     stayOpen,
		phase:        doorOpening,
		current:      closedHeight,
		closedHeight: closedHeight,
		openHeight:   g.lowestAdjacentCeiling(sectorIdx) - 4,
	}
}

// resolveDoorSector picks whichever of ld's two sectors is *not* the one
// the player is currently standing in — the common case where a door is
// its own small sector bordering the hallway the player used it from,
// matching EV_VerticalDoor's `sides[line->sidenum[side^1]].sector` (move
// the side opposite the player).
func (g *Game) resolveDoorSector(ld *wad.Linedef) int {
	current := bsp.PointSector(g.BSP, g.Level, float32(g.Camera.X), float32(g.Camera.Y))
	frontSD := g.Level.Sidedefs[ld.FrontSidedef]
	backSD := g.Level.Sidedefs[ld.BackSidedef]
	if current != nil && current == &g.Level.Sectors[frontSD.Sector] {
		return int(backSD.Sector)
	}
	return int(frontSD.Sector)
}

// lowestAdjacentCeiling finds the lowest ceiling height among every sector
// that shares a two-sided linedef with sectorIdx — P_FindLowestCeilingSurrounding
// in the original, used to decide how far a door should open.
func (g *Game) lowestAdjacentCeiling(sectorIdx int) float64 {
	lowest := math.Inf(1)
	for i := range g.Level.Linedefs {
		ld := &g.Level.Linedefs[i]
		if ld.BackSidedef == wad.NoSidedef {
			continue
		}
		frontSec := int(g.Level.Sidedefs[ld.FrontSidedef].Sector)
		backSec := int(g.Level.Sidedefs[ld.BackSidedef].Sector)

		var other int
		switch sectorIdx {
		case frontSec:
			other = backSec
		case backSec:
			other = frontSec
		default:
			continue
		}
		if h := float64(g.Level.Sectors[other].CeilingHeight); h < lowest {
			lowest = h
		}
	}
	if math.IsInf(lowest, 1) {
		return float64(g.Level.Sectors[sectorIdx].CeilingHeight)
	}
	return lowest
}

// updateDoors advances every active door thinker by dt seconds and writes
// its animated ceiling height straight into g.Level.Sectors — the same
// slice the renderer and collision code read every frame, so an opening
// door is immediately visible and immediately walkable through, with no
// extra plumbing needed between the three systems.
func (g *Game) updateDoors(dt float64) {
	for sectorIdx, d := range g.doorThinkers {
		switch d.phase {
		case doorOpening:
			d.current += doorSpeed * dt
			if d.current >= d.openHeight {
				d.current = d.openHeight
				if d.stayOpen {
					delete(g.doorThinkers, sectorIdx)
				} else {
					d.phase = doorWaitingOpen
					d.waitRemaining = doorWaitSeconds
				}
			}
		case doorWaitingOpen:
			d.waitRemaining -= dt
			if d.waitRemaining <= 0 {
				d.phase = doorClosing
				g.playSound("DSDORCLS")
			}
		case doorClosing:
			d.current -= doorSpeed * dt
			if d.current <= d.closedHeight {
				d.current = d.closedHeight
				delete(g.doorThinkers, sectorIdx)
			}
		}
		g.Level.Sectors[sectorIdx].CeilingHeight = int16(math.Round(d.current))
	}
}

// rayIntersectsSegment finds where the ray from (ox, oy) in unit direction
// (dx, dy) crosses the segment (x1,y1)-(x2,y2), returning the distance
// along the ray (valid since (dx,dy) is a unit vector) and whether it hit
// within the segment's bounds and in front of the ray's origin.
func rayIntersectsSegment(ox, oy, dx, dy, x1, y1, x2, y2 float64) (float64, bool) {
	v1x, v1y := ox-x1, oy-y1
	v2x, v2y := x2-x1, y2-y1
	v3x, v3y := -dy, dx

	denom := v2x*v3x + v2y*v3y
	if math.Abs(denom) < 1e-9 {
		return 0, false
	}

	t1 := (v2x*v1y - v2y*v1x) / denom
	t2 := (v1x*v3x + v1y*v3y) / denom
	if t1 >= 0 && t2 >= 0 && t2 <= 1 {
		return t1, true
	}
	return 0, false
}
