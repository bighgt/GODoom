package raster

// Camera is the viewer's position and orientation in map space, in the same
// units and axes as the WAD's own VERTEXES/SECTORS data (so a Camera can be
// built directly from a THINGS entry's X/Y/Angle plus a sector's floor
// height + eye offset).
type Camera struct {
	X, Y, Z float64
	// Angle is radians, 0 = facing +X (map "east"), increasing
	// counter-clockwise — Doom's own map-space angle convention.
	Angle float64
	// Pitch is radians of look up(+)/down(-), rendered as a true 3D camera
	// rotation — see worldgeo.CameraBasis and worldgeo.MaxPitch.
	Pitch float64
	// FOVDeg is the horizontal field of view in degrees. id's original
	// renderer used 90°; kept as the default here too.
	FOVDeg float64
}
