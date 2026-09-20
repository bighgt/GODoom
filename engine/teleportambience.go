package engine

import (
	"math"
	"strings"

	"twopointfive/bsp"
)

// Ambient rising glitter for teleporter-pad sectors (GATE1-4 floor flats —
// see isTeleportFlat below and worldgeo.KindTeleport, which replaces the
// pad's own flat texture with a matching procedural shimmer in the
// hardware renderer's shader). Precomputed once per level like
// liquidambience.go's own liquid sectors, then drained per-frame with one
// spawn-rate timer per pad — small blue/white motes that drift steadily
// upward out of the pad and fade well before reaching a normal room's
// ceiling, since a flat floor shader alone can't show genuine upward
// motion the way real particles can. Purely cosmetic, gated by config
// liquidAmbience — the same toggle already governing every other
// floor-sourced ambient particle effect.

// isTeleportFlat mirrors worldgeo's own (unexported, so not importable)
// classifier — the two packages already keep independent liquid/teleport
// flat classifiers for the same reason liquidlights.go's liquidKindOfFlat
// is its own copy rather than shared with worldgeo's.
func isTeleportFlat(name string) bool {
	return strings.HasPrefix(name, "GATE")
}

// teleportAmbientSector is one teleporter pad's precomputed bounds/floor
// height, built once by buildTeleportAmbientSectors and mutated (only its
// timer) every frame by updateTeleportAmbience.
type teleportAmbientSector struct {
	secIdx                 int // this sector's index — see spawnTeleportGlitter
	minX, minY, maxX, maxY float64
	floorZ                 float64
	timer                  float64
}

// maxTeleportAmbientSectors caps how many teleporter pads get an ambient
// emitter, same reasoning as liquidAmbientSector's own cap.
const maxTeleportAmbientSectors = 200

// buildTeleportAmbientSectors rebuilds g.teleportAmbientSectors for the
// current level — call once per level load, alongside
// buildLiquidAmbientSectors (see maplights.go's buildStaticLights).
// Teleporter pads don't move, so this never needs to run mid-level.
// Unlike buildLiquidAmbientSectors this does NOT skip a small sector — a
// teleporter pad is typically a single tile, exactly the size a liquid
// pool's own "thin trim strip" filter would exclude.
func (g *Game) buildTeleportAmbientSectors() {
	g.teleportAmbientSectors = g.teleportAmbientSectors[:0]
	if g.Level == nil || len(g.Level.Sectors) == 0 {
		return
	}
	g.ensureSectorIndex()
	for si := range g.Level.Sectors {
		if len(g.teleportAmbientSectors) >= maxTeleportAmbientSectors {
			break
		}
		s := &g.Level.Sectors[si]
		if !isTeleportFlat(s.FloorTexture) {
			continue
		}
		if int(s.CeilingHeight) <= int(s.FloorHeight) {
			continue // closed/degenerate sector — no room for anything to rise into
		}
		minX, minY, maxX, maxY, ok := g.sectorBBox(si)
		if !ok {
			continue
		}
		cx, cy := (minX+maxX)/2, (minY+maxY)/2
		g.teleportAmbientSectors = append(g.teleportAmbientSectors, teleportAmbientSector{
			secIdx: si,
			minX:   minX, minY: minY, maxX: maxX, maxY: maxY,
			floorZ: s.FloorZAt(cx, cy),
		})
	}
}

// teleportAmbienceCullDist/CullZ skip a pad entirely once it's too far (or
// too many floors away) to plausibly read on screen — same reasoning as
// liquidAmbienceCullDist/CullZ.
const (
	teleportAmbienceCullDist = 1400.0
	teleportAmbienceCullZ    = 400.0
)

// teleportGlitterIntervalMin/Max bound how often one pad's timer fires;
// teleportGlitterBurstCount is how many motes each firing spawns at once —
// scaling density through the burst size rather than shrinking the
// interval further, since drainSpawnTimer's own catch-up cap (4 per call)
// would otherwise silently ceiling how many actually spawn once the
// interval drops below a few frames' worth of dt. 100 motes/firing at this
// interval reads as a dense, continuous stream rather than the sparse trail
// of individual sparks it started as.
const (
	teleportGlitterIntervalMin = 0.03
	teleportGlitterIntervalMax = 0.09
	teleportGlitterBurstCount  = 100
)

// updateTeleportAmbience drains each nearby teleporter pad's spawn timer. A
// no-op (cheap: len check only) once the feature is off or the level has no
// teleporter pads.
func (g *Game) updateTeleportAmbience(dt float64) {
	if !g.liquidAmbience || len(g.teleportAmbientSectors) == 0 {
		return
	}
	camX, camY, camZ := g.Camera.X, g.Camera.Y, g.Camera.Z
	for i := range g.teleportAmbientSectors {
		s := &g.teleportAmbientSectors[i]
		cx, cy := (s.minX+s.maxX)/2, (s.minY+s.maxY)/2
		dx, dy := cx-camX, cy-camY
		if dx*dx+dy*dy > teleportAmbienceCullDist*teleportAmbienceCullDist {
			continue
		}
		if math.Abs(s.floorZ-camZ) > teleportAmbienceCullZ {
			continue
		}
		drainSpawnTimer(&s.timer, dt, randRange(teleportGlitterIntervalMin, teleportGlitterIntervalMax), func() { g.spawnTeleportGlitter(s) })
	}
}

// spawnTeleportGlitter spawns teleportGlitterBurstCount motes, each at its
// own random point in s's bbox — picked AND actually resolved back to s's
// own sector, same reasoning (and same retry-then-give-up shape) as
// liquidambience.go's spawnLiquidAmbientParticle: a Doom level can reuse
// one sector index across several disconnected areas of the map, so a bbox
// big enough to cover an irregular pad can just as easily cover a stretch
// of unrelated geometry in between.
func (g *Game) spawnTeleportGlitter(s *teleportAmbientSector) {
	const maxTries = 6
	for n := 0; n < teleportGlitterBurstCount; n++ {
		for i := 0; i < maxTries; i++ {
			x := s.minX + pRandomF01()*(s.maxX-s.minX)
			y := s.minY + pRandomF01()*(s.maxY-s.minY)
			if bsp.PointSectorIndex(g.BSP, g.Level, float32(x), float32(y)) != s.secIdx {
				continue
			}
			g.spawnTeleportMote(x, y, s.floorZ)
			break
		}
	}
}

// teleportGlitterLifeMin/Max/RiseSpeed: long enough, and fast enough, to
// visibly climb a good stretch of a normal room's height before fading —
// "constantly travelling upwards", not a quick pop. gravity is a small
// negative (buoyant) value so a mote holds a near-steady rise rate instead
// of slowing down, the way a spark's real gravity would. teleportGlitterLateral
// gives each mote a little sideways drift so a burst of them reads as
// several loose rising streams, not one rigid vertical column.
const (
	teleportGlitterLifeMin   = 1.4
	teleportGlitterLifeMax   = 2.4
	teleportGlitterSizeMin   = 0.5
	teleportGlitterSizeMax   = 1.0
	teleportGlitterRiseSpeed = 100.0
	teleportGlitterLateral   = 8.0
	teleportGlitterGravity   = -6.0
	teleportGlitterLight     = 1.8
)

func (g *Game) spawnTeleportMote(x, y, z float64) {
	frames := loadTeleportGlitterFrames()
	if len(frames) == 0 {
		return
	}
	dx, dy := yawSpeedXY(0, teleportGlitterLateral)
	g.particles = append(g.particles, &Particle{
		X: x, Y: y, Z: z,
		DX:         dx,
		DY:         dy,
		DZ:         teleportGlitterRiseSpeed * (0.7 + 0.3*pRandomF01()),
		life:       randRange(teleportGlitterLifeMin, teleportGlitterLifeMax),
		sizeMul:    randRange(teleportGlitterSizeMin, teleportGlitterSizeMax),
		framePhase: pRandom(),
		frameRate:  particleFrameRate,
		gravity:    teleportGlitterGravity,
		light:      teleportGlitterLight,
		fullBright: true,
		frames:     frames,
	})
}
