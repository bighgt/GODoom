package raster

import (
	"math"

	"twopointfive/assets"
)

// Voxel model rendering. A model is drawn as a cloud of screen-space splats
// — one small filled square per solid voxel, projected through the same
// pinhole+pitch-shear camera the walls use and depth-tested per pixel
// against r.depth, so a voxel monster occludes and is occluded by level
// geometry and other things exactly like a sprite. It rotates in 3D with
// the thing's facing (true parallax as you move around it) and is shaded by
// its sector light + distance, matching id's sprite lighting (see
// spriteShade). This replaces the flat billboard for any map object whose
// current sprite frame the loaded voxel pack has a model for.

const (
	// minVoxelHeightPx is the on-screen model height below which DrawVoxel
	// declines (returns false) so the caller draws the flat sprite instead —
	// a distant thing isn't worth the per-voxel cost and reads the same.
	minVoxelHeightPx = 24

	// voxelYawBias rotates a KVX model's built-in "front" to line up with a
	// thing's facing, on top of the per-model VOXELDEF AngleOffset. Cheello's
	// Voxel Doom models are authored a quarter turn off Doom's angle 0, so
	// this backs them 90 degrees clockwise (Doom angle increases CCW, so the
	// bias is negative). Flip the sign for CCW, use math.Pi for a half turn.
	voxelYawBias = -math.Pi / 2

	// voxelSplatGrow enlarges each splat slightly past one voxel so rotated
	// models don't show pinholes between neighbouring splats. The smooth
	// (coverage-antialiased) path needs a touch more so the soft edges of
	// adjacent splats overlap enough to reach full opacity across a seam.
	voxelSplatGrow       = 1.25
	voxelSplatGrowSmooth = 1.35

	// voxelMirrorY flips the model's Y axis. KVX/SLAB6 and Doom map space
	// can disagree on handedness; if models render left-right mirrored (a
	// monster's shoulder patch on the wrong side, text backwards), flip
	// this. Not verified against a running frame — see voxelYawBias.
	voxelMirrorY = false
)

// SetVoxelScale sets how many map units one voxel spans (config voxelScale).
// Cheello's Voxel Doom is authored at 1 unit/voxel; New defaults to that.
func (r *Renderer) SetVoxelScale(mu float64) {
	if mu > 0 {
		r.voxelScale = mu
	}
}

// pixCover is the fraction of the pixel column/row starting at p (i.e. the
// span [p, p+1]) that lies inside [lo, hi]. Used to antialias a splat's
// edges in the smooth path.
func pixCover(p, lo, hi float64) float64 {
	a := p
	if a < lo {
		a = lo
	}
	b := p + 1
	if b > hi {
		b = hi
	}
	if b <= a {
		return 0
	}
	return b - a
}

// DrawVoxel splats voxel model m for a thing standing at world (wx, wy) with
// its feet at wz, facing `angle` radians (angleOffsetDeg from VOXELDEF is
// added). sectorLight / fullBright shade it the same way a sprite is shaded.
// With r.voxelSmooth (config voxelFilter "smooth", the default) each splat's
// edges are coverage-antialiased — its rectangle is treated as a float and
// a partly-covered border pixel is alpha-blended by its coverage fraction —
// so the model's silhouette and the seams between splats soften instead of
// stair-stepping. "nearest" keeps the hard blocky write.
// Returns false without drawing anything if the model would be shorter than
// minVoxelHeightPx on screen or is otherwise not drawable — the caller then
// falls back to raster.DrawThing.
func (r *Renderer) DrawVoxel(cam Camera, wx, wy, wz, angle float64, sectorLight int16, fullBright bool, angleOffsetDeg float64, m *assets.VoxelModel) bool {
	return r.drawVoxel(cam, wx, wy, wz, angle, sectorLight, fullBright, angleOffsetDeg, m, r.fullBand())
}

// voxelOnScreenOK reports whether model m at world (wx,wy) is in front of the
// camera and big enough on screen to be worth splatting (vs. the flat
// sprite). Band-independent, so DrawThings can resolve voxel-vs-sprite once
// before fanning the splat out across workers.
func (r *Renderer) voxelOnScreenOK(cam Camera, wx, wy float64, m *assets.VoxelModel) bool {
	if m == nil || r.focal <= 0 {
		return false
	}
	centerDepth, _ := r.toCameraSpace(&cam, wx, wy)
	if centerDepth < nearPlane {
		return false
	}
	vmu := r.voxelScale
	if vmu <= 0 {
		vmu = 1
	}
	return float64(m.ZSiz)*vmu*r.focal/centerDepth >= minVoxelHeightPx
}

func (r *Renderer) drawVoxel(cam Camera, wx, wy, wz, angle float64, sectorLight int16, fullBright bool, angleOffsetDeg float64, m *assets.VoxelModel, band draw2DCtx) bool {
	if !r.voxelOnScreenOK(cam, wx, wy, m) {
		return false // too small / behind camera — let the sprite path handle it
	}
	centerDepth, _ := r.toCameraSpace(&cam, wx, wy)
	vmu := r.voxelScale
	if vmu <= 0 {
		vmu = 1
	}

	// Constant across the model (single centre depth), like a wall column.
	sm := uint32(256)
	if !r.gbuf && !fullBright {
		sm = spriteShade(sectorLight, centerDepth, false)
	}

	// Screen scale is fixed at the thing's centre depth — an orthographic-
	// style projection — so a voxel model occupies the same footprint as the
	// billboard sprite it replaces. With true per-column perspective the
	// model's near face renders ~20% larger than the sprite for a
	// monster-sized, monster-distanced thing. The model still rotates with
	// the thing's facing (each column's horizontal offset comes from its
	// rotated position) and still occludes correctly in 3D (the depth test
	// below uses each voxel's real camera-space depth).
	invCD := 1.0 / centerDepth

	yaw := angle + angleOffsetDeg*math.Pi/180 + voxelYawBias
	sinY, cosY := math.Sin(yaw), math.Cos(yaw)

	W, H := r.Width, r.Height
	halfW := float64(W) / 2
	horizon := r.horizonY()
	camZ := cam.Z

	// One splat half-extent for the whole model (uniform screen scale).
	halfF := vmu * r.focal * invCD * 0.5
	if r.voxelSmooth {
		halfF *= voxelSplatGrowSmooth
	} else {
		halfF *= voxelSplatGrow
	}
	if halfF < 0.5 {
		halfF = 0.5 // never let a voxel shrink below one pixel
	}

	// G-buffer stamp (enhanced mode): a camera-facing normal, the thing's
	// real sector light, sprite material key — mirrors blitBillboard.
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
	}
	stampGB := func(pix int) {
		r.Normal[pix], r.Normal[pix+1], r.Normal[pix+2], r.Normal[pix+3] = pnx, pny, 128, pmat
		r.LightParam[pix], r.LightParam[pix+1], r.LightParam[pix+2], r.LightParam[pix+3] = plight, 0, 0, 255
	}

	smooth := r.voxelSmooth

	xs, ys := m.XSiz, m.YSiz
	for gx := 0; gx < xs; gx++ {
		lx := (float64(gx) + 0.5 - m.XPivot) * vmu
		for gy := 0; gy < ys; gy++ {
			slabs := m.Slabs(gx, gy)
			if len(slabs) == 0 {
				continue
			}
			ly := (float64(gy) + 0.5 - m.YPivot) * vmu
			if voxelMirrorY {
				ly = -ly
			}

			// Column world (x, y) — rotate the local offset about the model's
			// vertical axis, then offset from the thing's position.
			wxx := wx + lx*cosY - ly*sinY
			wyy := wy + lx*sinY + ly*cosY
			depth, horiz := r.toCameraSpace(&cam, wxx, wyy)
			if depth < nearPlane {
				continue
			}
			// Horizontal position uses the column's rotated offset (so the
			// model turns) but the centre-depth scale (so it stays sprite-
			// sized). depth itself is kept for the per-voxel occlusion test.
			colX := halfW + horiz*invCD*r.focal
			if colX+halfF < float64(band.xlo) || colX-halfF >= float64(band.xhi) {
				continue
			}
			d32 := float32(depth)

			for si := range slabs {
				sl := &slabs[si]
				for i := range sl.Colors {
					gz := sl.ZTop + i
					// KVX z runs downward with the pivot at the base, so
					// height above the feet is (ZPivot - (z+0.5)) voxels.
					hz := (m.ZPivot - (float64(gz) + 0.5)) * vmu
					rowY := horizon - (wz+hz-camZ)*invCD*r.focal

					fx0, fx1 := colX-halfF, colX+halfF
					fy0, fy1 := rowY-halfF, rowY+halfF
					x0, x1 := int(fx0), int(fx1)+1
					y0, y1 := int(fy0), int(fy1)+1
					if x0 < band.xlo {
						x0 = band.xlo
					}
					if x1 >= band.xhi {
						x1 = band.xhi - 1
					}
					if y0 < 0 {
						y0 = 0
					}
					if y1 >= H {
						y1 = H - 1
					}
					if x0 > x1 || y0 > y1 {
						continue
					}

					cr, cg, cb := sl.Colors[i][0], sl.Colors[i][1], sl.Colors[i][2]
					if !fullBright {
						cr, cg, cb = voxFaceShade(sl.Face, cr, cg, cb)
					}
					var or, og, ob byte
					if gb || sm >= 256 {
						or, og, ob = cr, cg, cb
					} else {
						or = byte(uint32(cr) * sm >> 8)
						og = byte(uint32(cg) * sm >> 8)
						ob = byte(uint32(cb) * sm >> 8)
					}
					forr, forg, forb := float64(or), float64(og), float64(ob)

					rowBase := y0*W + x0
					for py := y0; py <= y1; py++ {
						covY := 1.0
						if smooth {
							if covY = pixCover(float64(py), fy0, fy1); covY <= 0 {
								rowBase += W
								continue
							}
						}
						cell := rowBase
						pix := cell * 4
						for px := x0; px <= x1; px++ {
							if d32 >= r.depth[cell] {
								cell++
								pix += 4
								continue
							}
							if !smooth {
								r.Pix[pix], r.Pix[pix+1], r.Pix[pix+2], r.Pix[pix+3] = or, og, ob, 255
								if gb {
									stampGB(pix)
								}
								r.depth[cell] = d32
								cell++
								pix += 4
								continue
							}
							w := covY * pixCover(float64(px), fx0, fx1)
							switch {
							case w >= 0.999:
								r.Pix[pix], r.Pix[pix+1], r.Pix[pix+2], r.Pix[pix+3] = or, og, ob, 255
								if gb {
									stampGB(pix)
								}
								r.depth[cell] = d32
							case w > 1.0/256:
								iv := 1 - w
								r.Pix[pix] = clampByte(forr*w + float64(r.Pix[pix])*iv)
								r.Pix[pix+1] = clampByte(forg*w + float64(r.Pix[pix+1])*iv)
								r.Pix[pix+2] = clampByte(forb*w + float64(r.Pix[pix+2])*iv)
								r.Pix[pix+3] = 255
								if w >= 0.5 {
									r.depth[cell] = d32
									if gb {
										stampGB(pix)
									}
								}
							}
							cell++
							pix += 4
						}
						rowBase += W
					}
				}
			}
		}
	}
	return true
}

// voxFaceShade gives a voxel a little top-lit volume from its KVX
// visible-face bits: an exposed top face full, an exposed bottom face
// darkened, everything else (side faces) a touch down. Kept subtle so it
// reads as form, not a hard cel-shade.
func voxFaceShade(face, r, g, b byte) (byte, byte, byte) {
	var m uint32
	switch {
	case face&0x10 != 0: // top exposed
		return r, g, b
	case face&0x20 != 0: // bottom exposed
		m = 190
	default: // side
		m = 232
	}
	return byte(uint32(r) * m >> 8), byte(uint32(g) * m >> 8), byte(uint32(b) * m >> 8)
}
