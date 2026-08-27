package wad

import (
	"encoding/binary"
	"fmt"
)

// LoadPnames decodes the PNAMES lump: a flat list of patch lump names that
// TEXTURE1/TEXTURE2 entries reference by index. Format: int32 count,
// followed by count 8-byte zero-padded ASCII names.
func LoadPnames(b []byte) ([]string, error) {
	if len(b) < 4 {
		return nil, fmt.Errorf("wad: PNAMES lump too small (%d bytes)", len(b))
	}
	count := int32(binary.LittleEndian.Uint32(b[0:4]))
	if count < 0 || 4+int64(count)*8 > int64(len(b)) {
		return nil, fmt.Errorf("wad: PNAMES header claims %d names but the lump is only %d bytes", count, len(b))
	}
	names := make([]string, count)
	for i := int32(0); i < count; i++ {
		o := 4 + int(i)*8
		names[i] = cleanName(b[o : o+8])
	}
	return names, nil
}

// TexturePatch is one patch placement within a composite TextureDef —
// mappatch_t in id's original source: which PNAMES-indexed patch, and where
// its top-left corner sits within the composite texture's canvas.
type TexturePatch struct {
	OriginX, OriginY int16
	PatchIndex       int16 // index into the PNAMES list
}

// TextureDef is one composite wall texture definition from TEXTURE1/TEXTURE2
// (maptexture_t) — a canvas size plus the patches stacked onto it to build
// the final texture. Doom does this so repeated decorative elements (a
// switch, a light fixture) are stored once as a small patch and reused
// across many wall textures instead of duplicating pixels.
type TextureDef struct {
	Name          string
	Width, Height int
	Patches       []TexturePatch
}

// LoadTextureDefs decodes a TEXTURE1 or TEXTURE2 lump: int32 numTextures,
// then numTextures int32 offsets (each pointing at a maptexture_t within
// this same lump), then the maptexture_t records themselves — 22-byte
// header (name[8], masked int32 (unused), width int16, height int16,
// columndirectory int32 (unused/obsolete), patchCount int16), followed by
// patchCount 10-byte mappatch_t entries (originX int16, originY int16,
// patch int16, stepdir int16 (unused), colormap int16 (unused)).
func LoadTextureDefs(b []byte) ([]TextureDef, error) {
	if len(b) < 4 {
		return nil, fmt.Errorf("wad: TEXTUREx lump too small (%d bytes)", len(b))
	}
	numTextures := int32(binary.LittleEndian.Uint32(b[0:4]))
	if numTextures < 0 || 4+int64(numTextures)*4 > int64(len(b)) {
		return nil, fmt.Errorf("wad: TEXTUREx header claims %d textures but the lump is only %d bytes", numTextures, len(b))
	}

	defs := make([]TextureDef, numTextures)
	for i := int32(0); i < numTextures; i++ {
		ofsField := 4 + int(i)*4
		o := int(binary.LittleEndian.Uint32(b[ofsField : ofsField+4]))
		if o < 0 || o+22 > len(b) {
			return nil, fmt.Errorf("wad: TEXTUREx entry %d has an out-of-range offset %d", i, o)
		}

		name := cleanName(b[o : o+8])
		width := int(int16(binary.LittleEndian.Uint16(b[o+12 : o+14])))
		height := int(int16(binary.LittleEndian.Uint16(b[o+14 : o+16])))
		patchCount := int(int16(binary.LittleEndian.Uint16(b[o+20 : o+22])))

		patches := make([]TexturePatch, 0, patchCount)
		po := o + 22
		for p := 0; p < patchCount; p++ {
			if po+10 > len(b) {
				return nil, fmt.Errorf("wad: TEXTUREx entry %q's patch list runs past the end of the lump", name)
			}
			patches = append(patches, TexturePatch{
				OriginX:    int16(binary.LittleEndian.Uint16(b[po : po+2])),
				OriginY:    int16(binary.LittleEndian.Uint16(b[po+2 : po+4])),
				PatchIndex: int16(binary.LittleEndian.Uint16(b[po+4 : po+6])),
			})
			po += 10
		}

		defs[i] = TextureDef{Name: name, Width: width, Height: height, Patches: patches}
	}
	return defs, nil
}

// ComposeTexture builds a TextureDef's full pixel canvas by decoding and
// stamping each of its patches in order, exactly as the original engine's
// texture cache did: later patches draw over earlier ones, but only where
// the later patch's own pixels are opaque — a patch's transparent pixels
// never erase what's already on the canvas.
func (w *WAD) ComposeTexture(def TextureDef, pnames []string) (*Patch, error) {
	if def.Width <= 0 || def.Height <= 0 {
		return nil, fmt.Errorf("wad: texture %q has invalid size %dx%d", def.Name, def.Width, def.Height)
	}
	canvas := make([]int16, def.Width*def.Height)
	for i := range canvas {
		canvas[i] = -1
	}

	for _, p := range def.Patches {
		if int(p.PatchIndex) < 0 || int(p.PatchIndex) >= len(pnames) {
			continue // a handful of shipped IWAD textures reference nonexistent PNAMES slots; skip rather than fail the whole texture
		}
		raw, ok := w.Find(pnames[p.PatchIndex])
		if !ok {
			continue
		}
		patch, err := DecodePatch(raw)
		if err != nil {
			continue
		}

		for y := 0; y < patch.Height; y++ {
			dy := int(p.OriginY) + y
			if dy < 0 || dy >= def.Height {
				continue
			}
			for x := 0; x < patch.Width; x++ {
				dx := int(p.OriginX) + x
				if dx < 0 || dx >= def.Width {
					continue
				}
				px := patch.Pixels[y*patch.Width+x]
				if px < 0 {
					continue // transparent source pixel: leave the canvas alone
				}
				canvas[dy*def.Width+dx] = px
			}
		}
	}

	return &Patch{Width: def.Width, Height: def.Height, Pixels: canvas}, nil
}

// Flat is a decoded floor/ceiling texture: a flat, uncompressed grid of
// palette indices (always 64x64 in every vanilla Doom IWAD, but this only
// assumes a square lump).
type Flat struct {
	Size   int // both width and height
	Pixels []byte
}

// DecodeFlat decodes a flat lump. Flats have no header at all — they are
// exactly Size*Size raw palette-index bytes, row-major.
func DecodeFlat(b []byte) (*Flat, error) {
	size := 0
	for s := 1; s*s <= len(b); s++ {
		if s*s == len(b) {
			size = s
		}
	}
	if size == 0 {
		return nil, fmt.Errorf("wad: flat lump size %d bytes is not a perfect square", len(b))
	}
	pixels := make([]byte, len(b))
	copy(pixels, b)
	return &Flat{Size: size, Pixels: pixels}, nil
}
