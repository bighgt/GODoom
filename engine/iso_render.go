package engine

import (
	"math"

	"twopointfive/raster"
	"twopointfive/render/worldgeo"
)

// isoViewerDist is how far along -camAzimuth from each thing the virtual
// viewpoint CollectIsoWorldObjects builds sits — far enough that every
// thing's rotation-frame quantisation (raster.SpriteRotation) matches what
// an actually-infinitely-distant orthographic viewer facing camAzimuth
// would pick, regardless of the thing's own position.
const isoViewerDist = 1 << 20

// SyncIsoWorldGeometry is the isometric front end's (cmd/isoengine) per-
// frame static-mesh sync — the same syncWorldGeometry buildWorldFrame runs
// for the first-person view. Call WorldBuilder.Build, then this frame's
// world objects/sprites, then the returned restore func, so authoritative
// (non-interpolated) sector heights are back before anything else reads
// them.
func (g *Game) SyncIsoWorldGeometry() (restore func()) { return g.syncWorldGeometry() }

// AimTarget is one live, shootable mobj's world position — see
// Game.AimTargetPositions.
type AimTarget struct{ X, Y, Z float64 }

// AimTargetPositions returns the world position of every live, shootable
// mobj (a monster, barrel, or other shootable game object — anything
// MF_SHOOTABLE and not already dead) for an isometric front end's
// crosshair-snap aim assist: project each one to screen space
// (worldgeo.IsoProjectToScreen) and find whichever lands nearest the mouse,
// then aim exactly at its (X, Y) — see IsoAimLocked — instead of whatever
// the mouse's raw ground-plane point happens to be, so a shot the crosshair
// is visually on a target reliably hits it.
func (g *Game) AimTargetPositions() []AimTarget {
	var out []AimTarget
	for _, mo := range g.mobjs {
		if mo.removed || mo.State == S_NULL || mo.Flags&MF_SHOOTABLE == 0 || mo.Health <= 0 {
			continue
		}
		out = append(out, AimTarget{X: mo.X, Y: mo.Y, Z: mo.Z})
	}
	return out
}

// CollectIsoWorldObjects is collectWorldObjects' isometric-viewpoint
// counterpart: the same voxel-vs-billboard resolution for every live map
// object and in-flight projectile, but sprite rotation is picked from
// camAzimuth (the isometric front end's current camera facing) via a
// virtual distant viewpoint, instead of Game.Camera — which
// IsoMovementSystem has repurposed to mean the player's own facing, not a
// render viewpoint. The "clearly behind the view plane" cull
// collectWorldObjects does isn't meaningful for an orthographic top-down
// camera (nothing is "behind" it the way it is for a first-person one), so
// this visits every live mobj. Reuses g.hwSprites/g.hwVoxels backing
// arrays just like collectWorldObjects.
func (g *Game) CollectIsoWorldObjects(camAzimuth float64) ([]worldgeo.Sprite, []worldgeo.VoxelInstance) {
	sprites := g.hwSprites[:0]
	voxels := g.hwVoxels[:0]
	frac := g.renderLerp()
	vscale := g.ModelScale
	if vscale <= 0 {
		vscale = 1
	}
	viewCam := raster.Camera{
		X: math.Cos(camAzimuth) * -isoViewerDist,
		Y: math.Sin(camAzimuth) * -isoViewerDist,
	}

	for _, mo := range g.mobjs {
		if mo.State == S_NULL {
			continue
		}
		name := mobjSpriteName(mo)
		ix, iy, iz, iang := mo.renderState(frac)
		if g.appendMobjVoxel(mo, name, ix, iy, iz, iang, vscale, &voxels, false) {
			continue // a voxel model, or a voxel-only map object with none — never a billboard
		}
		sp, flip, lift, ok := g.Raster.ThingSprite(viewCam, ix, iy, iang, name, mo.Frame)
		if !ok {
			continue
		}
		sprites = append(sprites, worldgeo.Sprite{
			X: float32(ix), Y: float32(iy), Z: float32(iz),
			Lift: float32(lift), SizeMul: 1,
			Tex:        sp,
			Light:      float32(g.spriteSectorLight(ix, iy)) / 255,
			FullBright: mo.Fullbright,
			Flip:       flip,
		})
	}
	for _, p := range g.projectiles {
		name, ok := p.CurrentSprite()
		if !ok {
			continue
		}
		sp, ok := g.Raster.Textures().Sprite(name)
		if !ok || sp == nil {
			continue
		}
		sprites = append(sprites, worldgeo.Sprite{
			X: float32(p.X), Y: float32(p.Y), Z: float32(p.Z),
			Lift: 0, SizeMul: float32(p.SizeMultiplier()),
			Tex:        sp,
			Light:      1,
			FullBright: true,
		})
	}
	g.hwSprites, g.hwVoxels = sprites, voxels
	return sprites, voxels
}
