package launcher

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"twopointfive/assets"
	"twopointfive/wad"
)

// ListWADs returns every real-3D *.wad/*.WAD file directly inside the discovered
// wad/ directory (non-recursive — a mods/ or import/ subfolder under it
// isn't meant to be picked directly), sorted case-insensitively, for the
// launcher's quick-pick list. Never an error: nothing found just means an
// empty list, and "Browse…" (window.PickWADFile) always still works.
//
// The directory itself is found via assets.FindDir("wad") — the same
// cwd-then-beside-the-executable-and-up-to-4-parents search every other
// on-disk asset (skybox, hi-res textures, HD weapons) uses, rather than a
// second copy of that search kept only for "wad".
func ListWADs() []string {
	dir, ok := assets.FindDir("wad")
	if !ok {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".wad") {
			continue
		}
		// Only real-3D WADs are playable (wad.CheckPlayable) — list just
		// those. PeekTPF3D reads the directory alone, so a folder of large
		// converted WADs stays cheap to scan.
		p := filepath.Join(dir, e.Name())
		if ok, _ := wad.PeekTPF3D(p); !ok {
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(filepath.Base(out[i])) < strings.ToLower(filepath.Base(out[j]))
	})
	return out
}
