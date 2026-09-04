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

// TestBuildRejectsCyclicNodes feeds Build a NODES array whose children point
// back at earlier nodes — the shape a corrupt or hostile user-supplied WAD
// could have. Build must return an error, not recurse until the stack blows.
func TestBuildRejectsCyclicNodes(t *testing.T) {
	lvl := &wad.Level{
		Name:       "CYCLE",
		Subsectors: []wad.Subsector{{SegCount: 1, FirstSeg: 0}},
		Segs:       []wad.Seg{{}},
		Nodes: []wad.Node{
			// node 0's right child is subsector 0; its left child is node 1.
			{RightChild: wad.SubsectorBit, LeftChild: 1},
			// node 1 (the root) points its left child back at node 0, and its
			// right child at itself — both make the graph not a tree.
			{RightChild: 1, LeftChild: 0},
		},
	}
	if _, err := Build(lvl); err == nil {
		t.Fatal("Build accepted a cyclic NODES lump; want an error")
	}
}
