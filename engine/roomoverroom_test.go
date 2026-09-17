package engine

import (
	"testing"

	"twopointfive/wad"
)

// threeStackGame builds a minimal *Game with three sectors, no WAD/BSP
// needed since planeAt only ever reads g.Level.Sectors/g.extraFloors
// directly by index:
//
//	sector 0 (target):  floor 0,   ceil 200 — the room
//	sector 1 (control):  floor 64,  ceil 72  — a platform floating in it
//	sector 2 (control2): floor 120, ceil 128 — a second, higher platform
func threeStackGame() *Game {
	return &Game{Level: &wad.Level{Sectors: []wad.Sector{
		{FloorHeight: 0, CeilingHeight: 200},
		{FloorHeight: 64, CeilingHeight: 72},
		{FloorHeight: 120, CeilingHeight: 128},
	}}}
}

func TestPlaneAtDegeneratesWithNoExtraFloors(t *testing.T) {
	g := threeStackGame()
	floor, ceil, active := g.planeAt(0, 0, 0, 50)
	if floor != 0 || ceil != 200 || active != 0 {
		t.Errorf("floor=%v ceil=%v active=%v, want 0,200,0", floor, ceil, active)
	}
}

func TestPlaneAtStandingOnPlatformTop(t *testing.T) {
	g := threeStackGame()
	g.extraFloors = map[int][]ExtraFloor{0: {{ControlSector: 1}}}

	// Feet at the platform's top (72): floor should be the platform's top,
	// ceiling the room's own ceiling (nothing above), and the active
	// sector the CONTROL sector (you're standing in its airspace).
	floor, ceil, active := g.planeAt(0, 0, 0, 72)
	if floor != 72 || ceil != 200 || active != 1 {
		t.Errorf("on top: floor=%v ceil=%v active=%v, want 72,200,1", floor, ceil, active)
	}
}

func TestPlaneAtWalkingUnderPlatform(t *testing.T) {
	g := threeStackGame()
	g.extraFloors = map[int][]ExtraFloor{0: {{ControlSector: 1}}}

	// Feet on the room's own floor (0), well below the platform: floor
	// stays the base floor, ceiling becomes the platform's underside (64),
	// and the active sector is still the base (target) sector.
	floor, ceil, active := g.planeAt(0, 0, 0, 0)
	if floor != 0 || ceil != 64 || active != 0 {
		t.Errorf("underneath: floor=%v ceil=%v active=%v, want 0,64,0", floor, ceil, active)
	}
}

func TestPlaneAtStackedPlatformsPicksHighestApplicable(t *testing.T) {
	g := threeStackGame()
	g.extraFloors = map[int][]ExtraFloor{0: {{ControlSector: 1}, {ControlSector: 2}}}

	// Standing on the LOWER platform's top (72): the ceiling must be the
	// HIGHER platform's underside (120), not the room's own ceiling (200)
	// — the closer obstruction wins.
	floor, ceil, active := g.planeAt(0, 0, 0, 72)
	if floor != 72 || ceil != 120 || active != 1 {
		t.Errorf("on lower platform: floor=%v ceil=%v active=%v, want 72,120,1", floor, ceil, active)
	}

	// Standing on the HIGHER platform's top (128): floor is 128, ceiling
	// is the room's own (200), active sector is the higher platform's
	// control sector.
	floor, ceil, active = g.planeAt(0, 0, 0, 128)
	if floor != 128 || ceil != 200 || active != 2 {
		t.Errorf("on higher platform: floor=%v ceil=%v active=%v, want 128,200,2", floor, ceil, active)
	}
}

func TestPlaneAtSkipsClosedControlSector(t *testing.T) {
	g := threeStackGame()
	g.extraFloors = map[int][]ExtraFloor{0: {{ControlSector: 1}}}
	g.Level.Sectors[1].FloorHeight = 100 // now floor (100) > ceiling (72): a closed/degenerate slab

	floor, ceil, active := g.planeAt(0, 0, 0, 50)
	if floor != 0 || ceil != 200 || active != 0 {
		t.Errorf("closed control sector should be ignored: floor=%v ceil=%v active=%v, want 0,200,0", floor, ceil, active)
	}
}

func TestPlaneAtIgnoresOutOfRangeControlSector(t *testing.T) {
	g := threeStackGame()
	g.extraFloors = map[int][]ExtraFloor{0: {{ControlSector: 99}}}

	floor, ceil, active := g.planeAt(0, 0, 0, 50)
	if floor != 0 || ceil != 200 || active != 0 {
		t.Errorf("out-of-range control sector should be ignored: floor=%v ceil=%v active=%v, want 0,200,0", floor, ceil, active)
	}
}
