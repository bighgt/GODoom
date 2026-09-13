package nodebuild

import (
	"encoding/binary"
	"math"

	"twopointfive/wad"
)

// Blockmap builds the BLOCKMAP lump: a 128x128-unit grid over the map, each
// cell listing the linedefs that touch it, for the engine's collision
// broad-phase. Layout: header (originX, originY, columns, rows — int16),
// then columns*rows int16 offsets (in words from the lump start) to each
// cell's list, then the lists themselves — each a leading 0, the linedef
// indices, and a terminating 0xFFFF.
func Blockmap(lvl *wad.Level) []byte {
	const blk = 128
	minX, minY, maxX, maxY := 1<<30, 1<<30, -(1 << 30), -(1 << 30)
	for _, v := range lvl.Vertexes {
		minX = min(minX, int(v.X))
		minY = min(minY, int(v.Y))
		maxX = max(maxX, int(v.X))
		maxY = max(maxY, int(v.Y))
	}
	if len(lvl.Vertexes) == 0 {
		minX, minY, maxX, maxY = 0, 0, blk, blk
	}
	originX := (minX & ^7) - 8
	originY := (minY & ^7) - 8
	cols := (maxX-originX)/blk + 1
	rows := (maxY-originY)/blk + 1
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}

	cells := make([][]uint16, cols*rows)
	for li := range lvl.Linedefs {
		ld := lvl.Linedefs[li]
		if int(ld.StartVertex) >= len(lvl.Vertexes) || int(ld.EndVertex) >= len(lvl.Vertexes) {
			continue
		}
		a := lvl.Vertexes[ld.StartVertex]
		b := lvl.Vertexes[ld.EndVertex]
		x0, y0 := float64(a.X), float64(a.Y)
		x1, y1 := float64(b.X), float64(b.Y)
		// walk the line in small steps, marking every cell it passes through
		steps := int(math.Hypot(x1-x0, y1-y0)/16) + 1
		last := -1
		for s := 0; s <= steps; s++ {
			t := float64(s) / float64(steps)
			cx := (int(x0+(x1-x0)*t) - originX) / blk
			cy := (int(y0+(y1-y0)*t) - originY) / blk
			if cx < 0 || cy < 0 || cx >= cols || cy >= rows {
				continue
			}
			idx := cy*cols + cx
			if idx == last {
				continue
			}
			last = idx
			if n := len(cells[idx]); n == 0 || cells[idx][n-1] != uint16(li) {
				cells[idx] = append(cells[idx], uint16(li))
			}
		}
	}

	// assemble: header(4) + offsets(cols*rows) + lists
	header := 4
	offsets := cols * rows
	words := header + offsets
	lists := make([][]uint16, cols*rows)
	for i, c := range cells {
		l := make([]uint16, 0, len(c)+2)
		l = append(l, 0)
		l = append(l, c...)
		l = append(l, 0xFFFF)
		lists[i] = l
		words += len(l)
	}

	out := make([]byte, words*2)
	put := func(wordIdx int, v uint16) { binary.LittleEndian.PutUint16(out[wordIdx*2:], v) }
	put(0, uint16(int16(originX)))
	put(1, uint16(int16(originY)))
	put(2, uint16(cols))
	put(3, uint16(rows))
	cursor := header + offsets
	for i, l := range lists {
		put(header+i, uint16(cursor))
		for _, v := range l {
			put(cursor, v)
			cursor++
		}
	}
	return out
}
