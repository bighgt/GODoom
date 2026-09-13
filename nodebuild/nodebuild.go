// Package nodebuild builds a BSP tree for a Doom map from its raw geometry
// (LINEDEFS / SIDEDEFS / VERTEXES / SECTORS) — the NODES / SSECTORS / SEGS
// the engine's renderer needs but that an edited or newly-drawn map does
// not yet carry. It emits the exact twopointfive/bsp types, so the software
// renderer and bsp.SubsectorPolygons consume the result unchanged, and it
// can also serialize the tree to the on-disk lump formats for saving.
//
// The algorithm is the classic recursive one (id's BSP, glBSP, ZDBSP
// lineage): turn each one/two-sided linedef into one or two segs, then at
// each step pick a seg whose line best splits the remaining set — fewest
// splits, most balanced — partition (splitting straddling segs at the
// intersection), and recurse; a set with no dividing line left is a convex
// leaf. Partitioning quality is "good enough for this engine", not
// bit-matched to any particular vanilla builder.
package nodebuild

import (
	"fmt"
	"math"

	"twopointfive/bsp"
	"twopointfive/wad"
)

// Result is one build. Verts is Level.Vertexes plus any vertices the build
// added where it split a seg — the caller must point the level at it
// (lvl.Vertexes = res.Verts) before rendering, since Seg vertex indices
// refer to it.
type Result struct {
	Root       bsp.Node
	Segs       []wad.Seg
	Subsectors []wad.Subsector
	Verts      []wad.Vertex
}

const (
	// onLineEps: perpendicular distance (world units, after normalising by
	// the partition length) under which a seg endpoint counts as lying ON
	// the partition line. Kept tiny — the build works in exact floats, and
	// Doom's integer geometry means a real crossing is at least ~0.5 units
	// off, so a genuinely collinear endpoint is essentially 0. Too large a
	// value would misread a shallow crossing as "entirely on one side" and
	// drop that wall from the other subtree — a leak between sectors.
	onLineEps = 0.05
	splitCost = 8    // score weight of one seg split vs. one unit of imbalance
	maxDepth  = 4096 // recursion guard against a pathological / malformed map
)

// wseg is a seg during the build: float endpoints (so split points are
// exact) plus the bookkeeping a wad.Seg needs.
type wseg struct {
	ax, ay, bx, by float64 // start / end, world units
	linedef        int
	dir            int     // 0 = along the linedef, 1 = against it
	offset         float64 // texture offset of the start point along the linedef
	svi, evi       int     // start / end vertex indices, filled at emit time (-1 until then)
}

// Build constructs the tree for lvl. It returns an error only for input it
// cannot make progress on (a malformed linedef set, or the depth guard
// tripping); a geometrically odd but closed map still builds.
func Build(lvl *wad.Level) (*Result, error) {
	verts := append([]wad.Vertex(nil), lvl.Vertexes...)
	vcache := map[[2]int]int{}
	addVert := func(x, y float64) int {
		key := [2]int{int(math.Round(x)), int(math.Round(y))}
		if i, ok := vcache[key]; ok {
			return i
		}
		i := len(verts)
		verts = append(verts, wad.Vertex{X: int16(key[0]), Y: int16(key[1])})
		vcache[key] = i
		return i
	}
	// seed the cache with the existing vertices so a split that lands on one
	// reuses it
	for i, v := range verts {
		vcache[[2]int{int(v.X), int(v.Y)}] = i
	}

	segs := make([]wseg, 0, len(lvl.Linedefs)*2)
	for li := range lvl.Linedefs {
		ld := lvl.Linedefs[li]
		if int(ld.StartVertex) >= len(lvl.Vertexes) || int(ld.EndVertex) >= len(lvl.Vertexes) {
			continue
		}
		s := lvl.Vertexes[ld.StartVertex]
		e := lvl.Vertexes[ld.EndVertex]
		sx, sy, ex, ey := float64(s.X), float64(s.Y), float64(e.X), float64(e.Y)
		if sx == ex && sy == ey {
			continue // zero-length
		}
		if ld.FrontSidedef != wad.NoSidedef && int(ld.FrontSidedef) < len(lvl.Sidedefs) {
			segs = append(segs, wseg{ax: sx, ay: sy, bx: ex, by: ey, linedef: li, dir: 0, svi: -1, evi: -1})
		}
		if ld.BackSidedef != wad.NoSidedef && int(ld.BackSidedef) < len(lvl.Sidedefs) {
			segs = append(segs, wseg{ax: ex, ay: ey, bx: sx, by: sy, linedef: li, dir: 1, svi: -1, evi: -1})
		}
	}
	if len(segs) == 0 {
		return nil, fmt.Errorf("nodebuild: map has no drawable linedefs")
	}

	b := &builder{addVert: addVert}
	root, err := b.node(segs, 0)
	if err != nil {
		return nil, err
	}
	// resolve every emitted seg's vertex indices now that no more splits
	// will happen
	out := make([]wad.Seg, len(b.segs))
	for i, s := range b.segs {
		out[i] = wad.Seg{
			StartVertex: uint16(addVert(s.ax, s.ay)),
			EndVertex:   uint16(addVert(s.bx, s.by)),
			Linedef:     uint16(s.linedef),
			Direction:   int16(s.dir),
			Offset:      int16(clampOff(s.offset)),
		}
	}
	return &Result{Root: root, Segs: out, Subsectors: b.ssectors, Verts: verts}, nil
}

type builder struct {
	addVert  func(x, y float64) int
	segs     []wseg // emitted leaf segs, contiguous per subsector
	ssectors []wad.Subsector
}

func (b *builder) node(in []wseg, depth int) (bsp.Node, error) {
	if depth > maxDepth {
		return nil, fmt.Errorf("nodebuild: recursion too deep (%d) — map geometry is not a closed partition", depth)
	}
	pi, ok := bestPartition(in)
	if !ok {
		return b.leaf(in), nil
	}
	p := in[pi]
	pdx, pdy := p.bx-p.ax, p.by-p.ay

	var right, left []wseg
	for _, s := range in {
		switch classify(p, s) {
		case sideRight:
			right = append(right, s)
		case sideLeft:
			left = append(left, s)
		case sideOn:
			if s.ax != s.bx || s.ay != s.by {
				if (s.bx-s.ax)*pdx+(s.by-s.ay)*pdy >= 0 {
					right = append(right, s)
				} else {
					left = append(left, s)
				}
			}
		case sideCross:
			rs, ls := splitSeg(p, s)
			right = append(right, rs)
			left = append(left, ls)
		}
	}
	if len(right) == 0 || len(left) == 0 {
		// bestPartition promised a real split; a rounding edge made one side
		// empty. Treat the set as a leaf rather than loop.
		return b.leaf(in), nil
	}
	rn, err := b.node(right, depth+1)
	if err != nil {
		return nil, err
	}
	ln, err := b.node(left, depth+1)
	if err != nil {
		return nil, err
	}
	return &bsp.InnerNode{
		X: float32(p.ax), Y: float32(p.ay),
		DX: float32(pdx), DY: float32(pdy),
		Right: rn, Left: ln,
	}, nil
}

func (b *builder) leaf(in []wseg) bsp.Node {
	first := len(b.segs)
	b.segs = append(b.segs, in...)
	ss := wad.Subsector{FirstSeg: uint16(first), SegCount: uint16(len(in))}
	idx := len(b.ssectors)
	b.ssectors = append(b.ssectors, ss)
	return &bsp.Leaf{Subsector: ss, Index: idx}
}

// ---- partition selection & classification -----------------------------

type sideKind int

const (
	sideRight sideKind = iota
	sideLeft
	sideOn
	sideCross
)

// signedDist is (p - line.a) x line.dir — positive on the right/front side,
// matching bsp.InnerNode.Side.
func signedDist(p wseg, x, y float64) float64 {
	return (x-p.ax)*(p.by-p.ay) - (y-p.ay)*(p.bx-p.ax)
}

func classify(p, s wseg) sideKind {
	d1 := signedDist(p, s.ax, s.ay)
	d2 := signedDist(p, s.bx, s.by)
	// normalise by the partition length so onLineEps is a real distance
	if L := math.Hypot(p.bx-p.ax, p.by-p.ay); L > 1e-9 {
		d1 /= L
		d2 /= L
	}
	r := d1 > onLineEps || d2 > onLineEps
	l := d1 < -onLineEps || d2 < -onLineEps
	switch {
	case r && l:
		return sideCross
	case r:
		return sideRight
	case l:
		return sideLeft
	default:
		return sideOn
	}
}

// splitSeg cuts s at its intersection with p's line, returning the piece on
// the right and the piece on the left. The texture offset accumulates along
// the seg from its start.
func splitSeg(p, s wseg) (right, left wseg) {
	d1 := signedDist(p, s.ax, s.ay)
	d2 := signedDist(p, s.bx, s.by)
	t := d1 / (d1 - d2)
	ix := s.ax + (s.bx-s.ax)*t
	iy := s.ay + (s.by-s.ay)*t
	cutOff := s.offset + math.Hypot(ix-s.ax, iy-s.ay)

	a := s
	a.bx, a.by = ix, iy
	c := s
	c.ax, c.ay, c.offset = ix, iy, cutOff
	if d1 >= 0 {
		return a, c
	}
	return c, a
}

// bestPartition returns the index of the seg whose line best divides in,
// and false when no seg's line puts any seg on both sides (the set is
// convex → a leaf). Every seg is tried as a candidate: subsampling risks
// missing the only valid partition for a node and merging two regions into
// one leaf (a "leak" between sectors).
func bestPartition(in []wseg) (int, bool) {
	best, bestScore := -1, math.MaxFloat64
	for i := 0; i < len(in); i++ {
		p := in[i]
		if p.ax == p.bx && p.ay == p.by {
			continue
		}
		var rn, ln, splits int
		for _, s := range in {
			switch classify(p, s) {
			case sideRight:
				rn++
			case sideLeft:
				ln++
			case sideCross:
				splits++
				rn++
				ln++
			case sideOn:
				if (s.bx-s.ax)*(p.bx-p.ax)+(s.by-s.ay)*(p.by-p.ay) >= 0 {
					rn++
				} else {
					ln++
				}
			}
		}
		if rn == 0 || ln == 0 {
			continue // not a dividing line
		}
		score := float64(splits*splitCost + abs(rn-ln))
		if score < bestScore {
			best, bestScore = i, score
		}
	}
	return best, best >= 0
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func clampOff(o float64) int {
	i := int(math.Round(o))
	if i > 32767 {
		return 32767
	}
	if i < -32768 {
		return -32768
	}
	return i
}
