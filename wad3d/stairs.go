package wad3d

import (
	"math"

	"twopointfive/engine/slopes"
)

// The stairs pass finds straight staircases — a chain of small sectors
// each one step higher than the last, divided by parallel riser lines — and
// replaces the whole run with ONE continuous ramp. Every step sector gets
// the identical plane (same three SLOPES points, so the engine derives the
// bit-identical tilt for each, and adjacent steps meet with no seam); the
// plane passes through the bottom riser at the lower landing's floor height
// and through the top riser at the upper landing's, so the landings
// themselves stay flat and join the ramp exactly. The riser walls between
// steps collapse to zero height on their own (worldgeo already evaluates a
// wall's bottom per endpoint), and collision/sight/monster moves already
// read the tilted planes (engine wallOpeningAt, sectorFloorCeil).
//
// Deliberately conservative: a sector that anything might move (tagged, a
// sector special, the back of a manual door — topology.mover) never
// becomes a step, since a mover rewrites FloorHeight (the ramp's pivot
// height) at runtime; a run whose steps spread sideways past the risers, or
// whose ramp would pinch the headroom under a step's ceiling, is left
// alone rather than approximated.
const (
	stairMaxRise     = 24.0  // vanilla's step-up limit — anything taller is a ledge, not a step
	stairRiseTol     = 4.0   // how far one step's rise may differ from the run's first
	stairRiserTol    = 2.0   // map units a riser endpoint may sit off its ideal line
	stairMaxSlope    = 1.0   // rise/run cap (45°) — steeper and it's no longer comfortably walkable
	stairMinHeadroom = 56.0  // player height: ceiling minus ramp must stay at least this everywhere
	stairParallelCos = 0.996 // ~5° — consecutive risers must face the same way
	stairPlaneStep   = 64.0  // spacing of P2/P3 from P1 in the emitted SLOPES points
)

// StairOptions tunes the pass; the zero value means the defaults.
type StairOptions struct {
	MinSteps int // shortest run converted (default 3)
}

// stairRun is one converted staircase.
type stairRun struct {
	lower, upper int   // landing sectors (unchanged)
	steps        []int // step sectors, bottom to top (all get plane)
	plane        [3][3]float64
	slope        float64
}

// riser is the boundary between two adjacent sectors a and b (b the higher
// one), reduced to a straight line: n is its unit normal pointing from a
// into b, t the offset of that line along n (n·p for any point on it).
type riser struct {
	a, b     int
	rise     float64
	nx, ny   float64
	mx, my   float64 // length-weighted midpoint
	lines    []int
	straight bool
}

func (t *topology) riserBetween(a, b int) riser {
	lvl := t.lvl
	r := riser{a: a, b: b, rise: float64(lvl.Sectors[b].FloorHeight) - float64(lvl.Sectors[a].FloorHeight)}
	var sx, sy, mx, my, total float64
	for _, e := range t.adj[a] {
		if e.other != b {
			continue
		}
		ld := lvl.Linedefs[e.line]
		v1, v2 := lvl.Vertexes[ld.StartVertex], lvl.Vertexes[ld.EndVertex]
		dx, dy := float64(v2.X-v1.X), float64(v2.Y-v1.Y)
		l := math.Hypot(dx, dy)
		if l == 0 {
			continue
		}
		// A linedef's front side is to the RIGHT of start->end: right normal
		// (dy, -dx). Point it into b.
		nx, ny := dy/l, -dx/l
		if t.sideSector(ld.FrontSidedef) != b {
			nx, ny = -nx, -ny
		}
		sx += nx * l
		sy += ny * l
		mx += (float64(v1.X) + float64(v2.X)) / 2 * l
		my += (float64(v1.Y) + float64(v2.Y)) / 2 * l
		total += l
		r.lines = append(r.lines, e.line)
	}
	if total == 0 {
		return r
	}
	nl := math.Hypot(sx, sy)
	if nl == 0 {
		return r
	}
	r.nx, r.ny = sx/nl, sy/nl
	r.mx, r.my = mx/total, my/total
	r.straight = t.riserFits(r, r.nx, r.ny, r.nx*r.mx+r.ny*r.my)
	return r
}

// riserFits reports whether every endpoint of r's lines lies within
// stairRiserTol of the line n·p = off.
func (t *topology) riserFits(r riser, nx, ny, off float64) bool {
	for _, li := range r.lines {
		ld := t.lvl.Linedefs[li]
		for _, vi := range [2]uint16{ld.StartVertex, ld.EndVertex} {
			v := t.lvl.Vertexes[vi]
			if math.Abs(nx*float64(v.X)+ny*float64(v.Y)-off) > stairRiserTol {
				return false
			}
		}
	}
	return true
}

// upNeighbors lists the distinct sectors adjacent to s whose floor is a
// plausible single step (0, stairMaxRise] above it.
func (t *topology) upNeighbors(s int) []int {
	var out []int
	seen := map[int]bool{}
	base := t.lvl.Sectors[s].FloorHeight
	for _, e := range t.adj[s] {
		if seen[e.other] {
			continue
		}
		seen[e.other] = true
		d := float64(t.lvl.Sectors[e.other].FloorHeight - base)
		if d > 0 && d <= stairMaxRise {
			out = append(out, e.other)
		}
	}
	return out
}

func parallel(a, b riser) bool { return a.nx*b.nx+a.ny*b.ny >= stairParallelCos }

func similarRise(a, b riser) bool { return math.Abs(a.rise-b.rise) <= stairRiseTol }

// findStairRuns returns every convertible staircase in the map. exclude
// marks sectors that must not become steps (already sloped by the source
// WAD's own SLOPES lump).
func findStairRuns(t *topology, exclude map[int]bool, opt StairOptions) []stairRun {
	minSteps := opt.MinSteps
	if minSteps <= 0 {
		minSteps = 3
	}
	lvl := t.lvl
	used := make([]bool, len(lvl.Sectors))
	eligible := func(s int) bool { return !t.mover[s] && !exclude[s] && !used[s] }

	var runs []stairRun
	for s1 := range lvl.Sectors {
		if !eligible(s1) {
			continue
		}
		for _, l0 := range t.downNeighbors(s1) {
			r0 := t.riserBetween(l0, s1)
			if !r0.straight || t.continuesBelow(l0, r0, eligible) {
				continue // not straight, or s1 is mid-run — found from the real bottom instead
			}
			run, ok := t.walkRun(l0, s1, r0, eligible)
			if !ok || len(run.steps) < minSteps {
				continue
			}
			for _, s := range run.steps {
				used[s] = true
			}
			runs = append(runs, run)
			break
		}
	}
	return runs
}

// downNeighbors is upNeighbors' mirror: sectors a single step below s.
func (t *topology) downNeighbors(s int) []int {
	var out []int
	seen := map[int]bool{}
	base := t.lvl.Sectors[s].FloorHeight
	for _, e := range t.adj[s] {
		if seen[e.other] {
			continue
		}
		seen[e.other] = true
		d := float64(base - t.lvl.Sectors[e.other].FloorHeight)
		if d > 0 && d <= stairMaxRise {
			out = append(out, e.other)
		}
	}
	return out
}

// continuesBelow reports whether landing candidate l0 is itself a step of
// the same run as riser r (an eligible sector one parallel, similar step
// above something else) — in which case the run's true bottom is further
// down and the walk should start there instead.
func (t *topology) continuesBelow(l0 int, r riser, eligible func(int) bool) bool {
	if !eligible(l0) {
		return false
	}
	for _, below := range t.downNeighbors(l0) {
		rb := t.riserBetween(below, l0)
		if rb.straight && parallel(rb, r) && similarRise(rb, r) {
			return true
		}
	}
	return false
}

// walkRun follows a staircase up from the riser l0->s1 and fits the ramp.
func (t *topology) walkRun(l0, s1 int, r0 riser, eligible func(int) bool) (stairRun, bool) {
	steps := []int{s1}
	risers := []riser{r0}
	inRun := map[int]bool{l0: true, s1: true}
	depthRef := -1.0
	upper := -1

	for cur := s1; ; {
		var next []riser
		for _, u := range t.upNeighbors(cur) {
			if inRun[u] {
				continue
			}
			r := t.riserBetween(cur, u)
			if r.straight && parallel(r, r0) && similarRise(r, r0) {
				next = append(next, r)
			}
		}
		if len(next) != 1 {
			// Dead end or a fork: cur is the top landing, not a step.
			upper = cur
			steps = steps[:len(steps)-1]
			break
		}
		r := next[0]
		last := risers[len(risers)-1]
		depth := (r.nx*r.mx + r.ny*r.my) - (r.nx*last.mx + r.ny*last.my)
		if depthRef < 0 {
			depthRef = depth
		}
		if depth <= 0 || depth > math.Max(2*depthRef, depthRef+16) {
			// cur is much deeper than a step — a landing partway up.
			upper = cur
			steps = steps[:len(steps)-1]
			break
		}
		risers = append(risers, r)
		inRun[r.b] = true
		if !eligible(r.b) {
			upper = r.b
			break
		}
		steps = append(steps, r.b)
		cur = r.b
	}
	if upper < 0 || len(steps) == 0 {
		return stairRun{}, false
	}
	// risers[i] runs into steps[i]; the top riser is the one into upper.
	risers = risers[:len(steps)+1]
	return t.fitRamp(l0, upper, steps, risers)
}

// fitRamp computes the single plane for a run and checks it's safe to use.
func (t *topology) fitRamp(lower, upper int, steps []int, risers []riser) (stairRun, bool) {
	lvl := t.lvl
	var nx, ny float64
	for _, r := range risers {
		nx += r.nx
		ny += r.ny
	}
	nl := math.Hypot(nx, ny)
	if nl == 0 {
		return stairRun{}, false
	}
	nx, ny = nx/nl, ny/nl
	proj := func(x, y float64) float64 { return nx*x + ny*y }

	bottom, top := risers[0], risers[len(risers)-1]
	t0, t1 := proj(bottom.mx, bottom.my), proj(top.mx, top.my)
	for _, r := range risers {
		if !t.riserFits(r, nx, ny, proj(r.mx, r.my)) {
			return stairRun{}, false
		}
	}
	z0 := float64(lvl.Sectors[lower].FloorHeight)
	z1 := float64(lvl.Sectors[upper].FloorHeight)
	if t1-t0 <= 0 || z1 <= z0 {
		return stairRun{}, false
	}
	m := (z1 - z0) / (t1 - t0)
	if m > stairMaxSlope {
		return stairRun{}, false
	}
	zAt := func(x, y float64) float64 { return z0 + m*(proj(x, y)-t0) }

	// Each intermediate riser must sit roughly where the ramp crosses its
	// step height — a staircase whose treads vary wildly isn't one ramp.
	for i := 1; i < len(risers)-1; i++ {
		r := risers[i]
		mid := (float64(lvl.Sectors[r.a].FloorHeight) + float64(lvl.Sectors[r.b].FloorHeight)) / 2
		if math.Abs(zAt(r.mx, r.my)-mid) > stairMaxRise {
			return stairRun{}, false
		}
	}
	// Every step vertex must lie between the bottom and top risers (a step
	// that spreads sideways past them would extrapolate the plane), with
	// full headroom under its own ceiling.
	for _, s := range steps {
		ceil := float64(lvl.Sectors[s].CeilingHeight)
		for _, li := range t.lines[s] {
			ld := lvl.Linedefs[li]
			for _, vi := range [2]uint16{ld.StartVertex, ld.EndVertex} {
				v := lvl.Vertexes[vi]
				x, y := float64(v.X), float64(v.Y)
				p := proj(x, y)
				if p < t0-stairRiserTol || p > t1+stairRiserTol {
					return stairRun{}, false
				}
				if ceil-zAt(x, y) < stairMinHeadroom {
					return stairRun{}, false
				}
			}
		}
	}

	// P1 on the bottom riser at the lower landing's (integer) height — the
	// engine rounds the pivot height to int16, so it must already be whole
	// for every step's plane to come out identical. P2 runs along the
	// riser (level), P3 straight up the ramp.
	px, py := bottom.mx, bottom.my
	plane := [3][3]float64{
		{px, py, z0},
		{px - ny*stairPlaneStep, py + nx*stairPlaneStep, z0},
		{px + nx*stairPlaneStep, py + ny*stairPlaneStep, z0 + m*stairPlaneStep},
	}
	return stairRun{lower: lower, upper: upper, steps: steps, plane: plane, slope: m}, true
}

// stairDefs turns runs into SLOPES floor-plane defs.
func stairDefs(runs []stairRun) []slopes.Def {
	var out []slopes.Def
	for _, r := range runs {
		for _, s := range r.steps {
			out = append(out, slopes.Def{SectorIndex: s, P1: r.plane[0], P2: r.plane[1], P3: r.plane[2]})
		}
	}
	return out
}

// slopedSectors lists the sectors a SLOPES def set already tilts or uses in
// an extra floor, so a pass leaves them alone.
func slopedSectors(planes []slopes.Def, floors []slopes.ExtraFloorDef) map[int]bool {
	out := map[int]bool{}
	for _, d := range planes {
		out[d.SectorIndex] = true
	}
	for _, f := range floors {
		out[f.TargetSector] = true
		out[f.ControlSector] = true
	}
	return out
}
