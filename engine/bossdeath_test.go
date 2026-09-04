package engine

import (
	"testing"

	"twopointfive/wad"
)

// bossMap builds a minimal E1M8-shaped level: sector 0 is tagged 666 with a
// raised floor, sector 1 is its lower neighbour, joined by one two-sided
// line — so EV_DoFloor(666, lowerToLowest) has somewhere to go.
func bossMap(name string) *Game {
	g := &Game{Level: &wad.Level{
		Name:     name,
		Sectors:  []wad.Sector{{Tag: 666, FloorHeight: 64, CeilingHeight: 128}, {FloorHeight: 0, CeilingHeight: 128}},
		Sidedefs: []wad.Sidedef{{Sector: 0}, {Sector: 1}},
		Vertexes: []wad.Vertex{{}, {X: 64}},
		Linedefs: []wad.Linedef{{StartVertex: 0, EndVertex: 1, FrontSidedef: 0, BackSidedef: 1}},
	}}
	g.sectorActive = map[int]bool{}
	g.Player.Health = 100
	g.playerMobj = &Mobj{Type: MT_PLAYER, Info: &mobjInfo[MT_PLAYER], Health: 100, Height: 56}
	return g
}

func TestBossDeathTriggersTaggedFloorOnlyWhenAllDead(t *testing.T) {
	g := bossMap("E1M8")
	b1 := g.P_SpawnMobj(0, 0, 0, MT_BRUISER)
	b2 := g.P_SpawnMobj(100, 0, 0, MT_BRUISER)

	// First Baron dies while the second still lives: nothing happens.
	b1.Health = 0
	aBossDeath(g, b1)
	if g.sectorActive[0] || len(g.thinkers) != 0 {
		t.Fatal("boss floor triggered while a Baron was still alive")
	}

	// Second (last) Baron dies: the tag-666 floor starts moving.
	b2.Health = 0
	aBossDeath(g, b2)
	if !g.sectorActive[0] || len(g.thinkers) != 1 {
		t.Fatalf("last Baron death didn't start the tag-666 floor (active=%v thinkers=%d)",
			g.sectorActive[0], len(g.thinkers))
	}

	// And it actually lowers sector 0 toward its neighbour's height.
	for i := 0; i < 200 && len(g.thinkers) > 0; i++ {
		g.tickSpecials()
	}
	if g.Level.Sectors[0].FloorHeight != 0 {
		t.Errorf("tag-666 floor stopped at %d, want 0", g.Level.Sectors[0].FloorHeight)
	}
}

func TestBossDeathInertOnNonBossMapAndWhenPlayerDead(t *testing.T) {
	// Right monster, wrong map.
	g := bossMap("E1M1")
	b := g.P_SpawnMobj(0, 0, 0, MT_BRUISER)
	b.Health = 0
	aBossDeath(g, b)
	if g.sectorActive[0] || len(g.thinkers) != 0 {
		t.Error("boss death fired on E1M1")
	}

	// Right map, but no player left alive for the victory.
	g2 := bossMap("E1M8")
	g2.Player.Health = 0
	b2 := g2.P_SpawnMobj(0, 0, 0, MT_BRUISER)
	b2.Health = 0
	aBossDeath(g2, b2)
	if g2.sectorActive[0] || len(g2.thinkers) != 0 {
		t.Error("boss death fired with a dead player")
	}
}
