package wad

import (
	"crypto/sha1"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// writeWAD writes buildWAD's bytes (see merge_test.go, same package) to name
// under dir and returns the full path.
func writeWAD(t *testing.T, dir, name, magic string, lumps [][2]string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, buildWAD(t, magic, lumps), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func TestLoadWithIWADSelfContained(t *testing.T) {
	dir := t.TempDir()
	path := writeWAD(t, dir, "game.wad", "IWAD", [][2]string{{"PLAYPAL", "palette"}})

	res, err := LoadWithIWAD(path, "")
	if err != nil {
		t.Fatalf("LoadWithIWAD: %v", err)
	}
	if res.IWADPath != "" {
		t.Errorf("IWADPath = %q, want \"\" (nothing to merge)", res.IWADPath)
	}
	if res.PickedMagic != "IWAD" || res.PickedLumps != 1 {
		t.Errorf("PickedMagic/PickedLumps = %q/%d, want IWAD/1", res.PickedMagic, res.PickedLumps)
	}
	if res.WAD.IndexOf("PLAYPAL") < 0 {
		t.Error("merged WAD missing PLAYPAL")
	}
}

func TestLoadWithIWADMergesPatch(t *testing.T) {
	dir := t.TempDir()
	// No map lumps -> resolveIWAD defaults to "doom2" format -> looks for
	// DOOM2.WAD (among others) next to the patch.
	writeWAD(t, dir, "DOOM2.WAD", "IWAD", [][2]string{{"PLAYPAL", "iwad-pal"}, {"SHARED", "iwad-shared"}})
	patchPath := writeWAD(t, dir, "mypatch.wad", "PWAD", [][2]string{{"SHARED", "patch-shared"}, {"MYSTUFF", "patch-only"}})

	res, err := LoadWithIWAD(patchPath, "")
	if err != nil {
		t.Fatalf("LoadWithIWAD: %v", err)
	}
	if res.IWADPath == "" {
		t.Fatal("IWADPath is empty, want the merged DOOM2.WAD path")
	}
	if filepath.Base(res.IWADPath) != "DOOM2.WAD" {
		t.Errorf("IWADPath = %q, want .../DOOM2.WAD", res.IWADPath)
	}
	if res.PickedMagic != "PWAD" || res.PickedLumps != 2 {
		t.Errorf("PickedMagic/PickedLumps = %q/%d, want PWAD/2", res.PickedMagic, res.PickedLumps)
	}
	// The patch's lump should win over the IWAD's for a shared name (Merge's
	// own override behavior — see TestMergeOverrideAndOffsets).
	shared, ok := res.WAD.Find("SHARED")
	if !ok {
		t.Fatal("merged WAD missing SHARED")
	}
	if got := string(shared); got != "patch-shared" {
		t.Errorf("SHARED = %q, want patch-shared (patch should win)", got)
	}
	if res.WAD.IndexOf("MYSTUFF") < 0 {
		t.Error("merged WAD missing patch-only lump MYSTUFF")
	}
}

func TestLoadWithIWADExplicitOverride(t *testing.T) {
	dir := t.TempDir()
	explicit := writeWAD(t, dir, "custom.wad", "IWAD", [][2]string{{"PLAYPAL", "pal"}})
	patchPath := writeWAD(t, dir, "mypatch.wad", "PWAD", [][2]string{{"ONLY", "x"}})

	res, err := LoadWithIWAD(patchPath, explicit)
	if err != nil {
		t.Fatalf("LoadWithIWAD: %v", err)
	}
	if res.IWADPath != explicit {
		t.Errorf("IWADPath = %q, want the explicit %q", res.IWADPath, explicit)
	}
}

// Deliberately no "IWAD not found anywhere" test: resolveIWAD searches the
// working directory (among other places) as a real convenience, and this
// repo's own wad/ package directory — go test's cwd for this package —
// holds real IWADs (Doom2.wad) for other tests, so "nowhere on this
// machine" isn't a hermetic condition to construct here.

// writeTPF3DPWAD writes a minimal real-3D PWAD into dir whose manifest
// records iwadPath/iwadSHA1 as its base IWAD.
func writeTPF3DPWAD(t *testing.T, dir, iwadPath, iwadSHA1 string) string {
	t.Helper()
	p := filepath.Join(dir, "conv_3d.wad")
	info := TPF3DInfo{Format: TPF3DFormat, Tool: "test", IWADPath: iwadPath, IWADSHA1: iwadSHA1}
	if err := WritePWAD(p, []NamedLump{{Name: TPF3DLump, Data: EncodeTPF3D(info)}}); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestLoadWithIWADUsesRecordedIWAD: a real-3D WAD is merged over exactly
// the IWAD its manifest records — not whatever the usual search finds
// first. (The bug this guards: a converted Doom II WAD saved in wad/ got
// merged over shareware DOOM1.WAD from testdata/, losing its music.)
func TestLoadWithIWADUsesRecordedIWAD(t *testing.T) {
	shareware, err := filepath.Abs("../testdata/DOOM1.WAD")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(shareware)
	if err != nil {
		t.Skip("DOOM1.WAD not available")
	}
	sum := sha1.Sum(data)
	sha := hex.EncodeToString(sum[:])

	// Recorded path still valid: used even though the WAD has no maps to
	// hint a format, and config's "iwad" names something else.
	pw := writeTPF3DPWAD(t, t.TempDir(), shareware, sha)
	res, err := LoadWithIWAD(pw, "/definitely/not/an/iwad.wad")
	if err != nil {
		t.Fatal(err)
	}
	if res.IWADPath != shareware {
		t.Errorf("merged over %q, want the recorded %q", res.IWADPath, shareware)
	}
	if _, ok := res.WAD.Find("E1M1"); !ok {
		t.Error("merged WAD lacks the recorded IWAD's maps")
	}

	// Recorded path gone (files moved): found by name next to the WAD.
	dir := t.TempDir()
	moved := filepath.Join(dir, "DOOM1.WAD")
	if err := os.WriteFile(moved, data, 0o644); err != nil {
		t.Fatal(err)
	}
	pw = writeTPF3DPWAD(t, dir, "/gone/elsewhere/DOOM1.WAD", sha)
	res, err = LoadWithIWAD(pw, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.IWADPath != moved {
		t.Errorf("merged over %q, want the same-named copy %q", res.IWADPath, moved)
	}
}
