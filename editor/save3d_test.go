package editor

import (
	"path/filepath"
	"strings"
	"testing"

	"twopointfive/assets"
	"twopointfive/wad"
)

// TestSaveWritesPlayable3DWAD: the editor's Save produces a real-3D WAD —
// the only kind it saves — that the engine's own rule accepts: a TPF3D
// marker recording the base IWAD, baked voxel models, the map's embedded
// textures and all three arsenals — the same content cmd/wad3d writes. The
// default name ends "_3d.wad" and lands in the launcher's wad/ folder.
func TestSaveWritesPlayable3DWAD(t *testing.T) {
	src, err := filepath.Abs("../wad/Game WADs/doom2.wad")
	if err != nil {
		t.Fatal(err)
	}
	w, err := wad.Load(src)
	if err != nil {
		t.Skip("doom2.wad not available")
	}
	t.Chdir("..") // voxel packs, hi-res textures, the HD weapon pack and wad/ live under the repo root
	if _, ok := assets.VoxelPackPath("Doom2_Voxel.pk3"); !ok {
		t.Skip("no voxel packs under assets/voxel")
	}
	d, err := NewDoc(w, src, "MAP01")
	if err != nil {
		t.Fatal(err)
	}

	def := d.DefaultSavePath()
	if !strings.HasSuffix(def, "doom2_MAP01_3d.wad") {
		t.Errorf("default save path %q, want ..._MAP01_3d.wad", def)
	}
	if wd, ok := assets.FindDir("wad"); ok && filepath.Dir(def) != wd {
		t.Errorf("default save dir %q, want the launcher's wad/ folder %q", filepath.Dir(def), wd)
	}

	out := filepath.Join(t.TempDir(), "saved_3d.wad")
	finish, err := d.BeginSave(out)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := finish()
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if !strings.Contains(msg, "saved 3D WAD") {
		t.Errorf("status %q", msg)
	}

	res, err := wad.LoadWithIWAD(out, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := wad.CheckPlayable(res.WAD, "saved_3d.wad"); err != nil {
		t.Fatalf("the engine would refuse the saved WAD: %v", err)
	}
	if res.IWADPath != src {
		t.Errorf("saved WAD merges over %q, want its source IWAD %q", res.IWADPath, src)
	}
	for _, lump := range []string{wad.TPFHiresLump, wad.TPFWeaponsLump, wad.TPFVoxelLump} {
		if _, ok := res.WAD.Find(lump); !ok {
			t.Errorf("saved WAD lacks %s", lump)
		}
	}
	if info, _ := res.WAD.TPF3D(); len(info.Maps) != 1 || info.Maps[0] != "MAP01" {
		t.Errorf("manifest maps = %v, want [MAP01]", info.Maps)
	}
}
