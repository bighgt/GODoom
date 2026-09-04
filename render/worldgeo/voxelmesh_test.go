package worldgeo

import (
	"encoding/binary"
	"math"
	"testing"

	"twopointfive/assets"
)

// oneVoxel builds a 1x1x1 KVX model with a single coloured voxel at the
// origin, pivot centred.
func oneVoxel(c [3]byte) *assets.VoxelModel {
	return &assets.VoxelModel{
		XSiz: 1, YSiz: 1, ZSiz: 1,
		XPivot: 0.5, YPivot: 0.5, ZPivot: 0.5,
		Cols: [][]assets.VoxSlab{{{ZTop: 0, Colors: [][3]byte{c}}}},
	}
}

func TestBuildVoxelMeshSingleCube(t *testing.T) {
	m := oneVoxel([3]byte{10, 20, 30})
	buf := BuildVoxelMesh(m)
	if len(buf)%VoxelPackedVertexSize != 0 {
		t.Fatalf("buffer %d not a multiple of vertex size %d", len(buf), VoxelPackedVertexSize)
	}
	verts := len(buf) / VoxelPackedVertexSize
	// A lone voxel: all 6 faces exposed, 2 triangles each, 3 verts per tri.
	if verts != 6*2*3 {
		t.Fatalf("got %d verts, want %d (6 faces * 2 tris * 3)", verts, 36)
	}

	le := binary.LittleEndian
	minX, minY, minZ := math.MaxFloat32, math.MaxFloat32, math.MaxFloat32
	maxX, maxY, maxZ := -math.MaxFloat32, -math.MaxFloat32, -math.MaxFloat32
	for i := 0; i < verts; i++ {
		o := i * VoxelPackedVertexSize
		x := math.Float32frombits(le.Uint32(buf[o:]))
		y := math.Float32frombits(le.Uint32(buf[o+4:]))
		z := math.Float32frombits(le.Uint32(buf[o+8:]))
		nx := math.Float32frombits(le.Uint32(buf[o+12:]))
		ny := math.Float32frombits(le.Uint32(buf[o+16:]))
		nz := math.Float32frombits(le.Uint32(buf[o+20:]))
		if n := math.Sqrt(float64(nx*nx + ny*ny + nz*nz)); math.Abs(n-1) > 1e-5 {
			t.Fatalf("vert %d normal not unit: %v", i, n)
		}
		if buf[o+24] != 10 || buf[o+25] != 20 || buf[o+26] != 30 || buf[o+27] != 255 {
			t.Fatalf("vert %d colour = %v, want {10 20 30 255}", i, buf[o+24:o+28])
		}
		minX, maxX = math.Min(minX, float64(x)), math.Max(maxX, float64(x))
		minY, maxY = math.Min(minY, float64(y)), math.Max(maxY, float64(y))
		minZ, maxZ = math.Min(minZ, float64(z)), math.Max(maxZ, float64(z))
	}
	// Pivot-centred 1-voxel cube spans [-0.5, 0.5] on every axis.
	for _, g := range []struct {
		name          string
		lo, hi        float64
		gotLo, gotHi  float64
	}{
		{"x", -0.5, 0.5, minX, maxX},
		{"y", -0.5, 0.5, minY, maxY},
		{"z", -0.5, 0.5, minZ, maxZ},
	} {
		if math.Abs(g.gotLo-g.lo) > 1e-5 || math.Abs(g.gotHi-g.hi) > 1e-5 {
			t.Errorf("%s extent [%.3f,%.3f], want [%.1f,%.1f]", g.name, g.gotLo, g.gotHi, g.lo, g.hi)
		}
	}
}

func TestBuildVoxelMeshHidesSharedFaces(t *testing.T) {
	// Two voxels stacked in Z: the touching faces (top of lower, bottom of
	// upper) must not be emitted — 6+6 faces minus the 2 shared = 10.
	m := &assets.VoxelModel{
		XSiz: 1, YSiz: 1, ZSiz: 2,
		XPivot: 0.5, YPivot: 0.5, ZPivot: 1,
		Cols:   [][]assets.VoxSlab{{{ZTop: 0, Colors: [][3]byte{{1, 1, 1}, {2, 2, 2}}}}},
	}
	verts := len(BuildVoxelMesh(m)) / VoxelPackedVertexSize
	if want := 10 * 2 * 3; verts != want {
		t.Fatalf("stacked pair: %d verts, want %d (10 faces)", verts, want)
	}
}

func TestBuildVoxelMeshNilSafe(t *testing.T) {
	if BuildVoxelMesh(nil) != nil {
		t.Fatal("nil model should give nil mesh")
	}
}

// A 3x3x3 shell with the centre voxel omitted (as a KVX exporter would):
// the exterior-air flood fill must recognise the centre is sealed and NOT
// emit the 6 inward faces around it — only the 54 outer faces (6 sides x 9).
func TestBuildVoxelMeshSealsHollow(t *testing.T) {
	cols := make([][]assets.VoxSlab, 9)
	for x := 0; x < 3; x++ {
		for y := 0; y < 3; y++ {
			if x == 1 && y == 1 {
				// centre column: voxels at z=0 and z=2, gap at z=1
				cols[x*3+y] = []assets.VoxSlab{
					{ZTop: 0, Colors: [][3]byte{{9, 9, 9}}},
					{ZTop: 2, Colors: [][3]byte{{9, 9, 9}}},
				}
				continue
			}
			cols[x*3+y] = []assets.VoxSlab{{ZTop: 0, Colors: [][3]byte{{9, 9, 9}, {9, 9, 9}, {9, 9, 9}}}}
		}
	}
	m := &assets.VoxelModel{XSiz: 3, YSiz: 3, ZSiz: 3, XPivot: 1.5, YPivot: 1.5, ZPivot: 1.5, Cols: cols}
	verts := len(BuildVoxelMesh(m)) / VoxelPackedVertexSize
	if want := 54 * 2 * 3; verts != want {
		t.Fatalf("hollow shell: %d verts, want %d (54 outer faces only)", verts, want)
	}
}
