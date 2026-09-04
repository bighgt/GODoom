package animdefs

import "testing"

func TestParseExplicitAndRange(t *testing.T) {
	src := []byte(`
// a lava flat with explicit frames
flat LAVA1
    pic LAVA1 tics 8
    pic LAVA2 tics 8
    pic LAVA3 rand 4 12
    oscillate

texture FIREBLU1 range FIREBLU2 tics 6

warp flat FWATER1
warp2 texture BLODGR1 2.5
`)
	a := Parse(src)

	if len(a.Defs) != 2 {
		t.Fatalf("defs = %d, want 2 (%+v)", len(a.Defs), a.Defs)
	}
	d := a.Defs[0]
	if d.Texture || d.Name != "LAVA1" || len(d.Frames) != 3 || !d.Oscillate {
		t.Fatalf("lava def wrong: %+v", d)
	}
	if d.Frames[0].Tics != 8 || d.Frames[2].Tics != 0 || d.Frames[2].RandLo != 4 || d.Frames[2].RandHi != 12 {
		t.Errorf("lava frames wrong: %+v", d.Frames)
	}
	r := a.Defs[1]
	if !r.Texture || r.Name != "FIREBLU1" || r.RangeLast != "FIREBLU2" || r.RangeTics != 6 {
		t.Errorf("range def wrong: %+v", r)
	}

	if len(a.Warps) != 2 {
		t.Fatalf("warps = %d, want 2", len(a.Warps))
	}
	if a.Warps[0].Texture || a.Warps[0].Name != "FWATER1" || a.Warps[0].Wide {
		t.Errorf("warp 0 wrong: %+v", a.Warps[0])
	}
	if !a.Warps[1].Texture || !a.Warps[1].Wide || a.Warps[1].Speed != 2.5 {
		t.Errorf("warp 1 wrong: %+v", a.Warps[1])
	}
}

func TestParseSkipsUnsupportedAndComments(t *testing.T) {
	src := []byte(`
switch METAL1 on pic METAL2 tics 8

/* block
   comment */
cameratexture FOO 128 128

flat NUKAGE1
    pic 1 tics 8
    pic 2 tics 8
`)
	a := Parse(src)
	if len(a.Defs) != 1 || a.Defs[0].Name != "NUKAGE1" || len(a.Defs[0].Frames) != 2 {
		t.Fatalf("expected only the NUKAGE1 flat, got %+v", a.Defs)
	}
	if a.Defs[0].Frames[0].Name != "1" {
		t.Errorf("numeric pic arg not preserved: %+v", a.Defs[0].Frames[0])
	}
	if len(a.Warnings) < 2 {
		t.Errorf("want warnings for switch + cameratexture, got %v", a.Warnings)
	}
}

func TestMergeLastWins(t *testing.T) {
	a := Parse([]byte("flat LAVA1\n pic LAVA1 tics 8\n pic LAVA2 tics 8\n"))
	b := Parse([]byte("flat LAVA1\n pic LAVA1 tics 4\n pic LAVA2 tics 4\n pic LAVA3 tics 4\n"))
	m := Merge(a, b)
	if len(m.Defs) != 1 {
		t.Fatalf("merge defs = %d, want 1", len(m.Defs))
	}
	if len(m.Defs[0].Frames) != 3 || m.Defs[0].Frames[0].Tics != 4 {
		t.Errorf("merge did not take the later LAVA1 def: %+v", m.Defs[0])
	}
}
