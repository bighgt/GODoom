package assets

import (
	"archive/zip"
	"fmt"
	"io"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// VoxelSet is a loaded voxel pack (a .pk3 — really a zip): the VOXELDEF
// sprite-frame -> KVX-file mapping, with every referenced model already
// decoded. Look a model up by the lowercase "<sprite><frame>" key the
// engine builds from a Mobj's sprite name and 0-based frame index
// (spriteNames[mo.Sprite] + 'a'+mo.Frame), e.g. "trooa".
type VoxelSet struct {
	models map[string]voxelModelRef
}

type voxelModelRef struct {
	model       *VoxelModel
	angleOffset float64
	scale       float64
}

// LoadVoxelPackDir locates assets/voxel/<pk3Name> and loads it. It looks in
// the working directory, then next to the executable and up to three parent
// directories of it — so bin\engine.exe finds the repo's assets/voxel/
// whether it's launched from the repo root or from bin\. Missing is a normal
// error the caller treats as "no voxels, use sprites".
func LoadVoxelPackDir(pk3Name string) (*VoxelSet, error) {
	rel := filepath.Join("assets", "voxel", pk3Name)
	tried := []string{}

	try := func(p string) (*VoxelSet, bool, error) {
		tried = append(tried, p)
		if _, err := os.Stat(p); err != nil {
			return nil, false, nil
		}
		vs, err := LoadVoxelPack(p)
		return vs, true, err
	}

	if wd, err := os.Getwd(); err == nil {
		if vs, found, err := try(filepath.Join(wd, rel)); found {
			return vs, err
		}
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		for i := 0; i < 4 && dir != ""; i++ {
			if vs, found, err := try(filepath.Join(dir, rel)); found {
				return vs, err
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return nil, fmt.Errorf("assets: %s not found (looked in %v)", rel, tried)
}

// LoadVoxelPack opens a voxel pk3 at pk3Path, reads its VOXELDEF and every
// KVX it references, and decodes them all up front (there are ~120 models,
// a few MB total — cheap, and it keeps the render path lock-free with no
// mid-game decode hitch).
func LoadVoxelPack(pk3Path string) (*VoxelSet, error) {
	zr, err := zip.OpenReader(pk3Path)
	if err != nil {
		return nil, fmt.Errorf("assets: open voxel pack %s: %w", pk3Path, err)
	}
	defer zr.Close()

	var defText strings.Builder
	kvx := make(map[string][]byte) // basename (no dir, no ".kvx") -> lump bytes
	for _, f := range zr.File {
		base := strings.ToLower(path.Base(f.Name))
		switch {
		case base == "voxeldef.txt":
			if b, err := readZipFile(f); err == nil {
				defText.Write(b)
				defText.WriteByte('\n')
			}
		case strings.HasSuffix(base, ".kvx"):
			if b, err := readZipFile(f); err == nil {
				kvx[strings.TrimSuffix(base, ".kvx")] = b
			}
		}
	}
	if defText.Len() == 0 {
		return nil, fmt.Errorf("assets: %s contains no VOXELDEF.txt", pk3Path)
	}

	entries := parseVoxeldef(defText.String())
	vs := &VoxelSet{models: make(map[string]voxelModelRef, len(entries))}
	decoded := make(map[string]*VoxelModel) // basename -> model (shared across frames)
	skipped := 0
	for key, e := range entries {
		raw, ok := kvx[e.file]
		if !ok {
			skipped++
			continue
		}
		m := decoded[e.file]
		if m == nil {
			mm, derr := decodeKVX(raw)
			if derr != nil {
				log.Printf("assets: voxel %s.kvx: %v", e.file, derr)
				skipped++
				continue
			}
			decoded[e.file] = mm
			m = mm
		}
		vs.models[key] = voxelModelRef{model: m, angleOffset: e.angleOffset, scale: e.scale}
	}
	if len(vs.models) == 0 {
		return nil, fmt.Errorf("assets: %s: VOXELDEF referenced no usable KVX models", pk3Path)
	}
	log.Printf("assets: voxel pack %s — %d sprite frames from %d models (%d skipped)",
		path.Base(pk3Path), len(vs.models), len(decoded), skipped)
	return vs, nil
}

// Model returns the voxel model for a sprite prefix ("TROO") and 0-based
// frame index (0 = 'A'), with its VOXELDEF orientation offset (degrees) and
// scale multiplier. ok is false when the pack has nothing for that frame —
// the caller then draws the flat sprite.
func (vs *VoxelSet) Model(spritePrefix string, frame int) (m *VoxelModel, angleOffsetDeg, scale float64, ok bool) {
	if vs == nil || frame < 0 || frame >= 26 {
		return nil, 0, 0, false
	}
	key := strings.ToLower(spritePrefix) + string(rune('a'+frame))
	ref, ok := vs.models[key]
	if !ok {
		return nil, 0, 0, false
	}
	return ref.model, ref.angleOffset, ref.scale, true
}

// Frames reports how many sprite frames the pack maps to a model — for
// startup logging and tests.
func (vs *VoxelSet) Frames() int {
	if vs == nil {
		return 0
	}
	return len(vs.models)
}

// Models returns every distinct decoded model in the pack (deduplicated —
// several sprite frames share one KVX). The hardware renderer pre-meshes
// these at level load so no upload stalls the render loop mid-game.
func (vs *VoxelSet) Models() []*VoxelModel {
	if vs == nil {
		return nil
	}
	seen := make(map[*VoxelModel]bool, len(vs.models))
	out := make([]*VoxelModel, 0, len(vs.models))
	for _, ref := range vs.models {
		if ref.model != nil && !seen[ref.model] {
			seen[ref.model] = true
			out = append(out, ref.model)
		}
	}
	return out
}

func readZipFile(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}
