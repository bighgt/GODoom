package bsp

import (
	"math"
	"os"
	"testing"

	"twopointfive/wad"
)

func loadLevelForPoly(t *testing.T, mapName string) (*wad.Level, Node) {
	t.Helper()
	const path = "../testdata/DOOM1.WAD"
	if _, err := os.Stat(path); err != nil {
		t.Skipf("no %s: %v", path, err)
	}
	w, err := wad.Load(path)
	if err != nil {
		t.Fatalf("wad.Load: %v", err)
	}
	lvl, err := w.LoadLevel(mapName)
	if err != nil {
		t.Fatalf("LoadLevel(%s): %v", mapName, err)
	}
	tree, err := Build(lvl)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return lvl, tree
}

func polyArea(p []Point) float64 {
	var a float64
	for i := range p {
		q := p[(i+1)%len(p)]
		a += float64(p[i].X)*float64(q.Y) - float64(q.X)*float64(p[i].Y)
	}
	return a / 2
}

func convexCCW(p []Point) bool {
	if len(p) < 3 {
		return false
	}
	for i := range p {
		a, b, c := p[i], p[(i+1)%len(p)], p[(i+2)%len(p)]
		cr := (float64(b.X)-float64(a.X))*(float64(c.Y)-float64(a.Y)) -
			(float64(b.Y)-float64(a.Y))*(float64(c.X)-float64(a.X))
		if cr < -4.0 { // allow small negative from float noise / bridge edges
			return false
		}
	}
	return true
}

// insideConvex reports whether (x,y) is inside/on a CCW convex polygon,
// allowing ~2 map units of perpendicular slack outside any edge (int16
// partition truncation + the collinear-vertex prune in finish() each move a
// reconstructed edge a fraction of a unit off the seg vertex it should pass
// through).
func insideConvex(p []Point, x, y float64) bool {
	for i := range p {
		a, b := p[i], p[(i+1)%len(p)]
		ex, ey := float64(b.X)-float64(a.X), float64(b.Y)-float64(a.Y)
		cr := ex*(y-float64(a.Y)) - ey*(x-float64(a.X))
		if l := math.Hypot(ex, ey); l > 0 {
			cr /= l // raw cross -> signed perpendicular distance (map units)
		}
		if cr < -2.0 {
			return false
		}
	}
	return true
}

func distinctSegVerts(lvl *wad.Level, ss wad.Subsector) int {
	seen := map[[2]int16]bool{}
	first, n := int(ss.FirstSeg), int(ss.SegCount)
	if first+n > len(lvl.Segs) {
		return 0
	}
	for s := 0; s < n; s++ {
		sg := lvl.Segs[first+s]
		for _, vi := range []uint16{sg.StartVertex, sg.EndVertex} {
			v := lvl.Vertexes[vi]
			seen[[2]int16{v.X, v.Y}] = true
		}
	}
	return len(seen)
}

// segRingOpen reports whether a subsector's segs do NOT form a closed loop
// (last seg's end != first seg's start) — those are the ones whose polygon
// needs a reconstructed bridging edge to close.
func segRingOpen(lvl *wad.Level, ss wad.Subsector) bool {
	first, n := int(ss.FirstSeg), int(ss.SegCount)
	if n < 2 || first+n > len(lvl.Segs) {
		return false
	}
	a := lvl.Vertexes[lvl.Segs[first].StartVertex]
	b := lvl.Vertexes[lvl.Segs[first+n-1].EndVertex]
	return a != b
}

func TestSubsectorPolygonsShapeAndCoverage(t *testing.T) {
	lvl, tree := loadLevelForPoly(t, "E1M1")
	polys := SubsectorPolygons(tree, lvl)

	if len(polys) != len(lvl.Subsectors) {
		t.Fatalf("got %d polygons, want %d subsectors", len(polys), len(lvl.Subsectors))
	}

	nonNil, bridgedOK := 0, 0
	for i, p := range polys {
		if p == nil {
			continue
		}
		nonNil++
		if len(p) < 3 {
			t.Fatalf("subsector %d polygon has %d verts", i, len(p))
		}
		if !convexCCW(p) {
			t.Fatalf("subsector %d polygon is not convex-CCW", i)
		}
		if polyArea(p) <= 0 {
			t.Fatalf("subsector %d polygon area %.2f not positive (bad winding)", i, polyArea(p))
		}

		ss := lvl.Subsectors[i]

		// THE correctness check for the primary (seg-hull) path: a subsector
		// with >= 3 distinct seg corners must contain every one of them.
		// The < 3-corner sliver leaves use a best-effort partition-clip
		// fallback (pending GL nodes) and are only checked for shape above.
		if distinctSegVerts(lvl, ss) >= 3 {
			for s := 0; s < int(ss.SegCount); s++ {
				seg := lvl.Segs[int(ss.FirstSeg)+s]
				for _, vi := range []uint16{seg.StartVertex, seg.EndVertex} {
					v := lvl.Vertexes[vi]
					if !insideConvex(p, float64(v.X), float64(v.Y)) {
						t.Fatalf("subsector %d: its own seg vertex (%d,%d) is outside its polygon", i, v.X, v.Y)
					}
				}
			}
		}
		if segRingOpen(lvl, ss) {
			bridgedOK++ // an open seg ring that still produced a valid closed polygon
		}
	}

	if nonNil < len(polys)*4/5 {
		t.Fatalf("only %d/%d subsectors got a polygon", nonNil, len(polys))
	}
	if bridgedOK == 0 {
		t.Error("no subsector with an open seg ring was closed — the reconstruction isn't bridging gaps")
	}
	t.Logf("subsectors=%d withPolygon=%d bridged=%d", len(polys), nonNil, bridgedOK)
}

func TestSubsectorPolygonsDeterministic(t *testing.T) {
	lvl, tree := loadLevelForPoly(t, "E1M1")
	a := SubsectorPolygons(tree, lvl)
	b := SubsectorPolygons(tree, lvl)
	if len(a) != len(b) {
		t.Fatal("length differs between runs")
	}
	for i := range a {
		if len(a[i]) != len(b[i]) {
			t.Fatalf("subsector %d vert count differs: %d vs %d", i, len(a[i]), len(b[i]))
		}
		for j := range a[i] {
			if a[i][j] != b[i][j] {
				t.Fatalf("subsector %d vert %d differs", i, j)
			}
		}
	}
}
