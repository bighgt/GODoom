package engine

import (
	"sort"
	"testing"

	"twopointfive/wad"
)

func TestBuildSectorIndex(t *testing.T) {
	// Three sectors in a row: 0 | 1 | 2. Line A is one-sided on sector 0's
	// outer edge; line B joins 0<->1; line C joins 1<->2.
	g := &Game{Level: &wad.Level{
		Sectors: []wad.Sector{{}, {}, {}},
		Sidedefs: []wad.Sidedef{
			{Sector: 0}, // 0: sd for line A (one-sided)
			{Sector: 0}, // 1: front of line B
			{Sector: 1}, // 2: back of line B
			{Sector: 1}, // 3: front of line C
			{Sector: 2}, // 4: back of line C
		},
		Linedefs: []wad.Linedef{
			{FrontSidedef: 0, BackSidedef: wad.NoSidedef}, // A
			{FrontSidedef: 1, BackSidedef: 2},             // B: 0<->1
			{FrontSidedef: 3, BackSidedef: 4},             // C: 1<->2
		},
	}}
	g.buildSectorIndex()

	nbr := func(s int) []int {
		var out []int
		for _, o := range g.sectorNeighbors[s] {
			out = append(out, int(o))
		}
		sort.Ints(out)
		return out
	}
	if got := nbr(0); len(got) != 1 || got[0] != 1 {
		t.Errorf("sector 0 neighbors = %v, want [1]", got)
	}
	if got := nbr(1); len(got) != 2 || got[0] != 0 || got[1] != 2 {
		t.Errorf("sector 1 neighbors = %v, want [0 2]", got)
	}
	if got := nbr(2); len(got) != 1 || got[0] != 1 {
		t.Errorf("sector 2 neighbors = %v, want [1]", got)
	}

	// sectorLines: sector 1 is touched by B and C (indices 1 and 2).
	if got := len(g.sectorLines[1]); got != 2 {
		t.Errorf("sector 1 touches %d lines, want 2", got)
	}
	if got := len(g.sectorLines[0]); got != 2 { // A and B
		t.Errorf("sector 0 touches %d lines, want 2", got)
	}

	// forEachAdjacentSector lazily builds the index when it wasn't primed.
	g2 := &Game{Level: g.Level}
	seen := map[int]bool{}
	g2.forEachAdjacentSector(1, func(o int) { seen[o] = true })
	if !seen[0] || !seen[2] || len(seen) != 2 {
		t.Errorf("lazy forEachAdjacentSector(1) visited %v, want {0,2}", seen)
	}
}
