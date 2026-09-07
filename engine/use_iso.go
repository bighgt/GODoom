package engine

import "math"

// IsoUseSystem is useSystem's isometric-viewpoint counterpart: activates
// the nearest special linedef within useRange of the player's position,
// regardless of facing angle, instead of useSystem/tryUseLine's single ray
// cast along Camera.Angle. That ray cast assumes Camera.Angle means "which
// way you're looking, i.e. facing" — true in first person, but
// IsoMovementSystem repurposes Camera.Angle as "aim toward the mouse
// cursor" instead (see its own doc comment), so a player who has walked up
// to a switch while aiming their mouse at a monster elsewhere would never
// trigger it: the ray would go toward the monster, not the switch, and
// "lowering platform" switches would read as simply broken. A proximity
// search sidesteps the mismatch entirely — the player doesn't have to face
// a switch's line, just stand near it, matching how a mouse-driven
// top-down game's "interact" key conventionally works.
//
// A Game defaults to the first-person useSystem (see NewGame); an
// isometric front end swaps it in via
// game.ReplaceUseSystem(engine.IsoUseSystem{}).
type IsoUseSystem struct{}

func (IsoUseSystem) Name() string { return "iso-use" }

func (IsoUseSystem) Update(g *Game, dt float32) {
	spaceDown := g.Window.UsePressed()
	if spaceDown && !g.spaceWasDown {
		if g.playerDead {
			g.restartLevel() // dead: Space takes you back and restarts the level, same as useSystem
		} else {
			g.tryUseNearbyLine()
		}
	}
	g.spaceWasDown = spaceDown
}

// tryUseNearbyLine activates the nearest special linedef within useRange of
// the player's (X, Y) map-unit position — see IsoUseSystem's own doc
// comment for why this doesn't reuse tryUseLine's ray cast.
func (g *Game) tryUseNearbyLine() {
	best, bestDist := -1, math.Inf(1)
	for i := range g.Level.Linedefs {
		if g.Level.Linedefs[i].SpecialType == 0 {
			continue
		}
		v1 := g.Level.Vertexes[g.Level.Linedefs[i].StartVertex]
		v2 := g.Level.Vertexes[g.Level.Linedefs[i].EndVertex]
		d := pointSegmentDist(g.Camera.X, g.Camera.Y, float64(v1.X), float64(v1.Y), float64(v2.X), float64(v2.Y))
		if d <= useRange && d < bestDist {
			best, bestDist = i, d
		}
	}
	if best < 0 {
		return
	}
	if g.activateLine(best, g.playerMobj, trUse) {
		g.changeSwitchTexture(best, repeatableSpecials[g.Level.Linedefs[best].SpecialType] ||
			manualDoor[g.Level.Linedefs[best].SpecialType] != 0)
	}
}

// pointSegmentDist is the shortest distance from (px, py) to the segment
// (x1,y1)-(x2,y2) — the standard clamped-projection formula.
func pointSegmentDist(px, py, x1, y1, x2, y2 float64) float64 {
	dx, dy := x2-x1, y2-y1
	lenSq := dx*dx + dy*dy
	if lenSq < 1e-9 {
		return math.Hypot(px-x1, py-y1)
	}
	t := ((px-x1)*dx + (py-y1)*dy) / lenSq
	t = math.Max(0, math.Min(1, t))
	cx, cy := x1+t*dx, y1+t*dy
	return math.Hypot(px-cx, py-cy)
}

// ReplaceUseSystem swaps out the default first-person useSystem (added
// second by NewGame, after movement and before combat/cheat/flashlight) for
// s — for an alternate front end (cmd/isoengine) that wants a proximity-
// based "use" instead of useSystem's facing-ray one (see IsoUseSystem). s
// runs in useSystem's original slot, so ordering among the other systems is
// unchanged. A no-op if called before NewGame has registered its systems.
func (g *Game) ReplaceUseSystem(s System) {
	if len(g.systems) > 1 {
		g.systems[1] = s
	}
}
