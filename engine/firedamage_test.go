package engine

import "testing"

// TestFireDamageBurnsPlayerInPuddle mirrors lavadamage_test.go's style: a
// fake burningPuddle centred exactly on the player must deal periodic
// damage (see checkFireDamage/inBurningFire).
func TestFireDamageBurnsPlayerInPuddle(t *testing.T) {
	g := loadRealLevel(t, "../testdata/DOOM1.WAD", "E1M1")
	g.mobjs = nil // only the fire itself should touch the player here

	g.burningPuddles = []*burningPuddle{{
		x: g.Camera.X, y: g.Camera.Y, z: g.PlayerZ,
		amount: 1, duration: 10, remaining: 10,
	}}

	before := g.Player.Health
	for i := 0; i < fireDamageTics+1; i++ {
		g.levelTime++
		g.checkFireDamage()
	}
	if g.Player.Health >= before {
		t.Errorf("standing in the fire: health %d -> %d, want a decrease", before, g.Player.Health)
	}
}

// TestFireDamageMissesOutsideRadius: a puddle far away must never touch
// the player, however long the check runs.
func TestFireDamageMissesOutsideRadius(t *testing.T) {
	g := loadRealLevel(t, "../testdata/DOOM1.WAD", "E1M1")
	g.mobjs = nil

	g.burningPuddles = []*burningPuddle{{
		x: g.Camera.X + 1000, y: g.Camera.Y + 1000, z: g.PlayerZ,
		amount: 1, duration: 10, remaining: 10,
	}}

	before := g.Player.Health
	for i := 0; i < fireDamageTics+1; i++ {
		g.levelTime++
		g.checkFireDamage()
	}
	if g.Player.Health != before {
		t.Errorf("far from the fire: health %d -> %d, want unchanged", before, g.Player.Health)
	}
}

// TestFireDamageBurnsMonsterInPuddle: the same fire also has to hurt
// monsters, not just the player.
func TestFireDamageBurnsMonsterInPuddle(t *testing.T) {
	g := loadRealLevel(t, "../testdata/DOOM1.WAD", "E1M1")
	g.mobjs = nil
	g.Camera.X, g.Camera.Y = 1000000, 1000000 // keep the player well clear

	imp := g.P_SpawnMobj(500, 500, onFloorZ, MT_TROOP)
	imp.Health = 60

	g.burningPuddles = []*burningPuddle{{
		x: imp.X, y: imp.Y, z: imp.Z,
		amount: 1, duration: 10, remaining: 10,
	}}

	before := imp.Health
	for i := 0; i < fireDamageTics+1; i++ {
		g.levelTime++
		g.checkFireDamage()
	}
	if imp.Health >= before {
		t.Errorf("monster standing in the fire: health %d -> %d, want a decrease", before, imp.Health)
	}
}
