package raster

import "testing"

// lightFactor is the 0..1 float form of a wall's brightness for a
// (light, depth) pair. It reads the same scalelight table as the production
// integer path (shadeMul), so the two agree exactly — the tests below pin
// that table against PrBoom's R_InitLightTables.
func lightFactor(sectorLight int16, depth float64) float64 {
	return float64(shadeMul(sectorLight, depth)) / 256
}

// shade multiplies c's RGB by f (a lightFactor result), leaving alpha
// intact; f >= 1 returns c unchanged. The renderer inlines an integer form
// of this (see shadeMul / lightRow) — this readable version exists only to
// exercise the arithmetic in isolation.
func shade(c [4]byte, f float64) [4]byte {
	if f >= 1 {
		return c
	}
	return [4]byte{
		byte(float64(c[0]) * f),
		byte(float64(c[1]) * f),
		byte(float64(c[2]) * f),
		c[3],
	}
}

func TestLightFactorMonotonicInDistance(t *testing.T) {
	// At a fixed sector light, moving further away never gets brighter.
	prev := 2.0
	for depth := 1.0; depth <= 4096; depth += 25 {
		f := lightFactor(160, depth)
		if f > prev+1e-9 {
			t.Fatalf("lightFactor(160, %.0f)=%.3f brighter than the nearer sample %.3f", depth, f, prev)
		}
		if f < 0 || f > 1 {
			t.Fatalf("lightFactor(160, %.0f)=%.3f out of [0,1]", depth, f)
		}
		prev = f
	}
}

func TestLightFactorMonotonicInSectorLight(t *testing.T) {
	// At a fixed distance, a brighter sector is never darker.
	prev := -1.0
	for l := int16(0); l <= 255; l += 8 {
		f := lightFactor(l, 300)
		if f < prev-1e-9 {
			t.Fatalf("lightFactor(%d, 300)=%.3f darker than the dimmer sector %.3f", l, f, prev)
		}
		prev = f
	}
}

func TestLightFactorBounds(t *testing.T) {
	// Point blank in a normal-lit sector is full bright: rw_scale clamps to
	// MAXLIGHTSCALE-1 and startmap - 23 goes negative -> COLORMAP row 0.
	if f := lightFactor(160, 1); f < 0.999 {
		t.Errorf("lightFactor(160,1)=%.3f, expected full bright", f)
	}
	// A light-255 sector has startmap 0, so every scalelight entry is row 0:
	// full bright at any distance, exactly as in vanilla.
	if f := lightFactor(255, 4000); f < 0.999 {
		t.Errorf("lightFactor(255,4000)=%.3f, expected full bright at all ranges", f)
	}
	// The darkest a surface ever gets is COLORMAP row 31 -> 8/256, never 0
	// (vanilla's last colormap row still shows shape).
	if f := lightFactor(0, 4000); f < 8.0/256-1e-9 || f > 8.0/256+1e-9 {
		t.Errorf("lightFactor(0,4000)=%.4f, want %.4f (row 31)", f, 8.0/256)
	}
	// Out-of-range light levels clamp, they don't extrapolate.
	if f := lightFactor(-40, 100); f < 0 || f > 1 {
		t.Errorf("lightFactor(-40,100)=%.3f out of [0,1]", f)
	}
	if f := lightFactor(4000, 100); f < 0 || f > 1 {
		t.Errorf("lightFactor(4000,100)=%.3f out of [0,1]", f)
	}
}

// The two render-time tables are lifted straight from PrBoom's
// R_InitLightTables. Pin a few cells so a stray refactor of the formulas is
// caught: startmap = (15-lightnum)*4, wall row = startmap - (2560/depth)/2,
// flat row = startmap - ((655360/(z+1))>>12)/2, each clamped to 0..31 and
// mapped to (32-row)*256/32.
func TestScaleLightMatchesPrBoom(t *testing.T) {
	cases := []struct {
		light   int16
		depth   float64
		wantRow int
	}{
		{160, 256, 15},  // lightnum 10, startmap 20, j=10 -> 20 - 10/2
		{160, 512, 18},  // j=5  -> 20 - 5/2
		{160, 4000, 20}, // j=0  -> startmap
		{96, 256, 31},   // lightnum 6, startmap 36, j=10 -> 31 (clamped)
		{224, 128, 0},   // lightnum 14, startmap 4, j=20 -> 4 - 20/2 -> 0
	}
	for _, c := range cases {
		want := float64(colormapMul(c.wantRow)) / 256
		if got := lightFactor(c.light, c.depth); got != want {
			t.Errorf("lightFactor(%d,%.0f)=%.4f, want row %d = %.4f",
				c.light, c.depth, got, c.wantRow, want)
		}
	}
}

func TestZLightMatchesPrBoom(t *testing.T) {
	// flatFactor: light level -> zlight row -> 0..1, matching drawFlatSpan.
	flatFactor := func(light int16, depth float64) float64 {
		return float64(lightRow(light)[distIdx(depth)]) / 256
	}
	// z index = depth/16; scale = (655360/(z+1))>>12; row = clamp(startmap - scale/2, 0, 31).
	// light 160 -> lightnum 10 -> startmap 20.
	for _, tc := range []struct {
		depth   float64
		wantRow int
	}{
		{16 * 4, 20 - ((655360/5)>>12)/2},   // z=4  scale=32 -> 20-16
		{16 * 20, 20 - ((655360/21)>>12)/2}, // z=20 scale=7  -> 20-3
		{16 * 200, 20},                      // z clamps to 127, scale=1 -> 20-0
	} {
		want := float64(colormapMul(tc.wantRow)) / 256
		if got := flatFactor(160, tc.depth); got != want {
			t.Errorf("flatFactor(160,%.0f)=%.4f, want row %d = %.4f",
				tc.depth, got, tc.wantRow, want)
		}
	}
}

// Fake contrast direction: renderSeg subtracts fakeContrast from an E-W
// wall's raw light and adds it to an N-S wall's, which after >>4 is
// r_segs.c's lightnum-- / lightnum++ — N-S brighter, E-W dimmer.
func TestFakeContrastDirection(t *testing.T) {
	const base int16 = 128
	depth := 200.0
	plain := lightFactor(base, depth)
	ew := lightFactor(int16(clampInt(int(base)-fakeContrast, 0, 255)), depth) // y1 == y2
	ns := lightFactor(int16(clampInt(int(base)+fakeContrast, 0, 255)), depth) // x1 == x2
	if !(ew <= plain && plain <= ns) {
		t.Errorf("fake contrast not ordered: E-W %.3f, plain %.3f, N-S %.3f", ew, plain, ns)
	}
	if fakeContrast>>lightSegShift != 1 {
		t.Errorf("fakeContrast %d is not exactly one lightnum step", fakeContrast)
	}
}

func TestExtraLightBrightens(t *testing.T) {
	defer func() { ExtraLight = 0 }()
	depth := 400.0
	dark := lightFactor(112, depth)
	ExtraLight = 2
	lit := lightFactor(112, depth)
	if lit < dark {
		t.Errorf("ExtraLight=2 made it darker: %.3f < %.3f", lit, dark)
	}
}

func TestShadeIdentityWhenFull(t *testing.T) {
	c := [4]byte{10, 128, 250, 255}
	if got := shade(c, 1); got != c {
		t.Errorf("shade(c,1)=%v, want unchanged %v", got, c)
	}
	if got := shade(c, 1.5); got != c {
		t.Errorf("shade(c,>1)=%v, want unchanged %v", got, c)
	}
}

func TestShadeHalvesAndKeepsAlpha(t *testing.T) {
	c := [4]byte{100, 200, 40, 123}
	got := shade(c, 0.5)
	if got[0] != 50 || got[1] != 100 || got[2] != 20 {
		t.Errorf("shade halved wrong: %v", got)
	}
	if got[3] != 123 {
		t.Errorf("shade changed alpha: %d, want 123", got[3])
	}
}

// spriteShade must read the exact same scalelight table the walls do, so a
// monster or item shades identically to the wall it stands against at the
// same sector light and depth (id's R_ProjectSprite:
// spritelights = scalelight[lightlevel>>LIGHTSEGSHIFT], no fake contrast).
func TestSpriteShadeMatchesWalls(t *testing.T) {
	for _, light := range []int16{16, 96, 160, 224, 255} {
		for _, depth := range []float64{8, 64, 300, 2000} {
			if got, want := spriteShade(light, depth, false), shadeMul(light, depth); got != want {
				t.Errorf("spriteShade(%d,%.0f)=%d, want shadeMul=%d", light, depth, got, want)
			}
		}
	}
}

func TestSpriteShadeFullBrightBypasses(t *testing.T) {
	// A dark, distant fullbright sprite (a lit powerup, a projectile) is not
	// dimmed at all.
	if got := spriteShade(0, 4000, true); got != 256 {
		t.Errorf("spriteShade(fullBright)=%d, want 256", got)
	}
}

func TestSpriteShadeMonotonic(t *testing.T) {
	// Darker sector -> darker sprite, at a fixed distance.
	prev := uint32(0)
	for l := int16(0); l <= 255; l += 15 {
		m := spriteShade(l, 300, false)
		if l > 0 && m < prev {
			t.Fatalf("spriteShade not monotonic in light: %d then %d", prev, m)
		}
		prev = m
	}
	// Further away -> never brighter, at a fixed sector light.
	prev = 300
	for d := 1.0; d <= 4000; d += 40 {
		m := spriteShade(160, d, false)
		if m > prev {
			t.Fatalf("spriteShade brightened with distance at depth %.0f: %d > %d", d, m, prev)
		}
		prev = m
	}
}

// pspriteShade is the bright ("point blank") end of a sector's scalelight —
// id shades the held weapon with spritelights[MAXLIGHTSCALE-1] — so it's
// never darker than a world sprite of the same sector light at any positive
// depth, tracks the room's brightness, and a muzzle-flash frame bypasses it.
func TestPspriteShade(t *testing.T) {
	if got := pspriteShade(120, true); got != 256 {
		t.Errorf("pspriteShade(fullBright)=%d, want 256", got)
	}
	if pspriteShade(224, false) <= pspriteShade(48, false) {
		t.Error("pspriteShade did not brighten with a brighter sector")
	}
	for _, d := range []float64{1, 50, 300, 3000} {
		if pspriteShade(128, false) < spriteShade(128, d, false) {
			t.Errorf("pspriteShade dimmer than a world sprite at depth %.0f", d)
		}
	}
}

// BuildLightLUT + the exported column helpers must reproduce the exact
// values the software path's shadeMul / lightRow+distIdx return, so a
// hardware renderer that uploads the LUT and indexes it the same way shades
// identically.
func TestLightLUTMatchesSoftwarePath(t *testing.T) {
	lut := BuildLightLUT()
	if lut.Levels != lightLevels || lut.ScaleCols != maxLightScale || lut.ZCols != maxLightZ {
		t.Fatalf("LUT dims %+v", lut)
	}
	if len(lut.Scale) != lut.Levels*lut.ScaleCols || len(lut.Z) != lut.Levels*lut.ZCols {
		t.Fatalf("LUT slice sizes wrong: scale=%d z=%d", len(lut.Scale), len(lut.Z))
	}

	for _, sl := range []int16{0, 15, 31, 80, 128, 160, 200, 255} {
		row := lightnumOf(sl)
		if row < 0 || row >= lut.Levels {
			t.Fatalf("lightnumOf(%d) = %d", sl, row)
		}
		zrow := lightRow(sl)
		for _, depth := range []float64{0, 1, 8, 32, 64, 200, 640, 4000, -5} {
			// Wall / sprite path.
			sc := scaleLightCol(depth)
			if got, want := lut.Scale[row*lut.ScaleCols+sc], uint16(shadeMul(sl, depth)); got != want {
				t.Errorf("scale sl=%d depth=%.0f: LUT %d != shadeMul %d", sl, depth, got, want)
			}
			// Flat path.
			zc := distIdx(depth)
			if got, want := lut.Z[row*lut.ZCols+zc], zrow[distIdx(depth)]; got != want {
				t.Errorf("z sl=%d depth=%.0f: LUT %d != zlight %d", sl, depth, got, want)
			}
		}
	}
	if LightScaleRef != wallScaleRef || LightScaleValue() != lightScale {
		t.Error("exported LightScaleRef / LightScaleValue don't match the package values")
	}
}
