package engine

import (
	"math"

	"twopointfive/bsp"
	"twopointfive/raster"
	"twopointfive/wad"
)

// StartCamera builds the initial raster.Camera from level's Player 1 start
// Thing (falling back to the map origin, facing east, if none is present),
// with its eye height resolved from whichever Sector the BSP tree says that
// point is actually in. Shared by cmd/engine and cmd/isoengine — both start
// a session the same way, just render what follows differently.
func StartCamera(level *wad.Level, tree bsp.Node) raster.Camera {
	const player1Start = 1

	x, y, angleDeg := 0.0, 0.0, 0.0
	for _, t := range level.Things {
		if t.Type == player1Start {
			x, y, angleDeg = float64(t.X), float64(t.Y), float64(t.Angle)
			break
		}
	}

	z := EyeHeight
	if sec := bsp.PointSector(tree, level, float32(x), float32(y)); sec != nil {
		z = float64(sec.FloorHeight) + EyeHeight
	}

	return raster.Camera{X: x, Y: y, Z: z, Angle: angleDeg * math.Pi / 180, FOVDeg: 90}
}
