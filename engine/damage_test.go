package engine

import "testing"

func TestDamageMobjPainAndDeath(t *testing.T) {
	g := &Game{}
	// A zombieman (spawn health 20). Non-lethal hit -> health drops, alive.
	z := g.P_SpawnMobj(0, 0, 0, MT_POSSESSED)
	g.pDamageMobj(z, nil, nil, 5)
	if z.Health != 15 || z.Flags&MF_CORPSE != 0 {
		t.Fatalf("after 5 dmg: health=%d corpse=%v", z.Health, z.Flags&MF_CORPSE != 0)
	}
	// Lethal hit -> corpse: not shootable, MF_CORPSE, squashed, in a death state.
	h0 := z.Height
	g.pDamageMobj(z, nil, nil, 100)
	if z.Flags&MF_SHOOTABLE != 0 {
		t.Error("dead monster still MF_SHOOTABLE")
	}
	if z.Flags&MF_CORPSE == 0 {
		t.Error("dead monster not MF_CORPSE")
	}
	if z.Height >= h0 {
		t.Errorf("corpse height %v not reduced from %v", z.Height, h0)
	}
	if z.State != S_POSS_XDIE1 && z.State != S_POSS_DIE1 {
		t.Errorf("dead monster state %d, want a death state", z.State)
	}
	if g.killCount != 1 {
		t.Errorf("killCount = %d, want 1", g.killCount)
	}
}

func TestDamageMobjGibThreshold(t *testing.T) {
	g := &Game{}
	z := g.P_SpawnMobj(0, 0, 0, MT_POSSESSED) // spawnhealth 20
	g.pDamageMobj(z, nil, nil, 60)            // health -> -40 < -20 -> gib
	if z.State != S_POSS_XDIE1 {
		t.Errorf("overkill state %d, want S_POSS_XDIE1", z.State)
	}
}

func TestPlayerArmorAbsorption(t *testing.T) {
	g := &Game{}
	g.Player = PlayerStats{Health: 100, Armor: 100, ArmorType: 1} // green: absorbs 1/3
	g.playerMobj = &Mobj{Type: MT_PLAYER, Info: &mobjInfo[MT_PLAYER], Flags: MF_SHOOTABLE, Health: 100, Height: 56}
	g.pDamageMobj(g.playerMobj, nil, nil, 30)
	// green armor saves 30/3 = 10, so 20 to health, 10 off armor.
	if g.Player.Health != 80 || g.Player.Armor != 90 {
		t.Errorf("health=%d armor=%d, want 80 / 90", g.Player.Health, g.Player.Armor)
	}
}

func TestRadiusAttackFalloff(t *testing.T) {
	g := &Game{}
	spot := &Mobj{X: 0, Y: 0}
	near := g.P_SpawnMobj(10, 0, 0, MT_BARREL) // health 20
	far := g.P_SpawnMobj(200, 0, 0, MT_BARREL) // outside a 128 blast
	nH, fH := near.Health, far.Health
	g.pRadiusAttack(spot, nil, 128)
	if near.Health >= nH {
		t.Errorf("nearby thing took no blast damage (%d -> %d)", nH, near.Health)
	}
	if far.Health != fH {
		t.Errorf("far thing (200u) took blast damage (%d -> %d)", fH, far.Health)
	}
}
