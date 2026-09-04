package engine

import "testing"

// newSweepGame is a minimal Game for exercising checkItemPickups directly:
// a player mobj at the origin (feet at Z=0) and nothing else. Items are
// spawned with an explicit z=0 so P_SpawnMobj doesn't need a BSP.
func newSweepGame() *Game {
	g := &Game{Player: DefaultPlayerStats()}
	g.playerMobj = &Mobj{
		Type: MT_PLAYER, Info: &mobjInfo[MT_PLAYER],
		Health: 100, Radius: 16, Height: 56,
	}
	return g
}

// moveAndCheck teleports the player to (x,y) and runs one pickup pass — the
// two-argument form also sets where the previous pass "saw" the player, so
// the swept segment is (fromX,fromY)->(x,y).
func (g *Game) sweepTo(fromX, fromY, x, y float64) {
	g.pickupPrevX, g.pickupPrevY, g.pickupPrevSet = fromX, fromY, true
	g.playerMobj.X, g.playerMobj.Y = x, y
	g.checkItemPickups()
}

func TestPickupSweepCatchesFastPass(t *testing.T) {
	g := newSweepGame()
	item := g.P_SpawnMobj(0, 0, 0, MT_MISC2) // health bonus, always collectable

	// Player runs from (40,10) to (-40,10): the end point is 40 units from
	// the item on X — outside the 36-unit (20+16) touch box, so a
	// point-only check would miss it — but the path passes 10 units away.
	g.sweepTo(40, 10, -40, 10)

	if !item.removed {
		t.Fatal("fast pass over an item was not picked up (swept test missed it)")
	}
	if g.Player.Health != 101 {
		t.Errorf("health bonus not applied: %d, want 101", g.Player.Health)
	}
}

func TestPickupStandingStillStillWorks(t *testing.T) {
	g := newSweepGame()
	item := g.P_SpawnMobj(10, 10, 0, MT_MISC2)

	// Not moving (from == to), item inside the touch box: the exact vanilla
	// box test still applies.
	g.sweepTo(5, 5, 5, 5)

	if !item.removed {
		t.Fatal("standing on an item did not pick it up")
	}
}

func TestPickupSweepIgnoresTeleportJump(t *testing.T) {
	g := newSweepGame()
	item := g.P_SpawnMobj(150, 150, 0, MT_MISC2)

	// A jump longer than pickupSweepMax (here ~424 units) is a teleport /
	// level load, not a run — it must not vacuum up items that merely lie
	// on the straight line between the two points.
	g.sweepTo(300, 300, 0, 0)

	if item.removed {
		t.Fatal("an item on the teleport line was wrongly collected")
	}
}

func TestPickupSweepRespectsReach(t *testing.T) {
	g := newSweepGame()
	g.playerMobj.Z = 0
	low := g.P_SpawnMobj(0, 0, -12, MT_MISC2) // 12 units below the feet

	g.sweepTo(40, 0, -40, 0) // path runs right over it in X/Y

	if low.removed {
		t.Fatal("swept pickup ignored the vertical reach limit")
	}
}

func TestSwitchWeaponOnPickupToggle(t *testing.T) {
	// On (the default): a newly acquired weapon becomes current.
	g := newSweepGame()
	g.Weapon = NewWeaponState() // current = pistol
	g.SwitchWeaponOnPickup = true
	g.pTouchSpecialThing(g.P_SpawnMobj(0, 0, 0, MT_SHOTGUN), g.playerMobj)
	if !g.Player.Weapons[wpShotgun] {
		t.Fatal("shotgun not added to the arsenal")
	}
	if g.Weapon.pending != wpShotgun {
		t.Errorf("auto-switch on: pending=%d, want %d", g.Weapon.pending, wpShotgun)
	}

	// Off: the weapon is still added, but the current one stays up.
	g = newSweepGame()
	g.Weapon = NewWeaponState()
	g.SwitchWeaponOnPickup = false
	g.pTouchSpecialThing(g.P_SpawnMobj(0, 0, 0, MT_SHOTGUN), g.playerMobj)
	if !g.Player.Weapons[wpShotgun] {
		t.Error("switch-off: shotgun not added to the arsenal")
	}
	if g.Weapon.pending == wpShotgun {
		t.Error("switch-off: auto-switched despite SwitchWeaponOnPickup=false")
	}
}
