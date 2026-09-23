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

// Fake contrast direction: renderSeg subtracts fakeContrast from an E-W
// wall's raw light and adds it to an N-S wall's, which after >>4 is
// r_segs.c's lightnum-- / lightnum++ — N-S brighter, E-W dimmer.
func TestFakeContrastDirection(t *testing.T) {
	clampInt := func(v, lo, hi int) int {
		if v < lo {
			return lo
		}
		if v > hi {
			return hi
		}
		return v
	}
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

// pspriteShade is the bright ("point blank") end of a sector's scalelight —
// id shades the held weapon with spritelights[MAXLIGHTSCALE-1] — so it's
// never darker than a world sprite of the same sector light at any positive
// depth (shadeMul at that depth — a world sprite reads the identical
// scalelight table pspriteShade does, per id's R_ProjectSprite), tracks the
// room's brightness, and a muzzle-flash frame bypasses it.
func TestPspriteShade(t *testing.T) {
	if got := pspriteShade(120, true); got != 256 {
		t.Errorf("pspriteShade(fullBright)=%d, want 256", got)
	}
	if pspriteShade(224, false) <= pspriteShade(48, false) {
		t.Error("pspriteShade did not brighten with a brighter sector")
	}
	for _, d := range []float64{1, 50, 300, 3000} {
		if pspriteShade(128, false) < shadeMul(128, d) {
			t.Errorf("pspriteShade dimmer than a world sprite at depth %.0f", d)
		}
	}
}

// LightScaleRef/LightScaleValue are the hardware renderer's own read-back of
// this package's wall-shading reference/state.
func TestLightScaleExports(t *testing.T) {
	if LightScaleRef != wallScaleRef || LightScaleValue() != lightScale {
		t.Error("exported LightScaleRef / LightScaleValue don't match the package values")
	}
}
