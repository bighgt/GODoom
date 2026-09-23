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
	// orbitSpeed, when nonzero, makes updateParticles override this
	// particle's X/Y every tic to travel in a circle of orbitRadius around
	// (orbitCenterX, orbitCenterY) — starting at angle orbitPhase0, turning
	// at orbitSpeed radians/sec — instead of integrating DX/DY as straight-
	// line drift. DZ/gravity still apply normally, so combined with a
	// rising DZ this reads as a particle spiralling upward. Zero (the zero
	// value, what every other spawn site in this package leaves it at)
	// means "use DX/DY normally" — see engine/teleportambience.go's rising
	// glitter for the one consumer of this today.
	orbitCenterX, orbitCenterY, orbitRadius, orbitPhase0, orbitSpeed float64
	// bounceFloor, when true, makes updateParticles reflect DZ (scaled by
	// particleBounceDamping) instead of letting a particle sink through the
	// sector floor beneath its own CURRENT (X, Y) — off by default (every
	// spawn site but the BFG's own spark layer leaves this false, so
	// ordinary particles keep flying through geometry unobstructed exactly
	// as before). Resolved live each tic, not cached from the burst's
	// origin: sparks fly far enough (particleSpeedMax alone is 500u/sec)
	// to cross into a sector with a different floor height than where the
	// explosion happened — extremely common in Doom maps (steps, raised
	// platforms, the next room over) — so a single cached height either
	// leaves them bouncing in mid-air over a lower floor or sinking through
	// a raised one. See ExplosionScale.SparkBounceFloor and
	// spawnBFGExtraSparks.
	bounceFloor bool
	// bounces, when bounceFloor is set, caps how many more times this
	// particle is allowed to bounce before it just settles at the floor
	// (DZ pinned to 0) instead of reflecting again. Negative means
	// unlimited — keep reflecting (damped by particleBounceDamping each
	// time) until it naturally settles or its life runs out, the original
	// behavior. spawnBurst always sets this explicitly (-1 unless the
	// cause set ExplosionScale.SparkBounceMin/Max — currently only the
	// rocket launcher's own spark layer does), so every OTHER &Particle{}
	// literal in this package (nothing outside spawnBurst constructs a
	// bouncing one) is fine leaving this at its zero value: 0 would
	// otherwise read as "already out of bounces" and settle on first
	// contact, not "unlimited" — the field is written so nothing has to
	// know that to stay correct.
	bounces int
	// pulseHz/pulseAmp/pulsePhase, when pulseHz > 0, make this particle's
	// rendered SIZE breathe rhythmically — collectAllParticleSprites
	// multiplies its size by 1+pulseAmp*sin(age*pulseHz*2π+pulsePhase) —
	// instead of holding steady. Size, not light: a fullBright additive
	// particle (KindSpriteFull) is shaded as tex.rgb*1.35 in the shader
	// with no read of this struct's own light field at all (see
	// worldgeo.AddParticleSprites/world.frag's KIND_SPRITE_FULL case), so
	// modulating light would be invisible for exactly the particles (the
	// teleporter's disco glitter, see engine/teleportambience.go) this was
	// built for — a visibly swelling/shrinking mote reads as "pulsing
	// light" just as well and actually shows up. Zero pulseHz (every spawn
	// site but the teleport glitter) means no pulse, unchanged.
	pulseHz, pulseAmp, pulsePhase float64
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

// explosionBurstMax caps how many particles any ONE spawnBurst call (one
// layer — sparks, chunks, or fire — of one explosion) can actually spawn,
// no matter how large Count/SparkCount/FireCount combine to compute n.
// Concrete incident this guards against: explosionRocket's SparkCount:10
// stacks with its own Count:10 — spawnBurst's own comment explains why
// that compounding (SparkCount on top of Count) is deliberate — to
// explosionParticleCount*10*10 = 320,000 sparks from ONE rocket hit, which
// is what actually took a real session from 450 FPS to ~20 FPS the instant
// a rocket exploded on screen: hundreds of thousands of additive billboards
// (unlike the translucent layer, nothing caps these — see
// maxTransparentParticles below) all alive at once, each paying its own
// share of updateParticles/collectAllParticleSprites every frame for its
// ~0.5s life. Applied at spawn time (not just before the GPU submit,
// unlike maxTransparentParticles' own cap) so it also saves that per-frame
// CPU update cost, not only the draw cost. 2000 is generous — comparable
// to or larger than explosionBFG's own ~3200-spark burst before its own
// separate +3000 flat bonus (bfgExtraSparkCount), and BFG isn't the
// complaint here — while still nowhere near catastrophic.
const explosionBurstMax = 2000

const (
	explosionParticleCount = 3200
	particleLifeMin        = 0.45
	particleLifeMax        = 0.75
	particleSpeedMin       = 140.0
	particleSpeedMax       = 500.0
	particleGravity        = 400.0 // units/sec^2
	particleDrag           = 0.9   // 1/sec velocity decay (air resistance) — see updateParticles
	// particleBounceDamping is the fraction of vertical speed a
	// bounceFloor particle keeps after reflecting off the floor — see
	// Particle.bounceFloor. <1 so each bounce loses energy and the particle
	// settles rather than bouncing forever at the same height.
	particleBounceDamping = 0.45
	particleFrameRate     = 24.0  // frames/sec through the 4-frame spark loop
	particleSizeMin       = 0.125 // extra multiplier on top of particleTexelsPerUnit's
	particleSizeMax       = 0.25  // native size
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
	// SparkIsotropic, when true, launches the spark layer in a full sphere
	// (straight down to straight up) instead of the default upward-only
	// cone (0.2..0.8 of straight up — see spawnBurst's own comment), so
	// the blast throws shrapnel outward in every direction at once, evenly
	// centred on the impact point, rather than reading as a mostly-upward
	// fountain. false (every other cause) keeps the original upward cone,
	// unchanged.
	SparkIsotropic bool
	// SparkBounceFloor, when true, makes the spark layer's own particles
	// bounce off the floor (Particle.bounceFloor) instead of flying through
	// it — the BFG's own signature: its sparks visibly skitter/bounce
	// across the floor instead of clipping through it. false (every other
	// cause) leaves sparks unobstructed, exactly as before.
	SparkBounceFloor bool
	// SparkLifeMin/SparkLifeMax, when SparkLifeMax > 0, replace the shared
	// particleLifeMin/particleLifeMax outright for the spark layer only —
	// the BFG's own sparks need to live long enough to actually fall,
	// bounce a few times (SparkBounceFloor), and settle, which the shared
	// short spark life (well under a second, tuned for a quick ballistic
	// burst) never gives them time to do. Zero (the zero value, what every
	// other cause leaves this at) means "use the shared range unchanged."
	SparkLifeMin, SparkLifeMax float64
	// SparkBounceMin/SparkBounceMax, when SparkBounceMax > 0 (and
	// SparkBounceFloor is also set), gives each spark its own random
	// Particle.bounces budget in [SparkBounceMin, SparkBounceMax] instead
	// of bouncing indefinitely — the rocket launcher's own debris settles
	// after a handful of bounces rather than skittering around forever.
	// 0 (every other cause, including the BFG/plasma sparks that pioneered
	// SparkBounceFloor) means unlimited, unchanged.
	SparkBounceMin, SparkBounceMax int
	// SparkMaxCount, when > 0, overrides explosionBurstMax as the spark
	// layer's own spawn cap for this cause — see explosionBurstMax's own
	// doc comment on why that shared safety cap exists, and burstParams.
	// maxCount for how a call opts out of it. 0 (every cause but the
	// rocket launcher) means "use the shared cap," unchanged.
	SparkMaxCount int
	// ChunkBounceFloor is SparkBounceFloor's counterpart for the chunk
	// debris layer — false (every cause but the plasma rifle) leaves
	// chunks unobstructed, exactly as before.
	ChunkBounceFloor bool
	// NoChunks skips the chunk debris layer entirely for this cause — the
	// BFG's own chunk art, scaled up by its own 10x Size and then
	// green-tinted, read as big shapeless blobs falling out of the blast
	// rather than debris, so its explosion is sparks only. false (every
	// other cause) spawns chunks exactly as before.
	NoChunks bool
	// SparkFrames/ChunkFrames/FireFrames, when set, load this cause's own
	// recolored debris art instead of the shared loadSparkFrames/
	// loadChunkFrames/loadFireFrames — e.g. the BFG's green tint (see
	// loadBFGSparkFrames et al.), so its whole blast reads as one color
	// instead of fighting its own green explosion animation with generic
	// yellow/orange debris. nil (every other cause) means "use the shared
	// art," unchanged. Functions rather than plain slices so nothing is
	// loaded/tinted until an explosion of that cause actually happens.
	SparkFrames, ChunkFrames, FireFrames func() []*assets.RGBA
	// SparkColorVariants, when set, makes each spark particle of this
	// cause independently pick a random one of several pre-tinted frame
	// sets instead of every spark in the burst sharing SparkFrames' one
	// fixed tint — e.g. the plasma rifle's sparks scattered across white to
	// deep blue (loadPlasmaSparkColorVariants), or the rocket launcher's
	// across red/orange/yellow/white (loadRocketSparkColorVariants),
	// instead of one flat tint repeated on every spark. nil (every other
	// cause) means "no per-particle variation," unchanged.
	SparkColorVariants func() [][]*assets.RGBA
}

// explosionDefault covers monster fireballs — unchanged from the original
// tuning. explosionRocket is the rocket launcher's boosted burst: 10x as
// many sparks on top of the spark layer's own Count scaling (further
// tuned by SparkMaxCount/SparkColorVariants/SparkBounceMin/Max/
// SparkIsotropic — see the struct literal below and their own field doc
// comments for what each does and why) — a rocket's blast is all
// shrapnel, sprayed outward from the impact point in every direction at
// once (SparkIsotropic) and nothing else: no chunk debris (NoChunks — the
// tumbling debris layer reads as generic grey blobs visibly raining down
// out of the blast and landing, not as shrapnel — the same complaint
// explosionBFG's own NoChunks already exists to avoid, see its doc
// comment below) and no lingering fire layer either (no FireCount — a
// real explosion radiates outward and is over, it doesn't hang around
// afterward as a rising fireball). explosionBarrel is bigger again (2x
// the rocket's own size), and fans its
// fire out low across the floor (FireSpread) rather than shooting upward,
// since it's igniting spilled fuel, not just burning in place — see
// engine/burningpuddle.go for the fire (and smoke) that keeps burning
// there afterward. explosionEnergy (plasma) is the same size as default,
// but at half the particle count (Count: 0.5), with its own spark layer
// halved yet again on top of that (SparkCount: 0.5 — a plasma bolt is a
// small, frequent shot, not a one-off blast, so its burst — sparks
// especially, the layer that dominates the particle count — is toned down
// from the shared baseline). Its SparkFrames/ChunkFrames/FireFrames are
// all re-tinted blue/white hot
// (tintFramesHot — the same "white-blue-hot" tint the rocket exhaust jet
// already uses, see loadRocketFlameFrames) instead of the generic
// yellow/orange every other explosion's debris and fire use, matching its
// own cyan-blue plasma bolt (lights.go's PLSE case) rather than fighting
// it. SparkColorVariants (loadPlasmaSparkColorVariants) then layers actual
// per-spark colour variety on top of that shared tint: each spark
// independently picks a random shade between white-hot and deep blue, so
// a burst reads as a scatter of differently-coloured sparks rather than
// one flat tint repeated on every one of them.
// SparkBounceFloor/ChunkBounceFloor+SparkLifeMin/Max make its sparks
// and chunks bounce a few times along the floor before fading, the same
// reasoning as explosionBFG's own below.
// explosionBFG is energy's much bigger sibling: 10x wider — the BFG is a
// massive weapon and reads like one. When a rocket lands the killing hit
// on a barrel, the two no longer stack independently — see
// rocketBarrelExplosionBoost in projectiles.go — the barrel's own burst is
// boosted instead, so it's one bigger explosion, not two overlapping ones.
// On top of this, its own explosion additionally throws bfgExtraSparkCount
// more sparks outward — see spawnBFGExtraSparks, called right alongside
// spawnExplosionParticles in updateProjectiles. No FireCount and NoChunks:
// both its lingering fire licks and its chunk debris read as big,
// shapeless green blobs at this size once tinted, rather than looking like
// fire or debris, so the BFG's blast is sparks only, sprayed out in every
// direction. SparkFrames is re-tinted green (see loadBFGSparkFrames) so
// every spark matches its own green BFE1/BFE2 explosion animation and
// green dynamic light (lights.go's BFE1 case) instead of fighting it with
// the generic yellow/orange every other explosion's sparks use.
// SparkBounceFloor+SparkLifeMin/Max make its sparks bounce a few times
// along the floor before fading, instead of the shared short spark life
// (well under a second) that never gave them time to fall and land even
// once — see Particle.bounceFloor.
var (
	explosionDefault = ExplosionScale{Size: 1, Count: 1, FireCount: 1, FireLife: 1, FireSize: 1}
	explosionRocket  = ExplosionScale{
		// Size doubled (1.8 -> 3.6): with NoChunks on (and no fire layer at
		// all — see below), Size only reaches the spark layer now, so this
		// alone is "sparks twice as large."
		Size: 3.6, Count: 10, NoChunks: true,
		// No FireCount (0, the zero value — see spawnBurst's own n *=
		// scale.Count: a 0 FireCount computes 0 fire particles regardless
		// of Count, the same "just omit it" shape explosionBFG's own doc
		// comment below already uses): no lingering flame-lick fireball at
		// all, blast is sparks only — a real explosion radiates outward
		// from the impact point and is over; it doesn't hang around as a
		// rising fireball afterward. FireLife/FireSize dropped too, since
		// there's no fire layer left for them to tune.
		// SparkMaxCount: 6000 sparks per rocket hit — well past
		// explosionBurstMax's shared 2000 safety cap (see its own doc
		// comment on the incident that added it), but SparkCount:10 stacked
		// with Count:10 already computes 320,000 before any cap, so this
		// override just moves where THIS cause's own ceiling sits rather
		// than fighting the shared one. SparkIsotropic: shrapnel sprays out
		// in every direction from the impact point, not mostly upward —
		// see ExplosionScale.SparkIsotropic's own doc comment.
		SparkCount: 10, SparkMaxCount: 6000, SparkIsotropic: true,
		// A glowing red/orange/yellow/white scatter (loadRocketSparkColorVariants)
		// instead of the generic yellow/orange every spark shared before,
		// and each one twice normal size (see Size above) and bouncing 1-5
		// times (SparkBounceMin/Max) before settling — SparkLifeMin/Max
		// borrows the BFG/plasma sparks' own longer life (bfgSparkLifeMin/
		// Max) since the shared short default (well under a second) never
		// gave a spark time to reach the floor even once, let alone bounce
		// repeatedly.
		SparkColorVariants: loadRocketSparkColorVariants,
		SparkBounceFloor:   true, SparkBounceMin: 1, SparkBounceMax: 5,
		SparkLifeMin: bfgSparkLifeMin, SparkLifeMax: bfgSparkLifeMax,
	}
	explosionBarrel = ExplosionScale{Size: 3.4, Count: 10, FireCount: 4.4, FireLife: 1, FireSize: 6, FireSpread: true}
	explosionEnergy = ExplosionScale{
		Size: 1, Count: 0.5, SparkCount: 0.5, FireCount: 1, FireLife: 1, FireSize: 1,
		SparkFrames: loadPlasmaSparkFrames, ChunkFrames: loadPlasmaChunkFrames, FireFrames: loadPlasmaFireFrames,
		SparkColorVariants: loadPlasmaSparkColorVariants,
		SparkBounceFloor:   true, ChunkBounceFloor: true,
		SparkLifeMin: plasmaSparkLifeMin, SparkLifeMax: plasmaSparkLifeMax,
	}
	explosionBFG = ExplosionScale{
		Size: 10, Count: 1,
		SparkFrames:      loadBFGSparkFrames,
		SparkBounceFloor: true,
		SparkLifeMin:     bfgSparkLifeMin, SparkLifeMax: bfgSparkLifeMax,
		NoChunks: true,
	}
)

// scaledExplosion returns scale with its "how big / how much" dials — Size,
// Count, FireSize, FireCount — multiplied by mult, leaving its qualitative
// dials (FireSpread, SparkIsotropic, ...) untouched: a boosted explosion (see
// aExplode's use of Mobj.explosionBoost) should look like more of the same
// fire, not a differently timed or shaped one.
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
	if scale.SparkFrames != nil {
		sparks = scale.SparkFrames()
	}
	chunks := loadChunkFrames()
	if scale.ChunkFrames != nil {
		chunks = scale.ChunkFrames()
	}
	fire := loadFireFrames()
	if scale.FireFrames != nil {
		fire = scale.FireFrames()
	}
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
		burstMax := explosionBurstMax
		if bp.maxCount > 0 {
			burstMax = bp.maxCount
		}
		if n > burstMax {
			n = burstMax
		}
		sizeMin, sizeMax := bp.sizeMin*scale.Size, bp.sizeMax*scale.Size
		for i := 0; i < n; i++ {
			var dx, dy, dz float64
			switch {
			case bp.lowAngle:
				// Shallow, near-horizontal launch (fireSpreadPitchMin/Max), sped
				// up by fireSpreadSpeedMul — burning fuel fanning out across the
				// floor rather than shooting upward like debris.
				dx, dy, dz = coneVelocity(fireSpreadPitchMin, fireSpreadPitchMax, bp.speedMin*fireSpreadSpeedMul, bp.speedMax*fireSpreadSpeedMul)
			case bp.isotropic:
				// Full sphere (-1..1 of straight up, i.e. straight down to
				// straight up) instead of the default upward-only cone — a
				// real blast throws shrapnel outward in every direction at
				// once, not mostly upward, so this layer's spawn shouldn't
				// have a blind spot below the horizon at all. See
				// ExplosionScale.SparkIsotropic.
				dx, dy, dz = coneVelocity(-1, 1, bp.speedMin, bp.speedMax)
			default:
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
			// colorVariants, when this cause set one (ExplosionScale.
			// SparkColorVariants), gives this ONE particle its own
			// independently-random tint instead of every particle in the
			// burst sharing bp.frames — a single cheap index pick, not a
			// re-tint (that stays a fixed, cached set of pre-built variants;
			// see loadPlasmaSparkColorVariants).
			frames := bp.frames
			if n := len(bp.colorVariants); n > 0 {
				frames = bp.colorVariants[pRandom()%n]
			}
			// bounces: -1 (unlimited) unless this cause gave the spark
			// layer its own random budget (ExplosionScale.SparkBounceMin/
			// Max) — see Particle.bounces' own doc comment on why the
			// zero value can't mean "unlimited" here.
			bounces := -1
			if bp.bounceMax > 0 {
				bounces = bp.bounceMin + pRandom()%(bp.bounceMax-bp.bounceMin+1)
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
				bounceFloor:   bp.bounceFloor,
				bounces:       bounces,
				frames:        frames,
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
	sparkLifeMin, sparkLifeMax := particleLifeMin, particleLifeMax
	if scale.SparkLifeMax > 0 {
		sparkLifeMin, sparkLifeMax = scale.SparkLifeMin, scale.SparkLifeMax
	}
	var sparkColorVariants [][]*assets.RGBA
	if scale.SparkColorVariants != nil {
		sparkColorVariants = scale.SparkColorVariants()
	}
	spawnBurst(int(float64(explosionParticleCount)*sparkCount), burstParams{
		speedMin: particleSpeedMin, speedMax: particleSpeedMax, lifeMin: sparkLifeMin, lifeMax: sparkLifeMax,
		sizeMin: particleSizeMin, sizeMax: particleSizeMax, frameRate: particleFrameRate, gravity: particleGravity,
		light: 1, fullBright: true, bounceFloor: scale.SparkBounceFloor, frames: sparks,
		colorVariants: sparkColorVariants, isotropic: scale.SparkIsotropic,
		bounceMin: scale.SparkBounceMin, bounceMax: scale.SparkBounceMax,
		maxCount: scale.SparkMaxCount,
	})
	// Chunks: a handful of bigger, slower, longer-lived tumbling debris
	// pieces mixed in so the burst isn't a uniform cloud of identical dots.
	// Skipped entirely for a cause that sets NoChunks (see its own doc
	// comment — the BFG's own chunk art read as shapeless blobs, not
	// debris, at its size).
	if !scale.NoChunks {
		spawnBurst(explosionChunkCount, burstParams{
			speedMin: chunkSpeedMin, speedMax: chunkSpeedMax, lifeMin: chunkLifeMin, lifeMax: chunkLifeMax,
			sizeMin: chunkSizeMin, sizeMax: chunkSizeMax, frameRate: chunkFrameRate, gravity: particleGravity,
			light: 1, fullBright: true, bounceFloor: scale.ChunkBounceFloor, frames: chunks,
		})
	}
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

// bfgExtraSparkCount is a flat number of extra spark particles thrown
// outward the instant a BFG ball explodes — on top of its own explosionBFG
// burst (see spawnExplosionParticles) — giving the biggest weapon in the
// game a dramatically bigger, more energetic-looking burst of debris
// without touching the shared ExplosionScale dials every other explosion
// type also uses.
const bfgExtraSparkCount = 3000

// bfgSparkLifeMin/Max: how long a BFG spark lives — long enough for even a
// fairly steep launch angle to fall back down, bounce (Particle.
// bounceFloor/particleBounceDamping) a few times as each bounce loses
// energy, and fade out, instead of the shared particleLifeMin/Max (well
// under a second, tuned for a quick ballistic spark burst) which would
// expire most of them well before they ever reached the floor once. Used
// by both explosionBFG's own SparkLifeMin/Max and spawnBFGExtraSparks.
const (
	bfgSparkLifeMin = 1.4
	bfgSparkLifeMax = 2.4
)

// plasmaSparkLifeMin/Max is explosionEnergy's own SparkLifeMin/Max — same
// reasoning and same values as bfgSparkLifeMin/Max above, just named for
// its own cause since the two are tuned independently even though they
// currently agree.
const (
	plasmaSparkLifeMin = 1.4
	plasmaSparkLifeMax = 2.4
)

// spawnBFGExtraSparks throws bfgExtraSparkCount additional spark particles
// outward from (x, y, z) — the BFG ball's own explosion point — using the
// same upward-biased cone and physics as spawnExplosionParticles' own
// spark layer, tinted the same BFG green as the rest of its burst (see
// loadBFGSparkFrames) rather than the generic yellow/white every other
// explosion's sparks use, and bouncing off the floor (Particle.bounceFloor)
// the same way explosionBFG's own SparkBounceFloor sparks do. A no-op if
// the feature is off or the spark art isn't loaded, same as every other
// particle spawn in this file.
func (g *Game) spawnBFGExtraSparks(x, y, z float64) {
	if !g.explosionParticles {
		return
	}
	sparks := loadBFGSparkFrames()
	if len(sparks) == 0 {
		return
	}
	for i := 0; i < bfgExtraSparkCount; i++ {
		dx, dy, dz := coneVelocity(0.2, 0.8, particleSpeedMin, particleSpeedMax)
		g.particles = append(g.particles, newPooledParticle(Particle{
			X: x, Y: y, Z: z,
			DX:          dx,
			DY:          dy,
			DZ:          dz,
			life:        randRange(bfgSparkLifeMin, bfgSparkLifeMax),
			sizeMul:     randRange(particleSizeMin, particleSizeMax),
			framePhase:  pRandom(),
			frameRate:   particleFrameRate,
			gravity:     particleGravity,
			light:       1,
			fullBright:  true,
			bounceFloor: true,
			frames:      sparks,
		}))
	}
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
	isotropic          bool    // full-sphere launch instead of the default upward-only cone — see ExplosionScale.SparkIsotropic
	bounceFloor        bool    // see Particle.bounceFloor / ExplosionScale.SparkBounceFloor
	frames             []*assets.RGBA
	// colorVariants, when non-empty, overrides frames per particle: each
	// one spawned this call independently picks a random entry instead of
	// every particle in the burst sharing frames — see
	// ExplosionScale.SparkColorVariants.
	colorVariants [][]*assets.RGBA
	// bounceMin/bounceMax, when bounceMax > 0, gives each particle its own
	// random Particle.bounces budget in [bounceMin, bounceMax] instead of
	// unlimited bouncing — see ExplosionScale.SparkBounceMin/Max.
	bounceMin, bounceMax int
	// maxCount, when > 0, overrides explosionBurstMax as this call's own
	// spawn cap — see explosionBurstMax's own doc comment. 0 (every call
	// but the rocket launcher's own spark layer) means "use the shared
	// cap," unchanged.
	maxCount int
}

// updateParticles advances every live particle by dt seconds — straight-line
// travel plus its own gravity (sparks/chunks fall, fire licks drift up) for
// an ordinary particle, or a clean circle around a fixed centre for one
// with orbitSpeed set (see Particle.orbitSpeed's own doc comment) — and
// drops any that outlived their life. Cheap to call every frame even with
// zero particles.
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
		if p.orbitSpeed != 0 {
			// Circular motion overrides DX/DY entirely — driven off p.age
			// (not accumulated per-tic) so there's no drift, just a clean
			// circle of orbitRadius around (orbitCenterX, orbitCenterY).
			ang := p.orbitPhase0 + p.orbitSpeed*p.age
			p.X = p.orbitCenterX + p.orbitRadius*math.Cos(ang)
			p.Y = p.orbitCenterY + p.orbitRadius*math.Sin(ang)
		} else {
			// Air resistance: without this a spark stays at full horizontal
			// speed for its whole life and the burst reads as a perfect
			// ballistic spray; decaying it lets the swarm visibly "settle"
			// instead.
			drag := 1 - particleDrag*dt
			if drag < 0 {
				drag = 0
			}
			p.DX *= drag
			p.DY *= drag
			p.X += p.DX * dt
			p.Y += p.DY * dt
		}
		p.Z += p.DZ * dt
		if p.bounceFloor {
			if floor, _ := g.sectorFloorCeil(p.X, p.Y); p.Z < floor {
				p.Z = floor
				if p.bounces == 0 {
					// Budget spent (see Particle.bounces' own doc comment on
					// why 0 means exactly this, not "unlimited") — settle
					// instead of reflecting again.
					p.DZ = 0
				} else {
					p.DZ = -p.DZ * particleBounceDamping
					if p.bounces > 0 {
						p.bounces--
					}
				}
			}
		}
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
// particleSightRadius is the same generous view-cone margin
// collectWorldObjects' mobjSightRadius gives things/projectiles, sized up a
// bit further: a translucent smoke puff grows to sizeMul*smokeGrowEnd (2.6x)
// and drifts as it ages, so it needs more slack than a fixed-size monster
// sprite to avoid visibly popping in right at the screen edge. Cheap to be
// generous here — this is a coarse cone test, not a hard clip, so a few
// extra borderline particles surviving it costs nothing.
const particleSightRadius = 300.0

func (g *Game) collectAllParticleSprites() (additive, transparent []worldgeo.Sprite) {
	additive = g.hwParticleSprites[:0]
	transparent = g.hwTransparentParticleSprites[:0]
	transparentFull := false
	// viewCam mirrors collectWorldObjects' own: g.particles can run into the
	// tens of thousands after a big explosion's spark burst (see this
	// function's own doc comment), and every one of them used to become a
	// worldgeo.Sprite — triangulated, packed, and submitted to the GPU —
	// regardless of whether it was anywhere near the camera's view. Dropping
	// the off-screen ones here, before any of that work happens, is the same
	// win cullLightsToView/collectWorldObjects already bank for lights and
	// things.
	fov := g.Camera.FOVDeg
	if fov <= 0 {
		fov = 90
	}
	viewCam := worldgeo.Camera{X: g.Camera.X, Y: g.Camera.Y, Angle: g.Camera.Angle, FOVDeg: fov}
	for _, p := range g.particles {
		if len(p.frames) == 0 {
			continue
		}
		if !worldgeo.LightVisible(viewCam, p.X, p.Y, particleSightRadius) {
			continue // outside the (widened) view cone — still ages/expires normally, just isn't drawn this frame
		}
		idx := (int(p.age*p.frameRate) + p.framePhase) % len(p.frames)

		if !p.translucent {
			size := p.sizeMul
			if frac := p.age / p.life; frac > particleFadeStart {
				t := (frac - particleFadeStart) / (1 - particleFadeStart) // 0..1 over the back half of life
				size *= 1 - t*(1-particleFadeFloor)
			}
			if p.pulseHz > 0 {
				size *= 1 + p.pulseAmp*math.Sin(p.age*p.pulseHz*2*math.Pi+p.pulsePhase)
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
	sparkFrames             []*assets.RGBA
	sparkFramesLoaded       bool
	chunkFrames             []*assets.RGBA
	chunkFramesLoaded       bool
	fireFrames              []*assets.RGBA
	fireFramesLoaded        bool
	smokeFrames             []*assets.RGBA
	smokeFramesLoaded       bool
	rocketFlameFrames       []*assets.RGBA
	rocketFlameFramesLoaded bool
	steamFrames             []*assets.RGBA
	steamFramesLoaded       bool
	mistFrames              []*assets.RGBA
	mistFramesLoaded        bool
	bubbleFrames            []*assets.RGBA
	bubbleFramesLoaded      bool
	toxicSmokeFrames        []*assets.RGBA
	toxicSmokeFramesLoaded  bool
	// teleportGlitterColorVariants[c] is colour c's own full set of
	// teleportGlitterFrameCount length-variant frames — see
	// loadTeleportGlitterColorVariants.
	teleportGlitterColorVariants       [][]*assets.RGBA
	teleportGlitterColorVariantsLoaded bool
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

// tintFramesBFGGreen returns new copies of src pushed toward the BFG's own
// green instead of whatever hue the source art natively has — the same
// green as its BFE1/BFE2 explosion animation and its dynamic light
// (lights.go's BFE1 case: r=0.50, g=1.0, b=0.45) — see tintFrames. Red and
// blue are both pulled well down while green is pushed to its ceiling, so
// even a hot white/yellow source core reads as bright green instead of
// leaving a pale or yellow-white centre behind.
func tintFramesBFGGreen(src []*assets.RGBA) []*assets.RGBA {
	return tintFrames(src, [3]float32{0.35, 1.0, 0.40}, [3]float32{10, 60, 10})
}

// tintKeyframe is one stop along a tintFramesGradient — the same [mul, add]
// pair a single tintFrames call takes, just named so a gradient's keyframe
// list reads as a sequence of colours rather than a wall of anonymous
// [3]float32 pairs.
type tintKeyframe [2][3]float32

// tintFramesGradient returns steps re-tinted copies of src (see
// tintFrames), their tint interpolated evenly along the given sequence of
// keyframes — the first keyframe lands exactly on step 0, the last exactly
// on step steps-1, and every keyframe in between is spaced evenly across
// the run, with straight linear interpolation of mul/add between whichever
// pair of keyframes a given step falls between. Used wherever a burst
// wants each particle to independently land somewhere along a smooth
// multi-colour progression (e.g. loadPlasmaSparkColorVariants' white-to-
// blue, loadRocketSparkColorVariants' red-orange-yellow-white) instead of
// every particle sharing one fixed tint. Needs at least 2 keyframes and a
// positive steps; callers here only ever pass their own fixed constants,
// so this is unchecked rather than returning an error.
func tintFramesGradient(src []*assets.RGBA, steps int, keyframes ...tintKeyframe) [][]*assets.RGBA {
	segs := len(keyframes) - 1
	out := make([][]*assets.RGBA, steps)
	for i := range out {
		pos := float32(i) / float32(steps-1) * float32(segs)
		seg := int(pos)
		if seg >= segs {
			seg = segs - 1
		}
		t := pos - float32(seg)
		a, b := keyframes[seg], keyframes[seg+1]
		var mul, add [3]float32
		for c := 0; c < 3; c++ {
			mul[c] = a[0][c] + (b[0][c]-a[0][c])*t
			add[c] = a[1][c] + (b[1][c]-a[1][c])*t
		}
		out[i] = tintFrames(src, mul, add)
	}
	return out
}

// bfgSparkFrames caches loadBFGSparkFrames' tinted output the same way
// sparkFrames caches the untinted original above.
var (
	bfgSparkFrames       []*assets.RGBA
	bfgSparkFramesLoaded bool
)

// loadBFGSparkFrames returns the shared spark art re-tinted toward the
// BFG's own green (tintFramesBFGGreen) — see explosionBFG's own doc
// comment for why: its blast should add to its green explosion animation,
// not fight it with generic yellow/orange debris. Lazily loaded and cached
// like every other frame pack in this file, so nothing is loaded/tinted
// until a BFG actually explodes. There is no loadBFGChunkFrames or
// loadBFGFireFrames — explosionBFG has no chunk or fire layer at all (see
// its own doc comment: at BFG size, tinted chunks/fire read as big
// shapeless green blobs rather than debris or flame).
func loadBFGSparkFrames() []*assets.RGBA {
	if bfgSparkFramesLoaded {
		return bfgSparkFrames
	}
	bfgSparkFramesLoaded = true
	if base := loadSparkFrames(); len(base) > 0 {
		bfgSparkFrames = tintFramesBFGGreen(base)
	}
	return bfgSparkFrames
}

// plasmaSparkFrames/ChunkFrames/FireFrames cache loadPlasmaSparkFrames et
// al.'s tinted output the same way bfgSparkFrames caches its own above.
var (
	plasmaSparkFrames       []*assets.RGBA
	plasmaSparkFramesLoaded bool
	plasmaChunkFrames       []*assets.RGBA
	plasmaChunkFramesLoaded bool
	plasmaFireFrames        []*assets.RGBA
	plasmaFireFramesLoaded  bool
)

// loadPlasmaSparkFrames/ChunkFrames/FireFrames return the shared
// spark/chunk/fire art re-tinted white-blue-hot (tintFramesHot — the same
// tint the rocket exhaust jet already uses, see loadRocketFlameFrames)
// instead of the generic yellow/orange every other explosion's debris and
// fire use — see explosionEnergy's own doc comment for why: the plasma
// rifle's blast should match its own cyan-blue bolt (lights.go's PLSE
// case), not fight it with yellow fire. Lazily loaded and cached like
// every other frame pack in this file, so nothing is loaded/tinted until a
// plasma bolt actually explodes.
func loadPlasmaSparkFrames() []*assets.RGBA {
	if plasmaSparkFramesLoaded {
		return plasmaSparkFrames
	}
	plasmaSparkFramesLoaded = true
	if base := loadSparkFrames(); len(base) > 0 {
		plasmaSparkFrames = tintFramesHot(base)
	}
	return plasmaSparkFrames
}

// plasmaSparkColorSteps is how many distinct tints loadPlasmaSparkColorVariants
// builds, evenly spanning white-hot (tintFramesHot's own tint) to a deep,
// saturated blue — each plasma spark independently picks one at spawn time
// (see spawnBurst's colorVariants handling), so a burst reads as a genuine
// scatter of different-coloured sparks instead of one flat tint repeated
// across every one of them. 8 is enough steps that the variety reads as
// continuous rather than a few visibly-repeating buckets, without building
// (and holding in memory) many more full copies of the spark art than that
// actually buys visually.
const plasmaSparkColorSteps = 8

// plasmaSparkColorVariants caches loadPlasmaSparkColorVariants' output the
// same way plasmaSparkFrames caches loadPlasmaSparkFrames' above.
var (
	plasmaSparkColorVariants       [][]*assets.RGBA
	plasmaSparkColorVariantsLoaded bool
)

// loadPlasmaSparkColorVariants returns plasmaSparkColorSteps re-tinted
// copies of the shared spark art (loadSparkFrames), their tints linearly
// interpolated from tintFramesHot's own white-blue-hot mul/add (step 0) to
// a deep saturated blue — red and green pushed hard toward black, blue
// held high (step plasmaSparkColorSteps-1) — see tintFrames for the
// mul/add shape. Each variant is tinted ONCE here and cached, not
// per-particle: spawnBurst just indexes into this slice with a single
// random pick per spark, so giving every spark its own colour costs one
// int and one slice access at spawn time, not a re-tint. Lazily built like
// every other frame pack in this file, so nothing is tinted until a plasma
// bolt actually explodes.
func loadPlasmaSparkColorVariants() [][]*assets.RGBA {
	if plasmaSparkColorVariantsLoaded {
		return plasmaSparkColorVariants
	}
	plasmaSparkColorVariantsLoaded = true
	base := loadSparkFrames()
	if len(base) == 0 {
		return nil
	}
	plasmaSparkColorVariants = tintFramesGradient(base, plasmaSparkColorSteps, tintKeyframe{
		{0.85, 0.95, 1.1}, {55, 100, 165}, // white-hot (tintFramesHot's own values)
	}, tintKeyframe{
		{0.15, 0.25, 1.0}, {0, 5, 200}, // deep, saturated blue
	})
	return plasmaSparkColorVariants
}

// rocketSparkColorSteps/rocketSparkColorVariants/Loaded are
// plasmaSparkColorSteps/Variants/Loaded's counterpart for the rocket
// launcher's own spark layer — see loadRocketSparkColorVariants.
const rocketSparkColorSteps = 12

var (
	rocketSparkColorVariants       [][]*assets.RGBA
	rocketSparkColorVariantsLoaded bool
)

// loadRocketSparkColorVariants returns rocketSparkColorSteps re-tinted
// copies of the shared spark art, their tints stepped evenly through a
// glowing-ember gradient — red, to orange, to yellow, to white — instead
// of one flat tint, the same "each spark independently picks one at spawn
// time" shape loadPlasmaSparkColorVariants already uses (see
// tintFramesGradient). Lazily built and cached like every other frame pack
// in this file, so nothing is tinted until a rocket actually explodes.
func loadRocketSparkColorVariants() [][]*assets.RGBA {
	if rocketSparkColorVariantsLoaded {
		return rocketSparkColorVariants
	}
	rocketSparkColorVariantsLoaded = true
	base := loadSparkFrames()
	if len(base) == 0 {
		return nil
	}
	rocketSparkColorVariants = tintFramesGradient(base, rocketSparkColorSteps,
		tintKeyframe{{0.9, 0.15, 0.1}, {80, 0, 0}},      // glowing red
		tintKeyframe{{0.95, 0.55, 0.1}, {60, 20, 0}},    // orange
		tintKeyframe{{0.95, 0.85, 0.15}, {40, 40, 0}},   // yellow
		tintKeyframe{{0.85, 0.95, 1.1}, {55, 100, 165}}, // glowing white (tintFramesHot's own values)
	)
	return rocketSparkColorVariants
}

func loadPlasmaChunkFrames() []*assets.RGBA {
	if plasmaChunkFramesLoaded {
		return plasmaChunkFrames
	}
	plasmaChunkFramesLoaded = true
	if base := loadChunkFrames(); len(base) > 0 {
		plasmaChunkFrames = tintFramesHot(base)
	}
	return plasmaChunkFrames
}

func loadPlasmaFireFrames() []*assets.RGBA {
	if plasmaFireFramesLoaded {
		return plasmaFireFrames
	}
	plasmaFireFramesLoaded = true
	if base := loadFireFrames(); len(base) > 0 {
		plasmaFireFrames = tintFramesHot(base)
	}
	return plasmaFireFrames
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

// teleportGlitterFrameCount/Width/Min|MaxLenMul/TexelsPerUnit:
// loadTeleportGlitterColorVariants returns procedurally-generated soft
// comet-like streaks (genTeleportGlitter) rather than any WAD sprite art —
// same reasoning as fire/smoke's own switch to procedural texture
// generation (see genFireGlow's doc comment): a procedural gradient
// guarantees no hard-edged silhouette, which a pixel-art sprite would
// expose the instant real/additive blending draws it at particle scale.
// Each of teleportGlitterColors' colours gets its own full spread of
// lengths (short blip to long trailing streak); a teleport glitter
// particle picks one colour AND is pinned to one length within it for its
// whole life (see spawnTeleportMote's frameRate: 0) rather than animating
// through either, so a burst of motes reads as a scatter of different
// colours and streak lengths rather than a uniform dot.
const (
	teleportGlitterFrameCount    = 6
	teleportGlitterWidth         = 10
	teleportGlitterMinLenMul     = 1.5
	teleportGlitterMaxLenMul     = 6.0
	teleportGlitterTexelsPerUnit = 12.0
)

// teleportGlitterColors is the disco palette each glitter mote's tail
// randomly picks from (see spawnTeleportMote) — a full spread around the
// hue wheel rather than the single fixed blue this used to be, each pushed
// toward a vivid, saturated tail so the burst reads as genuinely
// multi-coloured, not a pastel wash. Order doesn't matter (picked by
// random index, not sequentially).
var teleportGlitterColors = [][3]float64{
	{1.00, 0.15, 0.15}, // red
	{1.00, 0.55, 0.10}, // orange
	{1.00, 0.95, 0.20}, // yellow
	{0.25, 1.00, 0.30}, // green
	{0.20, 0.90, 1.00}, // cyan
	{0.15, 0.45, 1.00}, // blue — the original's own tail colour
	{0.60, 0.20, 1.00}, // purple
	{1.00, 0.25, 0.75}, // magenta/pink
}

func loadTeleportGlitterColorVariants() [][]*assets.RGBA {
	if teleportGlitterColorVariantsLoaded {
		return teleportGlitterColorVariants
	}
	teleportGlitterColorVariantsLoaded = true
	out := make([][]*assets.RGBA, len(teleportGlitterColors))
	for c, col := range teleportGlitterColors {
		frames := make([]*assets.RGBA, teleportGlitterFrameCount)
		for i := range frames {
			frac := float64(i) / float64(teleportGlitterFrameCount-1)
			lenMul := teleportGlitterMinLenMul + (teleportGlitterMaxLenMul-teleportGlitterMinLenMul)*frac
			h := int(float64(teleportGlitterWidth) * lenMul)
			frames[i] = genTeleportGlitter(teleportGlitterWidth, h, col[0], col[1], col[2])
		}
		out[c] = frames
	}
	teleportGlitterColorVariants = out
	return teleportGlitterColorVariants
}

// genTeleportGlitter procedurally paints one soft comet-like streak, w wide
// and h tall: a bright white-hot head at row 0 cooling toward (tailR,
// tailG, tailB) and fading to transparent down the tail (row h-1) —
// entirely soft-edged (see this function group's own doc comment above).
// tailR/G/B are 0..1; t eases toward the tail colour a little slower than
// the raw row fraction v, so the head still reads as white-hot for a beat
// before visibly taking on this mote's own colour, the same shape the
// original fixed-blue version had, just generalised to any tail colour
// instead of one hardcoded blue (see loadTeleportGlitterColorVariants for
// the disco palette this now serves). OffsetY: 0 anchors the BRIGHT HEAD at
// the particle's own Z (see worldgeo.Sprite's topZ/botZ math), so the tail
// trails below/behind it as it rises — standing in for a teleporter pad's
// rising light motes (engine/teleportambience.go).
func genTeleportGlitter(w, h int, tailR, tailG, tailB float64) *assets.RGBA {
	pix := make([]byte, w*h*4)
	cx := float64(w-1) / 2
	rx := cx * 0.95
	for y := 0; y < h; y++ {
		v := 0.0
		if h > 1 {
			v = float64(y) / float64(h-1) // 0 at the head, 1 at the tail's tip
		}
		rowRx := rx * (1 - 0.5*v)      // the streak narrows toward its tail
		rowAlpha := math.Pow(1-v, 1.6) // and fades toward its tail
		t := math.Pow(v, 0.8)
		for x := 0; x < w; x++ {
			d := math.Abs(float64(x)-cx) / math.Max(rowRx, 1e-3)
			o := (y*w + x) * 4
			if d >= 1 {
				pix[o+3] = 0
				continue
			}
			edge := math.Pow(1-d, 2.0)
			a := edge * rowAlpha
			pix[o+0] = clampByte(float32(255 * (1 + (tailR-1)*t))) // white head -> tailR/G/B at the tip
			pix[o+1] = clampByte(float32(255 * (1 + (tailG-1)*t)))
			pix[o+2] = clampByte(float32(255 * (1 + (tailB-1)*t)))
			pix[o+3] = clampByte(float32(a * 255))
		}
	}
	return &assets.RGBA{Width: w, Height: h, Pix: pix, TexelsPerUnit: teleportGlitterTexelsPerUnit, OffsetX: w / 2, OffsetY: 0}
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
