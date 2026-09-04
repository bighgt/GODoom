package assets

import (
	"encoding/binary"
	"fmt"
)

// VoxelModel is one decoded KVX voxel model (Ken Silverman's format, as used
// by the Build engine and GZDoom voxel packs). Only mip level 0 is kept, and
// the palette is already resolved to RGB.
//
// Storage is column-major run-length "slabs" — the same shape KVX uses on
// disk, which is already just the model's visible surface shell (fully
// interior, occluded-on-all-sides voxels are omitted by the exporter). That
// keeps a monster at a few thousand voxels rather than tens of thousands.
type VoxelModel struct {
	XSiz, YSiz, ZSiz int

	// Pivot is the model origin in voxel units (KVX xpivot/ypivot/zpivot,
	// converted from 8.8 fixed point). KVX Z runs downward with the pivot at
	// the model's base, so a voxel at grid z sits (ZPivot-(z+0.5)) voxels
	// above the thing's feet.
	XPivot, YPivot, ZPivot float64

	// Cols[x*YSiz + y] is that column's slabs, bottom (smallest ZTop) first.
	Cols [][]VoxSlab
}

// VoxSlab is one vertical run of solid voxels in a column: Colors[i] is the
// voxel at grid z = ZTop+i. Face carries KVX's per-slab visible-face bits
// (0x10 = top exposed, 0x20 = bottom exposed, the low nibble = the four
// sides) — enough for a cheap top-lit shading term.
type VoxSlab struct {
	ZTop   int
	Face   byte
	Colors [][3]byte
}

// Slabs returns the slabs of grid column (x, y), or nil if out of range.
func (m *VoxelModel) Slabs(x, y int) []VoxSlab {
	if x < 0 || y < 0 || x >= m.XSiz || y >= m.YSiz {
		return nil
	}
	return m.Cols[x*m.YSiz+y]
}

// VoxelCount is the total solid voxel count — used by tests and logging.
func (m *VoxelModel) VoxelCount() int {
	n := 0
	for _, col := range m.Cols {
		for _, s := range col {
			n += len(s.Colors)
		}
	}
	return n
}

// decodeKVX parses a KVX lump: an int32 mip-0 byte count, then that mip
// (int32 xsiz/ysiz/zsiz, int32 xpivot/ypivot/zpivot in 8.8 fixed point, an
// int32 xoffset[xsiz+1] table, an int16 xyoffset[xsiz][ysiz+1] table, then
// the slab bytes), then any lower mips, then a trailing 768-byte palette
// (256 RGB triplets — VGA 0..63 in every Doom voxel pack, scaled up here).
func decodeKVX(b []byte) (*VoxelModel, error) {
	const palBytes = 768
	if len(b) < 4+24+palBytes {
		return nil, fmt.Errorf("KVX too small (%d bytes)", len(b))
	}
	le := binary.LittleEndian

	numBytes := int(le.Uint32(b[0:4]))
	if numBytes < 24 || 4+numBytes > len(b) {
		return nil, fmt.Errorf("KVX mip-0 length %d out of range for a %d-byte lump", numBytes, len(b))
	}
	body := b[4 : 4+numBytes]

	xs := int(int32(le.Uint32(body[0:4])))
	ys := int(int32(le.Uint32(body[4:8])))
	zs := int(int32(le.Uint32(body[8:12])))
	if xs <= 0 || ys <= 0 || zs <= 0 || xs > 1024 || ys > 1024 || zs > 1024 {
		return nil, fmt.Errorf("KVX bad dimensions %dx%dx%d", xs, ys, zs)
	}
	xp := float64(int32(le.Uint32(body[12:16]))) / 256
	yp := float64(int32(le.Uint32(body[16:20]))) / 256
	zp := float64(int32(le.Uint32(body[20:24]))) / 256

	// Palette: the last 768 bytes of the whole lump.
	pal := b[len(b)-palBytes:]
	maxComp := byte(0)
	for _, v := range pal {
		if v > maxComp {
			maxComp = v
		}
	}
	var lut [256][3]byte
	for i := 0; i < 256; i++ {
		r, g, bl := pal[i*3], pal[i*3+1], pal[i*3+2]
		if maxComp <= 63 { // VGA 6-bit -> 8-bit, replicating the top bits
			r = r<<2 | r>>4
			g = g<<2 | g>>4
			bl = bl<<2 | bl>>4
		}
		lut[i] = [3]byte{r, g, bl}
	}

	off := 24
	tableBytes := 4*(xs+1) + 2*xs*(ys+1)
	if off+tableBytes > len(body) {
		return nil, fmt.Errorf("KVX offset tables truncated")
	}
	xoffset := make([]int, xs+1)
	for i := range xoffset {
		xoffset[i] = int(int32(le.Uint32(body[off : off+4])))
		off += 4
	}
	xyoffset := make([]int, xs*(ys+1))
	for i := range xyoffset {
		xyoffset[i] = int(int16(le.Uint16(body[off : off+2])))
		off += 2
	}
	slab := body[off:]
	// xoffset[] is stored relative to the start of the two offset tables;
	// rebase it onto slab[].
	for i := range xoffset {
		xoffset[i] -= tableBytes
	}

	m := &VoxelModel{
		XSiz: xs, YSiz: ys, ZSiz: zs,
		XPivot: xp, YPivot: yp, ZPivot: zp,
		Cols: make([][]VoxSlab, xs*ys),
	}
	for x := 0; x < xs; x++ {
		for y := 0; y < ys; y++ {
			start := xoffset[x] + xyoffset[x*(ys+1)+y]
			end := xoffset[x] + xyoffset[x*(ys+1)+y+1]
			if start < 0 || end > len(slab) || start >= end {
				continue // tolerate a malformed/empty column
			}
			var slabs []VoxSlab
			for p := start; p+3 <= end; {
				ztop := int(slab[p])
				zleng := int(slab[p+1])
				face := slab[p+2]
				p += 3
				if zleng <= 0 || p+zleng > end {
					break
				}
				cols := make([][3]byte, zleng)
				for i := 0; i < zleng; i++ {
					cols[i] = lut[slab[p+i]]
				}
				p += zleng
				slabs = append(slabs, VoxSlab{ZTop: ztop, Face: face, Colors: cols})
			}
			m.Cols[x*ys+y] = slabs
		}
	}
	return m, nil
}
