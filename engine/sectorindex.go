package engine

import "twopointfive/wad"

// buildSectorIndex precomputes, once per loaded level, two adjacency tables
// the p_spec.c "surrounding sector" queries need:
//
//   - sectorLines[s]:     every Linedef index that has a side facing sector s
//   - sectorNeighbors[s]: every sector that shares a two-sided Linedef with s
//
// id's P_FindLowestFloorSurrounding / P_FindNextHighestFloor /
// P_FindMinSurroundingLight / EV_BuildStairs and friends each rescan every
// Linedef in the level on every call. A single switch press can fire
// several of those, and a big Doom II map has a couple of thousand
// linedefs — so the scans turn into real per-press cost. With these tables
// each query touches only the handful of lines/sectors actually adjacent.
//
// Built from NewGame / loadNextMap (via spawnSpecials) and never mutated
// afterward: doors and lifts animate a sector's heights, not the linedef
// graph, so the adjacency never goes stale.
func (g *Game) buildSectorIndex() {
	n := len(g.Level.Sectors)
	g.sectorLines = make([][]int32, n)
	g.sectorNeighbors = make([][]int32, n)
	g.soundTarget = make([]*Mobj, n)
	g.soundTraversed = make([]int, n)
	g.soundValid = make([]uint32, n)
	g.soundValidCount = 0
	if n == 0 {
		return
	}

	nbr := make([]map[int32]struct{}, n)
	for i := range g.Level.Linedefs {
		ld := &g.Level.Linedefs[i]
		fs, fok := g.sideSector(ld.FrontSidedef)
		if fok {
			g.sectorLines[fs] = append(g.sectorLines[fs], int32(i))
		}
		if ld.BackSidedef == wad.NoSidedef {
			continue
		}
		bs, bok := g.sideSector(ld.BackSidedef)
		if !bok {
			continue
		}
		g.sectorLines[bs] = append(g.sectorLines[bs], int32(i))
		if !fok || fs == bs {
			continue
		}
		if nbr[fs] == nil {
			nbr[fs] = map[int32]struct{}{}
		}
		if nbr[bs] == nil {
			nbr[bs] = map[int32]struct{}{}
		}
		nbr[fs][int32(bs)] = struct{}{}
		nbr[bs][int32(fs)] = struct{}{}
	}
	for s := range nbr {
		if nbr[s] == nil {
			continue
		}
		lst := make([]int32, 0, len(nbr[s]))
		for o := range nbr[s] {
			lst = append(lst, o)
		}
		g.sectorNeighbors[s] = lst
	}
}

// sideSector resolves a sidedef index to its sector index, reporting false
// for the out-of-range indices some PWADs carry.
func (g *Game) sideSector(side uint16) (int, bool) {
	if int(side) >= len(g.Level.Sidedefs) {
		return 0, false
	}
	s := int(g.Level.Sidedefs[side].Sector)
	if s < 0 || s >= len(g.Level.Sectors) {
		return 0, false
	}
	return s, true
}

// ensureSectorIndex lazily builds the adjacency tables the first time a
// surrounding-sector query runs, so code paths that reach those helpers
// without having gone through spawnSpecials (unit tests, mainly) still work.
func (g *Game) ensureSectorIndex() {
	if g.sectorNeighbors == nil {
		g.buildSectorIndex()
	}
}
