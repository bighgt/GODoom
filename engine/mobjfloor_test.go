package engine

import (
	"testing"

	"twopointfive/bsp"
)

// TestMobjRidesLoweringFloor: a thing standing in a sector whose floor is
// lowered (a platform/lift, or a crusher) must ride it down instead of
// hanging in the air — id's P_ChangeSector behaviour, which this engine
// approximates by re-seating ground things on their sector floor each tic.
func TestMobjRidesLoweringFloor(t *testing.T) {
	g := loadRealLevel(t, "../testdata/DOOM1.WAD", "E1M1")

	// Find a spot inside a real sector and the sector index for it.
	var sx, sy float64
	var secIdx = -1
	for i := range g.Level.Sectors {
		// centroid of the sector's linedefs
		var cx, cy float64
		var n int
		for j := range g.Level.Linedefs {
			ld := &g.Level.Linedefs[j]
			in := func(s uint16) bool {
				return s != 0xFFFF && int(s) < len(g.Level.Sidedefs) && int(g.Level.Sidedefs[s].Sector) == i
			}
			if !in(ld.FrontSidedef) && !in(ld.BackSidedef) {
				continue
			}
			v1, v2 := g.Level.Vertexes[ld.StartVertex], g.Level.Vertexes[ld.EndVertex]
			cx += float64(v1.X) + float64(v2.X)
			cy += float64(v1.Y) + float64(v2.Y)
			n += 2
		}
		if n == 0 {
			continue
		}
		cx, cy = cx/float64(n), cy/float64(n)
		if s := bsp.PointSector(g.BSP, g.Level, float32(cx), float32(cy)); s == &g.Level.Sectors[i] {
			sx, sy, secIdx = cx, cy, i
			break
		}
	}
	if secIdx < 0 {
		t.Skip("could not locate a testable sector centroid")
	}

	// Spawn a corpse-like decoration (no gravity flag, not a missile) here.
	mo := g.P_SpawnMobj(sx, sy, onFloorZ, MT_GIBS)
	startFloor := g.Level.Sectors[secIdx].FloorHeight
	if int16(mo.Z) != startFloor {
		t.Fatalf("spawned Z=%.0f, expected floor %d", mo.Z, startFloor)
	}
	// Freeze it on a settled frame the way a real corpse ends up.
	mo.Tics = -1

	// Lower the floor 48 units, one step per tic, like a lift.
	for k := 0; k < 48; k++ {
		g.Level.Sectors[secIdx].FloorHeight--
		g.runTic()
	}

	wantFloor := startFloor - 48
	if int16(mo.Z) != wantFloor {
		t.Errorf("after lowering the floor to %d the thing is at Z=%.0f (left floating %d units up)",
			wantFloor, mo.Z, int16(mo.Z)-wantFloor)
	}

	// And a NOGRAVITY floater must NOT be dragged down.
	fl := g.P_SpawnMobj(sx, sy, float64(startFloor)+60, MT_HEAD) // cacodemon: MF_FLOAT|MF_NOGRAVITY
	fl.Tics = -1
	flZ := fl.Z
	for k := 0; k < 10; k++ {
		g.Level.Sectors[secIdx].FloorHeight--
		g.runTic()
	}
	if fl.Z != flZ {
		t.Errorf("a NOGRAVITY floater moved with the floor: Z %.0f -> %.0f", flZ, fl.Z)
	}
}
