package worldgeo

import (
	"encoding/binary"
	"math"
	"testing"
)

func TestPackRoundTrip(t *testing.T) {
	g := &Geometry{Tris: []Vert{
		{X: 1.5, Y: -2, Z: 3.25, U: 0.1, V: 0.9, Light: 0.5, Nx: 1, Tex: 7, Kind: KindWall},
		{X: -100, Y: 200.75, Z: 0, U: 4.5, V: -1.5, Light: 1, Nz: -1, Tex: SkyTex, Kind: KindSky},
		{X: 0, Y: 0, Z: 64, U: 0, V: 0, Light: 0.25, Ny: 0.5, Nz: 0.5, Tex: 0, Kind: KindMasked},
	}}
	buf := g.Pack(nil)
	if len(buf) != len(g.Tris)*PackedVertexSize {
		t.Fatalf("packed length %d, want %d", len(buf), len(g.Tris)*PackedVertexSize)
	}
	le := binary.LittleEndian
	for i, src := range g.Tris {
		o := i * PackedVertexSize
		got := Vert{
			X:     math.Float32frombits(le.Uint32(buf[o:])),
			Y:     math.Float32frombits(le.Uint32(buf[o+4:])),
			Z:     math.Float32frombits(le.Uint32(buf[o+8:])),
			U:     math.Float32frombits(le.Uint32(buf[o+12:])),
			V:     math.Float32frombits(le.Uint32(buf[o+16:])),
			Light: math.Float32frombits(le.Uint32(buf[o+20:])),
			Kind:  uint16(le.Uint32(buf[o+24:])),
			Nx:    math.Float32frombits(le.Uint32(buf[o+28:])),
			Ny:    math.Float32frombits(le.Uint32(buf[o+32:])),
			Nz:    math.Float32frombits(le.Uint32(buf[o+36:])),
		}
		// Tex is builder bookkeeping — deliberately not in the packed GPU layout.
		want := src
		want.Tex = 0
		if got != want {
			t.Fatalf("vert %d round-trip: %+v != %+v", i, got, want)
		}
	}
}

func TestPackReusesBuffer(t *testing.T) {
	g := &Geometry{Tris: make([]Vert, 100)}
	b1 := g.Pack(nil)
	p1 := &b1[:1][0]
	g.Tris = g.Tris[:60]
	b2 := g.Pack(b1)
	if len(b2) != 60*PackedVertexSize {
		t.Fatalf("len %d, want %d", len(b2), 60*PackedVertexSize)
	}
	if &b2[:1][0] != p1 {
		t.Error("Pack allocated a new buffer instead of reusing the one with capacity")
	}
}

func TestVertexAttributesCoverPackedLayout(t *testing.T) {
	attrs := VertexAttributes()
	// Locations 0..3, offsets strictly increasing, last field ends within a vertex.
	sizeOf := map[string]int{"R32G32B32_SFLOAT": 12, "R32G32_SFLOAT": 8, "R32_SFLOAT": 4, "R32_UINT": 4}
	prevEnd := -1
	for i, a := range attrs {
		if a.Location != i {
			t.Fatalf("attr %d has Location %d", i, a.Location)
		}
		sz, ok := sizeOf[a.Format]
		if !ok {
			t.Fatalf("attr %d unknown format %q", i, a.Format)
		}
		if a.Offset < prevEnd {
			t.Fatalf("attr %d offset %d overlaps previous field ending at %d", i, a.Offset, prevEnd)
		}
		prevEnd = a.Offset + sz
	}
	if prevEnd != PackedVertexSize {
		t.Fatalf("attributes span %d bytes, PackedVertexSize is %d", prevEnd, PackedVertexSize)
	}
}
