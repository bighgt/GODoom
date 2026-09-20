package engine

import "math"

// Player water ripple/wake — a small trailing puff spawned right at the
// player's feet while they're wading through water and actually moving.
// Distinct from liquidambience.go's ambient mist, which drifts at random
// points across the whole pool as atmosphere: this one is always exactly
// where the player is, answering "splashing should track the player, not
// happen randomly elsewhere." Purely cosmetic, gated by config
// liquidAmbience like every other liquid particle effect.

const (
	// waterRippleInterval is the time between ripple puffs while wading —
	// deliberately much more frequent than the ambient mist's own spawn
	// rate (liquidAmbientInterval), since this is meant to read as a
	// continuous wake trailing the player, not an occasional wisp.
	waterRippleInterval = 0.12
	// waterRippleMinSpeed: below this the player reads as standing still —
	// no wake from a stationary player (real water only ripples where
	// something is actually disturbing it).
	waterRippleMinSpeed = 8.0
)

// updatePlayerWaterRipple drains g.waterRippleTimer while the player is in
// water and moving. Called every tic; cheap (a couple of field reads) when
// it doesn't apply.
func (g *Game) updatePlayerWaterRipple(dt float64) {
	if !g.liquidAmbience || g.playerLiquidKind != liqWater {
		g.waterRippleTimer = 0 // reset so re-entering water starts a wake immediately, not mid-interval
		return
	}
	if math.Hypot(g.VelX, g.VelY) < waterRippleMinSpeed {
		return
	}
	drainSpawnTimer(&g.waterRippleTimer, dt, waterRippleInterval, func() {
		g.spawnWaterRipple(g.Camera.X, g.Camera.Y, g.PlayerZ)
	})
}

// Ripple puffs reuse the mist art (loadMistFrames) — quick and skimming
// rather than rising, unlike a lazy drifting ambient mist wisp, but sized
// and lit to be unmistakably a wake trailing the player, not a subtle wisp
// lost against the ambient mist elsewhere in the pool.
const (
	waterRippleLifeMin = 0.35
	waterRippleLifeMax = 0.55
	waterRippleSizeMin = 1.1
	waterRippleSizeMax = 1.7
	waterRippleAlpha0  = 0.55
	waterRippleLight   = 1.6
)

func (g *Game) spawnWaterRipple(x, y, z float64) {
	frames := loadMistFrames()
	if len(frames) == 0 {
		return
	}
	const outSpeed = 22.0
	dx, dy := yawSpeedXYAt(pRandomF01()*2*math.Pi, outSpeed)
	g.particles = append(g.particles, &Particle{
		X: x, Y: y, Z: z,
		DX:            dx,
		DY:            dy,
		life:          randRange(waterRippleLifeMin, waterRippleLifeMax),
		sizeMul:       randRange(waterRippleSizeMin, waterRippleSizeMax),
		framePhase:    pRandom(),
		frameRate:     smokeFrameRate,
		light:         waterRippleLight,
		translucent:   true,
		alpha0:        waterRippleAlpha0,
		clearBrighten: 1,
		frames:        frames,
	})
}
