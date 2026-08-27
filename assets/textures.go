// Package assets resolves the named wall textures and floor/ceiling flats a
// Level's geometry references into plain RGBA bitmaps, decoding Doom's
// palette-indexed graphic formats (via package wad) exactly once per name
// and caching the result.
package assets

import (
	"fmt"

	"twopointfive/wad"
)

// RGBA is an already palette-resolved bitmap: 4 bytes/pixel (R,G,B,A),
// row-major. Alpha is 0 for pixels that were transparent in the source
// Doom graphic and 255 otherwise — vanilla Doom's graphic formats have no
// partial transparency.
type RGBA struct {
	Width, Height int
	Pix           []byte
	// OffsetX/OffsetY are the source patch's LeftOffset/TopOffset (see
	// wad.Patch) — the "hotspot" HUD/sprite graphics are meant to be drawn
	// relative to, exactly the way the original engine's V_DrawPatch did:
	// screen position = requested (x, y) minus this offset. Zero for
	// WallTexture/Flat results, which have no such hotspot.
	OffsetX, OffsetY int
}

// At returns the pixel at (x, y). Callers are expected to keep x/y in
// bounds (the renderer always wraps texture coordinates before calling this).
func (im *RGBA) At(x, y int) [4]byte {
	i := (y*im.Width + x) * 4
	return [4]byte{im.Pix[i], im.Pix[i+1], im.Pix[i+2], im.Pix[i+3]}
}

func fromIndexed(width, height int, indices []int16, pal wad.Palette) *RGBA {
	pix := make([]byte, width*height*4)
	for i, idx := range indices {
		if idx < 0 {
			continue // leave fully transparent (already zeroed)
		}
		o := i * 4
		c := pal[byte(idx)]
		pix[o], pix[o+1], pix[o+2], pix[o+3] = c.R, c.G, c.B, 255
	}
	return &RGBA{Width: width, Height: height, Pix: pix}
}

func fromFlat(flat *wad.Flat, pal wad.Palette) *RGBA {
	pix := make([]byte, flat.Size*flat.Size*4)
	for i, idx := range flat.Pixels {
		o := i * 4
		c := pal[idx]
		pix[o], pix[o+1], pix[o+2], pix[o+3] = c.R, c.G, c.B, 255
	}
	return &RGBA{Width: flat.Size, Height: flat.Size, Pix: pix}
}

// Textures resolves and caches every wall texture / flat a Level's geometry
// can reference.
type Textures struct {
	w       *wad.WAD
	pal     wad.Palette
	pnames  []string
	texDefs map[string]wad.TextureDef

	walls   map[string]*RGBA
	flats   map[string]*RGBA
	sprites map[string]*RGBA

	missing *RGBA
}

// New loads the palette, PNAMES, and TEXTURE1(+TEXTURE2) definitions needed
// to resolve any texture/flat name a level might reference. Composite wall
// textures and flats are decoded lazily, on first request, via WallTexture
// and Flat.
func New(w *wad.WAD) (*Textures, error) {
	playpal, ok := w.Find("PLAYPAL")
	if !ok {
		return nil, fmt.Errorf("assets: no PLAYPAL lump in this WAD")
	}
	palettes, err := wad.LoadPlaypal(playpal)
	if err != nil {
		return nil, fmt.Errorf("assets: %w", err)
	}

	t := &Textures{
		w:       w,
		pal:     palettes[0],
		texDefs: make(map[string]wad.TextureDef),
		walls:   make(map[string]*RGBA),
		flats:   make(map[string]*RGBA),
		sprites: make(map[string]*RGBA),
		missing: checkerboard(),
	}

	if pn, ok := w.Find("PNAMES"); ok {
		names, err := wad.LoadPnames(pn)
		if err != nil {
			return nil, fmt.Errorf("assets: %w", err)
		}
		t.pnames = names
	}

	for _, lump := range []string{"TEXTURE1", "TEXTURE2"} {
		raw, ok := w.Find(lump)
		if !ok {
			continue
		}
		defs, err := wad.LoadTextureDefs(raw)
		if err != nil {
			return nil, fmt.Errorf("assets: %s: %w", lump, err)
		}
		for _, d := range defs {
			t.texDefs[d.Name] = d
		}
	}

	return t, nil
}

// WallTexture resolves a wall texture name (as found in a Sidedef's
// Upper/Lower/MiddleTexture field) to a decoded RGBA bitmap, composing and
// caching it on first use. ok is false only for "-"/empty (meaning "no
// texture here"); a *named* texture this WAD can't actually resolve falls
// back to a magenta/black checkerboard placeholder instead, so a bad
// reference is visibly wrong rather than invisibly missing.
func (t *Textures) WallTexture(name string) (*RGBA, bool) {
	if name == "" || name == "-" {
		return nil, false
	}
	if im, ok := t.walls[name]; ok {
		return im, true
	}

	def, ok := t.texDefs[name]
	if !ok {
		t.walls[name] = t.missing
		return t.missing, true
	}
	patch, err := t.w.ComposeTexture(def, t.pnames)
	if err != nil {
		t.walls[name] = t.missing
		return t.missing, true
	}
	im := fromIndexed(patch.Width, patch.Height, patch.Pixels, t.pal)
	t.walls[name] = im
	return im, true
}

// Flat resolves a floor/ceiling flat name to a decoded RGBA bitmap, the
// same fallback-to-placeholder policy as WallTexture.
func (t *Textures) Flat(name string) (*RGBA, bool) {
	if name == "" || name == "-" {
		return nil, false
	}
	if im, ok := t.flats[name]; ok {
		return im, true
	}
	raw, ok := t.w.Find(name)
	if !ok {
		t.flats[name] = t.missing
		return t.missing, true
	}
	f, err := wad.DecodeFlat(raw)
	if err != nil {
		t.flats[name] = t.missing
		return t.missing, true
	}
	im := fromFlat(f, t.pal)
	t.flats[name] = im
	return im, true
}

// Sprite resolves any single "picture format" lump by name — the status
// bar background, digit/face/key graphics, and (later) THINGS sprites all
// use this same format, unlike WallTexture's composite TEXTURE1/TEXTURE2
// patches or Flat's raw indexed blocks. Unlike WallTexture/Flat, a missing
// or malformed name simply returns ok=false rather than a placeholder —
// callers (see raster/statusbar.go) treat that as "skip drawing this
// widget" rather than something worth flagging visually, since HUD layout
// code probes for several optional lumps (e.g. only IWADs newer than the
// original shareware always ship every key/arms graphic).
func (t *Textures) Sprite(name string) (*RGBA, bool) {
	if im, ok := t.sprites[name]; ok {
		return im, true
	}
	raw, ok := t.w.Find(name)
	if !ok {
		return nil, false
	}
	patch, err := wad.DecodePatch(raw)
	if err != nil {
		return nil, false
	}
	im := fromIndexed(patch.Width, patch.Height, patch.Pixels, t.pal)
	im.OffsetX, im.OffsetY = patch.LeftOffset, patch.TopOffset
	t.sprites[name] = im
	return im, true
}

func checkerboard() *RGBA {
	const size = 32
	pix := make([]byte, size*size*4)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			o := (y*size + x) * 4
			if (x/8+y/8)%2 == 0 {
				pix[o], pix[o+1], pix[o+2], pix[o+3] = 255, 0, 255, 255
			} else {
				pix[o], pix[o+1], pix[o+2], pix[o+3] = 0, 0, 0, 255
			}
		}
	}
	return &RGBA{Width: size, Height: size, Pix: pix}
}
