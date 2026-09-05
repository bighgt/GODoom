package engine

import (
	"math"

	"twopointfive/raster"
)

// Head-mounted flashlight: F toggles it. In the enhanced/hardware render
// tiers (real per-pixel dynamic lights) it's a forward cone with a
// hotspot-to-spill falloff, a subtle battery flicker, and a slight
// handheld sway — see flashlightSpot, read by engine/lights.go's
// buildFrame and game.go's hardware render block. Vanilla has no
// per-pixel dynamic lights at all, so there it's a flat raster.ExtraLight
// bump instead (id's "extralight" — the same lever PrBoom's light-amp
// goggles use, a plain uniform brightening with no beam shape).
//
// Not implemented: battery drain / limited duration. It's a persistent
// on/off toggle, same as any other HUD/render toggle in this engine (see
// F1's hudKeyDown in handleMovement).
const (
	// flashlightRadius is the beam's hard cutoff — well past the muzzle
	// flash's 340u point light, since a headlamp is meant to reach across
	// a room. A monster gets no dynamicDiffuse contribution at all past
	// this distance (the falloff is a hard (1-(d/R)^2)^2, exactly 0 at R),
	// so it's the actual answer to "how far away does a monster read as
	// lit and detailed rather than just flat ambient" — bumped up from the
	// original 900 so that reveal happens well before point-blank range.
	flashlightRadius = 1400.0
	// flashlightConeOuterDeg/InnerDeg are the cone half-angles: fully lit
	// inside Inner, fading to nothing at Outer.
	flashlightConeOuterDeg = 32.0
	flashlightConeInnerDeg = 11.0
	// flashlightBaseIntensity is the beam's brightness before flicker,
	// tuned against the same shaders' other lights (muzzleFlashLight uses
	// 1.5 at a much shorter radius).
	flashlightBaseIntensity = 1.7
	// flashlightForward/Up offset the beam's origin from the eye — forward
	// along the aim so it doesn't sit exactly on the muzzle flash's own
	// point, up a little so it reads as head-mounted rather than
	// gun-mounted.
	flashlightForward = 10.0
	flashlightUp      = 6.0
	// flashlightExtraLight is the vanilla-path raster.ExtraLight bump.
	// Flat, not flickered: ExtraLight moves brightness a whole sector-light
	// row at a time, so animating it would band rather than flicker.
	flashlightExtraLight = 2
	// flashlightSeed distinguishes the beam's flicker from any map light's
	// (flickerWobble, maplights.go) sharing the same levelTime clock.
	flashlightSeed = 9001
)

// flashlightSystem toggles the flashlight on F (edge-triggered, like F1's
// HUD cycle in handleMovement) and drives the vanilla-path brightness bump
// every tic. A no-op when config disabled it (flashlightEnabled false) or
// there's no window (headless tests).
type flashlightSystem struct{}

func (flashlightSystem) Name() string { return "flashlight" }

func (flashlightSystem) Update(g *Game, dt float32) {
	if !g.flashlightEnabled {
		return
	}
	if g.Window != nil {
		down := g.Window.FlashlightPressed()
		if down && !g.flashlightKeyDown {
			g.flashlightOn = !g.flashlightOn
		}
		g.flashlightKeyDown = down
	}
	// The GPU-lit tiers get a real cone (flashlightSpot) instead — don't
	// also brighten the whole screen there.
	if !g.flashlightOn || g.LitMode || g.HardwareMode {
		raster.ExtraLight = 0
	} else {
		raster.ExtraLight = flashlightExtraLight
	}
}

// flashlightSpotLight is the beam's current shape/brightness — computed
// fresh per render since position/direction track the live camera and
// intensity carries this tic's flicker.
type flashlightSpotLight struct {
	X, Y, Z            float32
	DX, DY, DZ         float32
	R, G, B, Intensity float32
	Radius             float32
	CosOuter, CosInner float32
}

// flashlightSpot reports the beam for the enhanced/hardware render tiers.
// ok is false when it's off (or disabled) — callers then leave every
// Spot* field on render.Frame / worldgeo.Camera at its zero value, which
// both shaders read as "off" (SpotIntensity <= 0).
func (g *Game) flashlightSpot() (flashlightSpotLight, bool) {
	if !g.flashlightEnabled || !g.flashlightOn {
		return flashlightSpotLight{}, false
	}

	// Sway: two independent low-frequency sines on the free-running render
	// clock, applied to a copy of the camera before it feeds aimDirection —
	// a headlamp isn't bolted rigidly to the skull. Small enough (peak
	// under a degree) to read as "handheld", not wobbly.
	t := g.renderClock
	sway := raster.Camera{
		Angle: g.Camera.Angle + 0.010*math.Sin(t*0.9) + 0.006*math.Sin(t*2.3+1.1),
		Pitch: g.Camera.Pitch + 0.008*math.Sin(t*1.3+0.6) + 0.005*math.Sin(t*3.1+2.0),
	}
	dx, dy, dz := aimDirection(sway)

	// Position offset uses the unswayed aim so the origin doesn't itself
	// wander — only the direction the beam points does.
	fx, fy, _ := aimDirection(g.Camera)
	x := g.Camera.X + fx*flashlightForward
	y := g.Camera.Y + fy*flashlightForward
	z := g.Camera.Z + flashlightUp

	// Flicker: the same hash-noise the torch/lamp decorations use
	// (flickerWobble's 0.80..1.06 swing), compressed to a subtler
	// multiplier — a hint of an aging battery, not a strobe.
	w := flickerWobble(flashlightSeed, g.levelTime)
	intensity := flashlightBaseIntensity * (0.93 + 0.11*w)

	return flashlightSpotLight{
		X: float32(x), Y: float32(y), Z: float32(z),
		DX: float32(dx), DY: float32(dy), DZ: float32(dz),
		R: 0.90, G: 0.95, B: 1.0,
		Intensity: intensity,
		Radius:    flashlightRadius,
		CosOuter:  float32(math.Cos(flashlightConeOuterDeg * math.Pi / 180)),
		CosInner:  float32(math.Cos(flashlightConeInnerDeg * math.Pi / 180)),
	}, true
}
