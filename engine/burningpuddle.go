package engine

import (
	"math"

	"twopointfive/assets"
	"twopointfive/render"
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
	// seed is this splatter's own flickerWobble phase (maplights.go) for
	// puddleLight — drawn independently per splatter so several burning
	// side by side flicker out of sync with each other instead of pulsing
	// in unison, which would read as one synchronized light rather than
	// several independent restless flames.
	seed int
	// fire identifies the barrel this splatter came from — all of one
	// barrel's splatters share one dynamic light (appendFireLights), not
	// one light each.
	fire int
	// lit / litAt: whether this splatter has spawned its first flame lick
	// yet, and its burn time (duration - remaining) when it did — its light
	// waits for the flames and fades in with them (puddleLight).
	lit   bool
	litAt float64
	// cluster is how many of this barrel's OTHER splatters landed within
	// puddleClusterRadius of this one, capped at puddleClusterMax — fixed
	// once at spawn (see spawnBurningPuddle). A tightly packed group of
	// splatters gradually reads as one bigger combined flame instead of a
	// handful of separate small ones, and burns out faster than an
	// isolated splatter would — see puddleClusterFactor.
	cluster int
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

// puddleClusterRadius/Max: how close two of one barrel's splatters have to
// land to count as clustered, and the cap on how many neighbors contribute
// — a splash that happens to throw a dozen splatters into the same tight
// corner shouldn't scale forever, just noticeably more than a couple.
// puddleClusterBurnSpeedup shortens a clustered splatter's own duration
// (see spawnBurningPuddle) — several small fires fused together burn
// through their combined fuel faster than any one of them would alone.
// puddleClusterSizeBonus/RampSeconds are puddleClusterFactor's own size/
// light growth curve — see its doc comment for why this ramps in over
// time rather than applying instantly.
const (
	puddleClusterRadius      = 34.0
	puddleClusterMax         = 8
	puddleClusterBurnSpeedup = 0.12
	puddleClusterSizeBonus   = 0.18
	puddleClusterRampSeconds = 1.4
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
	g.nextFireID++
	n := burningSplatterMin + pRandom()%(burningSplatterMax-burningSplatterMin+1)
	// Landing spots are picked first, all of them, before any burningPuddle
	// is created — clustering (see puddleClusterRadius) needs every other
	// splatter's final position to count nearby neighbors, which isn't
	// available yet mid-loop if puddles were appended one at a time.
	type spot struct{ x, y, z, amount float64 }
	spots := make([]spot, n)
	for i := range spots {
		ang := pRandomF01() * 2 * math.Pi
		dist := math.Sqrt(pRandomF01()) * burningSplatterThrowRadius
		// Thrown liquid can't pass through a wall or closed door (or up a
		// step taller than puddleTraceLift): it lands short of it instead.
		sx, sy := g.pullBackFromWall(x, y, z+puddleTraceLift, ang, dist)
		sz, _ := g.sectorFloorCeil(sx, sy) // each splatter sits on its OWN floor height, not the blast's
		spots[i] = spot{sx, sy, sz, pRandomF01()}
	}
	for i := range spots {
		cluster := 0
		for j := range spots {
			if i == j {
				continue
			}
			dx, dy := spots[i].x-spots[j].x, spots[i].y-spots[j].y
			if dx*dx+dy*dy <= puddleClusterRadius*puddleClusterRadius {
				cluster++
			}
		}
		if cluster > puddleClusterMax {
			cluster = puddleClusterMax
		}
		amount := spots[i].amount
		// Clustered splatters burn faster together than any one of them
		// would alone — see puddleClusterBurnSpeedup's own doc comment.
		duration := (burningPuddleMinDuration + amount*(burningPuddleMaxDuration-burningPuddleMinDuration)) /
			(1 + puddleClusterBurnSpeedup*float64(cluster))
		g.burningPuddles = append(g.burningPuddles, &burningPuddle{
			x: spots[i].x, y: spots[i].y, z: spots[i].z,
			amount: amount, duration: duration, remaining: duration,
			ignitionDelay: randRange(burningPuddleIgnitionDelayMin, burningPuddleIgnitionDelayMax),
			decalRot:      pRandomF01() * 2 * math.Pi,
			seed:          pRandom(),
			cluster:       cluster,
			fire:          g.nextFireID,
		})
	}
}

// puddleClusterFactor is the size/light growth multiplier a clustered
// splatter has grown into at this moment — 1 (no change) for an isolated
// splatter (cluster == 0), ramping from 1 up to
// 1+puddleClusterSizeBonus*cluster over puddleClusterRampSeconds of actual
// burning (not real time — ignitionDelay doesn't count). Ramped rather
// than applied instantly so a tight group of splatters visibly grows
// together into one bigger flame over its first second or so, rather than
// snapping to full size the instant it ignites.
func puddleClusterFactor(p *burningPuddle) float64 {
	if p.cluster == 0 {
		return 1
	}
	elapsed := p.duration - p.remaining
	ramp := elapsed / puddleClusterRampSeconds
	if ramp > 1 {
		ramp = 1
	} else if ramp < 0 {
		ramp = 0
	}
	return 1 + puddleClusterSizeBonus*float64(p.cluster)*ramp
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
		// Divided by puddleClusterFactor: a clustered group of splatters
		// spawns flame licks faster as it grows into one bigger combined
		// fire (see puddleClusterFactor), reading as denser/thicker rather
		// than just each individual lick growing larger.
		interval := (puddleFlameIntervalMax - p.amount*(puddleFlameIntervalMax-puddleFlameIntervalMin)) * slow / puddleClusterFactor(p)
		drainSpawnTimer(&p.flameTimer, dt, interval, func() { g.spawnPuddleFlame(p, frames, fade) })
	}
	if frames := loadToxicSmokeFrames(); len(frames) > 0 {
		// *puddleSmokeClusterSize: each firing now spawns a whole cluster
		// (see spawnPuddleSmoke) instead of one particle, so the interval
		// between firings widens by the same factor — puffs arrive in
		// small clumps rather than trickling in one at a time, but the
		// overall particle budget/rate is unchanged.
		interval := (puddleSmokeIntervalMax - p.amount*(puddleSmokeIntervalMax-puddleSmokeIntervalMin)) * slow * puddleSmokeClusterSize
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

// A sample that would land across a wall or closed door from the puddle's
// centre is pulled back to just short of it (pullBackFromWall), so a
// puddle against a wall never puts flame or smoke on its far side.
func (g *Game) puddleSamplePoint(p *burningPuddle, radiusScale float64) (x, y float64) {
	ang := pRandomF01() * 2 * math.Pi
	dist := math.Sqrt(pRandomF01()) * puddleSpreadRadius(p) * radiusScale
	return g.pullBackFromWall(p.x, p.y, p.z+puddleTraceLift, ang, dist)
}

// puddleTraceLift is the height above the floor at which the barrel
// fire's wall checks run — low enough that a step up taller than this
// stops the spill, high enough to clear the floor itself.
const puddleTraceLift = 16.0

// puddleWallMargin is how far short of a blocking wall a pulled-back
// splatter or spawn point stops.
const puddleWallMargin = 6.0

// pullBackFromWall returns the point dist along angle ang from (x, y),
// shortened to stop puddleWallMargin before the nearest wall or closed
// door in the way (traceBlock at height z), if any.
func (g *Game) pullBackFromWall(x, y, z, ang, dist float64) (float64, float64) {
	ex, ey := x+math.Cos(ang)*dist, y+math.Sin(ang)*dist
	if t, blocked := g.traceBlock(x, y, z, ex, ey, z, true); blocked {
		dist = max(t*dist-puddleWallMargin, 0)
		ex, ey = x+math.Cos(ang)*dist, y+math.Sin(ang)*dist
	}
	return ex, ey
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
//
// puddleFlameSwaySpeedMax/puddleFlameFrameRateJitter/puddleFlameLightJitter
// are what actually make the result read as fire rather than a column of
// identical billboards rising in lockstep: real flame licks don't travel
// straight up, they lean and flicker independently of their neighbors.
// Each lick gets its own small random horizontal drift direction/speed
// (sway), its own animation speed within a range (jitter, so overlapping
// licks don't pulse in sync), and its own brightness offset (also jitter)
// so the flicker has real per-lick variation, not one uniform pulse.
const (
	puddleFlameLifeMin      = 0.35
	puddleFlameLifeMax      = 0.6
	puddleFlameSizeMin      = 2.2
	puddleFlameSizeMax      = 3.8
	puddleFlameRise         = 14.0
	puddleFlameGravity      = -18.0
	puddleFlameSwaySpeedMax = 18.0
	// puddleFlameFrameRateJitterMin/Max and puddleFlameLightJitterMin/Max
	// are deliberately wide (each lick's animation speed varies up to
	// ±45%, brightness up to ±35%) — a real open flame's flicker is
	// noticeably erratic, not a gentle shimmer.
	puddleFlameFrameRateJitterMin = 0.6
	puddleFlameFrameRateJitterMax = 1.5
	puddleFlameLightJitterMin     = 0.65
	puddleFlameLightJitterMax     = 1.35
)

func (g *Game) spawnPuddleFlame(p *burningPuddle, frames []*assets.RGBA, fade float64) {
	if !p.lit {
		p.lit, p.litAt = true, p.duration-p.remaining
	}
	// Same shrinking footprint as the visible puddle decal (see
	// collectGroundSplats) — the fire should stay confined to whatever's
	// actually left burning, not keep spawning across the puddle's
	// original, now-shrunk extent.
	x, y := g.puddleSamplePoint(p, 0.4+0.6*fade)
	sizeFade := 0.5 + 0.5*fade
	// clusterMul grows a tightly packed group of splatters into one bigger
	// combined flame over time — see puddleClusterFactor's own doc comment.
	clusterMul := puddleClusterFactor(p)
	swayAng := pRandomF01() * 2 * math.Pi
	swaySpeed := pRandomF01() * puddleFlameSwaySpeedMax
	fp := &Particle{
		X: x, Y: y, Z: p.z,
		DX:            math.Cos(swayAng) * swaySpeed,
		DY:            math.Sin(swayAng) * swaySpeed,
		DZ:            puddleFlameRise * (0.5 + 0.5*pRandomF01()),
		life:          randRange(puddleFlameLifeMin, puddleFlameLifeMax),
		sizeMul:       randRange(puddleFlameSizeMin, puddleFlameSizeMax) * sizeFade * clusterMul,
		framePhase:    pRandom(),
		frameRate:     fireFrameRate * randRange(puddleFlameFrameRateJitterMin, puddleFlameFrameRateJitterMax),
		gravity:       puddleFlameGravity,
		light:         fireLight * randRange(puddleFlameLightJitterMin, puddleFlameLightJitterMax),
		translucent:   true,
		alpha0:        fireAlpha0,
		hold:          fireHoldFrac,
		clearBrighten: fireClearCool,
		frames:        frames,
		wallClip:      true,
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
	// puddleSmokeClusterSize/Jitter/ZJitter: each spawn event throws this
	// many particles instead of one, each offset by its own small random
	// (x, y, z) jitter around the same anchor point — a single billboard
	// always reads as a flat 2D card no matter how it's positioned (that's
	// just what one camera-facing quad looks like), but several overlapping
	// ones sitting at genuinely different depths parallax against each
	// other as the camera moves and blend together into something that
	// reads as an actual volume of smoke rather than one obviously flat
	// sprite. The spawn interval (see updatePuddleEmission) is scaled up by
	// this same cluster size so the overall particle budget/rate is
	// unchanged — puffs arrive in small clumps instead of trickling in one
	// at a time, not more often overall.
	puddleSmokeClusterSize    = 3
	puddleSmokeClusterJitter  = 7.0
	puddleSmokeClusterZJitter = 10.0
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
		x, y = g.puddleSamplePoint(p, 0.6)
		baseZ = p.z
	}
	sizeFade := 0.5 + 0.5*fade
	// A cluster of jittered particles, not one — see
	// puddleSmokeClusterSize's own doc comment for why: several billboards
	// at slightly different (x, y, z) offsets read as an actual volume of
	// smoke, where a single one always reads as a flat card.
	for i := 0; i < puddleSmokeClusterSize; i++ {
		jang := pRandomF01() * 2 * math.Pi
		jr := pRandomF01() * puddleSmokeClusterJitter
		px, py := g.pullBackFromWall(x, y, baseZ+puddleSmokeSpawnLift, jang, jr)
		jz := (pRandomF01() - 0.5) * puddleSmokeClusterZJitter
		dx, dy := yawSpeedXYAt(pRandomF01()*2*math.Pi, 4)
		g.particles = append(g.particles, &Particle{
			X: px, Y: py, Z: baseZ + puddleSmokeSpawnLift + jz,
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
			wallClip:      true,
		})
	}
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

// puddleLightR/G/B: a warm orange flame glow, the same warm palette
// lava's own static light uses (liquidlights.go) since this is the same
// kind of hot open fire. puddleLightBaseIntensity/BaseRadius are the
// values at amount=1 (a big spill); puddleLight scales both down for a
// thinner splatter and fades them out entirely as the puddle burns down
// (puddleFade), so the light goes out together with the flame it's
// attached to rather than lingering after the flame is visibly gone.
// puddleLightHeight lifts it above the floor to roughly where the flame
// licks themselves reach, not down at ground level.
const (
	puddleLightR             = 1.0
	puddleLightG             = 0.45
	puddleLightB             = 0.12
	puddleLightBaseIntensity = 0.9
	puddleLightBaseRadius    = 110.0
	puddleLightHeight        = 20.0
	// puddleLightCullDist skips computing a splatter's light at all once
	// it's too far from the camera to plausibly matter — a single barrel
	// can leave up to burningSplatterMax (36) of these burning at once, so
	// this keeps a big multi-barrel level from scoring dozens of
	// far-away, functionally invisible lights every frame.
	puddleLightCullDist = 900.0
	// puddleLightFlickerBoost exaggerates flickerWobble's own swing just
	// for this light — the shared helper's stock 0.80..1.06 range (tuned
	// for a steady torch/lava glow) reads as too gentle for an open,
	// restless barrel fire; doubling the swing around the same centre
	// makes it noticeably more erratic without touching flickerWobble
	// itself (which every other flame/lava light in the game still uses
	// unboosted).
	puddleLightFlickerBoost = 2.0
	// puddleLightRampSeconds is how long a splatter's light takes to come
	// up to full after its first flame lick appears — about as long as the
	// flame bed takes to fill in (a lick lives ~0.5s, new ones arriving
	// every 0.07-0.2s), so the glow grows with the fire instead of
	// switching on at full strength under a single small flame.
	puddleLightRampSeconds = 0.6
)

// puddleLight returns the flickering point light one burning puddle casts,
// or ok=false until its flames have actually appeared (no light before the
// fire, nor at all with explosionParticles off, when the flames aren't
// drawn) or once it has burned down too far to matter. It fades in over
// puddleLightRampSeconds from the first flame lick. Reuses maplights.go's own flickerWobble — the same
// restless per-tic wobble a torch or lava's static light gets — so a
// barrel fire's glow looks like it belongs to the same family of flame
// light already established elsewhere in the engine, just for a dynamic
// (spawned at runtime, not baked into g.staticLights) emitter instead of a
// map-fixed one, with its own extra flicker boost (puddleLightFlickerBoost)
// and cluster growth (puddleClusterFactor) layered on top. p.seed keeps
// several puddles burning near each other flickering independently rather
// than in unison.
func (g *Game) puddleLight(p *burningPuddle) (render.Light, bool) {
	if !p.lit || !g.explosionParticles {
		return render.Light{}, false
	}
	ramp := min((p.duration-p.remaining-p.litAt)/puddleLightRampSeconds, 1)
	ramp = ramp * ramp * (3 - 2*ramp) // smoothstep: eases in and settles
	fade := puddleFade(p) * ramp
	if fade <= 0.05 {
		return render.Light{}, false
	}
	w := flickerWobble(p.seed, g.levelTime)
	w = 0.93 + (w-0.93)*puddleLightFlickerBoost
	cluster := float32(puddleClusterFactor(p))
	inten := puddleLightBaseIntensity * float32(0.4+0.6*p.amount) * float32(fade) * w * cluster
	radius := puddleLightBaseRadius * float32(0.5+0.5*p.amount) * float32(0.7+0.3*fade) * (0.95 + 0.05*w) * cluster
	if inten <= 0.02 || radius <= 1 {
		return render.Light{}, false
	}
	return render.Light{
		X: float32(p.x), Y: float32(p.y), Z: float32(p.z) + puddleLightHeight,
		Radius: radius, R: puddleLightR, G: puddleLightG, B: puddleLightB, Intensity: inten,
	}, true
}

// fireLightMaxIntensity caps one barrel fire's combined light. Its
// splatters' intensities sum, but as separate lights they were spread out
// and mostly didn't overlap, so the merged light is capped near one big
// flame's brightness instead. fireLightSpreadFrac is how much of the
// spill's spread the merged radius grows by — enough to reach the far
// splatters' surroundings without lighting the whole room.
const (
	fireLightMaxIntensity = 1.2
	fireLightSpreadFrac   = 0.5
)

// appendFireLights adds one light per burning barrel fire: its live
// splatters' puddleLights merged at their intensity-weighted centre, with
// the summed (capped) intensity and a radius grown to cover the spread —
// rather than one light per splatter (up to burningSplatterMax each). One
// barrel then costs the shaders one light instead of dozens, and its
// sector reach (lightreach.go) is flooded once.
func (g *Game) appendFireLights(out []render.Light, cx, cy float32) []render.Light {
	fires := g.fireLightScratch[:0]
	fireOf := func(id int) *fireAcc {
		for i := range fires {
			if fires[i].id == id {
				return &fires[i]
			}
		}
		fires = append(fires, fireAcc{id: id})
		return &fires[len(fires)-1]
	}
	for _, p := range g.burningPuddles {
		dx, dy := float32(p.x)-cx, float32(p.y)-cy
		if dx*dx+dy*dy > puddleLightCullDist*puddleLightCullDist {
			continue
		}
		l, ok := g.puddleLight(p)
		if !ok {
			continue
		}
		f := fireOf(p.fire)
		w := l.Intensity
		f.x, f.y, f.z, f.w = f.x+l.X*w, f.y+l.Y*w, f.z+l.Z*w, f.w+w
		f.r, f.g, f.b = f.r+l.R*w, f.g+l.G*w, f.b+l.B*w
		f.maxRad = max(f.maxRad, l.Radius)
	}
	for i := range fires {
		f := &fires[i]
		x, y, z := f.x/f.w, f.y/f.w, f.z/f.w
		// Grow the radius by part of how far the lit splatters spread from
		// the centre (fireLightSpreadFrac).
		spread := 0.0
		for _, p := range g.burningPuddles {
			if p.fire == f.id && p.lit {
				spread = max(spread, math.Hypot(p.x-float64(x), p.y-float64(y)))
			}
		}
		out = append(out, render.Light{
			X: x, Y: y, Z: z,
			Radius: f.maxRad + float32(spread)*fireLightSpreadFrac,
			R:      f.r / f.w, G: f.g / f.w, B: f.b / f.w,
			Intensity: min(f.w, fireLightMaxIntensity),
		})
	}
	g.fireLightScratch = fires
	return out
}

// fireAcc is one barrel fire's running intensity-weighted sums in
// appendFireLights.
type fireAcc struct {
	id                  int
	x, y, z, w, r, g, b float32
	maxRad              float32
}
