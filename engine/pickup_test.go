package engine

import "testing"

func newPickupGame() *Game {
	g := &Game{Player: DefaultPlayerStats(), SwitchWeaponOnPickup: true}
	g.playerMobj = &Mobj{
		Type: MT_PLAYER, Info: &mobjInfo[MT_PLAYER], Flags: MF_SOLID | MF_SHOOTABLE,
		Health: 100, Radius: 16, Height: 56,
	}
	return g
}

func TestGiveAmmoClampsAndRefuses(t *testing.T) {
	g := newPickupGame() // 50 bullets, max 200
	if !g.giveAmmo(0, 100) || g.Player.Ammo[0] != 150 {
		t.Fatalf("gave 100 bullets -> %d, want 150", g.Player.Ammo[0])
	}
	if !g.giveAmmo(0, 100) || g.Player.Ammo[0] != 200 {
		t.Fatalf("clamp to max: %d, want 200", g.Player.Ammo[0])
	}
	if g.giveAmmo(0, 10) {
		t.Error("gave ammo while already full")
	}
}

func TestGiveArmor(t *testing.T) {
	g := newPickupGame()
	if !g.giveArmor(1) || g.Player.Armor != 100 || g.Player.ArmorType != 1 {
		t.Fatalf("green armor -> %d/%d", g.Player.Armor, g.Player.ArmorType)
	}
	if g.giveArmor(1) {
		t.Error("green armor taken again while already at 100 green")
	}
	if !g.giveArmor(2) || g.Player.Armor != 200 || g.Player.ArmorType != 2 {
		t.Fatalf("blue armor upgrade -> %d/%d", g.Player.Armor, g.Player.ArmorType)
	}
}

func TestGiveWeaponGrantsAndSwitches(t *testing.T) {
	g := newPickupGame()
	g.Weapon = NewWeaponState() // current = pistol
	if !g.giveWeapon(wpShotgun, false) {
		t.Fatal("shotgun pickup returned false")
	}
	if !g.Player.Weapons[wpShotgun] {
		t.Error("shotgun not marked owned")
	}
	if g.Player.Ammo[1] == 0 {
		t.Error("shotgun pickup gave no shells")
	}
	if g.Weapon.pending != wpShotgun {
		t.Errorf("did not auto-switch to shotgun (pending=%d)", g.Weapon.pending)
	}
	// Second shotgun: already owned -> only ammo (and only if not full).
	shells := g.Player.Ammo[1]
	if g.giveWeapon(wpShotgun, false) != (g.Player.Ammo[1] > shells) {
		t.Error("second shotgun pickup result should track whether ammo was added")
	}
}

func TestSwitchWeaponRequiresOwnership(t *testing.T) {
	g := newPickupGame()
	g.Weapon = NewWeaponState()
	g.SwitchWeapon(wpMissile) // rocket launcher, not owned
	if g.Weapon.pending == wpMissile {
		t.Error("switched to an unowned weapon")
	}
	g.Player.Weapons[wpMissile] = true
	g.SwitchWeapon(wpMissile)
	if g.Weapon.pending != wpMissile {
		t.Error("did not switch to a now-owned weapon")
	}
}

func TestTouchWeaponPickups(t *testing.T) {
	// Every weapon thing the IWADs place should be collectable now — the
	// chainsaw (MT_MISC26) and super shotgun (MT_SUPERSHOTGUN) used to be
	// silently left on the floor.
	cases := []struct {
		name string
		typ  mobjType
		slot int
	}{
		{"rocket launcher", MT_MISC27, wpMissile},
		{"plasma rifle", MT_MISC28, wpPlasma},
		{"BFG9000", MT_MISC25, wpBFG},
		{"chainsaw", MT_MISC26, wpChainsaw},
		{"super shotgun", MT_SUPERSHOTGUN, wpSuperShotgun},
	}
	for _, tc := range cases {
		g := newPickupGame()
		g.Weapon = NewWeaponState()
		w := g.P_SpawnMobj(0, 0, 0, tc.typ)
		g.pTouchSpecialThing(w, g.playerMobj)
		if !w.removed {
			t.Errorf("%s: not picked up", tc.name)
		}
		if !g.Player.Weapons[tc.slot] {
			t.Errorf("%s: not marked owned (slot %d)", tc.name, tc.slot)
		}
	}

	// Super shotgun comes with shells; chainsaw brings no ammo.
	g := newPickupGame()
	g.Weapon = NewWeaponState()
	g.pTouchSpecialThing(g.P_SpawnMobj(0, 0, 0, MT_SUPERSHOTGUN), g.playerMobj)
	if g.Player.Ammo[1] == 0 {
		t.Error("super shotgun pickup gave no shells")
	}
}

func TestChainsawFireIsMeleeAndFree(t *testing.T) {
	g := newPickupGame()
	g.Weapon = NewWeaponState()
	g.pTouchSpecialThing(g.P_SpawnMobj(0, 0, 0, MT_MISC26), g.playerMobj)
	g.Weapon.Current = wpChainsaw
	g.Weapon.phase = weaponReady
	bullets := g.Player.Ammo[0]
	g.FireWeapon()
	if g.Player.Ammo[0] != bullets {
		t.Errorf("chainsaw consumed ammo: %d -> %d", bullets, g.Player.Ammo[0])
	}
	if g.Weapon.phase != weaponFiring {
		t.Errorf("chainsaw didn't start firing (phase %v)", g.Weapon.phase)
	}
}

func TestPickupFidelityVsIdSource(t *testing.T) {
	// Armour bonus: +1 (id SPR_BON2), not +2.
	g := newPickupGame()
	g.Player.Armor, g.Player.ArmorType = 10, 1
	g.pTouchSpecialThing(g.P_SpawnMobj(0, 0, 0, MT_MISC3), g.playerMobj)
	if g.Player.Armor != 11 {
		t.Errorf("armour bonus -> %d, want 11 (+1)", g.Player.Armor)
	}

	// Plasma rifle: found weapon gives two clips = 40 cells.
	g = newPickupGame()
	g.Weapon = NewWeaponState()
	g.pTouchSpecialThing(g.P_SpawnMobj(0, 0, 0, MT_MISC28), g.playerMobj)
	if g.Player.Ammo[2] != 40 {
		t.Errorf("plasma pickup -> %d cells, want 40", g.Player.Ammo[2])
	}

	// Backpack: MaxAmmo doubles once, not per pickup.
	g = newPickupGame()
	base := g.Player.MaxAmmo
	g.pTouchSpecialThing(g.P_SpawnMobj(0, 0, 0, MT_MISC24), g.playerMobj)
	g.pTouchSpecialThing(g.P_SpawnMobj(0, 0, 0, MT_MISC24), g.playerMobj)
	for i := range base {
		if g.Player.MaxAmmo[i] != base[i]*2 {
			t.Errorf("two backpacks: MaxAmmo[%d] = %d, want %d (doubled once)", i, g.Player.MaxAmmo[i], base[i]*2)
		}
	}

	// A duplicate keycard is still consumed in single-player.
	g = newPickupGame()
	g.Player.Keys[0] = true
	k := g.P_SpawnMobj(0, 0, 0, MT_MISC4)
	g.pTouchSpecialThing(k, g.playerMobj)
	if !k.removed {
		t.Error("duplicate blue keycard not removed (id removes it in single-player)")
	}

	// Invulnerability sphere cancels sub-telefrag damage.
	g = newPickupGame()
	g.Player.Health = 100
	g.Player.Powers[pwInvulnerability] = 100
	g.pDamageMobj(g.playerMobj, nil, nil, 50)
	if g.Player.Health != 100 {
		t.Errorf("invulnerable player took %d damage", 100-g.Player.Health)
	}

	// Item more than 8 units below the player's feet is out of reach (id).
	g = newPickupGame()
	g.playerMobj.Z = 0
	low := g.P_SpawnMobj(0, 0, -12, MT_MISC10)
	g.Player.Health = 50
	g.checkItemPickups()
	if low.removed {
		t.Error("picked up an item 12 units below the player's feet")
	}
}

func TestTouchStimpackWhenFullIsIgnored(t *testing.T) {
	g := newPickupGame()
	stim := g.P_SpawnMobj(0, 0, 0, MT_MISC10)
	g.Player.Health = 100
	g.pTouchSpecialThing(stim, g.playerMobj)
	if stim.removed {
		t.Error("stimpack consumed at full health")
	}
	g.Player.Health = 50
	g.pTouchSpecialThing(stim, g.playerMobj)
	if !stim.removed || g.Player.Health != 60 {
		t.Errorf("stimpack: removed=%v health=%d, want true/60", stim.removed, g.Player.Health)
	}
}

func TestUpdatePowersCountdownAndInvisExpiry(t *testing.T) {
	g := newPickupGame()
	g.Player.Powers[pwInvisibility] = 2
	g.playerMobj.Flags |= MF_SHADOW
	g.updatePowers()
	if g.Player.Powers[pwInvisibility] != 1 {
		t.Fatalf("power not decremented: %d", g.Player.Powers[pwInvisibility])
	}
	g.updatePowers()
	if g.Player.Powers[pwInvisibility] != 0 || g.playerMobj.Flags&MF_SHADOW != 0 {
		t.Error("invisibility did not clear MF_SHADOW on expiry")
	}
	// Berserk never ticks down.
	g.Player.Powers[pwStrength] = 1
	g.updatePowers()
	if g.Player.Powers[pwStrength] != 1 {
		t.Error("berserk (pwStrength) counted down")
	}
}
