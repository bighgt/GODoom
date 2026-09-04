package raster

import (
	"math"
	"testing"

	"twopointfive/assets"
	"twopointfive/wad"
)

// benchRenderer builds a Renderer with just the fields the span routines
// touch, at a 1920x1080-ish working size, plus a synthetic texture.
func benchRenderer(w, h int) (*Renderer, *assets.RGBA) {
	r := &Renderer{
		Width: w, Height: h,
		Pix:   make([]byte, w*h*4),
		depth: make([]float32, w*h),
	}
	r.focal = float64(h) / 2 / math.Tan(50*math.Pi/180/2)
	r.sinA, r.cosA = math.Sin(0.7), math.Cos(0.7)

	const ts = 64
	tex := &assets.RGBA{Width: ts, Height: ts, Pix: make([]byte, ts*ts*4), TexelsPerUnit: 1}
	for i := range tex.Pix {
		tex.Pix[i] = byte(i * 7)
		if i%4 == 3 {
			tex.Pix[i] = 255
		}
	}
	return r, tex
}

func BenchmarkDrawFlatSpanFullScreen(b *testing.B) {
	w, h := 1920, 1080
	r, tex := benchRenderer(w, h)
	cam := &Camera{X: 12.5, Y: -40, Z: 41}
	b.SetBytes(int64(w * h * 4))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for x := 0; x < w; x++ {
			r.drawFlatSpan(x, 0, h, tex, nil, 0, 160, true, liquidNone, cam)
		}
	}
}

// BenchmarkFlatSpanByQuality measures the anisotropic-filtering cost on the
// hottest surface (a full-screen floor) at each textureQuality level.
func BenchmarkFlatSpanByQuality(b *testing.B) {
	w, h := 1920, 1080
	cam := &Camera{X: 12.5, Y: -40, Z: 41}
	for _, q := range []string{"nearest", "bilinear", "trilinear", "aniso4x", "aniso16x"} {
		b.Run(q, func(b *testing.B) {
			r, tex := benchRenderer(w, h)
			r.worldMipsCache = make(map[*assets.RGBA]*texMips)
			r.SetTextureQuality(q)
			b.SetBytes(int64(w * h * 4))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				for x := 0; x < w; x++ {
					r.drawFlatSpan(x, 0, h, tex, nil, 0, 160, true, liquidNone, cam)
				}
			}
		})
	}
}

func BenchmarkDrawWallSpanFullScreen(b *testing.B) {
	w, h := 1920, 1080
	r, tex := benchRenderer(w, h)
	cam := &Camera{X: 12.5, Y: -40, Z: 41}
	sd := &wad.Sidedef{}
	front := &wad.Sector{FloorHeight: 0, CeilingHeight: 128, LightLevel: 160}
	back := &wad.Sector{FloorHeight: 24, CeilingHeight: 104, LightLevel: 160}
	b.SetBytes(int64(w * h * 4))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for x := 0; x < w; x++ {
			r.drawWallSpan(x, 0, h, x*3, float64(x*3), 3, tex, nil, sd, pieceMiddle, false, front, back, 220, 160, 0, -1, cam)
		}
	}
}

// BenchmarkFlatSpanOldStyle replicates the pre-refactor drawFlatSpan inner
// loop (per-pixel lightFactor + shade([4]byte) + At([4]byte) + wrapInt) so
// the speedup of the new path is measurable head to head.
func BenchmarkFlatSpanOldStyle(b *testing.B) {
	w, h := 1920, 1080
	r, flat := benchRenderer(w, h)
	cam := Camera{X: 12.5, Y: -40, Z: 41}
	const light = 160
	b.SetBytes(int64(w * h * 4))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for x := 0; x < w; x++ {
			zf := (0 - cam.Z) * r.focal
			s, c := r.sinA, r.cosA
			horizon := float64(r.Height)/2 + r.pitchShear
			halfW := float64(r.Width) / 2
			colFactor := (float64(x) + 0.5 - halfW) / r.focal
			kx := c + colFactor*s
			ky := s - colFactor*c
			tpu := flat.TexelsPerUnit
			fw, fh := flat.Width, flat.Height
			cell := x
			for y := 0; y < h; y++ {
				denom := horizon - (float64(y) + 0.5)
				depth := zf / denom
				if denom == 0 || depth <= 0 {
					cell += r.Width
					continue
				}
				fx := wrapInt(int(math.Floor((cam.X+depth*kx)*tpu)), fw)
				fy := wrapInt(int(math.Floor((cam.Y+depth*ky)*tpu)), fh)
				px := shade(flat.At(fx, fy), lightFactor(light, depth))
				j := cell * 4
				r.Pix[j], r.Pix[j+1], r.Pix[j+2], r.Pix[j+3] = px[0], px[1], px[2], px[3]
				r.depth[cell] = float32(depth)
				cell += r.Width
			}
		}
	}
}

func BenchmarkLightFactorLUT(b *testing.B) {
	var acc float64
	for i := 0; i < b.N; i++ {
		acc += lightFactor(int16(i&255), float64(i%1500))
	}
	_ = acc
}
