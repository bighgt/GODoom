package engine

import (
	"testing"

	"twopointfive/bsp"
	"twopointfive/wad"
)

// threeSectorGame: sectors 0 - 1 - 2 in a row, joined by two-sided lines
// B (0<->1) and C (1<->2), every sector 0..128 tall (open).
func threeSectorGame() *Game {
	g := &Game{Level: &wad.Level{
		Sectors: []wad.Sector{
			{FloorHeight: 0, CeilingHeight: 128},
			{FloorHeight: 0, CeilingHeight: 128},
			{FloorHeight: 0, CeilingHeight: 128},
		},
		Sidedefs: []wad.Sidedef{
			{Sector: 0}, {Sector: 1}, // 0,1: line B front/back
			{Sector: 1}, {Sector: 2}, // 2,3: line C front/back
		},
		Linedefs: []wad.Linedef{
			{FrontSidedef: 0, BackSidedef: 1}, // B
			{FrontSidedef: 2, BackSidedef: 3}, // C
		},
	}}
	g.buildSectorIndex()
	return g
}

func TestNoiseFloodReachesConnectedSectors(t *testing.T) {
	g := threeSectorGame()
	em := &Mobj{Flags: MF_SHOOTABLE}
	g.noiseFloodFrom(0, em)
	for s := 0; s < 3; s++ {
		if g.soundTarget[s] != em {
			t.Errorf("sector %d soundTarget = %v, want the emitter", s, g.soundTarget[s])
		}
	}
}

func TestNoiseFloodStopsAtClosedDoor(t *testing.T) {
	g := threeSectorGame()
	g.Level.Sectors[2].FloorHeight = 128 // sector 2 shut: no vertical opening at line C
	em := &Mobj{Flags: MF_SHOOTABLE}
	g.noiseFloodFrom(0, em)
	if g.soundTarget[0] != em || g.soundTarget[1] != em {
		t.Fatalf("sound should still reach sectors 0 and 1")
	}
	if g.soundTarget[2] != nil {
		t.Errorf("sound leaked through a closed door into sector 2")
	}
}

func TestNoiseFloodOneSoundBlockLinePasses(t *testing.T) {
	g := threeSectorGame()
	g.Level.Linedefs[0].Flags = wad.LinedefBlockSound // B blocks sound (crossed once = ok)
	em := &Mobj{Flags: MF_SHOOTABLE}
	g.noiseFloodFrom(0, em)
	for s := 0; s < 3; s++ {
		if g.soundTarget[s] != em {
			t.Errorf("sector %d not reached; one ML_SOUNDBLOCK line should still pass", s)
		}
	}
}

func TestNoiseFloodTwoSoundBlockLinesStop(t *testing.T) {
	g := threeSectorGame()
	g.Level.Linedefs[0].Flags = wad.LinedefBlockSound
	g.Level.Linedefs[1].Flags = wad.LinedefBlockSound
	em := &Mobj{Flags: MF_SHOOTABLE}
	g.noiseFloodFrom(0, em)
	if g.soundTarget[0] != em || g.soundTarget[1] != em {
		t.Fatalf("sound should reach sectors 0 and 1")
	}
	if g.soundTarget[2] != nil {
		t.Errorf("sound crossed two ML_SOUNDBLOCK lines into sector 2")
	}
}

func TestNoiseFloodPersistsAcrossAlerts(t *testing.T) {
	g := threeSectorGame()
	a := &Mobj{Flags: MF_SHOOTABLE}
	g.noiseFloodFrom(0, a)
	// A later alert that a shut door keeps out of sector 0 must not wipe
	// sector 0's earlier target (id never clears soundtarget).
	g.Level.Sectors[1].FloorHeight = 128 // shut B's and C's shared openings
	b := &Mobj{Flags: MF_SHOOTABLE}
	g.noiseFloodFrom(2, b)
	if g.soundTarget[0] != a {
		t.Errorf("sector 0 soundTarget = %v, want the first emitter (should persist)", g.soundTarget[0])
	}
	if g.soundTarget[2] != b {
		t.Errorf("sector 2 soundTarget = %v, want the second emitter", g.soundTarget[2])
	}
}

func TestHeardShotDeafNeedsSight(t *testing.T) {
	g := &Game{} // no level -> pCheckSight is a permissive stub; fine, we drive it via flags
	shooter := &Mobj{Flags: MF_SHOOTABLE}

	// Non-deaf: wakes on the sound alone.
	m := &Mobj{}
	if !g.heardShot(m, shooter) || m.Target != shooter {
		t.Errorf("a hearing monster did not wake to the shot")
	}
	// nil / non-shootable target: never.
	if g.heardShot(&Mobj{}, nil) {
		t.Errorf("heardShot(nil) woke a monster")
	}
	if g.heardShot(&Mobj{}, &Mobj{}) {
		t.Errorf("heardShot woke a monster to a non-shootable emitter")
	}
	// Deaf with LOS (permissive stub sight): wakes. Deaf without LOS is
	// covered by the real-level test below.
	d := &Mobj{Flags: MF_AMBUSH}
	if !g.heardShot(d, shooter) {
		t.Errorf("a deaf monster with line of sight did not wake")
	}
}

// placePlayerAtStart puts playerMobj + g.Camera on the map's Player-1 start.
func placePlayerAtStart(t *testing.T, g *Game) (float64, float64) {
	t.Helper()
	for _, th := range g.Level.Things {
		if th.Type != 1 {
			continue
		}
		x, y := float64(th.X), float64(th.Y)
		f, _ := g.sectorFloorCeil(x, y)
		g.playerMobj.X, g.playerMobj.Y, g.playerMobj.Z = x, y, f
		g.Camera.X, g.Camera.Y, g.Camera.Z = x, y, f+EyeHeight
		return x, y
	}
	t.Skip("no Player-1 start on this map")
	return 0, 0
}

// A monster facing away from the player, in the player's own sector but
// beyond melee range, does not notice the player on its own - a gunshot's
// noise wakes it.
func TestALookWakesToOffscreenShot(t *testing.T) {
	g := loadRealLevel(t, "../testdata/DOOM1.WAD", "E1M1")
	px, py := placePlayerAtStart(t, g)

	imp := g.P_SpawnMobj(px+80, py, onFloorZ, MT_TROOP)
	imp.Angle = 0 // faces +X; the player is at -X of it (behind its back)
	imp.Target = nil
	g.setMobjState(imp, imp.Info.SpawnState)

	// Clear LOS, but the player is behind its back and out of melee range,
	// so plain A_Look must NOT wake it.
	aLook(g, imp)
	if imp.Target != nil {
		t.Fatalf("A_Look woke a monster that only had the player behind its back")
	}

	g.pNoiseAlert(g.playerMobj) // the player fires
	aLook(g, imp)
	if imp.Target != g.playerMobj {
		t.Fatalf("A_Look did not wake the monster to the gunshot (target=%v)", imp.Target)
	}
	if imp.State == imp.Info.SpawnState {
		t.Errorf("monster acquired the target but stayed in its idle state")
	}
}

// A deaf (MF_AMBUSH) monster in a sector the shot's sound reaches, but with
// no line of sight to the shooter, stays asleep.
func TestALookDeafIgnoresShotOutOfSight(t *testing.T) {
	g := loadRealLevel(t, "../testdata/DOOM1.WAD", "E1M1")
	px, py := placePlayerAtStart(t, g)
	ps := bsp.PointSectorIndex(g.BSP, g.Level, float32(px), float32(py))
	if ps < 0 {
		t.Skip("player start sector unresolved")
	}

	// A neighbouring sector whose interior the player genuinely can't see,
	// and whose centroid really lands in that sector.
	var tx, ty float64
	found := false
	for _, other := range g.sectorNeighbors[ps] {
		cx, cy, ok := g.sectorCentroid(int(other))
		if !ok || bsp.PointSectorIndex(g.BSP, g.Level, float32(cx), float32(cy)) != int(other) {
			continue
		}
		if g.pCheckSight(g.playerMobj, &Mobj{X: cx, Y: cy, Height: 56}) {
			continue // player can see in there - not the case we want
		}
		tx, ty, found = cx, cy, true
		break
	}
	if !found {
		t.Skip("no out-of-sight neighbouring sector on this map")
	}

	mk := func(deaf bool) *Mobj {
		m := g.P_SpawnMobj(tx, ty, onFloorZ, MT_TROOP)
		if deaf {
			m.Flags |= MF_AMBUSH
		}
		g.setMobjState(m, m.Info.SpawnState)
		return m
	}
	hearing, deafMon := mk(false), mk(true)

	g.pNoiseAlert(g.playerMobj)
	aLook(g, hearing)
	aLook(g, deafMon)

	if hearing.State == hearing.Info.SpawnState {
		t.Errorf("a hearing monster in a sound-reached sector did not wake")
	}
	if deafMon.State != deafMon.Info.SpawnState {
		t.Errorf("a deaf monster woke to a shot it could not see (state %d, want %d)",
			deafMon.State, deafMon.Info.SpawnState)
	}
}
