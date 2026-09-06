package wad

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// hasLump reports whether w contains a lump of the given name.
func hasLump(w *WAD, name string) bool {
	return w.IndexOf(name) >= 0
}

// iwadNames lists the base IWAD filenames to look for, by map format, most
// common first. All Doom II-format IWADs (Doom II, Final Doom, Freedoom
// Phase 2) share the MAPxx lump set, so any of them serves a Doom II PWAD.
var iwadNames = map[string][]string{
	"doom2": {"DOOM2.WAD", "doom2.wad", "Doom2.wad", "TNT.WAD", "tnt.wad",
		"PLUTONIA.WAD", "plutonia.wad", "freedoom2.wad", "FREEDOOM2.WAD"},
	"doom1": {"DOOM.WAD", "doom.wad", "DOOMU.WAD", "doomu.wad",
		"DOOM1.WAD", "doom1.wad", "freedoom1.wad", "FREEDOOM1.WAD"},
}

// LoadResult is LoadWithIWAD's return value: the ready-to-use *WAD (already
// merged, if a merge was needed) plus the diagnostics a caller logs about
// what actually happened.
type LoadResult struct {
	WAD *WAD
	// PickedMagic/PickedLumps describe the file at the requested path BEFORE
	// any IWAD merge — "loaded %s WAD, %d lumps" is worth logging even when
	// (especially when) it turned out to need a merge.
	PickedMagic string
	PickedLumps int32
	// IWADPath is the base IWAD that got merged in, or "" if path was
	// already self-contained (a real IWAD, or any WAD with its own PLAYPAL).
	IWADPath string
}

// LoadWithIWAD loads the WAD at path and, if it turns out to be a PWAD (or
// any WAD missing the core palette — PLAYPAL), finds and merges its base
// IWAD before handing back one combined *WAD — load order IWAD-then-PWAD,
// exactly like Doom's `-file`. explicitIWAD (config.json's "iwad") wins over
// the auto-search when set; pass "" to always auto-search.
//
// This is the one place that knows how to turn a picked file into a
// complete, playable WAD — both cmd/engine's normal startup and package
// launcher (which needs a WAD's map list the moment one is picked, before
// the game itself loads) call this instead of duplicating the search.
func LoadWithIWAD(path, explicitIWAD string) (LoadResult, error) {
	picked, err := Load(path)
	if err != nil {
		return LoadResult{}, err
	}
	res := LoadResult{WAD: picked, PickedMagic: picked.Header.Magic, PickedLumps: picked.Header.NumLumps}
	if picked.Header.Magic != "PWAD" && hasLump(picked, "PLAYPAL") {
		return res, nil // self-contained — an IWAD, or a WAD carrying its own palette
	}
	iwad, iwadPath, err := resolveIWAD(path, picked, explicitIWAD)
	if err != nil {
		return LoadResult{}, err
	}
	res.WAD = Merge(iwad, picked)
	res.IWADPath = iwadPath
	return res, nil
}

// resolveIWAD finds the base IWAD a patch WAD needs. An explicit path (from
// config.json's "iwad") wins; otherwise it works out the map format the
// PWAD uses and hunts for a matching IWAD next to the PWAD, up its parent
// directories, and beside the executable / working directory (also checking
// wad/ and testdata/ subdirs). The returned WAD is a real IWAD with a
// palette.
func resolveIWAD(pickedPath string, picked *WAD, explicit string) (*WAD, string, error) {
	usable := func(p string) (*WAD, bool) {
		iw, err := Load(p)
		if err != nil || iw.Header.Magic != "IWAD" || !hasLump(iw, "PLAYPAL") {
			return nil, false
		}
		return iw, true
	}

	if explicit != "" {
		if iw, ok := usable(explicit); ok {
			return iw, explicit, nil
		}
		return nil, "", fmt.Errorf("wad: config iwad %q is not a usable IWAD (missing, unreadable, or no PLAYPAL)", explicit)
	}

	format := "doom2"
	for _, m := range picked.ListMaps() {
		if len(m) > 0 && m[0] == 'E' {
			format = "doom1"
			break
		}
	}

	// Base directories to search, in priority order.
	var dirs []string
	seenDir := map[string]bool{}
	addDir := func(d string) {
		if d == "" {
			return
		}
		for _, sub := range []string{"", "wad", "wads", "IWADs", "iwads", "testdata"} {
			p := filepath.Join(d, sub)
			if !seenDir[p] {
				seenDir[p] = true
				dirs = append(dirs, p)
			}
		}
	}
	dir := filepath.Dir(pickedPath)
	for i := 0; i < 5 && dir != "" && dir != string(filepath.Separator); i++ {
		addDir(dir)
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	if wd, err := os.Getwd(); err == nil {
		addDir(wd)
	}
	if exe, err := os.Executable(); err == nil {
		addDir(filepath.Dir(exe))
	}

	// Try the format's IWADs first, then the other format's (a WAD may
	// mis-detect, and a Doom II IWAD still opens a Doom II PWAD).
	tryOrder := []string{format}
	if format == "doom2" {
		tryOrder = append(tryOrder, "doom1")
	} else {
		tryOrder = append(tryOrder, "doom2")
	}
	for _, fmtKey := range tryOrder {
		for _, name := range iwadNames[fmtKey] {
			for _, d := range dirs {
				p := filepath.Join(d, name)
				if _, err := os.Stat(p); err != nil {
					continue
				}
				if iw, ok := usable(p); ok {
					return iw, p, nil
				}
			}
		}
	}

	want := "DOOM2.WAD (or TNT.WAD / PLUTONIA.WAD / freedoom2.wad)"
	if format == "doom1" {
		want = "DOOM.WAD (or DOOM1.WAD / freedoom1.wad)"
	}
	return nil, "", fmt.Errorf("wad: %s is a patch WAD and needs its base IWAD — put %s next to it, or set \"iwad\": \"C:\\path\\to\\%s\" in config.json",
		filepath.Base(pickedPath), want, strings.SplitN(want, " ", 2)[0])
}
