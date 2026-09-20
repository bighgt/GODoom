package engine

import "math"

// Ambient bubbling/off-gassing for intact MT_BARREL mobjs — the toxic waste
// they're full of visibly stirs on its own even before anything ignites it,
// reusing the same green-tinted art the burning-puddle/acid effects already
// use (loadBubbleFrames/loadToxicSmokeFrames) rather than dedicated art.
// Purely cosmetic, gated by config explosionParticles like every other
// barrel/explosion-adjacent particle effect in this package (see
// engine/burningpuddle.go's own such gate) — a barrel is exactly the kind
// of thing that toggle already governs.
//
// Unlike liquidAmbientSector (engine/liquidambience.go, built once per
// level since liquid sectors never move), a barrel is an ordinary Mobj
// that can spawn, die, or explode mid-level, so this walks g.mobjs
// directly every tic rather than keeping its own precomputed list —
// Mobj.barrelBubbleTimer/barrelFogTimer carry each barrel's own timer
// state, the same reason Projectile keeps its own flameTrailTimer/
// smokeTrailTimer rather than a side table keyed by pointer.

// barrelBubbleIntervalMin/Max bound how often one barrel pops a bubble at
// its top — fast enough to read as liquid audibly working away inside a
// sealed drum, slow enough that a room full of barrels doesn't turn into a
// constant fizz. barrelFogIntervalMin/Max are the same idea for the far
// sparser wisps of vented gas.
const (
	barrelBubbleIntervalMin = 0.5
	barrelBubbleIntervalMax = 1.4
	barrelFogIntervalMin    = 1.4
	barrelFogIntervalMax    = 3.0
)

// updateBarrelAmbience drains every live, not-yet-exploding barrel's own
// bubble/fog timers. Cheap even with none live or the feature off — a
// single flag check, then a plain type/state compare per mobj for anything
// that isn't a still-intact barrel.
func (g *Game) updateBarrelAmbience(dt float64) {
	if !g.explosionParticles {
		return
	}
	for _, mo := range g.mobjs {
		if mo == nil || mo.removed || mo.Type != MT_BARREL {
			continue
		}
		if mo.State != S_BAR1 && mo.State != S_BAR2 {
			continue // exploding or dead — see aExplode/S_BEXP1..S_BEXP5
		}
		// mo.Z + mo.Height is the same "top of this mobj" convention used
		// elsewhere (see eyeZ, maplights.go) — with the voxel model itself
		// tuned to roughly fill that same box, this is where the barrel's
		// lid actually is.
		top := mo.Z + mo.Height
		mx, my := mo.X, mo.Y
		drainSpawnTimer(&mo.barrelBubbleTimer, dt, randRange(barrelBubbleIntervalMin, barrelBubbleIntervalMax), func() {
			g.spawnBarrelBubble(mx, my, top, mo.Radius)
		})
		drainSpawnTimer(&mo.barrelFogTimer, dt, randRange(barrelFogIntervalMin, barrelFogIntervalMax), func() {
			g.spawnBarrelFog(mx, my, top, mo.Radius)
		})
	}
}

// barrelBubbleLifeMin/Max/Size*/RiseSpeed: a small, quick pop right at the
// surface — barely rises before it's gone, unlike acid's own bubble (which
// has open water to travel up through, see liquidambience.go's own
// spawnBubble). radius scales the landing spot's spread so it samples the
// barrel's own lid instead of every bubble popping from a single point.
const (
	barrelBubbleLifeMin   = 0.2
	barrelBubbleLifeMax   = 0.35
	barrelBubbleSizeMin   = 0.1
	barrelBubbleSizeMax   = 0.2
	barrelBubbleRiseSpeed = 6.0
)

func (g *Game) spawnBarrelBubble(x, y, z, radius float64) {
	frames := loadBubbleFrames()
	if len(frames) == 0 {
		return
	}
	bx, by := barrelLidSamplePoint(x, y, radius)
	g.particles = append(g.particles, &Particle{
		X: bx, Y: by, Z: z,
		DZ:         barrelBubbleRiseSpeed * (0.6 + 0.4*pRandomF01()),
		life:       randRange(barrelBubbleLifeMin, barrelBubbleLifeMax),
		sizeMul:    randRange(barrelBubbleSizeMin, barrelBubbleSizeMax),
		framePhase: pRandom(),
		frameRate:  particleFrameRate,
		light:      1,
		fullBright: true,
		frames:     frames,
	})
}

// barrelFogLifeMin/Max/Size*/RiseSpeed/Alpha0: a faint, thin wisp — "slight
// amounts" per the effect's own brief, nowhere near the burning-puddle
// fire's thick toxic smoke (loadToxicSmokeFrames' own doc comment) despite
// reusing the same dark-green art. Barely buoyant and quick to clear
// (barrelFogHold), so it reads as gas venting off a sealed lid rather than
// a cloud building up.
const (
	barrelFogLifeMin   = 1.0
	barrelFogLifeMax   = 1.8
	barrelFogSizeMin   = 1.0
	barrelFogSizeMax   = 1.7
	barrelFogRiseSpeed = 5.0
	barrelFogGravity   = -8.0
	barrelFogLight     = 0.8
	barrelFogAlpha0    = 0.16
	barrelFogHold      = 0.1
)

func (g *Game) spawnBarrelFog(x, y, z, radius float64) {
	frames := loadToxicSmokeFrames()
	if len(frames) == 0 {
		return
	}
	fx, fy := barrelLidSamplePoint(x, y, radius)
	g.particles = append(g.particles, &Particle{
		X: fx, Y: fy, Z: z,
		DZ:            barrelFogRiseSpeed * (0.5 + 0.5*pRandomF01()),
		life:          randRange(barrelFogLifeMin, barrelFogLifeMax),
		sizeMul:       randRange(barrelFogSizeMin, barrelFogSizeMax),
		framePhase:    pRandom(),
		frameRate:     smokeFrameRate,
		gravity:       barrelFogGravity,
		light:         barrelFogLight,
		translucent:   true,
		alpha0:        barrelFogAlpha0,
		hold:          barrelFogHold,
		clearBrighten: 1,
		frames:        frames,
	})
}

// barrelLidSamplePoint picks a random point over the barrel's own lid
// (sqrt-scaled so samples spread evenly over the AREA, not bunched toward
// the centre — the same reasoning as burningpuddle.go's puddleSamplePoint)
// rather than every bubble/wisp originating from the exact same (x, y).
// Kept within 70% of radius so nothing spawns hanging off the visible edge.
func barrelLidSamplePoint(x, y, radius float64) (sx, sy float64) {
	ang := pRandomF01() * 2 * math.Pi
	dist := math.Sqrt(pRandomF01()) * radius * 0.7
	return x + math.Cos(ang)*dist, y + math.Sin(ang)*dist
}
