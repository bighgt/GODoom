package engine

import (
	"image"
	"image/png"
	"log"
	"math"
	"os"
	"path/filepath"
	"sync"

	"twopointfive/assets"
	"twopointfive/render/worldgeo"
)

// Particle is a short-lived cosmetic billboard — no collision, no gameplay
// interaction, purely visual — currently just the flying embers
// spawnExplosionParticles bursts from a projectile impact. This is the
// engine's own native replacement for what PFEnh (a GZDoom mod — see
// C:\Users\BigH\Downloads\PFEnhV0_8) does with a DECORATE actor-spawn
// cascade: this engine has no DECORATE interpreter, so instead of running
// the mod's script, this reproduces the visual idea (a handful of small,
// fast, gravity-affected sparks) with a purpose-built Go type and the
// mod's own spark art (extracted once as loose PNGs — see loadSparkFrames).
type Particle struct {
	X, Y, Z       float64
	DX, DY, DZ    float64 // velocity, units/sec
	age           float64
	life          float64 // seconds until removal
	sizeMul       float64 // on top of the art's own TexelsPerUnit
	framePhase    int     // random start offset into frames, so a burst doesn't pulse in lockstep
	frameRate     float64 // frames/sec through frames — sparks/chunks/fire animate at different rates
	gravity       float64 // units/sec^2 — fire licks use a small negative (buoyant) value
	light         float64 // shading multiplier (see collectParticleSprites/collectTransparentParticleSprites)
	fullBright    bool    // sparks/chunks/fire are emissive; irrelevant if translucent
	translucent   bool    // smoke: real alpha blend (KindSpriteTrans) instead of additive
	alpha0        float64 // translucent only: starting alpha, faded to 0 over life
	hold          float64 // translucent only: fraction of life spent at full density before clearing starts
	growFrac      float64 // translucent only: fraction of life to reach full (smokeGrowEnd) size; 0 = derive from hold instead (see collectAllParticleSprites)
	clearBrighten float64 // translucent only: light multiplier reached by the end of clearing — >1 lightens (soot thinning to gray), <1 dims (hot gas cooling as it disperses), 1 leaves color unchanged
	frames        []*assets.RGBA
	// pooled marks a Particle allocated via newPooledParticle rather than a
	// plain &Particle{} literal — updateParticles returns it to
	// particlePool once it expires instead of leaving it for the GC. false
	// (the zero value, what every &Particle{} literal elsewhere in this
	// package gets) means never recycled, which matters for correctness:
	// engine/burningpuddle.go's flameParticles tracks its own flame-lick
	// Particles by pointer for their whole life to anchor smoke on top of
	// them, so a Particle it might still read must never be silently
	// handed to some unrelated spawn via a pool. Only this file's own
	// highest-volume, unaliased spawn sites (spawnBurst, updateRocketTrail)
	// use the pool.
	pooled bool
}

// particlePool recycles Particle structs for spawnBurst (sparks/chunks/
// fire — by far the highest-volume source: a single boosted barrel or
// rocket explosion can spawn hundreds of thousands of these in one burst,
// a real GC-pressure spike at exactly the moment the frame is already
// busiest) and updateRocketTrail's own two spawns below. See
// Particle.pooled's doc comment for why every other spawn site in this
// package deliberately does NOT use this pool.
var particlePool = sync.Pool{New: func() any { return new(Particle) }}

// newPooledParticle returns a *Particle from particlePool with its fields
// set to init — a drop-in replacement for &Particle{...} at any allocation
// this file owns exclusively for the particle's whole life.
func newPooledParticle(init Particle) *Particle {
	p := particlePool.Get().(*Particle)
	*p = init
	p.pooled = true
	return p
}

const (
	explosionParticleCount = 3200
	particleLifeMin        = 0.45
	particleLifeMax        = 0.75
	particleSpeedMin       = 140.0
	particleSpeedMax       = 500.0
	particleGravity        = 400.0 // units/sec^2
	particleDrag           = 0.9   // 1/sec velocity decay (air resistance) — see updateParticles
	particleFrameRate      = 24.0  // frames/sec through the 4-frame spark loop
	particleSizeMin        = 0.125 // extra multiplier on top of particleTexelsPerUnit's
	particleSizeMax        = 0.25  // native size
	// particleFadeStart is the fraction of a particle's life at which it
	// starts shrinking toward particleFadeFloor — since KIND_SPRITE_FULL is a
	// hard alpha-cutout, not real blending (see world.frag), there's no true
	// fade-to-transparent available without a shader change; shrinking to a
	// tiny fraction of size is a cheap stand-in that reads the same way at a
	// glance and needs no pipeline work.
	particleFadeStart = 0.5
	particleFadeFloor = 0.12

	// Chunk debris: a small number of bigger, slower, longer-lived tumbling
	// pieces (PFEnh's "NormalBoomChunk") mixed into the spark swarm so the
	// burst doesn't read as a single uniform cloud of identical dots — real
	// explosions throw a few chunky pieces of debris along with the sparks.
	explosionChunkCount = 24
	chunkLifeMin        = 0.6
	chunkLifeMax        = 1.1
	chunkSpeedMin       = 40.0
	chunkSpeedMax       = 160.0
	chunkSizeMin        = 0.6
	chunkSizeMax        = 1.3
	chunkFrameRate      = 30.0 // faster than sparks: reads as tumbling, not animating

	// Fire licks: a handful of slow, near-stationary flame billboards
	// (PFEnh's torch fire art) that linger at the impact point well after
	// the sparks/chunks are gone — real explosions leave a brief patch of
	// fire, not just an instant burst. gravity is negative (buoyant) so
	// they drift gently upward instead of falling.
	explosionFireCount = 60
	fireLifeMin        = 1.0
	fireLifeMax        = 1.8
	fireSpeedMin       = 5.0
	fireSpeedMax       = 30.0
	fireSizeMin        = 3.0 // 3x the original 1.0-1.8 — a proper burning-up plume, not a lick
	fireSizeMax        = 5.4
	fireFrameRate      = 14.0
	fireGravity        = -30.0
	// fireSpreadPitchMin/Max (fractions of straight-up, same units as the
	// default 0.2..0.8 upward bias below) give ExplosionScale.FireSpread
	// fire a shallow, near-horizontal launch angle instead — burning fuel
	// fanning out across the floor rather than shooting upward like debris
	// — and fireSpreadSpeedMul pushes it out across that floor faster,
	// before its own buoyancy (fireGravity, negative) lifts it into rising
	// flame licks.
	fireSpreadPitchMin = 0.02
	fireSpreadPitchMax = 0.18
	fireSpreadSpeedMul = 1.8
	// fireAlpha0/fireHoldFrac/fireClearCool: the explosion's own lingering
	// fire is real alpha-blended (translucent), not additive — additive
	// blending can only ever ADD light on top of whatever's behind it, it
	// can never hide it, no matter how bright or dense the fire gets. That
	// was exactly the bug: a monster standing in the initial fireball
	// stayed fully visible, just brightened, because the blend math never
	// gave the fire a way to actually replace what was behind it. Real
	// alpha blending (the same technique smoke already uses) lets a dense,
	// bright fireball genuinely occlude, while fireLight (set per spawnBurst
	// call below, same idea as rocketFlameLight) still pushes it well past
	// 1.0 for bloom, so it keeps reading as blindingly hot, not dim.
	// fireClearCool <1 dims it as it burns out, like cooling embers,
	// mirroring rocket exhaust's own clearCool rather than smoke's brighten.
	fireAlpha0    = 1.0
	fireHoldFrac  = 0.5
	fireClearCool = 0.5
	// fireLight replaces the old fullBright path's fixed *1.35 shader push
	// (KIND_SPRITE_FULL) — KIND_SPRITE_TRANS applies vLight directly with
	// no multiplier of its own, so this needs to carry the "blindingly hot"
	// push on its own, same idea as rocketFlameLight.
	fireLight = 2.2

	// smokeFrameRate is the shared animation rate every smoke-art consumer
	// reuses (rocket trail wisp below, plus liquidambience/liquidripple/
	// liquidsplash/burningpuddle's own smoke). smokeAlphaMax/smokeGrowEnd
	// are the shared caps collectAllParticleSprites' generic translucent
	// growth/fade curve uses for every smoke-like particle; everything else
	// about "how much smoke, how dark, how long it lingers" is each
	// consumer's own set of constants (e.g. burningpuddle.go's puddleSmoke*)
	// rather than a single shared tuning, since a barrel's toxic puddle
	// smoke, a lava vent's steam, and a rocket's thin exhaust wisp all need
	// to look nothing alike.
	smokeFrameRate = 8.0
	smokeAlphaMax  = 1.0
	smokeGrowEnd   = 2.6 // size multiplier reached once grown (see growWindow in collectAllParticleSprites)

	// Rocket exhaust: two layers spawned continuously behind an in-flight
	// rocket, both pushed mostly backward along its own flight line
	// (*PushSpeed) rather than scattering in every direction — with only a
	// little lateral spread (*Spread) on top — so consecutive puffs read as
	// one continuous jet trailing the rocket, not a string of separate
	// poofs. *Behind places spawns a little back from the rocket's nose so
	// they read as exhaust left behind, not part of its own sprite.
	//
	// rocketFlame* is the dominant layer: a big, dense, white-blue-hot
	// additive jet (loadRocketFlameFrames — the fire art re-tinted, see
	// tintFramesHot) meant to read as the motor's own raging exhaust, sized
	// and spawned densely enough to hide the rocket's sprite from directly
	// behind it. rocketSmokeTrail* is the faint secondary wisp: the same
	// idea as an explosion's smoke, but far fainter and thinner, since it's
	// meant to barely register next to the flame, not compete with it.
	rocketFlameInterval  = 0.018
	rocketFlameBehind    = 4.0
	rocketFlameLifeMin   = 0.12
	rocketFlameLifeMax   = 0.22
	rocketFlameSizeMin   = 8.0
	rocketFlameSizeMax   = 14.0
	rocketFlamePushSpeed = 90.0
	rocketFlameSpread    = 10.0
	rocketFlameLight     = 3.0 // pushed hard into HDR/bloom range — see world.frag's KindSpriteFull *1.35

	rocketSmokeTrailInterval  = 0.05
	rocketSmokeTrailBehind    = 14.0
	rocketSmokeTrailLifeMin   = 0.2
	rocketSmokeTrailLifeMax   = 0.35
	rocketSmokeTrailSizeMin   = 0.6
	rocketSmokeTrailSizeMax   = 1.0
	rocketSmokeTrailPushSpeed = 60.0
	rocketSmokeTrailSpread    = 16.0
	// rocketSmokeTrailAlpha0 is deliberately faint — this is meant to just
	// barely register as a wisp trailing the flame, not read as its own
	// cloud.
	// Hold is small (starts clearing almost immediately: exhaust disperses
	// in a fraction of a second) and ClearCool is below 1 so it dims/cools
	// as it disperses instead of lightening toward gray like sooty smoke.
	rocketSmokeTrailLight     = 1.2
	rocketSmokeTrailAlpha0    = 0.18
	rocketSmokeTrailHold      = 0.1
	rocketSmokeTrailClearCool = 0.5
)

// ExplosionScale bundles per-cause multipliers for spawnExplosionParticles.
// Size/Count apply to sparks/chunks/fire. No cause's own blast produces
// smoke — see spawnExplosionParticles' doc comment — so there's no
// smoke-specific dial here; a barrel's smoke comes entirely from its
// burning-puddle fire (engine/burningpuddle.go), tuned independently there.
type ExplosionScale struct {
	Size       float64
	Count      float64
	SparkCount float64 // extra multiplier on the spark layer's own count, on top of Count — 0 (the zero value) is treated as 1, so existing scales that don't set this are unaffected
	FireCount  float64 // extra multiplier on the fire layer's own count, on top of Count
	FireLife   float64 // extra multiplier on the fire layer's own life range
	FireSize   float64 // the fire layer's complete size multiplier — NOT compounded with Size (see the fire spawnBurst call), so this alone says how a type's fireball compares to another's
	FireSpread bool    // fire launches at a shallow angle and fans out across the floor (fireSpreadPitchMin/Max) instead of shooting upward like debris — burning fuel spilled from a ruptured barrel, not a warhead's airburst
}

// explosionDefault covers monster fireballs — unchanged from the original
// tuning. explosionRocket is the rocket launcher's boosted burst: 4x as
// much fire as everything else gets, and 10x as many sparks on top of the
// spark layer's own Count scaling — a rocket's blast is all shrapnel.
// explosionBarrel is bigger again (2x the rocket's own size), and fans its
// fire out low across the floor (FireSpread) rather than shooting upward,
// since it's igniting spilled fuel, not just burning in place — see
// engine/burningpuddle.go for the fire (and smoke) that keeps burning
// there afterward. explosionEnergy (plasma) is the same size as default —
// an energy blast has nothing to burn, just a flash and sparks.
// explosionBFG is energy's much bigger sibling: 10x wider, with 4x as much
// fire lasting twice as long — the BFG is a massive weapon and reads like
// one. When a rocket lands the killing hit on a barrel, the two no longer
// stack independently — see rocketBarrelExplosionBoost in projectiles.go —
// the barrel's own burst is boosted instead, so it's one bigger explosion,
// not two overlapping ones.
var (
	explosionDefault = ExplosionScale{Size: 1, Count: 1, FireCount: 1, FireLife: 1, FireSize: 1}
	explosionRocket  = ExplosionScale{Size: 1.8, Count: 10, SparkCount: 10, FireCount: 8, FireLife: 1, FireSize: 3}
	explosionBarrel  = ExplosionScale{Size: 3.4, Count: 10, FireCount: 4.4, FireLife: 1, FireSize: 6, FireSpread: true}
	explosionEnergy  = ExplosionScale{Size: 1, Count: 1, FireCount: 1, FireLife: 1, FireSize: 1}
	explosionBFG     = ExplosionScale{Size: 10, Count: 1, FireCount: 4, FireLife: 2, FireSize: 10}
)

// scaledExplosion returns scale with its "how big / how much" dials — Size,
// Count, FireSize, FireCount — multiplied by mult, leaving its qualitative
// dials (FireSpread) untouched: a boosted explosion (see aExplode's use of
// Mobj.explosionBoost) should look like more of the same fire, not a
// differently timed one.
func scaledExplosion(scale ExplosionScale, mult float64) ExplosionScale {
	scale.Size *= mult
	scale.Count *= mult
	scale.FireSize *= mult
	scale.FireCount *= mult
	return scale
}

// spawnExplosionParticles bursts sparks, chunk debris, and lingering fire
// from (x, y, z) — a rocket, plasma, BFG, monster-fireball, or barrel
// impact all call this at the same point they'd have played ExplodeSound.
// See ExplosionScale for what scale controls. Deliberately produces no
// smoke of its own — the only smoke any explosion leaves behind is a
// barrel's own burning-puddle fire (engine/burningpuddle.go), rising off
// the actual flames once they've caught, well after the initial blast; a
// clean warhead/fireball impact has none at all. A no-op if the feature is
// off (config explosionParticles) or none of the three art packs are
// present locally — same non-fatal-if-missing behaviour as every other
// loose asset pack in this project.
func (g *Game) spawnExplosionParticles(x, y, z float64, scale ExplosionScale) {
	if !g.explosionParticles {
		return
	}
	sparks := loadSparkFrames()
	chunks := loadChunkFrames()
	fire := loadFireFrames()
	if len(sparks) == 0 && len(chunks) == 0 && len(fire) == 0 {
		return
	}
	log.Printf("particles: spawning explosion at (%.0f, %.0f, %.0f) — size x%.1f, count x%.1f",
		x, y, z, scale.Size, scale.Count)
	spawnBurst := func(n int, bp burstParams) {
		if len(bp.frames) == 0 {
			return
		}
		n = int(float64(n) * scale.Count)
		sizeMin, sizeMax := bp.sizeMin*scale.Size, bp.sizeMax*scale.Size
		for i := 0; i < n; i++ {
			var dx, dy, dz float64
			if bp.lowAngle {
				// Shallow, near-horizontal launch (fireSpreadPitchMin/Max), sped
				// up by fireSpreadSpeedMul — burning fuel fanning out across the
				// floor rather than shooting upward like debris.
				dx, dy, dz = coneVelocity(fireSpreadPitchMin, fireSpreadPitchMax, bp.speedMin*fireSpreadSpeedMul, bp.speedMax*fireSpreadSpeedMul)
			} else {
				// Biased upward (0.2..0.8 of straight up) so the burst reads as
				// flying debris, not a flat horizontal ring.
				dx, dy, dz = coneVelocity(0.2, 0.8, bp.speedMin, bp.speedMax)
			}
			spawnZ := z
			if bp.zJitter > 0 {
				spawnZ += bp.zOffset + pRandomF01()*bp.zJitter
			} else {
				spawnZ += bp.zOffset
			}
			g.particles = append(g.particles, newPooledParticle(Particle{
				X: x, Y: y, Z: spawnZ,
				DX:            dx,
				DY:            dy,
				DZ:            dz,
				life:          randRange(bp.lifeMin, bp.lifeMax),
				sizeMul:       randRange(sizeMin, sizeMax),
				framePhase:    pRandom(),
				frameRate:     bp.frameRate,
				gravity:       bp.gravity,
				light:         bp.light,
				fullBright:    bp.fullBright,
				translucent:   bp.translucent,
				alpha0:        bp.alpha0,
				hold:          bp.hold,
				growFrac:      bp.growFrac,
				clearBrighten: bp.clearBrighten,
				frames:        bp.frames,
			}))
		}
	}
	// Sparks: the bulk of the burst, small and fast. sparkCount applies
	// SparkCount as its own extra multiplier on top of the Count every
	// layer already gets below (spawnBurst's own n*scale.Count) — same
	// pattern as FireCount for the fire layer — so a cause can throw far
	// more shrapnel without also inflating its chunk/fire/smoke layers.
	sparkCount := scale.SparkCount
	if sparkCount <= 0 {
		sparkCount = 1
	}
	spawnBurst(int(float64(explosionParticleCount)*sparkCount), burstParams{
		speedMin: particleSpeedMin, speedMax: particleSpeedMax, lifeMin: particleLifeMin, lifeMax: particleLifeMax,
		sizeMin: particleSizeMin, sizeMax: particleSizeMax, frameRate: particleFrameRate, gravity: particleGravity,
		light: 1, fullBright: true, frames: sparks,
	})
	// Chunks: a handful of bigger, slower, longer-lived tumbling debris
	// pieces mixed in so the burst isn't a uniform cloud of identical dots.
	spawnBurst(explosionChunkCount, burstParams{
		speedMin: chunkSpeedMin, speedMax: chunkSpeedMax, lifeMin: chunkLifeMin, lifeMax: chunkLifeMax,
		sizeMin: chunkSizeMin, sizeMax: chunkSizeMax, frameRate: chunkFrameRate, gravity: particleGravity,
		light: 1, fullBright: true, frames: chunks,
	})
	// Fire: a few slow, buoyant flame licks that linger well after the
	// sparks/chunks are gone. FireCount/FireLife are this layer's own extra
	// multipliers (the BFG's massive, long-lingering flame), on top of the
	// Size/Count every layer already gets above. No smoke of its own — see
	// this function's doc comment.
	spawnBurst(int(float64(explosionFireCount)*scale.FireCount), burstParams{
		speedMin: fireSpeedMin, speedMax: fireSpeedMax,
		lifeMin: fireLifeMin * scale.FireLife, lifeMax: fireLifeMax * scale.FireLife,
		// FireSize is fire's own complete size multiplier, deliberately not
		// compounded with the general Size scale below (spawnBurst's closure
		// applies scale.Size to every layer's sizeMin/sizeMax uniformly) —
		// dividing it out here first means FireSize alone says exactly how
		// big a type's fireball is relative to another's, instead of that
		// relationship silently depending on how their unrelated overall
		// Size values happen to compare.
		sizeMin: fireSizeMin * scale.FireSize / scale.Size, sizeMax: fireSizeMax * scale.FireSize / scale.Size, frameRate: fireFrameRate, gravity: fireGravity,
		light: fireLight, translucent: true, alpha0: fireAlpha0, hold: fireHoldFrac, clearBrighten: fireClearCool,
		lowAngle: scale.FireSpread, frames: fire,
	})
}

// updateRocketTrail spawns this frame's share of a flying rocket's two
// exhaust layers: a dominant, white-blue-hot flame jet (rocketFlame*, see
// loadRocketFlameFrames) big and dense enough to hide the rocket's own
// sprite when seen from behind it, plus a much fainter wisp of smoke
// trailing it (rocketSmokeTrail*, real alpha-blended like an explosion's
// smoke but far more transparent). Both layers are pushed mostly backward
// along the rocket's own flight line (with a little lateral spread) rather
// than scattering outward in every direction, so consecutive puffs read as
// one continuous jet trailing the rocket rather than a string of separate
// poofs.
//
// Each layer spawns at its own fixed rate regardless of frame rate —
// p.flameTrailTimer/smokeTrailTimer accumulate dt and the loops below drain
// them, so a slow frame (or a large dt spike) still spawns the right number
// of puffs to cover the distance travelled instead of thinning out at low
// framerate or bunching up after a hitch. A no-op once the feature is off;
// each layer independently no-ops if its own art pack is missing — same
// non-fatal-if-missing behaviour as spawnExplosionParticles.
func (g *Game) updateRocketTrail(p *Projectile, dt float64) {
	if !g.explosionParticles {
		return
	}
	if flame := loadRocketFlameFrames(); len(flame) > 0 {
		drainSpawnTimer(&p.flameTrailTimer, dt, rocketFlameInterval, func() {
			dx, dy, dz := jetTrailVelocity(p.DX, p.DY, p.DZ, rocketFlamePushSpeed, rocketFlameSpread)
			g.particles = append(g.particles, newPooledParticle(Particle{
				X:          p.X - p.DX*rocketFlameBehind,
				Y:          p.Y - p.DY*rocketFlameBehind,
				Z:          p.Z - p.DZ*rocketFlameBehind,
				DX:         dx,
				DY:         dy,
				DZ:         dz,
				life:       randRange(rocketFlameLifeMin, rocketFlameLifeMax),
				sizeMul:    randRange(rocketFlameSizeMin, rocketFlameSizeMax),
				framePhase: pRandom(),
				frameRate:  fireFrameRate,
				light:      rocketFlameLight,
				fullBright: true,
				frames:     flame,
			}))
		})
	}
	if smoke := loadSmokeFrames(); len(smoke) > 0 {
		drainSpawnTimer(&p.smokeTrailTimer, dt, rocketSmokeTrailInterval, func() {
			dx, dy, dz := jetTrailVelocity(p.DX, p.DY, p.DZ, rocketSmokeTrailPushSpeed, rocketSmokeTrailSpread)
			g.particles = append(g.particles, newPooledParticle(Particle{
				X:             p.X - p.DX*rocketSmokeTrailBehind,
				Y:             p.Y - p.DY*rocketSmokeTrailBehind,
				Z:             p.Z - p.DZ*rocketSmokeTrailBehind,
				DX:            dx,
				DY:            dy,
				DZ:            dz,
				life:          randRange(rocketSmokeTrailLifeMin, rocketSmokeTrailLifeMax),
				sizeMul:       randRange(rocketSmokeTrailSizeMin, rocketSmokeTrailSizeMax),
				framePhase:    pRandom(),
				frameRate:     smokeFrameRate,
				light:         rocketSmokeTrailLight,
				translucent:   true,
				alpha0:        rocketSmokeTrailAlpha0,
				hold:          rocketSmokeTrailHold,
				clearBrighten: rocketSmokeTrailClearCool,
				frames:        smoke,
			}))
		})
	}
}

// burstParams is one call's worth of arguments to spawnExplosionParticles's
// spawnBurst closure — pulled into a struct because the plain-float
// parameter list got long enough to be error-prone to call positionally.
type burstParams struct {
	speedMin, speedMax float64
	lifeMin, lifeMax   float64
	sizeMin, sizeMax   float64
	frameRate, gravity float64
	light              float64
	fullBright         bool
	translucent        bool
	hold               float64
	growFrac           float64 // translucent only: overrides the hold-derived grow window — see Particle.growFrac
	alpha0             float64
	clearBrighten      float64
	zOffset, zJitter   float64 // spawn height above the burst origin: zOffset..zOffset+zJitter
	lowAngle           bool    // fireSpreadPitchMin/Max instead of the default upward bias — see ExplosionScale.FireSpread
	frames             []*assets.RGBA
}

// updateParticles advances every live particle by dt seconds (straight-line
// travel plus its own gravity — sparks/chunks fall, fire licks drift up)
// and drops any that outlived their life. Cheap to call every frame even
// with zero particles.
func (g *Game) updateParticles(dt float64) {
	if len(g.particles) == 0 {
		return
	}
	alive := g.particles[:0]
	for _, p := range g.particles {
		p.age += dt
		if p.age >= p.life {
			if p.pooled {
				particlePool.Put(p)
			}
			continue
		}
		p.DZ -= p.gravity * dt
		// Air resistance: without this a spark stays at full horizontal speed
		// for its whole life and the burst reads as a perfect ballistic
		// spray; decaying it lets the swarm visibly "settle" instead.
		drag := 1 - particleDrag*dt
		if drag < 0 {
			drag = 0
		}
		p.DX *= drag
		p.DY *= drag
		p.X += p.DX * dt
		p.Y += p.DY * dt
		p.Z += p.DZ * dt
		alive = append(alive, p)
	}
	g.particles = alive
}

// maxTransparentParticles caps how many real-alpha-blended particles get
// submitted to the GPU in one frame. Unlike additive/opaque sprites, these
// can't be batched by texture (AddTransparentSprites draws one Draw call
// per sprite, since correct blending needs a strict back-to-front order) —
// so the draw-call count this produces scales directly with however many
// fire/smoke/etc. particles happen to be alive, with no cheap way to
// amortize a spike. drainSpawnTimer's catch-up cap keeps any one emitter
// from spawning an unbounded burst after a stall, but this is the backstop
// for the total across every emitter combined — if something (a future
// effect, an unusual combination of several explosions at once) still
// pushes the count too high, this trims it rather than letting draw calls
// grow without bound.
const maxTransparentParticles = 350

// collectAllParticleSprites walks g.particles exactly once, bucketing each
// live one into the additive output (fed to WorldBuilder.AddParticleSprites,
// drawn with the additive no-depth-write pipeline so a dense burst glows
// brighter where particles overlap) or the transparent output (fed to
// AddTransparentSprites, sorted and drawn with real alpha blending) by
// p.translucent. This used to be two separate functions, each walking all
// of g.particles independently — a slice that can run into the tens of
// thousands after a big explosion's spark burst — so merging them into one
// pass halves that iteration cost; the two branches' math is otherwise
// completely unchanged and still fully disjoint per particle.
//
// Additive particles fade toward particleFadeFloor over the back half of
// their life (KindSpriteFull is a hard alpha-cutout, not real blending, so
// shrinking is the cheap stand-in for a fade-out). Translucent particles
// spend their first p.hold fraction of life at full density — alpha
// pinned at its (capped) peak, color at its darkest, so nothing behind a
// puff is visible at all — then enter a "clearing" window where they both
// fade toward invisible and lighten in color toward p.clearBrighten, like
// real soot thinning into pale gray as it disperses. Size for translucent
// particles grows to smokeGrowEnd within the first slice of the hold
// window (not linearly across the whole life) so "big" and "opaque" line
// up instead of the cloud only ever being one or the other.
func (g *Game) collectAllParticleSprites() (additive, transparent []worldgeo.Sprite) {
	additive = g.hwParticleSprites[:0]
	transparent = g.hwTransparentParticleSprites[:0]
	transparentFull := false
	for _, p := range g.particles {
		if len(p.frames) == 0 {
			continue
		}
		idx := (int(p.age*p.frameRate) + p.framePhase) % len(p.frames)

		if !p.translucent {
			size := p.sizeMul
			if frac := p.age / p.life; frac > particleFadeStart {
				t := (frac - particleFadeStart) / (1 - particleFadeStart) // 0..1 over the back half of life
				size *= 1 - t*(1-particleFadeFloor)
			}
			additive = append(additive, worldgeo.Sprite{
				X: float32(p.X), Y: float32(p.Y), Z: float32(p.Z),
				SizeMul:    float32(size),
				Tex:        p.frames[idx],
				Light:      float32(p.light),
				FullBright: p.fullBright,
			})
			continue
		}

		if transparentFull {
			continue // see maxTransparentParticles — the rest still age/expire normally, just aren't drawn this frame
		}
		frac := p.age / p.life

		// clear is 0 throughout the hold window (full density, darkest
		// color) and ramps 0..1 across what's left of life afterward. Cap
		// hold below 1 so a puff always has *some* window left to clear in.
		hold := p.hold
		if hold > 0.95 {
			hold = 0.95
		}
		clear := 0.0
		if frac > hold {
			clear = (frac - hold) / (1 - hold)
		}

		// growT reaches 1 (full smokeGrowEnd size) within a small fraction of
		// the puff's life instead of drifting there linearly across its
		// whole lifetime. Growing at the same pace as the whole lifetime
		// meant the cloud was only ever big once it had already started
		// clearing, and only ever fully opaque while it was still small —
		// "big" and "opaque" never actually lined up, which is exactly why a
		// fully-opaque puff still failed to hide anything behind it: there
		// wasn't enough of it yet. Now it reaches full size fast and holds
		// there through the rest of hold and into clearing. p.growFrac, when
		// set, fixes that fraction directly instead of deriving it from
		// hold, for any translucent particle type whose hold is stretched a
		// lot longer than usual (a thick, long-lingering smoke) without also
		// wanting to stretch out how long it takes to become visible.
		growWindow := p.growFrac
		if growWindow <= 0 {
			growWindow = hold * 0.3
		}
		growT := 1.0
		if growWindow > 0.001 {
			if growT = frac / growWindow; growT > 1 {
				growT = 1
			}
		}
		size := p.sizeMul * (1 + (smokeGrowEnd-1)*growT)

		// 1-clear^2 stays near 1 through most of the clearing window and
		// only drops sharply right at the end — real smoke holds together
		// until it suddenly disperses, it doesn't thin at a constant rate.
		alpha := p.alpha0 * (1 - clear*clear)
		if alpha > smokeAlphaMax {
			alpha = smokeAlphaMax
		}
		if alpha <= 0 {
			continue
		}
		light := p.light * (1 + (p.clearBrighten-1)*clear)

		transparent = append(transparent, worldgeo.Sprite{
			X: float32(p.X), Y: float32(p.Y), Z: float32(p.Z),
			SizeMul: float32(size),
			Tex:     p.frames[idx],
			Light:   float32(light),
			Alpha:   float32(alpha),
		})
		if len(transparent) >= maxTransparentParticles {
			transparentFull = true
		}
	}
	g.hwParticleSprites = additive
	g.hwTransparentParticleSprites = transparent
	return additive, transparent
}

// pRandomF01 returns a pseudo-random float in [0, 1) off the same PRNG
// every other gameplay-randomness call in this package uses.
func pRandomF01() float64 { return float64(pRandom()) / 256.0 }

// randRange returns a pseudo-random float in [min, max) — the
// `min+pRandomF01()*(max-min)` idiom every particle spawn function in this
// engine repeats inline for its life/size/speed ranges.
func randRange(min, max float64) float64 { return min + pRandomF01()*(max-min) }

// yawSpeedXY resolves a random horizontal direction (0..2π) and a
// [speedMin,speedMax) magnitude into a velocity vector — the recurring
// "pick a yaw, pick a speed, spread outward" shape behind most of this
// engine's ambient/splash puffs (steam, mist, water ripples, splash
// plumes, puddle smoke). Callers that also need the chosen yaw (e.g. to
// bias a vertical component by the same angle) should compute it
// themselves and call yawSpeedXYAt instead.
func yawSpeedXY(speedMin, speedMax float64) (dx, dy float64) {
	return yawSpeedXYAt(pRandomF01()*2*math.Pi, randRange(speedMin, speedMax))
}

// yawSpeedXYAt is yawSpeedXY with the yaw and speed already chosen —
// shared by callers that need the same yaw for more than just DX/DY.
func yawSpeedXYAt(yaw, speed float64) (dx, dy float64) {
	return math.Cos(yaw) * speed, math.Sin(yaw) * speed
}

// jetTrailVelocity computes a velocity pushed backward along (dirX,dirY,dirZ)
// — typically a moving object's own flight direction — at a random 50-100%
// of pushSpeed, plus a little random lateral scatter up to lateralMax: the
// shared shape behind both of the rocket exhaust trail's layers (flame and
// smoke — see updateRocketTrail), which used to duplicate this exact
// formula with only their constants differing.
func jetTrailVelocity(dirX, dirY, dirZ, pushSpeed, lateralMax float64) (dx, dy, dz float64) {
	push := pushSpeed * (0.5 + 0.5*pRandomF01())
	lx, ly := yawSpeedXY(0, lateralMax)
	return -dirX*push + lx, -dirY*push + ly, -dirZ * push
}

// coneVelocity picks a random yaw (0..2π), a pitch biased upward between
// pitchMin and pitchMax (as a fraction of straight up: 0 = horizontal, 1 =
// vertical), and a speed in [speedMin,speedMax) — the recurring "burst of
// debris flying up and outward in a cone" shape behind spawnBurst's
// default cone, factored out so callers with this same shape don't
// re-derive the formula independently.
func coneVelocity(pitchMin, pitchMax, speedMin, speedMax float64) (dx, dy, dz float64) {
	yaw := pRandomF01() * 2 * math.Pi
	pitch := randRange(pitchMin, pitchMax) * math.Pi / 2
	speed := randRange(speedMin, speedMax)
	cp := math.Cos(pitch)
	return math.Cos(yaw) * cp * speed, math.Sin(yaw) * cp * speed, math.Sin(pitch) * speed
}

// maxSpawnCatchUp caps how many times drainSpawnTimer will call spawn() in
// one go, no matter how large dt is. Every continuous particle emitter in
// this engine (rocket trail, ambient liquid mist, the burning-puddle fire/
// smoke, the player's water ripple) drains a "seconds until next spawn"
// accumulator with a loop shaped like `for timer <= 0 { timer += interval;
// spawn() }` — correct for smoothing normal frame-time jitter, but with no
// cap a single large dt (an alt-tab, a level-load pause, or — this is the
// real incident that surfaced it — the CPU/GPU cost of a barrel
// explosion's own initial burst of tens of thousands of sparks stalling
// one frame) forces it to spawn a huge catch-up burst of MORE particles to
// "make up for lost time". That burst makes the NEXT frame slower still,
// which produces an even bigger dt next time: a spiral of death, and the
// actual mechanism behind barrel explosions cratering the frame rate for
// several seconds afterward rather than costing one bad frame. Capping the
// catch-up and discarding any remaining debt (see drainSpawnTimer) trades
// a few skipped spawns on the one pathological frame for never compounding.
const maxSpawnCatchUp = 4

// drainSpawnTimer advances *timer by -dt, then calls spawn() once per whole
// interval elapsed — up to maxSpawnCatchUp times — and snaps *timer forward
// to clear any remaining debt rather than letting it carry over. See
// maxSpawnCatchUp for why the cap exists.
func drainSpawnTimer(timer *float64, dt, interval float64, spawn func()) {
	*timer -= dt
	for i := 0; i < maxSpawnCatchUp && *timer <= 0; i++ {
		*timer += interval
		spawn()
	}
	if *timer <= 0 {
		*timer = interval
	}
}

// particleTexelsPerUnit sizes the spark art (56x60 native pixels) down to a
// small flying-ember footprint (~4-5 map units) — chosen to taste for this
// engine, not derived from the source mod (which scaled it via GZDoom's own
// unrelated per-actor "Scale" property).
const particleTexelsPerUnit = 12.0

// chunkTexelsPerUnit sizes the chunk-debris art (128x128 native) down to a
// ~14 map unit footprint — a piece large enough to read as a distinct chunk
// next to the small spark swarm.
const chunkTexelsPerUnit = 9.0

// fireTexelsPerUnit sizes the fire-lick art (77x76 native) down to a ~7 map
// unit flame — big enough to read as a lingering patch of fire.
const fireTexelsPerUnit = 11.0

// smokeTexelsPerUnit sizes the smoke puff art (141x130 native) down to a
// ~13 map unit haze — the biggest of the four, since it's meant to billow.
const smokeTexelsPerUnit = 10.5

// sparkFrames/chunkFrames/fireFrames/smokeFrames are lazily loaded once
// from loose, gitignored art (see .gitignore) under
// assets/particles/explosion/ — extracted from PFEnh, a third-party mod
// pack supplied locally, the same "supply your own locally" policy
// game_design.txt documents for every other optional asset pack. The
// *Loaded bools latch after the first attempt (successful or not) so a
// missing pack isn't retried every explosion.
var (
	sparkFrames                 []*assets.RGBA
	sparkFramesLoaded           bool
	chunkFrames                 []*assets.RGBA
	chunkFramesLoaded           bool
	fireFrames                  []*assets.RGBA
	fireFramesLoaded            bool
	smokeFrames                 []*assets.RGBA
	smokeFramesLoaded           bool
	rocketFlameFrames           []*assets.RGBA
	rocketFlameFramesLoaded     bool
	steamFrames                 []*assets.RGBA
	steamFramesLoaded           bool
	mistFrames                  []*assets.RGBA
	mistFramesLoaded            bool
	bubbleFrames                []*assets.RGBA
	bubbleFramesLoaded          bool
	toxicSmokeFrames            []*assets.RGBA
	toxicSmokeFramesLoaded      bool
	teleportGlitterFrames       []*assets.RGBA
	teleportGlitterFramesLoaded bool
)

func loadSparkFrames() []*assets.RGBA {
	if sparkFramesLoaded {
		return sparkFrames
	}
	sparkFramesLoaded = true
	sparkFrames = loadFramePack("spark", "BPUF", []byte{'A', 'B', 'C', 'D'}, particleTexelsPerUnit)
	return sparkFrames
}

func loadChunkFrames() []*assets.RGBA {
	if chunkFramesLoaded {
		return chunkFrames
	}
	chunkFramesLoaded = true
	chunkFrames = loadFramePack("chunk", "XPN1", []byte{'A', 'B', 'C', 'D', 'E', 'F', 'G', 'H', 'I', 'J', 'K'}, chunkTexelsPerUnit)
	return chunkFrames
}

// fireGlowFrameCount/W/H: loadFireFrames returns procedurally-generated
// soft flame-lick glows, not the PFEnh mod's FIRE*.png sprite frames — see
// its own doc comment for why. Several frames with independent random
// jitter give the flicker some per-frame shape variety instead of one
// static shape repeating; the resolution is plenty since it's a smooth
// analytic gradient, not sampled pixel art, so it never shows aliasing at
// any billboard size. Taller than wide — an actual flame silhouette
// (narrow rising tip, wider base), not a circle — see genFireGlow.
const (
	fireGlowFrameCount = 6
	fireGlowW          = 40
	fireGlowH          = 72
)

// loadFireFrames returns the fire billboard art shared by the explosion's
// own lingering fire, the burning-puddle fire, and (via tintFramesHot) the
// rocket exhaust jet. This used to load the PFEnh mod's FIRE*.png sprite
// frames, feathered to soften their edge — which worked fine back when
// fire was additive (PFEnh's own intended blend mode: a rough alpha
// channel is invisible there, since additive light just fades to nothing
// at low values with no hard boundary). Once fire became real
// alpha-blended (so it could actually occlude what's behind it — see
// fireAlpha0's doc comment), that same rough, boxy alpha channel became a
// real occlusion edge for the first time, and feathering only softens the
// *transition* at that edge, not the underlying geometric shape — so a
// basically-rectangular alpha mask still reads as a soft-edged rectangle,
// which is exactly the "straight lines" a bigger, more opaque flame made
// obvious. A procedural radial gradient sidesteps this entirely: it's
// circular and smooth by construction, so there's no geometric silhouette
// to expose no matter how opaque or how large it's drawn.
func loadFireFrames() []*assets.RGBA {
	if fireFramesLoaded {
		return fireFrames
	}
	fireFramesLoaded = true
	fireFrames = make([]*assets.RGBA, fireGlowFrameCount)
	for i := range fireFrames {
		fireFrames[i] = genFireGlow(fireGlowW, fireGlowH, true)
	}
	return fireFrames
}

// genFireGlow procedurally paints one soft flame glow texture: a hot
// white-yellow core cooling through orange to a transparent red edge,
// alpha falling off smoothly to 0 well before the texture's own border so
// there's never a hard boundary at any render size. A little per-call
// random jitter (core size/position) keeps a run of frames from looking
// like the same shape stamped out repeatedly.
//
// anchorBottom picks which of two shapes this is:
//   - true: a flame LICK — elliptical and asymmetric top/bottom (reaching
//     much further up than down from a low core), so the shape itself
//     reads as a rising flame tapering to a point, the way PFEnh's own
//     FIRE actor's DECORATE describes it behaving. OffsetY is the full
//     height (not half): callers anchor the BOTTOM of this texture at the
//     particle's spawn Z, so it sits on the floor and rises up from there.
//     Used for the explosion's own lingering fire and the burning puddle.
//   - false: a rounder, centred JET glow — a trailing exhaust plume has no
//     floor to sit on and no meaningful "up" to taper toward the way a
//     ground flame does, so this variant keeps the core centred and the
//     falloff roughly symmetric instead of forcing an upward taper onto
//     something that doesn't rise off a surface. Used for the rocket
//     exhaust (see loadRocketFlameFrames) — giving it the same tapered,
//     floor-anchored shape as ground fire was the actual regression a
//     "why does the rocket flame look worse now" report traced back to.
func genFireGlow(w, h int, anchorBottom bool) *assets.RGBA {
	pix := make([]byte, w*h*4)
	cx := float64(w-1) / 2
	var cy, ryUp, ryDown float64
	offY := h / 2
	if anchorBottom {
		cy = float64(h-1) * (0.68 + 0.1*pRandomF01())    // hot core sits low, near the base
		ryUp = float64(h) * (0.55 + 0.12*pRandomF01())   // long reach up to the tapering tip
		ryDown = float64(h) * (0.22 + 0.08*pRandomF01()) // short reach down to the base
		offY = h
	} else {
		cy = float64(h-1) * (0.5 + 0.1*(pRandomF01()-0.5))
		ryUp = float64(h) * (0.42 + 0.1*pRandomF01())
		ryDown = ryUp
	}
	rx := cx * (0.8 + 0.3*pRandomF01())
	ox := (pRandomF01() - 0.5) * cx * 0.3
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dx := float64(x) - cx - ox
			dy := float64(y) - cy
			ry := ryUp
			if dy > 0 {
				ry = ryDown
			}
			d := math.Sqrt((dx/rx)*(dx/rx) + (dy/ry)*(dy/ry))
			o := (y*w + x) * 4
			if d >= 1 {
				pix[o+3] = 0
				continue
			}
			a := math.Pow(1-d, 2.2)
			pix[o+0] = 255
			pix[o+1] = clampByte(float32(255 * (1 - 0.5*d)))
			pix[o+2] = clampByte(float32(255 * math.Max(0, 1-2.2*d)))
			pix[o+3] = clampByte(float32(a * 255))
		}
	}
	return &assets.RGBA{Width: w, Height: h, Pix: pix, TexelsPerUnit: fireTexelsPerUnit, OffsetX: w / 2, OffsetY: offY}
}

// puddleTexture is a lazily-generated, cached ground-decal texture for
// burningPuddle's floor splats (engine/burningpuddle.go) — a flat quad, not
// a billboard, so its own OffsetX/OffsetY/TexelsPerUnit are irrelevant
// (worldgeo.AddGroundSplats sizes it directly in world units from the
// splat's own Radius) and left at their zero values.
var (
	puddleTex       *assets.RGBA
	puddleTexLoaded bool
)

func loadPuddleTexture() *assets.RGBA {
	if puddleTexLoaded {
		return puddleTex
	}
	puddleTexLoaded = true
	puddleTex = genPuddleTexture(64)
	return puddleTex
}

// genPuddleTexture procedurally paints a soft, round, sickly-green liquid
// puddle: a darker, more saturated ring toward the edge softening into
// transparency, with a brighter yellow-green sheen near the centre
// suggesting a wet, faintly glowing chemical liquid — never fully opaque
// (alpha capped well under 255) since a puddle should still let the floor
// texture show through, not paint over it solid.
func genPuddleTexture(size int) *assets.RGBA {
	pix := make([]byte, size*size*4)
	c := float64(size-1) / 2
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx, dy := float64(x)-c, float64(y)-c
			d := math.Sqrt(dx*dx+dy*dy) / c
			o := (y*size + x) * 4
			if d >= 1 {
				pix[o+3] = 0
				continue
			}
			a := math.Pow(1-d, 1.6)
			pix[o+0] = clampByte(float32(40 + 60*(1-d)))
			pix[o+1] = clampByte(float32(130 + 90*(1-d)))
			pix[o+2] = clampByte(float32(20 + 20*(1-d)))
			pix[o+3] = clampByte(float32(a * 200))
		}
	}
	return &assets.RGBA{Width: size, Height: size, Pix: pix}
}

// rocketFlameGlowFrameCount/W/H: the rocket exhaust jet's own glow frames —
// generated directly, not derived from loadFireFrames' ground-flame shape
// (see genFireGlow's anchorBottom doc comment for why a trailing jet needs
// the rounder variant). Same cheap additive/batched rendering as before
// (fullBright, not translucent — see updateRocketTrail); only the texture
// shape changes here, so this carries no extra per-frame rendering cost.
const (
	rocketFlameGlowFrameCount = 6
	rocketFlameGlowW          = 56
	rocketFlameGlowH          = 56
)

// loadRocketFlameFrames returns a rounder, centred flame glow re-tinted
// white-blue and hot (see tintFramesHot) for the rocket exhaust jet — a
// trailing plume, not a ground flame lick, so it uses genFireGlow's
// anchorBottom=false shape rather than reusing the explosion fire's own
// (tapered, floor-anchored) frames the way this used to.
func loadRocketFlameFrames() []*assets.RGBA {
	if rocketFlameFramesLoaded {
		return rocketFlameFrames
	}
	rocketFlameFramesLoaded = true
	base := make([]*assets.RGBA, rocketFlameGlowFrameCount)
	for i := range base {
		base[i] = genFireGlow(rocketFlameGlowW, rocketFlameGlowH, false)
	}
	rocketFlameFrames = tintFramesHot(base)
	return rocketFlameFrames
}

// tintFrames returns new copies of src with each color channel put through
// channel*mul+add (clamped to a byte) — additive blending means a tint only
// has to bias the source gradient, not replace it outright, so whatever
// animated silhouette/flicker the art already has is preserved, just
// reading as a different color/heat. Alpha is left untouched.
func tintFrames(src []*assets.RGBA, mul, add [3]float32) []*assets.RGBA {
	out := make([]*assets.RGBA, len(src))
	for i, f := range src {
		cp := *f
		pix := make([]byte, len(f.Pix))
		for j := 0; j < len(f.Pix); j += 4 {
			pix[j+0] = clampByte(float32(f.Pix[j+0])*mul[0] + add[0])
			pix[j+1] = clampByte(float32(f.Pix[j+1])*mul[1] + add[1])
			pix[j+2] = clampByte(float32(f.Pix[j+2])*mul[2] + add[2])
			pix[j+3] = f.Pix[j+3]
		}
		cp.Pix = pix
		out[i] = &cp
	}
	return out
}

// tintFramesHot returns new copies of src pushed hard toward a white-hot,
// blue-white flame instead of whatever hue the source art natively has —
// see tintFrames.
func tintFramesHot(src []*assets.RGBA) []*assets.RGBA {
	return tintFrames(src, [3]float32{0.85, 0.95, 1.1}, [3]float32{55, 100, 165})
}

// smokeGlowFrameCount/Size: loadSmokeFrames returns procedurally-generated
// soft cloud puffs, not the WAD's SMOK*.png sprite frames. Those are
// Doom-era pixel-cutout art — a hard boolean silhouette with no soft
// antialiased boundary — which read as a crisp, geometric edge once real
// alpha blending (KindSpriteTrans) draws exactly what's in the alpha
// channel instead of additively glowing over it. Feathering (a box blur at
// load time) used to paper over this, but that only softens the
// *transition* at an edge, not the underlying shape — real smoke never has
// a hard edge in open air at all, feathered or not, unless it's actually
// being physically constrained (pressed up a wall, squeezed through a
// gap). A procedural gradient sidesteps the problem entirely, the same fix
// already applied to fire (see genFireGlow): it's smooth by construction,
// so there's no geometric silhouette to expose no matter how large or
// opaque it's drawn.
const (
	smokeGlowFrameCount = 6
	smokeGlowSize       = 96
)

func loadSmokeFrames() []*assets.RGBA {
	if smokeFramesLoaded {
		return smokeFrames
	}
	smokeFramesLoaded = true
	smokeFrames = make([]*assets.RGBA, smokeGlowFrameCount)
	for i := range smokeFrames {
		smokeFrames[i] = genSmokeGlow(smokeGlowSize)
	}
	return smokeFrames
}

// genSmokeGlow procedurally paints one soft, cloud-like smoke puff: several
// overlapping soft round lobes at random offsets/sizes, each falling off
// smoothly to transparent and combined by MAX (not sum, so overlaps don't
// harden into a brighter seam) — the combined silhouette reads as an
// irregular, billowy puff instead of one perfect circle, while still being
// entirely free of any hard edge at any render size. Pale, fairly neutral
// gray: per-puff tint/darkness is layered on separately via each consumer's
// own light multiplier and/or tintFrames (steam/mist/toxic smoke), not
// baked in here.
func genSmokeGlow(size int) *assets.RGBA {
	pix := make([]byte, size*size*4)
	c := float64(size) / 2
	type lobe struct{ x, y, r float64 }
	n := 3 + int(pRandomF01()*3) // 3-5 lobes
	lobes := make([]lobe, n)
	for i := range lobes {
		ang := pRandomF01() * 2 * math.Pi
		dist := pRandomF01() * c * 0.35
		lobes[i] = lobe{
			x: c + math.Cos(ang)*dist,
			y: c + math.Sin(ang)*dist,
			r: c * (0.55 + 0.35*pRandomF01()),
		}
	}
	gray := float32(165 + pRandomF01()*45) // pale gray — darkness/tint applied elsewhere
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var a float64
			for _, b := range lobes {
				dx, dy := float64(x)-b.x, float64(y)-b.y
				if d := math.Sqrt(dx*dx+dy*dy) / b.r; d < 1 {
					if v := math.Pow(1-d, 2.0); v > a {
						a = v
					}
				}
			}
			o := (y*size + x) * 4
			pix[o+0], pix[o+1], pix[o+2] = clampByte(gray), clampByte(gray), clampByte(gray)
			pix[o+3] = clampByte(float32(a * 255))
		}
	}
	return &assets.RGBA{Width: size, Height: size, Pix: pix, TexelsPerUnit: smokeTexelsPerUnit, OffsetX: size / 2, OffsetY: size / 2}
}

// loadSteamFrames returns the (already soft-edged) smoke art re-tinted
// pale and cool — lava's own boil-off, rising fast and clearing quickly,
// not the same dark sooty puff a barrel leaves behind. See
// engine/liquidambience.go's spawnSteamPuff.
func loadSteamFrames() []*assets.RGBA {
	if steamFramesLoaded {
		return steamFrames
	}
	steamFramesLoaded = true
	base := loadSmokeFrames()
	if len(base) == 0 {
		return nil
	}
	steamFrames = tintFrames(base, [3]float32{0.9, 0.95, 1.0}, [3]float32{60, 60, 55})
	return steamFrames
}

// loadMistFrames returns the smoke art re-tinted cool and dim — a thin haze
// clinging low over open water rather than rising off it. See
// engine/liquidambience.go's spawnMistPuff.
func loadMistFrames() []*assets.RGBA {
	if mistFramesLoaded {
		return mistFrames
	}
	mistFramesLoaded = true
	base := loadSmokeFrames()
	if len(base) == 0 {
		return nil
	}
	mistFrames = tintFrames(base, [3]float32{0.6, 0.75, 0.95}, [3]float32{20, 35, 55})
	return mistFrames
}

// loadToxicSmokeFrames returns the smoke art re-tinted a dark, sickly
// green — the burning-puddle fire's own smoke (engine/burningpuddle.go),
// distinct from ordinary sooty gray: this is flammable chemical runoff
// burning off, not wood/fuel smoke, and should read as visibly toxic. The
// low multipliers (well under loadMistFrames' own, itself already dim) keep
// it dark rather than merely tinted, since puddleSmokeAlpha0/Hold are also
// pushed toward near-total, long-held opacity — see spawnPuddleSmoke.
func loadToxicSmokeFrames() []*assets.RGBA {
	if toxicSmokeFramesLoaded {
		return toxicSmokeFrames
	}
	toxicSmokeFramesLoaded = true
	base := loadSmokeFrames()
	if len(base) == 0 {
		return nil
	}
	toxicSmokeFrames = tintFrames(base, [3]float32{0.30, 0.50, 0.26}, [3]float32{6, 12, 5})
	return toxicSmokeFrames
}

// loadBubbleFrames returns the spark art re-tinted green-yellow, standing
// in for acid bubbles surfacing and popping — see
// engine/liquidambience.go's spawnBubble and engine/liquidsplash.go's acid
// splash, which both reuse it rather than requiring dedicated bubble art.
func loadBubbleFrames() []*assets.RGBA {
	if bubbleFramesLoaded {
		return bubbleFrames
	}
	bubbleFramesLoaded = true
	base := loadSparkFrames()
	if len(base) == 0 {
		return nil
	}
	bubbleFrames = tintFrames(base, [3]float32{0.5, 0.9, 0.3}, [3]float32{10, 40, 5})
	return bubbleFrames
}

// teleportGlitterFrameCount/Size/TexelsPerUnit: loadTeleportGlitterFrames
// returns procedurally-generated soft glints (genTeleportGlitter) rather
// than any WAD sprite art — same reasoning as fire/smoke's own switch to
// procedural texture generation (see genFireGlow's doc comment): a
// procedural radial gradient guarantees no hard-edged silhouette, which a
// pixel-art sprite would expose the instant real/additive blending draws
// it at particle scale. A handful of independently-generated frames (not
// one frame reused) gives each mote a slightly different glint shape as it
// animates, reading as a subtle twinkle rather than a perfectly uniform dot.
const (
	teleportGlitterFrameCount    = 4
	teleportGlitterGlyphSize     = 24
	teleportGlitterTexelsPerUnit = 12.0
)

func loadTeleportGlitterFrames() []*assets.RGBA {
	if teleportGlitterFramesLoaded {
		return teleportGlitterFrames
	}
	teleportGlitterFramesLoaded = true
	teleportGlitterFrames = make([]*assets.RGBA, teleportGlitterFrameCount)
	for i := range teleportGlitterFrames {
		teleportGlitterFrames[i] = genTeleportGlitter(teleportGlitterGlyphSize)
	}
	return teleportGlitterFrames
}

// genTeleportGlitter procedurally paints one small soft glint — a bright
// white-hot core cooling to blue at the rim, entirely soft-edged (see this
// function group's own doc comment above) — standing in for a teleporter
// pad's rising light motes (engine/teleportambience.go).
func genTeleportGlitter(size int) *assets.RGBA {
	pix := make([]byte, size*size*4)
	c := float64(size-1) / 2
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx, dy := float64(x)-c, float64(y)-c
			d := math.Sqrt(dx*dx+dy*dy) / c
			o := (y*size + x) * 4
			if d >= 1 {
				pix[o+3] = 0
				continue
			}
			a := math.Pow(1-d, 2.4)
			pix[o+0] = clampByte(float32(255 * math.Max(0, 1-1.6*d)))
			pix[o+1] = clampByte(float32(255 * math.Max(0.55, 1-0.9*d)))
			pix[o+2] = 255
			pix[o+3] = clampByte(float32(a * 255))
		}
	}
	return &assets.RGBA{Width: size, Height: size, Pix: pix, TexelsPerUnit: teleportGlitterTexelsPerUnit, OffsetX: size / 2, OffsetY: size / 2}
}

func clampByte(v float32) byte {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return byte(v)
}

// loadFramePack loads a named animation (letters, e.g. "BPUF" A-D) from
// assets/particles/explosion/, decoding each at tpu texels/map-unit. label
// is just for the log line. Returns nil (logging why, non-fatally) if the
// pack directory or any single frame is missing — a partial set would
// animate oddly, so it's all-or-nothing.
func loadFramePack(label, prefix string, letters []byte, tpu float64) []*assets.RGBA {
	dir, ok := assets.FindAssetDir("assets/particles/explosion")
	if !ok {
		log.Printf("particles: no assets/particles/explosion found — %s disabled (non-fatal)", label)
		return nil
	}
	var frames []*assets.RGBA
	for _, letter := range letters {
		path := filepath.Join(dir, prefix+string(letter)+"0.png")
		im := loadParticlePNG(path, tpu)
		if im == nil {
			log.Printf("particles: %s missing/unreadable — %s disabled (non-fatal)", path, label)
			return nil
		}
		frames = append(frames, im)
	}
	log.Printf("particles: loaded %d %s frame(s) from %s", len(frames), label, dir)
	return frames
}

// loadParticlePNG decodes path into an *assets.RGBA at tpu texels per map
// unit, or nil if the file isn't there or isn't a valid image — never fatal.
func loadParticlePNG(path string, tpu float64) *assets.RGBA {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		return nil
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil
	}
	pix := make([]byte, w*h*4)
	if nrgba, ok := img.(*image.NRGBA); ok && nrgba.Stride == w*4 && b.Min.X == 0 && b.Min.Y == 0 {
		copy(pix, nrgba.Pix)
	} else {
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				r, g, bch, a := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
				o := (y*w + x) * 4
				pix[o], pix[o+1], pix[o+2], pix[o+3] = byte(r>>8), byte(g>>8), byte(bch>>8), byte(a>>8)
			}
		}
	}
	return &assets.RGBA{
		Width: w, Height: h, Pix: pix, TexelsPerUnit: tpu,
		// worldgeo.AddSprites anchors a billboard at (OffsetX, OffsetY) from
		// its top-left corner — a monster/pickup sprite sets this to its
		// WAD-authored "feet" hotspot, but these have no such hotspot, so
		// leaving it at the zero value anchored the quad by its top-left
		// corner instead: it rendered offset to one side and, vertically,
		// entirely *below* the spawn point — at a floor-level explosion that
		// buried the whole sprite under the floor, invisible. Half the
		// image's own size centers the quad on (X, Y, Z) instead.
		OffsetX: w / 2, OffsetY: h / 2,
	}
}
