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

// tryMove moves the camera by (dx, dy) for this frame. It subdivides the
// move into steps no longer than half the player radius before clipping
// each one, so a fast frame — or a post-hitch dt spike — can't carry the
// player across a wall thinner than playerRadius between collision checks
// (a single unclipped jump would tunnel straight through it).
func (g *Game) tryMove(dx, dy float64) {
	dist := math.Hypot(dx, dy)
	if dist == 0 {
		return
	}
	steps := int(math.Ceil(dist / (playerRadius / 2)))
	sx, sy := dx/float64(steps), dy/float64(steps)
	for i := 0; i < steps; i++ {
		if !g.stepMove(sx, sy) {
			return // wedged against geometry — the rest of the move is moot
		}
	}
}

// stepMove attempts one (dx, dy) increment, sliding along a wall instead of
// stopping dead if the direct path is blocked: try the full move, then
// X-only, then Y-only — a simple approximation of Doom's own axis-separated
// movement clipping (PIT_CheckLine via P_TryMove), good enough to walk
// smoothly along walls that aren't axis-aligned. Reports whether the
// position changed at all.
func (g *Game) stepMove(dx, dy float64) bool {
	// idclip cheat (cheats.go): move straight through everything. Like
	// vanilla MF_NOCLIP the player still falls/rests on sector floors
	// (resolvePlayerZ) and crosses no line specials.
	if g.noclip {
		g.Camera.X, g.Camera.Y = g.Camera.X+dx, g.Camera.Y+dy
		return true
	}
	// The step-up gate is measured from the feet, not the current sector
	// floor — id's P_TryMove tests `tmfloorz - thing->z`. They're equal
	// with your feet on the ground, but mid-fall the feet are higher, so a
	// player dropping past a ledge can still move onto a platform that's
	// within stepping distance of where they *are*, not where they started.
	fromFloor := g.PlayerZ
	if secIdx := bsp.PointSectorIndex(g.BSP, g.Level, float32(g.Camera.X), float32(g.Camera.Y)); secIdx >= 0 {
		if f, _, _ := g.planeAt(secIdx, g.Camera.X, g.Camera.Y, g.PlayerZ); f > fromFloor {
			fromFloor = f // a floor (or a room-over-room band's top) that has risen into us wins
		}
	}
	ox, oy := g.Camera.X, g.Camera.Y

	// Stuck-recovery: if the player's *current* spot already fails the clip
	// (a landing wedged in a pocket too tight for the normal slide, a sector
	// risen into them, an awkward teleport) then normal clipping refuses
	// every direction — each candidate is still inside some blocker. Fall
	// back to "crawl toward daylight": also accept a sub-step that strictly
	// increases the distance to the nearest blocking line. Without this a bad
	// fall is unrecoverable.
	stuck := !g.canMoveTo(ox, oy, fromFloor)
	curClear := math.Inf(1)
	if stuck {
		curClear = g.playerWallClearance(ox, oy, fromFloor)
	}
	accept := func(nx, ny float64) bool {
		if g.canMoveTo(nx, ny, fromFloor) {
			return true
		}
		return stuck && g.playerWallClearance(nx, ny, fromFloor) > curClear+0.01
	}

	moved := false
	switch {
	case accept(ox+dx, oy+dy):
		g.Camera.X, g.Camera.Y = ox+dx, oy+dy
		moved = true
	case accept(ox+dx, oy):
		g.Camera.X = ox + dx
		moved = true
	case accept(ox, oy+dy):
		g.Camera.Y = oy + dy
		moved = true
	}
	if moved {
		g.checkCrossedSpecials(ox, oy, g.Camera.X, g.Camera.Y)
	}
	return moved
}

// canMoveTo reports whether a player-radius circle centered at (x, y) can
// stand there, given fromFloor (the floor height of the sector the player
// is moving from, for the step-height check below).
//
// A two-sided line's opening / step-up only gates the move when the player
// box actually *crosses* that line (the `straddles` flag) — id's
// PIT_CheckLine is likewise only reached once P_BoxOnLineSide says the box
// spans the line. Gating on that is what stops two opposite bugs:
//
//   - Merely being within playerRadius of a two-sided line, but wholly on
//     one side of it, must NOT apply its opening/step rules — otherwise,
//     standing just below a ledge you walked off (its line still within
//     radius) rejects every move, even ones heading away. (This project's
//     earlier single-scan approach had exactly that freeze.)
//   - But once the box *does* reach the line's plane, the step-up must be
//     measured against that line's opening bottom (max of the two sector
//     floors), not only against the destination *center point's* sector —
//     which the center doesn't enter until it's already ~playerRadius past
//     the wall face, letting you visibly walk into / through the wall
//     before the move is finally refused.
//
// A one-sided line is solid regardless of direction. Real Doom exempts the
// player (not monsters, which avoid dropoffs on their own) from any "too
// far to step down" rule — walking off a ledge is always allowed; only
// climbing more than maxStepUp is not.
func (g *Game) canMoveTo(x, y, fromFloor float64) bool {
	if g.anyLineWithin(x, y, playerRadius, func(open wallOpening, straddles bool) bool {
		return lineBlocksPlayer(open, straddles, fromFloor)
	}) {
		return false
	}

	destFloor := fromFloor
	if secIdx := bsp.PointSectorIndex(g.BSP, g.Level, float32(x), float32(y)); secIdx >= 0 {
		// fromFloor doubles as "my current altitude," which is what picks
		// the right room-over-room band at the destination (x, y) — e.g.
		// stepping onto a platform's top vs. staying underneath it.
		var destCeil float64
		destFloor, destCeil, _ = g.planeAt(secIdx, x, y, fromFloor)
		// The nearby-line scan above catches a low opening on a line being
		// crossed, but not a destination sector that's simply too short to
		// stand in on its own (reached across a wide, tall opening whose own
		// clearance is fine). Check the destination sector directly, the
		// way PrBoom seeds tmceilingz from newsubsec->sector.
		if destCeil-destFloor < playerHeight {
			return false
		}
	}
	return destFloor-fromFloor <= maxStepUp
}

// lineBlocksPlayer decides whether one nearby Linedef stops a player-radius
// body at the query point: a one-sided line always does; a two-sided line
// only when the body's box actually crosses it (straddles) AND its opening
// is too short to fit through, its step up from fromFloor is taller than
// STEPSIZE, or its far-side ceiling is too low to fit under from here.
func lineBlocksPlayer(open wallOpening, straddles bool, fromFloor float64) bool {
	if !open.twoSided {
		return true
	}
	if !straddles {
		return false
	}
	return open.top-open.bottom < playerHeight ||
		open.bottom-fromFloor > maxStepUp ||
		open.top-fromFloor < playerHeight
}

// playerWallClearance is the distance from (x, y) to the closest Linedef
// that would block a player-radius body there (lineBlocksPlayer), or
// math.Inf(1) if none is within playerRadius. Only stepMove's stuck-
// recovery uses it — to tell whether a candidate sub-step crawls the
// wedged player toward looser space or deeper in.
func (g *Game) playerWallClearance(x, y, fromFloor float64) float64 {
	best := math.Inf(1)
	g.blockGrid.forEachNear(x, y, playerRadius, func(i int32) bool {
		ld := &g.Level.Linedefs[i]
		v1 := g.Level.Vertexes[ld.StartVertex]
		v2 := g.Level.Vertexes[ld.EndVertex]
		x1, y1, x2, y2 := float64(v1.X), float64(v1.Y), float64(v2.X), float64(v2.Y)
		d := distancePointToSegment(x, y, x1, y1, x2, y2)
		if d >= playerRadius {
			return true
		}
		if d < best && lineBlocksPlayer(g.wallOpeningAt(ld), boxStraddlesLine(x, y, playerRadius, x1, y1, x2, y2), fromFloor) {
			best = d
		}
		return true
	})
	return best
}

// wallOpening is the vertical gap a two-sided Linedef's front/back sectors
// share — the shared fact both player movement (above) and projectile
// flight (projectiles.go) need from a nearby wall, even though they judge
// it differently (a step-height tolerance for the player; none for a
// flying shot).
type wallOpening struct {
	bottom, top float64 // the shared gap: max of the two floors, min of the two ceilings
	lowFloor    float64 // the lower of the two sector floors (for monster dropoff avoidance)
	twoSided    bool
}

// wallOpeningAt resolves ld's opening, or twoSided=false if it's one-sided
// or its sector data doesn't resolve — either way, callers should treat it
// as solid.
//
// For a sloped front/back sector this evaluates each one's plane at BOTH of
// the line's endpoints and takes the tightest reading (highest floor,
// lowest ceiling) rather than the query point's own position — a
// conservative approximation (errs toward "blocked," never "narrower gap
// than actually there") rather than plumbing the query (x, y) through every
// caller. Every untilted sector (still the vanilla default) has all
// endpoint readings agree, so this is byte-for-byte the old sector-wide
// scalar behavior there.
func (g *Game) wallOpeningAt(ld *wad.Linedef) wallOpening {
	if ld.BackSidedef == wad.NoSidedef {
		return wallOpening{}
	}
	if int(ld.FrontSidedef) >= len(g.Level.Sidedefs) || int(ld.BackSidedef) >= len(g.Level.Sidedefs) {
		return wallOpening{}
	}
	frontSD := g.Level.Sidedefs[ld.FrontSidedef]
	backSD := g.Level.Sidedefs[ld.BackSidedef]
	if int(frontSD.Sector) >= len(g.Level.Sectors) || int(backSD.Sector) >= len(g.Level.Sectors) {
		return wallOpening{}
	}
	front := g.Level.Sectors[frontSD.Sector]
	back := g.Level.Sectors[backSD.Sector]
	// Endpoint positions refine the reading for a sloped sector (see the
	// doc comment above); if they don't resolve (e.g. a hand-built test
	// level with no VERTEXES lump at all — the scalar heights are all that
	// matters there anyway) fall back to the flat scalar reading, which
	// FloorZAt/CeilZAt would give at ANY point for an untilted sector, so
	// this is exactly the old behavior, not a degraded one.
	x1, y1, x2, y2 := 0.0, 0.0, 0.0, 0.0
	if int(ld.StartVertex) < len(g.Level.Vertexes) && int(ld.EndVertex) < len(g.Level.Vertexes) {
		v1 := g.Level.Vertexes[ld.StartVertex]
		v2 := g.Level.Vertexes[ld.EndVertex]
		x1, y1, x2, y2 = float64(v1.X), float64(v1.Y), float64(v2.X), float64(v2.Y)
	}
	fFloor := math.Max(front.FloorZAt(x1, y1), front.FloorZAt(x2, y2))
	bFloor := math.Max(back.FloorZAt(x1, y1), back.FloorZAt(x2, y2))
	fCeil := math.Min(front.CeilZAt(x1, y1), front.CeilZAt(x2, y2))
	bCeil := math.Min(back.CeilZAt(x1, y1), back.CeilZAt(x2, y2))
	return wallOpening{
		bottom:   math.Max(fFloor, bFloor),
		top:      math.Min(fCeil, bCeil),
		lowFloor: math.Min(fFloor, bFloor),
		twoSided: true,
	}
}

// anyLineWithin reports whether any Linedef passes within radius of
// (x, y) and blocks(...) says its opening counts as a hit — the per-frame
// "what walls are near this point" scan every collision query in this
// package (player movement, projectile flight) is built on. Queries
// g.blockGrid (collisiongrid.go) rather than scanning every Linedef in
// the level, so cost stays roughly constant regardless of map size
// instead of growing with it. The second argument to blocks is whether the
// radius-square box at (x, y) straddles that line (id's P_BoxOnLineSide
// != -1) — a two-sided line's opening only constrains a move the box
// actually crosses.
func (g *Game) anyLineWithin(x, y, radius float64, blocks func(open wallOpening, straddles bool) bool) bool {
	hit := false
	g.blockGrid.forEachNear(x, y, radius, func(i int32) bool {
		ld := &g.Level.Linedefs[i]
		v1 := g.Level.Vertexes[ld.StartVertex]
		v2 := g.Level.Vertexes[ld.EndVertex]
		x1, y1, x2, y2 := float64(v1.X), float64(v1.Y), float64(v2.X), float64(v2.Y)
		if distancePointToSegment(x, y, x1, y1, x2, y2) >= radius {
			return true // keep scanning
		}
		if blocks(g.wallOpeningAt(ld), boxStraddlesLine(x, y, radius, x1, y1, x2, y2)) {
			hit = true
			return false // found a blocker, stop early
		}
		return true
	})
	return hit
}

// boxStraddlesLine reports whether the axis-aligned square of half-width r
// centred at (x, y) is cut by the infinite line through (x1,y1)-(x2,y2):
// its four corners are not all on the same side. This is id's
// P_BoxOnLineSide test (== -1) that PIT_CheckLine gates on before it ever
// looks at a line — a body merely near a two-sided line, but wholly on one
// side of it, isn't constrained by that line's opening or step-up.
func boxStraddlesLine(x, y, r, x1, y1, x2, y2 float64) bool {
	dx, dy := x2-x1, y2-y1
	side := func(px, py float64) float64 { return (px-x1)*dy - (py-y1)*dx }
	c0 := side(x-r, y-r)
	c1 := side(x+r, y-r)
	c2 := side(x-r, y+r)
	c3 := side(x+r, y+r)
	pos := c0 > 0 || c1 > 0 || c2 > 0 || c3 > 0
	neg := c0 < 0 || c1 < 0 || c2 < 0 || c3 < 0
	return pos && neg
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
