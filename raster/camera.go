package raster

import "math"

// Camera is the viewer's position and orientation in map space, in the same
// units and axes as the WAD's own VERTEXES/SECTORS data (so a Camera can be
// built directly from a THINGS entry's X/Y/Angle plus a sector's floor
// height + eye offset).
type Camera struct {
	X, Y, Z float64
	// Angle is radians, 0 = facing +X (map "east"), increasing
	// counter-clockwise — Doom's own map-space angle convention.
	Angle float64
	// Pitch is radians of look up(+)/down(-). The original 1993 engine had
	// no vertical look at all; Pitch is rendered the way source ports that
	// later added mouselook did it (and still do, in their software
	// renderers) — as a vertical shear of the projection rather than true
	// 3D camera rotation, since walls/flats are still projected in the
	// horizontal plane only. See raster.Renderer's pitchShear and
	// MaxPitch for the details and the reason it's clamped.
	Pitch float64
	// FOVDeg is the horizontal field of view in degrees. id's original
	// renderer used 90°; kept as the default here too (see New in
	// renderer.go).
	FOVDeg float64
}

// MaxPitch is how far Camera.Pitch may go in either direction. The
// shear-based look-up/down approximation (see Pitch's doc comment)
// increasingly distorts floors/ceilings past this point — the same
// practical limit classic Doom source ports impose on their software
// renderers' freelook for the same reason.
const MaxPitch = 32 * math.Pi / 180
