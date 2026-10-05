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
	sz := t1.Z + t1.Height*0.75 // eye height, id's t1->z + t1->height/2 + ... (3/4 is close enough)
	ez := t2.Z + t2.Height*0.5
	_, blocked := g.traceBlock(t1.X, t1.Y, sz, t2.X, t2.Y, ez, false)
	return !blocked
}

// traceBlock walks the segment (sx,sy,sz)-(ex,ey,ez) and reports whether a
// wall blocks it: any one-sided line it crosses, or a two-sided line
// whose current vertical opening (wallOpeningAt — live, so a door that
// has opened since lets it through) is shut or doesn't contain the
// segment's interpolated height at the crossing. With nearest, it scans
// every crossing and returns the parameter (0..1) of the CLOSEST block to
// the start; without, it stops at the first one found (any order — a
// plain yes/no, cheaper) and the parameter is meaningless.
func (g *Game) traceBlock(sx, sy, sz, ex, ey, ez float64, nearest bool) (float64, bool) {
	if g.Level == nil {
		return 1, false
	}
	first, blocked := 1.0, false
	g.blockGrid.forEachAlongSegment(sx, sy, ex, ey, func(i int32) bool {
		ld := &g.Level.Linedefs[i]
		v1 := g.Level.Vertexes[ld.StartVertex]
		v2 := g.Level.Vertexes[ld.EndVertex]

		hit, tSeg := segCross(sx, sy, ex, ey, float64(v1.X), float64(v1.Y), float64(v2.X), float64(v2.Y))
		if !hit {
			return true
		}
		stop := false
		if ld.BackSidedef == wad.NoSidedef {
			stop = true // solid wall
		} else if open := g.wallOpeningAt(ld); !open.twoSided || open.top <= open.bottom {
			stop = true // closed door / sealed opening
		} else if z := sz + (ez-sz)*tSeg; z <= open.bottom || z >= open.top {
			stop = true // passes through floor or ceiling at this crossing
		}
		if !stop {
			return true
		}
		blocked = true
		first = min(first, tSeg)
		return nearest
	})
	return first, blocked
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
