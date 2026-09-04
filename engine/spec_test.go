package engine

import (
	"testing"

	"twopointfive/wad"
)

func TestSwitchToggle(t *testing.T) {
	if s, ok := switchToggle("SW1BRN1"); !ok || s != "SW2BRN1" {
		t.Errorf("SW1BRN1 -> %q,%v", s, ok)
	}
	if s, ok := switchToggle("SW2EXIT"); !ok || s != "SW1EXIT" {
		t.Errorf("SW2EXIT -> %q,%v", s, ok)
	}
	if _, ok := switchToggle("BROWN96"); ok {
		t.Error("non-switch texture toggled")
	}
}

func specGame(sectors []wad.Sector, lines []wad.Linedef) *Game {
	g := &Game{Level: &wad.Level{
		Sectors:  sectors,
		Linedefs: lines,
		Sidedefs: []wad.Sidedef{{Sector: 0}, {Sector: 1}},
		Vertexes: []wad.Vertex{{}, {X: 64}},
	}}
	g.sectorActive = map[int]bool{}
	return g
}

func TestFindSectorsFromTag(t *testing.T) {
	g := specGame([]wad.Sector{{Tag: 5}, {Tag: 7}, {Tag: 5}}, nil)
	got := g.findSectorsFromTag(5)
	if len(got) != 2 || got[0] != 0 || got[1] != 2 {
		t.Errorf("tag 5 -> %v, want [0 2]", got)
	}
	if len(g.findSectorsFromTag(9)) != 0 {
		t.Error("unknown tag returned sectors")
	}
}

func TestDoorOpensThenCloses(t *testing.T) {
	// Sector 0 is the door (closed: ceiling == floor); sector 1 is the
	// neighbouring room, ceiling 128, sharing a two-sided line.
	g := specGame(
		[]wad.Sector{{FloorHeight: 0, CeilingHeight: 0}, {FloorHeight: 0, CeilingHeight: 128}},
		[]wad.Linedef{{StartVertex: 0, EndVertex: 1, FrontSidedef: 0, BackSidedef: 1}},
	)
	g.startDoor(0, dkOpenWaitClose)
	// Rise: 0 -> (128-4) at 2/tic.
	for i := 0; i < 200 && g.Level.Sectors[0].CeilingHeight < 124; i++ {
		g.tickSpecials()
	}
	if g.Level.Sectors[0].CeilingHeight != 124 {
		t.Fatalf("door didn't open fully: ceiling=%d", g.Level.Sectors[0].CeilingHeight)
	}
	if !g.sectorActive[0] {
		t.Error("door sector not marked active while waiting")
	}
	// Wait (150 tics) then close back to 0.
	for i := 0; i < 400 && len(g.thinkers) > 0; i++ {
		g.tickSpecials()
	}
	if g.Level.Sectors[0].CeilingHeight != 0 {
		t.Errorf("door didn't close: ceiling=%d", g.Level.Sectors[0].CeilingHeight)
	}
	if g.sectorActive[0] {
		t.Error("door sector still active after finishing")
	}
}

func TestActivateExitLine(t *testing.T) {
	g := specGame([]wad.Sector{{}}, []wad.Linedef{{SpecialType: 11}})
	g.activateLine(0, nil, trUse)
	if g.exitLevel != 1 {
		t.Errorf("special 11 -> exitLevel %d, want 1 (normal)", g.exitLevel)
	}
	g2 := specGame([]wad.Sector{{}}, []wad.Linedef{{SpecialType: 51}})
	g2.activateLine(0, nil, trUse)
	if g2.exitLevel != 2 {
		t.Errorf("special 51 -> exitLevel %d, want 2 (secret)", g2.exitLevel)
	}
}

func TestNonRepeatableSpecialClearsItself(t *testing.T) {
	g := specGame([]wad.Sector{{Tag: 1}, {Tag: 1, CeilingHeight: 100}},
		[]wad.Linedef{{SpecialType: 2, SectorTag: 1}}) // W1 door open stay
	g.activateLine(0, nil, trCross)
	if g.Level.Linedefs[0].SpecialType != 0 {
		t.Error("W1 special not cleared after firing")
	}
}

// Regression: several specials the real IWADs use fell through activateLine's
// default (logged "unhandled", did nothing) — locked switch doors especially.
func TestPreviouslyUnhandledSpecials(t *testing.T) {
	tagged := func(num uint16, trig lineTrigger) bool {
		g := specGame([]wad.Sector{{Tag: 3, CeilingHeight: 0}, {Tag: 3, CeilingHeight: 128}},
			[]wad.Linedef{{SpecialType: num, SectorTag: 3}})
		return g.activateLine(0, g.playerMobj, trig)
	}
	for _, tc := range []struct {
		num  uint16
		trig lineTrigger
	}{
		{9, trUse},    // S1 donut (approximated)
		{14, trUse},   // S1 raise 32 & change
		{30, trCross}, // W1 raise by shortest lower texture
		{46, trShoot}, // GR open door
		{47, trShoot}, // G1 raise to next & change
		{109, trCross},
		{110, trCross},
		{116, trUse},
	} {
		if !tagged(tc.num, tc.trig) {
			t.Errorf("special %d still does nothing", tc.num)
		}
	}
}

// End-to-end walkover: checkCrossedSpecials uses the collision grid's
// segment walk (post-refactor) — make sure a line the player steps across
// still fires exactly once.
func TestCheckCrossedSpecialsFiresWalkover(t *testing.T) {
	g := specGame(
		[]wad.Sector{{Tag: 2, CeilingHeight: 0}, {Tag: 2, CeilingHeight: 128}},
		[]wad.Linedef{{StartVertex: 0, EndVertex: 1, SpecialType: 2, SectorTag: 2}}, // W1 door open stay, along x-axis 0..64
	)
	g.blockGrid = buildCollisionGrid(g.Level)
	g.playerMobj = &Mobj{Type: MT_PLAYER}

	// Walk from y=-20 to y=+20 at x=32 — straight across the line.
	g.checkCrossedSpecials(32, -20, 32, 20)
	if g.Level.Linedefs[0].SpecialType != 0 {
		t.Error("walkover W1 special didn't fire when the player crossed it")
	}
	if n := len(g.thinkers); n != 2 { // one door mover per tagged sector
		t.Errorf("expected 2 door thinkers after the crossing, got %d", n)
	}
}

func TestLockedSwitchDoorNeedsKey(t *testing.T) {
	newG := func() *Game {
		g := specGame([]wad.Sector{{Tag: 4, CeilingHeight: 0}, {Tag: 4, CeilingHeight: 128}},
			[]wad.Linedef{{SpecialType: 135, SectorTag: 4}}) // S1 red-locked blazing door
		g.playerMobj = &Mobj{Type: MT_PLAYER}
		return g
	}

	g := newG()
	if g.activateLine(0, g.playerMobj, trUse) {
		t.Error("red-locked door opened without the red key")
	}
	if g.Level.Linedefs[0].SpecialType != 135 {
		t.Error("locked S1 door consumed its special on a failed (keyless) use")
	}

	g = newG()
	g.Player.Keys[2] = true // red card
	if !g.activateLine(0, g.playerMobj, trUse) {
		t.Error("red-locked door stayed shut with the red key held")
	}
}
