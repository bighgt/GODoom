package engine

import (
	"log"
	"math"
	"slices"

	"twopointfive/render"
)

// Map light emitters for the enhanced (GPU deferred) pipeline — see
// engine/lights.go, render/vulkan/lit.go. Vanilla Doom has no light
// sources at all; the sector light level is just a fade. Here every
// light-animation sector and every flame/lamp decoration becomes a real
// render.Light layered over that ambient, so a strobe room pulses, a
// torch pools warm light on the floor around it, and (Phase 3) things
// cast shadows from them.
//
// The list is built once per level (buildStaticLights, from NewGame /
// loadNextMap after the specials and things are spawned); collectLights
// samples it every frame — the sector-driven ones re-read the live
// LightLevel so they track the animation, and flame ones get a small
// deterministic wobble.

// staticLight is one fixed emitter. Position never moves (sectors and
// decorations don't); output does.
type staticLight struct {
	x, y, z    float64
	r, g, b    float32
	baseInten  float32
	baseRadius float32
	// cullR2 is (max possible radius + margin)^2 — a cheap conservative
	// squared distance past which appendMapLights skips this light without
	// even sampling it.
	cullR2 float32
	// sector >= 0: a light-animation sector — intensity/radius scale with
	// g.Level.Sectors[sector].LightLevel each frame. sector < 0: a fixed
	// decoration light.
	sector  int
	flicker bool // flame: a small per-frame brightness wobble
	seed    int  // distinguishes one flame's wobble from the next
}

// Map-emitter range tuning. A static light is included when the camera is
// within (its radius + mapLightCullMargin); the per-pixel shader falloff
// then does the actual shaping. mapLightFadeBand is the outer slice of that
// range over which the light's intensity is ramped 0 -> full as the camera
// closes, so a torch fades in smoothly instead of snapping on the instant
// the camera crosses the cull sphere. mapLightRankFade dims the last few
// budget slots the same way, so which lights make the nearest-N cut can
// change frame to frame without a visible pop.
const (
	mapLightCullMargin = 512
	mapLightFadeBand   = 224
	mapLightRankFade   = 6
)

// setCullR2 records the pre-sample cull radius. sample() can only shrink
// the radius (sector scaling) or nudge it ~1% (flicker), so baseRadius plus
// a small factor and the appendMapLights reach margin is a safe upper bound.
func (sl *staticLight) setCullR2() {
	r := sl.baseRadius*1.03 + mapLightCullMargin
	sl.cullR2 = r * r
}

// decoLight maps a decoration mobj type to the light it emits. Radius is
// map units (hard cutoff); intensity scales the whole contribution.
type decoLightDef struct {
	r, g, b float32
	radius  float32
	inten   float32
	flicker bool
}

var decoLights = map[mobjType]decoLightDef{
	MT_BURNINGBARREL:    {1.00, 0.48, 0.15, 210, 1.45, true},
	MT_REDTORCH:         {1.00, 0.45, 0.20, 190, 1.20, true},
	MT_SHORTREDTORCH:    {1.00, 0.45, 0.20, 150, 0.95, true},
	MT_TORCHTREE:        {1.00, 0.50, 0.22, 200, 1.25, true},
	MT_GREENTORCH:       {0.45, 1.00, 0.50, 190, 1.10, true},
	MT_SHORTGREENTORCH:  {0.45, 1.00, 0.50, 150, 0.90, true},
	MT_BLUETORCH:        {0.40, 0.55, 1.00, 190, 1.10, true},
	MT_SHORTBLUETORCH:   {0.40, 0.55, 1.00, 150, 0.90, true},
	MT_TALLTECHLAMP:     {0.85, 0.90, 1.00, 240, 1.35, false},
	MT_SHORTTECHLAMP:    {0.85, 0.90, 1.00, 200, 1.15, false},
	MT_TECHPILLAR:       {0.80, 0.88, 1.00, 170, 0.80, false},
	MT_MISC_CANDLESTIK:  {1.00, 0.82, 0.52, 90, 0.45, true},
	MT_MISC_CANDELABRA:  {1.00, 0.82, 0.52, 120, 0.65, true},
	MT_MISC_HEADCANDLES: {1.00, 0.82, 0.52, 100, 0.50, true},
	MT_EVILEYE:          {0.55, 0.95, 0.45, 120, 0.60, true},
}

// buildStaticLights rebuilds g.staticLights for the current level. Call
// after spawnSpecials + spawnMapThings.
func (g *Game) buildStaticLights() {
	g.staticLights = g.staticLights[:0]

	// One light per light-animation sector, at its centroid, mid-height.
	// Skip a sector whose bright end is already dim — a faint flicker adds
	// noise, not atmosphere.
	seed := 1
	for _, t := range g.thinkers {
		lt, ok := t.(*lightThinker)
		if !ok || int(lt.maxL) < 112 {
			continue
		}
		cx, cy, okC := g.sectorCentroid(lt.sec)
		if !okC {
			continue
		}
		s := &g.Level.Sectors[lt.sec]
		z := float64(s.FloorHeight) + 0.5*float64(int(s.CeilingHeight)-int(s.FloorHeight))
		r, gg, b := float32(1.0), float32(0.96), float32(0.88)
		fire := lt.mode == lmFireFlicker
		if fire {
			r, gg, b = 1.0, 0.62, 0.28
		}
		g.staticLights = append(g.staticLights, staticLight{
			x: cx, y: cy, z: z, r: r, g: gg, b: b,
			baseInten: 1.5, baseRadius: 300,
			sector: lt.sec, flicker: fire, seed: seed,
		})
		seed++
	}

	// One light per emissive decoration. A loaded GLDEFS lump (GZDoom-family
	// PWADs) attaches lights to sprite frames as data; those take priority.
	// Only when GLDEFS says nothing about this mobj's current frame do we
	// fall back to the built-in decoLights table.
	for _, mo := range g.mobjs {
		if gl := g.gldefStaticLights(mo, seed); len(gl) > 0 {
			g.staticLights = append(g.staticLights, gl...)
			seed += len(gl)
			continue
		}
		d, ok := decoLights[mo.Type]
		if !ok {
			continue
		}
		g.staticLights = append(g.staticLights, staticLight{
			x: mo.X, y: mo.Y, z: mo.Z + mo.Height*0.72,
			r: d.r, g: d.g, b: d.b,
			baseInten: d.inten, baseRadius: d.radius,
			sector: -1, flicker: d.flicker, seed: seed,
		})
		seed++
	}

	// Liquid glow: lava lights the room orange, nukage/slime casts a sickly
	// green. One or more emitters per liquid sector, sized to the sector
	// (liquidlights.go).
	g.appendLiquidLights(seed)

	for i := range g.staticLights {
		g.staticLights[i].setCullR2()
	}
	log.Printf("engine: %d static map lights (torches / lamps / animated-light sectors / liquid glow)", len(g.staticLights))
}

// sectorCentroid averages the distinct vertex positions of every linedef
// facing a sector — a cheap "somewhere inside" point good enough to hang a
// light on. Reports false for a sector with no usable lines.
func (g *Game) sectorCentroid(sec int) (x, y float64, ok bool) {
	g.ensureSectorIndex()
	if sec < 0 || sec >= len(g.sectorLines) {
		return 0, 0, false
	}
	var sx, sy float64
	var n int
	for _, li := range g.sectorLines[sec] {
		ld := &g.Level.Linedefs[li]
		for _, vi := range [2]uint16{ld.StartVertex, ld.EndVertex} {
			if int(vi) >= len(g.Level.Vertexes) {
				continue
			}
			v := g.Level.Vertexes[vi]
			sx += float64(v.X)
			sy += float64(v.Y)
			n++
		}
	}
	if n == 0 {
		return 0, 0, false
	}
	return sx / float64(n), sy / float64(n), true
}

// sample turns a staticLight into this frame's render.Light: sector-driven
// ones scale with the live LightLevel, flame ones wobble. Reports false
// when the emitter has gone effectively dark.
func (sl *staticLight) sample(g *Game) (render.Light, bool) {
	inten, radius := sl.baseInten, sl.baseRadius
	if sl.sector >= 0 {
		lvl := float64(g.Level.Sectors[sl.sector].LightLevel)
		if lvl < 1 {
			return render.Light{}, false
		}
		frac := lvl / 255
		if frac > 1 {
			frac = 1
		}
		inten *= float32(0.30 + 0.95*frac)
		radius *= float32(0.55 + 0.60*frac)
	}
	if sl.flicker {
		w := flickerWobble(sl.seed, g.levelTime)
		inten *= w
		radius *= 0.95 + 0.05*w
	}
	if inten <= 0.02 || radius <= 1 {
		return render.Light{}, false
	}
	return render.Light{
		X: float32(sl.x), Y: float32(sl.y), Z: float32(sl.z),
		Radius: radius, R: sl.r, G: sl.g, B: sl.b, Intensity: inten,
	}, true
}

func clamp01f(v float32) float32 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// flickerWobble is a cheap deterministic 0.80..1.06 multiplier from a
// per-light seed and the level tic — enough to make a flame restless
// without a PRNG draw or per-light state.
func flickerWobble(seed, tic int) float32 {
	h := uint32(seed)*2654435761 + uint32(tic)*40503
	h ^= h >> 13
	h *= 2246822519
	h ^= h >> 16
	return 0.80 + 0.26*(float32(h&0xffff)/65535.0)
}

// appendMapLights adds the camera-range-relevant map emitters to out,
// nearest-first, until activeLightBudget is full (out already holds the
// dynamic lights the caller added first). Uses g.lightCands as a reused
// scratch buffer — no per-frame allocation.
func (g *Game) appendMapLights(out []render.Light) []render.Light {
	if len(g.staticLights) == 0 || len(out) >= activeLightBudget {
		return out
	}
	cx, cy, cz := float32(g.Camera.X), float32(g.Camera.Y), float32(g.Camera.Z)
	fdx, fdy := float32(math.Cos(g.Camera.Angle)), float32(math.Sin(g.Camera.Angle))

	cands := g.lightCands[:0]
	for i := range g.staticLights {
		sl := &g.staticLights[i]
		// Cheap position pre-cull before the (slightly pricier) sample —
		// skips the sector lookup + flicker hash for anything out of reach.
		pdx, pdy, pdz := float32(sl.x)-cx, float32(sl.y)-cy, float32(sl.z)-cz
		if pdx*pdx+pdy*pdy+pdz*pdz > sl.cullR2 {
			continue
		}
		// Well behind the view plane: its sphere touches no visible pixel
		// (bar a water reflection), but the shader would still loop it for
		// every pixel. Keep a fade-band of margin so a light doesn't blink
		// off the instant it passes the camera as you turn.
		if pdx*fdx+pdy*fdy < -(sl.baseRadius + mapLightFadeBand) {
			continue
		}
		l, ok := sl.sample(g)
		if !ok {
			continue
		}
		dx, dy, dz := l.X-cx, l.Y-cy, l.Z-cz
		d2 := dx*dx + dy*dy + dz*dz
		reach := l.Radius + mapLightCullMargin
		if d2 > reach*reach {
			continue // way out of range even for a distant contribution
		}
		// Smooth camera-distance ramp over the outer band: intensity 0 at
		// `reach`, full once the camera is `mapLightFadeBand` closer. Never
		// fades within the light's own lit sphere.
		fadeStart := reach - mapLightFadeBand
		if fadeStart < l.Radius {
			fadeStart = l.Radius
		}
		if d2 > fadeStart*fadeStart {
			dist := float32(math.Sqrt(float64(d2)))
			l.Intensity *= clamp01f(1 - (dist-fadeStart)/(reach-fadeStart))
			if l.Intensity <= 0.001 {
				continue
			}
		}
		cands = append(cands, scoredLight{l, d2})
	}
	g.lightCands = cands
	slices.SortFunc(cands, func(a, b scoredLight) int {
		switch {
		case a.d2 < b.d2:
			return -1
		case a.d2 > b.d2:
			return 1
		default:
			return 0
		}
	})

	// When more emitters are in range than budget slots, dim the ones
	// ranked closest to the cut so the nearest-N boundary can shift between
	// frames without a light blinking on or off.
	slotsLeft := activeLightBudget - len(out)
	softenRank := len(cands) > slotsLeft
	for idx := range cands {
		if len(out) >= activeLightBudget {
			break
		}
		l := cands[idx].l
		if softenRank {
			if rem := slotsLeft - idx; rem <= mapLightRankFade {
				l.Intensity *= clamp01f(float32(rem) / float32(mapLightRankFade+1))
			}
		}
		out = append(out, l)
	}
	return out
}
