package wad3d

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"twopointfive/assets"
	"twopointfive/assets/models"
	"twopointfive/wad"
)

// TestConvertedWADIsPlayable: a converted WAD carries its voxel models and
// passes the engine's playability rule; the model set the engine then
// loads from the WAD covers every Doom object type (Doom 1 maps borrowing
// Doom II models for the sprites their own pack lacks); and a vanilla WAD,
// or a 3D WAD converted without voxels, is refused with the convert-it
// message. Heretic converts too, and its report names the monsters that
// have no model (invisible under voxel-only).
func TestConvertedWADIsPlayable(t *testing.T) {
	type src struct {
		path, mapName string
		wantPacks     []string
	}
	srcs := []src{
		{"testdata/DOOM1.WAD", "E1M1", []string{"DoomVoxelPack.pk3", "Doom2_Voxel.pk3"}},
		{"wad/Game WADs/doom2.wad", "MAP01", []string{"Doom2_Voxel.pk3"}},
		{"wad/Game WADs/heretic.wad", "E1M1", []string{"reikallhereticvoxels.pk3"}},
	}
	t.Chdir("..") // the voxel packs live under assets/voxel
	if _, ok := assets.VoxelPackPath("Doom2_Voxel.pk3"); !ok {
		t.Skip("no voxel packs under assets/voxel")
	}
	for _, s := range srcs {
		iw, err := wad.Load(s.path)
		if err != nil {
			t.Logf("skip %s: %v", s.path, err)
			continue
		}
		t.Run(filepath.Base(s.path), func(t *testing.T) {
			if err := wad.CheckPlayable(iw, s.path); !errors.Is(err, wad.ErrNotReal3D) ||
				!strings.Contains(err.Error(), "wad3d") {
				t.Errorf("vanilla %s: CheckPlayable = %v, want ErrNotReal3D naming the converter", s.path, err)
			}
			lumps, rep, err := Convert(Source{Name: filepath.Base(s.path), Picked: iw, Merged: iw},
				Options{Maps: []string{s.mapName}})
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(rep.VoxelPacks, s.wantPacks) {
				t.Errorf("voxel packs = %v, want %v", rep.VoxelPacks, s.wantPacks)
			}
			out := filepath.Join(t.TempDir(), "out_3d.wad")
			if err := wad.WritePWAD(out, lumps); err != nil {
				t.Fatal(err)
			}
			if ok, err := wad.PeekTPF3D(out); !ok || err != nil {
				t.Errorf("PeekTPF3D(converted) = %v, %v", ok, err)
			}
			if ok, _ := wad.PeekTPF3D(s.path); ok {
				t.Errorf("PeekTPF3D(vanilla %s) = true", s.path)
			}
			pw, _ := wad.Load(out)
			merged := wad.Merge(iw, pw)
			if err := wad.CheckPlayable(merged, "out_3d.wad"); err != nil {
				t.Fatalf("converted WAD refused: %v", err)
			}
			vs, packs, err := assets.LoadVoxelsFromWAD(merged)
			if err != nil || len(packs) != len(s.wantPacks) || vs.Frames() == 0 {
				t.Fatalf("LoadVoxelsFromWAD = %d frames from %v, %v", vs.Frames(), packs, err)
			}
			if info, _ := merged.TPF3D(); !slices.Equal(info.Voxels, s.wantPacks) {
				t.Errorf("manifest voxels = %v, want %v", info.Voxels, s.wantPacks)
			}
			if iw.IsHeretic() {
				if !slices.ContainsFunc(rep.UnmodelledTypes, func(n string) bool { return strings.Contains(n, "iron lich") }) {
					t.Errorf("Heretic report should list its model-less monsters, got %v", rep.UnmodelledTypes)
				}
			} else if len(rep.UnmodelledTypes) > 0 {
				t.Errorf("Doom object types with no voxel model: %v", rep.UnmodelledTypes)
			}
		})
	}

	// A 3D WAD without baked voxels (an older conversion) is refused too.
	iw, err := wad.Load("testdata/DOOM1.WAD")
	if err != nil {
		return
	}
	lumps, _, err := Convert(Source{Name: "DOOM1.WAD", Picked: iw, Merged: iw},
		Options{Maps: []string{"E1M1"}, NoVoxels: true})
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "old_3d.wad")
	wad.WritePWAD(out, lumps)
	pw, _ := wad.Load(out)
	if err := wad.CheckPlayable(wad.Merge(iw, pw), "old_3d.wad"); !errors.Is(err, wad.ErrNotReal3D) ||
		!strings.Contains(err.Error(), "again") {
		t.Errorf("voxel-less 3D WAD: CheckPlayable = %v, want a convert-again error", err)
	}
}

// TestConvertBakesModels: a Doom conversion bakes the model pack found in
// assets/import (or assets/models) into TPFMODEL — only the files its
// MODELDEFs use — and the engine's loader reads it back; a Heretic
// conversion gets none (the pack is Doom art).
func TestConvertBakesModels(t *testing.T) {
	t.Chdir("..")
	if len(ModelPacks()) == 0 {
		t.Skip("no model pack in assets/models or assets/import")
	}
	d1, err := wad.Load("testdata/DOOM1.WAD")
	if err != nil {
		t.Skip("DOOM1.WAD not available")
	}
	lumps, rep, err := Convert(Source{Name: "DOOM1.WAD", Picked: d1, Merged: d1},
		Options{Maps: []string{"E1M1"}, NoVoxels: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.ModelPacks) == 0 || rep.ModelFrames == 0 || len(rep.ModelErrors) > 0 {
		t.Fatalf("models: packs %v, %d frames, errors %v", rep.ModelPacks, rep.ModelFrames, rep.ModelErrors)
	}
	out := filepath.Join(t.TempDir(), "m_3d.wad")
	if err := wad.WritePWAD(out, lumps); err != nil {
		t.Fatal(err)
	}
	pw, _ := wad.Load(out)
	set, ok, errs := models.LoadFromWAD(pw)
	if !ok || len(errs) > 0 || set.Stats.Frames != rep.ModelFrames {
		t.Errorf("reloaded: ok=%v, %d frames (baked %d), errors %v", ok, set.Stats.Frames, rep.ModelFrames, errs)
	}
	if info, _ := pw.TPF3D(); !slices.Equal(info.Models, rep.ModelPacks) {
		t.Errorf("manifest models = %v, want %v", info.Models, rep.ModelPacks)
	}

	her, err := wad.Load("wad/Game WADs/heretic.wad")
	if err != nil {
		return
	}
	lumps, rep, err = Convert(Source{Name: "heretic.wad", Picked: her, Merged: her},
		Options{Maps: []string{"E1M1"}, NoVoxels: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range lumps {
		if l.Name == wad.TPFModelLump {
			t.Error("a Heretic conversion baked the Doom model pack")
		}
	}
}
