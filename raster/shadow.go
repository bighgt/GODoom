package raster

import "math"

// Ground (contact) shadows. The voxel/sprite pass draws a thing as a
// camera-facing billboard or a splat with no cast shadow of its own, so a
// monster or barrel can look like it is hovering. DrawGroundShadow stamps a
// soft dark ellipse onto the floor the world pass already drew, directly
// under a thing — a cheap contact shadow that grounds it. It runs in both
// pipelines: in vanilla it darkens the finished pixel, in enhanced it dims
// the albedo a touch and the base sector light more, so the GPU lighting
// pass still lands a shadow there. Screen-space shadows from the dynamic
// lights (a thing blocking a torch) are a separate thing, done in
// light.frag.
//
// Call once per caster after Render (which fills r.depth) and before the
// sprite/voxel pass, so things draw on top of their own shadow.

const shadowMaxDarken = 0.82 // never punch a floor pixel fully black

// SetGroundShadows toggles the contact-shadow pass (config shadows).
func (r *Renderer) SetGroundShadows(on bool) { r.groundShadows = on }

// DrawGroundShadow darkens the floor around (wx, wy) at height floorZ,
// within radius map units, fading out toward the edge. strength is the
// centre opacity (0..1). A no-op when the centre is behind the camera, the
// projected disc is sub-pixel, or shadows are off.
func (r *Renderer) DrawGroundShadow(cam Camera, wx, wy, floorZ, radius, strength float64) {
	if !r.groundShadows || r.focal <= 0 || radius <= 1 || strength <= 0 {
		return
	}
	dc, hc := r.toCameraSpace(&cam, wx, wy)
	if dc <= nearPlane {
		return
	}
	halfW := float64(r.Width) / 2
	horizon := r.horizonY()
	sxc := halfW + hc/dc*r.focal
	syc := horizon - (floorZ-cam.Z)/dc*r.focal
	sr := radius / dc * r.focal
	if sr < 1 {
		return
	}

	x0, x1 := int(sxc-sr-1), int(sxc+sr+1)
	// a ground disc foreshortens vertically; a generous band plus the
	// per-pixel world-distance test below keeps the shape exact.
	y0, y1 := int(syc-sr*1.3-1), int(syc+sr*1.3+1)
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	if x1 >= r.Width {
		x1 = r.Width - 1
	}
	if y1 >= r.Height {
		y1 = r.Height - 1
	}
	if x0 > x1 || y0 > y1 {
		return
	}

	zf := (floorZ - cam.Z) * r.focal
	s, c := r.sinA, r.cosA
	invR := 1.0 / radius
	gb := r.gbuf
	dst := r.Pix
	lp := r.LightParam

	for y := y0; y <= y1; y++ {
		dn := horizon - (float64(y) + 0.5)
		if dn == 0 {
			continue
		}
		depth := zf / dn
		if depth <= nearPlane {
			continue
		}
		row := y * r.Width
		colBase := (0.5 - halfW) / r.focal
		colStep := 1.0 / r.focal
		colF := colBase + float64(x0)*colStep
		for x := x0; x <= x1; x, colF = x+1, colF+colStep {
			cell := row + x
			pd := float64(r.depth[cell])
			// only the floor the world pass drew here — not a nearer wall,
			// not the sky/void backdrop.
			if pd >= math.MaxFloat32 || pd < depth-3 || pd > depth+3 {
				continue
			}
			pxw := cam.X + depth*(c+colF*s)
			pyw := cam.Y + depth*(s-colF*c)
			dx, dy := pxw-wx, pyw-wy
			dd2 := dx*dx + dy*dy
			if dd2 >= radius*radius {
				continue
			}
			t := math.Sqrt(dd2) * invR
			k := strength * (1 - t*t)
			if k <= 0 {
				continue
			}
			if k > shadowMaxDarken {
				k = shadowMaxDarken
			}
			i := cell * 4
			if gb {
				am := 1 - k*0.55
				dst[i] = byte(float64(dst[i]) * am)
				dst[i+1] = byte(float64(dst[i+1]) * am)
				dst[i+2] = byte(float64(dst[i+2]) * am)
				lp[i] = byte(float64(lp[i]) * (1 - k))
			} else {
				m := 1 - k
				dst[i] = byte(float64(dst[i]) * m)
				dst[i+1] = byte(float64(dst[i+1]) * m)
				dst[i+2] = byte(float64(dst[i+2]) * m)
			}
		}
	}
}
