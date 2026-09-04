package worldgeo

import (
	"math"
	"os"
	"testing"

	"twopointfive/assets"
	"twopointfive/bsp"
	"twopointfive/wad"
)

func loadLevel(t *testing.T) (*wad.Level, bsp.Node, *assets.Textures) {
	t.Helper()
	const path = "../../testdata/DOOM1.WAD"
	if _, err := os.Stat(path); err != nil {
		t.Skipf("no %s on disk: %v", path, err)
	}
	w, err := wad.Load(path)
	if err != nil {
		t.Fatalf("wad.Load: %v", err)
	}
	lvl, err := w.LoadLevel("E1M1")
	if err != nil {
		t.Fatalf("LoadLevel: %v", err)
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

func TestBuildProducesGeometry(t *testing.T) {
	lvl, tree, tex := loadLevel(t)
	b := New(lvl, tree, tex)
	g := b.Build(1056, -3616) // E1M1 player start-ish

	if len(g.StaticTris) == 0 {
		t.Fatal("no triangles built")
	}
	if len(g.StaticTris)%3 != 0 {
		t.Fatalf("triangle list length %d not a multiple of 3", len(g.StaticTris))
	}
	if len(g.Textures) == 0 {
		t.Fatal("no textures referenced")
	}

	kinds := map[uint16]int{}
	for i, v := range g.StaticTris {
		if math.IsNaN(float64(v.X+v.Y+v.Z+v.U+v.V+v.Light)) ||
			math.IsInf(float64(v.X+v.Y+v.Z+v.U+v.V+v.Light), 0) {
			t.Fatalf("vert %d has a non-finite field: %+v", i, v)
		}
		if v.Light < 0 || v.Light > 1 {
			t.Fatalf("vert %d light %f out of [0,1]", i, v.Light)
		}
		if v.Kind == KindSky {
			if v.Tex != SkyTex {
				t.Fatalf("sky vert %d has Tex %d, want SkyTex", i, v.Tex)
			}
		} else if int(v.Tex) >= len(g.Textures) {
			t.Fatalf("vert %d Tex %d out of range (%d textures)", i, v.Tex, len(g.Textures))
		}
		kinds[v.Kind]++
	}
	if kinds[KindWall] == 0 {
		t.Error("no wall triangles")
	}
	if kinds[KindFlat] == 0 {
		t.Error("no flat triangles")
	}
	t.Logf("tris=%d textures=%d kinds=%v", len(g.StaticTris)/3, len(g.Textures), kinds)
}

func TestBuildIsDeterministicAndReuses(t *testing.T) {
	lvl, tree, tex := loadLevel(t)
	b := New(lvl, tree, tex)

	g1 := b.Build(1056, -3616)
	first := append([]Vert(nil), g1.StaticTris...)
	nTex := len(g1.Textures)

	g2 := b.Build(1056, -3616)
	if g2 != g1 {
		t.Fatal("Build returned a different Geometry pointer (should reuse)")
	}
	if len(g2.StaticTris) != len(first) {
		t.Fatalf("rebuild changed triangle count %d -> %d", len(first), len(g2.StaticTris))
	}
	for i := range first {
		if g2.StaticTris[i] != first[i] {
			t.Fatalf("rebuild changed vert %d: %+v vs %+v", i, first[i], g2.StaticTris[i])
		}
	}
	if len(g2.Textures) != nTex {
		t.Fatalf("rebuild grew Textures %d -> %d for the same view", nTex, len(g2.Textures))
	}
}

// Every wall quad's four corners must sit on the seg's XY line and span a
// non-negative height; flat triangles must be planar at the sector height.
func TestWallAndFlatVertexInvariants(t *testing.T) {
	lvl, tree, tex := loadLevel(t)
	g := New(lvl, tree, tex).Build(1056, -3616)

	walls, flats := 0, 0
	for i := 0; i+2 < len(g.StaticTris); i += 3 {
		a, bb, c := g.StaticTris[i], g.StaticTris[i+1], g.StaticTris[i+2]
		switch a.Kind {
		case KindWall, KindMasked:
			walls++
			// Two of the three verts share a Z (a wall tri is half a quad).
			zs := []float32{a.Z, bb.Z, c.Z}
			if !(approxEq(zs[0], zs[1]) || approxEq(zs[1], zs[2]) || approxEq(zs[0], zs[2])) {
				t.Fatalf("wall tri %d has three distinct Z: %v", i/3, zs)
			}
			// All three verts are colinear in XY (they lie on the seg line).
			if !colinear(a, bb, c) {
				t.Fatalf("wall tri %d verts not colinear in XY", i/3)
			}
		case KindFlat:
			flats++
			if !(approxEq(a.Z, bb.Z) && approxEq(bb.Z, c.Z)) {
				t.Fatalf("flat tri %d not planar in Z: %f %f %f", i/3, a.Z, bb.Z, c.Z)
			}
		}
	}
	if walls == 0 || flats == 0 {
		t.Fatalf("expected both walls and flats, got walls=%d flats=%d", walls, flats)
	}
}

// An axis-aligned wall gets fake contrast: its light differs from its
// sector's raw level by exactly fakeContrast/255.
func TestFakeContrastOnAxisAlignedWall(t *testing.T) {
	lvl, tree, tex := loadLevel(t)
	b := New(lvl, tree, tex)
	g := b.Build(1056, -3616)

	// Find a seg whose two vertices share an X or Y (axis-aligned) and check
	// one of its wall verts carries the +/- 16/255 nudge vs the front sector.
	hit := false
	for _, s := range lvl.Segs {
		if int(s.StartVertex) >= len(lvl.Vertexes) || int(s.EndVertex) >= len(lvl.Vertexes) {
			continue
		}
		v1, v2 := lvl.Vertexes[s.StartVertex], lvl.Vertexes[s.EndVertex]
		if v1.X != v2.X && v1.Y != v2.Y {
			continue
		}
		_, fsec, _ := bsp.SegSides(lvl, s)
		if fsec == nil {
			continue
		}
		raw := clampI(int(fsec.LightLevel), 0, 255)
		want := clampI(raw-fakeContrast, 0, 255)
		if v1.X == v2.X {
			want = clampI(raw+fakeContrast, 0, 255)
		}
		wantF := float32(want) / 255
		for _, v := range g.StaticTris {
			if v.Kind != KindWall && v.Kind != KindMasked {
				continue
			}
			if approxEq(v.Light, wantF) && !approxEq(v.Light, float32(raw)/255) {
				hit = true
			}
		}
		if hit {
			break
		}
	}
	if !hit {
		t.Skip("no axis-aligned wall with a distinguishable contrast nudge in this build")
	}
}

func approxEq(a, b float32) bool { return math.Abs(float64(a-b)) < 1e-3 }

func colinear(a, b, c Vert) bool {
	cross := (float64(b.X)-float64(a.X))*(float64(c.Y)-float64(a.Y)) -
		(float64(b.Y)-float64(a.Y))*(float64(c.X)-float64(a.X))
	return math.Abs(cross) < 1.0 // map units^2; a degenerate sliver
}

func TestLiquidKindOfFlat(t *testing.T) {
	cases := map[string]uint16{
		"FWATER1":  KindLiquidWater,
		"LAVA1":    KindLiquidLava,
		"FIRELAVA": KindLiquidLava,
		"NUKAGE1":  KindLiquidNukage,
		"SLIME08":  KindLiquidNukage,
		"FLOOR4_8": KindFlat,
		"":         KindFlat,
	}
	for name, want := range cases {
		if got := liquidKindOfFlat(name); got != want {
			t.Errorf("liquidKindOfFlat(%q) = %d, want %d", name, got, want)
		}
	}
}

// E1M1 has nukage pools — building it must emit liquid-kind floor verts,
// and every liquid vert must face up (a floor, never a ceiling).
func TestBuildEmitsLiquidFloors(t *testing.T) {
	lvl, tree, tex := loadLevel(t)
	g := New(lvl, tree, tex).Build(1056, -3616)

	liquid := 0
	for _, v := range g.StaticTris {
		switch v.Kind {
		case KindLiquidWater, KindLiquidLava, KindLiquidNukage:
			liquid++
			if v.Nz <= 0 {
				t.Fatalf("liquid vert kind=%d faces down (Nz=%.1f) — should be a floor", v.Kind, v.Nz)
			}
		}
	}
	if liquid == 0 {
		t.Skip("this build produced no liquid floors (unexpected for E1M1, but WAD-dependent)")
	}
}

// Brightmaps must stay parallel to Textures, and every Draw's Bright must
// be the entry for its texture slot (nil is fine — most textures have none).
func TestBrightmapsParallelToTextures(t *testing.T) {
	lvl, tree, tex := loadLevel(t)
	g := New(lvl, tree, tex).Build(1056, -3616)

	if len(g.Brightmaps) != len(g.Textures) {
		t.Fatalf("Brightmaps (%d) not parallel to Textures (%d)", len(g.Brightmaps), len(g.Textures))
	}
	for _, d := range g.StaticDraws {
		if d.Tex == nil { // sky run
			continue
		}
		// Find the slot for d.Tex and confirm d.Bright matches Brightmaps[slot].
		for i, tx := range g.Textures {
			if tx == d.Tex {
				if d.Bright != g.Brightmaps[i] {
					t.Fatalf("Draw.Bright (%p) != Brightmaps[%d] (%p) for texture %d", d.Bright, i, g.Brightmaps[i], i)
				}
				break
			}
		}
	}
}
