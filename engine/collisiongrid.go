package engine

import (
	"math"

	"twopointfive/wad"
)

// gridCellSize matches Doom's own BLOCKMAP convention (128 map units per
// block) — not load-bearing here (this is an independent index, not a
// parse of the WAD's own BLOCKMAP lump), just a sensible, well-tested
// bucket size for the density of a typical Doom level.
const gridCellSize = 128.0

// collisionGrid buckets every Linedef index by the grid cell(s) its
// bounding box overlaps, so a radius query only has to check the handful
// of lines actually near a point instead of every Linedef in the level.
// Built once per Level (see buildCollisionGrid, called from NewGame) and
// never mutated afterward — doors animate a Sector's ceiling height, not
// any Linedef's position, so the spatial index itself never goes stale.
type collisionGrid struct {
	minX, minY float64
	cols, rows int
	cells      [][]int32 // linedef indices, row-major by (row*cols + col)
}

// buildCollisionGrid indexes every Linedef in level. Returns nil if the
// level has no geometry at all (nothing to index) — callers treat a nil
// grid as "fall back to checking nothing," which never happens in
// practice since finishLevel already rejects a level with no Linedefs.
func buildCollisionGrid(level *wad.Level) *collisionGrid {
	if len(level.Vertexes) == 0 || len(level.Linedefs) == 0 {
		return nil
	}

	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for _, v := range level.Vertexes {
		x, y := float64(v.X), float64(v.Y)
		minX, maxX = math.Min(minX, x), math.Max(maxX, x)
		minY, maxY = math.Min(minY, y), math.Max(maxY, y)
	}

	g := &collisionGrid{
		minX: minX, minY: minY,
		cols: int((maxX-minX)/gridCellSize) + 1,
		rows: int((maxY-minY)/gridCellSize) + 1,
	}
	g.cells = make([][]int32, g.cols*g.rows)

	for i := range level.Linedefs {
		ld := &level.Linedefs[i]
		if int(ld.StartVertex) >= len(level.Vertexes) || int(ld.EndVertex) >= len(level.Vertexes) {
			continue
		}
		v1 := level.Vertexes[ld.StartVertex]
		v2 := level.Vertexes[ld.EndVertex]
		loX, hiX := math.Min(float64(v1.X), float64(v2.X)), math.Max(float64(v1.X), float64(v2.X))
		loY, hiY := math.Min(float64(v1.Y), float64(v2.Y)), math.Max(float64(v1.Y), float64(v2.Y))

		c0, c1 := g.cellX(loX), g.cellX(hiX)
		r0, r1 := g.cellY(loY), g.cellY(hiY)
		for r := r0; r <= r1; r++ {
			base := r * g.cols
			for c := c0; c <= c1; c++ {
				idx := base + c
				g.cells[idx] = append(g.cells[idx], int32(i))
			}
		}
	}
	return g
}

func (g *collisionGrid) cellX(x float64) int {
	c := int((x - g.minX) / gridCellSize)
	if c < 0 {
		return 0
	}
	if c >= g.cols {
		return g.cols - 1
	}
	return c
}

func (g *collisionGrid) cellY(y float64) int {
	r := int((y - g.minY) / gridCellSize)
	if r < 0 {
		return 0
	}
	if r >= g.rows {
		return g.rows - 1
	}
	return r
}

// forEachNear calls fn once for every Linedef index in a cell overlapping
// the (x-radius, y-radius)-(x+radius, y+radius) box — a small, superset
// neighborhood, not an exact radius query (fine, since every caller still
// does its own precise distance check on each candidate; see
// anyLineWithin). Stops early if fn returns false. A Linedef spanning
// multiple cells may be visited more than once; callers' predicates are
// pure/side-effect-free, so a little redundant re-checking is harmless
// and far cheaper than deduplicating.
func (g *collisionGrid) forEachNear(x, y, radius float64, fn func(lineIdx int32) bool) {
	if g == nil {
		return
	}
	c0, c1 := g.cellX(x-radius), g.cellX(x+radius)
	r0, r1 := g.cellY(y-radius), g.cellY(y+radius)
	for r := r0; r <= r1; r++ {
		base := r * g.cols
		for c := c0; c <= c1; c++ {
			for _, idx := range g.cells[base+c] {
				if !fn(idx) {
					return
				}
			}
		}
	}
}

// forEachAlongSegment calls fn for every Linedef index bucketed in a cell
// the segment (x0,y0)-(x1,y1) passes near — each index at most once. Like
// forEachNear it yields a superset (callers still run their own precise
// segment test); unlike it, the work is proportional to the segment's
// length in cells, not to the area of its bounding box, so a long diagonal
// sight line or hitscan doesn't degrade to a full-level scan. Stops early
// if fn returns false.
//
// It samples the segment every half-cell and, at each sample, visits the
// sample's cell and its 8 neighbours: with samples <=64 units apart and
// 128-unit cells, any cell the segment truly crosses is within Chebyshev
// distance 1 of some sample's cell, so nothing intersecting is missed.
func (g *collisionGrid) forEachAlongSegment(x0, y0, x1, y1 float64, fn func(lineIdx int32) bool) {
	if g == nil {
		return
	}
	length := math.Hypot(x1-x0, y1-y0)
	steps := int(length/(gridCellSize/2)) + 1

	// Skip cells already covered by the immediately preceding sample's 3x3
	// block — consecutive samples are <=64 units apart, so that overlap is
	// where nearly all the redundancy is. Residual repeats (a cell shared
	// with a sample two steps back) are harmless: like forEachNear, fn is
	// expected to be side-effect-free. This keeps the walk allocation-free
	// and safe to nest.
	var prev [9]int
	for i := range prev {
		prev[i] = -1
	}

	for s := 0; s <= steps; s++ {
		f := float64(s) / float64(steps)
		cc := g.cellX(x0 + (x1-x0)*f)
		cr := g.cellY(y0 + (y1-y0)*f)
		var cur [9]int
		n := 0
		for dr := -1; dr <= 1; dr++ {
			r := cr + dr
			if r < 0 || r >= g.rows {
				continue
			}
			base := r * g.cols
			for dc := -1; dc <= 1; dc++ {
				c := cc + dc
				if c < 0 || c >= g.cols {
					continue
				}
				cell := base + c
				cur[n] = cell
				n++
				repeat := false
				for _, p := range prev {
					if p == cell {
						repeat = true
						break
					}
				}
				if repeat {
					continue
				}
				for _, idx := range g.cells[cell] {
					if !fn(idx) {
						return
					}
				}
			}
		}
		prev = cur
		for k := n; k < len(prev); k++ {
			prev[k] = -1
		}
	}
}
