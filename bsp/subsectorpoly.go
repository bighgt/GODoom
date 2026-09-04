package bsp

import (
	"math"
	"sort"

	"twopointfive/wad"
)

// Point is a 2D map-space position in map units.
type Point struct{ X, Y float32 }

// SubsectorPolygons returns, indexed by subsector index, the closed convex
// polygon a flat (floor / ceiling) fills for each BSP leaf.
//
// Reconstruction (no GL nodes, the way PrBoom-GL / GZDoom do it): a
// map-covering box is carried down the tree, clipped by every ancestor
// node's partition half-plane on the way to a leaf, then — at the leaf —
// clipped again by the front side of each of that subsector's own seg
// lines. A vanilla subsector's cell is exactly that intersection of
// half-planes, so this gives its true outline, seg edges and the implicit
// partition-line edges alike (vanilla nodes carry no minisegs for the
// latter). The result is convex CCW with >= 3 vertices, near-collinear
// vertices pruned; a leaf whose cell collapses or escapes the map bounds
// yields nil and the caller skips its flat.
func SubsectorPolygons(root Node, level *wad.Level) [][]Point {
	out := make([][]Point, len(level.Subsectors))
	if root == nil || len(level.Vertexes) == 0 {
		return out
	}

	minX, minY := math.MaxFloat64, math.MaxFloat64
	maxX, maxY := -math.MaxFloat64, -math.MaxFloat64
	for _, v := range level.Vertexes {
		x, y := float64(v.X), float64(v.Y)
		minX, maxX = math.Min(minX, x), math.Max(maxX, x)
		minY, maxY = math.Min(minY, y), math.Max(maxY, y)
	}
	span := math.Max(maxX-minX, maxY-minY) + 1024
	box := []pt64{
		{minX - span, minY - span}, {maxX + span, minY - span},
		{maxX + span, maxY + span}, {minX - span, maxY + span},
	}
	// A polygon vertex further than this outside the map's vertex bounds
	// means the leaf's cell was never actually closed by its ancestors /
	// segs (an outer "sky" leaf) — reject rather than paint a flat across
	// half the level.
	const outMargin = 64.0
	bx0, by0, bx1, by1 := minX-outMargin, minY-outMargin, maxX+outMargin, maxY+outMargin

	var walk func(n Node, clip []pt64)
	walk = func(n Node, clip []pt64) {
		switch t := n.(type) {
		case *Leaf:
			if t.Index < 0 || t.Index >= len(out) {
				return
			}
			out[t.Index] = leafPolygon(level, t.Subsector, clip, bx0, by0, bx1, by1)
		case *InnerNode:
			walk(t.Right, clipHalfPlane(clip, t, true)) // Side >= 0
			walk(t.Left, clipHalfPlane(clip, t, false)) // Side <= 0
		}
	}
	walk(root, box)
	return out
}

type pt64 struct{ x, y float64 }

// leafPolygon carves a subsector's convex cell out of clip (the running
// intersection of every ancestor partition half-plane) by clipping to the
// front (interior) side of each of the leaf's own seg lines, then validates
// and orients the result.
func leafPolygon(level *wad.Level, ss wad.Subsector, clip []pt64, bx0, by0, bx1, by1 float64) []Point {
	first, n := int(ss.FirstSeg), int(ss.SegCount)
	if n <= 0 || first < 0 || first+n > len(level.Segs) {
		return nil
	}
	cell := clip
	segEnds := make([]pt64, 0, 2*n)
	for s := 0; s < n; s++ {
		sg := level.Segs[first+s]
		if int(sg.StartVertex) >= len(level.Vertexes) || int(sg.EndVertex) >= len(level.Vertexes) {
			continue
		}
		a := level.Vertexes[sg.StartVertex]
		b := level.Vertexes[sg.EndVertex]
		ax, ay := float64(a.X), float64(a.Y)
		bx, by := float64(b.X), float64(b.Y)
		segEnds = append(segEnds, pt64{ax, ay}, pt64{bx, by})
		dx, dy := bx-ax, by-ay
		l := math.Hypot(dx, dy)
		if l == 0 {
			continue
		}
		// Keep the seg's front (interior) side — Doom's front is the
		// +(DY,-DX) normal of the a->b direction — with a 1-unit interior
		// reference point (robust against a short seg rounding the nudge to
		// zero).
		nrm := 1.0 / l
		kx := (ax+bx)*0.5 + dy*nrm
		ky := (ay+by)*0.5 - dx*nrm
		cell = clipToLine(cell, ax, ay, bx, by, kx, ky)
		if len(cell) < 3 {
			return nil
		}
	}

	// Snap cell vertices that landed within snapDist of one of this
	// subsector's own seg endpoints onto that exact endpoint. A vanilla
	// node's int16-truncated partition direction misses the vertices it was
	// meant to pass through by a fraction of a unit, which otherwise leaves
	// hairline gaps between adjacent flats (and here, a wall a hair inside
	// its own floor). Both subsectors sharing an implicit edge snap to the
	// same shared endpoints, so the seam closes exactly.
	// The cell's shape comes from the half-plane intersection, but a vanilla
	// node's int16-truncated partition direction misses the vertices it was
	// meant to pass through by a fraction of a unit — enough to leave a
	// hairline gap between adjacent flats or drop a wall's own base vertex
	// just outside its floor. Fold in this subsector's exact seg endpoints
	// and take the convex hull of the union: the seg endpoints pin every
	// wall edge precisely, the intersection vertices carry the implicit
	// (no-wall) edges, and the hull is the tightest convex polygon holding
	// both. Adjacent subsectors pin to the same shared seg endpoints, so the
	// seam closes exactly.
	pts := make([]pt64, 0, len(cell)+len(segEnds))
	pts = append(pts, cell...)
	pts = append(pts, segEnds...)
	hull := convexHull(pts)

	for _, p := range hull {
		if p.x < bx0 || p.x > bx1 || p.y < by0 || p.y > by1 {
			return nil // cell not actually closed by ancestors + segs
		}
	}
	return finish(hull)
}

// convexHull returns the CCW convex hull of pts (Andrew's monotone chain);
// points on a hull edge are dropped. Mutates pts' order.
func convexHull(pts []pt64) []pt64 {
	if len(pts) < 3 {
		return pts
	}
	sort.Slice(pts, func(i, j int) bool {
		if pts[i].x != pts[j].x {
			return pts[i].x < pts[j].x
		}
		return pts[i].y < pts[j].y
	})
	cross := func(o, a, b pt64) float64 {
		return (a.x-o.x)*(b.y-o.y) - (a.y-o.y)*(b.x-o.x)
	}
	h := make([]pt64, 0, len(pts)+1)
	for _, p := range pts { // lower hull
		for len(h) >= 2 && cross(h[len(h)-2], h[len(h)-1], p) <= 0 {
			h = h[:len(h)-1]
		}
		h = append(h, p)
	}
	lower := len(h) + 1
	for i := len(pts) - 2; i >= 0; i-- { // upper hull
		p := pts[i]
		for len(h) >= lower && cross(h[len(h)-2], h[len(h)-1], p) <= 0 {
			h = h[:len(h)-1]
		}
		h = append(h, p)
	}
	return h[:len(h)-1]
}

// clipHalfPlane is Sutherland–Hodgman clipping of poly against node's
// partition line, keeping the half-plane the front (keepFront) or back
// child sits on. Returns a new slice; poly is unchanged.
//
// The tolerance is deliberately loose (partEps map units on the back side
// still count as "in"): a vanilla node's partition direction (DX,DY) is
// truncated to int16, so the infinite line it defines misses the actual
// subsector-corner vertices it's supposed to pass through — by several units
// at the far end of a short partition. Clipping tight there shaves real
// corners off the flat. The slack only loosens the *implicit* (no-wall)
// edges between subsectors of one sector at one height, where a few units of
// overlap is invisible; the seg-line clips that follow use exact vertices
// and stay tight, so every wall-adjacent flat edge is still precise.
func clipHalfPlane(poly []pt64, node *InnerNode, keepFront bool) []pt64 {
	nx, ny := float64(node.X), float64(node.Y)
	dx, dy := float64(node.DX), float64(node.DY)
	// Normalise so the tolerance is in map units regardless of |D|.
	inv := 1.0
	if l := math.Hypot(dx, dy); l > 0 {
		inv = 1.0 / l
	}
	side := func(p pt64) float64 {
		s := ((p.x-nx)*dy - (p.y-ny)*dx) * inv
		if !keepFront {
			s = -s
		}
		return s
	}
	// partEps: how far onto the back side a point may sit and still be kept
	// (see the doc comment). tiny: the divide-by-zero guard for the edge
	// intersection.
	const partEps, tiny = 4.0, 1e-9
	res := make([]pt64, 0, len(poly)+2)
	for i := range poly {
		cur := poly[i]
		prev := poly[(i+len(poly)-1)%len(poly)]
		sc, sp := side(cur), side(prev)
		curIn, prevIn := sc >= -partEps, sp >= -partEps
		if curIn != prevIn && math.Abs(sp-sc) > tiny {
			t := sp / (sp - sc)
			res = append(res, pt64{prev.x + t*(cur.x-prev.x), prev.y + t*(cur.y-prev.y)})
		}
		if curIn {
			res = append(res, cur)
		}
	}
	return res
}

// clipToLine clips poly to the half-plane of the line through (ax,ay)->
// (bx,by) that contains (kx,ky). Returns a new slice.
func clipToLine(poly []pt64, ax, ay, bx, by, kx, ky float64) []pt64 {
	dx, dy := bx-ax, by-ay
	sideOf := func(px, py float64) float64 { return (px-ax)*dy - (py-ay)*dx }
	want := sideOf(kx, ky)
	side := func(p pt64) float64 {
		s := sideOf(p.x, p.y)
		if want < 0 {
			s = -s
		}
		return s
	}
	const eps = 1e-6
	res := make([]pt64, 0, len(poly)+2)
	for i := range poly {
		cur := poly[i]
		prev := poly[(i+len(poly)-1)%len(poly)]
		sc, sp := side(cur), side(prev)
		curIn, prevIn := sc >= -eps, sp >= -eps
		if curIn != prevIn && math.Abs(sp-sc) > eps {
			t := sp / (sp - sc)
			res = append(res, pt64{prev.x + t*(cur.x-prev.x), prev.y + t*(cur.y-prev.y)})
		}
		if curIn {
			res = append(res, cur)
		}
	}
	return res
}

// finish prunes near-collinear vertices, guards against a degenerate area,
// orients the ring CCW, and converts to []Point. Returns nil below 3 real
// vertices or a sliver area.
func finish(poly []pt64) []Point {
	const flat = 4.0 // twice a triangle's area, map-units^2, below which a vertex is "on" its neighbours' edge

	if len(poly) < 3 {
		return nil
	}

	// Merge consecutive near-duplicate vertices: the convex hull of a clipped
	// cell can emit one corner twice, a hair apart, from two different line
	// pairs. Left in, the collinear prune below sees each as a degenerate
	// micro-triangle with its twin and deletes a real corner.
	const merge = 0.25
	dedup := make([]pt64, 0, len(poly))
	for i := range poly {
		if n := len(dedup); n > 0 && math.Hypot(poly[i].x-dedup[n-1].x, poly[i].y-dedup[n-1].y) < merge {
			continue
		}
		dedup = append(dedup, poly[i])
	}
	for len(dedup) >= 2 && math.Hypot(dedup[len(dedup)-1].x-dedup[0].x, dedup[len(dedup)-1].y-dedup[0].y) < merge {
		dedup = dedup[:len(dedup)-1]
	}
	poly = dedup
	if len(poly) < 3 {
		return nil
	}

	pruned := make([]pt64, 0, len(poly))
	for i := range poly {
		prev := poly[(i+len(poly)-1)%len(poly)]
		cur := poly[i]
		next := poly[(i+1)%len(poly)]
		cross := (cur.x-prev.x)*(next.y-prev.y) - (cur.y-prev.y)*(next.x-prev.x)
		if math.Abs(cross) < flat {
			continue
		}
		pruned = append(pruned, cur)
	}
	if len(pruned) < 3 {
		return nil
	}

	// Every real subsector cell is convex; reject a non-convex or
	// self-intersecting result (bad clipping on a sliver leaf) rather than
	// hand back a bowtie.
	pos, neg := false, false
	for i := range pruned {
		p := pruned[(i+len(pruned)-1)%len(pruned)]
		c := pruned[i]
		q := pruned[(i+1)%len(pruned)]
		cr := (c.x-p.x)*(q.y-p.y) - (c.y-p.y)*(q.x-p.x)
		if cr > flat {
			pos = true
		} else if cr < -flat {
			neg = true
		}
	}
	if pos && neg {
		return nil
	}

	var area2 float64
	for i := range pruned {
		a, b := pruned[i], pruned[(i+1)%len(pruned)]
		area2 += a.x*b.y - b.x*a.y
	}
	// Cull anything below ~64 map-units^2 (an 8x8 patch): a real cell is
	// thousands+, and a thinner sliver just rounds to a degenerate float32
	// polygon anyway — the caller skips its flat (a few-pixel gap at worst).
	if math.Abs(area2) < 128.0 {
		return nil
	}

	res := make([]Point, len(pruned))
	if area2 < 0 {
		for i, p := range pruned {
			res[len(pruned)-1-i] = Point{float32(p.x), float32(p.y)}
		}
	} else {
		for i, p := range pruned {
			res[i] = Point{float32(p.x), float32(p.y)}
		}
	}
	// Final guard on the actual float32 output: a sliver can survive the
	// float64 checks above yet round to a degenerate polygon here.
	var fa float64
	for i := range res {
		a, b := res[i], res[(i+1)%len(res)]
		fa += float64(a.X)*float64(b.Y) - float64(b.X)*float64(a.Y)
	}
	if fa <= 64.0 { // must be positive (CCW) and not a sliver
		return nil
	}
	return res
}
