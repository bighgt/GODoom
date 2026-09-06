package launcher

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// findWADDir locates a "wad" directory the same way assets.findAssetDir
// looks for an optional asset directory: relative to the current working
// directory first (how this project is run during development), then
// relative to the running executable's directory and up to four parents of
// it (so bin/linux_engine launched from anywhere still finds the repo's
// wad/ folder). Returns ok=false if none exists — the quick-pick list is
// just empty then, Browse… still works.
func findWADDir() (string, bool) {
	const relDir = "wad"
	isDir := func(p string) bool {
		st, err := os.Stat(p)
		return err == nil && st.IsDir()
	}
	if isDir(relDir) {
		return relDir, true
	}
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}
	dir := filepath.Dir(exe)
	for i := 0; i < 5 && dir != ""; i++ {
		if cand := filepath.Join(dir, relDir); isDir(cand) {
			return cand, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", false
}

// ListWADs returns every *.wad/*.WAD file directly inside the discovered
// wad/ directory (non-recursive — a mods/ or import/ subfolder under it
// isn't meant to be picked directly), sorted case-insensitively, for the
// launcher's quick-pick list. Never an error: nothing found just means an
// empty list, and "Browse…" (window.PickWADFile) always still works.
func ListWADs() []string {
	dir, ok := findWADDir()
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
