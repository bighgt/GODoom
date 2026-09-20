package engine

import (
	"math"

	"twopointfive/bsp"
)

// Ambient particles for liquid sectors — steam off lava, mist over water,
// bubbles in acid — purely cosmetic, gated by config liquidAmbience (see
// Game.liquidAmbience). Precomputed once per level like liquidlights.go's
// static point lights, then drained per-frame with one spawn-rate timer per
// sector, the same accumulator idiom updateRocketTrail uses per-projectile
// (see engine/particles.go).

// liquidAmbientSector is one liquid pool's precomputed bounds/kind/floor
// height, built once by buildLiquidAmbientSectors and mutated (only its
// timer) every frame by updateLiquidAmbience.
type liquidAmbientSector struct {
	secIdx                 int // this sector's index — see spawnLiquidAmbientParticle
	minX, minY, maxX, maxY float64
	kind                   int     // liqWater/liqLava/liqAcid — liquidlights.go
	floorZ                 float64 // representative floor height (bbox-centre sample)
	timer                  float64 // seconds until this sector's next spawn
}

// maxLiquidAmbientSectors caps how many liquid sectors get an ambient
// emitter, same reasoning as liquidlights.go's maxLiquidLights: a
// slaughtermap-sized level shouldn't make the per-frame cull loop unbounded.
const maxLiquidAmbientSectors = 200

// buildLiquidAmbientSectors rebuilds g.liquidAmbientSectors for the current
// level — call once per level load (see maplights.go's buildStaticLights,
// which calls this right alongside appendLiquidLights). Liquid sectors
// don't move, so this never needs to run mid-level.
func (g *Game) buildLiquidAmbientSectors() {
	g.liquidAmbientSectors = g.liquidAmbientSectors[:0]
	if g.Level == nil || len(g.Level.Sectors) == 0 {
		return
	}
	g.ensureSectorIndex()
	for si := range g.Level.Sectors {
		if len(g.liquidAmbientSectors) >= maxLiquidAmbientSectors {
			break
		}
		s := &g.Level.Sectors[si]
		kind := liquidKindOfFlat(s.FloorTexture)
		if kind != liqWater && kind != liqLava && kind != liqAcid {
			continue
		}
		if int(s.CeilingHeight) <= int(s.FloorHeight) {
			continue // closed/degenerate sector — no visible surface
		}
		minX, minY, maxX, maxY, ok := g.sectorBBox(si)
		if !ok {
			continue
		}
		if w, h := maxX-minX, maxY-minY; w < 24 || h < 24 {
			continue // thin liquid trim strip — would just add noise
		}
		cx, cy := (minX+maxX)/2, (minY+maxY)/2
		g.liquidAmbientSectors = append(g.liquidAmbientSectors, liquidAmbientSector{
			secIdx: si,
			minX:   minX, minY: minY, maxX: maxX, maxY: maxY,
			kind: kind, floorZ: s.FloorZAt(cx, cy),
		})
	}
}

// liquidAmbienceCullDist/CullZ skip a liquid sector entirely once it's too
// far (or too many floors away, catching room-over-room false positives) to
// plausibly read on screen — cheap distance/height checks, no frustum test,
// since the loop is already bounded by maxLiquidAmbientSectors.
const (
	liquidAmbienceCullDist = 1400.0
	liquidAmbienceCullZ    = 400.0
)

// liquidAmbientIntervalMin/Max bound how often one liquid sector spawns an
// ambient particle: a small puddle spawns rarely, a big lake spawns close
// to the minimum — liquidAmbientBigArea is the sector area (map units^2) at
// which the interval bottoms out.
const (
	liquidAmbientIntervalMin = 0.35
	liquidAmbientIntervalMax = 2.2
	liquidAmbientBigArea     = 640.0 * 640.0
)

// liquidAmbientInterval scales the base interval by sector area, then by a
// per-kind multiplier: acid fizzes the most often, lava churns a bit more
// than still water/mist.
func liquidAmbientInterval(kind int, area float64) float64 {
	frac := area / liquidAmbientBigArea
	if frac > 1 {
		frac = 1
	}
	interval := liquidAmbientIntervalMax - frac*(liquidAmbientIntervalMax-liquidAmbientIntervalMin)
	switch kind {
	case liqLava:
		return interval * 0.8
	case liqAcid:
		return interval * 0.6
	default:
		return interval
	}
}

// updateLiquidAmbience drains each nearby liquid sector's spawn timer. A
// no-op (and cheap: len check only) once the feature is off or the level
// has no liquid sectors.
func (g *Game) updateLiquidAmbience(dt float64) {
	if !g.liquidAmbience || len(g.liquidAmbientSectors) == 0 {
		return
	}
	camX, camY, camZ := g.Camera.X, g.Camera.Y, g.Camera.Z
	for i := range g.liquidAmbientSectors {
		s := &g.liquidAmbientSectors[i]
		cx, cy := (s.minX+s.maxX)/2, (s.minY+s.maxY)/2
		dx, dy := cx-camX, cy-camY
		if dx*dx+dy*dy > liquidAmbienceCullDist*liquidAmbienceCullDist {
			continue
		}
		if math.Abs(s.floorZ-camZ) > liquidAmbienceCullZ {
			continue
		}
		area := (s.maxX - s.minX) * (s.maxY - s.minY)
		interval := liquidAmbientInterval(s.kind, area)
		drainSpawnTimer(&s.timer, dt, interval, func() { g.spawnLiquidAmbientParticle(s) })
	}
}

// spawnLiquidAmbientParticle picks a random point in s's bbox (a sample
// landing just outside an L-shaped sector's true polygon is invisible in
// play) AND actually resolves back to s's own sector — Doom levels
// routinely reuse one sector index across several disconnected areas of
// the map (a common vanilla space-saving trick), so a bbox big enough to
// cover an L-shaped or split pool can just as easily cover a stretch of
// unrelated geometry in between. Without this check, a mist/steam/bubble
// puff could land somewhere that isn't part of the liquid at all — exactly
// what read as "splash particles happening away from the water." Gives up
// silently after a few tries rather than ever guessing.
func (g *Game) spawnLiquidAmbientParticle(s *liquidAmbientSector) {
	const maxTries = 6
	for i := 0; i < maxTries; i++ {
		x := s.minX + pRandomF01()*(s.maxX-s.minX)
		y := s.minY + pRandomF01()*(s.maxY-s.minY)
		if bsp.PointSectorIndex(g.BSP, g.Level, float32(x), float32(y)) != s.secIdx {
			continue
		}
		switch s.kind {
		case liqLava:
			g.spawnSteamPuff(x, y, s.floorZ)
		case liqWater:
			g.spawnMistPuff(x, y, s.floorZ)
		case liqAcid:
			g.spawnBubble(x, y, s.floorZ)
		}
		return
	}
}

// Steam (lava): rises fast, clears quickly, pale/cool tint — see
// loadSteamFrames. Real alpha-blended (translucent) like explosion smoke,
// but far fainter — this is meant to read as a wisp, not a cloud.
const (
	steamLifeMin      = 0.6
	steamLifeMax      = 1.1
	steamSizeMin      = 1.0
	steamSizeMax      = 1.8
	steamLateralSpeed = 6.0
	steamRiseSpeed    = 40.0
	steamGravity      = -55.0 // strongly buoyant — boils off fast
	steamLight        = 1.1
	steamAlpha0       = 0.35
	steamHold         = 0.05
)

func (g *Game) spawnSteamPuff(x, y, z float64) {
	frames := loadSteamFrames()
	if len(frames) == 0 {
		return
	}
	dx, dy := yawSpeedXY(0, steamLateralSpeed)
	g.particles = append(g.particles, &Particle{
		X: x, Y: y, Z: z,
		DX: dx, DY: dy,
		DZ:            steamRiseSpeed * (0.6 + 0.4*pRandomF01()),
		life:          randRange(steamLifeMin, steamLifeMax),
		sizeMul:       randRange(steamSizeMin, steamSizeMax),
		framePhase:    pRandom(),
		frameRate:     smokeFrameRate,
		gravity:       steamGravity,
		light:         steamLight,
		translucent:   true,
		alpha0:        steamAlpha0,
		hold:          steamHold,
		clearBrighten: 1,
		frames:        frames,
	})
}

// Mist (water): a thin haze clinging low over the surface — barely rises,
// spreads wide, and is fainter still than steam. See loadMistFrames.
const (
	mistLifeMin      = 1.2
	mistLifeMax      = 2.0
	mistSizeMin      = 1.8
	mistSizeMax      = 3.0
	mistLateralSpeed = 10.0
	mistRiseSpeed    = 4.0
	mistGravity      = -6.0 // barely buoyant — hugs the surface
	mistLight        = 0.9
	mistAlpha0       = 0.16
	mistHold         = 0.1
)

func (g *Game) spawnMistPuff(x, y, z float64) {
	frames := loadMistFrames()
	if len(frames) == 0 {
		return
	}
	dx, dy := yawSpeedXY(0, mistLateralSpeed)
	g.particles = append(g.particles, &Particle{
		X: x, Y: y, Z: z,
		DX: dx, DY: dy,
		DZ:            randRange(0, mistRiseSpeed),
		life:          randRange(mistLifeMin, mistLifeMax),
		sizeMul:       randRange(mistSizeMin, mistSizeMax),
		framePhase:    pRandom(),
		frameRate:     smokeFrameRate * 0.6, // slower than steam — a lazy drift, not a churn
		gravity:       mistGravity,
		light:         mistLight,
		translucent:   true,
		alpha0:        mistAlpha0,
		hold:          mistHold,
		clearBrighten: 1,
		frames:        frames,
	})
}

// Bubble (acid): small, quick, additive glint — reuses the tinted spark art
// (loadBubbleFrames). No gravity: it rises at a roughly constant speed and
// simply expires at the end of its short life, standing in for "popping"
// with no extra state needed.
const (
	bubbleLifeMin   = 0.25
	bubbleLifeMax   = 0.45
	bubbleSizeMin   = 0.15
	bubbleSizeMax   = 0.3
	bubbleRiseSpeed = 18.0
)

func (g *Game) spawnBubble(x, y, z float64) {
	frames := loadBubbleFrames()
	if len(frames) == 0 {
		return
	}
	g.particles = append(g.particles, &Particle{
		X: x, Y: y, Z: z,
		DZ:         bubbleRiseSpeed * (0.7 + 0.3*pRandomF01()),
		life:       randRange(bubbleLifeMin, bubbleLifeMax),
		sizeMul:    randRange(bubbleSizeMin, bubbleSizeMax),
		framePhase: pRandom(),
		frameRate:  particleFrameRate,
		light:      1,
		fullBright: true,
		frames:     frames,
	})
}
