package worldgeo

import (
	"math"

	"twopointfive/assets"
	"twopointfive/assets/models"
)

// ModelInstance is one textured 3D model part to draw this frame (a map
// object or projectile drawn from a MODELDEF model pack — see package
// assets/models). The backend draws Mesh (an unindexed triangle list,
// models.FloatsPerVertex floats per vertex) with Skin, transformed by
// Model (model space -> world, column-major), shaded like a voxel model.
type ModelInstance struct {
	Mesh *models.FrameMesh
	// Next, when set, is the frame this part is blending toward (the next
	// state's frame of the same model), Lerp how far along (0..1) — GZDoom's
	// model frame interpolation. nil = draw Mesh as-is.
	Next       *models.FrameMesh
	Lerp       float32
	Skin       *assets.RGBA
	Model      [16]float32
	Light      float32 // sector light 0..1
	FullBright bool
	// Additive > 0 draws the part with ZDoom's RenderStyle "Add" at this
	// alpha (no depth write, one:one blend) — a fireball's glow. 0 = solid.
	Additive float32
	// Alpha in (0,1) draws the part as translucent glass at that opacity
	// (the powerup spheres — engine/renderstyle.go); 0 = solid. Ignored
	// when Additive is set.
	Alpha float32
}

// ModelTransform describes where and how a model part sits in the world.
type ModelTransform struct {
	X, Y, Z          float64    // the thing's position (feet)
	Yaw, Pitch, Roll float64    // radians; pitch > 0 = nose up
	Scale            [3]float64 // model units -> map units, per model axis
	Offset           [3]float64 // map units, in the thing's frame (x forward, y left, z up)
	SpinAxis         [3]float64 // model-space axis of a continuous spin (zero = none)
	Spin             float64    // spin angle, radians
}

// ModelMatrix builds the model -> world matrix for t, applied right to
// left: scale the model, spin it about its own axis, then roll, pitch and
// yaw it (x forward, z up — MD2/MD3/DMD's own convention matches Doom's
// angle 0 = +X), then move it to the thing plus its offset.
func ModelMatrix(t ModelTransform) [16]float32 {
	m := mat3{{t.Scale[0], 0, 0}, {0, t.Scale[1], 0}, {0, 0, t.Scale[2]}}
	if l := math.Sqrt(t.SpinAxis[0]*t.SpinAxis[0] + t.SpinAxis[1]*t.SpinAxis[1] + t.SpinAxis[2]*t.SpinAxis[2]); l > 0 && t.Spin != 0 {
		m = axisAngle(t.SpinAxis[0]/l, t.SpinAxis[1]/l, t.SpinAxis[2]/l, t.Spin).mul(m)
	}
	// Roll and pitch are usually zero (only a projectile pitches, only a
	// MODELDEF offset rolls) — skip those rotations rather than multiply
	// by an identity, and build the yaw directly.
	if t.Roll != 0 {
		m = axisAngle(1, 0, 0, t.Roll).mul(m)
	}
	if t.Pitch != 0 {
		m = axisAngle(0, 1, 0, -t.Pitch).mul(m) // rotating about +Y by -pitch lifts +X (the nose)
	}
	c, s := math.Cos(t.Yaw), math.Sin(t.Yaw)
	yaw := mat3{{c, -s, 0}, {s, c, 0}, {0, 0, 1}}
	m = yaw.mul(m)
	off := yaw.apply(t.Offset)
	return mat4{
		m[0][0], m[0][1], m[0][2], t.X + off[0],
		m[1][0], m[1][1], m[1][2], t.Y + off[1],
		m[2][0], m[2][1], m[2][2], t.Z + off[2],
		0, 0, 0, 1,
	}.colMajor()
}

// mat3 is a row-major 3x3 rotation/scale.
type mat3 [3][3]float64

func (a mat3) mul(b mat3) mat3 {
	var o mat3
	for r := 0; r < 3; r++ {
		for c := 0; c < 3; c++ {
			o[r][c] = a[r][0]*b[0][c] + a[r][1]*b[1][c] + a[r][2]*b[2][c]
		}
	}
	return o
}

func (a mat3) apply(v [3]float64) [3]float64 {
	return [3]float64{
		a[0][0]*v[0] + a[0][1]*v[1] + a[0][2]*v[2],
		a[1][0]*v[0] + a[1][1]*v[1] + a[1][2]*v[2],
		a[2][0]*v[0] + a[2][1]*v[1] + a[2][2]*v[2],
	}
}

// axisAngle is the rotation by angle radians about the unit axis (x, y, z)
// (Rodrigues' formula).
func axisAngle(x, y, z, angle float64) mat3 {
	c, s := math.Cos(angle), math.Sin(angle)
	k := 1 - c
	return mat3{
		{c + x*x*k, x*y*k - z*s, x*z*k + y*s},
		{y*x*k + z*s, c + y*y*k, y*z*k - x*s},
		{z*x*k - y*s, z*y*k + x*s, c + z*z*k},
	}
}

// AddModels hands this frame's model parts to the backend.
func (b *Builder) AddModels(insts []ModelInstance) {
	b.g.Models = append(b.g.Models[:0], insts...)
}
