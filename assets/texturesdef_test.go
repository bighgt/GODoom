package assets

import (
	"testing"

	"twopointfive/wad"
)

func loadDoom2Textures(t *testing.T) *Textures {
	t.Helper()
	w, err := wad.Load("../wad/Doom2.wad")
	if err != nil {
		t.Skipf("Doom2.wad unavailable: %v", err)
	}
	tx, err := New(w)
	if err != nil {
		t.Fatalf("assets.New: %v", err)
	}
	return tx
}

// A registered Flat ZDDef shadows the raw lump, carries XScale as
// TexelsPerUnit, and composites its source patch.
func TestZDFlatOverride(t *testing.T) {
	tx := loadDoom2Textures(t)
	base, ok := tx.Flat("FLOOR0_1")
	if !ok {
		t.Skip("FLOOR0_1 not in this IWAD")
	}
	w, h := base.Width, base.Height

	tx.AddTextureDefs(nil, []ZDDef{{
		Name: "ZDFLOOR", Width: w, Height: h, XScale: 2,
		Patches: []ZDPatch{{Name: "FLOOR0_1", X: 0, Y: 0, Alpha: 1}},
	}}, nil)

	got, ok := tx.Flat("ZDFLOOR")
	if !ok {
		t.Fatal("ZDFLOOR did not resolve")
	}
	if got.Width != w || got.Height != h {
		t.Errorf("size = %dx%d, want %dx%d", got.Width, got.Height, w, h)
	}
	if got.TexelsPerUnit != 2 {
		t.Errorf("TexelsPerUnit = %v, want 2 (from XScale)", got.TexelsPerUnit)
	}
	// Pixels copied straight through.
	for i := 0; i < len(got.Pix) && i < 64*4; i++ {
		if got.Pix[i] != base.Pix[i] {
			t.Fatalf("pixel byte %d = %d, want %d", i, got.Pix[i], base.Pix[i])
		}
	}
}

// FlipX mirrors the source horizontally during composition.
func TestZDFlipX(t *testing.T) {
	tx := loadDoom2Textures(t)
	base, ok := tx.Flat("FLOOR0_1")
	if !ok {
		t.Skip("FLOOR0_1 not in this IWAD")
	}
	w, h := base.Width, base.Height

	tx.AddTextureDefs(nil, []ZDDef{{
		Name: "ZDFLIP", Width: w, Height: h,
		Patches: []ZDPatch{{Name: "FLOOR0_1", Alpha: 1, FlipX: true}},
	}}, nil)

	got, _ := tx.Flat("ZDFLIP")
	mismatch := 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			a := got.At(x, y)
			b := base.At(w-1-x, y)
			if a != b {
				mismatch++
			}
		}
	}
	if mismatch != 0 {
		t.Errorf("%d/%d pixels differ from a horizontal mirror of the source", mismatch, w*h)
	}
}

// A NullTexture wall def resolves like "-" (no texture).
func TestZDNullTexture(t *testing.T) {
	tx := loadDoom2Textures(t)
	tx.AddTextureDefs([]ZDDef{{Name: "ZDNULL", NullTexture: true}}, nil, nil)
	if im, ok := tx.WallTexture("ZDNULL"); ok || im != nil {
		t.Errorf("NullTexture resolved to %v, ok=%v; want (nil,false)", im, ok)
	}
}

// Two stacked patches: the second (opaque copy) overwrites the first where
// they overlap.
func TestZDPatchStack(t *testing.T) {
	tx := loadDoom2Textures(t)
	a, ok1 := tx.Flat("FLOOR0_1")
	b, ok2 := tx.Flat("CEIL1_1")
	if !ok1 || !ok2 {
		t.Skip("need FLOOR0_1 + CEIL1_1")
	}
	_ = a
	tx.AddTextureDefs(nil, []ZDDef{{
		Name: "ZDSTACK", Width: 64, Height: 64,
		Patches: []ZDPatch{
			{Name: "FLOOR0_1", Alpha: 1},
			{Name: "CEIL1_1", X: 16, Y: 16, Alpha: 1},
		},
	}}, nil)
	got, _ := tx.Flat("ZDSTACK")
	// A pixel well inside the second patch equals CEIL1_1.
	if got.At(40, 40) != b.At(24, 24) {
		t.Errorf("overlap pixel = %v, want CEIL1_1's %v", got.At(40, 40), b.At(24, 24))
	}
	// A pixel outside the second patch still equals FLOOR0_1.
	if got.At(4, 4) != a.At(4, 4) {
		t.Errorf("non-overlap pixel = %v, want FLOOR0_1's %v", got.At(4, 4), a.At(4, 4))
	}
}
