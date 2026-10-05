package wad3d

import (
	"archive/zip"
	"bytes"
	"fmt"
	"log"
	"sort"
	"strings"

	"twopointfive/assets"
	"twopointfive/engine"
	"twopointfive/wad"
)

// Every real-3D WAD carries all three games' weapons — Doom, Doom II (the
// Super Shotgun) and Heretic — in a TPFWEAPN container (wad.TPFWeaponsLump),
// so config's switchWeapons works in any game with no other IWAD on disk,
// and shareware Doom has the registered/Doom II guns' art too.
//
// Each sprite is frozen as the exact RGBA the engine's own Sprite lookup
// produces from that game's IWAD — its own palette (a Heretic sprite can't
// be drawn through Doom's), plus the on-disk HD weapon pack and generated
// hi-res sprite overrides where they apply — so the embedded copy looks
// the same as the live one would.

// WeaponIWAD is one arsenal's source.
type WeaponIWAD struct {
	Game string   // "doom" or "heretic"
	WAD  *wad.WAD // that game's IWAD (Doom II family for "doom", so the SSG is there)
	Path string   // for the report
}

// weaponZip is the built TPFWEAPN container plus what went into it.
type weaponZip struct {
	data            []byte
	sprites, sounds int
	games           []string
	missing         []string // sprite prefixes / sounds no source had
}

func buildWeaponZip(sources []WeaponIWAD) (weaponZip, error) {
	var out weaponZip
	doomSet, hereticSet := engine.ArsenalAssets()
	wantBy := map[string]engine.WeaponAssets{"doom": doomSet, "heretic": hereticSet}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	var meta strings.Builder
	spriteDone, soundDone := map[string]bool{}, map[string]bool{}
	prefixFound := map[string]bool{}

	for _, src := range sources {
		want, ok := wantBy[src.Game]
		if !ok || src.WAD == nil {
			continue
		}
		out.games = append(out.games, src.Game)
		tex, err := assets.New(src.WAD)
		if err != nil {
			return out, fmt.Errorf("%s: %w", src.Path, err)
		}
		prefixes := map[string]bool{}
		for _, p := range want.Sprites {
			prefixes[p] = true
		}
		// Candidate lumps: the IWAD's own sprite namespace, plus the HD
		// weapon pack's frames (Doom's viewmodels exist ONLY there).
		var names []string
		for _, n := range spriteNamespace(src.WAD) {
			if len(n) >= 4 && prefixes[n[:4]] {
				names = append(names, n)
			}
		}
		for _, n := range tex.WeaponPackFrames() {
			if len(n) >= 4 && prefixes[n[:4]] {
				names = append(names, n)
			}
		}
		sort.Strings(names)
		for _, n := range names {
			if spriteDone[n] {
				continue // an earlier arsenal already supplied it (the SSG, shared by both tables)
			}
			im, ok := tex.Sprite(n)
			if !ok || im == nil {
				continue
			}
			data, line, err := assets.EncodeEmbeddedSprite(n, im)
			if err != nil {
				return out, fmt.Errorf("sprite %s: %w", n, err)
			}
			if err := storeFile(zw, assets.EmbeddedSpritesDir+"/"+n+".png", data); err != nil {
				return out, err
			}
			meta.WriteString(line + "\n")
			spriteDone[n] = true
			prefixFound[n[:4]] = true
			out.sprites++
		}
		for _, s := range want.Sounds {
			if soundDone[s] {
				continue
			}
			raw, ok := src.WAD.Find(s)
			if !ok {
				continue
			}
			if err := storeFile(zw, assets.EmbeddedSoundsDir+"/"+s+".lmp", raw); err != nil {
				return out, err
			}
			soundDone[s] = true
			out.sounds++
		}
	}
	if err := storeFile(zw, assets.EmbeddedSpriteMeta, []byte(meta.String())); err != nil {
		return out, err
	}
	if err := zw.Close(); err != nil {
		return out, err
	}

	for _, src := range sources {
		want := wantBy[src.Game]
		for _, p := range want.Sprites {
			if !prefixFound[p] && !contains(out.missing, p) {
				out.missing = append(out.missing, p)
			}
		}
		for _, s := range want.Sounds {
			if !soundDone[s] && !contains(out.missing, s) {
				out.missing = append(out.missing, s)
			}
		}
	}
	sort.Strings(out.missing)
	out.data = buf.Bytes()
	return out, nil
}

// spriteNamespace lists the lumps between a WAD's S_START/SS_START and
// S_END/SS_END markers.
func spriteNamespace(w *wad.WAD) []string {
	var out []string
	in := false
	for _, e := range w.Entries {
		switch e.Name {
		case "S_START", "SS_START":
			in = true
			continue
		case "S_END", "SS_END":
			in = false
			continue
		}
		if in && e.Size > 0 {
			out = append(out, e.Name)
		}
	}
	return out
}

func storeFile(zw *zip.Writer, name string, data []byte) error {
	fw, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
	if err != nil {
		return err
	}
	_, err = fw.Write(data)
	return err
}

// FindWeaponIWADs resolves the two arsenals' IWADs: an explicit flag wins,
// then the source file itself if it IS that game's IWAD, then the same
// search the engine uses for IWADs. A Doom 1-family IWAD stands in for a
// missing Doom II one (everything but the Super Shotgun), with a warning.
// in is the source WAD's path (the search starts beside it); picked is
// that file itself. Shared by cmd/wad3d and the level editor's Save.
func FindWeaponIWADs(in string, picked *wad.WAD, doom2Path, hereticPath string) ([]WeaponIWAD, error) {
	load := func(p string) (*wad.WAD, error) {
		w, err := wad.Load(p)
		if err != nil {
			return nil, err
		}
		if w.Header.Magic != "IWAD" {
			return nil, fmt.Errorf("%s is a %s, not an IWAD", p, w.Header.Magic)
		}
		return w, nil
	}
	isIWAD := picked.Header.Magic == "IWAD"

	var doom WeaponIWAD
	switch {
	case doom2Path != "":
		w, err := load(doom2Path)
		if err != nil {
			return nil, fmt.Errorf("-doom2: %w", err)
		}
		doom = WeaponIWAD{Game: "doom", WAD: w, Path: doom2Path}
	case isIWAD && !picked.IsHeretic() && picked.IndexOf("SHT2A0") >= 0:
		doom = WeaponIWAD{Game: "doom", WAD: picked, Path: in}
	default:
		if w, p, ok := wad.FindGameIWAD(in, "doom2"); ok {
			doom = WeaponIWAD{Game: "doom", WAD: w, Path: p}
		} else if w, p, ok := wad.FindGameIWAD(in, "doom"); ok {
			log.Printf("warning: no Doom II IWAD found — Doom weapons come from %s, without the Super Shotgun (use -doom2)", p)
			doom = WeaponIWAD{Game: "doom", WAD: w, Path: p}
		}
	}

	var her WeaponIWAD
	switch {
	case hereticPath != "":
		w, err := load(hereticPath)
		if err != nil {
			return nil, fmt.Errorf("-heretic: %w", err)
		}
		her = WeaponIWAD{Game: "heretic", WAD: w, Path: hereticPath}
	case isIWAD && picked.IsHeretic():
		her = WeaponIWAD{Game: "heretic", WAD: picked, Path: in}
	default:
		if w, p, ok := wad.FindGameIWAD(in, "heretic"); ok {
			her = WeaponIWAD{Game: "heretic", WAD: w, Path: p}
		}
	}

	var missing []string
	if doom.WAD == nil {
		missing = append(missing, "a Doom II IWAD (DOOM2.WAD, TNT.WAD, PLUTONIA.WAD...) — pass -doom2")
	}
	if her.WAD == nil {
		missing = append(missing, "a Heretic IWAD (HERETIC.WAD) — pass -heretic")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("every 3D WAD carries the Doom, Doom II and Heretic weapons, but couldn't find %s; or pass -weapons=false to skip them",
			strings.Join(missing, " and "))
	}
	log.Printf("weapons: Doom/Doom II from %s, Heretic from %s", doom.Path, her.Path)
	return []WeaponIWAD{doom, her}, nil
}
