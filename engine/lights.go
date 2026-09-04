package engine

import (
	"sort"

	"twopointfive/render"
)

// Dynamic lights for the enhanced (GPU deferred) and hardware paths — see
// game.LitMode / game.HardwareMode. Vanilla Doom has no dynamic lights at
// all; these are the emitters the engine already simulates: the player's
// weapon muzzle flash, every in-flight rocket / plasma / BFG shot (each
// glowing in its own colour, flaring on impact), every MONSTER projectile
// in flight (imp / caco / baron-knight fireballs), and the muzzle flash of
// a hitscan monster (former human / shotgun guy / chaingunner) while it
// fires. collectLights assembles them per frame.

// muzzleFlashLight is where and how bright the weapon flash lights the
// scene: a warm point just in front of the eye, roughly a room's width of
// reach.
func (g *Game) muzzleFlashLight() (render.Light, bool) {
	if _, _, hasFlash := g.currentSprite(); !hasFlash {
		return render.Light{}, false
	}
	dx, dy, _ := aimDirection(g.Camera)
	return render.Light{
		X: float32(g.Camera.X + dx*48), Y: float32(g.Camera.Y + dy*48), Z: float32(g.Camera.Z),
		Radius: 340, R: 1.0, G: 0.92, B: 0.75, Intensity: 1.5,
	}, true
}

// projectileLight is the glow a single in-flight (or exploding) shot casts.
// A loaded GLDEFS lump attached to the flying sprite's frame (e.g.
// `object DoomImpBall { frame BAL1A { light PLASMABALL } }`) wins for the
// FLYING phase; otherwise the colour keys off the explosion-lump prefix
// (BEXP = rocket, PLSE = plasma bolt, BFE1 = BFG). The exploding flare
// keeps its hand-tuned ramp — GLDEFS can't express "flash that fades and
// spreads over the blast".
func (g *Game) projectileLight(p *Projectile) render.Light {
	if !p.exploding && g.gldefs != nil && len(p.def.Sprite) >= 5 {
		if defs := g.gldefs.Frame(p.def.Sprite[:4], int(p.def.Sprite[4]-'A')); len(defs) > 0 {
			d := defs[0]
			rad := d.Size
			if d.SecondarySize > rad {
				rad = 0.5 * (d.Size + d.SecondarySize)
			}
			inten := float32(1.3)
			if d.Scale > 0 {
				inten = d.Scale
			}
			if rad > 1 && (d.Color[0] != 0 || d.Color[1] != 0 || d.Color[2] != 0) {
				return render.Light{
					X: float32(p.X), Y: float32(p.Y), Z: float32(p.Z),
					Radius: rad, R: d.Color[0], G: d.Color[1], B: d.Color[2], Intensity: inten,
				}
			}
		}
	}

	var r, gg, b, radius float32
	switch p.def.ExplodePrefix {
	case "BEXP":
		r, gg, b, radius = 1.0, 0.55, 0.20, 220 // rocket: warm orange
	case "PLSE":
		r, gg, b, radius = 0.45, 0.65, 1.0, 190 // plasma: cyan-blue
	case "BFE1":
		r, gg, b, radius = 0.50, 1.0, 0.45, 320 // BFG: green
	default:
		r, gg, b, radius = 1.0, 0.80, 0.50, 190
	}
	intensity := float32(1.3)

	if p.exploding {
		frac := float32(explosionProgress(p)) // 0 at impact -> 1 as it dissipates
		intensity = 3.4 * (1 - frac)
		radius *= 1.4 + 2.0*frac
		// All blasts read as the same white-hot orange core.
		r, gg, b = 1.0, 0.72, 0.38
	}
	return render.Light{
		X: float32(p.X), Y: float32(p.Y), Z: float32(p.Z),
		Radius: radius, R: r, G: gg, B: b, Intensity: intensity,
	}
}

// missileLight is the glow of a monster's flying (or just-detonated)
// projectile — an imp / caco / baron-knight fireball. Coloured per shot
// type; while it is animating its death frames (MF_MISSILE already cleared
// by explodeMissile, State not yet S_NULL) it flares white-hot and wider,
// the impact flash. pos comes interpolated so the light tracks the sprite.
func missileLight(mo *Mobj, px, py, pz float64) (render.Light, bool) {
	var r, gg, b, radius, inten float32
	switch mo.Type {
	case MT_TROOPSHOT:
		r, gg, b, radius, inten = 1.0, 0.55, 0.20, 150, 1.3 // imp: warm orange
	case MT_HEADSHOT:
		r, gg, b, radius, inten = 0.55, 0.45, 1.0, 160, 1.3 // caco: blue-violet
	case MT_BRUISERSHOT:
		r, gg, b, radius, inten = 0.35, 1.0, 0.35, 160, 1.4 // baron / knight: green
	default:
		return render.Light{}, false
	}
	if mo.Flags&MF_MISSILE == 0 { // exploding
		r, gg, b = 1.0, 0.72, 0.40
		radius *= 1.6
		inten *= 1.8
	}
	return render.Light{
		X: float32(px), Y: float32(py), Z: float32(pz + mo.Height*0.5),
		Radius: radius, R: r, G: gg, B: b, Intensity: inten,
	}, true
}

// monsterFlashLight is the muzzle flash of a hitscan monster while it is
// firing — detected by its current state being full-bright (id marks the
// POSS/SPOS/CPOS attack flash frames bright). Same warm point as the
// player's own flash, at roughly weapon height.
func monsterFlashLight(mo *Mobj, px, py, pz float64) (render.Light, bool) {
	if !mo.Fullbright {
		return render.Light{}, false
	}
	switch mo.Type {
	case MT_POSSESSED, MT_SHOTGUY, MT_CHAINGUY:
	default:
		return render.Light{}, false
	}
	return render.Light{
		X: float32(px), Y: float32(py), Z: float32(pz + mo.Height*0.55),
		Radius: 260, R: 1.0, G: 0.92, B: 0.72, Intensity: 1.6,
	}, true
}

// explosionProgress is how far p is through its explosion animation, 0..1.
func explosionProgress(p *Projectile) float64 {
	total := float64(p.def.ExplodeTics) * float64(len(p.def.ExplodeFrames))
	if total <= 0 {
		return 1
	}
	f := p.explodeElapsed * ticsPerSecond / total
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}

// Light-budget tuning for the enhanced pipeline.
const (
	// activeLightBudget caps how many lights are handed to the shader per
	// frame. The UBO array is render.MaxLights (64), but the shader's
	// per-pixel loop cost is linear in the count and a pixel is seldom near
	// more than a handful — past this many, nearest-first, extra entries
	// cost frame time without changing the image. Kept comfortably above the
	// count a torch-lit room actually needs so the nearest-N cut rarely
	// bites (and appendMapLights ramps the ones near the cut, not snaps).
	activeLightBudget = 40
	// shadowCasterCap is the most leading (dynamic) lights that get the
	// screen-space shadow march. Static map emitters never do — marching
	// every torch every pixel every frame is what wrecked the frame rate.
	shadowCasterCap = 4
)

// scoredLight is one candidate map emitter plus its squared distance to the
// camera, for the nearest-first sort in appendMapLights.
type scoredLight struct {
	l  render.Light
	d2 float32
}

// collectLights gathers this frame's lights for the enhanced pipeline. The
// gameplay-critical dynamic emitters go in first — the muzzle flash, then
// each in-flight/exploding shot — and dynamicN reports how many, so the
// backend knows which leading lights may cast a screen-space shadow. The
// map's static emitters (torches, lamps, animated-light sectors —
// maplights.go) fill the rest of activeLightBudget, nearest-first, culled
// to those whose sphere reaches the camera. The returned slice is a reused
// buffer — valid only until the next call.
func (g *Game) collectLights() (lights []render.Light, dynamicN int) {
	cx, cy, cz := float32(g.Camera.X), float32(g.Camera.Y), float32(g.Camera.Z)

	out := g.lightScratch[:0]
	if l, ok := g.muzzleFlashLight(); ok {
		out = append(out, l)
	}
	for _, p := range g.projectiles {
		if l := g.projectileLight(p); l.Intensity > 0 && l.Radius > 0 {
			out = append(out, l)
		}
	}
	// Monster fireballs and hitscan-monster muzzle flashes — the enemy-side
	// equivalent of the two loops above. One pass over the mobj list; most
	// entries fail the cheap type / flag check immediately.
	frac := g.renderLerp()
	for _, mo := range g.mobjs {
		if mo.State == S_NULL {
			continue
		}
		ix, iy, iz, _ := mo.renderState(frac)
		switch mo.Type {
		case MT_TROOPSHOT, MT_HEADSHOT, MT_BRUISERSHOT:
			if l, ok := missileLight(mo, ix, iy, iz); ok {
				out = append(out, l)
			}
		case MT_POSSESSED, MT_SHOTGUY, MT_CHAINGUY:
			if l, ok := monsterFlashLight(mo, ix, iy, iz); ok {
				out = append(out, l)
			}
		}
	}
	// Dynamic lights alone overflowing the budget is pathological (BFG
	// spam) — if it happens, keep the nearest.
	if len(out) > activeLightBudget {
		sort.Slice(out, func(i, j int) bool {
			return lightDist2(out[i], cx, cy, cz) < lightDist2(out[j], cx, cy, cz)
		})
		out = out[:activeLightBudget]
	}

	dynamicN = len(out)
	if dynamicN > shadowCasterCap {
		dynamicN = shadowCasterCap
	}

	out = g.appendMapLights(out)
	g.lightScratch = out
	return out, dynamicN
}

func lightDist2(l render.Light, cx, cy, cz float32) float32 {
	dx, dy, dz := l.X-cx, l.Y-cy, l.Z-cz
	return dx*dx + dy*dy + dz*dz
}

// buildFrame packages the raster G-buffer and this frame's lights into the
// render.Frame the enhanced backend consumes. Call after the raster
// renderer has drawn the frame (Render + the sprite/overlay passes) and
// only when g.LitMode is set.
func (g *Game) buildFrame() *render.Frame {
	d := g.Raster.Deferred()
	lights, dynamicN := g.collectLights()
	var shadow float32
	shadowLights := 0
	if g.shadows {
		shadow = 1
		shadowLights = dynamicN
	}
	// flashlightSpot returns the zero value (SpotIntensity 0, read by
	// light.frag as "off") when the flashlight isn't on.
	spot, _ := g.flashlightSpot()
	g.frame = render.Frame{
		W: d.W, H: d.H,
		Albedo: d.Albedo, Normal: d.Normal, LightParam: d.LightParam, Overlay: d.Overlay,
		Depth:          d.Depth,
		CamX:           float32(g.Camera.X),
		CamY:           float32(g.Camera.Y),
		CamZ:           float32(g.Camera.Z),
		SinA:           float32(d.SinA),
		CosA:           float32(d.CosA),
		Focal:          float32(d.Focal),
		HorizonY:       float32(d.HorizonY),
		OverlayY:       d.OverlayY,
		OverlayH:       d.OverlayH,
		ExtraLight:     0,
		LightScale:     g.lightScale,
		AmbientLight:   g.ambientLight,
		Exposure:       g.exposure,
		FogR:           g.fogR,
		FogG:           g.fogG,
		FogB:           g.fogB,
		FogDensity:     g.fogDensity,
		AnimTime:       float32(g.renderClock),
		ShadowStrength: shadow,
		ShadowLights:   shadowLights,
		Lights:         lights,
		SpotX:          spot.X,
		SpotY:          spot.Y,
		SpotZ:          spot.Z,
		SpotDX:         spot.DX,
		SpotDY:         spot.DY,
		SpotDZ:         spot.DZ,
		SpotR:          spot.R,
		SpotG:          spot.G,
		SpotB:          spot.B,
		SpotIntensity:  spot.Intensity,
		SpotRadius:     spot.Radius,
		SpotCosOuter:   spot.CosOuter,
		SpotCosInner:   spot.CosInner,
	}
	return &g.frame
}
