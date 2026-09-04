package raster

import (
	"math"
	"testing"

	"twopointfive/assets"
)

// solid builds a w x h opaque sprite of one colour; alt builds a 2-colour
// vertical-stripe sprite for filtering checks.
func solidSprite(w, h int, r, g, b byte) *assets.RGBA {
	pix := make([]byte, w*h*4)
	for i := 0; i < len(pix); i += 4 {
		pix[i], pix[i+1], pix[i+2], pix[i+3] = r, g, b, 255
	}
	return &assets.RGBA{Width: w, Height: h, Pix: pix, TexelsPerUnit: 1}
}

func TestBuildMipChainDimensions(t *testing.T) {
	sp := solidSprite(40, 24, 200, 100, 50)
	mc := buildMipChain(sp)

	if mc.levels[0].w != 40 || mc.levels[0].h != 24 {
		t.Fatalf("level 0 = %dx%d, want 40x24", mc.levels[0].w, mc.levels[0].h)
	}
	// Each level halves (rounded up) until 1x1.
	for i := 1; i < len(mc.levels); i++ {
		pw, ph := mc.levels[i-1].w, mc.levels[i-1].h
		ww, wh := (pw+1)/2, (ph+1)/2
		if mc.levels[i].w != ww || mc.levels[i].h != wh {
			t.Errorf("level %d = %dx%d, want %dx%d", i, mc.levels[i].w, mc.levels[i].h, ww, wh)
		}
	}
	last := mc.levels[len(mc.levels)-1]
	if last.w != 1 || last.h != 1 {
		t.Errorf("chain bottoms out at %dx%d, want 1x1", last.w, last.h)
	}
	// A solid sprite stays its own colour at every level.
	if last.pix[0] != 200 || last.pix[1] != 100 || last.pix[2] != 50 || last.pix[3] != 255 {
		t.Errorf("1x1 level = %v, want [200 100 50 255]", last.pix[:4])
	}
}

func TestMipChainPremultipliesAlpha(t *testing.T) {
	// One opaque white texel, three transparent: level 1 should be 1/4
	// coverage with premultiplied (so also ~1/4) colour, no dark halo.
	pix := []byte{
		255, 255, 255, 255, 0, 0, 0, 0,
		0, 0, 0, 0, 0, 0, 0, 0,
	}
	sp := &assets.RGBA{Width: 2, Height: 2, Pix: pix, TexelsPerUnit: 1}
	mc := buildMipChain(sp)
	l1 := mc.levels[1]
	if l1.w != 1 || l1.h != 1 {
		t.Fatalf("level 1 = %dx%d, want 1x1", l1.w, l1.h)
	}
	// (255 + 0 + 0 + 0) / 4 ≈ 63 for every channel, alpha included.
	for i, got := range l1.pix {
		if got < 60 || got > 66 {
			t.Errorf("level 1 byte %d = %d, want ~63 (premultiplied quarter-coverage)", i, got)
		}
	}
}

func TestSampleBilinearHitsTexelCentres(t *testing.T) {
	sp := solidSprite(4, 1, 10, 20, 30)
	sp.Pix[0], sp.Pix[1], sp.Pix[2] = 100, 0, 0    // texel 0
	sp.Pix[12], sp.Pix[13], sp.Pix[14] = 0, 200, 0 // texel 3
	l := buildMipChain(sp).levels[0]

	r, _, _, a := sampleBilinear(l, (0.0+0.5)/4, 0.5)
	if math.Abs(r-100) > 0.5 || math.Abs(a-255) > 0.5 {
		t.Errorf("centre of texel 0: r=%.1f a=%.1f, want 100/255", r, a)
	}
	_, g, _, _ := sampleBilinear(l, (3.0+0.5)/4, 0.5)
	if math.Abs(g-200) > 0.5 {
		t.Errorf("centre of texel 3: g=%.1f, want 200", g)
	}
	// Clamp-to-edge: u well past the right edge still resolves to texel 3.
	_, g2, _, _ := sampleBilinear(l, 1.4, 0.5)
	if math.Abs(g2-200) > 0.5 {
		t.Errorf("u=1.4 clamped: g=%.1f, want 200", g2)
	}
}

func TestSampleTrilinearBlendsLevels(t *testing.T) {
	// 8x1 black/white stripes. At the coarsest level every texel has
	// averaged to mid-grey, so a high LOD sample is ~127 regardless of u —
	// exactly the anti-aliasing point-sampling can't do.
	sp := solidSprite(8, 1, 0, 0, 0)
	for x := 0; x < 8; x += 2 {
		o := x * 4
		sp.Pix[o], sp.Pix[o+1], sp.Pix[o+2] = 255, 255, 255
	}
	mc := buildMipChain(sp)

	rHi, _, _, _ := mc.sampleTrilinear(0.03, 0.5, float64(len(mc.levels)-1)) // near a white texel
	if math.Abs(rHi-127) > 20 {
		t.Errorf("coarsest-level sample = %.1f, want ~127 (stripes averaged out)", rHi)
	}
	// lod <= 0 is just bilinear of level 0 — near u=0.03 that's ~white.
	rLo, _, _, _ := mc.sampleTrilinear(0.03, 0.5, 0)
	if rLo < 200 {
		t.Errorf("level-0 sample near a white texel = %.1f, want bright", rLo)
	}
	// A mid LOD lands between the two.
	rMid, _, _, _ := mc.sampleTrilinear(0.03, 0.5, float64(len(mc.levels)-1)/2)
	if !(rMid > rHi-1 && rMid < rLo+1) {
		t.Errorf("mid-LOD %.1f not between coarse %.1f and fine %.1f", rMid, rHi, rLo)
	}
}
