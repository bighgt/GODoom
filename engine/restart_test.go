package engine

import "testing"

func TestRestartLevelResetsAndQueuesReload(t *testing.T) {
	g := loadRealLevel(t, "../testdata/DOOM1.WAD", "E1M1")

	// Kit the player out and kill them, the way a real death-screen restart
	// would find things.
	g.giveAllWeaponsAndAmmo()
	g.Player.Armor, g.Player.ArmorType = 200, 2
	g.Player.Keys[0] = true
	g.killPlayer(false)
	if !g.playerDead {
		t.Fatal("precondition: player should be dead")
	}

	g.restartLevel()

	if g.pendingWarp != "E1M1" {
		t.Fatalf("restartLevel queued %q, want E1M1", g.pendingWarp)
	}
	if g.Player.Health != 100 || g.Player.Armor != 0 || g.Player.Weapons[wpBFG] || g.Player.Keys[0] {
		t.Fatalf("restartLevel did not reset the loadout: %+v", g.Player)
	}
	if g.Weapon.Current != wpPistol {
		t.Fatalf("restartLevel weapon = %d, want pistol", g.Weapon.Current)
	}
}

func TestStepSimulationConsumesPendingWarp(t *testing.T) {
	g := loadRealLevel(t, "../testdata/DOOM1.WAD", "E1M1")
	g.playerMobj = &Mobj{Type: MT_PLAYER}
	g.playerDead = true
	g.mobjs = g.mobjs[:0] // prove the reload repopulates

	g.pendingWarp = "E1M1"
	g.stepSimulation(0) // ticAccum stays 0, so only the warp branch runs

	if g.pendingWarp != "" {
		t.Fatalf("pendingWarp not consumed: %q", g.pendingWarp)
	}
	if g.Level.Name != "E1M1" {
		t.Fatalf("reloaded the wrong map: %s", g.Level.Name)
	}
	if g.playerDead {
		t.Fatal("reload did not clear playerDead")
	}
	if len(g.mobjs) == 0 {
		t.Fatal("reload did not respawn map things")
	}
}
