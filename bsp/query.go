package bsp

import "twopointfive/wad"

// SegSides resolves a Seg's front (near/viewer-side) Sidedef plus the front
// and back (far side, nil if the line is one-sided) Sectors, honoring which
// side of its parent Linedef the seg actually runs along — Doom's own
// convention: Seg.Direction is exactly the "side" value (0 or 1) the
// original engine used the same way. This is the shared lookup both the
// renderer (wall/flat texturing) and PointSector (below) build on.
func SegSides(level *wad.Level, seg wad.Seg) (frontSD *wad.Sidedef, frontSec, backSec *wad.Sector) {
	if int(seg.Linedef) >= len(level.Linedefs) {
		return nil, nil, nil
	}
	linedef := level.Linedefs[seg.Linedef]

	frontIdx, backIdx := linedef.FrontSidedef, linedef.BackSidedef
	if seg.Direction != 0 {
		frontIdx, backIdx = backIdx, frontIdx
	}

	if frontIdx == wad.NoSidedef || int(frontIdx) >= len(level.Sidedefs) {
		return nil, nil, nil
	}
	fsd := &level.Sidedefs[frontIdx]
	if int(fsd.Sector) >= len(level.Sectors) {
		return nil, nil, nil
	}
	frontSD, frontSec = fsd, &level.Sectors[fsd.Sector]

	if backIdx != wad.NoSidedef && int(backIdx) < len(level.Sidedefs) {
		bsd := &level.Sidedefs[backIdx]
		if int(bsd.Sector) < len(level.Sectors) {
			backSec = &level.Sectors[bsd.Sector]
		}
	}
	return
}

// PointSectorIndex descends the BSP tree to find which Sector contains the
// map point (x, y), returning its index in level.Sectors — the same
// point-location query the original engine used for spawning things,
// gravity, stepping between sectors, and P_NoiseAlert. Returns -1 only if
// the tree/level data doesn't resolve to a sector, which shouldn't happen
// for a well-formed vanilla level.
func PointSectorIndex(tree Node, level *wad.Level, x, y float32) int {
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
				return -1
			}
			frontSD, _, _ := SegSides(level, level.Segs[t.Subsector.FirstSeg])
			if frontSD == nil {
				return -1
			}
			return int(frontSD.Sector)
		default:
			return -1
		}
	}
}

// PointSector is PointSectorIndex resolved to a *wad.Sector (nil if point
// location fails).
func PointSector(tree Node, level *wad.Level, x, y float32) *wad.Sector {
	if i := PointSectorIndex(tree, level, x, y); i >= 0 && i < len(level.Sectors) {
		return &level.Sectors[i]
	}
	return nil
}
