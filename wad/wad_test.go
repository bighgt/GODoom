package wad

import "testing"

// testdataPath points at the shareware doom1.wad checked into testdata/ —
// id Software's freely-redistributable shareware episode, used here purely
// as a real-world WAD to validate the parser against. See game_design.txt
// for why no other WAD data ships with the engine.
const testdataPath = "../testdata/DOOM1.WAD"

func TestLoadSharewareDoom(t *testing.T) {
	w, err := Load(testdataPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if w.Header.Magic != "IWAD" {
		t.Errorf("Header.Magic = %q, want IWAD", w.Header.Magic)
	}
	if int(w.Header.NumLumps) != len(w.Entries) {
		t.Errorf("Header.NumLumps = %d, but decoded %d directory entries", w.Header.NumLumps, len(w.Entries))
	}

	maps := w.ListMaps()
	if len(maps) == 0 {
		t.Fatal("ListMaps returned no maps")
	}
	t.Logf("maps: %v", maps)

	lvl, err := w.LoadLevel(maps[0])
	if err != nil {
		t.Fatalf("LoadLevel(%s): %v", maps[0], err)
	}
	if len(lvl.Vertexes) == 0 || len(lvl.Linedefs) == 0 || len(lvl.Sectors) == 0 || len(lvl.Nodes) == 0 {
		t.Fatalf("level %s decoded with unexpectedly empty geometry: %+v", lvl.Name, lvl)
	}
	t.Logf("%s: %d vertexes, %d linedefs, %d sidedefs, %d sectors, %d things, %d segs, %d subsectors, %d nodes",
		lvl.Name, len(lvl.Vertexes), len(lvl.Linedefs), len(lvl.Sidedefs), len(lvl.Sectors),
		len(lvl.Things), len(lvl.Segs), len(lvl.Subsectors), len(lvl.Nodes))
}

func TestLoadPlaypalAndPatch(t *testing.T) {
	w, err := Load(testdataPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	playpal, ok := w.Find("PLAYPAL")
	if !ok {
		t.Fatal("PLAYPAL lump not found")
	}
	palettes, err := LoadPlaypal(playpal)
	if err != nil {
		t.Fatalf("LoadPlaypal: %v", err)
	}
	if len(palettes) == 0 {
		t.Fatal("LoadPlaypal returned no palettes")
	}
	t.Logf("PLAYPAL: %d palettes", len(palettes))

	// TITLEPIC is present in every version of Doom's IWAD and is a good
	// stand-in for the "picture format" every wall texture patch also uses.
	raw, ok := w.Find("HELP1")
	if !ok {
		raw, ok = w.Find("TITLEPIC")
	}
	if !ok {
		t.Fatal("no full-screen picture lump (HELP1/TITLEPIC) found to test DecodePatch against")
	}
	patch, err := DecodePatch(raw)
	if err != nil {
		t.Fatalf("DecodePatch: %v", err)
	}
	if patch.Width <= 0 || patch.Height <= 0 || len(patch.Pixels) != patch.Width*patch.Height {
		t.Fatalf("decoded patch looks wrong: %dx%d, %d pixels", patch.Width, patch.Height, len(patch.Pixels))
	}
	t.Logf("decoded patch: %dx%d", patch.Width, patch.Height)
}
