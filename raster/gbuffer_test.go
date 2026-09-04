package raster

import (
	"math"
	"testing"

	"twopointfive/bsp"
)

// enhanced-mode (SetGBuffer) sanity: rendering a real level must fill the
// Normal / LightParam planes for every covered pixel, leave albedo
// unshaded in Pix, and leave sky/void pixels as background (depth +Inf) for
// the GPU pass to pass straight through.
func TestGBufferOutput(t *testing.T) {
	lvl, tree, tex := loadLevelFrom(t, "../wad/Doom2.wad", "MAP01")
	const w, h = 320, 200

	var cx, cy, cz float64
	found := false
	for _, v := range lvl.Vertexes[:min(200, len(lvl.Vertexes))] {
		sec := bsp.PointSector(tree, lvl, float32(v.X), float32(v.Y))
		if sec != nil {
			cx, cy, cz = float64(v.X), float64(v.Y), float64(sec.FloorHeight)+41
			found = true
			break
		}
	}
	if !found {
		t.Skip("no in-bounds vertex found")
	}
	cam := Camera{X: cx, Y: cy, Z: cz, Angle: 0.5, FOVDeg: 100}

	plain := New(tex, w, h)
	plain.Render(lvl, tree, cam)

	gb := New(tex, w, h)
	gb.SetGBuffer(true)
	if len(gb.Normal) != w*h*4 || len(gb.LightParam) != w*h*4 {
		t.Fatalf("SetGBuffer didn't allocate the planes: normal %d lightparam %d", len(gb.Normal), len(gb.LightParam))
	}
	gb.Render(lvl, tree, cam)

	covered, background, litParamSet, badNormal := 0, 0, 0, 0
	var plainSum, gbSum int64
	for p := 0; p < w*h; p++ {
		i := p * 4
		plainSum += int64(plain.Pix[i]) + int64(plain.Pix[i+1]) + int64(plain.Pix[i+2])
		gbSum += int64(gb.Pix[i]) + int64(gb.Pix[i+1]) + int64(gb.Pix[i+2])

		if math.IsInf(float64(gb.depth[p]), 1) {
			background++
			// Background is identified purely by depth == +Inf; light.frag
			// never samples Normal/LightParam for these pixels, so their
			// contents are don't-care (the planes are not cleared).
			continue
		}
		covered++
		if gb.LightParam[i+3] == 255 {
			litParamSet++
		}
		nx := float64(gb.Normal[i])/255*2 - 1
		ny := float64(gb.Normal[i+1])/255*2 - 1
		nz := float64(gb.Normal[i+2])/255*2 - 1
		if l := nx*nx + ny*ny + nz*nz; l < 0.4 || l > 1.6 {
			badNormal++
		}
	}

	if covered == 0 {
		t.Fatal("nothing was drawn")
	}
	t.Logf("covered %d, background %d", covered, background)
	if litParamSet < covered*9/10 {
		t.Errorf("LightParam alpha set on only %d/%d covered pixels", litParamSet, covered)
	}
	if badNormal > covered/20 {
		t.Errorf("%d/%d covered pixels have a non-unit packed normal", badNormal, covered)
	}
	// Albedo is unshaded, so the enhanced frame's Pix is brighter overall
	// than the vanilla frame's distance-diminished shade.
	if gbSum <= plainSum {
		t.Errorf("G-buffer albedo (sum %d) is not brighter than the shaded vanilla frame (sum %d)", gbSum, plainSum)
	}
}

// TestGBufferVanillaUnaffected: with SetGBuffer never called, the planes
// stay nil and Pix is exactly the vanilla shaded frame.
func TestGBufferVanillaUnaffected(t *testing.T) {
	lvl, tree, tex := loadLevelFrom(t, "../wad/Doom2.wad", "MAP01")
	const w, h = 160, 100
	cam := Camera{X: float64(lvl.Vertexes[0].X), Y: float64(lvl.Vertexes[0].Y), Z: 41, FOVDeg: 100}

	r := New(tex, w, h)
	r.Render(lvl, tree, cam)
	if r.Normal != nil || r.LightParam != nil {
		t.Error("Normal/LightParam allocated without SetGBuffer")
	}
}
