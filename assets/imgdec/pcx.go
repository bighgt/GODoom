package imgdec

import (
	"encoding/binary"
	"fmt"
	"image"
	"io"
)

// DecodePCX decodes a ZSoft PCX image: 8-bit palettised (one plane, the
// 256-colour palette after a 0x0C marker at the end of the file) or 24-bit
// (three planes), run-length encoded — the two variants model packs use
// for their skins.
func DecodePCX(r io.Reader) (*image.NRGBA, error) {
	blob, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	if len(blob) < 128 || blob[0] != 10 || blob[2] != 1 {
		return nil, fmt.Errorf("pcx: not an RLE PCX file")
	}
	bpp, planes := int(blob[3]), int(blob[65])
	x0, y0 := int(binary.LittleEndian.Uint16(blob[4:])), int(binary.LittleEndian.Uint16(blob[6:]))
	x1, y1 := int(binary.LittleEndian.Uint16(blob[8:])), int(binary.LittleEndian.Uint16(blob[10:]))
	bpl := int(binary.LittleEndian.Uint16(blob[66:]))
	w, h := x1-x0+1, y1-y0+1
	if bpp != 8 || (planes != 1 && planes != 3) || w <= 0 || h <= 0 || bpl < w {
		return nil, fmt.Errorf("pcx: unsupported %d-bit %d-plane %dx%d", bpp, planes, w, h)
	}

	// Decode the RLE scanlines: each row is planes*bpl bytes.
	rowLen := planes * bpl
	raw := make([]byte, rowLen*h)
	src := blob[128:]
	for di := 0; di < len(raw); {
		if len(src) == 0 {
			return nil, fmt.Errorf("pcx: truncated image data")
		}
		b := src[0]
		src = src[1:]
		n := 1
		if b&0xC0 == 0xC0 {
			n = int(b & 0x3F)
			if len(src) == 0 {
				return nil, fmt.Errorf("pcx: truncated run")
			}
			b = src[0]
			src = src[1:]
		}
		for ; n > 0 && di < len(raw); n-- {
			raw[di] = b
			di++
		}
	}

	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	if planes == 3 {
		for y := 0; y < h; y++ {
			row := raw[y*rowLen:]
			for x := 0; x < w; x++ {
				o := y*img.Stride + x*4
				img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = row[x], row[bpl+x], row[2*bpl+x], 255
			}
		}
		return img, nil
	}
	// The 256-colour palette is the file's last 768 bytes, normally after a
	// 0x0C marker — which some tools don't write, so the marker isn't
	// required (GZDoom doesn't either).
	if len(blob) < 128+768 {
		return nil, fmt.Errorf("pcx: 8-bit image without a 256-colour palette")
	}
	pal := blob[len(blob)-768:]
	for y := 0; y < h; y++ {
		row := raw[y*rowLen:]
		for x := 0; x < w; x++ {
			c := int(row[x]) * 3
			o := y*img.Stride + x*4
			img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = pal[c], pal[c+1], pal[c+2], 255
		}
	}
	return img, nil
}
