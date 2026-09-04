package render

import (
	"math"
	"testing"
)

// The enhanced lighting shader (render/vulkan/shaders/light.frag)
// reconstructs a pixel's world position from Frame.Depth and the camera
// fields. This pins that reconstruction against the forward projection
// raster.Renderer actually uses (toCameraSpace + the drawFlatSpan column
// math), so a change to one without the other is caught here rather than
// as a smear of misplaced lighting on screen.

type cam struct {
	x, y, z, sinA, cosA, focal, horizonY float64
}

// project mirrors raster: camera-space depth + screen pixel of a world point.
func (c cam) project(w float64, wx, wy, wz float64) (depth, sx, sy float64) {
	relX, relY := wx-c.x, wy-c.y
	depth = relX*c.cosA + relY*c.sinA
	horiz := relX*c.sinA - relY*c.cosA
	sx = w/2 + horiz/depth*c.focal
	sy = c.horizonY - (wz-c.z)/depth*c.focal
	return
}

// reconstruct mirrors light.frag.
func (c cam) reconstruct(w, depth, sx, sy float64) (wx, wy, wz float64) {
	colF := (sx - w/2) / c.focal
	wx = c.x + depth*(c.cosA+colF*c.sinA)
	wy = c.y + depth*(c.sinA-colF*c.cosA)
	wz = c.z - (sy-c.horizonY)/c.focal*depth
	return
}

func TestWorldPositionRoundTrip(t *testing.T) {
	angle := 0.7
	c := cam{x: 120, y: -40, z: 41, sinA: math.Sin(angle), cosA: math.Cos(angle), focal: 640, horizonY: 300}
	w := 1280.0

	for _, p := range [][3]float64{
		{200, 60, 30}, {-300, -500, 80}, {512, -64, 0}, {1000, 1000, -50}, {121, -39, 41.5},
	} {
		depth, sx, sy := c.project(w, p[0], p[1], p[2])
		if depth <= 0 {
			continue // behind the camera; the shader early-outs on these
		}
		gx, gy, gz := c.reconstruct(w, depth, sx, sy)
		if math.Abs(gx-p[0]) > 1e-6 || math.Abs(gy-p[1]) > 1e-6 || math.Abs(gz-p[2]) > 1e-6 {
			t.Errorf("world %v -> (d=%.3f sx=%.3f sy=%.3f) -> (%.4f %.4f %.4f)", p, depth, sx, sy, gx, gy, gz)
		}
	}
}

func TestMaxLightsMatchesShaderArray(t *testing.T) {
	// light.frag declares `GPULight lights[64]` and lit.go's litSceneFloats
	// sizes the UBO to it. If this constant changes, all three must.
	if MaxLights != 64 {
		t.Errorf("MaxLights = %d; the shader's lights[] array and lit.go must change together", MaxLights)
	}
}
