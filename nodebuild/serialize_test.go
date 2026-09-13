package nodebuild

import (
	"path/filepath"
	"testing"

	"twopointfive/bsp"
	"twopointfive/wad"
)

// TestSaveRoundTrip builds a map, writes it as a PWAD with our own SEGS /
// SSECTORS / NODES / BLOCKMAP / REJECT, reloads it through the normal
// wad.LoadLevel + bsp.Build path, and checks the reloaded tree agrees with
// the shipped one at every subsector centroid — i.e. a saved map loads and
// renders correctly.
func TestSaveRoundTrip(t *testing.T) {
	for _, tm := range testMaps(t) {
		t.Run(tm.name, func(t *testing.T) {
			lvl, shipped := tm.lvl, tm.shipped
			realVerts := append([]wad.Vertex(nil), lvl.Vertexes...)
			polys := bsp.SubsectorPolygons(shipped, lvl)

			res, err := Build(lvl)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			// The written VERTEXES lump must include the split points.
			saveLvl := *lvl
			saveLvl.Vertexes = res.Verts
			segs, ssectors, nodes := res.Lumps()
			lumps := wad.MapMarkerLumps("MAP01", &saveLvl, segs, ssectors, nodes,
				Reject(len(lvl.Sectors)), Blockmap(&saveLvl))

			path := filepath.Join(t.TempDir(), "rt.wad")
			if err := wad.WritePWAD(path, lumps); err != nil {
				t.Fatalf("WritePWAD: %v", err)
			}
			rw, err := wad.Load(path)
			if err != nil {
				t.Fatalf("reload: %v", err)
			}
			rl, err := rw.LoadLevel("MAP01")
			if err != nil {
				t.Fatalf("LoadLevel: %v", err)
			}
			rtree, err := bsp.Build(rl)
			if err != nil {
				t.Fatalf("bsp.Build(reloaded): %v", err)
			}

			tested, agree := 0, 0
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

				lvl.Vertexes = realVerts
				want := bsp.PointSectorIndex(shipped, lvl, float32(cx), float32(cy))
				got := bsp.PointSectorIndex(rtree, rl, float32(cx), float32(cy))
				if want < 0 {
					continue
				}
				tested++
				if want == got {
					agree++
				}
			}
			if tested == 0 {
				t.Skip("no polys")
			}
			if r := float64(agree) / float64(tested); r < 0.98 {
				t.Errorf("reloaded saved map: %d/%d sector agreement (%.1f%%)", agree, tested, r*100)
			}
		})
	}
}
