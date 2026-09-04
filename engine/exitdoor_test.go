package engine

import (
	"math"
	"testing"

	"twopointfive/bsp"
)

// Doom II MAP01: press use in front of the exit door, then walk through it
// into the exit room. Regression for "the door opens but it's black / you
// can't move in" — actually a door-activation bug: the exit-door line's
// front sidedef belongs to a thin threshold sector, not the room the player
// stands in, so doorSectorForLine's old point-location check opened the
// wrong sector and the door never moved.
func TestDoom2Map01ExitDoorOpensAndWalkThrough(t *testing.T) {
	g := loadRealLevel(t, "../wad/Doom2.wad", "MAP01")

	const doorSec = 37     // the door sector (starts closed: floor == ceil)
	const exitRoomSec = 36 // beyond it

	// Stand in the room just south of the exit door, facing north at it.
	g.Camera.X, g.Camera.Y = 992, 1004
	cur := bsp.PointSector(g.BSP, g.Level, float32(g.Camera.X), float32(g.Camera.Y))
	if cur == nil {
		t.Fatal("player start not in any sector")
	}
	g.Camera.Z = float64(cur.FloorHeight) + EyeHeight
	g.groundPlayer(g.Camera.Z - EyeHeight)
	g.Camera.Angle = math.Pi / 2
	g.playerMobj.X, g.playerMobj.Y, g.playerMobj.Z = g.Camera.X, g.Camera.Y, g.PlayerZ

	if int(g.Level.Sectors[doorSec].CeilingHeight) != int(g.Level.Sectors[doorSec].FloorHeight) {
		t.Fatalf("door sector %d is not closed at start (floor=%d ceil=%d)", doorSec,
			g.Level.Sectors[doorSec].FloorHeight, g.Level.Sectors[doorSec].CeilingHeight)
	}

	g.tryUseLine()

	found := false
	for _, th := range g.thinkers {
		if d, ok := th.(*vldoor); ok && d.sec == doorSec {
			found = true
		}
	}
	if !found {
		t.Fatalf("pressing use did not start a door on sector %d — the exit door never activated", doorSec)
	}

	opened := false
	for i := 0; i < 200 && !opened; i++ {
		g.runTic()
		opened = g.Level.Sectors[doorSec].CeilingHeight >= g.Level.Sectors[exitRoomSec].CeilingHeight-8
	}
	if !opened {
		t.Fatalf("door sector %d ceiling only reached %d", doorSec, g.Level.Sectors[doorSec].CeilingHeight)
	}

	for i := 0; i < 120 && g.Camera.Y < 1120; i++ {
		g.tryMove(0, 6)
		g.resolvePlayerZ(1.0 / 60)
		g.playerMobj.X, g.playerMobj.Y = g.Camera.X, g.Camera.Y
	}
	if g.Camera.Y < 1060 {
		t.Fatalf("player stuck at y=%.0f — could not walk through the open exit door", g.Camera.Y)
	}
	end := bsp.PointSector(g.BSP, g.Level, float32(g.Camera.X), float32(g.Camera.Y))
	ei := -1
	for i := range g.Level.Sectors {
		if &g.Level.Sectors[i] == end {
			ei = i
		}
	}
	if ei != exitRoomSec && ei != doorSec {
		t.Errorf("player ended in sector %d, want the door (%d) or exit room (%d)", ei, doorSec, exitRoomSec)
	}
}

// DOOM1 E1M1: the last door before the exit switch is sector 81, a
// two-linedef door (lines 324 & 325) whose BACK sidedef is the door sector
// on BOTH lines. id's EV_VerticalDoor always opens the back sidedef's
// sector; a player-side heuristic that used to live in doorSectorForLine
// opened the FRONT sector (84) when the used line was hit from its front
// side, so the real door never moved ("the last door didn't open").
func TestDoom1E1M1LastDoorSectorSelection(t *testing.T) {
	g := loadRealLevel(t, "../testdata/DOOM1.WAD", "E1M1")

	const doorSec, frontOf324, frontOf325 = 81, 84, 80

	find := func(a, b int) int {
		for i := range g.Level.Linedefs {
			ld := &g.Level.Linedefs[i]
			if ld.SpecialType != 1 || ld.BackSidedef == 0xFFFF {
				continue
			}
			fs := int(g.Level.Sidedefs[ld.FrontSidedef].Sector)
			bs := int(g.Level.Sidedefs[ld.BackSidedef].Sector)
			if fs == a && bs == b {
				return i
			}
		}
		return -1
	}
	l324, l325 := find(frontOf324, doorSec), find(frontOf325, doorSec)
	if l324 < 0 || l325 < 0 {
		t.Skipf("E1M1 door lines not found (l324=%d l325=%d) — WAD layout differs", l324, l325)
	}

	// Both linedefs of the door must resolve to the door sector regardless
	// of where the player stands (the two candidate positions bracket line
	// 324 north/south).
	for _, tc := range []struct {
		name string
		line int
		x, y float64
	}{
		{"324 from north", l324, 3008, -4600},
		{"324 from south", l324, 3008, -4700},
		{"325 from north", l325, 3008, -4600},
	} {
		g.Camera.X, g.Camera.Y = tc.x, tc.y
		if got := g.doorSectorForLine(&g.Level.Linedefs[tc.line]); got != doorSec {
			t.Errorf("%s: doorSectorForLine = %d, want the door sector %d", tc.name, got, doorSec)
		}
	}
}

// End to end: use the door from the corridor and watch it open to
// player height.
func TestDoom1E1M1LastDoorOpens(t *testing.T) {
	g := loadRealLevel(t, "../testdata/DOOM1.WAD", "E1M1")
	const doorSec = 81

	if int(g.Level.Sectors[doorSec].CeilingHeight) != int(g.Level.Sectors[doorSec].FloorHeight) {
		t.Fatalf("door sector %d not closed at start (floor=%d ceil=%d)", doorSec,
			g.Level.Sectors[doorSec].FloorHeight, g.Level.Sectors[doorSec].CeilingHeight)
	}

	g.Camera.X, g.Camera.Y = 3008, -4616
	g.Camera.Angle = -math.Pi / 2 // south (-Y), at the door
	if cur := bsp.PointSector(g.BSP, g.Level, float32(g.Camera.X), float32(g.Camera.Y)); cur != nil {
		g.Camera.Z = float64(cur.FloorHeight) + EyeHeight
	}
	g.playerMobj.X, g.playerMobj.Y = g.Camera.X, g.Camera.Y

	g.tryUseLine()

	var d *vldoor
	for _, th := range g.thinkers {
		if v, ok := th.(*vldoor); ok {
			if v.sec != doorSec {
				t.Fatalf("use started a door on sector %d, not the door sector %d", v.sec, doorSec)
			}
			d = v
		}
	}
	if d == nil {
		t.Fatal("pressing use started no door — E1M1's last door never activated")
	}
	if d.top <= float64(g.Level.Sectors[doorSec].FloorHeight) {
		t.Fatalf("door top %.0f at/below the floor — it would not open", d.top)
	}

	opened := false
	for i := 0; i < 200 && !opened; i++ {
		g.runTic()
		s := &g.Level.Sectors[doorSec]
		opened = s.CeilingHeight-s.FloorHeight >= 56
	}
	if !opened {
		t.Fatalf("door sector %d never opened to player height", doorSec)
	}
}
