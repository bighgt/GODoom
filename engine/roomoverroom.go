package engine

// ExtraFloor is one Boom/GZDoom-style "3D floor" band a target sector
// borrows from a control sector elsewhere in the map — a floating platform
// (or a floating ceiling slab) inside the target's own footprint. The
// control sector's own ceiling becomes a walkable TOP surface (a floor for
// anything standing on it); its floor becomes the BOTTOM, a ceiling for
// anything passing underneath. Authored via a SLOPES lump's "extrafloor"
// line (engine/slopes) — see slopes_load.go's applySlopes, which builds
// Game.extraFloors from it.
//
// Scope note: only the PLAYER's own movement/collision/damage-floor code
// (resolvePlayerZ, collision.go, checkPlayerCrush, playerInSpecialSector)
// consults this. Monster/thing gravity (mobj.go's sectorFloorCeil) and the
// general line-opening query wallOpeningAt (hitscan/sight/sound/missile
// flight) do not yet — a monster or a shot passes through a 3D floor as if
// it weren't there. That's a real, deliberate limitation of this first
// pass, not an oversight: those touch far more call sites for far less
// certain payoff until room-over-room content actually exists to test
// against.
type ExtraFloor struct {
	ControlSector int
}

// planeEpsilon is the vertical slop planeAt allows when deciding whether a
// mover's Z counts as "resting on" a band's top or "just under" its
// bottom — floating-point landing from fallStep rarely settles at an exact
// integer height.
const planeEpsilon = 0.5

// planeAt resolves the floor/ceiling Z a mover at (x, y, z) actually
// experiences standing in sector secIdx, refined by any room-over-room
// extra-floor bands attached to it (g.extraFloors[secIdx]) — the base
// sector's own plane (already slope-aware, see wad.Sector.FloorZAt/
// CeilZAt), tightened by whichever band's top surface the mover is
// resting on or ceiling the mover's head is under.
//
// activeIdx is which sector's OWN data (SpecialType, light level, etc.)
// should apply at that height: secIdx itself, unless the mover is on a
// band borrowed from a control sector, in which case it's that control
// sector's index — standing on a 3D floor puts you logically inside its
// control sector's airspace (its ceiling texture is the floor under your
// feet), the same convention GZDoom's 3D floors use for sector effects.
//
// A sector with no extra floors (the vanilla case, and any sector before a
// SLOPES lump's "extrafloor" lines are applied) degenerates to exactly
// (sec.FloorZAt(x,y), sec.CeilZAt(x,y), secIdx) — unchanged from before
// this feature existed.
func (g *Game) planeAt(secIdx int, x, y, z float64) (floor, ceil float64, activeIdx int) {
	sec := &g.Level.Sectors[secIdx]
	floor, ceil = sec.FloorZAt(x, y), sec.CeilZAt(x, y)
	activeIdx = secIdx
	for _, ef := range g.extraFloors[secIdx] {
		if ef.ControlSector < 0 || ef.ControlSector >= len(g.Level.Sectors) {
			continue
		}
		ctrl := &g.Level.Sectors[ef.ControlSector]
		top, bottom := ctrl.CeilZAt(x, y), ctrl.FloorZAt(x, y)
		if bottom > top {
			continue // the control sector is momentarily closed (e.g. a shut door) — no slab right now
		}
		if top <= z+planeEpsilon && top > floor {
			floor = top
			activeIdx = ef.ControlSector
		}
		if bottom >= z-planeEpsilon && bottom < ceil {
			ceil = bottom
		}
	}
	return floor, ceil, activeIdx
}
