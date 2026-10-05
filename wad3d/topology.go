package wad3d

import "twopointfive/wad"

// topology is the sector-level view of a map the passes reason about: which
// sectors touch which, across which lines, and which sectors something at
// runtime might move (so a pass must leave their planes alone).
type topology struct {
	lvl *wad.Level

	// lines[s] lists every linedef with a side in sector s.
	lines [][]int
	// adj[s] lists every two-sided linedef between s and a different sector,
	// as (line, other sector) pairs.
	adj [][]sectorEdge
	// mover[s] is true for a sector a special can move: tagged (a tag-driven
	// door/lift/floor/stair-builder may target it), carrying a sector
	// special, or the back sector of a manually-activated (D-type) line.
	mover []bool
}

type sectorEdge struct {
	line  int
	other int
}

func buildTopology(lvl *wad.Level) *topology {
	n := len(lvl.Sectors)
	t := &topology{
		lvl:   lvl,
		lines: make([][]int, n),
		adj:   make([][]sectorEdge, n),
		mover: make([]bool, n),
	}
	for s, sec := range lvl.Sectors {
		t.mover[s] = sec.Tag != 0 || sec.SpecialType != 0
	}
	for li, ld := range lvl.Linedefs {
		front, back := t.sideSector(ld.FrontSidedef), t.sideSector(ld.BackSidedef)
		if front >= 0 {
			t.lines[front] = append(t.lines[front], li)
		}
		if back >= 0 && back != front {
			t.lines[back] = append(t.lines[back], li)
		}
		if front >= 0 && back >= 0 && front != back {
			t.adj[front] = append(t.adj[front], sectorEdge{line: li, other: back})
			t.adj[back] = append(t.adj[back], sectorEdge{line: li, other: front})
		}
		if back >= 0 && isManualSpecial(ld.SpecialType) {
			t.mover[back] = true
		}
	}
	return t
}

// sideSector returns the sector a sidedef index faces, or -1 for "no side".
func (t *topology) sideSector(sd uint16) int {
	if sd == wad.NoSidedef || int(sd) >= len(t.lvl.Sidedefs) {
		return -1
	}
	s := int(t.lvl.Sidedefs[sd].Sector)
	if s >= len(t.lvl.Sectors) {
		return -1
	}
	return s
}

// isManualSpecial reports whether a linedef special acts on the line's own
// BACK sector rather than on tagged sectors — vanilla's manual doors
// (DR/D1 and the locked variants) and BOOM generalized types whose trigger
// is D1/DR. Such a back sector moves even with tag 0.
func isManualSpecial(sp uint16) bool {
	switch sp {
	case 1, 26, 27, 28, 31, 32, 33, 34, 117, 118:
		return true
	}
	return sp >= 0x2F80 && sp&7 >= 6
}
