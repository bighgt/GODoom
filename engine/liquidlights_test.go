package engine

import (
	"testing"

	"twopointfive/wad"
)

func TestLiquidKindOfFlat(t *testing.T) {
	cases := map[string]int{
		"LAVA1":    liqLava,
		"LAVA4":    liqLava,
		"FIRELAVA": liqLava,
		"NUKAGE1":  liqAcid,
		"NUKAGE3":  liqAcid,
		"SLIME05":  liqAcid,
		"SLIME16":  liqAcid,
		"FWATER1":  liqWater,
		"BLOOD3":   liqBlood,
		"FLOOR0_1": liqNone,
		"CEIL1_1":  liqNone,
		"":         liqNone,
	}
	for flat, want := range cases {
		if got := liquidKindOfFlat(flat); got != want {
			t.Errorf("liquidKindOfFlat(%q) = %d, want %d", flat, got, want)
		}
	}
}

func TestSampleSectorPoints(t *testing.T) {
	if got := sampleSectorPoints(0, 0, 128, 128); len(got) != 1 {
		t.Errorf("small pool: %d points, want 1", len(got))
	}
	if got := sampleSectorPoints(0, 0, 2000, 128); len(got) != 2 {
		t.Errorf("long pool: %d points, want 2", len(got))
	}
	if got := sampleSectorPoints(0, 0, 2000, 2000); len(got) != 4 {
		t.Errorf("big lake: %d points, want 4", len(got))
	}
	// Points must sit inside the bbox.
	for _, p := range sampleSectorPoints(-100, -100, 3000, 3000) {
		if p[0] < -100 || p[0] > 3000 || p[1] < -100 || p[1] > 3000 {
			t.Errorf("sample point %v outside bbox", p)
		}
	}
}

// A lava sector must seed a warm light; a nukage sector a green one; the
// count must land in buildStaticLights' total.
func TestLavaAndNukageSeedLights(t *testing.T) {
	g := &Game{}
	g.Level = fakeLiquidLevel()
	g.ensureSectorIndex()

	g.appendLiquidLights(1)

	var lava, acid int
	for _, sl := range g.staticLights {
		switch {
		case sl.r > 0.8 && sl.g < 0.6 && sl.b < 0.3:
			lava++
		case sl.g > 0.7 && sl.r < 0.5:
			acid++
		}
	}
	if lava == 0 {
		t.Error("no lava (warm) light produced")
	}
	if acid == 0 {
		t.Error("no nukage (green) light produced")
	}
}

// fakeLiquidLevel builds a 2-sector level: sector 0 floored LAVA1, sector 1
// floored NUKAGE1, each a ~256u square, sharing no lines (two separate
// boxes) so sectorBBox has vertices to work with.
func fakeLiquidLevel() *wad.Level {
	// square 0: (0,0)-(256,256); square 1: (512,0)-(768,256)
	verts := []wad.Vertex{
		{X: 0, Y: 0}, {X: 256, Y: 0}, {X: 256, Y: 256}, {X: 0, Y: 256},
		{X: 512, Y: 0}, {X: 768, Y: 0}, {X: 768, Y: 256}, {X: 512, Y: 256},
	}
	sides := []wad.Sidedef{{Sector: 0}, {Sector: 0}, {Sector: 0}, {Sector: 0},
		{Sector: 1}, {Sector: 1}, {Sector: 1}, {Sector: 1}}
	mk := func(a, b, side uint16) wad.Linedef {
		return wad.Linedef{StartVertex: a, EndVertex: b, FrontSidedef: side, BackSidedef: wad.NoSidedef}
	}
	lines := []wad.Linedef{
		mk(0, 1, 0), mk(1, 2, 1), mk(2, 3, 2), mk(3, 0, 3),
		mk(4, 5, 4), mk(5, 6, 5), mk(6, 7, 6), mk(7, 4, 7),
	}
	secs := []wad.Sector{
		{FloorHeight: 0, CeilingHeight: 128, FloorTexture: "LAVA1", CeilingTexture: "F_SKY1", LightLevel: 128},
		{FloorHeight: 0, CeilingHeight: 128, FloorTexture: "NUKAGE1", CeilingTexture: "F_SKY1", LightLevel: 128},
	}
	return &wad.Level{Vertexes: verts, Sidedefs: sides, Linedefs: lines, Sectors: secs}
}
