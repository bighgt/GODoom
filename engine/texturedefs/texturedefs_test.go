package texturedefs

import "testing"

func TestParseTextureBlock(t *testing.T) {
	src := []byte(`
// a hi-res redefinition
WallTexture "BRICK1", 128, 128
{
    XScale 2.0
    YScale 2.0
    Patch "BRIK1HI", 0, 0
}

optional Sprite POSSA1, 64, 68
{
    Offset 32, 68
    Patch BODYA0, 0, 0 { FlipX Alpha 0.5 Style Translucent }
    Patch GLOW, 4, 4 { Blend "FF8000", 0.6 Rotate 180 }
}

Flat LAVA1 { Patch LAVACOMP, 0, 0 }
`)
	defs, warns := Parse(src)
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	if len(defs) != 3 {
		t.Fatalf("defs = %d, want 3", len(defs))
	}

	w := defs[0]
	if w.Kind != KindWallTexture || w.Name != "BRICK1" || w.Width != 128 || w.XScale != 2 || w.YScale != 2 {
		t.Errorf("walltexture def wrong: %+v", w)
	}
	if len(w.Patches) != 1 || w.Patches[0].Name != "BRIK1HI" {
		t.Errorf("walltexture patch wrong: %+v", w.Patches)
	}

	s := defs[1]
	if s.Kind != KindSprite || !s.Optional || s.OffsetX != 32 || s.OffsetY != 68 {
		t.Errorf("sprite def wrong: %+v", s)
	}
	if len(s.Patches) != 2 {
		t.Fatalf("sprite patches = %d, want 2", len(s.Patches))
	}
	if !s.Patches[0].FlipX || s.Patches[0].Alpha != 0.5 || s.Patches[0].Style != "translucent" {
		t.Errorf("sprite patch 0 wrong: %+v", s.Patches[0])
	}
	if !s.Patches[1].HasBlend || s.Patches[1].Rotate != 180 {
		t.Errorf("sprite patch 1 wrong: %+v", s.Patches[1])
	}
	if r, g, b := s.Patches[1].BlendRGBA[0], s.Patches[1].BlendRGBA[1], s.Patches[1].BlendRGBA[2]; r != 1 || g < 0.49 || g > 0.51 || b != 0 {
		t.Errorf("blend colour = %v, want ~(1, .5, 0)", s.Patches[1].BlendRGBA)
	}

	if defs[2].Kind != KindFlat || defs[2].Name != "LAVA1" {
		t.Errorf("flat def wrong: %+v", defs[2])
	}
}

func TestParseSkipsUnknownDirectives(t *testing.T) {
	src := []byte(`
AnimatedDoor DOOR1 { pic DOOR2 tics 4 }

Texture GOOD, 8, 8 { Patch P, 0, 0 }
`)
	defs, warns := Parse(src)
	if len(defs) != 1 || defs[0].Name != "GOOD" {
		t.Fatalf("expected just GOOD, got %+v", defs)
	}
	if len(warns) == 0 {
		t.Errorf("want a warning for AnimatedDoor")
	}
}

func TestParseBarePatchLine(t *testing.T) {
	// Some lumps write `Patch` as the only keyword and rely on defaults.
	defs, _ := Parse([]byte(`Texture T, 16, 16 { Patch A, 1, 2  Patch B, 3, 4 }`))
	if len(defs) != 1 || len(defs[0].Patches) != 2 {
		t.Fatalf("got %+v", defs)
	}
	if defs[0].Patches[1].X != 3 || defs[0].Patches[1].Y != 4 {
		t.Errorf("second patch coords = %+v", defs[0].Patches[1])
	}
}
