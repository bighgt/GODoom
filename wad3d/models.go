package wad3d

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"twopointfive/assets"
	"twopointfive/assets/models"
	"twopointfive/wad"
)

// A real-3D WAD for Doom carries the 3D models (MD2/MD3/DMD + skins, bound
// to sprite frames by GZDoom MODELDEF) its map objects and projectiles are
// drawn with, when a model pack is on hand — they outrank the baked voxels
// in the first-person view. Only what the definitions actually use is
// baked: the MODELDEF files, the referenced models and their skins, pack
// paths kept, in a TPFMODEL zip (wad.TPFModelLump) that
// assets/models.Load reads exactly like the original .pk3.

// ModelPacks finds the model packs on disk: every .pk3 in assets/models/,
// else every .pk3 in assets/import/ that holds a MODELDEF (so an unrelated
// pack dropped there, like a texture pack, is skipped). Sorted by name.
func ModelPacks() []string {
	var out []string
	if dir, ok := assets.FindDir(filepath.Join("assets", "models")); ok {
		out = pk3sIn(dir, false)
	}
	if len(out) == 0 {
		if dir, ok := assets.FindDir(filepath.Join("assets", "import")); ok {
			out = pk3sIn(dir, true)
		}
	}
	return out
}

func pk3sIn(dir string, needModelDef bool) []string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".pk3") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		if needModelDef && !hasModelDef(p) {
			continue
		}
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func hasModelDef(p string) bool {
	zr, err := zip.OpenReader(p)
	if err != nil {
		return false
	}
	defer zr.Close()
	for _, f := range zr.File {
		if strings.SplitN(strings.ToLower(path.Base(f.Name)), ".", 2)[0] == "modeldef" {
			return true
		}
	}
	return false
}

// modelZip is the built TPFMODEL container plus what went into it.
type modelZip struct {
	data   []byte
	packs  []string
	frames int // sprite frames with a model
	files  int
	errs   []error
}

// buildModelZip bakes the first usable pack in packs (Doom only — model
// packs are Doom art; a Heretic WAD gets none).
func buildModelZip(w *wad.WAD, packs []string) (modelZip, error) {
	var out modelZip
	if w.IsHeretic() {
		return out, nil
	}
	for _, p := range packs {
		zr, err := zip.OpenReader(p)
		if err != nil {
			out.errs = append(out.errs, err)
			continue
		}
		set, errs := models.Load(&zr.Reader)
		out.errs = append(out.errs, errs...)
		if set == nil || set.Stats.Frames == 0 {
			zr.Close()
			continue
		}
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		byName := map[string]*zip.File{}
		for _, f := range zr.File {
			byName[f.Name] = f
		}
		for _, name := range set.Files {
			f := byName[name]
			if f == nil {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				zr.Close()
				return out, err
			}
			data, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				zr.Close()
				return out, err
			}
			if err := storeFile(zw, name, data); err != nil {
				zr.Close()
				return out, err
			}
		}
		zr.Close()
		if err := zw.Close(); err != nil {
			return out, err
		}
		out.data = buf.Bytes()
		out.packs = []string{filepath.Base(p)}
		out.frames, out.files = set.Stats.Frames, len(set.Files)
		return out, nil
	}
	if len(packs) > 0 && out.data == nil {
		return out, fmt.Errorf("no usable model pack among %v", packs)
	}
	return out, nil
}
