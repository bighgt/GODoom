package engine

import (
	"fmt"
	"math"

	"twopointfive/raster"
)

// projectileRadius/projectileStartDist are shared shot-flight tuning that
// doesn't vary per weapon (each weapon's own ProjectileDef — see
// weapons.go — carries what does: sprite, speed, explosion animation/sound).
const (
	projectileRadius    = 4.0  // for wall-hit testing
	projectileStartDist = 20.0 // spawn this far in front of the camera, clear of the weapon viewmodel/camera itself
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
}

// spawnProjectile fires one from the camera's current position along its
// facing direction (yaw and pitch both), offset slightly ahead so it
// doesn't immediately collide with geometry right at the camera.
func (g *Game) spawnProjectile(def *ProjectileDef) {
	dx, dy, dz := aimDirection(g.Camera)
	g.projectiles = append(g.projectiles, &Projectile{
		X:  g.Camera.X + dx*projectileStartDist,
		Y:  g.Camera.Y + dy*projectileStartDist,
		Z:  g.Camera.Z + dz*projectileStartDist,
		DX: dx, DY: dy, DZ: dz,
		def:   def,
		Alive: true,
	})
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
// flying one moves and, on hitting a wall, freezes in place and starts
// its explosion animation (playing its ExplodeSound once, right then); an
// exploding one just counts through that animation until it finishes, at
// which point it's removed. Compacts the slice to just what's still alive.
func (g *Game) updateProjectiles(dt float64) {
	if len(g.projectiles) == 0 {
		return
	}
	alive := g.projectiles[:0]
	for _, p := range g.projectiles {
		if p.exploding {
			p.explodeElapsed += dt
			if _, ok := p.explosionFrame(); !ok {
				p.Alive = false
			}
		} else {
			step := p.def.Speed * dt
			nx, ny := p.X+p.DX*step, p.Y+p.DY*step
			if g.projectileHitsWall(nx, ny) {
				p.exploding = true
				p.explodeElapsed = 0
				g.playSound(p.def.ExplodeSound)
			} else {
				p.X, p.Y, p.Z = nx, ny, p.Z+p.DZ*step
				p.traveled += step
			}
		}
		if p.Alive {
			alive = append(alive, p)
		}
	}
	g.projectiles = alive
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
	return g.anyLineWithin(x, y, projectileRadius, func(open wallOpening) bool {
		return !open.twoSided || open.top <= open.bottom
	})
}
