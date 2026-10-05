package engine

import (
	"math"
	"math/rand"
	"sync"

	"twopointfive/assets"
	"twopointfive/render/worldgeo"
)

// Rendered monster fireballs: the imp's (config impFireballs) and the
// cacodemon's (config cacoFireballs), both default on.
//
// The monster still throws its missile exactly as id's attack code does
// (MT_TROOPSHOT from A_TroopAttack, MT_HEADSHOT from A_HeadAttack) — same
// launch, speed, direct-hit damage and AI timing — but it isn't drawn as
// the BAL1 / BAL2 sprite or the model pack's mesh. Instead:
//
//   - in flight, a procedurally generated fire core (animated turbulence
//     baked into the kind's frames at first use — no art pack needed) and
//     a soft glow halo, both additive, plus a trail of embers shed behind
//     it; its dynamic light is lights.go's missileLight;
//   - on impact, a fire burst (flash, fire blobs, bouncing sparks, a puff
//     of smoke when the smoke art is present) and SPLASH DAMAGE around the
//     impact point — so a near miss still burns.
//
// The imp's is orange fire; the cacodemon's is a little larger, a red-hot
// core wrapped in blue flames (cacoFireRamp), with a bigger splash. The
// death frames still run (they time missileLight's impact flash) but
// aren't drawn either.

// fireballKind is one monster's rendered fireball: its art, how big it's
// drawn, and its impact splash.
type fireballKind struct {
	art          func() *fireballArt
	splashRadius float64  // map units from the impact to a body's edge
	splashDamage int      // at the centre, falling linearly to 0 at the radius
	kin          mobjType // the thrower's type — spared by the splash, like a direct hit's infighting rule
}

var (
	impFireball = fireballKind{
		art:          impFireballArt,
		splashRadius: 64, splashDamage: 16,
		kin: MT_TROOP,
	}
	cacoFireball = fireballKind{
		art:          cacoFireballArt,
		splashRadius: 72, splashDamage: 20,
		kin: MT_HEAD,
	}
)

const (
	impCoreSize     = 18.0 // world units across the imp's core at SizeMul 1
	impHaloSize     = 56.0 // ...and its glow halo
	cacoSizeMul     = 1.25 // the cacodemon's ball is this much bigger
	impTrailEvery   = 0.012
	impTrailLifeMin = 0.16
	impTrailLifeMax = 0.32
	impCoreFrames   = 8
	impFrameRate    = 18.0
)

// fxRand drives the purely cosmetic randomness here (ember scatter, frame
// phases). Kept off pRandom — the gameplay generator — since emitters run
// per render frame, and pulling from pRandom at frame rate would shift
// every later AI roll with the frame rate.
var fxRand = rand.New(rand.NewSource(0x1f1e))

func fxRange(lo, hi float64) float64 { return lo + fxRand.Float64()*(hi-lo) }

// renderedFireball is mo's rendered-fireball kind, or nil when mo is
// drawn the vanilla way (not a fireball, or its config switch is off).
func (g *Game) renderedFireball(mo *Mobj) *fireballKind {
	switch {
	case mo.Type == MT_TROOPSHOT && g.impFireballs:
		return &impFireball
	case mo.Type == MT_HEADSHOT && g.cacoFireballs:
		return &cacoFireball
	}
	return nil
}

// appendFireballSprites draws an in-flight fireball's core and halo at its
// interpolated position into the additive billboard list.
func (g *Game) appendFireballSprites(mo *Mobj, fb *fireballKind, x, y, z float64) {
	if mo.Flags&MF_MISSILE == 0 {
		return // exploding: the burst particles carry the impact
	}
	art := fb.art()
	cz := float32(z + mo.Height/2)
	t := g.renderClock
	idx := int(t*impFrameRate+float64(mo.fxPhase)) % len(art.core)
	pulse := 1 + 0.08*math.Sin(t*23+float64(mo.fxPhase))
	g.hwAddSprites = append(g.hwAddSprites,
		worldgeo.Sprite{X: float32(x), Y: float32(y), Z: cz, SizeMul: float32(pulse), Tex: art.halo, Light: 1, FullBright: true},
		worldgeo.Sprite{X: float32(x), Y: float32(y), Z: cz, SizeMul: float32(pulse), Tex: art.core[idx], Light: 1, FullBright: true},
	)
}

// updateMobjFire is the per-frame driver for the rendered fire on map
// objects: the fireballs' ember trails and the lost souls' flames
// (lostsoulfire.go) — one pass over g.mobjs for all. Each emitter sheds
// at a fixed rate whatever the frame rate (drainSpawnTimer), from where its
// mobj is drawn this frame. Embers come from particlePool: they're
// unaliased and short-lived, the same case as the rocket trail.
func (g *Game) updateMobjFire(dt float64) {
	if !g.impFireballs && !g.cacoFireballs && !g.lostSoulFire {
		return
	}
	frac := g.renderLerp()
	for _, mo := range g.mobjs {
		if fb := g.renderedFireball(mo); fb != nil {
			if mo.Flags&MF_MISSILE != 0 && !mo.removed {
				g.fireballTrail(mo, fb, frac, dt)
			}
		} else if mo.Type == MT_SKULL && g.lostSoulBurning(mo) {
			g.lostSoulTrail(mo, frac, dt)
		}
	}
}

// fireballTrail sheds this frame's embers behind in-flight ball mo.
func (g *Game) fireballTrail(mo *Mobj, fb *fireballKind, frac, dt float64) {
	core := fb.art().core
	x, y, z, _ := mo.renderState(frac)
	z += mo.Height / 2
	// Backward along the flight line, per second (momentum is per tic).
	bx, by, bz := -mo.MomX*ticsPerSecond*0.15, -mo.MomY*ticsPerSecond*0.15, -mo.MomZ*ticsPerSecond*0.15
	drainSpawnTimer(&mo.fxTimer, dt, impTrailEvery, func() {
		g.particles = append(g.particles, newPooledParticle(Particle{
			X: x + fxRange(-2, 2), Y: y + fxRange(-2, 2), Z: z + fxRange(-2, 2),
			DX: bx + fxRange(-18, 18), DY: by + fxRange(-18, 18), DZ: bz + fxRange(-10, 22),
			life:       fxRange(impTrailLifeMin, impTrailLifeMax),
			sizeMul:    fxRange(0.3, 0.55),
			framePhase: fxRand.Intn(impCoreFrames),
			frameRate:  impFrameRate,
			gravity:    -40, // embers rise
			light:      1,
			fullBright: true,
			frames:     core,
		}))
	})
}

// fireballImpact is the rendered fireball's detonation: the fire burst
// and the splash damage. Called from explodeMissile.
func (g *Game) fireballImpact(mo *Mobj, fb *fireballKind) {
	g.fireballSplash(mo, fb)

	art := fb.art()
	core, halo := art.core, []*assets.RGBA{art.halo}
	x, y, z := mo.X, mo.Y, mo.Z+mo.Height/2
	add := func(p Particle) { g.particles = append(g.particles, newPooledParticle(p)) }
	sphere := func(lo, hi float64) (float64, float64, float64) {
		for {
			dx, dy, dz := fxRange(-1, 1), fxRange(-1, 1), fxRange(-1, 1)
			if l := math.Sqrt(dx*dx + dy*dy + dz*dz); l > 0.1 && l <= 1 {
				s := fxRange(lo, hi) / l
				return dx * s, dy * s, dz * s
			}
		}
	}
	// The flash: a few big halo blooms that collapse almost at once.
	for i := 0; i < 3; i++ {
		add(Particle{X: x, Y: y, Z: z, life: fxRange(0.12, 0.2), sizeMul: fxRange(1.3, 1.8),
			frameRate: 1, light: 1, fullBright: true, frames: halo})
	}
	// Fire blobs boiling outward and up.
	for i := 0; i < 16; i++ {
		dx, dy, dz := sphere(30, 110)
		add(Particle{X: x, Y: y, Z: z, DX: dx, DY: dy, DZ: dz + 25,
			life: fxRange(0.25, 0.5), sizeMul: fxRange(0.55, 1.0),
			framePhase: fxRand.Intn(impCoreFrames), frameRate: impFrameRate,
			gravity: -60, light: 1, fullBright: true, frames: core})
	}
	// Sparks that fall and skip once off the floor.
	for i := 0; i < 26; i++ {
		dx, dy, dz := sphere(120, 280)
		add(Particle{X: x, Y: y, Z: z, DX: dx, DY: dy, DZ: dz + 60,
			life: fxRange(0.35, 0.7), sizeMul: fxRange(0.09, 0.16),
			framePhase: fxRand.Intn(impCoreFrames), frameRate: impFrameRate,
			gravity: particleGravity, light: 1, fullBright: true,
			bounceFloor: true, bounces: 1, frames: core})
	}
	// A short-lived puff of smoke, when the smoke art is present.
	if smoke := loadSmokeFrames(); len(smoke) > 0 {
		for i := 0; i < 4; i++ {
			add(Particle{X: x + fxRange(-4, 4), Y: y + fxRange(-4, 4), Z: z + fxRange(-2, 6),
				DX: fxRange(-12, 12), DY: fxRange(-12, 12), DZ: fxRange(15, 35),
				life: fxRange(0.8, 1.3), sizeMul: fxRange(0.7, 1.1),
				framePhase: fxRand.Intn(256), frameRate: smokeFrameRate,
				light: 0.55, translucent: true, alpha0: 0.35, hold: 0.25, clearBrighten: 1.3,
				frames: smoke})
		}
	}
}

// fireballSplash damages every shootable thing within fb.splashRadius of
// the burst — id's P_RadiusAttack falloff (distance to the body's edge,
// the larger axis gap, no damage through walls) — except the thing the
// ball hit squarely (it already took the direct hit), the monster that
// threw it, and others of its kind (a splash doesn't start imp-on-imp or
// caco-on-caco infighting, the same kin rule a direct hit follows).
func (g *Game) fireballSplash(mo *Mobj, fb *fireballKind) {
	hit := func(t *Mobj) {
		if t == nil || t.removed || t.Flags&MF_SHOOTABLE == 0 || t.Health <= 0 {
			return
		}
		if t == mo.directHit || t == mo.Target || t.Type == fb.kin {
			return
		}
		d := math.Max(math.Abs(t.X-mo.X), math.Abs(t.Y-mo.Y)) - t.Radius
		if d < 0 {
			d = 0
		}
		if d >= fb.splashRadius {
			return
		}
		// Vertically too: a ball bursting on a ledge doesn't scorch the
		// floor far below.
		if mo.Z > t.Z+t.Height+fb.splashRadius || mo.Z+mo.Height < t.Z-fb.splashRadius {
			return
		}
		if !g.pCheckSight(t, mo) {
			return
		}
		if dmg := int(float64(fb.splashDamage) * (1 - d/fb.splashRadius)); dmg > 0 {
			g.pDamageMobj(t, mo, mo.Target, dmg)
		}
	}
	hit(g.playerMobj) // the player isn't in g.mobjs
	for _, t := range g.mobjs {
		hit(t)
	}
}

// --- procedural fire art ------------------------------------------------

// fireballArt is one fireball kind's frames: the animated core and the
// glow halo.
type fireballArt struct {
	core []*assets.RGBA
	halo *assets.RGBA
}

const fireballTexPx = 64

var (
	impArtOnce, cacoArtOnce sync.Once
	impArt, cacoArt         *fireballArt
)

func impFireballArt() *fireballArt {
	impArtOnce.Do(func() { impArt = buildFireballArt(fireRamp, impHaloColor, impCoreSize, impHaloSize) })
	return impArt
}

func cacoFireballArt() *fireballArt {
	cacoArtOnce.Do(func() {
		cacoArt = buildFireballArt(cacoFireRamp, cacoHaloColor, impCoreSize*cacoSizeMul, impHaloSize*cacoSizeMul)
	})
	return cacoArt
}

// fireballCoreFrames is the imp's core art — also the lost souls' flames
// (lostsoulfire.go).
func fireballCoreFrames() []*assets.RGBA { return impFireballArt().core }

// buildFireballArt renders a fireball's frames: a round core whose rim is
// torn up by drifting value-noise turbulence (each frame samples the noise
// further along, so the flames boil), coloured by ramp from heat 0..1 —
// the hot centre at 1, the ragged flame edge toward 0. Additive, so black
// is see-through; texels that would add nothing are cut out (alpha 0) for
// the sprite pass's alpha test. Plus one soft round halo for the glow
// around it, haloColor(k) at falloff k (1 at the centre, 0 at the edge).
// coreSize / haloSize are their world sizes at SizeMul 1.
func buildFireballArt(ramp func(float64) (byte, byte, byte), haloColor func(float64) (byte, byte, byte), coreSize, haloSize float64) *fireballArt {
	n := fireballTexPx
	centred := func(img *assets.RGBA, size float64) {
		img.OffsetX, img.OffsetY = n/2, n/2 // billboard centred on (X, Y, Z)
		img.TexelsPerUnit = float64(n) / size
	}
	art := &fireballArt{}
	for f := 0; f < impCoreFrames; f++ {
		img := &assets.RGBA{Width: n, Height: n, Pix: make([]byte, n*n*4)}
		phase := float64(f) / impCoreFrames * 2 * math.Pi
		for py := 0; py < n; py++ {
			for px := 0; px < n; px++ {
				u := (float64(px)+0.5)/float64(n)*2 - 1
				v := (float64(py)+0.5)/float64(n)*2 - 1
				r := math.Hypot(u, v)
				ang := math.Atan2(v, u)
				// Turbulence on a loop: the sample point circles through the
				// noise field over the frames, so frame 7 flows into frame 0.
				nz := fbm(u*2.6+math.Cos(phase)*1.3, v*2.6+math.Sin(phase)*1.3, 3)
				rim := fbm(math.Cos(ang)*1.7+math.Cos(phase)*0.9, math.Sin(ang)*1.7+math.Sin(phase)*0.9, 2)
				rr := r + (nz-0.5)*0.35 + (rim-0.5)*0.45*r
				heat := 1 - rr/0.82
				if heat <= 0 {
					continue
				}
				heat = math.Min(1, math.Pow(heat, 0.9)*(0.8+0.4*nz))
				cr, cg, cb := ramp(heat)
				i := (py*n + px) * 4
				img.Pix[i], img.Pix[i+1], img.Pix[i+2] = cr, cg, cb
				if int(cr)+int(cg)+int(cb) > 12 {
					img.Pix[i+3] = 255
				}
			}
		}
		centred(img, coreSize)
		art.core = append(art.core, img)
	}
	halo := &assets.RGBA{Width: n, Height: n, Pix: make([]byte, n*n*4)}
	for py := 0; py < n; py++ {
		for px := 0; px < n; px++ {
			u := (float64(px)+0.5)/float64(n)*2 - 1
			v := (float64(py)+0.5)/float64(n)*2 - 1
			k := 1 - math.Hypot(u, v)
			if k <= 0 {
				continue
			}
			cr, cg, cb := haloColor(k)
			i := (py*n + px) * 4
			halo.Pix[i], halo.Pix[i+1], halo.Pix[i+2] = cr, cg, cb
			if int(cr)+int(cg)+int(cb) > 3 {
				halo.Pix[i+3] = 255
			}
		}
	}
	centred(halo, haloSize)
	art.halo = halo
	return art
}

// rampStop is one colour stop of a heat ramp.
type rampStop struct{ t, r, g, b float64 }

// rampColor linearly interpolates stops (ascending t, 0..1) at h.
func rampColor(stops []rampStop, h float64) (byte, byte, byte) {
	for i := 1; i < len(stops); i++ {
		if h <= stops[i].t {
			a, b := stops[i-1], stops[i]
			k := (h - a.t) / (b.t - a.t)
			return byte(a.r + (b.r-a.r)*k), byte(a.g + (b.g-a.g)*k), byte(a.b + (b.b-a.b)*k)
		}
	}
	s := stops[len(stops)-1]
	return byte(s.r), byte(s.g), byte(s.b)
}

// fireRamp is the imp's black-body ramp: black, deep red, orange, yellow,
// near-white.
var fireStops = []rampStop{
	{0.00, 0, 0, 0},
	{0.18, 150, 25, 5},
	{0.42, 255, 105, 20},
	{0.70, 255, 200, 70},
	{1.00, 255, 250, 215},
}

func fireRamp(h float64) (byte, byte, byte) { return rampColor(fireStops, h) }

// cacoFireRamp is the cacodemon's: the ragged outer flames burn blue (deep
// blue at the very edge, bright blue a little in), shading through violet
// into a red core that runs hot toward a pinkish-white centre.
var cacoFireStops = []rampStop{
	{0.00, 0, 0, 0},
	{0.12, 10, 25, 160},
	{0.30, 40, 110, 255},
	{0.44, 90, 90, 255},
	{0.56, 200, 30, 90},
	{0.70, 240, 35, 40},
	{0.86, 255, 90, 60},
	{1.00, 255, 200, 180},
}

func cacoFireRamp(h float64) (byte, byte, byte) { return rampColor(cacoFireStops, h) }

// impHaloColor is the imp ball's warm orange glow, soft (k²) falloff.
func impHaloColor(k float64) (byte, byte, byte) {
	k *= k
	return byte(150 * k), byte(62 * k), byte(14 * k)
}

// cacoHaloColor glows red near the ball and blue further out, matching its
// red core and blue flames.
func cacoHaloColor(k float64) (byte, byte, byte) {
	a := k * k
	r := 170*k + 15*(1-k)
	g := 20*k + 55*(1-k)
	b := 40*k + 220*(1-k)
	return byte(r * a), byte(g * a), byte(b * a)
}

// fbm is a few octaves of smooth value noise, 0..1.
func fbm(x, y float64, octaves int) float64 {
	sum, amp, norm := 0.0, 1.0, 0.0
	for o := 0; o < octaves; o++ {
		sum += amp * valueNoise(x, y)
		norm += amp
		x, y, amp = x*2.03, y*2.03, amp*0.5
	}
	return sum / norm
}

func valueNoise(x, y float64) float64 {
	x0, y0 := math.Floor(x), math.Floor(y)
	fx, fy := x-x0, y-y0
	fx, fy = fx*fx*(3-2*fx), fy*fy*(3-2*fy)
	ix, iy := int(x0), int(y0)
	a, b := noiseHash(ix, iy), noiseHash(ix+1, iy)
	c, d := noiseHash(ix, iy+1), noiseHash(ix+1, iy+1)
	return a + (b-a)*fx + (c-a)*fy + (a-b-c+d)*fx*fy
}

func noiseHash(x, y int) float64 {
	h := uint32(x)*374761393 + uint32(y)*668265263
	h = (h ^ (h >> 13)) * 1274126177
	return float64(h^(h>>16)) / float64(math.MaxUint32)
}
