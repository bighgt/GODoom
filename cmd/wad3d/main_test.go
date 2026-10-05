package main

import (
	"path/filepath"
	"testing"
)

// TestResolveOut: -out's three forms — a folder gets <name>_3d.wad inside
// it, a bare name gets ".wad", a full file name is used as-is — and no
// -out falls back to defaultOutput.
func TestResolveOut(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join("somewhere", "doom2.wad")
	cases := []struct{ out, want string }{
		{dir, filepath.Join(dir, "doom2_3d.wad")},
		{filepath.Join(dir, "mine"), filepath.Join(dir, "mine.wad")},
		{filepath.Join(dir, "mine.wad"), filepath.Join(dir, "mine.wad")},
		{"", defaultOutput(in)},
	}
	for _, c := range cases {
		if got := resolveOut(in, c.out); got != c.want {
			t.Errorf("resolveOut(%q) = %q, want %q", c.out, got, c.want)
		}
	}
}
