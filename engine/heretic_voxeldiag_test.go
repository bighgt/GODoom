package engine

import (
	"math"
	"os"
	"testing"

	"twopointfive/assets"
	"twopointfive/bsp"
	"twopointfive/raster"
	"twopointfive/render/worldgeo"
)

// TestHereticVoxelFirstPersonPath drives the exact collector the hardware
// renderer feeds (Game.collectWorldObjects) with the real Heretic IWAD and
// reikall's voxel pack, and checks that decorations/items that have a KVX
// model in the pack become worldgeo.VoxelInstances that project into the NDC
// frustum. Regression guard for "voxels aren't used in the Heretic WAD" —
// the data pipeline half of it (the GPU draw is exercised live, not here).
func TestHereticVoxelFirstPersonPath(t *testing.T) {
	const vpath = "../assets/voxel/reikallhereticvoxels.pk3"
	if _, err := os.Stat(vpath); err != nil {
		t.Skipf("no %s", vpath)
	}
	g := hereticGame(t, "E1M1")
	vs, err := assets.LoadVoxelPack(vpath)
	if err != nil {
		t.Fatalf("LoadVoxelPack: %v", err)
	}
	tex, err := assets.New(g.WAD)
	if err != nil {
		t.Fatalf("assets.New: %v", err)
	}
	const W, H = 1280, 720
	g.Raster = raster.New(tex, W, H)
	g.Voxels = vs
	g.VoxelScale = 0.9

	var sx, sy float64
	for _, th := range g.Level.Things {
		if th.Type == 1 {
			sx, sy = float64(th.X), float64(th.Y)
		}
	}
	sec := bsp.PointSector(g.BSP, g.Level, float32(sx), float32(sy))
	if sec == nil {
		t.Fatal("player start not in a sector")
	}
	g.Camera = raster.Camera{X: sx, Y: sy, Z: float64(sec.FloorHeight) + EyeHeight, Angle: 0, FOVDeg: 90}

	// A serpent torch (SRTC — a DECORATE-only model this engine hand-aliases)
	// 96u dead ahead: the collector must voxelize it, and its mesh must
	// project on-screen through the frame's ViewProj * VoxelModelMatrix.
	torch := g.P_SpawnMobj(sx+96, sy, onFloorZ, MT_HERETIC)
	torch.spriteOverride = "SRTC"
	if m, _, _, ok := vs.Model("SRTC", 0); !ok || m == nil {
		t.Fatal("pack has no SRTC voxel model")
	}

	_, voxels := g.collectWorldObjects()
	if len(voxels) == 0 {
		t.Fatal("collectWorldObjects emitted no voxel instance for a torch dead ahead")
	}

	cam := worldgeo.Camera{X: g.Camera.X, Y: g.Camera.Y, Z: g.Camera.Z, FOVDeg: 90}
	vp := worldgeo.ViewProj(cam, W, H)
	onScreen := 0
	for _, inst := range voxels {
		mesh := worldgeo.BuildVoxelMesh(inst.Model)
		if len(mesh) == 0 {
			t.Errorf("voxel model %p meshed to nothing", inst.Model)
			continue
		}
		if meshProjectsOnScreen(mesh, worldgeo.Mat4Mul(vp, worldgeo.VoxelModelMatrix(inst))) {
			onScreen++
		}
	}
	if onScreen == 0 {
		t.Fatal("voxel instances emitted but none project into the NDC frustum — matrix/scale bug")
	}

	// Sweeping the view yaw must keep producing instances (the collector's
	// back-plane cull drops things behind the camera, never all of them).
	total := 0
	for step := 0; step < 72; step++ {
		g.Camera.Angle = float64(step) / 72 * 2 * math.Pi
		_, vx := g.collectWorldObjects()
		total += len(vx)
	}
	if total < 72 {
		t.Errorf("only %d voxel instances over 72 yaw steps — expected the map's ~100 decorations to keep cycling through view", total)
	}
}

func meshProjectsOnScreen(mesh []byte, mvp [16]float32) bool {
	const stride = worldgeo.VoxelPackedVertexSize
	for o := 0; o+stride <= len(mesh); o += stride {
		x := math.Float32frombits(le32(mesh[o:]))
		y := math.Float32frombits(le32(mesh[o+4:]))
		z := math.Float32frombits(le32(mesh[o+8:]))
		cx := mvp[0]*x + mvp[4]*y + mvp[8]*z + mvp[12]
		cy := mvp[1]*x + mvp[5]*y + mvp[9]*z + mvp[13]
		cz := mvp[2]*x + mvp[6]*y + mvp[10]*z + mvp[14]
		cw := mvp[3]*x + mvp[7]*y + mvp[11]*z + mvp[15]
		if cw <= 0 {
			continue
		}
		if nx, ny, nz := cx/cw, cy/cw, cz/cw; nx >= -1 && nx <= 1 && ny >= -1 && ny <= 1 && nz >= 0 && nz <= 1 {
			return true
		}
	}
	return false
}

func le32(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}
