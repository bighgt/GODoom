package raster

import (
	"math"

	"twopointfive/assets"
)

// Sprite filtering (config spriteFilter). World sprites are billboards
// scaled by distance, so point-sampling them — the vanilla look — shimmers
// badly once a monster or item is more than a few tiles away, especially
// against the smoothly-filtered walls of the enhanced renderer. bilinear
// smooths within a sprite; trilinear additionally blends between two levels
// of a box-filtered mip chain picked from the on-screen scale, which is
// what actually kills the shimmer on distant things.
//
// Everything here works in PREMULTIPLIED alpha. Doom sprites are hard
// cut-outs (alpha 0 or 255); filtering straight RGBA across a silhouette
// averages opaque pixels with transparent (0,0,0,0) ones and darkens the
// edge. Premultiplying first makes a transparent texel contribute nothing
// to either colour or coverage, so edges stay clean and simply fade in
// coverage — which blitBillboard then composites with a normal "over".
const (
	filterNearest = iota
	filterBilinear
	filterTrilinear
)

// mipLevel is one level of a sprite's chain: premultiplied RGBA8, row-major.
type mipLevel struct {
	pix  []byte
	w, h int
}

// mipChain is level 0 (a premultiplied copy of the sprite) followed by
// successive halvings down to 1x1.
type mipChain struct {
	levels []mipLevel
}

// mipsFor returns sp's cached mip chain, building it on first use. The
// sprite/HUD pass that calls this is single-threaded (see
// assets.SpriteFrame's note), but the map is guarded anyway since it's
// cheap and the cost of getting it wrong is a data race.
func (r *Renderer) mipsFor(sp *assets.RGBA) *mipChain {
	r.spriteMipsMu.Lock()
	defer r.spriteMipsMu.Unlock()
	if mc, ok := r.spriteMips[sp]; ok {
		return mc
	}
	mc := buildMipChain(sp)
	r.spriteMips[sp] = mc
	return mc
}

func buildMipChain(sp *assets.RGBA) *mipChain {
	base := mipLevel{pix: make([]byte, len(sp.Pix)), w: sp.Width, h: sp.Height}
	for i := 0; i+3 < len(sp.Pix); i += 4 {
		a := uint32(sp.Pix[i+3])
		base.pix[i] = byte(uint32(sp.Pix[i]) * a / 255)
		base.pix[i+1] = byte(uint32(sp.Pix[i+1]) * a / 255)
		base.pix[i+2] = byte(uint32(sp.Pix[i+2]) * a / 255)
		base.pix[i+3] = sp.Pix[i+3]
	}

	mc := &mipChain{levels: []mipLevel{base}}
	cur := base
	for cur.w > 1 || cur.h > 1 {
		cur = downsample2x(cur)
		mc.levels = append(mc.levels, cur)
	}
	return mc
}

// downsample2x box-filters one premultiplied level to half size on each
// axis (rounded up, so a 1-wide level still steps down its height). Odd
// edges average the 1 or 2 source texels that exist.
func downsample2x(src mipLevel) mipLevel {
	nw, nh := (src.w+1)/2, (src.h+1)/2
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	dst := mipLevel{pix: make([]byte, nw*nh*4), w: nw, h: nh}
	for y := 0; y < nh; y++ {
		for x := 0; x < nw; x++ {
			var r, g, b, a, n uint32
			for dy := 0; dy < 2; dy++ {
				sy := y*2 + dy
				if sy >= src.h {
					continue
				}
				for dx := 0; dx < 2; dx++ {
					sx := x*2 + dx
					if sx >= src.w {
						continue
					}
					s := (sy*src.w + sx) * 4
					r += uint32(src.pix[s])
					g += uint32(src.pix[s+1])
					b += uint32(src.pix[s+2])
					a += uint32(src.pix[s+3])
					n++
				}
			}
			d := (y*nw + x) * 4
			dst.pix[d] = byte(r / n)
			dst.pix[d+1] = byte(g / n)
			dst.pix[d+2] = byte(b / n)
			dst.pix[d+3] = byte(a / n)
		}
	}
	return dst
}

// sampleBilinear reads level lv at (u, v) in [0,1] with clamp-to-edge,
// returning premultiplied RGBA as floats 0..255.
func sampleBilinear(lv mipLevel, u, v float64) (r, g, b, a float64) {
	fx := u*float64(lv.w) - 0.5
	fy := v*float64(lv.h) - 0.5
	x0 := int(math.Floor(fx))
	y0 := int(math.Floor(fy))
	tx := fx - float64(x0)
	ty := fy - float64(y0)

	x0c, x1c := clampInt(x0, 0, lv.w-1), clampInt(x0+1, 0, lv.w-1)
	y0c, y1c := clampInt(y0, 0, lv.h-1), clampInt(y0+1, 0, lv.h-1)

	p00 := (y0c*lv.w + x0c) * 4
	p10 := (y0c*lv.w + x1c) * 4
	p01 := (y1c*lv.w + x0c) * 4
	p11 := (y1c*lv.w + x1c) * 4

	lerp := func(i int) float64 {
		top := float64(lv.pix[p00+i])*(1-tx) + float64(lv.pix[p10+i])*tx
		bot := float64(lv.pix[p01+i])*(1-tx) + float64(lv.pix[p11+i])*tx
		return top*(1-ty) + bot*ty
	}
	return lerp(0), lerp(1), lerp(2), lerp(3)
}

// sampleTrilinear picks two chain levels from lod (0 = level 0, i.e. the
// base) and blends their bilinear samples. lod is clamped to the chain.
func (mc *mipChain) sampleTrilinear(u, v, lod float64) (r, g, b, a float64) {
	if lod <= 0 || len(mc.levels) == 1 {
		return sampleBilinear(mc.levels[0], u, v)
	}
	maxLod := float64(len(mc.levels) - 1)
	if lod >= maxLod {
		return sampleBilinear(mc.levels[len(mc.levels)-1], u, v)
	}
	lo := int(lod)
	frac := lod - float64(lo)
	r0, g0, b0, a0 := sampleBilinear(mc.levels[lo], u, v)
	r1, g1, b1, a1 := sampleBilinear(mc.levels[lo+1], u, v)
	return r0 + (r1-r0)*frac, g0 + (g1-g0)*frac, b0 + (b1-b0)*frac, a0 + (a1-a0)*frac
}
