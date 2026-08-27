package wad

import (
	"encoding/binary"
	"fmt"
)

// NoSidedef marks a Linedef side (Front/BackSidedef) as absent — the line
// borders the void or is one-sided.
const NoSidedef = 0xFFFF

// SubsectorBit, when set on a Node's child index, marks that child as a leaf:
// the remaining 15 bits index into Subsectors rather than Nodes. This is the
// exact bit Doom's own renderer used to tell BSP nodes and leaves apart.
const SubsectorBit = 0x8000

// Linedef.Flags bits relevant to rendering, from the original Doom Specs
// (unchanged since 1993). UpperUnpegged/LowerUnpegged control texture
// alignment — see raster.Renderer.drawWallSpan, ported from PrBoom's r_segs.c.
const (
	LinedefUpperUnpegged = 0x0008
	LinedefLowerUnpegged = 0x0010
)

// Vertex is one entry of the VERTEXES lump (4 bytes): a 2D map-space point.
type Vertex struct {
	X, Y int16
}

// Linedef is one entry of the LINEDEFS lump (14 bytes): a wall segment
// between two vertices, with up to two Sidedefs (front/back) describing
// what's drawn on each face.
type Linedef struct {
	StartVertex, EndVertex    uint16
	Flags                     uint16
	SpecialType               uint16
	SectorTag                 uint16
	FrontSidedef, BackSidedef uint16 // NoSidedef if absent
}

// Sidedef is one entry of the SIDEDEFS lump (30 bytes): the texturing of one
// face of a Linedef, and which Sector it belongs to.
type Sidedef struct {
	XOffset, YOffset                          int16
	UpperTexture, LowerTexture, MiddleTexture string
	Sector                                    uint16
}

// Sector is one entry of the SECTORS lump (26 bytes): a floor/ceiling height
// pair, their textures ("flats"), light level, and any special behavior.
type Sector struct {
	FloorHeight, CeilingHeight   int16
	FloorTexture, CeilingTexture string
	LightLevel                   int16
	SpecialType                  uint16
	Tag                          uint16
}

// Thing is one entry of the THINGS lump (10 bytes): a placed monster, item,
// decoration, or player start.
type Thing struct {
	X, Y  int16
	Angle uint16
	Type  uint16
	Flags uint16
}

// Seg is one entry of the SEGS lump (12 bytes): one straight fragment of a
// Subsector's boundary, referencing the Linedef it runs along.
type Seg struct {
	StartVertex, EndVertex uint16
	Angle                  int16 // binary angle measure (BAM): 0..65535 covers 0..360°
	Linedef                uint16
	Direction              int16 // 0 = same direction as the linedef, 1 = opposite
	Offset                 int16
}

// Subsector is one entry of the SSECTORS lump (4 bytes): a convex leaf of
// the BSP tree, described as a run of consecutive Segs.
type Subsector struct {
	SegCount uint16
	FirstSeg uint16
}

// Node is one entry of the NODES lump (28 bytes): a single split of the
// precomputed BSP tree — a partition line, the bounding boxes of both
// children, and the two child indices themselves (see SubsectorBit).
type Node struct {
	X, Y                  int16    // partition line start point
	DX, DY                int16    // partition line direction vector
	RightBBox, LeftBBox   [4]int16 // each: top, bottom, left, right
	RightChild, LeftChild uint16
}

// Level is one map's full geometry and gameplay data, assembled from the
// fixed sequence of lumps vanilla Doom stores after a map marker (E1M1,
// MAP01, ...): THINGS, LINEDEFS, SIDEDEFS, VERTEXES, SEGS, SSECTORS, NODES,
// SECTORS, REJECT, BLOCKMAP.
type Level struct {
	Name string

	Things     []Thing
	Linedefs   []Linedef
	Sidedefs   []Sidedef
	Vertexes   []Vertex
	Segs       []Seg
	Subsectors []Subsector
	Nodes      []Node
	Sectors    []Sector

	Reject   []byte // sector-pair visibility bit table, used by AI/sound
	Blockmap []byte // spatial hash grid, used for collision
}

// LoadLevel assembles the Level for the map marked by mapName (e.g. "E1M1"
// or "MAP01"). It walks the directory forward from the marker, decoding each
// recognized lump as it's found, and stops at the first lump that doesn't
// belong to a level block (typically the next map's marker).
func (w *WAD) LoadLevel(mapName string) (*Level, error) {
	idx := w.IndexOf(mapName)
	if idx < 0 {
		return nil, fmt.Errorf("wad: no map marker lump named %q", mapName)
	}

	lvl := &Level{Name: mapName}
	for i := idx + 1; i < len(w.Entries); i++ {
		e := w.Entries[i]
		switch e.Name {
		case "THINGS":
			lvl.Things = decodeThings(w.Lump(i))
		case "LINEDEFS":
			lvl.Linedefs = decodeLinedefs(w.Lump(i))
		case "SIDEDEFS":
			lvl.Sidedefs = decodeSidedefs(w.Lump(i))
		case "VERTEXES":
			lvl.Vertexes = decodeVertexes(w.Lump(i))
		case "SEGS":
			lvl.Segs = decodeSegs(w.Lump(i))
		case "SSECTORS":
			lvl.Subsectors = decodeSubsectors(w.Lump(i))
		case "NODES":
			lvl.Nodes = decodeNodes(w.Lump(i))
		case "SECTORS":
			lvl.Sectors = decodeSectors(w.Lump(i))
		case "REJECT":
			lvl.Reject = w.Lump(i)
		case "BLOCKMAP":
			lvl.Blockmap = w.Lump(i)
		default:
			// Reached a lump that isn't part of the fixed level block (most
			// often the next map's own marker) — this level is complete.
			return finishLevel(lvl)
		}
	}
	return finishLevel(lvl)
}

func finishLevel(lvl *Level) (*Level, error) {
	if len(lvl.Vertexes) == 0 || len(lvl.Linedefs) == 0 || len(lvl.Sectors) == 0 {
		return nil, fmt.Errorf("wad: map %q is missing required geometry lumps", lvl.Name)
	}
	if len(lvl.Nodes) == 0 || len(lvl.Subsectors) == 0 {
		return nil, fmt.Errorf("wad: map %q has no precomputed BSP (NODES/SSECTORS) — not a vanilla-format map", lvl.Name)
	}
	return lvl, nil
}

func decodeVertexes(b []byte) []Vertex {
	n := len(b) / 4
	out := make([]Vertex, n)
	for i := 0; i < n; i++ {
		o := i * 4
		out[i] = Vertex{
			X: int16(binary.LittleEndian.Uint16(b[o : o+2])),
			Y: int16(binary.LittleEndian.Uint16(b[o+2 : o+4])),
		}
	}
	return out
}

func decodeLinedefs(b []byte) []Linedef {
	n := len(b) / 14
	out := make([]Linedef, n)
	for i := 0; i < n; i++ {
		o := i * 14
		out[i] = Linedef{
			StartVertex:  binary.LittleEndian.Uint16(b[o : o+2]),
			EndVertex:    binary.LittleEndian.Uint16(b[o+2 : o+4]),
			Flags:        binary.LittleEndian.Uint16(b[o+4 : o+6]),
			SpecialType:  binary.LittleEndian.Uint16(b[o+6 : o+8]),
			SectorTag:    binary.LittleEndian.Uint16(b[o+8 : o+10]),
			FrontSidedef: binary.LittleEndian.Uint16(b[o+10 : o+12]),
			BackSidedef:  binary.LittleEndian.Uint16(b[o+12 : o+14]),
		}
	}
	return out
}

func decodeSidedefs(b []byte) []Sidedef {
	n := len(b) / 30
	out := make([]Sidedef, n)
	for i := 0; i < n; i++ {
		o := i * 30
		out[i] = Sidedef{
			XOffset:       int16(binary.LittleEndian.Uint16(b[o : o+2])),
			YOffset:       int16(binary.LittleEndian.Uint16(b[o+2 : o+4])),
			UpperTexture:  cleanName(b[o+4 : o+12]),
			LowerTexture:  cleanName(b[o+12 : o+20]),
			MiddleTexture: cleanName(b[o+20 : o+28]),
			Sector:        binary.LittleEndian.Uint16(b[o+28 : o+30]),
		}
	}
	return out
}

func decodeSectors(b []byte) []Sector {
	n := len(b) / 26
	out := make([]Sector, n)
	for i := 0; i < n; i++ {
		o := i * 26
		out[i] = Sector{
			FloorHeight:    int16(binary.LittleEndian.Uint16(b[o : o+2])),
			CeilingHeight:  int16(binary.LittleEndian.Uint16(b[o+2 : o+4])),
			FloorTexture:   cleanName(b[o+4 : o+12]),
			CeilingTexture: cleanName(b[o+12 : o+20]),
			LightLevel:     int16(binary.LittleEndian.Uint16(b[o+20 : o+22])),
			SpecialType:    binary.LittleEndian.Uint16(b[o+22 : o+24]),
			Tag:            binary.LittleEndian.Uint16(b[o+24 : o+26]),
		}
	}
	return out
}

func decodeThings(b []byte) []Thing {
	n := len(b) / 10
	out := make([]Thing, n)
	for i := 0; i < n; i++ {
		o := i * 10
		out[i] = Thing{
			X:     int16(binary.LittleEndian.Uint16(b[o : o+2])),
			Y:     int16(binary.LittleEndian.Uint16(b[o+2 : o+4])),
			Angle: binary.LittleEndian.Uint16(b[o+4 : o+6]),
			Type:  binary.LittleEndian.Uint16(b[o+6 : o+8]),
			Flags: binary.LittleEndian.Uint16(b[o+8 : o+10]),
		}
	}
	return out
}

func decodeSegs(b []byte) []Seg {
	n := len(b) / 12
	out := make([]Seg, n)
	for i := 0; i < n; i++ {
		o := i * 12
		out[i] = Seg{
			StartVertex: binary.LittleEndian.Uint16(b[o : o+2]),
			EndVertex:   binary.LittleEndian.Uint16(b[o+2 : o+4]),
			Angle:       int16(binary.LittleEndian.Uint16(b[o+4 : o+6])),
			Linedef:     binary.LittleEndian.Uint16(b[o+6 : o+8]),
			Direction:   int16(binary.LittleEndian.Uint16(b[o+8 : o+10])),
			Offset:      int16(binary.LittleEndian.Uint16(b[o+10 : o+12])),
		}
	}
	return out
}

func decodeSubsectors(b []byte) []Subsector {
	n := len(b) / 4
	out := make([]Subsector, n)
	for i := 0; i < n; i++ {
		o := i * 4
		out[i] = Subsector{
			SegCount: binary.LittleEndian.Uint16(b[o : o+2]),
			FirstSeg: binary.LittleEndian.Uint16(b[o+2 : o+4]),
		}
	}
	return out
}

func decodeNodes(b []byte) []Node {
	n := len(b) / 28
	out := make([]Node, n)
	for i := 0; i < n; i++ {
		o := i * 28
		rd16 := func(k int) int16 { return int16(binary.LittleEndian.Uint16(b[o+k : o+k+2])) }
		out[i] = Node{
			X:          rd16(0),
			Y:          rd16(2),
			DX:         rd16(4),
			DY:         rd16(6),
			RightBBox:  [4]int16{rd16(8), rd16(10), rd16(12), rd16(14)},
			LeftBBox:   [4]int16{rd16(16), rd16(18), rd16(20), rd16(22)},
			RightChild: binary.LittleEndian.Uint16(b[o+24 : o+26]),
			LeftChild:  binary.LittleEndian.Uint16(b[o+26 : o+28]),
		}
	}
	return out
}
