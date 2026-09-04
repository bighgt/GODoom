package assets

import (
	"image/png"
	"os"
)

// Brightmaps: a per-texture greyscale mask marking the texels that are
// self-illuminated (a computer screen, a wall lamp, a warning light, lava
// veins). The renderer draws a masked texel at full brightness regardless
// of the sector's light level — GZDoom's brightmap feature.
//
// Masks are loaded, lazily, from two loose PNG trees (same discovery as the
// hi-res override set, hires.go):
//
//	assets/mods/brightmaps/       a drop-in pack, keyed by texture/flat name
//	assets/textures/brightmaps/   where `go run ./tools/genbright` writes its
//	                              procedurally generated set
//
// The mod tree wins on a name clash. A mask is stored resized to the base
// texture's exact pixel dimensions so the renderer can sample it with the
// same texel coordinates as the base (no separate wrap/scale bookkeeping).

const (
	brightmapsModDir = "assets/mods/brightmaps"
	brightmapsGenDir = "assets/textures/brightmaps"
)

func indexBrightmaps() map[string]string {
	out := indexHiresDir(brightmapsGenDir)
	for name, path := range indexHiresDir(brightmapsModDir) {
		if out == nil {
			out = map[string]string{}
		}
		out[name] = path // mod tree shadows the generated one
	}
	return out
}

// Brightmap returns the brightmap mask for texture/flat name, resized to
// base's dimensions (so it samples at the same texel coords), or ok=false
// when the loaded packs define none. base is the already-resolved texture
// the caller holds — passing it avoids a re-lookup and fixes the target
// size. Results (including the "none" answer) are cached.
func (t *Textures) Brightmap(name string, base *RGBA) (*RGBA, bool) {
	if base == nil || name == "" {
		return nil, false
	}
	t.mu.Lock()
	if t.brightCache == nil {
		t.brightCache = make(map[string]*RGBA)
	}
	if m, seen := t.brightCache[name]; seen {
		t.mu.Unlock()
		return m, m != nil
	}
	path, have := t.brightmaps[name]
	t.mu.Unlock()

	var mask *RGBA
	if have {
		mask = loadBrightmapMask(path, base.Width, base.Height)
	}

	t.mu.Lock()
	t.brightCache[name] = mask
	t.mu.Unlock()
	return mask, mask != nil
}

// loadBrightmapMask decodes a mask PNG and nearest-resamples it to w x h
// (the base texture size). Only the luma is meaningful; it is written into
// all three colour channels so a caller can read any of them. Returns nil
// on any failure.
func loadBrightmapMask(path string, w, h int) *RGBA {
	if w <= 0 || h <= 0 {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	img, err := png.Decode(f)
	f.Close()
	if err != nil {
		return nil
	}
	b := img.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if sw <= 0 || sh <= 0 {
		return nil
	}
	pix := make([]byte, w*h*4)
	any := false
	for y := 0; y < h; y++ {
		sy := b.Min.Y + y*sh/h
		for x := 0; x < w; x++ {
			sx := b.Min.X + x*sw/w
			r, g, bl, _ := img.At(sx, sy).RGBA()
			// Rec.601 luma, 16-bit -> 8-bit.
			l := (299*(r>>8) + 587*(g>>8) + 114*(bl>>8)) / 1000
			o := (y*w + x) * 4
			pix[o], pix[o+1], pix[o+2], pix[o+3] = byte(l), byte(l), byte(l), 255
			if l > 6 {
				any = true
			}
		}
	}
	if !any {
		return nil // an all-black mask contributes nothing
	}
	return &RGBA{Width: w, Height: h, Pix: pix, TexelsPerUnit: 1}
}
