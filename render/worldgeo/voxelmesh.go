package worldgeo

import (
	"encoding/binary"
	"math"

	"twopointfive/assets"
)

// Voxel models for the hardware path. A KVX model is turned once into a
// static triangle mesh of its exposed cube faces (BuildVoxelMesh, pure Go,
// cached by the backend), in model space with 1 unit = 1 voxel and the
// origin at the KVX pivot — the same localX/Y/Z frame raster/voxel.go uses.
// Per frame the backend draws each VoxelInstance with a model matrix that
// applies the config voxel scale, the thing's yaw, and its world position.

// VoxelInstance is one voxel model to draw this frame.
type VoxelInstance struct {
	Model      *assets.VoxelModel
	X, Y, Z    float32 // thing centre (X,Y) and feet (Z), map units
	Yaw        float32 // radians — thing facing + VOXELDEF AngleOffset + KVX front bias
	Scale      float32 // map units per voxel (config voxelScale * VOXELDEF Scale)
	Light      float32 // sector light 0..1 (sprite shading)
	FullBright bool
}

// VoxelPackedVertexSize is one voxel-mesh vertex: vec3 position + vec3
// normal (model space, 1 unit = 1 voxel) then 4 bytes RGBA colour. 28 bytes.
const VoxelPackedVertexSize = 28

// VoxelVertexAttributes is the packed voxel-mesh layout, in location order:
//
//	0  vec3  position  (model space, voxel units)
//	1  vec3  normal     (model space, unit)
//	2  vec4  colour     (R8G8B8A8_UNORM -> 0..1)
func VoxelVertexAttributes() []VertexAttribute {
	return []VertexAttribute{
		{Location: 0, Format: "R32G32B32_SFLOAT", Offset: 0},
		{Location: 1, Format: "R32G32B32_SFLOAT", Offset: 12},
		{Location: 2, Format: "R8G8B8A8_UNORM", Offset: 24},
	}
}

// six unit face normals and the four corner offsets (each ±0.5) of that
// face's quad, wound CCW seen from outside.
var voxFaces = [6]struct {
	nx, ny, nz float32
	// dx,dy,dz for the 4 quad corners
	corners [4][3]float32
}{
	{1, 0, 0, [4][3]float32{{.5, -.5, -.5}, {.5, .5, -.5}, {.5, .5, .5}, {.5, -.5, .5}}},   // +X
	{-1, 0, 0, [4][3]float32{{-.5, .5, -.5}, {-.5, -.5, -.5}, {-.5, -.5, .5}, {-.5, .5, .5}}}, // -X
	{0, 1, 0, [4][3]float32{{.5, .5, -.5}, {-.5, .5, -.5}, {-.5, .5, .5}, {.5, .5, .5}}},   // +Y
	{0, -1, 0, [4][3]float32{{-.5, -.5, -.5}, {.5, -.5, -.5}, {.5, -.5, .5}, {-.5, -.5, .5}}}, // -Y
	{0, 0, 1, [4][3]float32{{-.5, -.5, .5}, {.5, -.5, .5}, {.5, .5, .5}, {-.5, .5, .5}}},   // +Z
	{0, 0, -1, [4][3]float32{{.5, -.5, -.5}, {-.5, -.5, -.5}, {-.5, .5, -.5}, {.5, .5, -.5}}}, // -Z
}

// Grid-cell offset of the neighbour on the OUTSIDE of each face (voxFaces
// order). X/Y match the face normal directly; Z is negated because KVX grid
// z runs downward while model/world z runs up (cz = ZPivot-(gz+0.5)) — so
// the model-up (+Z) face's outside cell is at gz-1, not gz+1. Getting this
// wrong drops the top face of every voxel (a box's lid, the armour's top).
var voxFaceNeighbour = [6][3]int{{1, 0, 0}, {-1, 0, 0}, {0, 1, 0}, {0, -1, 0}, {0, 0, -1}, {0, 0, 1}}

// BuildVoxelMesh turns a KVX model into the packed GPU vertex buffer of its
// visible surface (little-endian: 3 float32 position, 3 float32 normal, 4
// bytes RGBA). Model space: 1 unit = 1 voxel, X/Y offset by the KVX pivot,
// Z = (ZPivot-(z+0.5)) so it grows up from the thing's feet — the exact
// frame raster/voxel.go's localX/localY/hz use before scaling by voxelScale
// (the backend's model matrix applies that scale).
//
// KVX stores only the model's shell (fully-interior voxels are omitted), so
// "emit a face where the neighbour cell isn't stored" would spray hidden
// inward faces into every hollow — doubling the geometry and, worse, the
// per-fragment overdraw. Instead an exterior-air flood fill from the
// bounding box marks which empty cells are actually outside; a face is
// emitted only where its neighbour is exterior air. Quads are wound CCW seen
// from outside so the pipeline can back-face cull. Deterministic.
func BuildVoxelMesh(m *assets.VoxelModel) []byte {
	if m == nil || m.XSiz <= 0 || m.YSiz <= 0 || m.ZSiz <= 0 {
		return nil
	}
	X, Y, Z := m.XSiz, m.YSiz, m.ZSiz
	idx := func(x, y, z int) int { return (x*Y+y)*Z + z }

	// occ: 0 = unknown (stays "interior" — never faced toward), 1 = stored
	// solid, 2 = exterior air.
	const (
		unknown = 0
		solid   = 1
		air     = 2
	)
	occ := make([]byte, X*Y*Z)
	for x := 0; x < X; x++ {
		for y := 0; y < Y; y++ {
			for _, s := range m.Cols[x*Y+y] {
				for i := range s.Colors {
					if z := s.ZTop + i; z >= 0 && z < Z {
						occ[idx(x, y, z)] = solid
					}
				}
			}
		}
	}

	// Flood exterior air inward from every non-solid boundary cell.
	queue := make([]int32, 0, 2*(X*Y+Y*Z+Z*X))
	push := func(x, y, z int) {
		p := idx(x, y, z)
		if occ[p] == unknown {
			occ[p] = air
			queue = append(queue, int32(p))
		}
	}
	for x := 0; x < X; x++ {
		for y := 0; y < Y; y++ {
			push(x, y, 0)
			push(x, y, Z-1)
		}
	}
	for x := 0; x < X; x++ {
		for z := 0; z < Z; z++ {
			push(x, 0, z)
			push(x, Y-1, z)
		}
	}
	for y := 0; y < Y; y++ {
		for z := 0; z < Z; z++ {
			push(0, y, z)
			push(X-1, y, z)
		}
	}
	for len(queue) > 0 {
		p := int(queue[len(queue)-1])
		queue = queue[:len(queue)-1]
		z := p % Z
		y := (p / Z) % Y
		x := p / (Z * Y)
		if x > 0 {
			push(x-1, y, z)
		}
		if x < X-1 {
			push(x+1, y, z)
		}
		if y > 0 {
			push(x, y-1, z)
		}
		if y < Y-1 {
			push(x, y+1, z)
		}
		if z > 0 {
			push(x, y, z-1)
		}
		if z < Z-1 {
			push(x, y, z+1)
		}
	}
	exterior := func(x, y, z int) bool {
		if x < 0 || y < 0 || z < 0 || x >= X || y >= Y || z >= Z {
			return true
		}
		return occ[idx(x, y, z)] == air
	}

	le := binary.LittleEndian
	var buf []byte
	var tmp [VoxelPackedVertexSize]byte
	emit := func(px, py, pz, nx, ny, nz float32, c [3]byte) {
		le.PutUint32(tmp[0:], math.Float32bits(px))
		le.PutUint32(tmp[4:], math.Float32bits(py))
		le.PutUint32(tmp[8:], math.Float32bits(pz))
		le.PutUint32(tmp[12:], math.Float32bits(nx))
		le.PutUint32(tmp[16:], math.Float32bits(ny))
		le.PutUint32(tmp[20:], math.Float32bits(nz))
		tmp[24], tmp[25], tmp[26], tmp[27] = c[0], c[1], c[2], 255
		buf = append(buf, tmp[:]...)
	}

	// Accumulate the incident face normals at each distinct vertex position
	// (keyed by exact float bits — shared corners compute identically) so a
	// second pass can replace the flat per-face normal with the averaged
	// one. The geometry stays blocky but the *lighting* gradient runs smooth
	// across the facets, which is what makes the stair-steps read softer
	// (GZDoom's "smooth" voxel mode).
	type vn struct{ x, y, z float32 }
	nacc := make(map[[3]uint32]vn, 8192)
	nkey := func(px, py, pz float32) [3]uint32 {
		return [3]uint32{math.Float32bits(px), math.Float32bits(py), math.Float32bits(pz)}
	}

	for gx := 0; gx < X; gx++ {
		cx := float32(float64(gx) + 0.5 - m.XPivot)
		for gy := 0; gy < Y; gy++ {
			cy := float32(float64(gy) + 0.5 - m.YPivot)
			for _, s := range m.Cols[gx*Y+gy] {
				for i := range s.Colors {
					gz := s.ZTop + i
					col := s.Colors[i]
					cz := float32(m.ZPivot - (float64(gz) + 0.5))
					for f := 0; f < 6; f++ {
						d := voxFaceNeighbour[f]
						if !exterior(gx+d[0], gy+d[1], gz+d[2]) {
							continue // neighbour is another voxel or a sealed pocket
						}
						fc := &voxFaces[f]
						var q [4][3]float32
						for k := 0; k < 4; k++ {
							q[k] = [3]float32{cx + fc.corners[k][0], cy + fc.corners[k][1], cz + fc.corners[k][2]}
							a := nacc[nkey(q[k][0], q[k][1], q[k][2])]
							nacc[nkey(q[k][0], q[k][1], q[k][2])] = vn{a.x + fc.nx, a.y + fc.ny, a.z + fc.nz}
						}
						emit(q[0][0], q[0][1], q[0][2], fc.nx, fc.ny, fc.nz, col)
						emit(q[1][0], q[1][1], q[1][2], fc.nx, fc.ny, fc.nz, col)
						emit(q[2][0], q[2][1], q[2][2], fc.nx, fc.ny, fc.nz, col)
						emit(q[0][0], q[0][1], q[0][2], fc.nx, fc.ny, fc.nz, col)
						emit(q[2][0], q[2][1], q[2][2], fc.nx, fc.ny, fc.nz, col)
						emit(q[3][0], q[3][1], q[3][2], fc.nx, fc.ny, fc.nz, col)
					}
				}
			}
		}
	}

	// Second pass: overwrite each vertex normal with the averaged one.
	for o := 0; o+VoxelPackedVertexSize <= len(buf); o += VoxelPackedVertexSize {
		px := math.Float32frombits(le.Uint32(buf[o:]))
		py := math.Float32frombits(le.Uint32(buf[o+4:]))
		pz := math.Float32frombits(le.Uint32(buf[o+8:]))
		a := nacc[nkey(px, py, pz)]
		l := math.Sqrt(float64(a.x)*float64(a.x) + float64(a.y)*float64(a.y) + float64(a.z)*float64(a.z))
		nx, ny, nz := a.x, a.y, a.z
		if l > 1e-6 {
			inv := float32(1.0 / l)
			nx, ny, nz = a.x*inv, a.y*inv, a.z*inv
		}
		le.PutUint32(buf[o+12:], math.Float32bits(nx))
		le.PutUint32(buf[o+16:], math.Float32bits(ny))
		le.PutUint32(buf[o+20:], math.Float32bits(nz))
	}
	return buf
}
