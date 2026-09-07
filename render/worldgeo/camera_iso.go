package worldgeo

import "math"

// IsoCamera is a top-down/isometric orthographic camera: it looks at
// (TargetX, TargetY, TargetZ) from Azimuth (yaw, radians, 0 = looking down
// -Y... same convention as Camera.Angle: 0 = +X, CCW) rotated down by
// Elevation (radians above the horizon; IsoTrueElevation is the classic
// "true isometric" 2:1 angle). Zoom is the orthographic view's half-height
// in map units — smaller zooms in, larger zooms out. Unlike Camera, there
// is no shear/pitch hack: this is a real 3D rotation, appropriate for a
// camera that is actually tilted down rather than approximately so.
//
// Mode is read only by the input layer (cmd/isoengine): "orbit" lets
// Azimuth/Elevation/Zoom move freely, "fixed" snaps Azimuth to one of a
// small set of angles and holds Elevation constant. IsoViewProj doesn't
// care which — it just projects whatever Azimuth/Elevation/Zoom it's given.
type IsoCamera struct {
	TargetX, TargetY, TargetZ float64
	Azimuth                   float64
	Elevation                 float64
	Zoom                      float64
	Mode                      string
}

// IsoTrueElevation is atan(1/sqrt(2)) — the "true isometric" camera tilt
// (a cube's top/front/side faces all read as equal-area parallelograms at
// this angle), the classic default for both IsoCamera.Mode values.
const IsoTrueElevation = 0.6154797086703873 // math.Atan(1 / math.Sqrt2)

// isoNear/isoFar are IsoViewProj's orthographic clip planes, in the eye-
// space depth this projection uses (see below) — generous enough to hold
// any Doom map (worldgeo.FarPlane) on either side of the target.
const (
	isoNear = -farPlane
	isoFar  = farPlane
)

// IsoViewProj returns the column-major (GLSL mat4) view-projection matrix
// for cam and a w x h render target: world-space geometry (worldgeo.Vert,
// Z up, map units) transformed by it lands in Vulkan clip space (x right,
// y down, z in [0,1], w = 1 — an orthographic projection has no
// perspective divide, unlike ViewProj's).
func IsoViewProj(cam IsoCamera, w, h int) [16]float32 {
	return isoViewProjRM(cam, w, h).colMajor()
}

// IsoBasis is cam's eye-space basis: R (screen right), U (screen up), D
// (view/depth direction, camera-to-target) — three orthonormal world-space
// vectors. Built directly from Azimuth/Elevation rather than composed
// rotation matrices, so it can be checked by inspection against ViewProj's
// own convention: at Elevation == 0 this must degenerate to exactly
// ViewProj's (right, up, forward) — right = (sinA,-cosA,0), up = (0,0,1),
// forward = (cosA,sinA,0) — since a non-tilted iso camera is just the
// perspective camera's horizontal aim. R stays horizontal at any Elevation
// (tilting a camera about its own right axis doesn't move that axis); D
// tilts down by Elevation; U is R x D, which reduces to world-up (0,0,1)
// at Elevation == 0 as required. Shared by IsoViewProj (the projection
// matrix) and IsoUnprojectToGround (screen -> world), so the two can't
// silently disagree on the camera's orientation.
func IsoBasis(cam IsoCamera) (r, u, d [3]float64) {
	sinA, cosA := math.Sin(cam.Azimuth), math.Cos(cam.Azimuth)
	sinE, cosE := math.Sin(cam.Elevation), math.Cos(cam.Elevation)
	r = [3]float64{sinA, -cosA, 0}
	d = [3]float64{cosE * cosA, cosE * sinA, -sinE}
	u = [3]float64{r[1]*d[2] - r[2]*d[1], r[2]*d[0] - r[0]*d[2], r[0]*d[1] - r[1]*d[0]}
	return r, u, d
}

// isoHalfExtents is IsoViewProj's orthographic view half-height/half-width
// in map units for cam and a w x h render target — zoom is the half-height,
// width follows from the render target's aspect so a non-square window
// doesn't stretch the map. Shared by IsoViewProj and IsoUnprojectToGround.
func isoHalfExtents(cam IsoCamera, w, h int) (halfW, halfH float64) {
	zoom := cam.Zoom
	if zoom <= 0 {
		zoom = 512
	}
	aspect := float64(w) / float64(h)
	return zoom * aspect, zoom
}

func isoViewProjRM(cam IsoCamera, w, h int) mat4 {
	r, u, d := IsoBasis(cam)
	tx, ty, tz := cam.TargetX, cam.TargetY, cam.TargetZ
	v := mat4{
		r[0], r[1], r[2], -(tx*r[0] + ty*r[1] + tz*r[2]),
		u[0], u[1], u[2], -(tx*u[0] + ty*u[1] + tz*u[2]),
		d[0], d[1], d[2], -(tx*d[0] + ty*d[1] + tz*d[2]),
		0, 0, 0, 1,
	}

	// Projection: eye -> clip, orthographic.
	halfW, halfH := isoHalfExtents(cam, w, h)
	p := mat4{
		1 / halfW, 0, 0, 0,
		0, -1 / halfH, 0, 0,
		0, 0, 1 / (isoFar - isoNear), -isoNear / (isoFar - isoNear),
		0, 0, 0, 1,
	}

	return p.mul(v)
}

// IsoUnprojectToGround maps a screen pixel (screenX, screenY — origin
// top-left, the same convention window cursor coordinates use) back to a
// world-space point on the horizontal plane Z == groundZ, for cam and a
// w x h render target (the same triple IsoViewProj was built from). This is
// what an isometric front end uses to turn a mouse position into an aim
// point (see engine.Game.IsoAimX/Y) — an orthographic camera has no single
// "the" world point under a pixel the way a perspective one's ray-cast
// does, but intersecting with a known ground height resolves it exactly.
// ok is false only when cam is tilted edge-on (Elevation == 0), where every
// point along the view direction shares the same ground height and the
// intersection is undefined.
func IsoUnprojectToGround(cam IsoCamera, w, h int, screenX, screenY, groundZ float64) (worldX, worldY float64, ok bool) {
	r, u, d := IsoBasis(cam)
	if math.Abs(d[2]) < 1e-6 {
		return 0, 0, false
	}
	halfW, halfH := isoHalfExtents(cam, w, h)

	// screen -> NDC (-1..1, y still down) -> eye-space X/Y (undoing
	// IsoViewProj's projection row exactly).
	ndcX := screenX/float64(w)*2 - 1
	ndcY := screenY/float64(h)*2 - 1
	eyeX := ndcX * halfW
	eyeY := -ndcY * halfH

	// World = Target + eyeX*R + eyeY*U + eyeZ*D (R/U/D orthonormal, so this
	// is IsoBasis's exact inverse); solve eyeZ from the ground-plane
	// constraint worldZ == groundZ, using that R is always horizontal
	// (r[2] == 0, so it drops out of the Z equation).
	eyeZ := (groundZ - cam.TargetZ - eyeY*u[2]) / d[2]

	worldX = cam.TargetX + eyeX*r[0] + eyeY*u[0] + eyeZ*d[0]
	worldY = cam.TargetY + eyeX*r[1] + eyeY*u[1] + eyeZ*d[1]
	return worldX, worldY, true
}

// IsoProjectToScreen maps a world-space point to the screen pixel (origin
// top-left, matching window cursor coordinates and IsoUnprojectToGround's
// own convention) it lands on for cam and a w x h render target — the
// forward direction of IsoUnprojectToGround, built from the same IsoBasis/
// isoHalfExtents so the two can't disagree. Used for a crosshair-snap aim
// assist: an isometric front end projects each nearby shootable thing to
// screen space and checks how close it lands to the mouse, rather than
// trying to invert that per-thing (an orthographic screen point maps to a
// whole line in world space, not a single point — see
// IsoUnprojectToGround's own doc comment on why the forward direction has
// no such ambiguity to resolve).
func IsoProjectToScreen(cam IsoCamera, w, h int, worldX, worldY, worldZ float64) (screenX, screenY float64) {
	screenX, screenY, _ = IsoProjectToScreenDepth(cam, w, h, worldX, worldY, worldZ)
	return screenX, screenY
}

// IsoProjectToScreenDepth is IsoProjectToScreen plus the point's own clip-
// space depth (the same [0,1] value gl_FragCoord.z carries in iso.frag,
// where 0.5 is exactly cam.Target — see IsoViewProj's near/far comment):
// for a caller that needs to know not just where a point lands on screen
// but whether it's nearer to the camera than something else there (an
// isometric front end builds a small list of these — monsters, items,
// barrels — for iso.frag's per-fragment occluder-fade check, so a wall
// between the camera and any of them fades the same way one between the
// camera and the player already does).
func IsoProjectToScreenDepth(cam IsoCamera, w, h int, worldX, worldY, worldZ float64) (screenX, screenY, clipZ float64) {
	r, u, d := IsoBasis(cam)
	dx, dy, dz := worldX-cam.TargetX, worldY-cam.TargetY, worldZ-cam.TargetZ
	eyeX := dx*r[0] + dy*r[1] + dz*r[2]
	eyeY := dx*u[0] + dy*u[1] + dz*u[2]
	eyeZ := dx*d[0] + dy*d[1] + dz*d[2]

	halfW, halfH := isoHalfExtents(cam, w, h)
	ndcX := eyeX / halfW
	ndcY := -eyeY / halfH
	screenX = (ndcX + 1) / 2 * float64(w)
	screenY = (ndcY + 1) / 2 * float64(h)
	clipZ = (eyeZ - isoNear) / (isoFar - isoNear)
	return screenX, screenY, clipZ
}
