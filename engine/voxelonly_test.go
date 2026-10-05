package engine

import (
	"testing"

	"twopointfive/assets"
	"twopointfive/raster"
	"twopointfive/render/worldgeo"
)

// TestMapObjectsAreVoxelOnly: with Doom II MAP01 and its voxel pack, every
// map object in view comes out as a voxel instance and none as a sprite
// billboard; a map object with no model at all isn't drawn (voxel-only —
// never a sprite fallback); while an effect (a bullet puff) still
// billboards; and a monster frame the pack skipped (the demon's SARG G
// pain frame) borrows its nearest modelled frame instead of vanishing.
func TestMapObjectsAreVoxelOnly(t *testing.T) {
	t.Chdir("..")
	g := loadRealLevel(t, "wad/Game WADs/doom2.wad", "MAP01")
	vs, err := assets.LoadVoxelPackDir("Doom2_Voxel.pk3")
	if err != nil {
		t.Skip("no Doom2_Voxel.pk3 under assets/voxel")
	}
	tex, err := assets.New(g.WAD)
	if err != nil {
		t.Fatal(err)
	}
	g.Raster = raster.New(tex, 640, 400)
	g.Voxels = vs

	objects := 0
	for _, mo := range g.mobjs {
		if isMapObject(mo) {
			objects++
		}
	}
	if objects == 0 {
		t.Fatal("test setup: MAP01 spawned no map objects")
	}
	// Look everywhere: point the camera at each object in turn and require
	// that nothing it collects is a billboard sitting on a map object.
	sawVoxel := false
	for _, mo := range g.mobjs {
		if !isMapObject(mo) {
			continue
		}
		g.Camera.X, g.Camera.Y = mo.X-64, mo.Y
		g.Camera.Angle = 0
		sprs, vox := g.collectWorldObjects()
		for _, s := range sprs {
			for _, o := range g.mobjs {
				if isMapObject(o) && s.X == float32(o.X) && s.Y == float32(o.Y) {
					t.Fatalf("map object %s drawn as a sprite billboard", o.Info.Name)
				}
			}
		}
		sawVoxel = sawVoxel || len(vox) > 0
	}
	if !sawVoxel {
		t.Error("no map object was drawn as a voxel model")
	}

	// No model at all: not drawn (appendMobjVoxel reports it handled).
	g.Voxels = nil
	var none []worldgeo.VoxelInstance
	obj := g.mobjs[0]
	for _, mo := range g.mobjs {
		if isMapObject(mo) {
			obj = mo
			break
		}
	}
	if !g.appendMobjVoxel(obj, mobjSpriteName(obj), obj.X, obj.Y, obj.Z, 0, 1, &none, true) || len(none) != 0 {
		t.Error("a map object with no voxel model must be skipped, not billboarded")
	}
	// An effect is left to the billboard path.
	puff := &Mobj{Type: MT_PUFF, Info: &mobjInfo[MT_PUFF]}
	if isMapObject(puff) || g.appendMobjVoxel(puff, "PUFF", 0, 0, 0, 0, 1, &none, true) {
		t.Error("a bullet puff (an effect) must still draw as a sprite")
	}
	g.Voxels = vs

	// The demon's pain frame: missing from the pack, borrowed from nearby.
	if _, _, _, ok := vs.Model("SARG", 'G'-'A'); ok {
		t.Skip("pack now has SARG G — nearest-frame fallback no longer exercised")
	}
	if _, _, _, ok := vs.ModelNearest("SARG", 'G'-'A'); !ok {
		t.Error("ModelNearest found no stand-in for the demon's SARG G pain frame")
	}
}
