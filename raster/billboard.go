package raster

// DrawBillboard draws a small square marker at the world-space point
// (wx, wy, wz), always facing the camera and scaled by distance — the
// simplest possible "thing" renderer, used today for projectiles
// (engine.Projectile). It reuses cam/r.focal/r.horizonY() exactly as
// Render (called earlier the same frame with the same cam) already set
// them up, so it's cheap and needs no state of its own.
//
// It does not test against the walls Render already drew — this package
// keeps only a per-column open/closed range (see Renderer.ceilClip /
// floorClip), not a real per-pixel depth buffer, so a billboard currently
// draws on top of everything regardless of whether a nearer wall should
// have hidden it. Good enough for a handful of fast-moving projectiles;
// real depth-tested sprite occlusion is Phase 4 work alongside proper
// THINGS/monster rendering — see game_design.txt.
func (r *Renderer) DrawBillboard(cam Camera, wx, wy, wz, worldSize float64, col [3]byte) {
	depth, horiz := toCameraSpace(cam, wx, wy)
	if depth < nearPlane {
		return
	}

	sx := float64(r.Width)/2 + horiz/depth*r.focal
	sy := r.horizonY() - (wz-cam.Z)/depth*r.focal

	halfPx := worldSize / depth * r.focal / 2
	if halfPx < 1 {
		halfPx = 1
	}

	x0, x1 := int(sx-halfPx), int(sx+halfPx)
	y0, y1 := int(sy-halfPx), int(sy+halfPx)
	for y := y0; y <= y1; y++ {
		if y < 0 || y >= r.Height {
			continue
		}
		for x := x0; x <= x1; x++ {
			if x < 0 || x >= r.Width {
				continue
			}
			i := (y*r.Width + x) * 4
			r.Pix[i], r.Pix[i+1], r.Pix[i+2], r.Pix[i+3] = col[0], col[1], col[2], 255
		}
	}
}
