package raster

import (
	"os"
	"testing"

	"twopointfive/assets"
	"twopointfive/bsp"
	"twopointfive/wad"
)

func loadLevelFrom(t testing.TB, path, mapName string) (*wad.Level, bsp.Node, *assets.Textures) {
	t.Helper()
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
	tree, err := bsp.Build(lvl)
	if err != nil {
		t.Fatalf("bsp.Build: %v", err)
	}
	tex, err := assets.New(w)
	if err != nil {
		t.Fatalf("assets.New: %v", err)
	}
	return lvl, tree, tex
}

// TestDoom2Map01ParallelVsSerial sweeps a grid of camera positions/angles
// over Doom II MAP01 and asserts the strip-parallel frame is byte-identical
// to a single-strip one.
func TestDoom2Map01ParallelVsSerial(t *testing.T) {
	lvl, tree, tex := loadLevelFrom(t, "../wad/Doom2.wad", "MAP01")
	const w, h = 640, 400

	ser := New(tex, w, h)
	setStripCount(ser, 1)
	par := New(tex, w, h)
	if len(par.strips) < 2 {
		setStripCount(par, 8)
	}

	minX, minY, maxX, maxY := 1e9, 1e9, -1e9, -1e9
	for _, v := range lvl.Vertexes {
		x, y := float64(v.X), float64(v.Y)
		minX, maxX = min(minX, x), max(maxX, x)
		minY, maxY = min(minY, y), max(maxY, y)
	}

	samples := 0
	for gx := 1; gx < 8; gx++ {
		for gy := 1; gy < 8; gy++ {
			cx := minX + (maxX-minX)*float64(gx)/8
			cy := minY + (maxY-minY)*float64(gy)/8
			sec := bsp.PointSector(tree, lvl, float32(cx), float32(cy))
			if sec == nil {
				continue
			}
			z := float64(sec.FloorHeight) + 41
			for _, ang := range []float64{0, 1.57, 3.14, 4.71} {
				cam := Camera{X: cx, Y: cy, Z: z, Angle: ang, FOVDeg: 100}
				ser.Render(lvl, tree, cam)
				par.Render(lvl, tree, cam)
				samples++
				for i := range ser.Pix {
					if ser.Pix[i] != par.Pix[i] {
						t.Fatalf("parallel != serial at (%.0f,%.0f) ang=%.2f", cx, cy, ang)
					}
				}
			}
		}
	}
	t.Logf("swept %d frames, %d strips — identical", samples, len(par.strips))
}
