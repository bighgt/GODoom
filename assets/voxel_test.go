package assets

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseVoxeldef(t *testing.T) {
	src := `
//===========================================================================
// WEAPONS
//===========================================================================

csawa = "csawa" {}
clipa = "clipa" { AngleOffset = 270 }
media = "media" { AngleOffset = 270  Scale = 1.5 }
misla = "misla" //{ UseActorPitch }

/*
// disabled projectile — must NOT be picked up
bal1a = "bal1a" //{ UseActorPitch }
*/

// a comment-only line
possa = "possa" {}
`
	got := parseVoxeldef(src)

	if len(got) != 5 {
		t.Fatalf("parsed %d entries, want 5: %+v", len(got), got)
	}
	if e, ok := got["csawa"]; !ok || e.file != "csawa" || e.angleOffset != 0 || e.scale != 1 {
		t.Errorf("csawa = %+v (ok=%v)", e, ok)
	}
	if e := got["clipa"]; e.angleOffset != 270 {
		t.Errorf("clipa AngleOffset = %v, want 270", e.angleOffset)
	}
	if e := got["media"]; e.angleOffset != 270 || e.scale != 1.5 {
		t.Errorf("media = %+v, want angleOffset 270 scale 1.5", e)
	}
	if e, ok := got["misla"]; !ok || e.file != "misla" || e.angleOffset != 0 {
		t.Errorf("misla (property block behind //) = %+v ok=%v", e, ok)
	}
	if _, ok := got["bal1a"]; ok {
		t.Error("bal1a inside /* */ was not stripped")
	}
}

// voxelPackPath returns the local Doom1 voxel pack, or "" if it isn't
// present (it's gitignored — supplied locally, like the WADs).
func voxelPackPath(t *testing.T) string {
	t.Helper()
	p := filepath.Join("voxel", "Doom1_Voxel.pk3")
	if _, err := os.Stat(p); err != nil {
		t.Skipf("no %s", p)
	}
	return p
}

func TestLoadVoxelPackDoom1(t *testing.T) {
	vs, err := LoadVoxelPack(voxelPackPath(t))
	if err != nil {
		t.Fatalf("LoadVoxelPack: %v", err)
	}
	if vs.Frames() < 100 {
		t.Errorf("only %d sprite frames mapped, expected the full Doom 1 set", vs.Frames())
	}

	// The imp's first walk frame must resolve to a real, non-trivial model.
	m, angOff, scale, ok := vs.Model("TROO", 0) // frame A
	if !ok {
		t.Fatal("no voxel model for TROO frame A")
	}
	if m.XSiz < 4 || m.YSiz < 4 || m.ZSiz < 20 {
		t.Errorf("imp model looks too small: %dx%dx%d", m.XSiz, m.YSiz, m.ZSiz)
	}
	if n := m.VoxelCount(); n < 500 {
		t.Errorf("imp model has only %d voxels", n)
	}
	if scale != 1 {
		t.Errorf("imp scale = %v, want 1", scale)
	}
	_ = angOff

	// A bullet clip carries the pack's AngleOffset = 270.
	if _, ao, _, ok := vs.Model("CLIP", 0); !ok || ao != 270 {
		t.Errorf("CLIP frame A: ok=%v angleOffset=%v (want 270)", ok, ao)
	}

	// An unmapped frame falls through cleanly (the imp's voxel frames stop
	// at 'u'/20 in this pack; 'w'/22 has nothing).
	if _, _, _, ok := vs.Model("TROO", 22); ok {
		t.Error("TROO frame 22 unexpectedly has a voxel model")
	}
	if _, _, _, ok := vs.Model("XXXX", 0); ok {
		t.Error("bogus sprite name resolved to a voxel model")
	}

	// Every mapped model decodes to something with voxels and a sane palette.
	checked := 0
	for _, prefix := range []string{"POSS", "SPOS", "SARG", "HEAD", "BOSS", "ARM1", "MEDI", "SHEL"} {
		for f := 0; f < 8; f++ {
			mm, _, _, ok := vs.Model(prefix, f)
			if !ok {
				continue
			}
			checked++
			if mm.VoxelCount() == 0 {
				t.Errorf("%s frame %d decoded to an empty model", prefix, f)
			}
		}
	}
	if checked < 10 {
		t.Errorf("only sampled %d models — pack looks incomplete", checked)
	}
}

func TestKVXPaletteAndBounds(t *testing.T) {
	vs, err := LoadVoxelPack(voxelPackPath(t))
	if err != nil {
		t.Fatalf("LoadVoxelPack: %v", err)
	}
	m, _, _, ok := vs.Model("TROO", 0)
	if !ok {
		t.Skip("no imp model")
	}
	// Slab ZTop+len must stay within ZSiz, and colours must be scaled up out
	// of the VGA 0..63 range (some component somewhere should exceed 63).
	maxComp := byte(0)
	for x := 0; x < m.XSiz; x++ {
		for y := 0; y < m.YSiz; y++ {
			for _, s := range m.Slabs(x, y) {
				if s.ZTop < 0 || s.ZTop+len(s.Colors) > m.ZSiz {
					t.Fatalf("slab out of z range: ztop=%d len=%d zsiz=%d", s.ZTop, len(s.Colors), m.ZSiz)
				}
				for _, c := range s.Colors {
					for _, comp := range c {
						if comp > maxComp {
							maxComp = comp
						}
					}
				}
			}
		}
	}
	if maxComp <= 63 {
		t.Errorf("palette never exceeds 63 (max %d) — VGA 6-bit was not scaled to 8-bit", maxComp)
	}
}
