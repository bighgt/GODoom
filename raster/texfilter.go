package raster

import (
	"math"

	"twopointfive/assets"
	"twopointfive/wad"
)

// Unified world-texture filtering (config.json's "textureQuality"). One
// global setting drives every surface — walls, flats, sprites, voxels:
//
//	tqNearest    classic hard pixels, no mips (the vanilla look; fastest)
//	tqBilinear   smooth within the base level
//	tqTrilinear  bilinear + a box-filtered mip chain picked by on-screen scale
//	tqAniso      trilinear + up to maxAniso samples along the footprint's
//	             major axis, so a surface seen edge-on (a long wall, a floor
//	             receding to the horizon) stays sharp instead of blurring
//
// Walls and flats go through the texMips + sample* helpers below (straight,
// wrap-addressed RGBA). Sprites keep their own premultiplied mip chain
// (spritemips.go) since they composite with alpha; SetTextureQuality just
// points r.spriteFilter at the matching mode. Voxels have no texture, so
// for them anything but tqNearest simply means "antialias the splats".
type texQuality int

const (
	tqNearest texQuality = iota
	tqBilinear
	tqTrilinear
	tqAniso
)

// parseTexQuality maps a config keyword to (mode, maxAnisoTaps). The tap
// count is 1 for every non-aniso mode. Unknown -> trilinear.
func parseTexQuality(name string) (texQuality, int) {
	switch name {
	case "nearest":
		return tqNearest, 1
	case "bilinear":
		return tqBilinear, 1
	case "aniso2x":
		return tqAniso, 2
	case "aniso4x":
		return tqAniso, 4
	case "aniso8x":
		return tqAniso, 8
	case "aniso16x":
		return tqAniso, 16
	default: // "trilinear" and anything unrecognised
		return tqTrilinear, 1
	}
}

// SetTextureQuality selects the global texture filter (config.json's
// textureQuality) and fans it out to the per-surface knobs the inner loops
// read. Anything unrecognised falls back to trilinear.
func (r *Renderer) SetTextureQuality(name string) {
	r.texQ, r.maxAniso = parseTexQuality(name)
	switch r.texQ {
	case tqNearest:
		r.spriteFilter, r.voxelSmooth = filterNearest, false
	case tqBilinear:
		r.spriteFilter, r.voxelSmooth = filterBilinear, true
	default: // trilinear / aniso — a camera-facing billboard has an isotropic
		// footprint, so anisotropic taps collapse to trilinear for sprites.
		r.spriteFilter, r.voxelSmooth = filterTrilinear, true
	}
}

// texMips is a straight (NOT premultiplied) RGBA8 mip chain for one
// wall/flat texture: level 0 is a copy of the source, each level a
// box-filtered halving down to 1x1. The box filter is alpha-weighted so a
// transparent texel in a 2-sided middle texture (a grate, a fence) doesn't
// bleed black into its neighbours; coverage is kept in alpha.
type texMips struct {
	lv []texLevel
}

type texLevel struct {
	pix  []byte
	w, h int
}

// worldMips returns t's cached mip chain, building it on first use. Never
// built while r.texQ == tqNearest (that path samples the source directly).
func (r *Renderer) worldMips(t *assets.RGBA) *texMips {
	r.worldMipsMu.Lock()
	defer r.worldMipsMu.Unlock()
	if m, ok := r.worldMipsCache[t]; ok {
		return m
	}
	m := buildTexMips(t)
	r.worldMipsCache[t] = m
	return m
}

func buildTexMips(t *assets.RGBA) *texMips {
	base := texLevel{pix: append([]byte(nil), t.Pix...), w: t.Width, h: t.Height}
	if base.w < 1 {
		base.w = 1
	}
	if base.h < 1 {
		base.h = 1
	}
	m := &texMips{lv: []texLevel{base}}
	for cur := base; cur.w > 1 || cur.h > 1; {
		cur = boxHalve(cur)
		m.lv = append(m.lv, cur)
	}
	return m
}

// boxHalve alpha-weighted-averages src down to half size on each axis
// (rounded up). A fully transparent 2x2 stays transparent with rgb 0.
func boxHalve(s texLevel) texLevel {
	nw, nh := (s.w+1)/2, (s.h+1)/2
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	d := texLevel{pix: make([]byte, nw*nh*4), w: nw, h: nh}
	for y := 0; y < nh; y++ {
		for x := 0; x < nw; x++ {
			var rr, gg, bb, aw, n uint32
			for dy := 0; dy < 2; dy++ {
				sy := y*2 + dy
				if sy >= s.h {
					continue
				}
				for dx := 0; dx < 2; dx++ {
					sx := x*2 + dx
					if sx >= s.w {
						continue
					}
					p := (sy*s.w + sx) * 4
					a := uint32(s.pix[p+3])
					rr += uint32(s.pix[p]) * a
					gg += uint32(s.pix[p+1]) * a
					bb += uint32(s.pix[p+2]) * a
					aw += a
					n++
				}
			}
			o := (y*nw + x) * 4
			if aw > 0 {
				d.pix[o] = byte(rr / aw)
				d.pix[o+1] = byte(gg / aw)
				d.pix[o+2] = byte(bb / aw)
			}
			if n > 0 {
				d.pix[o+3] = byte(aw / n)
			}
		}
	}
	return d
}

// bilerpLevel samples chain level k at texel coordinate (u, v) — given in
// LEVEL-0 texel units — with wrap addressing, returning straight RGBA as
// 0..255 floats. The coordinate is rescaled to level k by that level's own
// dimensions (exact for the power-of-two case, a sub-texel drift at coarse
// levels for the odd composite-texture heights).
func (m *texMips) bilerpLevel(k int, u, v float64) (r, g, b, a float64) {
	l := m.lv[k]
	l0 := m.lv[0]
	uk := u*float64(l.w)/float64(l0.w) - 0.5
	vk := v*float64(l.h)/float64(l0.h) - 0.5
	u0 := int(math.Floor(uk))
	v0 := int(math.Floor(vk))
	tu := uk - float64(u0)
	tv := vk - float64(v0)
	u0w, u1w := wrapInt(u0, l.w), wrapInt(u0+1, l.w)
	v0w, v1w := wrapInt(v0, l.h), wrapInt(v0+1, l.h)
	p00 := (v0w*l.w + u0w) * 4
	p10 := (v0w*l.w + u1w) * 4
	p01 := (v1w*l.w + u0w) * 4
	p11 := (v1w*l.w + u1w) * 4
	f := func(i int) float64 {
		top := float64(l.pix[p00+i])*(1-tu) + float64(l.pix[p10+i])*tu
		bot := float64(l.pix[p01+i])*(1-tu) + float64(l.pix[p11+i])*tu
		return top*(1-tv) + bot*tv
	}
	return f(0), f(1), f(2), f(3)
}

// trilinear blends the two chain levels bracketing lod (0 = base). lod is
// clamped to the chain.
func (m *texMips) trilinear(u, v, lod float64) (r, g, b, a float64) {
	last := len(m.lv) - 1
	if lod <= 0 || last == 0 {
		return m.bilerpLevel(0, u, v)
	}
	if lod >= float64(last) {
		return m.bilerpLevel(last, u, v)
	}
	lo := int(lod)
	fr := lod - float64(lo)
	r0, g0, b0, a0 := m.bilerpLevel(lo, u, v)
	r1, g1, b1, a1 := m.bilerpLevel(lo+1, u, v)
	return r0 + (r1-r0)*fr, g0 + (g1-g0)*fr, b0 + (b1-b0)*fr, a0 + (a1-a0)*fr
}

// anisoPlan is the part of an anisotropic sample that depends only on the
// footprint, not on where in the texture it lands — so a wall column (whose
// footprint is constant down the column) resolves it once instead of
// recomputing a Log2 + Ceil + several divides for every pixel. See
// planAniso / sampleAnisoPlan.
type anisoPlan struct {
	taps     int
	lod      float64
	majorLen float64 // texels, along the major axis
	mdu, mdv float64 // unit major-axis direction in texel space
	step     float64 // 1/taps, hoisted
}

// planAniso resolves the tap count and mip LOD for a footprint: major/minor
// axis lengths (texels) and the unit major-axis direction (mdu, mdv), with a
// hard cap of maxTaps. A near-isotropic footprint (a billboard, a face-on
// wall) plans a single trilinear tap, which is why one sampler serves every
// surface.
func planAniso(major, minor, mdu, mdv float64, maxTaps int) anisoPlan {
	if maxTaps < 1 {
		maxTaps = 1
	}
	if minor < 1e-6 {
		minor = 1e-6
	}
	if major < minor {
		major = minor
	}
	taps := int(math.Ceil(major / minor))
	if taps < 1 {
		taps = 1
	}
	if taps > maxTaps {
		taps = maxTaps
	}
	// Sample from a level where the minor footprint is ~1 texel, but no
	// finer than what `taps` samples along the major axis can cover.
	lodLen := major / float64(taps)
	if lodLen < minor {
		lodLen = minor
	}
	lod := math.Log2(lodLen)
	if lod < 0 {
		lod = 0
	}
	return anisoPlan{taps: taps, lod: lod, majorLen: major, mdu: mdu, mdv: mdv, step: 1 / float64(taps)}
}

// sampleAnisoPlan takes p.taps trilinear samples spread along the major axis,
// centred at (u, v) in level-0 texel units, and averages them.
func (m *texMips) sampleAnisoPlan(u, v float64, p anisoPlan) (r, g, b, a float64) {
	if p.taps == 1 {
		return m.trilinear(u, v, p.lod)
	}
	var sr, sg, sb, sa float64
	for i := 0; i < p.taps; i++ {
		off := ((float64(i)+0.5)*p.step - 0.5) * p.majorLen
		tr, tg, tb, ta := m.trilinear(u+p.mdu*off, v+p.mdv*off, p.lod)
		sr += tr
		sg += tg
		sb += tb
		sa += ta
	}
	return sr * p.step, sg * p.step, sb * p.step, sa * p.step
}

// aniso is planAniso + sampleAnisoPlan in one call, for the flat path (whose
// footprint changes every row, so there is nothing to hoist).
func (m *texMips) aniso(u, v, major, minor, mdu, mdv float64, maxTaps int) (r, g, b, a float64) {
	return m.sampleAnisoPlan(u, v, planAniso(major, minor, mdu, mdv, maxTaps))
}

// worldFiltering reports whether walls/flats take the filtered
// (mip/aniso) path this frame rather than the classic integer nearest one.
func (r *Renderer) worldFiltering() bool { return r.texQ != tqNearest }

// WarmTextureCache builds the mip chain for every wall texture and flat the
// level uses, up front. Without it the filtered path builds each chain
// lazily on the strip worker that first samples it — several megabytes of
// box-filtering under the cache mutex, mid-frame, the first time the camera
// sees a new room: a visible hitch. A no-op when texQ is "nearest" (no
// mips) or textures aren't wired (tests). Call at level load, after
// SetTextureQuality.
func (r *Renderer) WarmTextureCache(level *wad.Level) {
	if r.texQ == tqNearest || r.textures == nil || level == nil {
		return
	}
	seen := make(map[*assets.RGBA]struct{})
	warm := func(t *assets.RGBA, ok bool) {
		if !ok || t == nil {
			return
		}
		if _, dup := seen[t]; dup {
			return
		}
		seen[t] = struct{}{}
		r.worldMips(t)
	}
	for i := range level.Sidedefs {
		sd := &level.Sidedefs[i]
		warm(r.textures.WallTexture(sd.UpperTexture))
		warm(r.textures.WallTexture(sd.MiddleTexture))
		warm(r.textures.WallTexture(sd.LowerTexture))
	}
	for i := range level.Sectors {
		sec := &level.Sectors[i]
		warm(r.textures.Flat(sec.FloorTexture))
		if sec.CeilingTexture != skyFlatName {
			warm(r.textures.Flat(sec.CeilingTexture))
		}
	}
}
