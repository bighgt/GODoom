package nodebuild

import (
	"os"
	"testing"

	"twopointfive/bsp"
	"twopointfive/wad"
)

// testMaps yields (name, level, shippedTree) for a handful of first maps
// across whatever WADs are on disk.
func testMaps(t testing.TB) []struct {
	name    string
	lvl     *wad.Level
	shipped bsp.Node
} {
	t.Helper()
	type row = struct {
		name    string
		lvl     *wad.Level
		shipped bsp.Node
	}
	var out []row
	for _, p := range []string{
		"../testdata/DOOM1.WAD", "../wad/Game WADs/doom2.wad",
		"../wad/heretic.wad", "../wad/Game WADs/doom.wad",
	} {
		if _, err := os.Stat(p); err != nil {
			continue
		}
		w, err := wad.Load(p)
		if err != nil {
			continue
		}
		maps := w.ListMaps()
		for _, m := range firstN(maps, 3) {
			lvl, err := w.LoadLevel(m)
			if err != nil {
				continue
			}
			tree, err := bsp.Build(lvl)
			if err != nil {
				continue
			}
			out = append(out, row{p + " " + m, lvl, tree})
		}
	}
	if len(out) == 0 {
		t.Skip("no test WAD available")
	}
	return out
}

func firstN(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func TestBuildLeavesAreConvexSingleSector(t *testing.T) {
	for _, tm := range testMaps(t) {
		t.Run(tm.name, func(t *testing.T) { checkSingleSector(t, tm.lvl) })
	}
}

func checkSingleSector(t *testing.T, lvl *wad.Level) {
	res, err := Build(lvl)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(res.Subsectors) == 0 {
		t.Fatal("no subsectors produced")
	}
	lvl.Vertexes = res.Verts
	for si, ss := range res.Subsectors {
		if ss.SegCount == 0 {
			t.Errorf("subsector %d has no segs", si)
			continue
		}
		sector := -1
		for i := 0; i < int(ss.SegCount); i++ {
			seg := res.Segs[int(ss.FirstSeg)+i]
			_, fs, _ := bsp.SegSides(lvl, seg)
			if fs == nil {
				continue
			}
			idx := int(seg.Linedef)
			ld := lvl.Linedefs[idx]
			var sd uint16
			if seg.Direction == 0 {
				sd = ld.FrontSidedef
			} else {
				sd = ld.BackSidedef
			}
			if int(sd) >= len(lvl.Sidedefs) {
				continue
			}
			secIdx := int(lvl.Sidedefs[sd].Sector)
			if sector == -1 {
				sector = secIdx
			} else if sector != secIdx {
				t.Errorf("subsector %d mixes sectors %d and %d", si, sector, secIdx)
				break
			}
		}
	}
}

func TestBuildPointSectorMatchesShippedTree(t *testing.T) {
	for _, tm := range testMaps(t) {
		t.Run(tm.name, func(t *testing.T) {
			checkAgainstShipped(t, tm.lvl, tm.shipped)
		})
	}
}

func checkAgainstShipped(t *testing.T, lvl *wad.Level, shipped bsp.Node) {
	realVerts := append([]wad.Vertex(nil), lvl.Vertexes...)
	realSegs := append([]wad.Seg(nil), lvl.Segs...)

	// Sample points guaranteed to be inside real geometry: the centroid of
	// every subsector polygon of the SHIPPED tree. For each, the shipped
	// tree's own sector is ground truth; the built tree must agree.
	polys := bsp.SubsectorPolygons(shipped, lvl)

	res, err := Build(lvl)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	tested, agree := 0, 0
	var misses []string
	for _, poly := range polys {
		if len(poly) < 3 {
			continue
		}
		var cx, cy float64
		for _, p := range poly {
			cx += float64(p.X)
			cy += float64(p.Y)
		}
		cx /= float64(len(poly))
		cy /= float64(len(poly))

		lvl.Vertexes, lvl.Segs = realVerts, realSegs
		want := bsp.PointSectorIndex(shipped, lvl, float32(cx), float32(cy))
		lvl.Vertexes, lvl.Segs = res.Verts, res.Segs
		got := bsp.PointSectorIndex(res.Root, lvl, float32(cx), float32(cy))
		if want < 0 {
			continue
		}
		tested++
		if want == got {
			agree++
		} else if len(misses) < 15 {
			misses = append(misses, fmtMiss(cx, cy, want, got))
		}
	}
	if tested == 0 {
		t.Skip("no subsector polygons")
	}
	ratio := float64(agree) / float64(tested)
	t.Logf("subsector-centroid sector agreement: %d/%d = %.1f%%", agree, tested, ratio*100)
	for _, m := range misses {
		t.Log("  mismatch " + m)
	}
	if ratio < 0.98 {
		t.Errorf("built tree disagrees with the shipped NODES at %.1f%% of interior points (want <= 2%%)", (1-ratio)*100)
	}
}

func fmtMiss(x, y float64, want, got int) string {
	return itoaf(x) + "," + itoaf(y) + " want sector " + itoaf(float64(want)) + " got " + itoaf(float64(got))
}
func itoaf(f float64) string {
	n := int(f)
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

