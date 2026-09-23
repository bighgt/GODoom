package engine

import (
	"fmt"
	"math"

	"twopointfive/bsp"
	"twopointfive/raster"
)

// projectileRadius/projectileStartDist are shared shot-flight tuning that
// doesn't vary per weapon (each weapon's own ProjectileDef — see
// weapons.go — carries what does: sprite, speed, explosion animation/sound).
const (
	projectileRadius    = 4.0  // wall-hit test radius, and the flight substep length
	projectileStartDist = 20.0 // spawn this far in front of the camera, clear of the weapon viewmodel/camera itself
	// projectileMaxRange is a backstop: the floor/ceiling and left-the-level
	// checks in advanceProjectile end virtually every shot, but a rocket in
	// a huge open sector with no wall in range would otherwise fly forever,
	// staying in the slice and drawn every frame. ~128 tiles.
	projectileMaxRange = 8192.0
	// rocketBarrelExplosionBoost is how much bigger a barrel's own
	// explosion gets when a rocket's direct hit is what kills it — see
	// advanceProjectile and aExplode.
	rocketBarrelExplosionBoost = 4.0
)

// Projectile is a weapon's true traveling shot in flight (see
// WeaponDef.Projectile — the three hitscan weapons never create one of
// these at all). It carries its own copy of the ProjectileDef's visual/
// sound data because a projectile in flight outlives — and should keep
// its own look even across — whatever the player switches to next.
type Projectile struct {
	X, Y, Z    float64
	DX, DY, DZ float64 // unit direction
	traveled   float64
	Alive      bool

	def *ProjectileDef

	// exploding/explodeElapsed drive the impact animation: once a
	// Projectile hits a wall it stops moving and plays through
	// def.ExplodeFrames in place before disappearing.
	exploding      bool
	explodeElapsed float64 // seconds since impact

	// flameTrailTimer/smokeTrailTimer count down to the next rocket exhaust
	// flame/smoke spawn — see updateRocketTrail in particles.go. Unused by
	// any non-rocket shot.
	flameTrailTimer float64
	smokeTrailTimer float64

	// skipOwnExplosion suppresses this shot's own spawnExplosionParticles
	// call on impact — set when it just landed the killing hit on a barrel
	// (see updateProjectiles), so that death's own (boosted) explosion is
	// the only one that plays instead of two overlapping bursts.
	skipOwnExplosion bool
}

// spawnProjectile fires one from the camera's current position along its
// facing direction (yaw and pitch both), offset slightly ahead so it
// doesn't immediately collide with geometry right at the camera. Returns
// the shot(s) just created (normally one; a spread weapon fires several at
// once) — RocketFollow uses this to grab the exact shot to chase.
func (g *Game) spawnProjectile(def *ProjectileDef) []*Projectile {
	baseYaw := g.Camera.Angle
	_, _, dz := aimDirection(g.Camera)
	shots := def.Shots
	if shots < 1 {
		shots = 1
	}
	spawned := make([]*Projectile, 0, shots)
	for i := 0; i < shots; i++ {
		yaw := baseYaw
		if shots > 1 {
			yaw += (float64(i) - float64(shots-1)/2) * def.SpreadRad
		}
		dx, dy := math.Cos(yaw), math.Sin(yaw)
		p := &Projectile{
			X:  g.Camera.X + dx*projectileStartDist,
			Y:  g.Camera.Y + dy*projectileStartDist,
			Z:  g.Camera.Z + dz*projectileStartDist,
			DX: dx, DY: dy, DZ: dz,
			def:   def,
			Alive: true,
		}
		g.projectiles = append(g.projectiles, p)
		spawned = append(spawned, p)
	}
	return spawned
}

// aimDirection is the camera's forward unit vector in full 3D, combining
// yaw (Camera.Angle) and pitch (Camera.Pitch) — unlike the wall/flat
// renderer, a projectile's path isn't confined to the horizontal plane.
func aimDirection(cam raster.Camera) (dx, dy, dz float64) {
	cp := math.Cos(cam.Pitch)
	dz = math.Sin(cam.Pitch)
	dx = math.Cos(cam.Angle) * cp
	dy = math.Sin(cam.Angle) * cp
	return
}

// updateProjectiles advances every live projectile by dt seconds: a
// flying one moves (see advanceProjectile) and, on hitting something,
// freezes in place and starts its explosion animation (playing its
// ExplodeSound once, right then); an exploding one just counts through
// that animation until it finishes, at which point it's removed. Compacts
// the slice to just what's still alive.
func (g *Game) updateProjectiles(dt float64) {
	if len(g.projectiles) == 0 {
		return
	}
	// Refresh mobjGrid (mobjgrid.go) here too, not just once at the top of
	// runTic: this runs every rendered frame (combatSystem.Update), not
	// just every 35Hz tic, and a thing spawned since runTic's own rebuild
	// (e.g. a scripted spawn the same tic a shot goes off — see
	// TestProjectileDirectHitDamagesMonster, which spawns and fires
	// without an intervening runTic at all) must still be a valid
	// projectileHitMobj target. Mobj positions themselves are frozen
	// between tics, so on every OTHER frame this just re-buckets the same
	// data runTic already did — redundant, but cheap (O(mobjs), same
	// budget headroom that made the old per-query O(mobjs) scan a non-issue
	// at today's mobj counts) and simpler than tracking exactly when a
	// rebuild is actually needed.
	g.rebuildMobjGrid()
	alive := g.projectiles[:0]
	for _, p := range g.projectiles {
		if p.exploding {
			p.explodeElapsed += dt
			if _, ok := p.explosionFrame(); !ok {
				p.Alive = false
			}
		} else if g.advanceProjectile(p, dt) {
			p.exploding = true
			p.explodeElapsed = 0
			g.playSound(p.def.ExplodeSound)
			if !p.skipOwnExplosion {
				scale := explosionDefault
				switch p.def.ExplodePrefix {
				case "MISL": // rocket: bigger, denser burst
					scale = explosionRocket
				case "PLSE": // plasma: energy blast, no smoke to burn
					scale = explosionEnergy
				case "BFE1": // BFG: a massive energy blast — much bigger, no smoke, huge lingering fire
					scale = explosionBFG
				}
				g.spawnExplosionParticles(p.X, p.Y, p.Z, scale)
				// The BFG's own signature: a big extra burst of sparks
				// thrown outward from the same explosion, on top of its
				// ordinary explosionBFG burst — see spawnBFGExtraSparks.
				if p.def.ExplodePrefix == "BFE1" {
					g.spawnBFGExtraSparks(p.X, p.Y, p.Z)
				}
			}
			// Rockets (and any blast-radius shot) hurt everything nearby.
			if p.def.ExplodeRadius > 0 {
				g.pRadiusAttack(&Mobj{X: p.X, Y: p.Y, Z: p.Z}, g.playerMobj, p.def.ExplodeRadius)
			}
			// The BFG ball's own splash is centred on the SHOOTER, not the
			// impact point — see ProjectileDef.Spray.
			if p.def.Spray && g.playerMobj != nil {
				g.aBFGSpray(g.playerMobj)
			}
		}
		if p.Alive && !p.exploding && p.def.ExplodePrefix == "MISL" {
			g.updateRocketTrail(p, dt)
		}
		if p.Alive {
			alive = append(alive, p)
		}
	}
	g.projectiles = alive
	// RocketFollow: the followed rocket just finished its explosion
	// animation and was removed above — hand the camera back to the player.
	if g.rocketCam != nil && !g.rocketCam.Alive {
		g.rocketCam = nil
	}
}

// rocketCamChaseDist/rocketCamLift trail RocketFollow's camera a little
// behind and above the rocket rather than riding literally inside it —
// avoids the view burying itself in the rocket's own sprite (or, on
// impact, the surface it hit). Because the offset is applied along the
// rocket's own straight flight line, the camera arrives already framed on
// the explosion the instant the rocket stops — no separate pull-back
// transition needed when p.exploding flips true.
const (
	rocketCamChaseDist = 96.0
	rocketCamLift      = 12.0
)

// rocketFollowCamera computes RocketFollow's camera pose for the followed
// projectile p, reusing fovDeg from the player's own camera so the follow
// shot doesn't jump to a different field of view.
func rocketFollowCamera(p *Projectile, fovDeg float64) raster.Camera {
	return raster.Camera{
		X:      p.X - p.DX*rocketCamChaseDist,
		Y:      p.Y - p.DY*rocketCamChaseDist,
		Z:      p.Z - p.DZ*rocketCamChaseDist + rocketCamLift,
		Angle:  math.Atan2(p.DY, p.DX),
		Pitch:  math.Asin(math.Max(-1, math.Min(1, p.DZ))),
		FOVDeg: fovDeg,
	}
}

// advanceProjectile moves p forward by one frame's worth of travel,
// checking for impact as it goes, and reports whether it hit something (in
// which case p is left at its impact position, about to explode).
//
// The move is walked in steps no longer than projectileRadius so a fast
// shot — a rocket covers ~14 map units per 60fps frame, more after a dt
// spike — can't skip over a wall thinner than that between checks. Each
// step tests, in order: a shootable monster the shot has flown into
// (projectileHitMobj — the direct hit id's PIT_CheckThing does, dealing
// def.Damage before the blast); a solid wall (projectileHitsWall,
// horizontal only); the floor/ceiling of the sector the shot is now in (so
// a missile aimed up or down has something to stop against); and whether
// the shot has left the level's geometry entirely (PointSector nil) or
// exceeded projectileMaxRange.
func (g *Game) advanceProjectile(p *Projectile, dt float64) bool {
	remaining := p.def.Speed * dt
	for remaining > 0 {
		seg := remaining
		if seg > projectileRadius {
			seg = projectileRadius
		}
		remaining -= seg

		nx := p.X + p.DX*seg
		ny := p.Y + p.DY*seg
		nz := p.Z + p.DZ*seg
		// Advance to this step's candidate position BEFORE testing it for an
		// impact, not just on a confirmed clean step: every return below
		// (mobj/wall/floor-ceiling/out-of-bounds) is a stop, and the
		// explosion this triggers (updateProjectiles' own caller) always
		// bursts at p's CURRENT position — leaving it at the END of the
		// PREVIOUS step instead of at the actual impact point (up to one
		// projectileRadius short of the wall/floor/ceiling it just hit) is
		// exactly why a rocket's blast used to visibly land short of where
		// it hit instead of centred there.
		p.X, p.Y, p.Z = nx, ny, nz

		if mo := g.projectileHitMobj(nx, ny, nz); mo != nil {
			// A rocket landing the killing blow on a barrel would otherwise
			// produce two overlapping explosions: this shot's own burst
			// right now, then the barrel's own (a few tics later, once its
			// death animation reaches S_BEXP4 — see aExplode). Detect that
			// exact case and merge them into one, bigger burst instead: this
			// shot skips its own, and the barrel's gets boosted.
			rocketBarrel := p.def.ExplodePrefix == "MISL" && mo.Type == MT_BARREL && mo.Health > 0
			if p.def.Damage > 0 {
				dmg := (pRandom()%8 + 1) * p.def.Damage
				g.pDamageMobj(mo, nil, g.playerMobj, dmg)
			}
			if rocketBarrel && mo.Health <= 0 {
				mo.explosionBoost = rocketBarrelExplosionBoost
				p.skipOwnExplosion = true
			}
			return true
		}
		if g.projectileHitsWall(nx, ny) {
			return true
		}
		sec := bsp.PointSector(g.BSP, g.Level, float32(nx), float32(ny))
		if sec == nil || nz <= sec.FloorZAt(nx, ny) || nz >= sec.CeilZAt(nx, ny) {
			return true
		}

		p.traveled += seg
		if p.traveled >= projectileMaxRange {
			return true
		}
	}
	return false
}

// projectileHitMobj returns the first live, shootable map object whose body
// the point (x, y, z) has entered — id's missile-vs-thing overlap from
// PIT_CheckThing: an axis-aligned box test on radius (plus the shot's own
// ~4u), and a vertical overlap against [thing feet, thing head] with the
// missile's own 8u height. g.playerMobj is deliberately excluded (not just
// "never in g.mobjs" — mobjGrid does include it) so a player shot can't
// detonate on the shooter. Candidates come from mobjGrid (mobjgrid.go)
// instead of a linear scan of every mobj in the level; every candidate
// still gets the exact same precise checks below.
func (g *Game) projectileHitMobj(x, y, z float64) *Mobj {
	const projHeight = 8.0
	var hit *Mobj
	g.forEachMobjNear(x, y, projectileRadius, func(mo *Mobj) bool {
		if mo == nil || mo == g.playerMobj || mo.removed || mo.Flags&MF_SHOOTABLE == 0 || mo.Health <= 0 {
			return true
		}
		block := mo.Radius + projectileRadius
		if math.Abs(x-mo.X) >= block || math.Abs(y-mo.Y) >= block {
			return true
		}
		if z+projHeight < mo.Z || z > mo.Z+mo.Height {
			return true // passing over its head or under its feet
		}
		hit = mo
		return false
	})
	return hit
}

// explosionFrame reports the explosion frame letter active at p's current
// explodeElapsed, or ok=false once it's run past def.ExplodeFrames
// entirely (meaning the projectile is done and should be removed).
func (p *Projectile) explosionFrame() (frame byte, ok bool) {
	idx := int(p.explodeElapsed * ticsPerSecond / float64(p.def.ExplodeTics))
	if idx < 0 || idx >= len(p.def.ExplodeFrames) {
		return 0, false
	}
	return p.def.ExplodeFrames[idx], true
}

// CurrentSprite returns the sprite lump name to draw for p right now:
// its flying sprite, or — mid-explosion — the current explosion frame's
// sprite (def.ExplodePrefix + frame + "0", the same "picture format"
// naming every sprite in the WAD uses). ok is false only if the
// explosion has run past its last frame (p.Alive will already be false
// by then too).
func (p *Projectile) CurrentSprite() (name string, ok bool) {
	if !p.exploding {
		return p.def.Sprite, true
	}
	frame, ok := p.explosionFrame()
	if !ok {
		return "", false
	}
	return fmt.Sprintf("%s%c0", p.def.ExplodePrefix, frame), true
}

// explosionSizeMultiplier makes the explosion animation read as roughly
// twice the size its sprite pixels would naturally draw at (on top of,
// not instead of, the usual distance-based perspective scaling every
// world sprite gets — see raster.DrawWorldSprite) — a blast is meant to
// feel big, not just be a literal reproduction of a fairly small sprite.
const explosionSizeMultiplier = 2.0

// SizeMultiplier is the value to pass raster.DrawWorldSprite for p's
// current sprite: 1 (native size) while flying, explosionSizeMultiplier
// once it's exploding.
func (p *Projectile) SizeMultiplier() float64 {
	if p.exploding {
		return explosionSizeMultiplier
	}
	return 1
}

// projectileHitsWall reports whether (x, y) has crossed into a solid
// linedef, reusing the same per-line wall-opening query player movement
// does (collision.go's anyLineWithin/wallOpeningAt): a one-sided line
// always blocks; a two-sided line blocks only if its two sectors share no
// vertical opening at all. Unlike player collision, this doesn't check the
// projectile's own Z against a step-height tolerance — a flying shot
// either has a way through at some height or it doesn't; see
// game_design.txt for the simplification this implies (a shot can pass
// through a two-sided line's opening regardless of exactly how high up it is).
func (g *Game) projectileHitsWall(x, y float64) bool {
	return g.anyLineWithin(x, y, projectileRadius, func(open wallOpening, _ bool) bool {
		return !open.twoSided || open.top <= open.bottom
	})
}
