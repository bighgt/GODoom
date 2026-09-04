package engine

import (
	"testing"

	"twopointfive/assets"
	"twopointfive/raster"
)

// drawGroundShadows must not touch the renderer at all when config
// groundShadows is off (loadRealLevel leaves g.Raster nil, so a leak would
// panic). It's off by default, so a freshly loaded Game already covers this.
func TestDrawGroundShadowsGatedOff(t *testing.T) {
	g := loadRealLevel(t, "../testdata/DOOM1.WAD", "E1M1")
	g.groundShadows = false
	g.drawGroundShadows() // nil g.Raster — must be a no-op
}

// With shadows on and a real renderer, drawGroundShadows sweeps every
// grounded mobj + the player and darkens the frame somewhere.
func TestDrawGroundShadowsRunsOnRealLevel(t *testing.T) {
	g := loadRealLevel(t, "../testdata/DOOM1.WAD", "E1M1")
	tex, err := assets.New(g.WAD)
	if err != nil {
		t.Fatalf("assets.New: %v", err)
	}
	const w, h = 320, 200
	g.Raster = raster.New(tex, w, h)
	g.groundShadows = true

	// Put the camera on the player start and render so r.depth is filled.
	if len(g.mobjs) == 0 {
		t.Skip("no mobjs on E1M1")
	}
	g.Camera = raster.Camera{X: g.mobjs[0].X, Y: g.mobjs[0].Y, Z: g.mobjs[0].Z + EyeHeight, FOVDeg: 90}
	g.Raster.Render(g.Level, g.BSP, g.Camera)
	before := append([]byte(nil), g.Raster.Pix...)

	g.drawGroundShadows()

	// Not asserting a specific count (depends on what's in view), just that
	// the pass is wired and only ever darkens.
	for i := 0; i < len(before); i += 4 {
		if g.Raster.Pix[i] > before[i] || g.Raster.Pix[i+1] > before[i+1] || g.Raster.Pix[i+2] > before[i+2] {
			t.Fatalf("ground-shadow pass brightened pixel %d", i/4)
		}
	}
}
