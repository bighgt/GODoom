package engine

import (
	"os"
	"testing"

	"twopointfive/assets"
	"twopointfive/bsp"
	"twopointfive/config"
	"twopointfive/raster"
	"twopointfive/wad"
)

func hereticGame(t *testing.T, mapName string) *Game {
	t.Helper()
	const path = "../wad/heretic.wad"
	if _, err := os.Stat(path); err != nil {
		t.Skipf("no %s", path)
	}
	w, err := wad.Load(path)
	if err != nil {
		t.Fatalf("wad.Load: %v", err)
	}
	if !w.IsHeretic() {
		t.Fatalf("IsHeretic() = false for %s", path)
	}
	lvl, err := w.LoadLevel(mapName)
	if err != nil {
		t.Fatalf("LoadLevel(%s): %v", mapName, err)
	}
	tree, err := bsp.Build(lvl)
	if err != nil {
		t.Fatalf("bsp.Build: %v", err)
	}
	return NewGame(nil, nil, nil, w, lvl, tree, raster.Camera{}, config.Config{Skill: 3})
}

func TestHereticThingsSpawn(t *testing.T) {
	g := hereticGame(t, "E1M1")
	if !g.heretic {
		t.Fatal("g.heretic not set")
	}

	bySprite := map[string]int{}
	var yellowKey *Mobj
	for _, mo := range g.mobjs {
		if mo.spriteOverride == "" {
			continue
		}
		bySprite[mo.spriteOverride]++
		if mo.spriteOverride == "AKYY" {
			yellowKey = mo
		}
	}
	t.Logf("Heretic things by sprite: %v", bySprite)

	// E1M1 places exactly one yellow key (doomednum 80).
	if yellowKey == nil {
		t.Fatal("no AKYY (yellow key) mobj spawned on E1M1")
	}
	if yellowKey.hereticGift != hgKeyYellow {
		t.Errorf("yellow key gift = %d, want hgKeyYellow", yellowKey.hereticGift)
	}
	if yellowKey.Flags&MF_SPECIAL == 0 {
		t.Error("yellow key not MF_SPECIAL (can't be picked up)")
	}

	// Common E1M1 decorations must be present with the right Heretic sprite
	// (they'd otherwise be Doom gibs / hanging bodies / nothing).
	for _, want := range []string{"SRTC" /*serpent torch*/, "WTRH" /*wall torch*/, "SKH1" /*skull*/} {
		if bySprite[want] == 0 {
			t.Errorf("expected at least one %s decoration on E1M1, got none", want)
		}
	}
}

func TestHereticKeyOpensLockedDoor(t *testing.T) {
	g := hereticGame(t, "E1M1")

	// Find the E1M1 yellow-key manual door (Heretic special 34) and the
	// sector it drives.
	var lineIdx, doorSec = -1, -1
	for i := range g.Level.Linedefs {
		if g.Level.Linedefs[i].SpecialType == 34 {
			lineIdx = i
			doorSec = g.doorSectorForLine(&g.Level.Linedefs[i])
			break
		}
	}
	if lineIdx < 0 {
		t.Skip("E1M1 has no special-34 door")
	}
	closedH := g.Level.Sectors[doorSec].CeilingHeight

	// Without the key: refused.
	if g.activateLine(lineIdx, g.playerMobj, trUse) {
		t.Fatal("keyed door opened without the key")
	}

	// Pick up the yellow key, then it opens.
	got, _ := g.giveHeretic(hgKeyYellow)
	if !got || !g.Player.Keys[1] {
		t.Fatalf("giveHeretic(hgKeyYellow): got=%v Keys[1]=%v", got, g.Player.Keys[1])
	}
	if !g.activateLine(lineIdx, g.playerMobj, trUse) {
		t.Fatal("keyed door did not open with the yellow key held")
	}
	// A vldoor thinker should now be raising doorSec.
	raising := false
	for _, th := range g.thinkers {
		if d, ok := th.(*vldoor); ok && d.sec == doorSec && d.dir == 1 {
			raising = true
		}
	}
	if !raising {
		t.Fatalf("no door thinker raising sector %d (closed ceiling %d)", doorSec, closedH)
	}
}

func TestHereticMonstersSpawnFightAndDie(t *testing.T) {
	g := hereticGame(t, "E1M1")

	var mons []*Mobj
	byName := map[string]int{}
	for _, mo := range g.mobjs {
		if mo.hact == nil {
			continue
		}
		mons = append(mons, mo)
		byName[mo.hact.name]++
		if mo.Flags&(MF_SHOOTABLE|MF_COUNTKILL|MF_SOLID) != (MF_SHOOTABLE | MF_COUNTKILL | MF_SOLID) {
			t.Errorf("%s spawned without the monster flag set (flags %#x)", mo.hact.name, mo.Flags)
		}
		if mo.Info == nil || mo.Info.SpawnHealth != mo.hact.health {
			t.Errorf("%s: synthesized MobjInfo not wired (health %v want %d)", mo.hact.name, mo.Info, mo.hact.health)
		}
	}
	t.Logf("E1M1 Heretic monsters: %v", byName)
	if len(mons) < 5 {
		t.Fatalf("only %d Heretic monsters spawned on E1M1, expected several", len(mons))
	}

	// Advance the sim a few tics — the FSM must not panic and idle monsters
	// must stay put.
	for i := 0; i < 40; i++ {
		g.runTic()
	}

	// Pick a golem and kill it: it should go to pain at least once on the
	// way down, then end up a non-shootable corpse.
	var victim *Mobj
	for _, mo := range mons {
		if mo.hact.name == "golem" || mo.hact.name == "gargoyle" {
			victim = mo
			break
		}
	}
	if victim == nil {
		victim = mons[0]
	}
	sawPain := false
	for n := 0; n < 200 && victim.Health > 0; n++ {
		g.pDamageMobj(victim, nil, g.playerMobj, 7)
		if victim.hPhase == hpPain {
			sawPain = true
		}
		g.runTic()
	}
	if victim.Health > 0 {
		t.Fatalf("%s survived 200 hits of 7 (health still %d)", victim.hact.name, victim.Health)
	}
	// run out its death animation
	for i := 0; i < 60; i++ {
		g.runTic()
	}
	if victim.Flags&MF_SHOOTABLE != 0 {
		t.Errorf("dead %s is still shootable", victim.hact.name)
	}
	if victim.hPhase != hpDeath && victim.hPhase != hpXDeath {
		t.Errorf("dead %s not in a death phase (%d)", victim.hact.name, victim.hPhase)
	}
	if !sawPain {
		t.Logf("note: %s never entered pain (painchance/RNG) — not fatal", victim.hact.name)
	}
	if g.killCount < 1 {
		t.Errorf("killCount not incremented (%d)", g.killCount)
	}
}

func TestHereticMonsterChasesAndHitsPlayer(t *testing.T) {
	g := hereticGame(t, "E1M1")
	g.Player.Armor = 0

	// Drop a golem right on top of the player and point it at them.
	px, py := g.Camera.X, g.Camera.Y
	if !g.spawnHereticMonster(68 /*golem*/, px+40, py, 180, false) {
		t.Fatal("couldn't spawn a golem")
	}
	mon := g.mobjs[len(g.mobjs)-1]
	mon.Target = g.playerMobj
	mon.Threshold = 100
	g.setMobjState(mon, mon.Info.SeeState) // wake it

	startHP := g.Player.Health
	tookMelee := false
	for i := 0; i < 210 && g.Player.Health == startHP; i++ {
		g.runTic()
		if mon.hPhase == hpMelee {
			tookMelee = true
		}
	}
	if !tookMelee {
		t.Error("golem never entered its melee phase next to the player")
	}
	if g.Player.Health >= startHP {
		t.Fatalf("player took no damage from an adjacent golem over 6s (hp %d)", g.Player.Health)
	}
	t.Logf("golem clawed the player for %d over %d hp", startHP-g.Player.Health, startHP)
}

func TestHereticSpecial99IsInertScroller(t *testing.T) {
	// Heretic reuses special 99 (Doom's "SR blue-key blazing door") for a
	// passive scrolling wall. Pressing Space at one must do NOTHING — the
	// Doom path would have doorTagged(tag=0, ...) fling every untagged
	// sector open.
	g := hereticGame(t, "E3M4") // E3M4 has many special-99 walls
	li := -1
	for i := range g.Level.Linedefs {
		if g.Level.Linedefs[i].SpecialType == 99 {
			li = i
			break
		}
	}
	if li < 0 {
		t.Skip("E3M4 has no special-99 line")
	}
	before := len(g.thinkers)
	if g.activateLine(li, g.playerMobj, trUse) {
		t.Fatal("special-99 wall reported as an activated trigger")
	}
	if len(g.thinkers) != before {
		t.Fatalf("special-99 use spawned %d thinkers (a door/floor flew open)", len(g.thinkers)-before)
	}
	// It should have been registered as a passive scroller at level load.
	scrollers := 0
	for _, th := range g.thinkers {
		if _, ok := th.(*scroller); ok {
			scrollers++
		}
	}
	if scrollers == 0 {
		t.Error("no scroller thinkers — Heretic special 99 walls aren't animating")
	}
}

func TestHereticVoxelPackLoads(t *testing.T) {
	const path = "../assets/voxel/reikallhereticvoxels.pk3"
	if _, err := os.Stat(path); err != nil {
		t.Skipf("no %s", path)
	}
	vs, err := assets.LoadVoxelPack(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// reikall's VOXELDEF declares ~60 sprite frames directly; the rest of
	// the pack's models are wired through its DECORATE, which this engine
	// doesn't parse (those things fall back to their Heretic sprite).
	if n := vs.Frames(); n < 50 {
		t.Errorf("only %d voxel frames mapped, expected >=50", n)
	}
	// The keys use a bare 4-letter VOXELDEF key ("akyy" etc.) — the
	// whole-sprite fallback in VoxelSet.Model must resolve frame 0.
	for _, s := range []string{"AKYY", "BKYY", "CKYY"} {
		if m, _, _, ok := vs.Model(s, 0); !ok || m == nil {
			t.Errorf("no voxel model for key sprite %s frame 0", s)
		}
	}
	// And a normal per-frame entry.
	if _, _, _, ok := vs.Model("SRTC", 3); !ok {
		t.Errorf("no voxel model for SRTC frame 3 (serpent torch)")
	}
}

// TestHereticSimStability runs a long simulation of several Heretic maps
// (all monsters woken and infighting) to shake out FSM edge cases — a
// missing animation frame, a nil deref, a phase that never terminates.
func TestHereticSimStability(t *testing.T) {
	for _, m := range []string{"E1M1", "E1M4", "E1M8", "E2M2", "E2M8", "E3M4", "E3M8", "E4M1", "E4M8", "E5M2"} {
		g := hereticGame(t, m)
		// wake everything and set it fighting the player
		for _, mo := range g.mobjs {
			if mo.hact == nil {
				continue
			}
			mo.Target = g.playerMobj
			mo.Threshold = 100
			g.setMobjState(mo, mo.Info.SeeState)
		}
		for i := 0; i < 700; i++ { // 20 seconds
			g.runTic()
		}
		// nobody should be wedged in an impossible state
		for _, mo := range g.mobjs {
			if mo.hact == nil || mo.removed {
				continue
			}
			if mo.hFrameIdx < 0 || mo.hPhase < hpSpawn || mo.hPhase > hpXDeath {
				t.Errorf("%s: %s in bad state phase=%d idx=%d", m, mo.hact.name, mo.hPhase, mo.hFrameIdx)
			}
		}
		t.Logf("%s: %d tics OK, %d mobjs live, %d kills", m, 700, len(g.mobjs), g.killCount)
	}
}

func TestHereticWeaponsAndArtifacts(t *testing.T) {
	g := hereticGame(t, "E1M1")

	// fresh Heretic loadout: staff + wand, 50 wand crystals, Heretic caps
	if !g.Player.Weapons[wpFist] || !g.Player.Weapons[wpPistol] {
		t.Error("Heretic start doesn't own staff + wand")
	}
	if g.Player.Ammo[0] != 50 || g.Player.MaxAmmo[4] != 20 || g.Player.MaxAmmo[5] != 150 {
		t.Errorf("Heretic ammo wrong: %v / caps %v", g.Player.Ammo, g.Player.MaxAmmo)
	}
	if Weapons[wpBFG].SpritePrefix != "MACE" || Weapons[wpShotgun].Name != "Ethereal Crossbow" {
		t.Errorf("weapon table not reshaped: %q / %q", Weapons[wpBFG].SpritePrefix, Weapons[wpShotgun].Name)
	}
	if Weapons[wpShotgun].Projectile == nil {
		t.Error("crossbow has no ProjectileDef")
	}

	// give + select + use an artifact
	if !g.giveArtifact(artiHealth) {
		t.Fatal("giveArtifact(flask) failed")
	}
	if g.Player.ArtiCurrent != int(artiHealth) || g.Player.Inventory[artiHealth] != 1 {
		t.Fatalf("flask not selected/stocked: cur=%d inv=%v", g.Player.ArtiCurrent, g.Player.Inventory)
	}
	g.Player.Health = 50
	g.useArtifact()
	if g.Player.Health != 75 || g.Player.Inventory[artiHealth] != 0 {
		t.Errorf("flask use: health %d (want 75), inv %d (want 0)", g.Player.Health, g.Player.Inventory[artiHealth])
	}

	// tome sets its timer; wings sets flight
	g.giveArtifact(artiTome)
	g.useArtifact()
	if g.Player.Powers[pwTome] <= 0 {
		t.Error("Tome of Power didn't set pwTome")
	}
	g.giveArtifact(artiWings)
	g.Player.ArtiCurrent = int(artiWings)
	g.useArtifact()
	if g.Player.Powers[pwFlight] <= 0 {
		t.Error("Wings didn't set pwFlight")
	}

	// egg chickens the monster it hits
	if !g.spawnHereticMonster(68, g.Camera.X+64, g.Camera.Y, 180, false) {
		t.Fatal("no golem")
	}
	mon := g.mobjs[len(g.mobjs)-1]
	g.chickenize(mon)
	if mon.hact == nil || mon.hact.name != "chicken" || mon.spriteOverride != "CHKN" {
		t.Errorf("chickenize failed: %+v", mon.hact)
	}

	// weapon table must NOT leak into a following Doom session
	resetWeaponTables()
	if Weapons[wpBFG].SpritePrefix != "BFGG" {
		t.Errorf("resetWeaponTables didn't restore Doom BFG (got %q)", Weapons[wpBFG].SpritePrefix)
	}
}

func TestHereticTomeOfPower(t *testing.T) {
	g := hereticGame(t, "E1M1")
	g.playerMobj.Angle = 0

	// no tome: crossbow fires its 3-bolt spread
	g.Player.Weapons[wpShotgun] = true
	g.Weapon.Current = wpShotgun
	g.Player.Ammo[1] = 10
	g.projectiles = g.projectiles[:0]
	g.beginFire()
	base := len(g.projectiles)
	if base != 3 {
		t.Errorf("crossbow fired %d bolts, want 3", base)
	}

	// tome makes the wand fire a wide 5-bolt fan
	g.giveArtifact(artiTome)
	g.Player.ArtiCurrent = int(artiTome)
	g.useArtifact()
	if g.Player.Powers[pwTome] <= 0 {
		t.Fatal("tome not active")
	}
	if p := Weapons[wpPistol].Powered; p == nil || p.Shots != 5 {
		t.Fatalf("powered wand = %+v, want a 5-shot projectile", p)
	}
	g.Weapon.Current = wpPistol
	g.Player.Ammo[0] = 10
	g.projectiles = g.projectiles[:0]
	g.beginFire()
	if n := len(g.projectiles); n != 5 {
		t.Errorf("empowered wand fired %d bolts, want 5", n)
	}

	// tome + firemace: one huge death ball with a blast radius
	g.Player.Weapons[wpBFG] = true
	g.Weapon.Current = wpBFG
	g.Player.Ammo[5] = 10
	g.projectiles = g.projectiles[:0]
	g.beginFire()
	if len(g.projectiles) != 1 || g.projectiles[0].def.ExplodeRadius < 100 {
		t.Errorf("powered firemace: %d projectiles, radius %d", len(g.projectiles),
			func() int {
				if len(g.projectiles) > 0 {
					return g.projectiles[0].def.ExplodeRadius
				}
				return -1
			}())
	}
}
