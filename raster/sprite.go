package raster

// DrawWorldSprite draws a real sprite (a flying projectile, an explosion
// frame, and eventually monsters/items/decorations) as a billboard at the
// world-space point (wx, wy, wz): always facing the camera, scaled by
// distance the same way wall textures are (1 texel = 1 map unit at the
// sprite's own native size, so a 15-texel-wide graphic reads as 15 map
// units wide up close and shrinks with depth exactly like everything
// else the renderer draws), and positioned so the sprite's own hotspot
// (name.OffsetX/OffsetY — see wad.Patch) lands on that projected point.
// Unlike DrawWeapon's viewmodel sprites, ordinary thing/projectile
// sprites use their offsets in exactly this straightforward way; there's
// no pseudo-3D-projection quirk to work around here.
//
// sizeMultiplier scales the sprite beyond its natural 1-texel-per-map-unit
// size (still perspective-scaled by distance on top of that — a
// multiplier doesn't change *whether* a nearer hit reads as bigger than a
// farther one, only the size both ends of that range land on). 1 draws it
// at native size, as e.g. a flying projectile's own sprite should.
//
// Like the rest of this package, there's no depth test against the walls
// Render already drew (see game_design.txt) — a sprite always draws on
// top regardless of what should occlude it.
func (r *Renderer) DrawWorldSprite(cam Camera, wx, wy, wz, sizeMultiplier float64, name string) {
	sp, ok := r.textures.Sprite(name)
	if !ok {
		return
	}

	depth, horiz := toCameraSpace(cam, wx, wy)
	if depth < nearPlane {
		return
	}
	scale := r.focal / depth * sizeMultiplier

	centerX := float64(r.Width)/2 + horiz/depth*r.focal
	centerY := r.horizonY() - (wz-cam.Z)/depth*r.focal

	w := maxInt(1, int(float64(sp.Width)*scale))
	h := maxInt(1, int(float64(sp.Height)*scale))
	x0 := int(centerX - float64(sp.OffsetX)*scale)
	y0 := int(centerY - float64(sp.OffsetY)*scale)

	for sy := 0; sy < h; sy++ {
		dy := y0 + sy
		if dy < 0 || dy >= r.Height {
			continue
		}
		srcY := sy * sp.Height / h
		for sx := 0; sx < w; sx++ {
			dx := x0 + sx
			if dx < 0 || dx >= r.Width {
				continue
			}
			srcX := sx * sp.Width / w
			c := sp.At(srcX, srcY)
			if c[3] == 0 {
				continue
			}
			i := (dy*r.Width + dx) * 4
			r.Pix[i], r.Pix[i+1], r.Pix[i+2], r.Pix[i+3] = c[0], c[1], c[2], c[3]
		}
	}
}
