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
// spawn-rate timer per pad — small multi-coloured motes (see
// loadTeleportGlitterColorVariants' disco palette, and Particle.pulseHz
// for the size-pulse each one breathes with) that drift steadily upward
// out of the pad and fade well before reaching a normal room's ceiling,
// since a flat floor shader alone can't show genuine upward motion the way
// real particles can. Purely cosmetic, gated by config liquidAmbience —
// the same toggle already governing every other floor-sourced ambient
// particle effect.

// isTeleportFlat mirrors worldgeo's own (unexported, so not importable)
// classifier — the two packages already keep independent liquid/teleport
// flat classifiers for the same reason liquidlights.go's liquidKindOfFlat
// is its own copy rather than shared with worldgeo's.
func isTeleportFlat(name string) bool {
	return strings.HasPrefix(name, "GATE")
}

// maxTeleportLights caps how many teleporter pads contribute a glow light,
// same reasoning as liquidlights.go's maxLiquidLights.
const maxTeleportLights = 64

// appendTeleportLights adds a faint white/blue glow light for every
// teleporter-pad sector, the same way appendLiquidLights seeds lava/acid
// their own glow — vanilla Doom's GATE flat throws no light of its own, so
// without this the pad's new shimmering surface would light nothing around
// it. Always on (not gated by config liquidAmbience, which governs the
// cosmetic rising-glitter particles only): a real light source, same
// treatment lava/acid's own always-on glow gets. firstSeed is the flicker
// wobble seed for the first one added; it returns the next unused seed.
func (g *Game) appendTeleportLights(firstSeed int) int {
	if g.Level == nil || len(g.Level.Sectors) == 0 {
		return firstSeed
	}
	g.ensureSectorIndex()
	seed := firstSeed
	added := 0

	for si := range g.Level.Sectors {
		if added >= maxTeleportLights {
			break
		}
		s := &g.Level.Sectors[si]
		if !isTeleportFlat(s.FloorTexture) {
			continue
		}
		if int(s.CeilingHeight) <= int(s.FloorHeight) {
			continue // closed/degenerate sector — no visible surface to glow
		}
		minX, minY, maxX, maxY, ok := g.sectorBBox(si)
		if !ok {
			continue
		}
		w, h := maxX-minX, maxY-minY
		span := float32(math.Hypot(w, h))
		radius := teleportLightBaseRadius + 0.20*span
		if radius > teleportLightBaseRadius*1.9 {
			radius = teleportLightBaseRadius * 1.9
		}
		for _, p := range sampleSectorPoints(minX, minY, maxX, maxY) {
			if added >= maxTeleportLights {
				break
			}
			// Raised well above the floor (unlike liquidlights.go's own
			// +16) so the glow actually reaches nearby walls/ceiling
			// instead of just grazing the floor around the pad — the
			// point of this is lighting the room, not the pad itself
			// (the pad already has its own shimmer shader).
			z := s.FloorZAt(p[0], p[1]) + teleportLightHeight
			g.staticLights = append(g.staticLights, staticLight{
				x: p[0], y: p[1], z: z,
				r: teleportLightR, g: teleportLightG, b: teleportLightB,
				baseInten: teleportLightIntensity, baseRadius: radius,
				sector: -1, flicker: true, seed: seed,
			})
			seed++
			added++
		}
	}
	return seed
}

// teleportLightR/G/B/Intensity/BaseRadius/Height: a faint, cool white/blue
// glow — dimmer than lava's own (1.7) since this is meant to read as
// ambient shimmer spilling into the room, not a dramatic light source in
// its own right.
const (
	teleportLightR          = 0.55
	teleportLightG          = 0.75
	teleportLightB          = 1.0
	teleportLightIntensity  = 0.6
	teleportLightBaseRadius = 200.0
	teleportLightHeight     = 48.0
)

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
	cx, cy := (s.minX+s.maxX)/2, (s.minY+s.maxY)/2
	const maxTries = 6
	for n := 0; n < teleportGlitterBurstCount; n++ {
		for i := 0; i < maxTries; i++ {
			x := s.minX + pRandomF01()*(s.maxX-s.minX)
			y := s.minY + pRandomF01()*(s.maxY-s.minY)
			if bsp.PointSectorIndex(g.BSP, g.Level, float32(x), float32(y)) != s.secIdx {
				continue
			}
			g.spawnTeleportMote(x, y, s.floorZ, cx, cy)
			break
		}
	}
}

// teleportGlitterLifeMin/Max/RiseSpeed: long enough, and fast enough, to
// visibly climb a good stretch of a normal room's height before fading —
// "constantly travelling upwards", not a quick pop. gravity is a small
// negative (buoyant) value so a mote holds a near-steady rise rate instead
// of slowing down, the way a spark's real gravity would.
// teleportGlitterOrbitSpeedMin/Max bound how fast a mote circles the pad's
// own centre as it rises (see Particle.orbitSpeed) — always the same sign
// (positive: counter-clockwise from above) so a whole burst reads as one
// coherent rising vortex rather than a scatter of independently-spinning
// motes.
const (
	teleportGlitterLifeMin       = 1.4
	teleportGlitterLifeMax       = 2.4
	teleportGlitterSizeMin       = 0.5
	teleportGlitterSizeMax       = 1.0
	teleportGlitterRiseSpeed     = 100.0
	teleportGlitterGravity       = -6.0
	teleportGlitterLight         = 1.8
	teleportGlitterOrbitSpeedMin = 1.0
	teleportGlitterOrbitSpeedMax = 2.5
	// teleportGlitterPulseHzMin/Max/Amp: each mote's own SIZE breathes at
	// its own random rate within this range (Particle.pulseHz — see its
	// own doc comment on why size, not light) — a whole burst of
	// differently-phased, differently-rated pulses reads as a shimmering
	// "disco light" flicker rather than a uniform, boring throb. Amp 0.35
	// means a mote swells to 1.35x and shrinks to 0.65x its base size —
	// a clearly visible pulse without vanishing at the trough.
	teleportGlitterPulseHzMin = 1.5
	teleportGlitterPulseHzMax = 4.0
	teleportGlitterPulseAmp   = 0.35
)

// spawnTeleportMote spawns one glitter particle at (x, y, z), orbiting
// (cx, cy) — the pad's own centre — at its own current distance from that
// centre, so it keeps spiralling around roughly the same radius it started
// at rather than snapping to a shared ring. frameRate: 0 pins it to
// whichever of loadTeleportGlitterColorVariants' length variants
// framePhase picked for its whole life (see that function's own doc
// comment) instead of animating through them; the colour variant itself
// (which of the disco palette's hues this mote's tail is) is picked once
// here, independently per mote, so a burst reads as a genuine scatter of
// colours rather than one flat tint.
func (g *Game) spawnTeleportMote(x, y, z, cx, cy float64) {
	variants := loadTeleportGlitterColorVariants()
	if len(variants) == 0 {
		return
	}
	frames := variants[pRandom()%len(variants)]
	dx0, dy0 := x-cx, y-cy
	radius := math.Hypot(dx0, dy0)
	phase0 := math.Atan2(dy0, dx0)
	g.particles = append(g.particles, &Particle{
		X: x, Y: y, Z: z,
		DZ:           teleportGlitterRiseSpeed * (0.7 + 0.3*pRandomF01()),
		life:         randRange(teleportGlitterLifeMin, teleportGlitterLifeMax),
		sizeMul:      randRange(teleportGlitterSizeMin, teleportGlitterSizeMax),
		framePhase:   pRandom(),
		frameRate:    0,
		gravity:      teleportGlitterGravity,
		light:        teleportGlitterLight,
		fullBright:   true,
		frames:       frames,
		orbitCenterX: cx,
		orbitCenterY: cy,
		orbitRadius:  radius,
		orbitPhase0:  phase0,
		orbitSpeed:   randRange(teleportGlitterOrbitSpeedMin, teleportGlitterOrbitSpeedMax),
		pulseHz:      randRange(teleportGlitterPulseHzMin, teleportGlitterPulseHzMax),
		pulseAmp:     teleportGlitterPulseAmp,
		pulsePhase:   pRandomF01() * 2 * math.Pi,
	})
}
