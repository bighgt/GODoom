package raster

import "twopointfive/assets"

// SceneThing is one map object resolved for drawing this frame: a world
// position + facing + light, and either a voxel Model or (Model == nil) a
// sprite named by Prefix/Frame. The engine builds the whole frame's slice
// and hands it to DrawThings, which fans the draws out across the strip
// workers — the post-world 2D pass is otherwise single-threaded and a
// screenful of big sprites/voxels is the next cost after the wall render.
type SceneThing struct {
	X, Y, Z, Angle float64
	Light          int16
	FullBright     bool

	Model         *assets.VoxelModel // nil -> draw the sprite
	ModelAngleOff float64            // VOXELDEF AngleOffset, degrees

	Prefix string // 4-letter sprite name (voxel fallback + non-voxel things)
	Frame  int

	// Filled by DrawThings' serial pre-pass (assets.SpriteFrame isn't
	// goroutine-safe, so the parallel splat must not resolve sprites): the
	// decoded frame for this thing's view rotation, and whether to mirror it.
	sprite     *assets.RGBA
	spriteFlip bool
}

// DrawThings draws every thing in list, farthest-first is the caller's
// responsibility (the depth buffer makes ordering only matter for the
// translucent-free alpha test, but a back-to-front list still looks best).
// Voxel-vs-sprite is resolved once here (band-independent), then the splats
// are fanned across the strip workers. Returns how many drew as a voxel
// model and how many as a flat sprite, for the caller's diagnostics.
func (r *Renderer) DrawThings(cam Camera, list []SceneThing) (voxels, sprites int) {
	// Serial pre-pass: resolve voxel-or-sprite (the on-screen size test is
	// band-independent) and, for sprites, decode the frame — assets.
	// SpriteFrame lazily writes an unsynchronised cache, so it must not run
	// on the strip workers.
	for i := range list {
		t := &list[i]
		if t.Model != nil && !r.voxelOnScreenOK(cam, t.X, t.Y, t.Model) {
			t.Model = nil // too small / off screen — fall back to the sprite
		}
		if t.Model != nil {
			voxels++
			continue
		}
		rot := spriteRotation(cam.X, cam.Y, t.X, t.Y, t.Angle)
		sp, flip, ok := r.textures.SpriteFrame(t.Prefix, t.Frame, rot)
		if !ok {
			continue // no such sprite — nothing to draw for this thing
		}
		t.sprite, t.spriteFlip = sp, flip
		sprites++
	}

	r.parallel2D(func(c draw2DCtx) {
		for i := range list {
			t := &list[i]
			switch {
			case t.Model != nil:
				r.drawVoxel(cam, t.X, t.Y, t.Z, t.Angle, t.Light, t.FullBright, t.ModelAngleOff, t.Model, c)
			case t.sprite != nil:
				r.drawThingSprite(cam, t, c)
			}
		}
	})
	return voxels, sprites
}
