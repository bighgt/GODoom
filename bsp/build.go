package bsp

import (
	"fmt"

	"twopointfive/wad"
)

// Build reconstructs the BSP tree object graph a level's NODES lump already
// encodes. Vanilla Doom stores the tree as a flat array where each node's
// two child fields are themselves node/leaf references (see
// wad.SubsectorBit), with the tree's root always the final entry. Build
// walks that array once, turning it into the Node/InnerNode/Leaf graph the
// rest of the engine works with.
func Build(lvl *wad.Level) (Node, error) {
	if len(lvl.Nodes) == 0 {
		return nil, fmt.Errorf("bsp: level %q has no NODES lump", lvl.Name)
	}
	root := uint16(len(lvl.Nodes) - 1)
	return buildNode(lvl, root)
}

func buildNode(lvl *wad.Level, index uint16) (Node, error) {
	if index&wad.SubsectorBit != 0 {
		ssIdx := int(index &^ wad.SubsectorBit)
		if ssIdx >= len(lvl.Subsectors) {
			return nil, fmt.Errorf("bsp: subsector index %d out of range (level has %d)", ssIdx, len(lvl.Subsectors))
		}
		return &Leaf{Subsector: lvl.Subsectors[ssIdx], Index: ssIdx}, nil
	}

	if int(index) >= len(lvl.Nodes) {
		return nil, fmt.Errorf("bsp: node index %d out of range (level has %d)", index, len(lvl.Nodes))
	}
	raw := lvl.Nodes[index]

	right, err := buildNode(lvl, raw.RightChild)
	if err != nil {
		return nil, err
	}
	left, err := buildNode(lvl, raw.LeftChild)
	if err != nil {
		return nil, err
	}

	return &InnerNode{
		X: float32(raw.X), Y: float32(raw.Y),
		DX: float32(raw.DX), DY: float32(raw.DY),
		Right: right, Left: left,
	}, nil
}

// CountNodesAndLeaves walks the tree and reports how many InnerNodes and
// Leaves it contains — Phase 1 uses this to log proof the tree was rebuilt
// correctly from the WAD.
func CountNodesAndLeaves(n Node) (nodes, leaves int) {
	switch t := n.(type) {
	case *InnerNode:
		rn, rl := CountNodesAndLeaves(t.Right)
		ln, ll := CountNodesAndLeaves(t.Left)
		return rn + ln + 1, rl + ll
	case *Leaf:
		return 0, 1
	default:
		return 0, 0
	}
}
