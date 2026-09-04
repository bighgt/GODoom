package assets

import (
	"sync"

	"twopointfive/wad"
)

// ZDoom TEXTURES support. engine/texturedefs is the clean-room text parser;
// the engine hands the parsed definitions here as ZDDef values (assets
// can't import engine). A registered def shadows the WAD's own
// TEXTURE1/TEXTURE2 entry, any hi-res or Quake override, and — for a flat
// or sprite — the raw lump, so a TEXTURES lump fully re-authors a name the
// way ZDoom does. XScale becomes the resolved bitmap's TexelsPerUnit, so
// the renderers sample the composite at the density the author declared
// instead of the old width-ratio guess.

// ZDPatch is one stamped source image inside a ZDDef (mirror of
// engine/texturedefs.Patch).
type ZDPatch struct {
	Name       string
	X, Y       int
	FlipX      bool
	FlipY      bool
	Rotate     int // 0, 90, 180, 270
	Alpha      float64
	Style      string // lower-case; "" == copy
	UseOffsets bool
	Blend      [4]float64
	HasBlend   bool
}

// ZDDef is one TEXTURES definition (mirror of engine/texturedefs.Def, minus
// the parse-only flags).
type ZDDef struct {
	Name           string
	Width, Height  int
	XScale, YScale float64
	OffsetX        int
	OffsetY        int
	WorldPanning   bool
	NullTexture    bool
	Patches        []ZDPatch
}

// zdRegistry holds the registered defs by namespace. Guarded by
// Textures.mu (populated once at load, read during resolution).
type zdRegistry struct {
	walls   map[string]ZDDef
	flats   map[string]ZDDef
	sprites map[string]ZDDef
	once    sync.Once
}

func (t *Textures) zdInit() {
	t.zd.once.Do(func() {
		t.zd.walls = map[string]ZDDef{}
		t.zd.flats = map[string]ZDDef{}
		t.zd.sprites = map[string]ZDDef{}
	})
}

// AddTextureDefs registers parsed TEXTURES definitions. walls covers the
// Texture / WallTexture namespace, flats the Flat namespace, sprites the
// Sprite / Graphic namespace. Later calls layer over earlier ones
// (last-wins per name). Call before the first resolution of an affected
// name — normally right after assets.New, from the engine's load path.
func (t *Textures) AddTextureDefs(walls, flats, sprites []ZDDef) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.zdInit()
	for _, d := range walls {
		t.zd.walls[d.Name] = d
		delete(t.walls, d.Name) // drop any cached plain decode
	}
	for _, d := range flats {
		t.zd.flats[d.Name] = d
		delete(t.flats, d.Name)
	}
	for _, d := range sprites {
		t.zd.sprites[d.Name] = d
		delete(t.sprites, d.Name)
	}
}

// zdWall / zdFlat / zdSprite report a registered def (mu held by caller).
func (t *Textures) zdWall(name string) (ZDDef, bool) {
	if t.zd.walls == nil {
		return ZDDef{}, false
	}
	d, ok := t.zd.walls[name]
	return d, ok
}

func (t *Textures) zdFlatDef(name string) (ZDDef, bool) {
	if t.zd.flats == nil {
		return ZDDef{}, false
	}
	d, ok := t.zd.flats[name]
	return d, ok
}

func (t *Textures) zdSpriteDef(name string) (ZDDef, bool) {
	if t.zd.sprites == nil {
		return ZDDef{}, false
	}
	d, ok := t.zd.sprites[name]
	return d, ok
}

// composeZD renders a ZDDef to an RGBA. mu is held by the caller. depth
// guards against a TEXTURES patch that names another composite in a cycle.
func (t *Textures) composeZD(d ZDDef, depth int) *RGBA {
	w, h := d.Width, d.Height
	// Infer the canvas from the first resolvable patch when the header
	// omitted a size.
	if (w <= 0 || h <= 0) && len(d.Patches) > 0 {
		for _, p := range d.Patches {
			if src := t.zdSource(p.Name, depth); src != nil {
				if w <= 0 {
					w = src.Width + p.X
				}
				if h <= 0 {
					h = src.Height + p.Y
				}
				break
			}
		}
	}
	if w <= 0 || h <= 0 {
		return t.missing
	}

	out := &RGBA{Width: w, Height: h, Pix: make([]byte, w*h*4), TexelsPerUnit: 1}
	if d.XScale > 0 {
		out.TexelsPerUnit = d.XScale
	}
	out.OffsetX, out.OffsetY = d.OffsetX, d.OffsetY

	for _, p := range d.Patches {
		src := t.zdSource(p.Name, depth)
		if src == nil {
			continue
		}
		src = orientRGBA(src, p.Rotate, p.FlipX, p.FlipY)
		if p.HasBlend {
			src = blendTintRGBA(src, p.Blend)
		}
		ox, oy := p.X, p.Y
		if p.UseOffsets {
			ox -= src.OffsetX
			oy -= src.OffsetY
		}
		compositePatch(out, src, ox, oy, p.Style, clampAlpha(p.Alpha))
	}
	return out
}

// zdSource resolves a patch-source name to pixels: a registered composite
// (recursing, depth-limited), then a raw patch lump, then a flat lump.
func (t *Textures) zdSource(name string, depth int) *RGBA {
	if depth > 8 {
		return nil
	}
	if d, ok := t.zdWall(name); ok {
		return t.composeZD(d, depth+1)
	}
	if d, ok := t.zdSpriteDef(name); ok {
		return t.composeZD(d, depth+1)
	}
	if d, ok := t.zdFlatDef(name); ok {
		return t.composeZD(d, depth+1)
	}
	if raw, ok := t.w.Find(name); ok {
		if p, err := wad.DecodePatch(raw); err == nil && p != nil {
			im := fromIndexed(p.Width, p.Height, p.Pixels, t.pal)
			im.OffsetX, im.OffsetY = p.LeftOffset, p.TopOffset
			return im
		}
		if f, err := wad.DecodeFlat(raw); err == nil && f != nil {
			return fromFlat(f, t.pal)
		}
	}
	return nil
}

func clampAlpha(a float64) float64 {
	if a <= 0 {
		return 1 // an unset Alpha means opaque
	}
	if a > 1 {
		return 1
	}
	return a
}
