package engine

import (
	"math"

	"twopointfive/bsp"
	"twopointfive/wad"
)

// Movement/collision constants, ported from id's original p_local.h:
// PLAYERRADIUS (16 map units) and VIEWHEIGHT-adjacent player height
// (56 units — the standard Doom thing height used for opening checks),
// and STEPSIZE (24 units, the tallest step the player can walk up without
// it counting as a wall).
const (
	playerRadius = 16.0
	playerHeight = 56.0
	maxStepUp    = 24.0
)

// tryMove attempts to move the camera by (dx, dy), sliding along a wall
// instead of stopping dead if the direct path is blocked: try the full
// move, then X-only, then Y-only — a simple approximation of Doom's own
// axis-separated movement clipping (PIT_CheckLine via P_TryMove), good
// enough to walk smoothly along walls that aren't axis-aligned.
func (g *Game) tryMove(dx, dy float64) {
	fromFloor := 0.0
	if sec := bsp.PointSector(g.BSP, g.Level, float32(g.Camera.X), float32(g.Camera.Y)); sec != nil {
		fromFloor = float64(sec.FloorHeight)
	}

	if g.canMoveTo(g.Camera.X+dx, g.Camera.Y+dy, fromFloor) {
		g.Camera.X += dx
		g.Camera.Y += dy
		return
	}
	if g.canMoveTo(g.Camera.X+dx, g.Camera.Y, fromFloor) {
		g.Camera.X += dx
		return
	}
	if g.canMoveTo(g.Camera.X, g.Camera.Y+dy, fromFloor) {
		g.Camera.Y += dy
	}
}

// canMoveTo reports whether a player-radius circle centered at (x, y) can
// stand there, given fromFloor (the floor height of the sector the player
// is moving from, used for the step-height check below).
func (g *Game) canMoveTo(x, y, fromFloor float64) bool {
	for i := range g.Level.Linedefs {
		if g.lineBlocks(&g.Level.Linedefs[i], x, y, fromFloor) {
			return false
		}
	}
	return true
}

// lineBlocks mirrors the two checks Doom's own PIT_CheckLine made against
// each nearby linedef: a one-sided line is always solid; a two-sided line
// only blocks if the opening between the sectors it borders is too short
// to stand in, or too high a step up from fromFloor to climb.
func (g *Game) lineBlocks(ld *wad.Linedef, x, y, fromFloor float64) bool {
	v1 := g.Level.Vertexes[ld.StartVertex]
	v2 := g.Level.Vertexes[ld.EndVertex]
	if distancePointToSegment(x, y, float64(v1.X), float64(v1.Y), float64(v2.X), float64(v2.Y)) >= playerRadius {
		return false
	}

	if ld.BackSidedef == wad.NoSidedef {
		return true
	}
	if int(ld.FrontSidedef) >= len(g.Level.Sidedefs) || int(ld.BackSidedef) >= len(g.Level.Sidedefs) {
		return true
	}
	frontSD := g.Level.Sidedefs[ld.FrontSidedef]
	backSD := g.Level.Sidedefs[ld.BackSidedef]
	if int(frontSD.Sector) >= len(g.Level.Sectors) || int(backSD.Sector) >= len(g.Level.Sectors) {
		return true
	}
	front := g.Level.Sectors[frontSD.Sector]
	back := g.Level.Sectors[backSD.Sector]

	openBottom := math.Max(float64(front.FloorHeight), float64(back.FloorHeight))
	openTop := math.Min(float64(front.CeilingHeight), float64(back.CeilingHeight))

	if openTop-openBottom < playerHeight {
		return true // too short to stand in (includes a fully-closed door)
	}
	if openBottom-fromFloor > maxStepUp {
		return true // too high a step to climb
	}
	return false
}

func distancePointToSegment(px, py, x1, y1, x2, y2 float64) float64 {
	dx, dy := x2-x1, y2-y1
	lenSq := dx*dx + dy*dy
	if lenSq == 0 {
		return math.Hypot(px-x1, py-y1)
	}
	t := clamp(((px-x1)*dx+(py-y1)*dy)/lenSq, 0, 1)
	cx, cy := x1+t*dx, y1+t*dy
	return math.Hypot(px-cx, py-cy)
}
