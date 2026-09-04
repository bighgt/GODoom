package worldgeo

import "math"

// Camera mirrors raster.Camera's fields (package render must not import
// package raster). Angle is radians, 0 = +X, CCW; Pitch is look up(+) /
// down(-) as a projection shear, not a true 3D rotation; FOVDeg is the
// horizontal field of view at the 16:10 reference aspect.
type Camera struct {
	X, Y, Z float64
	Angle   float64
	Pitch   float64
	FOVDeg  float64
	// Time is a free-running wall-clock seconds value for animated surface
	// effects (water ripple). Presentation only; 0 is a valid "unset" =
	// static.
	Time float64

	// Distance fog (config fogColor / fogDensity): FogR/G/B is the colour
	// 0..1, FogDensity the exponential strength. FogDensity == 0 disables it.
	FogR, FogG, FogB float32
	FogDensity       float32

	// ShadowCasterN is how many of this frame's leading lights (the muzzle
	// flash, in-flight shots, monster attacks, then the nearest map
	// emitters — same order as the light array) the present pass traces a
	// screen-space shadow ray for, so torches and weapon fire throw real
	// occlusion from world geometry and monsters. 0 = none this frame.
	ShadowCasterN int

	// ScreenTint is a full-frame colour wash the present pass applies after
	// tonemap — the player standing in lava / nukage / blood (id's palette
	// flash). RGB 0..1, A = strength (0 = no tint). A negative A additionally
	// asks for a subtle heat-haze wobble (lava).
	TintR, TintG, TintB, TintA float32

	// GroundShadows is this frame's contact-shadow casters (grounded things),
	// each {worldX, worldY, floorZ, radius}. world.frag stamps a soft dark
	// blob on the floor under each so nothing reads as hovering. Backed by a
	// reused engine buffer; the backend copies what it needs immediately.
	GroundShadows [][4]float32

	// Spot is the player's head-mounted flashlight — see render.Frame's
	// identical fields (the two render tiers duplicate this the same way
	// they already duplicate FogR/FogG/FogB/FogDensity). SpotIntensity <= 0
	// means off.
	SpotX, SpotY, SpotZ        float32
	SpotDX, SpotDY, SpotDZ     float32
	SpotR, SpotG, SpotB        float32
	SpotIntensity              float32
	SpotRadius                 float32
	SpotCosOuter, SpotCosInner float32
}

// Projection constants — kept byte-identical to raster so a hardware render
// lands the geometry exactly where the software rasterizer would.
const (
	// refAspect is raster's InternalWidth/InternalHeight (320/200): the FOV
	// is specified at this aspect and the vertical FOV is held constant as
	// the real aspect widens (Hor+ widescreen).
	refAspect = 320.0 / 200.0
	nearPlane = 1.0
	farPlane  = 65536.0 // map units; a Doom level fits inside ±32k
)

// NearPlane / FarPlane are ViewProj's clip planes, exported so a backend
// that samples the world pass depth buffer (hardware SSR) can linearise a
// stored NDC-z value with the exact same near/far this projection used.
const (
	NearPlane = nearPlane
	FarPlane  = farPlane
)

// focalFor is raster.Renderer's focal length for a render height h and a
// horizontal FOV: the vertical FOV (constant across aspects) drives it.
func focalFor(fovDeg float64, h int) float64 {
	hRef := fovDeg * math.Pi / 180
	vFOV := 2 * math.Atan(math.Tan(hRef/2)/refAspect)
	return float64(h) / 2 / math.Tan(vFOV/2)
}

// FocalFor is the focal length (pixels) ViewProj uses for cam at render
// height h — the backend needs it for the sky's per-column view angle
// (world.frag skyColor), matching raster/sky.go's own focal.
func FocalFor(cam Camera, h int) float64 { return focalFor(cam.FOVDeg, h) }

// ViewProj returns the column-major (GLSL mat4) view-projection matrix for
// cam and a w x h render target. World-space geometry (worldgeo.Vert, Z
// up, map units) transformed by it lands in Vulkan clip space: x right,
// y DOWN, z in [0,1], w = camera-forward distance (so the perspective
// divide gives the same screen position raster.Renderer.toCameraSpace +
// its projection produce). No model matrix — the geometry is already world
// space.
//
// The pitch "shear" is a post-divide constant Y offset (raster adds it to
// horizonY after dividing by depth), encoded here as the w->y term of the
// projection.
func ViewProj(cam Camera, w, h int) [16]float32 {
	return viewProjRM(cam, w, h).colMajor()
}

// viewProjRM is ViewProj's row-major matrix before the column-major
// conversion — so VoxelMVP can premultiply a model matrix into it.
func viewProjRM(cam Camera, w, h int) mat4 {
	focal := focalFor(cam.FOVDeg, h)
	shear := math.Tan(cam.Pitch) * focal
	sinA, cosA := math.Sin(cam.Angle), math.Cos(cam.Angle)
	fw, fh := float64(w), float64(h)

	// View: world -> eye (eyeX = right/horiz, eyeY = up = worldZ-camZ,
	// eyeZ = forward/depth). Row-major.
	v := mat4{
		sinA, -cosA, 0, -(cam.X*sinA - cam.Y*cosA),
		0, 0, 1, -cam.Z,
		cosA, sinA, 0, -(cam.X*cosA + cam.Y*sinA),
		0, 0, 0, 1,
	}

	// Projection: eye -> clip (Vulkan: NDC y down, z [0,1], w = eyeZ).
	zc := farPlane / (farPlane - nearPlane)
	p := mat4{
		2 * focal / fw, 0, 0, 0,
		0, -2 * focal / fh, 2 * shear / fh, 0,
		0, 0, zc, -nearPlane * farPlane / (farPlane - nearPlane),
		0, 0, 1, 0,
	}

	return p.mul(v)
}

// VoxelModelMatrix is a voxel instance's column-major model matrix:
// translate(inst.X,Y,Z) . rotZ(Yaw) . scale(inst.Scale) — voxel-unit model
// space to world space.
func VoxelModelMatrix(inst VoxelInstance) [16]float32 {
	c := math.Cos(float64(inst.Yaw))
	s := math.Sin(float64(inst.Yaw))
	k := float64(inst.Scale)
	return mat4{
		k * c, -k * s, 0, float64(inst.X),
		k * s, k * c, 0, float64(inst.Y),
		0, 0, k, float64(inst.Z),
		0, 0, 0, 1,
	}.colMajor()
}

// Mat4Mul multiplies two column-major (GLSL) 4x4 matrices: result = a*b.
// The backend uses it to fold a per-voxel model matrix into the frame's one
// view-projection instead of rebuilding the projection per instance.
func Mat4Mul(a, b [16]float32) [16]float32 {
	var o [16]float32
	for col := 0; col < 4; col++ {
		for row := 0; row < 4; row++ {
			var s float32
			for k := 0; k < 4; k++ {
				s += a[k*4+row] * b[col*4+k]
			}
			o[col*4+row] = s
		}
	}
	return o
}

// mat4 is a row-major 4x4: element [r][c] is at index r*4+c.
type mat4 [16]float64

func (a mat4) mul(b mat4) mat4 {
	var o mat4
	for r := 0; r < 4; r++ {
		for c := 0; c < 4; c++ {
			o[r*4+c] = a[r*4+0]*b[0*4+c] + a[r*4+1]*b[1*4+c] + a[r*4+2]*b[2*4+c] + a[r*4+3]*b[3*4+c]
		}
	}
	return o
}

// colMajor transposes to the column-major float32 layout GLSL/Vulkan
// expects for a mat4 uniform (index = col*4 + row).
func (a mat4) colMajor() [16]float32 {
	var o [16]float32
	for r := 0; r < 4; r++ {
		for c := 0; c < 4; c++ {
			o[c*4+r] = float32(a[r*4+c])
		}
	}
	return o
}
