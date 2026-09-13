package nodebuild

import (
	"twopointfive/bsp"
	"twopointfive/wad"
)

// subsectorBit marks a NODES child reference as a subsector index rather
// than a node index (Doom's NF_SUBSECTOR).
const subsectorBit = 0x8000

// Nodes walks the built tree depth-first, post-order, and returns the
// []wad.Node the NODES lump encodes: children are emitted before their
// parent so a parent can reference them by index, and a leaf child is
// stored as (subsector index | subsectorBit). The root is the last entry.
func Nodes(root bsp.Node) []wad.Node {
	var out []wad.Node
	var walk func(n bsp.Node) uint16
	walk = func(n bsp.Node) uint16 {
		switch t := n.(type) {
		case *bsp.Leaf:
			return uint16(t.Index) | subsectorBit
		case *bsp.InnerNode:
			r := walk(t.Right)
			l := walk(t.Left)
			out = append(out, wad.Node{
				X: int16(t.X), Y: int16(t.Y),
				DX: int16(t.DX), DY: int16(t.DY),
				RightChild: r, LeftChild: l,
			})
			return uint16(len(out) - 1)
		}
		return subsectorBit // empty tree — one dummy subsector ref
	}
	walk(root)
	return out
}

// Lumps encodes a build Result plus its map into the raw bytes for the
// SEGS, SSECTORS and NODES lumps (in that order).
func (r *Result) Lumps() (segs, ssectors, nodes []byte) {
	return wad.EncodeSegs(r.Segs), wad.EncodeSubsectors(r.Subsectors), wad.EncodeNodes(Nodes(r.Root))
}

// Reject returns an all-zero REJECT lump for n sectors — "no sector pair is
// ever rejected", always valid, just no sight-check acceleration.
func Reject(numSectors int) []byte {
	if numSectors < 0 {
		numSectors = 0
	}
	bits := numSectors * numSectors
	return make([]byte, (bits+7)/8)
}
