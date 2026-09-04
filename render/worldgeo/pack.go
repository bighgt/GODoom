package worldgeo

import (
	"encoding/binary"
	"math"
)

// PackedVertexSize is one vertex's byte size in Pack's output: eight float32
// (position xyz, uv, light, normal xyz) with one uint32 (kind) between light
// and normal. 40 bytes, every field 4-byte aligned — a straight Vulkan
// vertex buffer. The texture is bound per draw (Geometry.Draws), not per
// vertex.
const PackedVertexSize = 40

// Pack serialises g.Tris (the per-frame dynamic geometry) into a
// little-endian GPU vertex buffer. Reuses buf when it has the capacity.
func (g *Geometry) Pack(buf []byte) []byte { return PackVerts(g.Tris, buf) }

// PackVerts serialises verts into a little-endian GPU vertex buffer, one
// PackedVertexSize block per vertex, reusing buf when it has the capacity.
func PackVerts(verts []Vert, buf []byte) []byte {
	need := len(verts) * PackedVertexSize
	if cap(buf) < need {
		buf = make([]byte, need)
	} else {
		buf = buf[:need]
	}
	le := binary.LittleEndian
	for i := range verts {
		v := &verts[i]
		o := i * PackedVertexSize
		le.PutUint32(buf[o:], math.Float32bits(v.X))
		le.PutUint32(buf[o+4:], math.Float32bits(v.Y))
		le.PutUint32(buf[o+8:], math.Float32bits(v.Z))
		le.PutUint32(buf[o+12:], math.Float32bits(v.U))
		le.PutUint32(buf[o+16:], math.Float32bits(v.V))
		le.PutUint32(buf[o+20:], math.Float32bits(v.Light))
		le.PutUint32(buf[o+24:], uint32(v.Kind))
		le.PutUint32(buf[o+28:], math.Float32bits(v.Nx))
		le.PutUint32(buf[o+32:], math.Float32bits(v.Ny))
		le.PutUint32(buf[o+36:], math.Float32bits(v.Nz))
	}
	return buf
}

// VertexAttribute describes one shader input for the packed layout: its
// GLSL `location`, its Vulkan format name, and its byte offset within a
// PackedVertexSize vertex. The backend maps Format to a vk.Format.
type VertexAttribute struct {
	Location int
	Format   string
	Offset   int
}

// VertexAttributes is the packed layout's attribute set, in location order:
//
//	0  vec3  position   (world, map units)
//	1  vec2  uv         (texture tiles, 0..1)
//	2  float light      (sector light 0..1, fake contrast folded in for walls)
//	3  uint  kind       (Kind* — the shader keys texturing / alpha-test / sky off it)
//	4  vec3  normal      (unit world-space surface normal, for the dynamic-light Lambert term)
func VertexAttributes() []VertexAttribute {
	return []VertexAttribute{
		{Location: 0, Format: "R32G32B32_SFLOAT", Offset: 0},
		{Location: 1, Format: "R32G32_SFLOAT", Offset: 12},
		{Location: 2, Format: "R32_SFLOAT", Offset: 20},
		{Location: 3, Format: "R32_UINT", Offset: 24},
		{Location: 4, Format: "R32G32B32_SFLOAT", Offset: 28},
	}
}
