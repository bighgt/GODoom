package engine

import (
	"testing"

	"twopointfive/engine/slopes"
	"twopointfive/wad"
)

// Test-only exports for package engine_test — the external test package
// that can import packages which themselves import engine (tools' library
// wad3d), which an in-package test can't without an import cycle.

// LoadRealLevelForTest is loadRealLevel for package engine_test.
func LoadRealLevelForTest(t *testing.T, path, mapName string) *Game {
	return loadRealLevel(t, path, mapName)
}

// ApplySlopesForTest parses a SLOPES lump body and applies it to g's
// current map, as a level load would.
func (g *Game) ApplySlopesForTest(t *testing.T, src []byte) {
	t.Helper()
	planes, floors, errs := slopes.Parse(src)
	for _, e := range errs {
		t.Errorf("SLOPES: %v", e)
	}
	g.slopeDefs, g.extraFloorDefs = planes, floors
	g.applySlopes()
}

// WalkForTest stands the player on the floor at (x, y), then moves by
// (dx, dy) each tic for n tics through the real movement path (tryMove +
// resolvePlayerZ), calling each(x, y, z) after every tic.
func (g *Game) WalkForTest(x, y, dx, dy float64, n int, each func(x, y, z float64)) {
	g.Camera.X, g.Camera.Y = x, y
	f, _ := g.sectorFloorCeil(x, y)
	g.groundPlayer(f)
	for i := 0; i < n; i++ {
		g.tryMove(dx, dy)
		for j := 0; j < 4; j++ {
			g.resolvePlayerZ(1.0 / 35 / 4)
		}
		each(g.Camera.X, g.Camera.Y, g.PlayerZ)
	}
}

// SoundForTest reports whether a session on w can resolve sound lump name
// through loadSound's full lookup (WAD, embedded weapon set, AltWAD).
func SoundForTest(w *wad.WAD, name string) bool {
	g := &Game{WAD: w}
	return g.loadSound(name) != nil
}
