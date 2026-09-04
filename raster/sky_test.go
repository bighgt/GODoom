package raster

import (
	"math"
	"testing"

	"twopointfive/assets"
	"twopointfive/wad"
)

func TestSkyTextureForMap(t *testing.T) {
	cases := map[string]string{
		"E1M1": "SKY1", "E1M8": "SKY1",
		"E2M1": "SKY2", "E3M5": "SKY3", "E4M1": "SKY4",
		"MAP01": "SKY1", "MAP11": "SKY1",
		"MAP12": "SKY2", "MAP20": "SKY2",
		"MAP21": "SKY3", "MAP32": "SKY3",
		"": "SKY1", "GARBAGE": "SKY1",
	}
	for in, want := range cases {
		if got := skyTextureForMap(in); got != want {
			t.Errorf("skyTextureForMap(%q) = %q, want %q", in, got, want)
		}
	}
}

// fakeSky is a solid W x H texture, enough for buildSkyTable to fill skyTx.
func fakeSky(w, h int) *assets.RGBA {
	return &assets.RGBA{Width: w, Height: h, Pix: make([]byte, w*h*4), TexelsPerUnit: 1}
}

// meanColumnShift returns the average (mod width) change in skyTx after
// yawing the camera by dAngle radians.
func meanColumnShift(r *Renderer, dAngle float64) float64 {
	lvl := &wad.Level{Name: "MAP01"}
	r.buildSkyTable(&Camera{Angle: 0}, lvl)
	base := append([]int(nil), r.skyTx...)
	r.buildSkyTable(&Camera{Angle: dAngle}, lvl)

	w := r.skyTex.Width
	var sum float64
	for x := range base {
		d := r.skyTx[x] - base[x]
		d = ((d % w) + w) % w
		if d > w/2 {
			d -= w
		}
		sum += math.Abs(float64(d))
	}
	return sum / float64(len(base))
}

// A narrow (WAD-format) sky scrolls at id's ANGLETOSKYSHIFT rate —
// skyPixelsPerTurn columns per full turn, so a 256-wide sky wraps 4x. A
// wide panorama maps its whole width to one turn instead.
func TestSkyScrollRate(t *testing.T) {
	const w, h = 400, 300
	r := New(nil, w, h)
	r.focal = 200
	r.skyColAngleFocal = -1

	// One turn's worth of yaw split into a small step.
	const step = 2 * math.Pi / 512

	r.skybox = fakeSky(256, 128) // narrow -> 1024 cols/turn
	narrow := meanColumnShift(r, step)
	wantNarrow := skyPixelsPerTurn * step / (2 * math.Pi)
	if math.Abs(narrow-wantNarrow) > 0.75 {
		t.Errorf("narrow sky shift %.2f cols for %.4f rad, want ~%.2f (ANGLETOSKYSHIFT)", narrow, step, wantNarrow)
	}

	r.skybox = fakeSky(4096, 2048) // wide panorama -> its own width per turn
	r.skyColAngleFocal = -1
	wide := meanColumnShift(r, step)
	wantWide := 4096.0 * step / (2 * math.Pi)
	if math.Abs(wide-wantWide) > 4 {
		t.Errorf("panorama shift %.2f cols for %.4f rad, want ~%.2f (1:1 per turn)", wide, step, wantWide)
	}

	// The narrow sky must move meaningfully faster per screen-relative pixel:
	// 1024/turn vs 4096/turn is 4x on the same 256-wide texture.
	if narrow/256 <= wide/4096*1.5 {
		t.Errorf("narrow WAD sky not scrolling faster than the panorama (%.3f vs %.3f per texel)", narrow/256, wide/4096)
	}
}

// With no disk panorama, buildSkyTable resolves the WAD's own per-map sky
// (PrBoom's rule) through the texture set — SKY1 exists in every IWAD's
// TEXTURE1 lump.
func TestBuildSkyTableResolvesWADSky(t *testing.T) {
	_, _, tex := loadTestLevel(t)
	r := New(tex, 320, 200)
	if r.skybox != nil {
		t.Skip("a real skybox asset is on disk; this test wants the WAD sky path")
	}
	r.focal = 160
	r.skyColAngleFocal = -1

	r.buildSkyTable(&Camera{Angle: 1}, &wad.Level{Name: "E1M1"})
	if r.skyTex == nil {
		t.Fatal("buildSkyTable resolved no WAD sky for E1M1")
	}
	if r.skyTex.Width <= 0 || r.skyTex.Height <= 0 {
		t.Fatalf("resolved sky has bad dims %dx%d", r.skyTex.Width, r.skyTex.Height)
	}
	if r.skyScale <= 0 {
		t.Fatalf("skyScale = %v", r.skyScale)
	}

	// A different map keeps the cache honest.
	r.buildSkyTable(&Camera{Angle: 1}, &wad.Level{Name: "E1M1"})
	if r.skyWADFor != "E1M1" {
		t.Fatalf("skyWADFor = %q, want E1M1", r.skyWADFor)
	}
}

// drawSkySpan paints its column into r.Pix and, unlike every other span,
// leaves r.depth untouched (the sky is infinitely far — the lighting pass
// and sprite depth test both key off depth == +Inf).
func TestDrawSkySpanWritesColumnNotDepth(t *testing.T) {
	const w, h = 64, 80
	r := New(nil, w, h)
	for i := range r.depth {
		r.depth[i] = math.MaxFloat32
	}
	r.pitchShear = 0
	r.skybox = fakeSky(256, 128)
	// A recognisable gradient so a written pixel is obviously not the zero clear.
	for p := 0; p+3 < len(r.skybox.Pix); p += 4 {
		r.skybox.Pix[p] = byte((p / 4) & 255)
		r.skybox.Pix[p+1] = 200
		r.skybox.Pix[p+2] = 100
		r.skybox.Pix[p+3] = 255
	}
	r.focal = 100
	r.skyColAngleFocal = -1
	r.buildSkyTable(&Camera{Angle: 0}, &wad.Level{Name: "MAP01"})

	const col = 20
	r.drawSkySpan(col, 0, h)
	wrote := 0
	for y := 0; y < h; y++ {
		cell := y*w + col
		if r.Pix[cell*4+1] == 200 && r.Pix[cell*4+2] == 100 {
			wrote++
		}
		if r.depth[cell] != math.MaxFloat32 {
			t.Fatalf("drawSkySpan wrote depth at row %d", y)
		}
	}
	if wrote != h {
		t.Fatalf("drawSkySpan filled %d/%d rows of the column", wrote, h)
	}
}
