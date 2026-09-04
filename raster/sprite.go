package raster

import (
	"math"

	"twopointfive/assets"
)

// blitBillboard draws sp as a camera-facing billboard. (wx, wy, wz) is the
// world point the sprite's hotspot (OffsetX/OffsetY) maps to; lift raises
// the whole sprite by that many map units first (DrawThing uses it to seat
// a ground thing's feet exactly on the floor — see there). Perspective-
// scaled by distance (1 texel = 1 map unit at native size, TexelsPerUnit
// dividing out a hi-res override), optionally mirrored, and depth-tested
// per pixel against r.depth so it's hidden behind level geometry.
//
// Lighting mirrors id's R_ProjectSprite: in the vanilla path the whole
// billboard is darkened by one multiplier from spriteShade(sectorLight,
// depth) — its sector's scalelight row at its distance, the same table and
// index the walls use — so a thing shades to match the geometry around it.
// emissive (a glowing projectile / explosion) or fullBright (an FF_FULLBRIGHT
// frame — a lit powerup, a monster attack flash) skip it. In the enhanced
// G-buffer path nothing is shaded here; the real sector light is written to
// LightParam for the GPU pass instead.
//
// Sampling follows r.spriteFilter: nearest keeps the original hard-pixel
// look; bilinear/trilinear read a premultiplied (mip) chain and composite
// the result with a normal "over" so silhouettes anti-alias instead of
// stair-stepping (see spritemips.go).
func (r *Renderer) blitBillboard(cam Camera, wx, wy, wz, lift, sizeMultiplier float64, sp *assets.RGBA, flip, emissive bool, sectorLight int16, fullBright bool, band draw2DCtx) {
	depth, horiz := r.toCameraSpace(&cam, wx, wy)
	if depth < nearPlane {
		return
	}
	scale := r.focal / depth * sizeMultiplier

	// Constant across the billboard (uniform scale, single depth), so it's
	// one lookup for the whole sprite — exactly how drawWallSpan holds one
	// multiplier for a wall column. 256 == full bright, applied as c*sm>>8.
	sm := uint32(256)
	if !r.gbuf && !emissive {
		sm = spriteShade(sectorLight, depth, fullBright)
	}
	shaded := sm < 256

	centerX := float64(r.Width)/2 + horiz/depth*r.focal
	centerY := r.horizonY() - (wz+lift-cam.Z)/depth*r.focal

	t := tpu(sp)
	w := maxInt(1, int(float64(sp.Width)/t*scale))
	h := maxInt(1, int(float64(sp.Height)/t*scale))
	x0 := int(centerX - float64(sp.OffsetX)/t*scale)
	y0 := int(centerY - float64(sp.OffsetY)/t*scale)

	// Clamp the sprite's screen columns to this worker's band (and thereby
	// the frame) once, rather than testing every pixel. band is {0, r.Width}
	// on the single-threaded path.
	sxLo, sxHi := 0, w
	if lo := band.xlo - x0; lo > sxLo {
		sxLo = lo
	}
	if hi := band.xhi - x0; hi < sxHi {
		sxHi = hi
	}
	if sxLo >= sxHi {
		return
	}

	dpth := float32(depth)

	// G-buffer stamp for the enhanced lighting pass: a billboard normal
	// (horizontal, pointing back at the camera) and the thing's real sector
	// light level, so the GPU's sector/distance fade darkens it to match the
	// geometry around it just as the vanilla path's sm does. A fullbright
	// frame pins that to 255 (the fade is then a no-op, but dynamic lights
	// still land). Emissive sprites (muzzle-flash / explosion / projectile)
	// pass straight through unlit. Material key lives in Normal's alpha:
	// 128 = sprite (wrap-lambert), 255 = emissive.
	gb := r.gbuf
	var pnx, pny, pmat, plight byte
	if gb {
		if inv := 1 / math.Hypot(cam.X-wx, cam.Y-wy); !math.IsInf(inv, 0) {
			pnx = byte(((cam.X-wx)*inv*0.5 + 0.5) * 255)
			pny = byte(((cam.Y-wy)*inv*0.5 + 0.5) * 255)
		} else {
			pnx, pny = 128, 128
		}
		pmat = 128
		plight = byte(clampInt(int(sectorLight), 0, 255))
		if fullBright {
			plight = 255
		}
		if emissive {
			pmat = 255
		}
	}
	writeGBuf := func(i, cell int) {
		r.Normal[i], r.Normal[i+1], r.Normal[i+2], r.Normal[i+3] = pnx, pny, 128, pmat
		r.LightParam[i], r.LightParam[i+1], r.LightParam[i+2], r.LightParam[i+3] = plight, 0, 0, 255
		r.depth[cell] = dpth
	}

	if r.spriteFilter == filterNearest {
		for sy := 0; sy < h; sy++ {
			dy := y0 + sy
			if dy < 0 || dy >= r.Height {
				continue
			}
			srcY := sy * sp.Height / h
			for sx := sxLo; sx < sxHi; sx++ {
				dx := x0 + sx
				cell := dy*r.Width + dx
				if dpth >= r.depth[cell] {
					continue // a wall or flat is nearer here
				}
				srcX := sx * sp.Width / w
				if flip {
					srcX = sp.Width - 1 - srcX
				}
				c := sp.At(srcX, srcY)
				if c[3] == 0 {
					continue
				}
				i := cell * 4
				if shaded {
					r.Pix[i] = byte(uint32(c[0]) * sm >> 8)
					r.Pix[i+1] = byte(uint32(c[1]) * sm >> 8)
					r.Pix[i+2] = byte(uint32(c[2]) * sm >> 8)
					r.Pix[i+3] = c[3]
				} else {
					r.Pix[i], r.Pix[i+1], r.Pix[i+2], r.Pix[i+3] = c[0], c[1], c[2], c[3]
				}
				if gb {
					writeGBuf(i, cell)
				}
			}
		}
		return
	}

	// Filtered (bilinear / trilinear). LOD is constant across the sprite
	// since a billboard scales uniformly: ratio is source texels per screen
	// pixel; >1 means we're minifying and want a coarser mip.
	mc := r.mipsFor(sp)
	lod := 0.0
	if r.spriteFilter == filterTrilinear {
		if ratio := float64(sp.Width) / float64(w); ratio > 1 {
			lod = math.Log2(ratio)
		}
	}

	invW, invH := 1.0/float64(w), 1.0/float64(h)
	for sy := 0; sy < h; sy++ {
		dy := y0 + sy
		if dy < 0 || dy >= r.Height {
			continue
		}
		v := (float64(sy) + 0.5) * invH
		for sx := sxLo; sx < sxHi; sx++ {
			dx := x0 + sx
			cell := dy*r.Width + dx
			if dpth >= r.depth[cell] {
				continue // a wall or flat is nearer here
			}
			u := (float64(sx) + 0.5) * invW
			if flip {
				u = 1 - u
			}
			// Premultiplied sample: sr/sg/sb already carry coverage.
			sr, sg, sb, sa := mc.sampleTrilinear(u, v, lod)
			if sa < 1 {
				continue // effectively transparent
			}
			if shaded {
				// Premultiplied, so scaling the colour channels darkens the
				// sprite without touching its coverage/alpha.
				f := float64(sm) / 256
				sr, sg, sb = sr*f, sg*f, sb*f
			}
			i := cell * 4
			inv := 1 - sa/255
			r.Pix[i] = clampByte(sr + float64(r.Pix[i])*inv)
			r.Pix[i+1] = clampByte(sg + float64(r.Pix[i+1])*inv)
			r.Pix[i+2] = clampByte(sb + float64(r.Pix[i+2])*inv)
			r.Pix[i+3] = 255
			if gb && sa >= 128 {
				// Solidly-covered pixel: it's the sprite here. Leave the
				// thin sub-128 anti-aliased fringe reading the wall behind
				// it, so the lighting pass doesn't halo the silhouette.
				writeGBuf(i, cell)
			}
		}
	}
}

// clampByte rounds and clamps a 0..255-ish float to a byte.
func clampByte(v float64) byte {
	if v <= 0 {
		return 0
	}
	if v >= 255 {
		return 255
	}
	return byte(v + 0.5)
}

// DrawWorldSprite draws a single named sprite lump as a billboard —
// used for the legacy in-flight projectile / explosion path, which has no
// rotations. sizeMultiplier scales beyond native size (still perspective-
// scaled on top).
func (r *Renderer) DrawWorldSprite(cam Camera, wx, wy, wz, sizeMultiplier float64, name string) {
	r.drawWorldSprite(cam, wx, wy, wz, sizeMultiplier, name, r.fullBand())
}

func (r *Renderer) drawWorldSprite(cam Camera, wx, wy, wz, sizeMultiplier float64, name string, band draw2DCtx) {
	if sp, ok := r.textures.Sprite(name); ok {
		// The projectile / explosion path — these glow, so they light the
		// scene rather than being lit by it: emissive (sectorLight/fullBright
		// are unused for an emissive sprite).
		r.blitBillboard(cam, wx, wy, wz, 0, sizeMultiplier, sp, false, true, 0, false, band)
	}
}

// DrawThing draws a map object at (wx, wy, wz) where wz is the thing's Z
// (its feet, for a ground monster/item). It picks the sprite rotation from
// the object's facing vs. the view (id's R_ProjectSprite — rotation 0 is
// facing the camera, 4 facing away), then seats the sprite so its bottom
// edge sits exactly on wz: Doom art has topoffset a few pixels short of the
// sprite height, an overlap that's invisible at 320x200 but pokes the feet
// through the floor at a high render resolution, so the shortfall is added
// back as lift. prefix is the 4-letter sprite name, frame the 0-based
// animation frame. sectorLight is the light level of the sector the thing
// stands in (engine looks it up); fullBright is set for an FF_FULLBRIGHT
// frame (a lit powerup, a monster's attack flash) — see blitBillboard.
func (r *Renderer) DrawThing(cam Camera, wx, wy, wz, thingAngle float64, sectorLight int16, fullBright bool, prefix string, frame int) {
	r.drawThing(cam, wx, wy, wz, thingAngle, sectorLight, fullBright, prefix, frame, r.fullBand())
}

func (r *Renderer) drawThing(cam Camera, wx, wy, wz, thingAngle float64, sectorLight int16, fullBright bool, prefix string, frame int, band draw2DCtx) {
	rot := spriteRotation(cam.X, cam.Y, wx, wy, thingAngle)
	sp, flip, ok := r.textures.SpriteFrame(prefix, frame, rot)
	if !ok {
		return
	}
	r.drawThingSprite(cam, &SceneThing{
		X: wx, Y: wy, Z: wz, Light: sectorLight, FullBright: fullBright,
		sprite: sp, spriteFlip: flip,
	}, band)
}

// drawThingSprite billboards an already-resolved sprite frame (t.sprite /
// t.spriteFlip) — the split lets DrawThings decode frames serially and splat
// them in parallel. Seats the sprite so its bottom edge sits on t.Z: Doom
// art has topoffset a few pixels short of the sprite height, an overlap
// invisible at 320x200 but poking the feet through the floor at a high
// render resolution, so the shortfall is added back as lift.
func (r *Renderer) drawThingSprite(cam Camera, t *SceneThing, band draw2DCtx) {
	sp := t.sprite
	tt := tpu(sp)
	lift := float64(sp.Height)/tt - float64(sp.OffsetY)/tt
	if lift < 0 {
		lift = 0 // topoffset already puts the bottom at/above the feet (hanging/floating art)
	}
	r.blitBillboard(cam, t.X, t.Y, t.Z, lift, 1, sp, t.spriteFlip, false, t.Light, t.FullBright, band)
}

// ThingSprite resolves a map object at world (wx, wy) with facing
// thingAngle, viewed from cam, to the billboard bitmap the software renderer
// would draw (the correct 0..7 rotation), whether it's U-mirrored, and the
// vertical seat "lift" drawThingSprite applies so art whose topoffset falls
// short of the sprite height doesn't sink through the floor at high render
// resolution. ok is false when the frame has no art. The hardware-geometry
// path (render/worldgeo.AddSprites) uses this so its billboards match.
func (r *Renderer) ThingSprite(cam Camera, wx, wy, thingAngle float64, prefix string, frame int) (sp *assets.RGBA, flip bool, lift float64, ok bool) {
	rot := spriteRotation(cam.X, cam.Y, wx, wy, thingAngle)
	sp, flip, ok = r.textures.SpriteFrame(prefix, frame, rot)
	if !ok || sp == nil {
		return nil, false, 0, false
	}
	tt := tpu(sp)
	lift = float64(sp.Height)/tt - float64(sp.OffsetY)/tt
	if lift < 0 {
		lift = 0
	}
	return sp, flip, lift, true
}

// spriteRotation picks the 0..7 sprite rotation to show for a thing at
// (tx, ty) facing thingAngle (radians), seen from (vx, vy) — id's
// R_ProjectSprite quantisation: rotation 0 is the thing facing the viewer,
// 4 is facing away, increasing counter-clockwise.
func spriteRotation(vx, vy, tx, ty, thingAngle float64) int {
	rel := math.Atan2(ty-vy, tx-vx) - thingAngle
	deg := math.Mod(rel*180/math.Pi, 360)
	if deg < 0 {
		deg += 360
	}
	return int(math.Floor((deg+202.5)/45.0)) & 7
}
