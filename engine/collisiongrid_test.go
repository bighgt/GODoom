package engine

import (
	"testing"

	"twopointfive/wad"
)

func TestCollisionGridBuckets(t *testing.T) {
	lvl := &wad.Level{
		Vertexes: []wad.Vertex{
			{X: 0, Y: 0}, {X: 0, Y: 64}, // line 0 sits on the origin
			{X: 1000, Y: 1000}, {X: 1000, Y: 1064}, // line 1 is ~1400 units away
		},
		Linedefs: []wad.Linedef{
			{StartVertex: 0, EndVertex: 1},
			{StartVertex: 2, EndVertex: 3},
		},
	}
	g := buildCollisionGrid(lvl)
	if g == nil {
		t.Fatal("buildCollisionGrid returned nil for a non-empty level")
	}

	saw := map[int32]bool{}
	g.forEachNear(0, 0, 16, func(i int32) bool { saw[i] = true; return true })
	if !saw[0] {
		t.Error("query at the origin didn't visit the linedef sitting on it")
	}
	if saw[1] {
		t.Error("query at the origin visited a far-away linedef")
	}

	count := 0
	g.forEachNear(1000, 1000, 16, func(int32) bool { count++; return false })
	if count != 1 {
		t.Errorf("forEachNear kept iterating after fn returned false: %d calls", count)
	}
}

func TestCollisionGridEmptyLevelAndNilSafety(t *testing.T) {
	if g := buildCollisionGrid(&wad.Level{}); g != nil {
		t.Error("buildCollisionGrid(empty level) should be nil")
	}
	var g *collisionGrid
	g.forEachNear(0, 0, 10, func(int32) bool {
		t.Fatal("forEachNear called fn on a nil grid")
		return true
	})
	g.forEachAlongSegment(0, 0, 100, 100, func(int32) bool {
		t.Fatal("forEachAlongSegment called fn on a nil grid")
		return true
	})
}

func TestForEachAlongSegment(t *testing.T) {
	// A long horizontal corridor of vertical linedefs, one every 128 units.
	var verts []wad.Vertex
	var lines []wad.Linedef
	for i := 0; i < 40; i++ {
		x := int16(i * 128)
		verts = append(verts, wad.Vertex{X: x, Y: -32}, wad.Vertex{X: x, Y: 32})
		lines = append(lines, wad.Linedef{StartVertex: uint16(2 * i), EndVertex: uint16(2*i + 1)})
	}
	g := buildCollisionGrid(&wad.Level{Vertexes: verts, Linedefs: lines})

	// A ray down the corridor must see every line between its endpoints and
	// visit each at most... well, a bounded handful of times.
	visits := map[int32]int{}
	g.forEachAlongSegment(10, 0, 1000, 0, func(i int32) bool { visits[i]++; return true })
	for i := int32(1); i <= 7; i++ { // lines at x=128..896 lie within [10,1000]
		if visits[i] == 0 {
			t.Errorf("segment missed line %d at x=%d", i, i*128)
		}
		if visits[i] > 6 {
			t.Errorf("segment visited line %d %d times (dedup ineffective)", i, visits[i])
		}
	}
	if visits[20] != 0 {
		t.Errorf("segment visited far line 20 (x=2560), %d times", visits[20])
	}

	// Early-out is honoured.
	calls := 0
	g.forEachAlongSegment(10, 0, 5000, 0, func(int32) bool { calls++; return false })
	if calls != 1 {
		t.Errorf("forEachAlongSegment kept going after fn returned false: %d calls", calls)
	}
}
