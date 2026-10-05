package engine

import (
	"testing"

	"twopointfive/wad"
)

// reachGame: sector 0 (the light's room, x < 0) and sector 1 (x > 0)
// joined by a two-sided line on the Y axis — a door, closed when sector
// 1's ceiling is down on its floor.
func reachGame(doorOpen bool) *Game {
	ceil := int16(0)
	if doorOpen {
		ceil = 128
	}
	g := &Game{Level: &wad.Level{
		Vertexes: []wad.Vertex{{X: 0, Y: -64}, {X: 0, Y: 64}},
		Sectors: []wad.Sector{
			{FloorHeight: 0, CeilingHeight: 128},
			{FloorHeight: 0, CeilingHeight: ceil},
		},
		Sidedefs: []wad.Sidedef{{Sector: 0}, {Sector: 1}},
		Linedefs: []wad.Linedef{{StartVertex: 0, EndVertex: 1, FrontSidedef: 0, BackSidedef: 1}},
	}}
	g.buildSectorIndex()
	return g
}

func reaches(reach []uint32, s int) bool { return reach[s/32]&(1<<(s%32)) != 0 }

func TestLightReachStopsAtClosedDoor(t *testing.T) {
	g := reachGame(false)
	r := g.floodLightReach(0, -32, 0, 32, 200)
	if !reaches(r, 0) {
		t.Fatal("light doesn't reach its own sector")
	}
	if reaches(r, 1) {
		t.Error("light reaches through a closed door")
	}
}

func TestLightReachPassesOpenDoor(t *testing.T) {
	g := reachGame(true)
	if r := g.floodLightReach(0, -32, 0, 32, 200); !reaches(r, 1) {
		t.Error("light doesn't reach through an open door")
	}
	// ...but not when the doorway is beyond the light's radius.
	if r := g.floodLightReach(0, -300, 0, 32, 200); reaches(r, 1) {
		t.Error("light reaches a doorway outside its radius")
	}
}
