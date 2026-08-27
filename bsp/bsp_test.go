package bsp

import (
	"testing"

	"twopointfive/wad"
)

func TestBuildAndTraverseSharewareDoom(t *testing.T) {
	w, err := wad.Load("../testdata/DOOM1.WAD")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	maps := w.ListMaps()
	if len(maps) == 0 {
		t.Fatal("no maps found")
	}
	lvl, err := w.LoadLevel(maps[0])
	if err != nil {
		t.Fatalf("LoadLevel(%s): %v", maps[0], err)
	}

	tree, err := Build(lvl)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	nodes, leaves := CountNodesAndLeaves(tree)
	if leaves == 0 {
		t.Fatal("expected at least one leaf in the BSP tree")
	}
	if leaves != len(lvl.Subsectors) {
		t.Errorf("tree has %d leaves, level has %d subsectors", leaves, len(lvl.Subsectors))
	}
	t.Logf("%s BSP: %d inner nodes, %d leaves", lvl.Name, nodes, leaves)

	visited := 0
	Traverse(tree, 0, 0, func(*Leaf) { visited++ })
	if visited != leaves {
		t.Errorf("Traverse visited %d leaves, want %d (every leaf exactly once)", visited, leaves)
	}
}
