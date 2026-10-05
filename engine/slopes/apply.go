package slopes

import (
	"fmt"
	"math"

	"twopointfive/wad"
)

// Apply tilts def's sector in lvl — shared by the engine's level load and
// the editor. Returns a diagnostic (and changes nothing) when the sector
// index is out of range or the three points are collinear/degenerate.
func (d Def) Apply(lvl *wad.Level) error {
	if d.SectorIndex < 0 || d.SectorIndex >= len(lvl.Sectors) {
		return fmt.Errorf("line %d: sector %d out of range (map has %d)", d.Line, d.SectorIndex, len(lvl.Sectors))
	}
	tiltX, tiltY, ok := PlaneTilt(d.P1, d.P2, d.P3)
	if !ok {
		return fmt.Errorf("line %d: sector %d's three points are collinear/degenerate — skipped", d.Line, d.SectorIndex)
	}
	sec := &lvl.Sectors[d.SectorIndex]
	// The pivot is shared between a sector's floor and ceiling tilt (see
	// wad.Sector's doc comment) — the last plane definition for this
	// sector to set it wins, which only matters if floor and ceiling are
	// each given a DIFFERENT P1 (unusual but not forbidden).
	sec.SlopePivotX, sec.SlopePivotY = d.P1[0], d.P1[1]
	if d.Ceiling {
		sec.CeilingHeight = int16(math.Round(d.P1[2]))
		sec.CeilTiltX, sec.CeilTiltY = tiltX, tiltY
	} else {
		sec.FloorHeight = int16(math.Round(d.P1[2]))
		sec.FloorTiltX, sec.FloorTiltY = tiltX, tiltY
	}
	return nil
}

// PlaneTilt solves for the (tiltX, tiltY) of the plane through p1, p2, p3
// that passes through p1 with that exact height (p1 is the pivot — see
// wad.Sector's doc comment) — i.e. z(x,y) = p1.z + tiltX*(x-p1.x) +
// tiltY*(y-p1.y), solved from the two other points' offsets from p1. ok is
// false when the three points are collinear (or p2/p3 coincide with p1) —
// no unique plane, or a degenerate (zero-tilt either way) one.
func PlaneTilt(p1, p2, p3 [3]float64) (tiltX, tiltY float64, ok bool) {
	dx2, dy2, dz2 := p2[0]-p1[0], p2[1]-p1[1], p2[2]-p1[2]
	dx3, dy3, dz3 := p3[0]-p1[0], p3[1]-p1[1], p3[2]-p1[2]
	det := dx2*dy3 - dx3*dy2
	if det == 0 {
		return 0, 0, false
	}
	tiltX = (dz2*dy3 - dz3*dy2) / det
	tiltY = (dx2*dz3 - dx3*dz2) / det
	return tiltX, tiltY, true
}
