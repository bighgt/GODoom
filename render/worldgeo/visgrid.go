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
// does is granularity vs. draw-call count. Was 512 (a first, untuned
// guess); lowered to 256 after disabling the hardware renderer's opaque
// depth pre-pass as an experiment measured ~100 FPS worse (see
// recordWorldCommandBuffer's own comment) — that result says the per-pixel
// dynamic-light shading this cull skips is the expensive resource on this
// hardware, not draw-call count, so trading more (still cheap) draw calls
// for finer culling — fewer off-screen-but-in-cell fragments admitted
// through to that expensive shading — should net positive the same
// direction the pre-pass finding pointed.
const visGridCellSize = 256.0

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
	radius := math.Hypot(float64(maxX-minX), float64(maxY-minY)) / 2 // bounding-circle radius
	return circleVisible(cam, float64(cx), float64(cy), radius)
}

// circleVisible reports whether a world-space circle centred at (cx, cy)
// with radius r can be seen from cam — the camera-inside-it or angular-span-
// overlaps-the-(widened)-FOV-cone test cellVisible's own doc comment
// explains, factored out so LightVisible can reuse it for a light's reach
// instead of a geometry cell's bounding box.
func circleVisible(cam Camera, cx, cy, r float64) bool {
	dx, dy := cx-cam.X, cy-cam.Y
	dist := math.Hypot(dx, dy)
	if dist <= r {
		return true // camera is inside (or right on top of) the circle
	}
	angToCenter := math.Atan2(dy, dx) - cam.Angle
	angToCenter = math.Mod(angToCenter+math.Pi, 2*math.Pi)
	if angToCenter < 0 {
		angToCenter += 2 * math.Pi
	}
	angToCenter -= math.Pi
	angularRadius := math.Asin(r / dist) // dist > r above, so r/dist < 1
	half := (cam.FOVDeg/2 + cullMarginDeg) * math.Pi / 180
	return math.Abs(angToCenter)-angularRadius <= half
}

// LightVisible reports whether a point light at world position (x, y) with
// reach radius r could contribute to anything on screen from cam — the same
// generous cone test cellVisible uses for static-geometry culling (see its
// own doc comment for why the angular-span test is safe to use as a real
// cull, not just an estimate), applied to a light's own sphere instead of a
// geometry cell's bounding box. A render backend can use this to skip
// uploading/shading a light that's nowhere near the camera's view, the same
// way UpdateVisibility already skips drawing an off-screen geometry cell.
// Ignores Z (camera pitch): Doom levels are shallow enough that cellVisible
// already gets away with a 2D horizontal-only cone too.
func LightVisible(cam Camera, x, y, r float64) bool {
	return circleVisible(cam, x, y, r)
}
