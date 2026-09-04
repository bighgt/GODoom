package raster

import (
	"math"
	"testing"

	"twopointfive/assets"
)

func TestParseTexQuality(t *testing.T) {
	cases := []struct {
		in string
		q  texQuality
		n  int
	}{
		{"nearest", tqNearest, 1},
		{"bilinear", tqBilinear, 1},
		{"trilinear", tqTrilinear, 1},
		{"aniso2x", tqAniso, 2},
		{"aniso4x", tqAniso, 4},
		{"aniso8x", tqAniso, 8},
		{"aniso16x", tqAniso, 16},
		{"junk", tqTrilinear, 1},
		{"", tqTrilinear, 1},
	}
	for _, c := range cases {
		if q, n := parseTexQuality(c.in); q != c.q || n != c.n {
			t.Errorf("parseTexQuality(%q) = (%d,%d), want (%d,%d)", c.in, q, n, c.q, c.n)
		}
	}
}

func TestSetTextureQualityFansOut(t *testing.T) {
	r := New(nil, 64, 64)

	r.SetTextureQuality("nearest")
	if r.texQ != tqNearest || r.spriteFilter != filterNearest || r.voxelSmooth {
		t.Fatalf("nearest fan-out wrong: tq=%d sprite=%d voxelSmooth=%v", r.texQ, r.spriteFilter, r.voxelSmooth)
	}
	r.SetTextureQuality("bilinear")
	if r.spriteFilter != filterBilinear || !r.voxelSmooth {
		t.Fatal("bilinear fan-out wrong")
	}
	r.SetTextureQuality("aniso8x")
	if r.texQ != tqAniso || r.maxAniso != 8 || r.spriteFilter != filterTrilinear || !r.voxelSmooth {
		t.Fatalf("aniso8x fan-out wrong: tq=%d max=%d sprite=%d", r.texQ, r.maxAniso, r.spriteFilter)
	}
}

// solidTex builds an opaque w×h texture from a per-texel colour function.
func solidTex(w, h int, fn func(x, y int) (r, g, b byte)) *assets.RGBA {
	pix := make([]byte, w*h*4)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := (y*w + x) * 4
			pix[i], pix[i+1], pix[i+2] = fn(x, y)
			pix[i+3] = 255
		}
	}
	return &assets.RGBA{Width: w, Height: h, Pix: pix, TexelsPerUnit: 1}
}

func TestBuildTexMipsChain(t *testing.T) {
	src := solidTex(4, 4, func(x, _ int) (byte, byte, byte) {
		if x < 2 {
			return 0, 0, 0
		}
		return 255, 255, 255
	})
	m := buildTexMips(src)
	if got := len(m.lv); got != 3 { // 4x4 -> 2x2 -> 1x1
		t.Fatalf("mip levels = %d, want 3", got)
	}
	if m.lv[2].w != 1 || m.lv[2].h != 1 {
		t.Fatalf("last level = %dx%d, want 1x1", m.lv[2].w, m.lv[2].h)
	}
	if r := m.lv[2].pix[0]; r < 120 || r > 136 {
		t.Fatalf("1x1 mip red = %d, want ~128 (avg of 0 and 255)", r)
	}
}

func TestBilerpWrap(t *testing.T) {
	src := solidTex(2, 1, func(x, _ int) (byte, byte, byte) {
		if x == 0 {
			return 0, 0, 0
		}
		return 255, 255, 255
	})
	m := buildTexMips(src)

	if r, _, _, _ := m.bilerpLevel(0, 0.5, 0.5); r > 8 {
		t.Errorf("texel-0 centre red = %.0f, want ~0", r)
	}
	if r, _, _, _ := m.bilerpLevel(0, 1.5, 0.5); r < 247 {
		t.Errorf("texel-1 centre red = %.0f, want ~255", r)
	}
	r0, _, _, _ := m.bilerpLevel(0, 0.5, 0.5)
	rw, _, _, _ := m.bilerpLevel(0, 2.5, 0.5) // wraps back onto texel 0
	if math.Abs(r0-rw) > 1 {
		t.Errorf("wrap mismatch: u=0.5 -> %.1f, u=2.5 -> %.1f", r0, rw)
	}
}

func TestAnisoIsotropicEqualsTrilinear(t *testing.T) {
	src := solidTex(16, 16, func(x, y int) (byte, byte, byte) {
		return byte(x * 15), byte(y * 15), 128
	})
	m := buildTexMips(src)
	for _, lod := range []float64{0, 1.3, 3} {
		fp := math.Exp2(lod) // isotropic footprint: major == minor
		tr, tg, tb, _ := m.trilinear(6.2, 9.7, lod)
		ar, ag, ab, _ := m.aniso(6.2, 9.7, fp, fp, 1, 0, 16)
		if math.Abs(tr-ar)+math.Abs(tg-ag)+math.Abs(tb-ab) > 1.5 {
			t.Errorf("lod %.1f: aniso (%.1f,%.1f,%.1f) != trilinear (%.1f,%.1f,%.1f)",
				lod, ar, ag, ab, tr, tg, tb)
		}
	}
}

func TestAnisoAveragesAlongMajorAxis(t *testing.T) {
	// Vertical stripes: even columns black, odd white. A wide horizontal
	// footprint spanning many stripes must grey out toward ~128.
	src := solidTex(32, 4, func(x, _ int) (byte, byte, byte) {
		if x&1 == 0 {
			return 0, 0, 0
		}
		return 255, 255, 255
	})
	m := buildTexMips(src)
	r, _, _, _ := m.aniso(8, 2, 8 /*major texels*/, 0.01 /*minor*/, 1, 0, 16)
	if r < 96 || r > 160 {
		t.Errorf("aniso across stripes red = %.0f, want ~128", r)
	}
}

func TestAnisoPlanMatchesInline(t *testing.T) {
	src := solidTex(24, 20, func(x, y int) (byte, byte, byte) {
		return byte((x * 11) & 255), byte((y * 13) & 255), byte((x ^ y) & 255)
	})
	m := buildTexMips(src)
	for _, tc := range []struct{ major, minor, mdu, mdv float64 }{
		{8, 1, 1, 0}, {6, 2, 0, 1}, {1, 1, 1, 0}, {12, 0.5, 0.7071, 0.7071},
	} {
		p := planAniso(tc.major, tc.minor, tc.mdu, tc.mdv, 8)
		ar, ag, ab, aa := m.sampleAnisoPlan(3.3, 7.1, p)
		br, bg, bb, ba := m.aniso(3.3, 7.1, tc.major, tc.minor, tc.mdu, tc.mdv, 8)
		if ar != br || ag != bg || ab != bb || aa != ba {
			t.Errorf("plan vs inline mismatch for %+v: (%.2f,%.2f,%.2f,%.2f) != (%.2f,%.2f,%.2f,%.2f)",
				tc, ar, ag, ab, aa, br, bg, bb, ba)
		}
	}
}

func TestWarmTextureCachePopulates(t *testing.T) {
	lvl, _, tex := loadTestLevel(t)
	r := New(tex, 320, 200)

	r.SetTextureQuality("nearest")
	r.WarmTextureCache(lvl)
	if len(r.worldMipsCache) != 0 {
		t.Fatalf("nearest warmed %d chains, want 0", len(r.worldMipsCache))
	}

	r.SetTextureQuality("aniso8x")
	r.WarmTextureCache(lvl)
	if len(r.worldMipsCache) == 0 {
		t.Fatal("aniso8x WarmTextureCache built no mip chains")
	}
	// Idempotent: a second call adds nothing.
	n := len(r.worldMipsCache)
	r.WarmTextureCache(lvl)
	if len(r.worldMipsCache) != n {
		t.Fatalf("second WarmTextureCache changed the cache size %d -> %d", n, len(r.worldMipsCache))
	}
}

func TestRenderFilteredQualitiesNoPanicAndDiffer(t *testing.T) {
	lvl, tree, tex := loadTestLevel(t)
	const w, h = 480, 300
	cam := testCameras(lvl, tree)[0]

	base := New(tex, w, h)
	base.SetTextureQuality("nearest")
	base.Render(lvl, tree, cam)
	near := append([]byte(nil), base.Pix...)

	for _, q := range []string{"bilinear", "trilinear", "aniso2x", "aniso8x", "aniso16x"} {
		r := New(tex, w, h)
		r.SetTextureQuality(q)
		r.Render(lvl, tree, cam) // must not panic
		diff := 0
		for i := range r.Pix {
			if r.Pix[i] != near[i] {
				diff++
			}
		}
		if diff == 0 {
			t.Errorf("%s produced a byte-identical frame to nearest", q)
		}
	}
}

func TestFilteredRenderSerialEqualsParallel(t *testing.T) {
	lvl, tree, tex := loadTestLevel(t)
	const w, h = 512, 320

	ser := New(tex, w, h)
	setStripCount(ser, 1)
	ser.SetTextureQuality("aniso8x")

	par := New(tex, w, h)
	if len(par.strips) < 2 {
		setStripCount(par, 8)
	}
	par.SetTextureQuality("aniso8x")

	for i, cam := range testCameras(lvl, tree) {
		ser.Render(lvl, tree, cam)
		par.Render(lvl, tree, cam)
		for b := range ser.Pix {
			if ser.Pix[b] != par.Pix[b] {
				t.Fatalf("cam %d: serial != parallel at byte %d (%d vs %d)", i, b, ser.Pix[b], par.Pix[b])
			}
		}
	}
}
