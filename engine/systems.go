package engine

// The per-frame simulation is split into ordered System implementations
// (registered in NewGame) instead of a fixed call sequence hand-written in
// Run: Game.Run holds an []System and drives it polymorphically, so a new
// subsystem is an addition here rather than an edit to the loop. Each of
// these is stateless — the state they act on lives on *Game — so a plain
// zero-value struct is all that's registered.

// movementSystem applies input, momentum, collision, and the eye-height
// pin for this frame (see handleMovement).
type movementSystem struct{}

func (movementSystem) Name() string { return "movement" }

func (movementSystem) Update(g *Game, dt float32) {
	g.handleMovement(dt)
}

// useSystem edge-detects the "use" key (Space) and activates the nearest
// special linedef in front of the player — doors, switches, exits. The
// sector effects those spawn are advanced on the 35Hz tic (tickSpecials
// from runTic), not here.
type useSystem struct{}

func (useSystem) Name() string { return "use" }

func (useSystem) Update(g *Game, dt float32) {
	spaceDown := g.Window.UsePressed()
	if spaceDown && !g.spaceWasDown {
		if g.playerDead {
			g.restartLevel() // dead: Space takes you back and restarts the level
		} else {
			g.tryUseLine()
		}
	}
	g.spaceWasDown = spaceDown
}

// combatSystem handles weapon selection and firing input, then advances
// the viewmodel animation and any in-flight projectiles (see weapons.go,
// projectiles.go).
type combatSystem struct{}

func (combatSystem) Name() string { return "combat" }

func (combatSystem) Update(g *Game, dt float32) {
	if g.playerDead {
		// No weapon switching or firing; still let in-flight shots and the
		// (hidden) viewmodel animation settle.
		g.updateWeapon(float64(dt))
		g.updateProjectiles(float64(dt))
		return
	}
	// Weapon slots 1-7 (classic Doom layout). Edge-triggered so holding a
	// key doesn't strobe a toggle slot (1: fist/chainsaw, 3: shotgun/SSG).
	for slot := 1; slot <= 7; slot++ {
		down := g.Window.WeaponKeyPressed(slot)
		if down && !g.weaponKeyDown[slot] {
			g.SelectWeaponSlot(slot)
		}
		g.weaponKeyDown[slot] = down
	}
	// Held, not edge-triggered: real Doom refires every weapon
	// automatically for as long as the fire button stays down, at whatever
	// cadence that weapon's own animation allows — FireWeapon already
	// no-ops unless the weapon is fully back in its ready state, so this
	// alone reproduces each gun's actual rate of fire (e.g. the chaingun's
	// ~4.4 rounds/sec from its 8-tic cycle) instead of capping every
	// weapon at one shot per click.
	if g.Window.FirePressed() {
		g.FireWeapon()
	}
	g.updateWeapon(float64(dt))
	g.updateProjectiles(float64(dt))
}
