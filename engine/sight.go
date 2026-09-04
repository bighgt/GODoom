package engine

import "twopointfive/wad"

// pCheckSight is id's P_CheckSight, simplified: a straight segment trace
// from t1's eye to t2's eye. Any one-sided line the segment crosses blocks
// it; a two-sided line blocks only if the sight line's interpolated height
// at the crossing falls outside that line's vertical opening. The REJECT
// table (a fast per-sector-pair "can never see" bit matrix) isn't consulted
// yet — the trace is cheap enough at this level count.
func (g *Game) pCheckSight(t1, t2 *Mobj) bool {
	if g.Level == nil || t1 == nil || t2 == nil {
		return t1 != nil && t2 != nil
	}
	sx, sy := t1.X, t1.Y
	ex, ey := t2.X, t2.Y
	sz := t1.Z + t1.Height*0.75 // eye height, id's t1->z + t1->height/2 + ... (3/4 is close enough)
	ez := t2.Z + t2.Height*0.5

	blocked := false
	g.blockGrid.forEachAlongSegment(sx, sy, ex, ey, func(i int32) bool {
		ld := &g.Level.Linedefs[i]
		v1 := g.Level.Vertexes[ld.StartVertex]
		v2 := g.Level.Vertexes[ld.EndVertex]

		hit, tSeg := segCross(sx, sy, ex, ey, float64(v1.X), float64(v1.Y), float64(v2.X), float64(v2.Y))
		if !hit {
			return true
		}
		if ld.BackSidedef == wad.NoSidedef {
			blocked = true
			return false // solid wall across the line of sight
		}
		open := g.wallOpeningAt(ld)
		if !open.twoSided || open.top <= open.bottom {
			blocked = true
			return false
		}
		z := sz + (ez-sz)*tSeg
		if z <= open.bottom || z >= open.top {
			blocked = true
			return false // sight passes through floor or ceiling at this crossing
		}
		return true
	})
	return !blocked
}

// segCross reports whether segments A(ax,ay)-(bx,by) and B(cx,cy)-(dx,dy)
// properly cross, and if so the parameter (0..1) along A of the crossing.
func segCross(ax, ay, bx, by, cx, cy, dx, dy float64) (bool, float64) {
	r1x, r1y := bx-ax, by-ay
	r2x, r2y := dx-cx, dy-cy
	denom := r1x*r2y - r1y*r2x
	if denom == 0 {
		return false, 0 // parallel or degenerate
	}
	t := ((cx-ax)*r2y - (cy-ay)*r2x) / denom
	u := ((cx-ax)*r1y - (cy-ay)*r1x) / denom
	if t <= 0 || t >= 1 || u <= 0 || u >= 1 {
		return false, 0
	}
	return true, t
}
