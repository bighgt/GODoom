package engine

import (
	"encoding/binary"
	"reflect"
	"testing"
)

// makeAnimatedLump builds a BOOM ANIMATED lump from a set of rows.
func makeAnimatedLump(rows []struct {
	isTex       bool
	last, first string
	speed       uint32
}) []byte {
	var b []byte
	for _, r := range rows {
		rec := make([]byte, 23)
		if r.isTex {
			rec[0] = 1
		}
		copy(rec[1:10], r.last)
		copy(rec[10:19], r.first)
		binary.LittleEndian.PutUint32(rec[19:23], r.speed)
		b = append(b, rec...)
	}
	return append(b, 0xFF)
}

func TestParseAnimatedLump(t *testing.T) {
	lump := makeAnimatedLump([]struct {
		isTex       bool
		last, first string
		speed       uint32
	}{
		{false, "BLOOD3", "BLOOD1", 8},
		{true, "FIREBLU2", "FIREBLU1", 8},
		{true, "SWIRLY9", "SWIRLY1", 700000}, // GZDoom warp sentinel
	})
	got := parseAnimatedLump(lump)
	if len(got) != 3 {
		t.Fatalf("parsed %d defs, want 3", len(got))
	}
	if got[0].isTexture || got[0].first != "BLOOD1" || got[0].last != "BLOOD3" || got[0].speed != 8 {
		t.Errorf("row 0 = %+v", got[0])
	}
	if !got[1].isTexture || got[1].first != "FIREBLU1" || got[1].last != "FIREBLU2" {
		t.Errorf("row 1 = %+v", got[1])
	}
	if got[2].speed != 8 { // huge speed falls back to the normal rate, not frozen
		t.Errorf("warp-sentinel speed = %d, want 8", got[2].speed)
	}

	// A lump that is only the terminator yields nothing.
	if d := parseAnimatedLump([]byte{0xFF}); len(d) != 0 {
		t.Errorf("terminator-only lump parsed %d defs", len(d))
	}
	// A truncated trailing record is ignored, not read out of bounds.
	if d := parseAnimatedLump([]byte{0x01, 'A', 'B', 'C'}); len(d) != 0 {
		t.Errorf("truncated record parsed %d defs", len(d))
	}
}

func TestNameRange(t *testing.T) {
	order := []string{"FIREWALA", "FIREWALB", "FIREWALL", "OTHER"}
	if got := nameRange(order, "FIREWALA", "FIREWALL"); !reflect.DeepEqual(got, []string{"FIREWALA", "FIREWALB", "FIREWALL"}) {
		t.Errorf("forward = %v", got)
	}
	if got := nameRange(order, "FIREWALL", "FIREWALA"); !reflect.DeepEqual(got, []string{"FIREWALA", "FIREWALB", "FIREWALL"}) {
		t.Errorf("reversed endpoints = %v", got)
	}
	if got := nameRange(order, "FIREWALA", "NOPE"); got != nil {
		t.Errorf("missing endpoint = %v, want nil", got)
	}
}

// The built-in table's animated wall textures resolve against a real IWAD,
// including the two non-numbered chains (FIREWALL, FIRELAVA).
func TestBuiltinWallAnimsResolve(t *testing.T) {
	g := loadRealLevel(t, "../wad/Doom2.wad", "MAP01")

	for _, name := range []string{"FIREBLU1", "BFALL1", "SFALL1", "GSTFONT1", "FIREWALA", "FIREWALL", "FIRELAV3", "FIRELAVA"} {
		if _, ok := g.wallAnimByName[name]; !ok {
			t.Errorf("wall texture %q not registered as an animation frame", name)
		}
	}
	// FIREBLU is a plain numbered pair.
	if a, b := g.frameFor("FIREBLU1", 0), g.frameFor("FIREBLU1", 8); a != "FIREBLU1" || b != "FIREBLU2" {
		t.Errorf("FIREBLU cycle: t0=%q t8=%q, want FIREBLU1/FIREBLU2", a, b)
	}
	// FIREWALL is the A/B/L chain (no trailing digit).
	got := []string{g.frameFor("FIREWALA", 0), g.frameFor("FIREWALA", 8), g.frameFor("FIREWALA", 16), g.frameFor("FIREWALA", 24)}
	want := []string{"FIREWALA", "FIREWALB", "FIREWALL", "FIREWALA"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FIREWALL chain = %v, want %v", got, want)
	}
}

// A non-empty ANIMATED lump replaces the built-in table entirely.
func TestChooseAnimDefs(t *testing.T) {
	if got := chooseAnimDefs(nil); &got[0] != &builtinAnimDefs[0] {
		t.Error("nil lump should yield the built-in table")
	}
	lump := makeAnimatedLump([]struct {
		isTex       bool
		last, first string
		speed       uint32
	}{
		{false, "NUKAGE3", "NUKAGE1", 4},
	})
	got := chooseAnimDefs(lump)
	if len(got) != 1 || got[0].first != "NUKAGE1" || got[0].speed != 4 {
		t.Fatalf("lump defs = %+v, want one NUKAGE row at speed 4", got)
	}
	// Garbage that parses to nothing falls back to the built-ins rather
	// than leaving the game with no animations.
	if got := chooseAnimDefs([]byte{0xFF}); &got[0] != &builtinAnimDefs[0] {
		t.Error("empty parse should fall back to the built-in table")
	}
}

// A sidedef whose authored texture is animated gets its live texture name
// rewritten each tic; other sidedefs are untouched.
func TestWallAnimRewritesSidedef(t *testing.T) {
	g := loadRealLevel(t, "../wad/Doom2.wad", "MAP01")

	// Force a known sidedef onto an animated texture and re-snapshot.
	if len(g.Level.Sidedefs) == 0 {
		t.Skip("no sidedefs")
	}
	g.Level.Sidedefs[0].MiddleTexture = "FIREBLU1"
	g.initTexAnims()

	found := false
	for _, s := range g.wallAnimSlots {
		if s.side == 0 && s.slot == 1 {
			found = true
		}
	}
	if !found {
		t.Fatal("sidedef 0 middle slot not picked up as animated")
	}

	other := g.Level.Sidedefs[len(g.Level.Sidedefs)-1].MiddleTexture

	g.levelTime = 8
	g.animateWalls()
	if got := g.Level.Sidedefs[0].MiddleTexture; got != "FIREBLU2" {
		t.Errorf("t=8 middle = %q, want FIREBLU2", got)
	}
	g.levelTime = 16
	g.animateWalls()
	if got := g.Level.Sidedefs[0].MiddleTexture; got != "FIREBLU1" {
		t.Errorf("t=16 middle = %q, want wrap to FIREBLU1", got)
	}
	if g.Level.Sidedefs[len(g.Level.Sidedefs)-1].MiddleTexture != other {
		t.Error("a non-animated sidedef's texture changed")
	}
}

func TestExpandFrameRange(t *testing.T) {
	cases := []struct {
		a, b string
		want []string
	}{
		{"NUKAGE1", "NUKAGE3", []string{"NUKAGE1", "NUKAGE2", "NUKAGE3"}},
		{"FWATER1", "FWATER4", []string{"FWATER1", "FWATER2", "FWATER3", "FWATER4"}},
		{"SLIME09", "SLIME12", []string{"SLIME09", "SLIME10", "SLIME11", "SLIME12"}},
		{"RROCK05", "RROCK08", []string{"RROCK05", "RROCK06", "RROCK07", "RROCK08"}},
		{"NUKAGE3", "NUKAGE1", nil}, // backwards
		{"FOO", "BAR", nil},         // no digits
		{"A1", "B3", nil},           // different prefix
	}
	for _, c := range cases {
		if got := expandFrameRange(c.a, c.b); !reflect.DeepEqual(got, c.want) {
			t.Errorf("expandFrameRange(%q,%q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestFlatAnimCyclesInGame(t *testing.T) {
	g := loadRealLevel(t, "../wad/Doom2.wad", "MAP01")
	if len(g.flatAnimByName) == 0 {
		t.Fatal("no flat animation groups resolved from Doom2 MAP01")
	}
	if _, ok := g.flatAnimByName["NUKAGE1"]; !ok {
		t.Error("NUKAGE group not registered (MAP01 uses it)")
	}

	// Find a sector whose authored floor flat is animated.
	si := -1
	for i, base := range g.animBaseFloor {
		if base != "" {
			si = i
			break
		}
	}
	if si < 0 {
		t.Skip("MAP01 build has no animated-floor sector (unexpected but not fatal)")
	}
	base := g.animBaseFloor[si]
	grp := g.flatAnimByName[base].grp
	t.Logf("sector %d authored floor %q, group %v (speed %d)", si, base, grp.frames, grp.speed)

	// Over one full cycle the sector's live flat must visit every frame in
	// order and return to the start.
	seen := map[string]int{}
	for k := 0; k < len(grp.frames); k++ {
		g.levelTime = k * grp.speed
		g.animateFlats()
		seen[g.Level.Sectors[si].FloorTexture]++
	}
	if len(seen) != len(grp.frames) {
		t.Errorf("over a cycle the flat showed %d distinct frames, want %d (%v)", len(seen), len(grp.frames), seen)
	}
	g.levelTime = len(grp.frames) * grp.speed
	g.animateFlats()
	// Back to the authored frame's own phase after a whole cycle.
	if got := g.Level.Sectors[si].FloorTexture; got != g.frameFor(base, 0) {
		t.Errorf("after one full cycle flat = %q, want %q", got, g.frameFor(base, 0))
	}

	// A non-animated sector is never touched.
	for i, b := range g.animBaseFloor {
		if b == "" {
			before := g.Level.Sectors[i].FloorTexture
			g.levelTime = 12345
			g.animateFlats()
			if g.Level.Sectors[i].FloorTexture != before {
				t.Errorf("non-animated sector %d floor changed %q -> %q", i, before, g.Level.Sectors[i].FloorTexture)
			}
			break
		}
	}
}

func TestFlatAnimPhaseOffsetByStartFrame(t *testing.T) {
	// Two sectors on NUKAGE1 vs NUKAGE2 sit one frame apart, always.
	g := loadRealLevel(t, "../wad/Doom2.wad", "MAP01")
	if _, ok := g.flatAnimByName["NUKAGE2"]; !ok {
		t.Skip("NUKAGE not present")
	}
	for tic := 0; tic < 40; tic++ {
		a := g.frameFor("NUKAGE1", tic)
		b := g.frameFor("NUKAGE2", tic)
		if a == b {
			t.Fatalf("tic %d: NUKAGE1 and NUKAGE2 sectors show the same frame %q (should stay 1 apart)", tic, a)
		}
	}
}
