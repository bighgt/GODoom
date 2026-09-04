package raster

import (
	"bytes"
	"math"
	"os"
	"testing"

	"twopointfive/assets"
	"twopointfive/bsp"
	"twopointfive/wad"
)

// setStripCount rebuilds r.strips with exactly n workers — the same layout
// New produces, but with a count the test controls so a parallel render can
// be compared against a single-threaded one.
func setStripCount(r *Renderer, n int) {
	strips := make([]stripCtx, n)
	for i := range strips {
		strips[i] = stripCtx{
			x0:        i * r.Width / n,
			x1:        (i+1)*r.Width/n - 1,
			ceilClip:  make([]int, r.Width),
			floorClip: make([]int, r.Width),
		}
	}
	r.strips = strips
}

func loadTestLevel(t testing.TB) (*wad.Level, bsp.Node, *assets.Textures) {
	t.Helper()
	const path = "../testdata/DOOM1.WAD"
	if _, err := os.Stat(path); err != nil {
		t.Skipf("no %s on disk: %v", path, err)
	}
	w, err := wad.Load(path)
	if err != nil {
		t.Fatalf("wad.Load: %v", err)
	}
	maps := w.ListMaps()
	if len(maps) == 0 {
		t.Fatal("no maps in WAD")
	}
	lvl, err := w.LoadLevel(maps[0])
	if err != nil {
		t.Fatalf("LoadLevel(%s): %v", maps[0], err)
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

// testCameras returns the player-1 start plus a handful of yaw/pitch
// variations around it — enough geometry (walls, steps, sky, flats) to
// exercise every span path.
func testCameras(lvl *wad.Level, tree bsp.Node) []Camera {
	var px, py, ang float64
	for _, th := range lvl.Things {
		if th.Type == 1 { // player 1 start
			px, py = float64(th.X), float64(th.Y)
			ang = float64(th.Angle) * math.Pi / 180
		}
	}
	z := 41.0
	if sec := bsp.PointSector(tree, lvl, float32(px), float32(py)); sec != nil {
		z = float64(sec.FloorHeight) + 41
	}
	cams := []Camera{}
	for _, da := range []float64{0, 0.9, 1.9, 2.9, 3.9, 4.9} {
		cams = append(cams, Camera{X: px, Y: py, Z: z, Angle: ang + da, FOVDeg: 100})
	}
	cams = append(cams,
		Camera{X: px, Y: py, Z: z, Angle: ang, Pitch: 0.4, FOVDeg: 100},
		Camera{X: px, Y: py, Z: z, Angle: ang, Pitch: -0.4, FOVDeg: 100},
	)
	return cams
}

// TestParallelMatchesSerial is the core correctness guarantee for strip
// parallelism: partitioning the screen into worker strips must produce a
// byte-for-byte identical frame to rendering it on one worker. Run it with
// -race to also prove the workers never touch the same memory.
func TestParallelMatchesSerial(t *testing.T) {
	lvl, tree, tex := loadTestLevel(t)
	const w, h = 640, 400

	serial := New(tex, w, h)
	setStripCount(serial, 1)

	par := New(tex, w, h)
	if len(par.strips) < 2 {
		setStripCount(par, 8) // force real parallelism on a 1-core CI box
	}

	for i, cam := range testCameras(lvl, tree) {
		serial.Render(lvl, tree, cam)
		want := append([]byte(nil), serial.Pix...)

		par.Render(lvl, tree, cam)
		if !bytes.Equal(want, par.Pix) {
			t.Fatalf("camera %d: parallel frame differs from serial frame (%d strips)", i, len(par.strips))
		}

		// And rendering the same camera again must be identical (no
		// cross-frame state leak between strips).
		par.Render(lvl, tree, cam)
		if !bytes.Equal(want, par.Pix) {
			t.Fatalf("camera %d: parallel render not deterministic frame-to-frame", i)
		}
	}
}

func BenchmarkRenderFullSceneParallel(b *testing.B) {
	lvl, tree, tex := loadTestLevel(b)
	r := New(tex, 1920, 1080)
	cams := testCameras(lvl, tree)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.Render(lvl, tree, cams[i%len(cams)])
	}
}

func BenchmarkRenderFullScene720pParallel(b *testing.B) {
	lvl, tree, tex := loadTestLevel(b)
	r := New(tex, 1280, 720)
	cams := testCameras(lvl, tree)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.Render(lvl, tree, cams[i%len(cams)])
	}
}

func BenchmarkRenderFullSceneSerial(b *testing.B) {
	lvl, tree, tex := loadTestLevel(b)
	r := New(tex, 1920, 1080)
	setStripCount(r, 1)
	cams := testCameras(lvl, tree)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.Render(lvl, tree, cams[i%len(cams)])
	}
}

// BenchmarkRenderGBuffer1440p is the enhanced-lighting CPU cost at a heavy
// full-screen resolution — the path the "windowed fast, fullscreen slow"
// report is about.
func BenchmarkRenderGBuffer1440p(b *testing.B) {
	lvl, tree, tex := loadTestLevel(b)
	r := New(tex, 2560, 1440)
	r.SetGBuffer(true)
	cams := testCameras(lvl, tree)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.Render(lvl, tree, cams[i%len(cams)])
	}
}
