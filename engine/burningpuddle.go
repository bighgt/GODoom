package engine

import (
	"math"

	"twopointfive/assets"
	"twopointfive/render/worldgeo"
)

// A barrel is full of flammable liquid — after it explodes, some of that
// liquid is left burning on the floor: small ground-hugging flame licks
// with smoke rising off them, spread over a patch around the blast, for a
// random duration before it all burns off. Modeled as a timed continuous
// emitter (like liquidambience.go's ambient mist/steam) rather than one
// long-lived Particle, since Particle only supports a flat fixed lifespan
// and this needs its own flicker/smoke animation running for however long
// this particular puddle takes to burn out. The particles themselves are
// cosmetic, gated by config explosionParticles like every other explosion
// effect — but the fire is a real hazard: see engine/firedamage.go, which
// burns anything standing in it regardless of that setting.

// burningPuddle is one barrel's worth of spilled, still-burning liquid.
type burningPuddle struct {
	x, y, z float64
	// amount is how much liquid was spilled, 0..1 — bigger puddle, spread
	// wider (see burningPuddleMinRadius/MaxRadius) and burns longer (see
	// burningPuddleMinDuration/MaxDuration): a bigger spill takes longer to
	// burn off, same as a real pool of fuel would.
	amount float64
	// ignitionDelay counts down before this splatter's fire/smoke starts
	// emitting at all — see burningPuddleIgnitionDelayMin/Max. The ground
	// decal itself (collectGroundSplats) isn't gated on this: the spilled
	// liquid should be visible on the floor immediately, it just doesn't
	// catch fire until the blast itself has finished.
	ignitionDelay float64
	duration      float64 // total burn time once lit, fixed at spawn — see fade in updatePuddleEmission
	remaining     float64 // seconds left before this puddle is fully burned off, counted down only once lit
	flameTimer    float64
	smokeTimer    float64
	// flameParticles are this puddle's own live flame-lick Particles (see
	// spawnPuddleFlame), tracked so spawnPuddleSmoke can spawn each new
	// smoke puff from wherever one of them currently is — smoke visibly
	// coming off the top of an actual flame — rather than an independently
	// sampled point elsewhere on the puddle. Never pruned: it only grows to
	// roughly (duration / puddleFlameIntervalMin) entries over a puddle's
	// whole lifetime, a few hundred pointers at most, cheap to scan.
	flameParticles []*Particle
	// decalRot is this splatter's ground-puddle rotation, fixed once at
	// spawn (see collectGroundSplats) so its floor decal doesn't visibly
	// spin as it burns down — only its size/alpha should change over time.
	decalRot float64
}

// burningPuddleIgnitionDelayMin/Max: how long spilled liquid sits before it
// catches — the barrel's own blast (sparks/chunks/fire licks) needs to
// finish reading as "the explosion" before the floor around it visibly
// catches fire, rather than the puddle fire and the blast's own fire
// starting in the same instant and blurring together. Randomized per
// splatter so a barrel's several puddles don't all ignite in perfect
// unison.
const (
	burningPuddleIgnitionDelayMin = 0.5
	burningPuddleIgnitionDelayMax = 1.1
)

// burningPuddleMinDuration/MaxDuration: how long a puddle burns (once lit)
// before it's gone entirely, picked once at spawn and scaled by amount.
const (
	burningPuddleMinDuration = 1.5
	burningPuddleMaxDuration = 10.0
)

// burningPuddleMinRadius/MaxRadius: how far one splatter's own liquid
// spreads from where it landed, scaled by its own amount — more liquid in
// that splatter covers more floor.
const (
	burningPuddleMinRadius = 8.0
	burningPuddleMaxRadius = 22.0
)

// burningSplatterMin/Max: how many separate splatters one barrel throws —
// randomized per barrel, but never fewer than 6 nor more than 36, so even
// an unlucky roll still leaves a real spread of fire and a lucky one turns
// into a proper field of it. burningSplatterThrowRadius is how far from the
// blast centre a splatter can land — sqrt-scaled (see spawnBurningPuddle)
// so landings spread evenly over the floor AREA around the blast instead of
// bunching near the centre, the same reasoning as puddleSamplePoint's own
// disc sampling.
const (
	burningSplatterMin         = 6
	burningSplatterMax         = 36
	burningSplatterThrowRadius = 90.0
)

// spawnBurningPuddle scatters a barrel's spilled liquid across the floor
// around the blast as several separate splatters, each landing at its own
// random distance/direction and burning independently (its own random
// amount/duration) — a real explosion throws burning fuel outward in a
// spray, not one neat circle centred on the barrel. Called by aExplode
// (enemy.go) right after a barrel's own explosion burst, never for
// rocket/plasma/BFG impacts (those don't spill anything flammable).
//
// Deliberately NOT gated on config explosionParticles here: that toggle
// governs the cosmetic flame/smoke/decal (see updatePuddleEmission and
// collectGroundSplats, which are gated) but the puddle itself is also a
// real gameplay hazard now (engine/firedamage.go), and a graphics setting
// shouldn't silently remove a damage source. Turning explosionParticles
// off does mean an active fire hazard goes unrendered — an accepted, rare
// tradeoff of that performance setting, consistent with it already
// governing every other particle effect in the game.
func (g *Game) spawnBurningPuddle(x, y, z float64) {
	n := burningSplatterMin + pRandom()%(burningSplatterMax-burningSplatterMin+1)
	for i := 0; i < n; i++ {
		ang := pRandomF01() * 2 * math.Pi
		dist := math.Sqrt(pRandomF01()) * burningSplatterThrowRadius
		sx, sy := x+math.Cos(ang)*dist, y+math.Sin(ang)*dist
		sz, _ := g.sectorFloorCeil(sx, sy) // each splatter sits on its OWN floor height, not the blast's
		amount := pRandomF01()
		duration := burningPuddleMinDuration + amount*(burningPuddleMaxDuration-burningPuddleMinDuration)
		g.burningPuddles = append(g.burningPuddles, &burningPuddle{
			x: sx, y: sy, z: sz,
			amount: amount, duration: duration, remaining: duration,
			ignitionDelay: randRange(burningPuddleIgnitionDelayMin, burningPuddleIgnitionDelayMax),
			decalRot:      pRandomF01() * 2 * math.Pi,
		})
	}
}

// updateBurningPuddles waits out every live puddle's ignitionDelay, then
// drains its remaining burn time, spawning its flame/smoke particles along
// the way, and drops any that have fully burned off. Cheap to call every
// frame even with none live.
func (g *Game) updateBurningPuddles(dt float64) {
	if len(g.burningPuddles) == 0 {
		return
	}
	alive := g.burningPuddles[:0]
	for _, p := range g.burningPuddles {
		if p.ignitionDelay > 0 {
			p.ignitionDelay -= dt
			alive = append(alive, p)
			continue
		}
		p.remaining -= dt
		if p.remaining <= 0 {
			continue
		}
		g.updatePuddleEmission(p, dt)
		alive = append(alive, p)
	}
	g.burningPuddles = alive
}

// puddleFlameIntervalMin/Max bound how often one puddle spawns a flame
// lick — Max at amount=0 (a thin spill, sparse flame), Min at amount=1 (a
// big spill, a denser bed of fire). puddleSmokeIntervalMin/Max are the same
// idea for the smoke rising off it — closer together than the flame's own
// interval would suggest, since each puff now needs to overlap with its
// neighbors to build up a thick, continuous toxic cloud rather than reading
// as separate wisps.
const (
	puddleFlameIntervalMin = 0.07
	puddleFlameIntervalMax = 0.2
	puddleSmokeIntervalMin = 0.15
	puddleSmokeIntervalMax = 0.4
	// puddleDyingIntervalMul is how much slower both spawn rates get by the
	// very end of the burn (see puddleFade) — the fire thins out and
	// quiets down gradually instead of burning at full strength right up
	// until it's simply gone.
	puddleDyingIntervalMul = 4.0
)

// puddleFadeStart is the fraction of a puddle's total duration (elapsed,
// not remaining) after which it starts dying down — before this it burns
// at full strength regardless of how long its total duration is; a 10s
// puddle isn't a weaker fire than a 1.5s one, it's the same fire lasting
// longer. puddleFade returns 1 (full strength) before that point, then
// ramps linearly to 0 (embers, about to go out) by the end.
const puddleFadeStart = 0.55

func puddleFade(p *burningPuddle) float64 {
	elapsed := 1 - p.remaining/p.duration
	if elapsed < puddleFadeStart {
		return 1
	}
	return 1 - (elapsed-puddleFadeStart)/(1-puddleFadeStart)
}

func (g *Game) updatePuddleEmission(p *burningPuddle, dt float64) {
	if !g.explosionParticles {
		return // still burns (see engine/firedamage.go) — just not rendered
	}
	fade := puddleFade(p)
	slow := 1 + (puddleDyingIntervalMul-1)*(1-fade)
	if frames := loadFireFrames(); len(frames) > 0 {
		interval := (puddleFlameIntervalMax - p.amount*(puddleFlameIntervalMax-puddleFlameIntervalMin)) * slow
		drainSpawnTimer(&p.flameTimer, dt, interval, func() { g.spawnPuddleFlame(p, frames, fade) })
	}
	if frames := loadToxicSmokeFrames(); len(frames) > 0 {
		interval := (puddleSmokeIntervalMax - p.amount*(puddleSmokeIntervalMax-puddleSmokeIntervalMin)) * slow
		drainSpawnTimer(&p.smokeTimer, dt, interval, func() { g.spawnPuddleSmoke(p, frames, fade) })
	}
}

// puddleSpreadRadius is how far from centre a single flame/smoke spawn can
// land, scaled by this puddle's own amount — sqrt of a uniform [0,1) keeps
// the samples evenly spread over the disc's AREA rather than bunching
// toward the centre the way a plain linear scale would.
func puddleSpreadRadius(p *burningPuddle) float64 {
	return burningPuddleMinRadius + p.amount*(burningPuddleMaxRadius-burningPuddleMinRadius)
}

func puddleSamplePoint(p *burningPuddle, radiusScale float64) (x, y float64) {
	ang := pRandomF01() * 2 * math.Pi
	dist := math.Sqrt(pRandomF01()) * puddleSpreadRadius(p) * radiusScale
	return p.x + math.Cos(ang)*dist, p.y + math.Sin(ang)*dist
}

// Flame licks: many small, short-lived instances rather than a few big
// blobs — this is PFEnh's own DECORATE technique for its "burning floor
// fire" actor (ReplacerFire/UTNTFireLarge, researched directly from the
// mod's source): spawn a constant stream of brief (~0.3-0.5s), modestly
// sized flame billboards clustered near the base, each fading out on its
// own, and let many overlapping instances build up the flicker — not one
// long-lived particle slowly changing size. Real alpha-blended, reusing
// the same fireLight/fireAlpha0/fireHoldFrac/fireClearCool treatment so it
// matches the rest of the fire system. fade (0..1, see puddleFade) shrinks
// each lick as the puddle dies down, but never below half size — even the
// last gasp of a dying puddle should still read as a flame, not vanish
// mid-flicker. puddleFlameRise is deliberately modest: genFireGlow's own
// texture shape already reaches upward on its own (a tall, tapering
// silhouette anchored at the base, not a centred blob), so the particle
// itself doesn't need to travel far to read as "rising."
const (
	puddleFlameLifeMin = 0.35
	puddleFlameLifeMax = 0.6
	puddleFlameSizeMin = 2.2
	puddleFlameSizeMax = 3.8
	puddleFlameRise    = 14.0
	puddleFlameGravity = -18.0
)

func (g *Game) spawnPuddleFlame(p *burningPuddle, frames []*assets.RGBA, fade float64) {
	// Same shrinking footprint as the visible puddle decal (see
	// collectGroundSplats) — the fire should stay confined to whatever's
	// actually left burning, not keep spawning across the puddle's
	// original, now-shrunk extent.
	x, y := puddleSamplePoint(p, 0.4+0.6*fade)
	sizeFade := 0.5 + 0.5*fade
	fp := &Particle{
		X: x, Y: y, Z: p.z,
		DZ:            puddleFlameRise * (0.5 + 0.5*pRandomF01()),
		life:          randRange(puddleFlameLifeMin, puddleFlameLifeMax),
		sizeMul:       randRange(puddleFlameSizeMin, puddleFlameSizeMax) * sizeFade,
		framePhase:    pRandom(),
		frameRate:     fireFrameRate,
		gravity:       puddleFlameGravity,
		light:         fireLight,
		translucent:   true,
		alpha0:        fireAlpha0,
		hold:          fireHoldFrac,
		clearBrighten: fireClearCool,
		frames:        frames,
	}
	g.particles = append(g.particles, fp)
	// Tracked so spawnPuddleSmoke can spawn off wherever this flame lick
	// currently is — see burningPuddle.flameParticles' own doc comment.
	p.flameParticles = append(p.flameParticles, fp)
}

// pickAliveFlame returns a uniformly-random still-burning flame particle
// from flames, or nil if every one of them has already expired (e.g. right
// after ignition, before the first flame lick has spawned at all). A few
// random probes cover the common case cheaply — most of a puddle's own
// flame particles are alive most of the time — falling back to a full
// linear scan only for the rare case where just a few stragglers remain.
func pickAliveFlame(flames []*Particle) *Particle {
	if len(flames) == 0 {
		return nil
	}
	for i := 0; i < 6; i++ {
		idx := int(pRandomF01() * float64(len(flames)))
		if idx >= len(flames) {
			idx = len(flames) - 1
		}
		if p := flames[idx]; p.age < p.life {
			return p
		}
	}
	for _, p := range flames {
		if p.age < p.life {
			return p
		}
	}
	return nil
}

// Smoke: the only smoke any explosion produces (see
// spawnExplosionParticles' own doc comment) — thick, dark, and toxic,
// since this is spilled chemical runoff burning off, not ordinary wood/fuel
// smoke (see loadToxicSmokeFrames' dark sickly-green tint). Spawns
// noticeably above the flame's own base (puddleSmokeSpawnLift) and rises
// distinctly slower than it lifts initially (puddleSmokeRise vs.
// puddleFlameRise's own launch speed) so it visibly comes off the TOP of
// the fire and then hangs/climbs sluggishly — a heavy, toxic cloud, not a
// light wisp — rather than mixing in at the same height or shooting away.
// puddleSmokeHold/Alpha0 are pushed close to fully thick for most of each
// puff's (now much longer) life, and puddleSmokeBrighten barely lightens it
// at all by the time it clears, so it stays dark throughout instead of
// thinning to pale gray the way ordinary soot would.
const (
	puddleSmokeSpawnLift = 12.0
	puddleSmokeLifeMin   = 2.6
	puddleSmokeLifeMax   = 4.2
	puddleSmokeSizeMin   = 4.5
	puddleSmokeSizeMax   = 8.0
	puddleSmokeRise      = 10.0
	puddleSmokeGravity   = -10.0
	puddleSmokeLight     = 0.30
	puddleSmokeAlpha0    = 0.95
	puddleSmokeHold      = 0.55
	puddleSmokeBrighten  = 1.15
	// puddleSmokeGrowFrac overrides the default hold-derived grow window
	// (see Particle.growFrac/collectAllParticleSprites) with a fixed
	// fraction of life instead — puddleSmokeHold is pushed high enough
	// (thick smoke that holds together) that the default hold*0.3 formula
	// would otherwise make freshly-spawned puffs take noticeably longer to
	// reach full size than they used to.
	puddleSmokeGrowFrac = 0.12
)

func (g *Game) spawnPuddleSmoke(p *burningPuddle, frames []*assets.RGBA, fade float64) {
	// Anchor to an actual live flame lick when one exists, so smoke visibly
	// comes off the top of a real flame instead of an independently
	// sampled point elsewhere on the puddle — falls back to a puddle-wide
	// sample point (the old behavior) only in the rare case no flame
	// particle is currently alive to anchor to (e.g. right at ignition).
	var x, y, baseZ float64
	if fp := pickAliveFlame(p.flameParticles); fp != nil {
		x, y, baseZ = fp.X, fp.Y, fp.Z
	} else {
		x, y = puddleSamplePoint(p, 0.6)
		baseZ = p.z
	}
	dx, dy := yawSpeedXYAt(pRandomF01()*2*math.Pi, 4)
	sizeFade := 0.5 + 0.5*fade
	g.particles = append(g.particles, &Particle{
		X: x, Y: y, Z: baseZ + puddleSmokeSpawnLift,
		DX: dx, DY: dy,
		DZ:            puddleSmokeRise * (0.5 + 0.5*pRandomF01()),
		life:          randRange(puddleSmokeLifeMin, puddleSmokeLifeMax),
		sizeMul:       randRange(puddleSmokeSizeMin, puddleSmokeSizeMax) * sizeFade,
		framePhase:    pRandom(),
		frameRate:     smokeFrameRate,
		gravity:       puddleSmokeGravity,
		light:         puddleSmokeLight,
		translucent:   true,
		alpha0:        puddleSmokeAlpha0,
		hold:          puddleSmokeHold,
		growFrac:      puddleSmokeGrowFrac,
		clearBrighten: puddleSmokeBrighten,
		frames:        frames,
	})
}

// Ground puddle decal: a flat, sickly-green liquid pool the fire visibly
// sits on and burns down from — see genPuddleTexture/loadPuddleTexture in
// particles.go and worldgeo.AddGroundSplats (a real floor-aligned quad, not
// a billboard, so it reads correctly looking down at it). Sized off the
// same puddleSpreadRadius the flame/smoke particles already sample from,
// so the visible puddle and the fire burning on it always match footprints.
const (
	puddleDecalLift  = 0.3 // world units above the floor, avoids z-fighting with it
	puddleDecalAlpha = 0.6
)

// collectGroundSplats returns this frame's puddle decals — one flat quad
// per live burningPuddle, shrinking and dimming as puddleFade approaches 0
// (the same curve the fire/smoke emission rate already follows), so the
// visible puddle and its fire die down together instead of the puddle
// either outlasting the fire or vanishing before it.
func (g *Game) collectGroundSplats() []worldgeo.GroundSplat {
	if !g.explosionParticles || len(g.burningPuddles) == 0 {
		return nil
	}
	tex := loadPuddleTexture()
	if tex == nil {
		return nil
	}
	out := g.hwGroundSplats[:0]
	for _, p := range g.burningPuddles {
		fade := puddleFade(p)
		radius := puddleSpreadRadius(p) * (0.4 + 0.6*fade)
		alpha := puddleDecalAlpha * (0.55 + 0.45*fade)
		out = append(out, worldgeo.GroundSplat{
			X: float32(p.x), Y: float32(p.y), Z: float32(p.z) + puddleDecalLift,
			Radius: float32(radius), Rot: float32(p.decalRot),
			Light: 1, Alpha: float32(alpha), Tex: tex,
		})
	}
	g.hwGroundSplats = out
	return out
}
