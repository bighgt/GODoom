package engine

import "math"

// useRange is id's USERANGE — how far the "use" (Space) action reaches to
// activate a door or switch linedef.
const useRange = 64.0

// rayIntersectsSegment finds where the ray from (ox, oy) in unit direction
// (dx, dy) crosses the segment (x1,y1)-(x2,y2), returning the distance
// along the ray (valid since (dx,dy) is a unit vector) and whether it hit
// within the segment's bounds and in front of the ray's origin. Shared by
// the "use" raycast (spec.go) and the hitscan trace (attack.go).
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
