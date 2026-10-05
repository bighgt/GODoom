package engine

import (
	"math"

	"twopointfive/render"
)

// Lost-soul flames (config lostSoulFire, default on): a living lost soul
// sheds the rendered imp fireball's fire (fireballCoreFrames — see
// impfireball.go) off the back of its skull, so a 3D model trails real
// flames the way the sprite's painted ones suggest, and carries a
// flickering fire light. Idle, the licks drift back and rise; charging
// (MF_SKULLFLY) they come thicker and are left behind as a streak.
// Purely cosmetic.

const (
	soulFireEvery       = 0.02  // seconds between licks, floating
	soulFireChargeEvery = 0.007 // ...and charging
	soulFireBack        = 10.0  // map units behind the skull's centre (the back of the pack model's head)
	soulFireUp          = 30.0  // above the mobj's Z (the pack model's zoffset 28, upper skull)
)

// lostSoulBurning reports whether mo is a lost soul whose flames show.
func (g *Game) lostSoulBurning(mo *Mobj) bool {
	return g.lostSoulFire && mo.Type == MT_SKULL && mo.Health > 0 && !mo.removed && mo.State != S_NULL
}

// lostSoulTrail sheds this frame's flame licks off the back of burning
// lost soul mo (updateMobjFire, impfireball.go, drives it).
func (g *Game) lostSoulTrail(mo *Mobj, frac, dt float64) {
	k := g.ModelScale
	if k <= 0 {
		k = 1
	}
	x, y, z, ang := mo.renderState(frac)
	bx, by := -math.Cos(ang), -math.Sin(ang) // "behind" = away from the face
	every, push, life := soulFireEvery, 30.0, 0.0
	if mo.Flags&MF_SKULLFLY != 0 { // charging: thicker, streaming opposite the flight
		if sp := math.Hypot(mo.MomX, mo.MomY); sp > 0 {
			bx, by = -mo.MomX/sp, -mo.MomY/sp
		}
		every, push, life = soulFireChargeEvery, 10, 0.1
	}
	ox, oy, oz := x+bx*soulFireBack*k, y+by*soulFireBack*k, z+soulFireUp*k
	core := fireballCoreFrames()
	drainSpawnTimer(&mo.fxTimer, dt, every, func() {
		g.particles = append(g.particles, newPooledParticle(Particle{
			X: ox + fxRange(-5, 5)*k, Y: oy + fxRange(-5, 5)*k, Z: oz + fxRange(-4, 6)*k,
			DX:         bx*fxRange(push*0.6, push) + fxRange(-10, 10),
			DY:         by*fxRange(push*0.6, push) + fxRange(-10, 10),
			DZ:         fxRange(25, 55),
			life:       fxRange(0.22, 0.42) + life,
			sizeMul:    fxRange(0.35, 0.7) * k,
			framePhase: fxRand.Intn(impCoreFrames),
			frameRate:  impFrameRate,
			gravity:    -50, // flames rise
			light:      1,
			fullBright: true,
			frames:     core,
		}))
	})
}

// lostSoulLight is the flickering glow a burning lost soul throws.
func (g *Game) lostSoulLight(mo *Mobj, px, py, pz float64) (render.Light, bool) {
	if !g.lostSoulBurning(mo) {
		return render.Light{}, false
	}
	t := g.renderClock*11 + float64(mo.fxPhase)
	flicker := float32(0.85 + 0.1*math.Sin(t) + 0.05*math.Sin(t*2.7+1))
	return render.Light{
		X: float32(px), Y: float32(py), Z: float32(pz + soulFireUp),
		Radius: 110, R: 1.0, G: 0.62, B: 0.25, Intensity: 0.9 * flicker,
	}, true
}
