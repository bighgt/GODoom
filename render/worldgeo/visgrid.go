package worldgeo

import "math"

// Static-geometry visibility culling (see the "vivid-gliding-river" plan
// doc from that session): a coarse world-space grid giving Builder's
// per-texture draw buckets a second axis — location — to split on, plus a
// per-frame test of which draws the camera can actually see.
// render/vulkan/world.go's colour pass skips a StaticDraws entry whose
// actual vertex bounding box (Draw.MinX/MinY/MaxX/MaxY, computed once in
// Invalidate from that draw's own vertices — not the grid cell's nominal
// bounds; see cellOf's doc for why the two can differ) tests as not visible
// (Geometry.StaticDrawVisible, populated by Builder.UpdateVisibility) — the
// depth pre-pass does not (see that file for why: it draws one contiguous
// vertex range rather than iterating per-Draw, so filtering it would need
// its own restructuring; left as a known, deliberate gap rather than
// attempted here).

// visGridCellSize is the bucket size. engine/collisiongrid.go's
// collisionGrid — the pattern this mirrors — uses 128 units/cell, tuned for
// collision-query density; that tradeoff doesn't apply here; the one that
// does is granularity vs. draw-call count; and 512 is a first empirical
// guess (Stage 1's measurement pass found it culled a meaningful fraction
// of a real level from a real vantage point — see the plan doc's numbers —
// but it hasn't been tuned beyond that one data point).
const visGridCellSize = 512.0

// visGrid is a uniform grid over one level's vertex bounds. Unlike
// collisionGrid, a cell doesn't need to list what's in it — Builder visits
// every subsector once at build time and just needs cellOf to tell it which
// cell number that subsector's geometry belongs to as it goes.
type visGrid struct {
	minX, minY float32
	cols, rows int
}

func newVisGrid(minX, minY, maxX, maxY float32) *visGrid {
	g := &visGrid{minX: minX, minY: minY}
	g.cols = int((maxX-minX)/visGridCellSize) + 1
	g.rows = int((maxY-minY)/visGridCellSize) + 1
	if g.cols < 1 {
		g.cols = 1
	}
	if g.rows < 1 {
		g.rows = 1
	}
	return g
}

// cellOf returns the cell index containing the point (x, y) — Builder calls
// this once per subsector, at its polygon's centroid, treating the whole
// subsector as one atomic visibility unit (the same granularity id's own
// renderer uses) rather than duplicating a straddling subsector's vertices
// into every cell its bounding box touches: this is purely a batching key,
// not itself the culling test, so a subsector spanning more than one
// visGridCellSize (common in a map's big outdoor sectors — e.g. E1M2's
// courtyard) only costs a coarser (texture, cell) split, not a wrong
// answer: Invalidate derives each Draw's actual vertex bounding box
// (Draw.MinX/MinY/MaxX/MaxY) from what centroid-bucketing put there, and
// UpdateVisibility culls against that real footprint rather than this
// cell's nominal square — see the Draw doc comment.
func (g *visGrid) cellOf(x, y float32) int32 {
	col := int((x - g.minX) / visGridCellSize)
	row := int((y - g.minY) / visGridCellSize)
	if col < 0 {
		col = 0
	} else if col >= g.cols {
		col = g.cols - 1
	}
	if row < 0 {
		row = 0
	} else if row >= g.rows {
		row = g.rows - 1
	}
	return int32(row*g.cols + col)
}

// bounds returns cell's world-space AABB.
func (g *visGrid) bounds(cell int32) (minX, minY, maxX, maxY float32) {
	row := int(cell) / g.cols
	col := int(cell) % g.cols
	minX = g.minX + float32(col)*visGridCellSize
	minY = g.minY + float32(row)*visGridCellSize
	return minX, minY, minX + visGridCellSize, minY + visGridCellSize
}

// cullMarginDeg widens the camera's own horizontal FOV before testing a
// cell against it — general safety headroom (the widescreen Hor+ vertical-
// FOV-locked projection, a turn between this frame's cull decision and what
// actually renders, ordinary approximation slop), on top of — not a
// substitute for — cellVisible's own exact box/cone math below.
const cullMarginDeg = 15.0

// cellVisible reports whether cell's bounding box can be seen from cam: is
// the camera inside the box, or does the box's bounding CIRCLE overlap the
// camera's (widened) horizontal FOV cone? Using the circle's angular extent
// (its half-diagonal, projected to an angle at the box's distance) rather
// than testing only the box's 4 corners is what makes this safe to use for
// an actual cull (not just an estimate, see UpdateVisibility): a large box
// can cross a narrow FOV cone through an edge, with all 4 corners outside
// it — a corners-only test would wrongly cull that box; measuring the
// box's angular SPAN against the cone's doesn't have that blind spot.
func cellVisible(cam Camera, minX, minY, maxX, maxY float32) bool {
	cx, cy := (minX+maxX)/2, (minY+maxY)/2
	dx, dy := float64(cx)-cam.X, float64(cy)-cam.Y
	dist := math.Hypot(dx, dy)
	radius := math.Hypot(float64(maxX-minX), float64(maxY-minY)) / 2 // bounding-circle radius
	if dist <= radius {
		return true // camera is inside (or right on top of) the box
	}
	angToCenter := math.Atan2(dy, dx) - cam.Angle
	angToCenter = math.Mod(angToCenter+math.Pi, 2*math.Pi)
	if angToCenter < 0 {
		angToCenter += 2 * math.Pi
	}
	angToCenter -= math.Pi
	angularRadius := math.Asin(radius / dist) // dist > radius above, so radius/dist < 1
	half := (cam.FOVDeg/2 + cullMarginDeg) * math.Pi / 180
	return math.Abs(angToCenter)-angularRadius <= half
}
