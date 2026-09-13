package wad

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
)

// Writing support for the level editor: binary encoders for each map lump
// (the exact inverse of the decoders in level.go) and a minimal PWAD
// writer. The engine only reads THINGS/LINEDEFS/SIDEDEFS/VERTEXES/SEGS/
// SSECTORS/NODES/SECTORS, but a complete level block also carries REJECT
// and BLOCKMAP so other tools/ports accept the file — the caller supplies
// those (see package nodebuild).

// NamedLump is one lump for WritePWAD: a name (<= 8 chars, upper-cased) and
// its raw bytes (nil / empty for a marker lump).
type NamedLump struct {
	Name string
	Data []byte
}

// headerSize (12) and dirEntrySize (16) are defined in wad.go.

// WritePWAD writes lumps to path as a PWAD, in order.
func WritePWAD(path string, lumps []NamedLump) error {
	var body bytes.Buffer
	dir := make([]byte, 0, len(lumps)*dirEntrySize)
	for _, l := range lumps {
		pos := headerSize + body.Len()
		body.Write(l.Data)
		var e [dirEntrySize]byte
		binary.LittleEndian.PutUint32(e[0:4], uint32(pos))
		binary.LittleEndian.PutUint32(e[4:8], uint32(len(l.Data)))
		copy(e[8:16], lumpNameBytes(l.Name))
		dir = append(dir, e[:]...)
	}
	var out bytes.Buffer
	out.WriteString("PWAD")
	var h [8]byte
	binary.LittleEndian.PutUint32(h[0:4], uint32(len(lumps)))
	binary.LittleEndian.PutUint32(h[4:8], uint32(headerSize+body.Len()))
	out.Write(h[:])
	out.Write(body.Bytes())
	out.Write(dir)
	return os.WriteFile(path, out.Bytes(), 0o644)
}

func lumpNameBytes(name string) []byte {
	var b [8]byte
	for i := 0; i < len(name) && i < 8; i++ {
		c := name[i]
		if c >= 'a' && c <= 'z' {
			c -= 32
		}
		b[i] = c
	}
	return b[:]
}

// ---- per-lump encoders (inverse of level.go's decoders) -------------

func put16(w *bytes.Buffer, v int16) {
	var b [2]byte
	binary.LittleEndian.PutUint16(b[:], uint16(v))
	w.Write(b[:])
}
func putU16(w *bytes.Buffer, v uint16) {
	var b [2]byte
	binary.LittleEndian.PutUint16(b[:], v)
	w.Write(b[:])
}
func putName(w *bytes.Buffer, s string) {
	var b [8]byte
	for i := 0; i < len(s) && i < 8; i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' {
			c -= 32
		}
		b[i] = c
	}
	if s == "-" {
		b[0] = '-'
	}
	w.Write(b[:])
}

// EncodeVertexes / EncodeLinedefs / ... produce the on-disk lump bytes.
func EncodeVertexes(v []Vertex) []byte {
	w := bytes.NewBuffer(make([]byte, 0, len(v)*4))
	for _, x := range v {
		put16(w, x.X)
		put16(w, x.Y)
	}
	return w.Bytes()
}

func EncodeLinedefs(ld []Linedef) []byte {
	w := bytes.NewBuffer(make([]byte, 0, len(ld)*14))
	for _, l := range ld {
		putU16(w, l.StartVertex)
		putU16(w, l.EndVertex)
		putU16(w, l.Flags)
		putU16(w, l.SpecialType)
		putU16(w, l.SectorTag)
		putU16(w, l.FrontSidedef)
		putU16(w, l.BackSidedef)
	}
	return w.Bytes()
}

func EncodeSidedefs(sd []Sidedef) []byte {
	w := bytes.NewBuffer(make([]byte, 0, len(sd)*30))
	for _, s := range sd {
		put16(w, s.XOffset)
		put16(w, s.YOffset)
		putName(w, s.UpperTexture)
		putName(w, s.LowerTexture)
		putName(w, s.MiddleTexture)
		putU16(w, s.Sector)
	}
	return w.Bytes()
}

func EncodeSectors(sc []Sector) []byte {
	w := bytes.NewBuffer(make([]byte, 0, len(sc)*26))
	for _, s := range sc {
		put16(w, s.FloorHeight)
		put16(w, s.CeilingHeight)
		putName(w, s.FloorTexture)
		putName(w, s.CeilingTexture)
		put16(w, s.LightLevel)
		putU16(w, s.SpecialType)
		putU16(w, s.Tag)
	}
	return w.Bytes()
}

func EncodeThings(th []Thing) []byte {
	w := bytes.NewBuffer(make([]byte, 0, len(th)*10))
	for _, t := range th {
		put16(w, t.X)
		put16(w, t.Y)
		putU16(w, t.Angle)
		putU16(w, t.Type)
		putU16(w, t.Flags)
	}
	return w.Bytes()
}

func EncodeSegs(sg []Seg) []byte {
	w := bytes.NewBuffer(make([]byte, 0, len(sg)*12))
	for _, s := range sg {
		putU16(w, s.StartVertex)
		putU16(w, s.EndVertex)
		putU16(w, 0) // BAM angle — the renderer derives orientation from the vertices
		putU16(w, s.Linedef)
		put16(w, s.Direction)
		put16(w, s.Offset)
	}
	return w.Bytes()
}

func EncodeSubsectors(ss []Subsector) []byte {
	w := bytes.NewBuffer(make([]byte, 0, len(ss)*4))
	for _, s := range ss {
		putU16(w, s.SegCount)
		putU16(w, s.FirstSeg)
	}
	return w.Bytes()
}

func EncodeNodes(nd []Node) []byte {
	w := bytes.NewBuffer(make([]byte, 0, len(nd)*28))
	for _, n := range nd {
		put16(w, n.X)
		put16(w, n.Y)
		put16(w, n.DX)
		put16(w, n.DY)
		// two child bounding boxes (top, bottom, left, right) — the engine
		// ignores them; a real builder fills them, so a caller that has the
		// data can carry it via the Node's own fields if it grows them.
		for i := 0; i < 8; i++ {
			put16(w, 0)
		}
		putU16(w, n.RightChild)
		putU16(w, n.LeftChild)
	}
	return w.Bytes()
}

// MapMarkerLumps returns the 11-lump block for a vanilla map, ready for
// WritePWAD: the marker lump (name, empty) followed by the map lumps in the
// order the engine's loader expects. reject and blockmap are supplied by
// the caller (nodebuild.BuildReject / BuildBlockmap).
func MapMarkerLumps(name string, lvl *Level, segs []byte, ssectors []byte, nodes []byte, reject, blockmap []byte) []NamedLump {
	return []NamedLump{
		{Name: name},
		{Name: "THINGS", Data: EncodeThings(lvl.Things)},
		{Name: "LINEDEFS", Data: EncodeLinedefs(lvl.Linedefs)},
		{Name: "SIDEDEFS", Data: EncodeSidedefs(lvl.Sidedefs)},
		{Name: "VERTEXES", Data: EncodeVertexes(lvl.Vertexes)},
		{Name: "SEGS", Data: segs},
		{Name: "SSECTORS", Data: ssectors},
		{Name: "NODES", Data: nodes},
		{Name: "SECTORS", Data: EncodeSectors(lvl.Sectors)},
		{Name: "REJECT", Data: reject},
		{Name: "BLOCKMAP", Data: blockmap},
	}
}

var _ = fmt.Sprint
