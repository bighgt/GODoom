package wad3d

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"strings"

	"twopointfive/assets"
	"twopointfive/engine"
	"twopointfive/wad"
)

// Every real-3D WAD carries the voxel models its map objects are drawn
// with — the engine draws map objects voxel-only and reads voxels only from
// the WAD (assets.LoadVoxelsFromWAD). The chosen packs are stored whole in
// a TPFVOXEL zip (wad.TPFVoxelLump), named "<n>-<pack>.pk3" so their order
// is their priority: each later pack fills only sprites the earlier ones
// lack entirely.

// VoxelPacksFor picks the voxel packs (assets/voxel/*.pk3) for a WAD's
// game, in priority order:
//   - Heretic: reikall's Heretic pack (items/decorations — it has no
//     monster models, so Heretic monsters are invisible voxel-only);
//   - Doom II-format maps (MAPxx): Doom2_Voxel.pk3, which covers the whole
//     Doom + Doom II roster;
//   - Doom 1 maps (ExMy): DoomVoxelPack.pk3 (the updated Doom 1 set), with
//     Doom2_Voxel.pk3 filling the ~16 sprites it lacks (chaingunner, Hell
//     knight, megasphere, some lamps and gore, ...).
func VoxelPacksFor(w *wad.WAD, maps []string) []string {
	if w.IsHeretic() {
		return []string{"reikallhereticvoxels.pk3"}
	}
	for _, m := range maps {
		if strings.HasPrefix(m, "MAP") {
			return []string{"Doom2_Voxel.pk3"}
		}
	}
	return []string{"DoomVoxelPack.pk3", "Doom2_Voxel.pk3"}
}

// voxelZip is the built TPFVOXEL container plus what went into it.
type voxelZip struct {
	data       []byte
	packs      []string
	unmodelled []string // map-object types no pack has a model for
	sprites    int      // sprite frames covered
}

func buildVoxelZip(w *wad.WAD, packs []string) (voxelZip, error) {
	var out voxelZip
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	var vs *assets.VoxelSet
	for i, name := range packs {
		p, ok := assets.VoxelPackPath(name)
		if !ok {
			return out, fmt.Errorf("voxel pack assets/voxel/%s not found — every 3D WAD needs its voxel models", name)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return out, err
		}
		// Load it too: proves it's a usable pack before it's baked in, and
		// gives the coverage report below.
		v, err := assets.LoadVoxelPackBytes(data, name)
		if err != nil {
			return out, err
		}
		if vs == nil {
			vs = v
		} else {
			vs.FillFrom(v)
		}
		if err := storeFile(zw, fmt.Sprintf("%d-%s", i+1, name), data); err != nil {
			return out, err
		}
		out.packs = append(out.packs, name)
	}
	if err := zw.Close(); err != nil {
		return out, err
	}
	out.data = buf.Bytes()
	out.sprites = vs.Frames()
	out.unmodelled = engine.UnmodelledObjects(w, vs)
	return out, nil
}
