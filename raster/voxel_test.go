package raster

import (
	"math"
	"testing"

	"twopointfive/assets"
)

// cube builds a solid s×s×s voxel model, pivot centred on X/Y and at the
// base on Z (KVX convention), every voxel a flat colour.
func cube(s int, r, g, b byte) *assets.VoxelModel {
	m := &assets.VoxelModel{
		XSiz: s, YSiz: s, ZSiz: s,
		XPivot: float64(s) / 2, YPivot: float64(s) / 2, ZPivot: float64(s),
		Cols: make([][]assets.VoxSlab, s*s),
	}
	col := make([][3]byte, s)
	for i := range col {
		col[i] = [3]byte{r, g, b}
	}
	for x := 0; x < s; x++ {
		for y := 0; y < s; y++ {
			cc := make([][3]byte, s)
			copy(cc, col)
			m.Cols[x*s+y] = []assets.VoxSlab{{ZTop: 0, Face: 0x10, Colors: cc}}
		}
	}
	return m
}

// primeCamera sets the per-frame projection state Render normally computes,
// for a camera at the origin looking along +X with no pitch, and resets the
// depth buffer the way Render does (New leaves it zeroed).
func primeCamera(r *Renderer, focal float64) {
	r.focal = focal
	r.sinA, r.cosA = 0, 1 // angle 0 -> forward +X, right -Y
	r.pitchShear = 0
	r.voxelSmooth = false // the base invariants are tested on the hard path
	copy(r.depth, r.blankDepth)
}

func TestDrawVoxelWritesPixelsAndDepth(t *testing.T) {
	r := New(nil, 200, 200)
	primeCamera(r, 320)

	m := cube(20, 200, 40, 40)
	cam := Camera{X: 0, Y: 0, Z: 10}
	// Thing 100 units ahead, feet on the floor at z=0: ~64 px tall on screen.
	if !r.DrawVoxel(cam, 100, 0, 0, 0, 255, false, 0, m) {
		t.Fatal("DrawVoxel declined a model that should be well above the size cutoff")
	}

	painted, depthSet := 0, 0
	var sawRed bool
	for p := 0; p < r.Width*r.Height; p++ {
		i := p * 4
		if r.Pix[i] != 0 || r.Pix[i+1] != 0 || r.Pix[i+2] != 0 {
			painted++
			if r.Pix[i] > 100 && r.Pix[i] > r.Pix[i+1] && r.Pix[i] > r.Pix[i+2] {
				sawRed = true
			}
		}
		if r.depth[p] < math.MaxFloat32 {
			depthSet++
			if d := r.depth[p]; d < 85 || d > 115 {
				t.Fatalf("voxel depth %v outside the model's ~100u range", d)
			}
		}
	}
	if painted < 200 {
		t.Errorf("only %d pixels painted for a near cube", painted)
	}
	if depthSet != painted {
		t.Errorf("painted %d pixels but wrote depth for %d", painted, depthSet)
	}
	if !sawRed {
		t.Error("cube colour did not come through (expected reddish pixels)")
	}
}

func TestDrawVoxelDeclinesWhenTiny(t *testing.T) {
	r := New(nil, 320, 200)
	primeCamera(r, 160)
	m := cube(16, 255, 255, 255)
	// Far enough that 16 voxels * 160 / depth < minVoxelHeightPx.
	far := 16.0 * 160.0 / (minVoxelHeightPx - 1)
	if r.DrawVoxel(Camera{Z: 8}, far+50, 0, 0, 0, 255, false, 0, m) {
		t.Errorf("DrawVoxel drew a model only ~%.0f px tall; expected it to decline", 16*160/(far+50))
	}
	for p := range r.depth {
		if r.depth[p] < math.MaxFloat32 {
			t.Fatal("a declined DrawVoxel still wrote to the depth buffer")
		}
	}
}

func TestDrawVoxelRespectsExistingDepth(t *testing.T) {
	r := New(nil, 120, 120)
	primeCamera(r, 120)
	// Pretend a wall already filled the whole frame at depth 10 — closer
	// than the model at ~150 — so nothing of the voxel should survive.
	for p := range r.depth {
		r.depth[p] = 10
	}
	m := cube(20, 90, 200, 90)
	r.DrawVoxel(Camera{Z: 10}, 150, 0, 0, 0, 255, false, 0, m)
	for p := 0; p < r.Width*r.Height; p++ {
		i := p * 4
		if r.Pix[i] != 0 || r.Pix[i+1] != 0 || r.Pix[i+2] != 0 {
			t.Fatalf("voxel pixel survived in front of a nearer wall at cell %d", p)
		}
	}
}

func TestDrawVoxelNilModel(t *testing.T) {
	r := New(nil, 64, 64)
	primeCamera(r, 64)
	if r.DrawVoxel(Camera{}, 50, 0, 0, 0, 255, false, 0, nil) {
		t.Error("DrawVoxel(nil model) returned true")
	}
}

// The smooth filter must anti-alias the model's edges: its silhouette pixels
// get a partial blend of the model colour and the background instead of a
// hard on/off, and the interior stays solid. It also paints a slightly
// wider footprint than the hard splats.
func TestDrawVoxelSmoothAntialiases(t *testing.T) {
	render := func(smooth bool) (*Renderer, int, int) {
		r := New(nil, 200, 200)
		primeCamera(r, 320)
		r.voxelSmooth = smooth
		m := cube(20, 220, 30, 30) // strong red on black
		if !r.DrawVoxel(Camera{X: 0, Y: 0, Z: 10}, 100, 0, 0, 0, 255, false, 0, m) {
			t.Fatal("DrawVoxel declined")
		}
		painted, partial := 0, 0
		for p := 0; p < r.Width*r.Height; p++ {
			i := p * 4
			cr, cg, cb := r.Pix[i], r.Pix[i+1], r.Pix[i+2]
			if cr == 0 && cg == 0 && cb == 0 {
				continue
			}
			painted++
			// a blended edge pixel: reddish but clearly dimmer than the solid
			// interior, and not a pure copy of it.
			if cr > 8 && cr < 200 && cr > cg && cr > cb {
				partial++
			}
		}
		return r, painted, partial
	}

	_, _, hardPartial := render(false)
	_, softPainted, softPartial := render(true)

	if softPainted == 0 {
		t.Fatal("smooth painted nothing")
	}
	// Hard splats are on/off: a "partial" pixel is at most a rounding
	// artefact. Smooth deliberately blends every silhouette pixel.
	if softPartial < hardPartial*4 || softPartial < 40 {
		t.Errorf("smooth produced only %d antialiased edge pixels (hard: %d) — filter not blending", softPartial, hardPartial)
	}
}

// Nearest and smooth must both leave the model correctly occluded by a
// nearer wall (smooth's alpha blend still honours the depth buffer).
func TestDrawVoxelSmoothRespectsDepth(t *testing.T) {
	r := New(nil, 120, 120)
	primeCamera(r, 120)
	r.voxelSmooth = true
	for p := range r.depth {
		r.depth[p] = 10
	}
	r.DrawVoxel(Camera{Z: 10}, 150, 0, 0, 0, 255, false, 0, cube(20, 90, 200, 90))
	for i := 0; i+3 < len(r.Pix); i += 4 {
		if r.Pix[i] != 0 || r.Pix[i+1] != 0 || r.Pix[i+2] != 0 {
			t.Fatalf("smooth voxel pixel survived in front of a nearer wall at %d", i/4)
		}
	}
}
