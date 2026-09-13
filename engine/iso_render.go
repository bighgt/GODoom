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

// SyncIsoWorldGeometry rebuilds Game.WorldBuilder's static geometry (walls/
// flats) if anything that would invalidate it changed since the last call
// — a sector height moved (a door/lift/floor mover), one is mid-glide this
// frame, or an animated flat/wall texture stepped a frame — mirroring
// HardwareMode's own per-frame rebuild-on-change check in renderFrame (see
// game.go's geomChanged/animChanged/restoreSecH). An isometric front end
// (cmd/isoengine) calls this once per frame in renderFrame's place, since
// it doesn't go through renderFrame (a CPU-rasterize-then-present method)
// at all. Call WorldBuilder.Build, then this frame's world objects/sprites,
// then the returned restore func — the same order renderFrame's own
// geomChanged/restoreSecH pair requires, so authoritative (non-interpolated)
// sector heights are back before anything else reads them.
func (g *Game) SyncIsoWorldGeometry() (restore func()) {
	geomChanged := g.worldGeomChanged()
	animChanged := g.animFrameChanged()
	restoreSecH := g.applySectorInterp()
	// See renderFrame's identical gating (game.go) and
	// renderedSectorHeightsChanged's own doc comment (interp.go): a mover
	// mid-glide reinterpolates every render frame, but most of those round
	// to the same int16 height as the one before, so sectorInterpActive
	// alone would force a needless full-level Invalidate + blocking GPU
	// re-upload far more often than the geometry actually looks different.
	heightsChanged := g.renderedSectorHeightsChanged()
	if geomChanged || animChanged || (g.sectorInterpActive && heightsChanged) {
		g.WorldBuilder.Invalidate()
	}
	return restoreSecH
}

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
	vscale := g.VoxelScale
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
		if g.Voxels != nil {
			if model, angOff, mscale, ok := g.Voxels.Model(name, mo.Frame); ok && model != nil {
				if mscale <= 0 {
					mscale = 1
				}
				voxels = append(voxels, worldgeo.VoxelInstance{
					Model: model,
					X:     float32(ix), Y: float32(iy), Z: float32(iz),
					Yaw:        float32(iang + angOff*math.Pi/180 + voxelKVXFrontBias),
					Scale:      float32(vscale * mscale),
					Light:      float32(g.spriteSectorLight(ix, iy)) / 255,
					FullBright: mo.Fullbright,
				})
				if g.heretic {
					if f, ok := g.hereticFlameFor(name, model, ix, iy, iz, vscale*mscale, g.renderClock); ok {
						voxels = append(voxels, f)
					}
				}
				continue // the voxel model replaces the billboard
			}
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
