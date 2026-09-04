package worldgeo

import (
	"math"
	"testing"
)

// rasterProject reproduces raster.Renderer's screen projection for one
// world point (the exact math ViewProj must agree with).
func rasterProject(cam Camera, w, h int, wx, wy, wz float64) (sx, sy, depth float64) {
	focal := focalFor(cam.FOVDeg, h)
	shear := math.Tan(cam.Pitch) * focal
	sinA, cosA := math.Sin(cam.Angle), math.Cos(cam.Angle)
	relX, relY := wx-cam.X, wy-cam.Y
	depth = relX*cosA + relY*sinA
	horiz := relX*sinA - relY*cosA
	sx = float64(w)/2 + horiz/depth*focal
	sy = (float64(h)/2 + shear) - (wz-cam.Z)/depth*focal
	return
}

func applyVP(m [16]float32, wx, wy, wz float64) (px, py, ndcZ, clipW float64) {
	// m is column-major: clip_r = sum_c m[c*4+r] * v_c
	v := [4]float64{wx, wy, wz, 1}
	var clip [4]float64
	for r := 0; r < 4; r++ {
		for c := 0; c < 4; c++ {
			clip[r] += float64(m[c*4+r]) * v[c]
		}
	}
	clipW = clip[3]
	ndcX, ndcY, ndcZ := clip[0]/clipW, clip[1]/clipW, clip[2]/clipW
	return (ndcX + 1) / 2, (ndcY + 1) / 2, ndcZ, clipW // px,py in 0..1 of the viewport
}

func TestViewProjMatchesRasterProjection(t *testing.T) {
	cams := []Camera{
		{X: 0, Y: 0, Z: 41, Angle: 0, Pitch: 0, FOVDeg: 90},
		{X: 128, Y: -256, Z: 50, Angle: 1.1, Pitch: 0, FOVDeg: 100},
		{X: -500, Y: 300, Z: 41, Angle: -2.3, Pitch: 0.3, FOVDeg: 95},
		{X: 64, Y: 64, Z: 60, Angle: math.Pi, Pitch: -0.4, FOVDeg: 110},
	}
	pts := [][3]float64{
		{100, 0, 0}, {200, 150, 64}, {-300, 400, -32},
		{50, -800, 128}, {1000, 1000, 0}, {40, 5, 96},
	}
	const w, h = 1920, 1080

	for ci, cam := range cams {
		m := ViewProj(cam, w, h)
		for pi, p := range pts {
			sx, sy, depth := rasterProject(cam, w, h, p[0], p[1], p[2])
			if depth <= nearPlane {
				continue // behind the near plane — raster would clip it
			}
			nx, ny, ndcZ, clipW := applyVP(m, p[0], p[1], p[2])
			gpx, gpy := nx*float64(w), ny*float64(h)

			if math.Abs(gpx-sx) > 0.02 || math.Abs(gpy-sy) > 0.02 {
				t.Errorf("cam %d pt %d: GPU (%.3f,%.3f) vs raster (%.3f,%.3f)", ci, pi, gpx, gpy, sx, sy)
			}
			// w must be the camera-forward depth (so early-Z / fog can use it).
			if math.Abs(clipW-depth) > 1e-3 {
				t.Errorf("cam %d pt %d: clip.w %.3f != depth %.3f", ci, pi, clipW, depth)
			}
			// NDC z in [0,1] and monotonically increasing with depth.
			if ndcZ < -1e-4 || ndcZ > 1+1e-4 {
				t.Errorf("cam %d pt %d: ndcZ %.4f outside [0,1]", ci, pi, ndcZ)
			}
		}
	}
}

func TestViewProjDepthOrdering(t *testing.T) {
	cam := Camera{X: 0, Y: 0, Z: 41, Angle: 0, FOVDeg: 90}
	m := ViewProj(cam, 800, 600)
	var prev float64 = -1
	for _, d := range []float64{2, 10, 64, 256, 1024, 8192, 60000} {
		_, _, ndcZ, w := applyVP(m, d, 0, 41) // straight ahead
		if w <= 0 {
			t.Fatalf("depth %.0f: clip.w %.3f not positive", d, w)
		}
		if ndcZ <= prev {
			t.Fatalf("depth %.0f: ndcZ %.5f did not increase past %.5f", d, ndcZ, prev)
		}
		prev = ndcZ
	}
}

func TestViewProjCentreAndEdges(t *testing.T) {
	cam := Camera{X: 0, Y: 0, Z: 0, Angle: 0, Pitch: 0, FOVDeg: 90}
	m := ViewProj(cam, 1000, 800)
	// A point dead ahead projects to screen centre.
	nx, ny, _, _ := applyVP(m, 500, 0, 0)
	if math.Abs(nx-0.5) > 1e-4 || math.Abs(ny-0.5) > 1e-4 {
		t.Fatalf("point straight ahead -> (%.4f,%.4f), want (0.5,0.5)", nx, ny)
	}
	// A point up and to the right is above-right of centre: px > 0.5, py < 0.5
	// (Vulkan NDC y is down, so "up" is a smaller y).
	nx, ny, _, _ = applyVP(m, 500, -200, 200)
	if !(nx > 0.5 && ny < 0.5) {
		t.Fatalf("up-right point -> (%.4f,%.4f), want px>0.5 & py<0.5", nx, ny)
	}
}
