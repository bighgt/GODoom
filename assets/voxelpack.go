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

// hFlameKey is the reserved models[] key holding reikall's Heretic wall-torch
// flame (TFULA) — not a sprite, so it can't collide with a real entry.
// HereticTorchFlame reads it; engine overlays it on voxelized torches.
const hFlameKey = "\x00hflame"

// HereticTorchFlame returns reikall's standalone wall-torch flame model, or
// nil if this pack doesn't carry one. The engine draws it (emissive, gently
// spinning) on top of every Heretic torch/brazier that resolved to a voxel,
// which reikall's pack models without a flame.
func (vs *VoxelSet) HereticTorchFlame() *VoxelModel {
	if vs == nil {
		return nil
	}
	if ref, ok := vs.models[hFlameKey]; ok {
		return ref.model
	}
	return nil
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
		case base == "voxeldef.txt" || base == "voxeldef":
			// GZDoom accepts the lump both named "VOXELDEF" (no extension,
			// e.g. reikall's Heretic pack) and "voxeldef.txt".
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

	// reikall's Heretic pack wires most decoration / ammo voxels through its
	// DECORATE, not VOXELDEF (this engine has no DECORATE parser). Detect
	// that pack (its keys use the bare-4-letter form, stored "<name>*") and
	// fill in the common sprite -> KVX pairings by hand so those things
	// voxelize too. Each maps every frame of the sprite to one model.
	if _, heretic := vs.models["akyy*"]; heretic {
		added := 0
		for sprite, file := range hereticVoxelAlias {
			if _, have := vs.models[sprite+"*"]; have {
				continue
			}
			raw, ok := kvx[file]
			if !ok {
				continue
			}
			m := decoded[file]
			if m == nil {
				mm, derr := decodeKVX(raw)
				if derr != nil {
					continue
				}
				decoded[file] = mm
				m = mm
			}
			vs.models[sprite+"*"] = voxelModelRef{model: m, scale: 1}
			added++
		}
		if added > 0 {
			log.Printf("assets: voxel pack %s — +%d Heretic decoration/ammo aliases", path.Base(pk3Path), added)
		}

		// reikall's wall-torch flame (TFULA.kvx) is a standalone model its
		// DECORATE spawns as a child of the wall torch — a voxelized Heretic
		// torch otherwise shows only the bare bracket. Stash it under a
		// reserved key so the engine can overlay it, emissive, on every
		// voxelized torch/brazier (engine/heretic.go hereticTorchFlames).
		if raw, ok := kvx["tfula"]; ok {
			m := decoded["tfula"]
			if m == nil {
				if mm, derr := decodeKVX(raw); derr == nil {
					decoded["tfula"] = mm
					m = mm
				}
			}
			if m != nil {
				vs.models[hFlameKey] = voxelModelRef{model: m, scale: 1}
			}
		}
	}

	log.Printf("assets: voxel pack %s — %d sprite frames from %d models (%d skipped)",
		path.Base(pk3Path), len(vs.models), len(decoded), skipped)
	return vs, nil
}

// hereticVoxelAlias pairs a Heretic sprite prefix (lowercase) with the KVX
// basename in reikall's pack that its DECORATE would bind to it.
var hereticVoxelAlias = map[string]string{
	"amg1": "amg1a", "amg2": "amg2a", "amc1": "amc1a", "amc2": "amc2a",
	"amb1": "amb1a", "amb2": "amb2a", "amm1": "amm1a", "amm2": "amm2a",
	"ams1": "ams1a", "ams2": "ams2a", "amp1": "amp1a", "amp2": "amp2a",
	"kfr1": "kfr1n", "wtrh": "wtrhd", "barl": "barla", "brpl": "brpla",
	"smpl": "smpla", "stgs": "stgsa", "stgl": "stgla", "stcs": "stcsa",
	"stcl": "stcla", "mos1": "mos1a", "mos2": "mos2a", "hcor": "hcora",
	"vlco": "vlcoa", "chdl": "chdla", "srtc": "srtct",
	"kgz1": "kgz1a", "kgzb": "kgzba", "kgzg": "kgzga", "kgzy": "kgzya",
	// hanging skulls-on-a-stick and the six weapon pickups — KVX present in
	// the pack, wired only through its DECORATE.
	"skh1": "skh1a", "skh2": "skh2a", "skh3": "skh3a", "skh4": "skh4a",
	"wgnt": "wgnta", "wbow": "wbowa", "wbls": "wblsa",
	"wskl": "wskla", "wphx": "wphxa", "wmce": "wmcea",
}

// Model returns the voxel model for a sprite prefix ("TROO") and 0-based
// frame index (0 = 'A'), with its VOXELDEF orientation offset (degrees) and
// scale multiplier. ok is false when the pack has nothing for that frame —
// the caller then draws the flat sprite.
func (vs *VoxelSet) Model(spritePrefix string, frame int) (m *VoxelModel, angleOffsetDeg, scale float64, ok bool) {
	if vs == nil || frame < 0 || frame >= 26 {
		return nil, 0, 0, false
	}
	pfx := strings.ToLower(spritePrefix)
	ref, ok := vs.models[pfx+string(rune('a'+frame))]
	if !ok {
		// A VOXELDEF entry keyed by a bare 4-letter sprite name (no frame)
		// applies to every frame — parseVoxeldef stores it under "<sprite>*".
		ref, ok = vs.models[pfx+"*"]
	}
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
