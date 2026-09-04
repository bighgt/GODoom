package engine

import (
	"reflect"
	"testing"

	"twopointfive/assets"
	"twopointfive/engine/animdefs"
	"twopointfive/raster"
	"twopointfive/wad"
)

// animDefsFromANIMDEFS: explicit pic list -> per-frame durations, oscillate
// ping-pongs both the names and the durations.
func TestAnimDefsConversionOscillate(t *testing.T) {
	g := &Game{}
	g.animDefsProbed = true
	g.animDefsCache = &animdefs.AnimDefs{Defs: []animdefs.Def{{
		Name: "GLASS1",
		Frames: []animdefs.Frame{
			{Name: "GLASS1", Tics: 3},
			{Name: "GLASS2", Tics: 5},
			{Name: "GLASS3", Tics: 7},
		},
		Oscillate: true,
	}}}

	defs := g.animDefsFromANIMDEFS()
	if len(defs) != 1 {
		t.Fatalf("defs = %d, want 1", len(defs))
	}
	d := defs[0]
	if !reflect.DeepEqual(d.explicit, []string{"GLASS1", "GLASS2", "GLASS3", "GLASS2"}) {
		t.Errorf("explicit = %v", d.explicit)
	}
	if !reflect.DeepEqual(d.durs, []int{3, 5, 7, 5}) {
		t.Errorf("durs = %v", d.durs)
	}
}

// A numeric `pic N` resolves as a 1-based offset from the base name's own
// trailing number.
func TestAnimDefsNumericPic(t *testing.T) {
	g := &Game{}
	g.animDefsProbed = true
	g.animDefsCache = &animdefs.AnimDefs{Defs: []animdefs.Def{{
		Name: "SLIME05",
		Frames: []animdefs.Frame{
			{Name: "1", Tics: 8}, {Name: "2", Tics: 8}, {Name: "3", Tics: 8},
		},
	}}}
	d := g.animDefsFromANIMDEFS()[0]
	if !reflect.DeepEqual(d.explicit, []string{"SLIME05", "SLIME06", "SLIME07"}) {
		t.Errorf("numeric pic offsets = %v, want SLIME05..07", d.explicit)
	}
}

// End to end against a real IWAD: an ANIMDEFS `flat` with per-frame tics
// drives frameFor through the running-sum path.
func TestFrameForPerFrameDurations(t *testing.T) {
	g := loadRealLevel(t, "../wad/Doom2.wad", "MAP01")
	g.animDefsProbed = true
	g.animDefsCache = &animdefs.AnimDefs{Defs: []animdefs.Def{{
		Name: "LAVA1",
		Frames: []animdefs.Frame{
			{Name: "LAVA1", Tics: 3},
			{Name: "LAVA2", Tics: 5},
			{Name: "LAVA3", Tics: 7},
		},
	}}}
	g.initTexAnims()

	// cycle = 3+5+7 = 15; prefix = [0,3,8,15].
	cases := []struct {
		t    int
		want string
	}{
		{0, "LAVA1"}, {2, "LAVA1"}, {3, "LAVA2"}, {7, "LAVA2"},
		{8, "LAVA3"}, {14, "LAVA3"}, {15, "LAVA1"}, {18, "LAVA2"},
	}
	for _, c := range cases {
		if got := g.frameFor("LAVA1", c.t); got != c.want {
			t.Errorf("frameFor(LAVA1, %d) = %q, want %q", c.t, got, c.want)
		}
	}
}

// warpImage keeps the dimensions, is a pure rearrangement of the source
// pixels (nearest + wrap), and phase 0 differs from a later phase.
func TestWarpImage(t *testing.T) {
	src := &assets.RGBA{Width: 16, Height: 16, TexelsPerUnit: 1, Pix: make([]byte, 16*16*4)}
	for i := range src.Pix {
		src.Pix[i] = byte(i * 7)
	}
	a := warpImage(src, 0, false)
	b := warpImage(src, 8, false)
	if a.Width != 16 || a.Height != 16 || len(a.Pix) != len(src.Pix) {
		t.Fatalf("warp dims wrong: %dx%d", a.Width, a.Height)
	}

	// Nearest + wrap: every output texel must be some input texel (a
	// resample, never an invented colour), though not a bijection.
	srcSet := map[[4]byte]bool{}
	for i := 0; i+4 <= len(src.Pix); i += 4 {
		srcSet[[4]byte{src.Pix[i], src.Pix[i+1], src.Pix[i+2], src.Pix[i+3]}] = true
	}
	for i := 0; i+4 <= len(a.Pix); i += 4 {
		if !srcSet[[4]byte{a.Pix[i], a.Pix[i+1], a.Pix[i+2], a.Pix[i+3]}] {
			t.Fatalf("warped texel at %d is not from the source", i/4)
		}
	}
	if reflect.DeepEqual(a.Pix, b.Pix) {
		t.Error("phase 0 and phase 8 produced identical images")
	}
	// A mid-texture row/col does get displaced at phase 0.
	if reflect.DeepEqual(a.Pix, src.Pix) {
		t.Error("phase 0 left the texture completely undistorted")
	}
}

// initWarpAnims bakes a phase cycle for a `warp flat` and registers it
// keyed by the base flat name; the phase frames resolve through the shared
// texture cache.
func TestInitWarpAnims(t *testing.T) {
	w, err := wad.Load("../wad/Doom2.wad")
	if err != nil {
		t.Skipf("Doom2.wad unavailable: %v", err)
	}
	tx, err := assets.New(w)
	if err != nil {
		t.Fatalf("assets.New: %v", err)
	}
	g := loadRealLevel(t, "../wad/Doom2.wad", "MAP01")
	g.Raster = raster.New(tx, 320, 200)
	g.animDefsProbed = true
	g.animDefsCache = &animdefs.AnimDefs{Warps: []animdefs.Warp{{Name: "FWATER1"}}}

	g.initTexAnims()

	ref, ok := g.flatAnimByName["FWATER1"]
	if !ok {
		t.Fatal("FWATER1 not registered after initWarpAnims")
	}
	if len(ref.grp.frames) != warpPhases {
		t.Errorf("warp group has %d frames, want %d", len(ref.grp.frames), warpPhases)
	}
	f0 := g.frameFor("FWATER1", 0)
	if _, ok := tx.Flat(f0); !ok || f0 == "FWATER1" {
		t.Errorf("phase-0 frame %q did not resolve to an injected image", f0)
	}
	// Two phases far apart should name different injected lumps.
	if g.frameFor("FWATER1", 0) == g.frameFor("FWATER1", warpPhases/2*ref.grp.speed) {
		t.Error("warp did not advance frames over time")
	}
}
