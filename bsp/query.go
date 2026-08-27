package bsp

import "twopointfive/wad"

// SegSides resolves a Seg's front (near/viewer-side) and back (far side,
// nil if the line is one-sided) Sidedefs and Sectors, honoring which side
// of its parent Linedef the seg actually runs along — Doom's own
// convention: Seg.Direction is exactly the "side" value (0 or 1) the
// original engine used the same way. This is the shared lookup both the
// renderer (wall/flat texturing) and PointSector (below) build on.
func SegSides(level *wad.Level, seg wad.Seg) (frontSD, backSD *wad.Sidedef, frontSec, backSec *wad.Sector) {
	if int(seg.Linedef) >= len(level.Linedefs) {
		return nil, nil, nil, nil
	}
	linedef := level.Linedefs[seg.Linedef]

	frontIdx, backIdx := linedef.FrontSidedef, linedef.BackSidedef
	if seg.Direction != 0 {
		frontIdx, backIdx = backIdx, frontIdx
	}

	if frontIdx == wad.NoSidedef || int(frontIdx) >= len(level.Sidedefs) {
		return nil, nil, nil, nil
	}
	fsd := &level.Sidedefs[frontIdx]
	if int(fsd.Sector) >= len(level.Sectors) {
		return nil, nil, nil, nil
	}
	frontSD, frontSec = fsd, &level.Sectors[fsd.Sector]

	if backIdx != wad.NoSidedef && int(backIdx) < len(level.Sidedefs) {
		bsd := &level.Sidedefs[backIdx]
		if int(bsd.Sector) < len(level.Sectors) {
			backSD, backSec = bsd, &level.Sectors[bsd.Sector]
		}
	}
	return
}

// PointSector descends the BSP tree to find which Sector contains the map
// point (x, y) — the same point-location query the original engine used
// for spawning things, gravity, and stepping between sectors. Returns nil
// only if the tree/level data doesn't resolve to a sector, which shouldn't
// happen for a well-formed vanilla level.
func PointSector(tree Node, level *wad.Level, x, y float32) *wad.Sector {
	n := tree
	for {
		switch t := n.(type) {
		case *InnerNode:
			if t.Side(x, y) >= 0 {
				n = t.Right
			} else {
				n = t.Left
			}
		case *Leaf:
			if t.Subsector.SegCount == 0 || int(t.Subsector.FirstSeg) >= len(level.Segs) {
				return nil
			}
			_, _, frontSec, _ := SegSides(level, level.Segs[t.Subsector.FirstSeg])
			return frontSec
		default:
			return nil
		}
	}
}
