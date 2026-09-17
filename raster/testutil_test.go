package raster

import (
	"os"
	"testing"

	"twopointfive/assets"
	"twopointfive/bsp"
	"twopointfive/wad"
)

// loadTestLevel loads the shareware IWAD's first map — real geometry/
// textures for the 2D overlay tests (crosshair/HUD/status bar/weapon) that
// want a live *assets.Textures without hand-building one.
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
