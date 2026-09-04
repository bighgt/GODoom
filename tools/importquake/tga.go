package main

import (
	"encoding/binary"
	"fmt"
	"image"
	"io"
)

// decodeTGA decodes the Targa images used by the Quake 1 texture pack into
// an *image.NRGBA. The pack is a mix of 24- and 32-bit truecolour TGAs,
// some stored raw (image type 2) and some run-length encoded (type 10),
// with either a bottom-left or top-left origin — that is the whole matrix
// this handles. Colour-mapped and greyscale TGAs (types 1/3/9/11) aren't
// used by the pack and are rejected rather than half-supported. The 2.0
// footer/extension area, when present, just sits after the pixel data and
// is ignored.
func decodeTGA(r io.Reader) (*image.NRGBA, error) {
	blob, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	if len(blob) < 18 {
		return nil, fmt.Errorf("tga: short header (%d bytes)", len(blob))
	}

	idLen := int(blob[0])
	colorMapType := blob[1]
	imageType := blob[2]
	width := int(binary.LittleEndian.Uint16(blob[12:14]))
	height := int(binary.LittleEndian.Uint16(blob[14:16]))
	depth := int(blob[16])
	descriptor := blob[17]

	if colorMapType != 0 {
		return nil, fmt.Errorf("tga: colour-mapped images not supported (colorMapType=%d)", colorMapType)
	}
	if imageType != 2 && imageType != 10 {
		return nil, fmt.Errorf("tga: unsupported image type %d (want 2 or 10, truecolour)", imageType)
	}
	if depth != 24 && depth != 32 {
		return nil, fmt.Errorf("tga: unsupported pixel depth %d (want 24 or 32)", depth)
	}
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("tga: bad dimensions %dx%d", width, height)
	}

	bpp := depth / 8
	pos := 18 + idLen
	// A colour-map spec is present (5 bytes) even for non-mapped images; it
	// sits in blob[3:8] and is already accounted for by the fixed 18-byte
	// header offset, so nothing extra to skip here.

	pixels := width * height
	raw := make([]byte, pixels*bpp) // BGRA/BGR, row 0 = first row in file order

	switch imageType {
	case 2: // uncompressed
		if pos+len(raw) > len(blob) {
			return nil, fmt.Errorf("tga: truncated pixel data (need %d, have %d)", len(raw), len(blob)-pos)
		}
		copy(raw, blob[pos:pos+len(raw)])
	case 10: // run-length encoded
		if err := decodeTGARLE(blob[pos:], raw, bpp); err != nil {
			return nil, err
		}
	}

	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	topOrigin := descriptor&0x20 != 0   // bit 5: 0 = bottom-left (flip), 1 = top-left
	rightOrigin := descriptor&0x10 != 0 // bit 4: 1 = right-to-left (rare)
	for fy := 0; fy < height; fy++ {
		dy := fy
		if !topOrigin {
			dy = height - 1 - fy
		}
		for fx := 0; fx < width; fx++ {
			dx := fx
			if rightOrigin {
				dx = width - 1 - fx
			}
			s := (fy*width + fx) * bpp
			b, g, rr := raw[s], raw[s+1], raw[s+2]
			a := byte(0xff)
			if bpp == 4 {
				a = raw[s+3]
			}
			d := img.PixOffset(dx, dy)
			img.Pix[d+0] = rr
			img.Pix[d+1] = g
			img.Pix[d+2] = b
			img.Pix[d+3] = a
		}
	}
	return img, nil
}

// decodeTGARLE expands a type-10 RLE stream into dst (already sized to
// width*height*bpp). Packets: a 1-byte header whose top bit selects RLE
// (count = low 7 bits + 1, one pixel follows, repeated) vs raw (count + 1
// literal pixels follow).
func decodeTGARLE(src, dst []byte, bpp int) error {
	di, si := 0, 0
	for di < len(dst) {
		if si >= len(src) {
			return fmt.Errorf("tga: RLE stream ended early (%d/%d bytes out)", di, len(dst))
		}
		hdr := src[si]
		si++
		count := int(hdr&0x7f) + 1
		if hdr&0x80 != 0 { // RLE packet
			if si+bpp > len(src) {
				return fmt.Errorf("tga: RLE packet truncated")
			}
			px := src[si : si+bpp]
			si += bpp
			for k := 0; k < count && di+bpp <= len(dst); k++ {
				copy(dst[di:di+bpp], px)
				di += bpp
			}
		} else { // raw packet
			n := count * bpp
			if si+n > len(src) {
				return fmt.Errorf("tga: raw packet truncated")
			}
			for k := 0; k < n && di < len(dst); k++ {
				dst[di] = src[si+k]
				di++
			}
			si += n
		}
	}
	return nil
}
