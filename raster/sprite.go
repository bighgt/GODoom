package raster

import (
	"math"

	"twopointfive/assets"
)

// ThingSprite resolves a map object at world (wx, wy) with facing
// thingAngle, viewed from cam, to the billboard bitmap the hardware
// (GPU-geometry) path draws (the correct 0..7 rotation), whether it's
// U-mirrored, and the vertical seat "lift" that keeps art whose topoffset
// falls short of the sprite height from sinking through the floor at a high
// render resolution. ok is false when the frame has no art.
// render/worldgeo.AddSprites' billboards (see engine/mobj.go's
// collectWorldObjects) come from this.
func (r *Renderer) ThingSprite(cam Camera, wx, wy, thingAngle float64, prefix string, frame int) (sp *assets.RGBA, flip bool, lift float64, ok bool) {
	rot := SpriteRotation(cam.X, cam.Y, wx, wy, thingAngle)
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

// SpriteRotation picks the 0..7 sprite rotation to show for a thing at
// (tx, ty) facing thingAngle (radians), seen from (vx, vy) — id's
// R_ProjectSprite quantisation: rotation 0 is the thing facing the viewer,
// 4 is facing away, increasing counter-clockwise. Exported (beyond this
// package's own ThingSprite) for a caller like cmd/isoengine that resolves
// a thing's billboard sprite without a live raster.Renderer.
func SpriteRotation(vx, vy, tx, ty, thingAngle float64) int {
	rel := math.Atan2(ty-vy, tx-vx) - thingAngle
	deg := math.Mod(rel*180/math.Pi, 360)
	if deg < 0 {
		deg += 360
	}
	return int(math.Floor((deg+202.5)/45.0)) & 7
}
