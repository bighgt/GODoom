package launcher

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"twopointfive/assets"
)

// ListWADs returns every *.wad/*.WAD file directly inside the discovered
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
		out = append(out, filepath.Join(dir, e.Name()))
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(filepath.Base(out[i])) < strings.ToLower(filepath.Base(out[j]))
	})
	return out
}
