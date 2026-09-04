package engine

import "log"

// Damage and death, ported from id's p_inter.c (P_DamageMobj / P_KillMobj).

// pDamageMobj applies damage to target. inflictor is the thing dealing it
// (a missile, or the attacker for melee); source is who to credit / who a
// monster should turn on. Handles armour, pain, death, and infighting.
func (g *Game) pDamageMobj(target, inflictor, source *Mobj, damage int) {
	if target == nil || target.Flags&MF_SHOOTABLE == 0 || target.Health <= 0 || damage <= 0 {
		return
	}
	if target.Flags&MF_SKULLFLY != 0 {
		target.MomX, target.MomY, target.MomZ = 0, 0, 0
	}

	if target == g.playerMobj {
		if g.playerDead {
			return
		}
		// id: below a 1000-damage "telefrag" threshold, an active
		// invulnerability sphere — or the iddqd cheat (cheats.go) — cancels
		// the hit entirely.
		if damage < 1000 && (g.godMode || g.Player.Powers[pwInvulnerability] > 0) {
			return
		}
		// config damageScale (percent, 100 = normal) — a testing knob that
		// scales every ordinary hit; a crush bypasses this (crushPlayer).
		if sc := g.damageScale; sc > 0 && sc != 100 {
			if damage = damage * sc / 100; damage <= 0 {
				return
			}
		}
		if g.Player.Armor > 0 {
			saved := damage / 2
			if g.Player.ArmorType == 1 {
				saved = damage / 3
			}
			if g.Player.Armor <= saved {
				saved = g.Player.Armor
				g.Player.ArmorType = 0
			}
			g.Player.Armor -= saved
			damage -= saved
		}
		g.Player.Health -= damage
		if g.Player.Health < 0 {
			g.Player.Health = 0
		}
		target.Health = g.Player.Health
		if g.Player.Health <= 0 {
			g.killPlayer(damage > 50) // a big finishing blow gets the gib cry
		}
		return
	}

	target.Health -= damage
	if target.Health <= 0 {
		g.pKillMobj(source, target)
		return
	}

	if pRandom() < target.Info.PainChance && target.Flags&MF_SKULLFLY == 0 {
		target.Flags |= MF_JUSTHIT
		g.setMobjState(target, target.Info.PainState)
	}
	target.ReactionTime = 0

	// Wake up / infighting: unless already fixated on something, turn to
	// face whoever just hit us (the player, or another monster).
	if target.Threshold == 0 && source != nil && source != target {
		target.Target = source
		target.Threshold = 100
		if target.State == target.Info.SpawnState && target.Info.SeeState != S_NULL {
			g.setMobjState(target, target.Info.SeeState)
		}
	}
}

// killPlayer runs the player's death: health to 0, movement frozen, weapon
// hidden, the eye sinking to the floor (resolvePlayerZ), and the death face
// on the status bar. bigHit picks the harder death cry. Idempotent.
func (g *Game) killPlayer(bigHit bool) {
	if g.playerDead {
		return
	}
	g.playerDead = true
	g.Player.Health = 0
	g.VelX, g.VelY, g.VelZ = 0, 0, 0
	g.deadEye = g.Camera.Z // sink starts from wherever the eye is now
	if g.playerMobj != nil {
		g.playerMobj.Health = 0
		g.playerMobj.Flags &^= MF_SHOOTABLE
	}
	if bigHit {
		g.playSound("DSPDIEHI")
	} else {
		g.playSound("DSPLDETH")
	}
	log.Println("engine: player died")
}

// crushPlayer is the "squished by a closing door / crusher / rising floor"
// death — always lethal, ignoring config damageScale.
func (g *Game) crushPlayer() {
	if g.playerDead {
		return
	}
	// Vanilla crushing is 10 damage/tic through P_DamageMobj, so god mode
	// (iddqd) stops it; mirror that here since this path is direct.
	if g.godMode {
		return
	}
	g.Player.Health = 0
	g.killPlayer(true)
}

// pKillMobj turns target into a corpse and starts its death (or gib)
// animation, ported from id's P_KillMobj.
func (g *Game) pKillMobj(source, target *Mobj) {
	target.Flags &^= MF_SHOOTABLE | MF_FLOAT | MF_SKULLFLY
	if target.Type != MT_SKULL {
		target.Flags &^= MF_NOGRAVITY
	}
	target.Flags |= MF_CORPSE | MF_DROPOFF
	target.Height /= 4

	if target.Flags&MF_COUNTKILL != 0 {
		g.killCount++
	}

	if target.Health < -target.Info.SpawnHealth && target.Info.XDeathState != S_NULL {
		g.setMobjState(target, target.Info.XDeathState)
	} else {
		g.setMobjState(target, target.Info.DeathState)
	}
	// Randomise the death start so a group doesn't animate in lockstep.
	target.Tics -= pRandom() & 3
	if target.Tics < 1 {
		target.Tics = 1
	}

	// Monster item drop (Phase 4's pickup code will make it collectible).
	drop, ok := mobjType(0), false
	switch target.Type {
	case MT_POSSESSED:
		drop, ok = MT_CLIP, true
	case MT_SHOTGUY:
		drop, ok = MT_SHOTGUN, true
	case MT_CHAINGUY:
		drop, ok = MT_CHAINGUN, true
	}
	if ok {
		it := g.P_SpawnMobj(target.X, target.Y, onFloorZ, drop)
		it.Flags |= MF_DROPPED
	}
}
