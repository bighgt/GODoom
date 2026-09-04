package engine

import (
	"os"
	"testing"

	"twopointfive/assets"
)

// TestDecorationsSpawn checks that the map-decoration THING types added for
// voxel coverage (columns, torches, hanging bodies, gore, tech lamps, ...)
// actually resolve through doomedNumToType and spawn from a real IWAD map.
func TestDecorationsSpawn(t *testing.T) {
	// Every decoration doomednum must map to a type whose sprite has a name.
	want := []int{30, 31, 32, 33, 36, 37, 41, 42, 43, 44, 45, 46, 47, 48,
		49, 50, 51, 52, 53, 54, 55, 56, 57, 59, 60, 61, 62, 63,
		25, 26, 27, 28, 29, 70, 73, 74, 75, 76, 77, 78, 79, 80, 81, 85, 86}
	for _, dn := range want {
		typ, ok := doomedNumToType[dn]
		if !ok {
			t.Errorf("decoration doomednum %d not registered", dn)
			continue
		}
		info := &mobjInfo[typ]
		if info.SpawnState == S_NULL || spriteNames[states[info.SpawnState].Sprite] == "" {
			t.Errorf("doomednum %d (type %d) has no spawn sprite", dn, typ)
		}
	}

	g := loadRealLevel(t, "../wad/Doom2.wad", "MAP03") // MAP03 is decoration-heavy
	g.Skill = 3
	g.mobjs = g.mobjs[:0]
	g.spawnMapThings()

	kinds := map[string]int{}
	for _, mo := range g.mobjs {
		if mo.Flags&(MF_SPECIAL|MF_COUNTKILL|MF_MISSILE) != 0 {
			continue
		}
		kinds[spriteNames[mo.Sprite]]++
	}
	t.Logf("MAP03 decoration sprites: %v", kinds)
	if len(kinds) < 3 {
		t.Errorf("expected several decoration kinds on MAP03, got %d: %v", len(kinds), kinds)
	}

	// Given the local voxel pack, every spawned decoration sprite frame 0
	// must have a model (that's the whole point of adding them).
	const packPath = "../assets/voxel/Doom2_Voxel.pk3"
	if _, err := os.Stat(packPath); err != nil {
		return
	}
	vs, err := assets.LoadVoxelPack(packPath)
	if err != nil {
		t.Fatalf("LoadVoxelPack: %v", err)
	}
	for spr := range kinds {
		if _, _, _, ok := vs.Model(spr, 0); !ok {
			t.Errorf("decoration %s spawned but has no voxel model", spr)
		}
	}
}
