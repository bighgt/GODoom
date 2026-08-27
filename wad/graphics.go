package wad

import (
	"encoding/binary"
	"fmt"
)

// RGB is one 24-bit palette entry.
type RGB struct {
	R, G, B byte
}

// Palette is one of the PLAYPAL lump's 256-color palettes. Index 0 is the
// normal palette; the rest are used for effects like taking damage, picking
// up items, or wearing a radiation suit.
type Palette [256]RGB

// LoadPlaypal decodes the PLAYPAL lump (14 palettes x 256 colors x 3 bytes
// in vanilla Doom, though this only assumes the size is a multiple of a
// single palette's byte size).
func LoadPlaypal(b []byte) ([]Palette, error) {
	const paletteSize = 256 * 3
	if len(b) == 0 || len(b)%paletteSize != 0 {
		return nil, fmt.Errorf("wad: PLAYPAL size %d is not a multiple of %d bytes", len(b), paletteSize)
	}
	n := len(b) / paletteSize
	palettes := make([]Palette, n)
	for p := 0; p < n; p++ {
		for c := 0; c < 256; c++ {
			o := p*paletteSize + c*3
			palettes[p][c] = RGB{R: b[o], G: b[o+1], B: b[o+2]}
		}
	}
	return palettes, nil
}

// Colormap is one of the 34 light/effect shading tables in the COLORMAP
// lump: 256 palette-index -> palette-index remaps, one per light level.
type Colormap [256]byte

// LoadColormap decodes the COLORMAP lump.
func LoadColormap(b []byte) ([]Colormap, error) {
	if len(b) == 0 || len(b)%256 != 0 {
		return nil, fmt.Errorf("wad: COLORMAP size %d is not a multiple of 256 bytes", len(b))
	}
	n := len(b) / 256
	maps := make([]Colormap, n)
	for i := 0; i < n; i++ {
		copy(maps[i][:], b[i*256:i*256+256])
	}
	return maps, nil
}

// Patch is a decoded Doom "picture" — the column-major, run-length-encoded
// graphic format used for wall textures, sprites, and UI elements. Pixels
// hold palette indices; -1 marks a transparent (unset) pixel.
type Patch struct {
	Width, Height         int
	LeftOffset, TopOffset int
	// Pixels is Width*Height palette indices in row-major order.
	Pixels []int16
}

// DecodePatch parses a lump in Doom's "picture format": an 8-byte header
// (width, height, left/top offset), a per-column array of post offsets, and
// for each column a run of "posts" — vertical spans of opaque pixels
// separated by transparent gaps, each post prefixed by its starting row and
// length and padded by one unused byte on either side of the pixel data.
func DecodePatch(b []byte) (*Patch, error) {
	if len(b) < 8 {
		return nil, fmt.Errorf("wad: patch lump too small (%d bytes)", len(b))
	}
	width := int(binary.LittleEndian.Uint16(b[0:2]))
	height := int(binary.LittleEndian.Uint16(b[2:4]))
	left := int(int16(binary.LittleEndian.Uint16(b[4:6])))
	top := int(int16(binary.LittleEndian.Uint16(b[6:8])))
	if width <= 0 || height <= 0 || 8+width*4 > len(b) {
		return nil, fmt.Errorf("wad: patch has invalid dimensions %dx%d for a %d byte lump", width, height, len(b))
	}

	pixels := make([]int16, width*height)
	for i := range pixels {
		pixels[i] = -1
	}

	const colOfsBase = 8
	for x := 0; x < width; x++ {
		o := colOfsBase + x*4
		colOfs := int(binary.LittleEndian.Uint32(b[o : o+4]))

		for {
			if colOfs+1 >= len(b) {
				break
			}
			topDelta := b[colOfs]
			if topDelta == 0xFF { // end-of-column marker
				break
			}
			length := int(b[colOfs+1])
			dataOfs := colOfs + 3 // one padding byte before the pixel run
			if dataOfs+length > len(b) {
				break
			}
			for i := 0; i < length; i++ {
				y := int(topDelta) + i
				if y >= 0 && y < height {
					pixels[y*width+x] = int16(b[dataOfs+i])
				}
			}
			colOfs = dataOfs + length + 1 // one padding byte after the pixel run
		}
	}

	return &Patch{Width: width, Height: height, LeftOffset: left, TopOffset: top, Pixels: pixels}, nil
}
