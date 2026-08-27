// Package bsp reconstructs and traverses the binary space partitioning tree
// that every vanilla-format Doom level carries precomputed inside its own
// WAD data (the NODES, SSECTORS and SEGS lumps). See game_design.txt for
// why the engine reads this tree rather than building one itself.
package bsp

import "twopointfive/wad"

// Node is one node of the BSP tree — either an *InnerNode (a split with two
// children) or a *Leaf (a convex subsector with no further splits). It's an
// interface rather than a tagged struct so traversal code stays polymorphic
// over the two kinds instead of branching on a type field everywhere.
type Node interface {
	isNode()
}

// InnerNode is one split of the tree: a partition line plus the two child
// subtrees that lie on either side of it.
type InnerNode struct {
	X, Y   float32 // partition line start point
	DX, DY float32 // partition line direction vector
	Right  Node    // the side the partition's normal faces (the "front")
	Left   Node    // the other side
}

func (*InnerNode) isNode() {}

// Leaf is a convex subsector: a leaf of the BSP tree, bounded by one or more
// Segs, entirely within a single Sector.
type Leaf struct {
	Subsector wad.Subsector
	Index     int // this leaf's index into the level's Subsectors slice
}

func (*Leaf) isNode() {}

// Side reports which side of the partition line a point falls on, using
// Doom's convention: >= 0 is the right/front side, < 0 is the left/back side.
func (n *InnerNode) Side(x, y float32) float32 {
	return (x-n.X)*n.DY - (y-n.Y)*n.DX
}
