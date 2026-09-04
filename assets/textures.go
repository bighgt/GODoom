// Package assets resolves the named wall textures and floor/ceiling flats a
// Level's geometry references into plain RGBA bitmaps, decoding Doom's
// palette-indexed graphic formats (via package wad) exactly once per name
// and caching the result.
package assets

import (
	"fmt"
	"image"
	"sync"

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
	// TexelsPerUnit is how many pixels of Pix correspond to one map unit —
	// always 1 for anything decoded straight from a WAD (vanilla Doom's own
	// convention: wall/flat textures are exactly 1 texel per map unit), but
	// greater for a HiresTextures override (see hires.go), whose pixel data
	// is the same texture at higher resolution over the same physical wall/
	// floor size. The renderer (raster package) multiplies every texture
	// coordinate by this before sampling, which is what makes the extra
	// resolution actually show up instead of just changing the apparent
	// pattern scale or being silently cropped to a corner of the image.
	TexelsPerUnit float64
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
	return &RGBA{Width: width, Height: height, Pix: pix, TexelsPerUnit: 1}
}

func fromFlat(flat *wad.Flat, pal wad.Palette) *RGBA {
	pix := make([]byte, flat.Size*flat.Size*4)
	for i, idx := range flat.Pixels {
		o := i * 4
		c := pal[idx]
		pix[o], pix[o+1], pix[o+2], pix[o+3] = c.R, c.G, c.B, 255
	}
	return &RGBA{Width: flat.Size, Height: flat.Size, Pix: pix, TexelsPerUnit: 1}
}

// Textures resolves and caches every wall texture / flat a Level's geometry
// can reference.
type Textures struct {
	w       *wad.WAD
	pal     wad.Palette
	pnames  []string
	texDefs map[string]wad.TextureDef

	// mu guards the lazily-populated caches below. The raster renderer walks
	// the BSP on several goroutines at once (one per screen strip) and each
	// resolves its segs' textures through WallTexture/Flat, so those map
	// writes must be serialised. Contention is negligible: resolution is
	// once per seg per frame and a hit is just a map read.
	mu      sync.Mutex
	walls   map[string]*RGBA
	flats   map[string]*RGBA
	sprites map[string]*RGBA

	// spriteIndex maps a 4-letter sprite prefix to its per-frame rotation
	// table (id's R_InitSpriteDefs). Built lazily on first SpriteFrame call.
	spriteIndex map[string]*spriteDef

	missing *RGBA

	// hiresWalls/hiresFlats/hiresSprites index the procedurally upscaled
	// overrides (see hires.go, tools/gentex) by lump name -> PNG path. Only
	// an *index* is built at startup; the PNG for a given name is decoded
	// lazily on first use (the full set can be >100MB) and then cached in
	// walls/flats/sprites above like any other resolved image. A nil map
	// means the assets/textures/ directory wasn't found (non-fatal).
	hiresWalls   map[string]string
	hiresFlats   map[string]string
	hiresSprites map[string]string

	// quakeWalls/quakeFlats index the optional Quake 1 texture pack
	// (assets/textures/quake1/, tools/importquake) the same lazy lump-name ->
	// PNG-path way as the hi-res set above. WallTexture/Flat only consult
	// them when quake1 is true (set once at startup by SetQuake1Textures from
	// config's quake1Textures); left alone, a pack sitting on disk changes
	// nothing. A hit here is preferred over the hiresWalls/hiresFlats entry
	// for the same name — see WallTexture's doc comment.
	quakeWalls map[string]string
	quakeFlats map[string]string
	quake1     bool

	// weaponSprites are a locally-supplied HD replacement pack for the
	// first-person weapon viewmodel sprites (assets/weapons/*.pk3 — see
	// weapons.go), decoded eagerly and keyed by lump name. Sprite() prefers
	// one of these over both the WAD's own psprite and any generated
	// hi-res sprite override. nil when no pack is present.
	weaponSprites map[string]image.Image

	// brightmaps indexes brightmap mask PNGs (texture/flat name -> path);
	// brightCache holds the decoded, base-sized masks (a nil entry == "no
	// mask for this name", so the miss is cached too). See brightmap.go.
	brightmaps  map[string]string
	brightCache map[string]*RGBA

	// zd holds ZDoom TEXTURES definitions registered via AddTextureDefs; a
	// registered name is composed on first resolution and shadows the WAD's
	// own decode. See texturesdef.go.
	zd zdRegistry
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
		w:             w,
		pal:           palettes[0],
		texDefs:       make(map[string]wad.TextureDef),
		walls:         make(map[string]*RGBA),
		flats:         make(map[string]*RGBA),
		sprites:       make(map[string]*RGBA),
		missing:       checkerboard(),
		hiresWalls:    indexHiresWalls(),
		hiresFlats:    indexHiresFlats(),
		hiresSprites:  indexHiresSprites(),
		quakeWalls:    indexQuakeWalls(),
		quakeFlats:    indexQuakeFlats(),
		weaponSprites: loadWeaponPack(),
		brightmaps:    indexBrightmaps(),
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

// SetQuake1Textures turns the optional Quake 1 texture pack on or off
// (config's quake1Textures). When on, any wall/flat name the pack covers
// (assets/textures/quake1/, filled by tools/importquake) is served from
// there in preference to the assets/textures/walls|flats hi-res PNG; every
// other name is resolved exactly as before. Call once at startup, before
// the renderer resolves anything. No effect if the pack directory is
// absent. Not safe to call concurrently with rendering.
func (t *Textures) SetQuake1Textures(on bool) { t.quake1 = on }

// Inject registers an already-decoded bitmap under a name so a later
// Flat(name) (flat=true) or WallTexture(name) (flat=false) returns it with
// no WAD lookup. The engine's ANIMDEFS `warp` support uses this: it bakes a
// cycle of pre-distorted frames of a real texture and swaps a surface's
// live name between them each tic. Overwrites any prior entry — including a
// cached decode of a real lump with that name — so a warped FWATER1 wins
// over the WAD's own FWATER1. Safe to call concurrently with resolution.
func (t *Textures) Inject(name string, im *RGBA, flat bool) {
	if name == "" || im == nil {
		return
	}
	t.mu.Lock()
	if flat {
		t.flats[name] = im
	} else {
		t.walls[name] = im
	}
	t.mu.Unlock()
}

// WallTexture resolves a wall texture name (as found in a Sidedef's
// Upper/Lower/MiddleTexture field) to a decoded RGBA bitmap, composing and
// caching it on first use. ok is false only for "-"/empty (meaning "no
// texture here"); a *named* texture this WAD can't actually resolve falls
// back to a magenta/black checkerboard placeholder instead, so a bad
// reference is visibly wrong rather than invisibly missing.
//
// A procedurally upscaled replacement (see hires.go, tools/gentex) is
// preferred over the WAD's own composed texture whenever one exists for
// this exact name — currently every wall texture DOOM1.WAD itself defines,
// but not necessarily every texture an arbitrary other WAD/PWAD might
// reference, which is why this still falls through to the normal WAD path
// rather than replacing it outright. When the Quake 1 pack is enabled
// (SetQuake1Textures) and covers this name, it is preferred over even the
// hi-res override.
func (t *Textures) WallTexture(name string) (*RGBA, bool) {
	if name == "" || name == "-" {
		return nil, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if im, ok := t.walls[name]; ok {
		return im, true
	}
	if zd, ok := t.zdWall(name); ok {
		if zd.NullTexture {
			return nil, false
		}
		im := t.composeZD(zd, 0)
		t.walls[name] = im
		return im, true
	}

	def, ok := t.texDefs[name]

	if t.quake1 {
		if path, qok := t.quakeWalls[name]; qok {
			origW := 0.0
			if ok {
				origW = float64(def.Width)
			}
			if im := loadHiresOverride(path, origW); im != nil {
				t.walls[name] = im
				return im, true
			}
			// decode failed — fall through to the hi-res / WAD paths
		}
	}

	if path, hok := t.hiresWalls[name]; hok {
		origW := 0.0
		if ok {
			origW = float64(def.Width) // TEXTURE1 authored width, in map units
		}
		if im := loadHiresOverride(path, origW); im != nil {
			t.walls[name] = im
			return im, true
		}
		// decode failed — fall through to the WAD's own composed texture
	}

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
// same fallback-to-placeholder policy as WallTexture, and the same
// hires-override preference (see WallTexture's doc comment).
func (t *Textures) Flat(name string) (*RGBA, bool) {
	if name == "" || name == "-" {
		return nil, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if im, ok := t.flats[name]; ok {
		return im, true
	}
	if zd, ok := t.zdFlatDef(name); ok {
		im := t.composeZD(zd, 0)
		t.flats[name] = im
		return im, true
	}
	if t.quake1 {
		if path, qok := t.quakeFlats[name]; qok {
			if im := loadHiresOverride(path, 64); im != nil {
				t.flats[name] = im
				return im, true
			}
			// decode failed — fall through to the hi-res / WAD paths
		}
	}
	if path, ok := t.hiresFlats[name]; ok {
		// Doom flats are always 64x64, so the override's TexelsPerUnit is
		// its own width / 64.
		if im := loadHiresOverride(path, 64); im != nil {
			t.flats[name] = im
			return im, true
		}
		// decode failed — fall through to the WAD's own flat
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
	t.mu.Lock()
	defer t.mu.Unlock()
	if im, ok := t.sprites[name]; ok {
		return im, true
	}
	if zd, ok := t.zdSpriteDef(name); ok {
		im := t.composeZD(zd, 0)
		t.sprites[name] = im
		return im, true
	}

	raw, hasLump := t.w.Find(name)
	var patch *wad.Patch
	if hasLump {
		if p, err := wad.DecodePatch(raw); err == nil {
			patch = p
		}
	}

	// A locally-supplied HD weapon viewmodel pack (weapons.go) wins over
	// both the WAD's own psprite and any generated hi-res sprite override.
	// It resolves even for a weapon this IWAD doesn't ship a sprite for
	// (patch == nil), so a PWAD missing e.g. the SSG frames still gets the
	// HD gun.
	if wimg, ok := t.weaponSprites[name]; ok {
		hi := weaponOverrideRGBA(wimg, patch)
		t.sprites[name] = hi
		return hi, true
	}

	if patch == nil {
		return nil, false
	}

	// Prefer a procedurally upscaled override (see hires.go, tools/gentex).
	// The PNG carries only pixels, so the hotspot still comes from the WAD
	// patch, scaled into the override's own (denser) pixel space.
	if path, ok := t.hiresSprites[name]; ok {
		if hi := loadHiresOverride(path, float64(patch.Width)); hi != nil {
			hi.OffsetX = int(float64(patch.LeftOffset) * hi.TexelsPerUnit)
			hi.OffsetY = int(float64(patch.TopOffset) * hi.TexelsPerUnit)
			t.sprites[name] = hi
			return hi, true
		}
		// decode failed — fall through to the WAD's own sprite
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
	return &RGBA{Width: size, Height: size, Pix: pix, TexelsPerUnit: 1}
}
