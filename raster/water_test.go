package raster

import (
	"math"
	"testing"

	"twopointfive/assets"
)

func TestClassifyFloorLiquid(t *testing.T) {
	for name, want := range map[string]uint8{
		"FWATER1":  liquidWater,
		"FWATER4":  liquidWater,
		"LAVA1":    liquidLava,
		"FIRELAVA": liquidLava,
		"NUKAGE1":  liquidNukage,
		"SLIME05":  liquidNukage,
		"FLOOR0_1": liquidNone,
		"FWAT":     liquidNone, // too short to be a family name
		"":         liquidNone,
	} {
		if got := classifyFloorLiquid(name); got != want {
			t.Errorf("classifyFloorLiquid(%q) = %d, want %d", name, got, want)
		}
	}
}

// Lava must brighten a pixel (it is self-lit) and, in G-buffer mode, stamp
// the emissive material key so the enhanced lighting pass leaves it alone.
func TestShadeLiquidLava(t *testing.T) {
	w, h := 4, 4
	r := &Renderer{Width: w, Height: h, Pix: make([]byte, w*h*4), Normal: make([]byte, w*h*4)}
	i := 0
	r.Pix[i], r.Pix[i+1], r.Pix[i+2] = 40, 20, 10 // dim lava texel in shadow

	r.shadeLiquid(liquidLava, 0, 0, i, true, 0, 0, 0, 0, 0, 0)

	if r.Pix[i] <= 40 {
		t.Errorf("lava should brighten R: got %d", r.Pix[i])
	}
	if r.Normal[i+3] != 255 {
		t.Errorf("G-buffer lava must set emissive material key, got %d", r.Normal[i+3])
	}
	if r.Pix[i] < r.Pix[i+2] { // warm: red dominates blue
		t.Errorf("lava not warm-biased: %d,%d,%d", r.Pix[i], r.Pix[i+1], r.Pix[i+2])
	}
}

// Nukage tints toward green without setting the emissive key.
func TestShadeLiquidNukage(t *testing.T) {
	w, h := 4, 4
	r := &Renderer{Width: w, Height: h, Pix: make([]byte, w*h*4), Normal: make([]byte, w*h*4)}
	i := 0
	r.Pix[i], r.Pix[i+1], r.Pix[i+2] = 90, 90, 90

	r.shadeLiquid(liquidNukage, 0, 0, i, true, 0, 0, 0, 0, 0, 0)

	if !(r.Pix[i+1] > r.Pix[i] && r.Pix[i+1] > r.Pix[i+2]) {
		t.Errorf("nukage should be green-dominant: %d,%d,%d", r.Pix[i], r.Pix[i+1], r.Pix[i+2])
	}
	if r.Normal[i+3] != 0 {
		t.Errorf("nukage must not set the emissive key, got %d", r.Normal[i+3])
	}
}

// blendWater with no sky present must darken and cool the pixel (its
// no-reflection fallback), never brighten it.
func TestBlendWaterNoSkyDarkens(t *testing.T) {
	w, h := 8, 8
	r := &Renderer{Width: w, Height: h, Pix: make([]byte, w*h*4)}
	r.focal = 100
	// mid-grey water pixel at column 3, row 6 (below the horizon).
	x, y := 3, 6
	i := (y*w + x) * 4
	r.Pix[i], r.Pix[i+1], r.Pix[i+2], r.Pix[i+3] = 150, 150, 150, 255

	r.blendWater(x, y, i, 0, 0, 0.5, 100)

	if r.Pix[i] >= 150 || r.Pix[i+1] >= 150 {
		t.Errorf("no-sky water should darken R,G: got %d,%d,%d", r.Pix[i], r.Pix[i+1], r.Pix[i+2])
	}
	if r.Pix[i+3] != 255 {
		t.Errorf("alpha clobbered: %d", r.Pix[i+3])
	}
}

// With a sky bound, blendWater must pull the pixel toward the mirrored sky
// colour (here a saturated blue sky over a red floor pixel).
func TestBlendWaterReflectsSky(t *testing.T) {
	w, h := 16, 16
	r := &Renderer{Width: w, Height: h, Pix: make([]byte, w*h*4)}
	r.focal = 100
	sky := &assets.RGBA{Width: 4, Height: 4, Pix: make([]byte, 4*4*4)}
	for p := 0; p < len(sky.Pix); p += 4 {
		sky.Pix[p], sky.Pix[p+1], sky.Pix[p+2], sky.Pix[p+3] = 0, 0, 255, 255
	}
	r.skyTex = sky
	r.skyScale = 1
	r.skyTx = make([]int, w)

	x, y := 8, 12
	i := (y*w + x) * 4
	r.Pix[i], r.Pix[i+1], r.Pix[i+2], r.Pix[i+3] = 255, 0, 0, 255

	r.blendWater(x, y, i, 0, 0, 0.5, 100)

	if r.Pix[i] >= 255 || r.Pix[i+2] <= 0 {
		t.Errorf("expected red down / blue up from sky reflection, got %d,%d,%d",
			r.Pix[i], r.Pix[i+1], r.Pix[i+2])
	}
}

func TestWaterWaveBounded(t *testing.T) {
	// Slope components stay small; crest stays in [0,1]; deterministic.
	for _, tc := range [][3]float64{{0, 0, 0}, {123, -456, 2.5}, {1e4, 1e4, 100}} {
		gx, gy, crest := waterWave(tc[0], tc[1], tc[2])
		if crest < 0 || crest > 1 {
			t.Errorf("crest %.3f out of [0,1]", crest)
		}
		if math.Abs(gx) > 3 || math.Abs(gy) > 3 {
			t.Errorf("slope (%.3f,%.3f) unexpectedly large", gx, gy)
		}
		gx2, _, _ := waterWave(tc[0], tc[1], tc[2])
		if gx != gx2 {
			t.Error("waterWave not deterministic")
		}
	}
	// Time actually moves the wave.
	a, _, _ := waterWave(50, 50, 0)
	b, _, _ := waterWave(50, 50, 1.0)
	if a == b {
		t.Error("waterWave did not change over time")
	}
}

// The ripple must actually perturb a reflected water pixel as animTime
// advances (different sky texel sampled), and animTime==0 is stable.
func TestBlendWaterRippleAnimates(t *testing.T) {
	w, h := 64, 64
	mk := func() *Renderer {
		r := &Renderer{Width: w, Height: h, Pix: make([]byte, w*h*4)}
		r.focal = 200
		// horizontal colour gradient sky so a small sample shift changes colour
		sky := &assets.RGBA{Width: 64, Height: 8, Pix: make([]byte, 64*8*4)}
		for yy := 0; yy < 8; yy++ {
			for xx := 0; xx < 64; xx++ {
				p := (yy*64 + xx) * 4
				sky.Pix[p], sky.Pix[p+1], sky.Pix[p+2], sky.Pix[p+3] = byte(xx*4), 40, byte(255-xx*4), 255
			}
		}
		r.skyTex, r.skyScale = sky, 1
		r.skyTx = make([]int, w)
		for xx := range r.skyTx {
			r.skyTx[xx] = xx
		}
		return r
	}
	x, y, wx, wy := 32, 40, 100.0, 100.0
	i := (y*w + x) * 4

	gx0, gy0, cr0 := waterWave(wx, wy, 0)
	gx1, gy1, cr1 := waterWave(wx, wy, 1.7)

	r0 := mk()
	r0.SetAnimTime(0)
	r0.Pix[i], r0.Pix[i+1], r0.Pix[i+2] = 120, 120, 120
	r0.blendWater(x, y, i, gx0, gy0, cr0, 100)
	base := [3]byte{r0.Pix[i], r0.Pix[i+1], r0.Pix[i+2]}

	r1 := mk()
	r1.SetAnimTime(1.7)
	r1.Pix[i], r1.Pix[i+1], r1.Pix[i+2] = 120, 120, 120
	r1.blendWater(x, y, i, gx1, gy1, cr1, 100)
	moved := [3]byte{r1.Pix[i], r1.Pix[i+1], r1.Pix[i+2]}

	if base == moved {
		t.Errorf("ripple did not change the pixel between t=0 and t=1.7: %v", base)
	}

	// Same time -> identical (keeps serial/parallel renders bit-identical).
	r2 := mk()
	r2.SetAnimTime(0)
	r2.Pix[i], r2.Pix[i+1], r2.Pix[i+2] = 120, 120, 120
	r2.blendWater(x, y, i, gx0, gy0, cr0, 100)
	if ([3]byte{r2.Pix[i], r2.Pix[i+1], r2.Pix[i+2]}) != base {
		t.Error("blendWater not deterministic at a fixed animTime")
	}
}

// Water must not read as a mirror: even at maximum grazing, a black water
// pixel under a pure-white sky stays far below 50% reflection.
func TestBlendWaterIsNotMirror(t *testing.T) {
	w, h := 16, 24
	r := &Renderer{Width: w, Height: h, Pix: make([]byte, w*h*4)}
	r.focal = 100
	sky := &assets.RGBA{Width: 4, Height: 4, Pix: make([]byte, 4*4*4)}
	for p := range sky.Pix {
		sky.Pix[p] = 255
	}
	r.skyTex, r.skyScale = sky, 1
	r.skyTx = make([]int, w)

	// Row 1px below the horizon (~h/2) = the most grazing water can get.
	x, y := 8, h/2+1
	i := (y*w + x) * 4
	r.Pix[i], r.Pix[i+1], r.Pix[i+2], r.Pix[i+3] = 0, 0, 0, 255

	r.blendWater(x, y, i, 0, 0, 0.5, 100)

	if r.Pix[i] > 120 || r.Pix[i+1] > 120 {
		t.Errorf("water at grazing reflects too much white sky (mirror-like): %d,%d,%d",
			r.Pix[i], r.Pix[i+1], r.Pix[i+2])
	}
	// The murky tint should leave blue the strongest channel.
	if r.Pix[i+2] < r.Pix[i] || r.Pix[i+2] < r.Pix[i+1] {
		t.Errorf("expected a blue-green liquid tint, got %d,%d,%d", r.Pix[i], r.Pix[i+1], r.Pix[i+2])
	}
}

// Water has depth: a bright bottom is absorbed (darkened) more the further
// the view ray travels through the water — much more at the horizon than
// underfoot — and red is absorbed faster than blue, so a white bottom
// trends blue-green with depth.
func TestBlendWaterHasDepth(t *testing.T) {
	w, h := 16, 80
	mk := func(y int) (rr, gg, bb byte) {
		r := &Renderer{Width: w, Height: h, Pix: make([]byte, w*h*4)}
		r.focal = 100
		i := (y*w + 8) * 4
		r.Pix[i], r.Pix[i+1], r.Pix[i+2], r.Pix[i+3] = 220, 220, 220, 255 // bright grey bottom
		r.blendWater(8, y, i, 0, 0, 0.5, 100)
		return r.Pix[i], r.Pix[i+1], r.Pix[i+2]
	}

	// y just below the horizon (~h/2) is the deepest view; near the bottom
	// of the screen is the shallowest.
	deepR, deepG, deepB := mk(h/2 + 2)
	shallowR, shallowG, shallowB := mk(h - 3)

	if !(int(deepR)+int(deepG)+int(deepB) < int(shallowR)+int(shallowG)+int(shallowB)-40) {
		t.Errorf("deep water not noticeably darker: deep(%d,%d,%d) shallow(%d,%d,%d)",
			deepR, deepG, deepB, shallowR, shallowG, shallowB)
	}
	// Blue survives the water column better than red.
	if !(deepB > deepR) {
		t.Errorf("deep water should trend blue: %d,%d,%d", deepR, deepG, deepB)
	}
}

// Depth also comes from view DISTANCE: the same pixel over a bright bottom
// darkens as `dist` grows, independent of screen row.
func TestBlendWaterDepthFromDistance(t *testing.T) {
	w, h := 16, 40
	shade := func(dist float64) int {
		r := &Renderer{Width: w, Height: h, Pix: make([]byte, w*h*4)}
		r.focal = 100
		x, y := 8, 26
		i := (y*w + x) * 4
		r.Pix[i], r.Pix[i+1], r.Pix[i+2], r.Pix[i+3] = 210, 210, 210, 255
		r.blendWater(x, y, i, 0, 0, 0.5, dist)
		return int(r.Pix[i]) + int(r.Pix[i+1]) + int(r.Pix[i+2])
	}
	near, far := shade(30), shade(600)
	if !(far < near-20) {
		t.Errorf("far water (%d) not darker than near water (%d)", far, near)
	}
}

// The sun glint is a localised highlight, not a uniform sheen: a crest
// pixel near the drifting sun column and the horizon is brighter than the
// same pixel far off to the side.
func TestBlendWaterSunGlint(t *testing.T) {
	w, h := 128, 40
	shade := func(x int) int {
		r := &Renderer{Width: w, Height: h, Pix: make([]byte, w*h*4)}
		r.focal = 100
		r.sinA, r.cosA = 0, 1 // yaw 0 -> sun column at screen centre
		y := h/2 + 1          // just below the horizon (grazing)
		i := (y*w + x) * 4
		r.Pix[i], r.Pix[i+1], r.Pix[i+2], r.Pix[i+3] = 30, 30, 30, 255
		r.blendWater(x, y, i, 0, 0, 1.0, 100) // crest = 1 (a wave peak)
		return int(r.Pix[i]) + int(r.Pix[i+1]) + int(r.Pix[i+2])
	}
	onSun := shade(w / 2)  // under the sun column
	offSun := shade(w / 8) // well to the side
	if !(onSun > offSun+30) {
		t.Errorf("sun glint not localised: on-sun %d vs off-sun %d", onSun, offSun)
	}
}

// Lava's glow breathes: the same pixel differs between two clock times.
func TestShadeLiquidLavaPulses(t *testing.T) {
	mk := func(at float64) [3]byte {
		r := &Renderer{Width: 4, Height: 4, Pix: make([]byte, 4*4*4), Normal: make([]byte, 4*4*4)}
		r.SetAnimTime(at)
		r.Pix[0], r.Pix[1], r.Pix[2] = 60, 30, 15
		r.shadeLiquid(liquidLava, 0, 0, 0, false, 300, 700, 0, 0, 0, 0) // off-origin so the phase isn't 0
		return [3]byte{r.Pix[0], r.Pix[1], r.Pix[2]}
	}
	if mk(0) == mk(0.9) {
		t.Error("lava glow did not pulse over time")
	}
	if mk(2.0) != mk(2.0) {
		t.Error("lava shading not deterministic at a fixed time")
	}
}

// In G-buffer mode, water must stamp the matWater material key so the
// enhanced lighting pass (light.frag) knows to damp dynamic lights over it
// — the fix for a muzzle flash blowing the reflection out.
func TestWaterStampsMaterialKey(t *testing.T) {
	w, h := 8, 8
	r := &Renderer{Width: w, Height: h, Pix: make([]byte, w*h*4), Normal: make([]byte, w*h*4)}
	r.focal = 100
	x, y := 4, 5
	i := (y*w + x) * 4
	r.Pix[i], r.Pix[i+1], r.Pix[i+2] = 100, 100, 100

	r.shadeLiquid(liquidWater, x, y, i, true, 0, 0, 0, 0, 0.5, 100)
	if r.Normal[i+3] != matWater {
		t.Errorf("water material key = %d, want %d", r.Normal[i+3], matWater)
	}
	// matWater must sit clear of the sprite (0.25..0.75) and emissive
	// (>0.75) buckets light.frag already uses.
	m := float64(matWater) / 255
	if m <= 0.10 || m >= 0.22 {
		t.Errorf("matWater %.3f outside light.frag's water window (0.10, 0.22)", m)
	}
	// Non-G-buffer mode leaves Normal untouched.
	r2 := &Renderer{Width: w, Height: h, Pix: make([]byte, w*h*4)}
	r2.focal = 100
	r2.shadeLiquid(liquidWater, x, y, i, false, 0, 0, 0, 0, 0.5, 100) // must not panic without Normal
}

// applyBright: G-buffer mode records the mask value in LightParam.g;
// vanilla mode scales the shaded pixel up (hue preserved); a ~zero mask is
// a no-op.
func TestApplyBright(t *testing.T) {
	w, h := 4, 4
	mask := &assets.RGBA{Width: 2, Height: 2, Pix: []byte{
		255, 255, 255, 255, 0, 0, 0, 255,
		0, 0, 0, 255, 10, 10, 10, 255,
	}}

	// G-buffer: LightParam.g gets the luma; a dark mask texel writes nothing.
	r := &Renderer{Width: w, Height: h, Pix: make([]byte, w*h*4), LightParam: make([]byte, w*h*4)}
	r.applyBright(mask, 0, 0, 0, true) // bright texel
	if r.LightParam[1] != 255 {
		t.Errorf("gb: LightParam.g = %d, want 255", r.LightParam[1])
	}
	r.applyBright(mask, 1, 0, 4, true) // black texel -> no-op
	if r.LightParam[5] != 0 {
		t.Errorf("gb: black mask texel wrote %d", r.LightParam[5])
	}

	// Vanilla: a lit green panel in shadow gets scaled up, staying green.
	r2 := &Renderer{Width: w, Height: h, Pix: make([]byte, w*h*4)}
	r2.Pix[0], r2.Pix[1], r2.Pix[2] = 20, 60, 20
	r2.applyBright(mask, 0, 0, 0, false)
	if r2.Pix[1] <= 60 {
		t.Errorf("vanilla: brightmap did not lift the pixel: %d,%d,%d", r2.Pix[0], r2.Pix[1], r2.Pix[2])
	}
	if !(r2.Pix[1] > r2.Pix[0] && r2.Pix[1] > r2.Pix[2]) {
		t.Errorf("vanilla: brightmap changed the hue: %d,%d,%d", r2.Pix[0], r2.Pix[1], r2.Pix[2])
	}
}

// ApplyFog blends world pixels toward the fog colour by distance, skips the
// sky (background depth), and is a no-op when disabled.
func TestApplyFog(t *testing.T) {
	w, h := 8, 4
	mk := func() *Renderer {
		r := &Renderer{Width: w, Height: h, Pix: make([]byte, w*h*4), depth: make([]float32, w*h)}
		for i := range r.Pix {
			r.Pix[i] = 200
		}
		for i := range r.depth {
			r.depth[i] = math.MaxFloat32 // background everywhere (the sky sentinel)
		}
		return r
	}

	// Disabled -> untouched.
	r := mk()
	r.ApplyFog()
	if r.Pix[0] != 200 {
		t.Fatal("ApplyFog ran with fog disabled")
	}

	// Enabled, but every pixel is background -> still untouched.
	r = mk()
	r.SetFog(0, 0, 0, 0.01)
	r.ApplyFog()
	if r.Pix[0] != 200 {
		t.Fatal("ApplyFog fogged a background (sky) pixel")
	}

	// A near pixel barely fogs; a far pixel fogs heavily toward black.
	r = mk()
	r.SetFog(0, 0, 0, 0.01)
	r.depth[0] = 5      // near
	r.depth[1] = 5000   // far -> 1-exp(-50) ~= 1
	r.ApplyFog()
	if !(r.Pix[0] > 180 && r.Pix[0] < 200) {
		t.Errorf("near pixel fogged too much/little: %d", r.Pix[0])
	}
	if r.Pix[4] > 20 { // cell 1 -> byte offset 4
		t.Errorf("far pixel not fogged toward black: %d", r.Pix[4])
	}
}
