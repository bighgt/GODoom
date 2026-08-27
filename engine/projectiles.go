package engine

import (
	"math"

	"twopointfive/raster"
)

// Projectile tuning. There's no per-weapon distinction yet (a real rocket,
// plasma bolt, and hitscan bullet all behave the same way here) — see
// game_design.txt for that as noted future work.
const (
	projectileSpeed         = 1000.0 // map units/sec
	projectileRadius        = 4.0    // for wall-hit testing
	projectileMaxRange      = 4000.0
	projectileStartDist     = 20.0 // spawn this far in front of the camera, clear of the weapon viewmodel/camera itself
	projectileBillboardSize = 12.0 // world-unit size of the drawn marker (see raster.DrawBillboard)
)

// projectileColor is a bright orange-yellow — reads clearly as "a shot in
// flight" against most wall/flat textures without needing a real sprite.
var projectileColor = [3]byte{255, 200, 60}

// Projectile is a simple visible shot in flight: a world-space point moving
// in a straight line (including vertically, so aiming up/down with
// mouselook genuinely changes its trajectory) until it travels out of
// range or hits a wall.
type Projectile struct {
	X, Y, Z    float64
	DX, DY, DZ float64 // unit direction
	traveled   float64
	Alive      bool
}

// spawnProjectile fires one from the camera's current position along its
// facing direction (yaw and pitch both), offset slightly ahead so it
// doesn't immediately collide with geometry right at the camera.
func (g *Game) spawnProjectile() {
	dx, dy, dz := aimDirection(g.Camera)
	g.projectiles = append(g.projectiles, &Projectile{
		X:  g.Camera.X + dx*projectileStartDist,
		Y:  g.Camera.Y + dy*projectileStartDist,
		Z:  g.Camera.Z + dz*projectileStartDist,
		DX: dx, DY: dy, DZ: dz,
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

// updateProjectiles advances every live projectile by dt seconds, killing
// it on a wall hit or once it's traveled past projectileMaxRange, and
// compacts the slice to just what's still alive.
func (g *Game) updateProjectiles(dt float64) {
	if len(g.projectiles) == 0 {
		return
	}
	alive := g.projectiles[:0]
	step := projectileSpeed * dt
	for _, p := range g.projectiles {
		nx, ny, nz := p.X+p.DX*step, p.Y+p.DY*step, p.Z+p.DZ*step
		p.traveled += step

		switch {
		case g.projectileHitsWall(nx, ny):
			p.Alive = false
		case p.traveled >= projectileMaxRange:
			p.Alive = false
		default:
			p.X, p.Y, p.Z = nx, ny, nz
		}

		if p.Alive {
			alive = append(alive, p)
		}
	}
	g.projectiles = alive
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
