package engine

import (
	"testing"

	"twopointfive/bsp"
)

// TestTeleporterRepeats guards the "teleporter only works once" bug: the
// player mobj isn't in g.mobjs, so nothing was counting down its
// post-teleport TeleportFreeze — after the first use evTeleport refused
// every crossing. runTic now decrements it in the player-sync block.
func TestTeleporterRepeats(t *testing.T) {
	g := loadRealLevel(t, "../wad/heretic.wad", "E1M1")

	line := -1
	for i := range g.Level.Linedefs {
		if g.Level.Linedefs[i].SpecialType == 97 { // WR Teleport
			line = i
			break
		}
	}
	if line < 0 {
		t.Skip("no special-97 teleport line on this map")
	}

	if !g.evTeleport(line, g.playerMobj, false) {
		t.Fatal("first teleport did nothing (no reachable MT_TELEPORTMAN?)")
	}
	if g.playerMobj.TeleportFreeze != teleportFreezeTics {
		t.Fatalf("post-teleport freeze = %d, want %d", g.playerMobj.TeleportFreeze, teleportFreezeTics)
	}

	// The immunity must count down on its own.
	for i := 0; i < teleportFreezeTics+2 && g.playerMobj.TeleportFreeze > 0; i++ {
		g.runTic()
	}
	if g.playerMobj.TeleportFreeze != 0 {
		t.Fatalf("freeze stuck at %d after %d tics — it never counts down", g.playerMobj.TeleportFreeze, teleportFreezeTics+2)
	}

	if !g.evTeleport(line, g.playerMobj, false) {
		t.Error("teleporter refused a second use — it only works once")
	}
}

// TestMonsterCrushedByCeiling: a descending ceiling grinds a live monster
// to death (10 dmg / 4 tics) and then flattens the corpse to a smear —
// id's PIT_ChangeSector crush branch, which previously only ran for the
// player.
func TestMonsterCrushedByCeiling(t *testing.T) {
	g := loadRealLevel(t, "../testdata/DOOM1.WAD", "E1M1")

	var sx, sy float64
	for _, th := range g.Level.Things {
		if th.Type == 1 {
			sx, sy = float64(th.X), float64(th.Y)
		}
	}
	sec := bsp.PointSector(g.BSP, g.Level, float32(sx), float32(sy))
	if sec == nil {
		t.Fatal("player start not in a sector")
	}
	sec.FloorHeight, sec.CeilingHeight = 0, 200

	imp := g.P_SpawnMobj(sx, sy, onFloorZ, MT_TROOP)
	imp.Health = 60
	if imp.Flags&MF_SHOOTABLE == 0 {
		t.Fatal("imp not shootable")
	}

	// Close the ceiling right down onto it.
	sec.CeilingHeight = 5

	tics := 0
	for ; tics < 400 && imp.Health > 0; tics++ {
		g.levelTime++
		g.mobjZClip(imp)
	}
	if imp.Health > 0 {
		t.Fatalf("imp not crushed to death after %d tics (health %d)", tics, imp.Health)
	}
	if imp.Flags&MF_CORPSE == 0 {
		t.Error("crushed imp isn't a corpse")
	}

	// A few more grinding tics flatten the corpse.
	for i := 0; i < 4; i++ {
		g.levelTime++
		g.mobjZClip(imp)
	}
	if imp.Height != 0 {
		t.Errorf("ground corpse not flattened: height %v", imp.Height)
	}
	if imp.Flags&MF_SOLID != 0 {
		t.Error("flattened corpse still MF_SOLID")
	}
}

// TestCeilingDoesNotCrushDecorations: a lamp / barrel-less decoration under
// a closed ceiling is shoved down but takes no damage and isn't flattened
// (it's not MF_SHOOTABLE).
func TestCeilingDoesNotCrushDecorations(t *testing.T) {
	g := loadRealLevel(t, "../testdata/DOOM1.WAD", "E1M1")
	var sx, sy float64
	for _, th := range g.Level.Things {
		if th.Type == 1 {
			sx, sy = float64(th.X), float64(th.Y)
		}
	}
	sec := bsp.PointSector(g.BSP, g.Level, float32(sx), float32(sy))
	sec.FloorHeight, sec.CeilingHeight = 0, 200

	col := g.P_SpawnMobj(sx, sy, onFloorZ, MT_COLUMN) // 48-tall solid pillar, not shootable
	h0 := col.Height
	sec.CeilingHeight = 5
	for i := 0; i < 40; i++ {
		g.levelTime++
		g.mobjZClip(col)
	}
	if col.Height != h0 {
		t.Errorf("decoration flattened by a crusher: %v -> %v", h0, col.Height)
	}
}
