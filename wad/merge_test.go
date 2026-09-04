package wad

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// buildWAD assembles a minimal in-memory WAD (magic + lumps + directory)
// from an ordered list of name/content pairs — enough to exercise Merge.
func buildWAD(t *testing.T, magic string, lumps [][2]string) []byte {
	t.Helper()
	var body bytes.Buffer
	type ent struct {
		pos, size int32
		name      string
	}
	var dir []ent
	body.WriteString("\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00") // 12-byte header placeholder
	for _, l := range lumps {
		pos := int32(body.Len())
		body.WriteString(l[1])
		dir = append(dir, ent{pos: pos, size: int32(len(l[1])), name: l[0]})
	}
	dirOfs := int32(body.Len())
	for _, e := range dir {
		var rec [16]byte
		binary.LittleEndian.PutUint32(rec[0:4], uint32(e.pos))
		binary.LittleEndian.PutUint32(rec[4:8], uint32(e.size))
		copy(rec[8:16], e.name)
		body.Write(rec[:])
	}
	b := body.Bytes()
	copy(b[0:4], magic)
	binary.LittleEndian.PutUint32(b[4:8], uint32(len(dir)))
	binary.LittleEndian.PutUint32(b[8:12], uint32(dirOfs))
	return b
}

func TestMergeOverrideAndOffsets(t *testing.T) {
	base, err := Parse(buildWAD(t, "IWAD", [][2]string{
		{"PLAYPAL", "iwad-palette-bytes"},
		{"THINGA", "iwad-thing-a"},
		{"SHARED", "iwad-shared"},
	}))
	if err != nil {
		t.Fatalf("parse base: %v", err)
	}
	patch, err := Parse(buildWAD(t, "PWAD", [][2]string{
		{"SHARED", "pwad-shared-OVERRIDE"},
		{"THINGB", "pwad-thing-b"},
	}))
	if err != nil {
		t.Fatalf("parse patch: %v", err)
	}

	m := Merge(base, patch)
	if int(m.Header.NumLumps) != len(m.Entries) || len(m.Entries) != 5 {
		t.Fatalf("merged lump count = %d (header %d), want 5", len(m.Entries), m.Header.NumLumps)
	}

	// Base-only lump still resolves, with intact bytes (offsets rebased).
	if got, _ := m.Find("PLAYPAL"); string(got) != "iwad-palette-bytes" {
		t.Errorf("PLAYPAL = %q", got)
	}
	if got, _ := m.Find("THINGA"); string(got) != "iwad-thing-a" {
		t.Errorf("THINGA = %q", got)
	}
	// Patch-only lump resolves too.
	if got, _ := m.Find("THINGB"); string(got) != "pwad-thing-b" {
		t.Errorf("THINGB = %q", got)
	}
	// A shared name: the PATCH wins (last loaded).
	if got, _ := m.Find("SHARED"); string(got) != "pwad-shared-OVERRIDE" {
		t.Errorf("SHARED = %q, want the PWAD override", got)
	}
	// Inputs untouched.
	if got, _ := base.Find("SHARED"); string(got) != "iwad-shared" {
		t.Errorf("Merge mutated the base WAD: SHARED = %q", got)
	}

	// Single / zero arg behaviour.
	if Merge(base) != base {
		t.Error("Merge(one) should return it unchanged")
	}
	if Merge() != nil {
		t.Error("Merge() should be nil")
	}
}

func TestListMapsDedupsAfterMerge(t *testing.T) {
	base, _ := Parse(buildWAD(t, "IWAD", [][2]string{
		{"MAP01", ""}, {"THINGS", ""}, {"MAP02", ""}, {"THINGS", ""},
	}))
	patch, _ := Parse(buildWAD(t, "PWAD", [][2]string{
		{"MAP01", ""}, {"THINGS", ""}, // replaces MAP01
	}))
	got := Merge(base, patch).ListMaps()
	if len(got) != 2 || got[0] != "MAP01" || got[1] != "MAP02" {
		t.Errorf("ListMaps after merge = %v, want [MAP01 MAP02]", got)
	}
}

// TestMergeRealPWAD merges a real PWAD with its IWAD if both happen to be
// present locally (they're gitignored). Mirrors what cmd/engine does for a
// picked PWAD.
func TestMergeRealPWAD(t *testing.T) {
	dir := "Doom + Doom II Collection WAD files"
	pwadPath := filepath.Join(dir, "iddm1.wad")
	iwadPath := filepath.Join(dir, "doom2.wad")
	if _, err := os.Stat(pwadPath); err != nil {
		t.Skipf("no %s", pwadPath)
	}
	iw, err := Load(iwadPath)
	if err != nil {
		t.Fatalf("load iwad: %v", err)
	}
	pw, err := Load(pwadPath)
	if err != nil {
		t.Fatalf("load pwad: %v", err)
	}
	if pw.IndexOf("PLAYPAL") >= 0 {
		t.Skip("this PWAD carries its own PLAYPAL — not the case we care about")
	}
	m := Merge(iw, pw)
	if pp, ok := m.Find("PLAYPAL"); !ok || len(pp) == 0 || len(pp)%768 != 0 {
		t.Fatalf("merged PLAYPAL bad (ok=%v len=%d)", ok, len(pp))
	}
	if m.IndexOf("PNAMES") < 0 || m.IndexOf("TEXTURE1") < 0 {
		t.Error("merged WAD missing PNAMES/TEXTURE1 from the IWAD")
	}
	lvl, err := m.LoadLevel("MAP01")
	if err != nil || len(lvl.Vertexes) == 0 || len(lvl.Nodes) == 0 {
		t.Fatalf("MAP01 from merged WAD: err=%v verts=%d nodes=%d", err, len(lvl.Vertexes), len(lvl.Nodes))
	}
	t.Logf("merged %d + %d lumps -> %d, MAP01 %d verts", iw.Header.NumLumps, pw.Header.NumLumps, m.Header.NumLumps, len(lvl.Vertexes))
}
