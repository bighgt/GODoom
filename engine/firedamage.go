package engine

import "math"

// Fire damage: anything standing in a burning puddle's fire (the lingering
// ground fire barrels leave behind — see engine/burningpuddle.go) takes
// periodic damage for as long as it stays there, the same way standing in
// a damaging nukage/lava sector does (see specials.go's
// playerInSpecialSector). Scoped to burning puddles specifically, not
// every individual spark/fire particle from an explosion's initial burst:
// those scatter outward and are gone within one or two seconds — nowhere
// near long enough to be something you could carelessly stand in — and the
// blast itself already deals its own direct damage via pRadiusAttack.
// Checking hundreds of scattering sub-two-second particles against every
// mobj every tic isn't worth the cost for a hazard that brief; the puddle
// fire lingers for up to burningPuddleMaxDuration seconds and is exactly
// the "walked into it and got burned" case worth a real check against.

// fireDamageTics/Amount: how often anything standing in the fire takes
// damage, and how much — twice as frequent as nukage/lava's 32-tic
// cadence (playerInSpecialSector), since open fire is a more urgent hazard
// than ambient toxic floor. fireDamageHeightTolerance mirrors
// liquidContactTolerance's reasoning: a flame lick only reaches modestly
// above the floor (see puddleFlameRise), so this just needs to cover
// "standing at floor level near it," not any real height range.
const (
	fireDamageTics            = 16
	fireDamageAmount          = 8
	fireDamageHeightTolerance = 40.0
)

// checkFireDamage burns the player and any live monster standing in a
// burning puddle's fire. Called from runTic every tic; cheap even when it
// applies, since g.burningPuddles is always a short list (a handful of
// splatters per barrel) and the tics check below skips most calls outright.
func (g *Game) checkFireDamage() {
	if len(g.burningPuddles) == 0 || g.levelTime%fireDamageTics != 0 {
		return
	}
	if g.playerMobj != nil && g.Player.Health > 0 && inBurningFire(g.burningPuddles, g.Camera.X, g.Camera.Y, g.PlayerZ) {
		g.pDamageMobj(g.playerMobj, nil, nil, fireDamageAmount)
	}
	for _, mo := range g.mobjs {
		if mo == nil || mo.removed || mo.Flags&MF_SHOOTABLE == 0 || mo.Health <= 0 {
			continue
		}
		if inBurningFire(g.burningPuddles, mo.X, mo.Y, mo.Z) {
			g.pDamageMobj(mo, nil, nil, fireDamageAmount)
		}
	}
}

// inBurningFire reports whether (x, y, z) is within any live puddle's
// current fire radius — puddleFade's same shrinking-footprint curve the
// visible flame/decal already use (see collectGroundSplats), so the
// hazard always matches what's actually on screen, not the puddle's
// original (possibly long-since-shrunk) extent.
func inBurningFire(puddles []*burningPuddle, x, y, z float64) bool {
	for _, p := range puddles {
		if p.ignitionDelay > 0 {
			continue // spilled liquid hasn't caught fire yet — see burningPuddleIgnitionDelayMin/Max
		}
		if math.Abs(z-p.z) > fireDamageHeightTolerance {
			continue
		}
		fade := puddleFade(p)
		radius := puddleSpreadRadius(p) * (0.4 + 0.6*fade)
		dx, dy := x-p.x, y-p.y
		if dx*dx+dy*dy <= radius*radius {
			return true
		}
	}
	return false
}
