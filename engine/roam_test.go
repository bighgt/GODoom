package engine

import (
	"math"
	"testing"

	"twopointfive/bsp"
	"twopointfive/wad"
)

// roamRun hides the player (no monster can notice a dead one), runs the
// level for secs simulated seconds, and reports how many roam-capable
// monsters got well away from their spawn point at some time during the
// run / ended in another sector, and how many doors monsters opened. Every
// monster must end somewhere valid.
func roamRun(t *testing.T, g *Game, secs int) (roamers, moved, changedSector, doorOpens int) {
	t.Helper()
	prng.Seed(0x7eed) // trips are random; don't depend on what earlier tests drew
	g.Player.Health = 0
	type spawn struct {
		x, y float64
		sec  int
	}
	start := map[*Mobj]spawn{}
	for _, mo := range g.mobjs {
		if canRoam(mo) {
			start[mo] = spawn{mo.X, mo.Y, bsp.PointSectorIndex(g.BSP, g.Level, float32(mo.X), float32(mo.Y))}
		}
	}
	doors := func() int {
		n := 0
		for _, th := range g.thinkers {
			if _, ok := th.(*vldoor); ok {
				n++
			}
		}
		return n
	}
	farthest := map[*Mobj]float64{}
	for tic := 0; tic < 35*secs; tic++ {
		before := doors()
		g.runTic()
		if after := doors(); after > before {
			doorOpens += after - before
		}
		for mo, s := range start {
			farthest[mo] = math.Max(farthest[mo], math.Hypot(mo.X-s.x, mo.Y-s.y))
		}
	}
	for mo, s := range start {
		if farthest[mo] > 128 { // got well away at some point (it may have wandered back)
			moved++
		}
		if bsp.PointSectorIndex(g.BSP, g.Level, float32(mo.X), float32(mo.Y)) != s.sec {
			changedSector++
		}
		if mo.Health <= 0 {
			continue // a corpse (a crushed one is flattened, radius 0) needn't fit anywhere
		}
		var res checkPosResult
		flags := mo.Flags
		mo.Flags &^= MF_SOLID // its own body isn't an obstacle to itself
		ok := g.pCheckPosition(mo, mo.X, mo.Y, &res)
		mo.Flags = flags // exactly as it was — never make anything solid that wasn't
		if !ok {
			sec := bsp.PointSectorIndex(g.BSP, g.Level, float32(mo.X), float32(mo.Y))
			sc := g.Level.Sectors[sec]
			var near []string
			for _, o := range g.mobjs {
				if o != mo && o.Flags&MF_SOLID != 0 && math.Abs(o.X-mo.X) < o.Radius+mo.Radius && math.Abs(o.Y-mo.Y) < o.Radius+mo.Radius {
					near = append(near, o.Info.Name)
				}
			}
			t.Errorf("%s ended at (%.0f,%.0f) z=%.0f in sector %d (floor %d ceil %d, door=%v), overlapping %v — inside a wall or another monster",
				mo.Info.Name, mo.X, mo.Y, mo.Z, sec, sc.FloorHeight, sc.CeilingHeight, g.navGraphFor().door[sec], near)
		}
	}
	for mo := range start {
		if mo.Health <= 0 {
			t.Errorf("%s died with nobody around — crushed by a door it was walking through?", mo.Info.Name)
		}
	}
	return len(start), moved, changedSector, doorOpens
}

// TestIdleMonstersRoam: with nobody to notice, monsters don't stand still —
// in two simulated minutes most of E1M1's leave their spawn area and move
// into other rooms, and they open doors to do it. On Doom II MAP01 the ones
// that stay are those that truly can't leave (high sniper ledges they won't
// step off, closets behind switch-operated doors).
func TestIdleMonstersRoam(t *testing.T) {
	g := loadRealLevel(t, "../testdata/DOOM1.WAD", "E1M1")
	g.monsterRoaming = true
	roamers, moved, changed, doors := roamRun(t, g, 120)
	t.Logf("E1M1: %d roamers, %d moved, %d changed sector, %d door openings", roamers, moved, changed, doors)
	if roamers == 0 || moved*10 < roamers*7 {
		t.Errorf("E1M1: only %d of %d roam-capable monsters moved", moved, roamers)
	}
	if changed == 0 || doors == 0 {
		t.Errorf("E1M1: monsters changed sector %d times and opened %d doors — want both > 0", changed, doors)
	}

	if _, err := wad.Load("../wad/Game WADs/doom2.wad"); err != nil {
		return
	}
	g = loadRealLevel(t, "../wad/Game WADs/doom2.wad", "MAP01")
	g.monsterRoaming = true
	roamers, moved, changed, doors = roamRun(t, g, 120)
	t.Logf("MAP01: %d roamers, %d moved, %d changed sector, %d door openings", roamers, moved, changed, doors)
	if moved*2 < roamers || doors == 0 {
		t.Errorf("MAP01: %d of %d moved, %d doors opened", moved, roamers, doors)
	}
}

// TestClassicMonstersWait: config monsterBehavior "classic" is vanilla —
// with nobody to notice, no monster leaves its spot or opens a door.
func TestClassicMonstersWait(t *testing.T) {
	g := loadRealLevel(t, "../testdata/DOOM1.WAD", "E1M1")
	g.monsterRoaming = false
	roamers, moved, changed, doors := roamRun(t, g, 120)
	if roamers == 0 {
		t.Fatal("E1M1: no roam-capable monsters to test with")
	}
	if moved != 0 || changed != 0 || doors != 0 {
		t.Errorf("classic: %d of %d monsters moved, %d sector changes, %d doors opened — want none", moved, roamers, changed, doors)
	}
}

// TestMonsterDoorRules: monsters open plain doors that close behind them
// (DR 1, blazing DR 117) and nothing else — not key-locked doors, not
// open-and-stay doors, not switch/trigger doors, not doors on a line
// marked secret.
func TestMonsterDoorRules(t *testing.T) {
	cases := []struct {
		special, flags uint16
		want           bool
	}{
		{1, 0, true}, {117, 0, true},
		{26, 0, false}, {27, 0, false}, {28, 0, false}, // key-locked DR
		{32, 0, false}, {33, 0, false}, {34, 0, false}, // key-locked D1
		{31, 0, false}, {118, 0, false}, // open and stay
		{2, 0, false}, {63, 0, false}, // walk-over / switch remote doors
		{1, mlSecret, false}, // secret door
		{0, 0, false},
	}
	for _, c := range cases {
		ld := &wad.Linedef{SpecialType: c.special, Flags: c.flags}
		if got := monsterCanOpen(ld); got != c.want {
			t.Errorf("special %d flags %#x: monsterCanOpen = %v, want %v", c.special, c.flags, got, c.want)
		}
	}
}

// TestRoamingMonsterNoticesAndChases: a roaming monster that sees the player
// drops its trip and chases; one that's shot mid-trip does too (the damage
// code hands it the shooter as its target).
func TestRoamingMonsterNoticesAndChases(t *testing.T) {
	g := loadRealLevel(t, "../testdata/DOOM1.WAD", "E1M1")
	g.monsterRoaming = true
	var mo *Mobj
	for _, m := range g.mobjs {
		if canRoam(m) && g.planRoam(m) != nil { // one that has somewhere to go
			mo = m
			break
		}
	}
	if mo == nil {
		t.Fatal("no roam-capable monster on E1M1")
	}
	g.playerMobj.Health, g.Player.Health = 0, 0 // unseen while the trip starts
	g.idleRoamStart(mo)
	if !mo.roaming {
		t.Fatal("test setup: monster didn't start roaming")
	}
	// Stand right in front of it.
	g.Camera.X, g.Camera.Y = mo.X+40*math.Cos(mo.Angle), mo.Y+40*math.Sin(mo.Angle)
	g.playerMobj.X, g.playerMobj.Y, g.playerMobj.Health = g.Camera.X, g.Camera.Y, 100
	aChase(g, mo)
	if mo.roaming || mo.Target != g.playerMobj {
		t.Errorf("seen player: roaming=%v target=%v, want chasing the player", mo.roaming, mo.Target)
	}

	// Shot from behind, out of sight.
	g.playerMobj.Health = 0 // can't be seen...
	g.idleRoamStart(mo)
	if !mo.roaming {
		t.Fatal("test setup: monster didn't restart roaming")
	}
	shooter := &Mobj{Type: MT_POSSESSED, Info: &mobjInfo[MT_POSSESSED], Health: 20, Flags: MF_SHOOTABLE}
	mo.Target = shooter // ...but P_DamageMobj names the attacker
	aChase(g, mo)
	if mo.roaming {
		t.Error("shot mid-trip: still roaming, want chasing the attacker")
	}
}

// idleRoamStart puts mo straight into a trip (test helper — skips the
// idle pause).
func (g *Game) idleRoamStart(mo *Mobj) {
	mo.roamReadyAt = 1
	g.levelTime = max(g.levelTime, 2)
	g.idleRoam(mo)
}

// TestDoorReopensOnThingInDoorway: a normal door closing on a monster —
// or on the player — standing in the doorway goes back up instead of
// crushing them (id's T_VerticalDoor closes with crush off). Before, doors
// ground whoever stood under them, which with monsters roaming through
// doorways killed them in droves.
func TestDoorReopensOnThingInDoorway(t *testing.T) {
	g := loadRealLevel(t, "../testdata/DOOM1.WAD", "E1M1")
	door := -1
	for i := range g.Level.Linedefs {
		if ld := &g.Level.Linedefs[i]; monsterCanOpen(ld) {
			door = g.doorSectorForLine(ld)
			break
		}
	}
	if door < 0 {
		t.Fatal("no monster-openable door on E1M1")
	}
	// A point inside the door sector: its first subsector's centroid.
	pts := g.navGraphFor().spots[door]
	if len(pts) == 0 {
		t.Fatal("door sector has no interior point")
	}
	x, y := pts[0][0], pts[0][1]

	for _, who := range []string{"monster", "player"} {
		// The door fully open and about to close (end of its wait).
		g.thinkers = nil
		g.sectorActive[door] = false // the previous case's door thinker is gone
		g.startDoor(door, dkOpenWaitClose)
		for _, th := range g.thinkers {
			if d, ok := th.(*vldoor); ok && d.sec == door {
				g.Level.Sectors[door].CeilingHeight = int16(d.top)
				d.dir, d.count = 0, 5
			}
		}
		var mo *Mobj
		if who == "monster" {
			mo = g.P_SpawnMobj(x, y, float64(g.Level.Sectors[door].FloorHeight), MT_TROOP)
			g.Player.Health = 0 // out of the way
		} else {
			g.Camera.X, g.Camera.Y = x, y
			g.PlayerZ = float64(g.Level.Sectors[door].FloorHeight)
			g.Player.Health, g.playerDead = 100, false
		}
		// Open, wait, try to close on them, and some more.
		sawReopen := false
		for tic := 0; tic < 35*6; tic++ {
			if mo != nil {
				mo.X, mo.Y = x, y // stands still in the doorway
			}
			var dir int
			for _, th := range g.thinkers {
				if d, ok := th.(*vldoor); ok && d.sec == door {
					dir = d.dir
				}
			}
			g.runTic()
			for _, th := range g.thinkers {
				if d, ok := th.(*vldoor); ok && d.sec == door && dir == -1 && d.dir == 1 {
					sawReopen = true
				}
			}
		}
		if !sawReopen {
			t.Errorf("%s in the doorway: the closing door never went back up", who)
		}
		if who == "monster" && mo.Health <= 0 {
			t.Error("the door crushed the monster standing in it")
		}
		if who == "player" && (g.playerDead || g.Player.Health <= 0) {
			t.Error("the door crushed the player standing in it")
		}
		if mo != nil {
			g.removeMobj(mo)
		}
	}
}
