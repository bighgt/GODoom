package editor

import (
	"image"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"twopointfive/assets"
	"twopointfive/wad"
)

// Bank is the editor's aggregated asset set: one merged WAD built from every
// *.wad under wad/Game WADs/ and assets/ plus the WAD currently open, one
// assets.Textures over it, and the sorted name lists the browsers show.
type Bank struct {
	Merged   *wad.WAD
	Tex      *assets.Textures
	Walls    []string // composite wall-texture names (TEXTURE1/2)
	Flats    []string // flat lump names (F_START..F_END)
	thumbs   map[string]*image.RGBA
	flatThmb map[string]*image.RGBA
}

// scanDirs are searched (recursively) for extra WADs to pull textures and
// flats from, relative to the working directory.
var scanDirs = []string{"wad/Game WADs", "wad", "assets"}

// LoadBank merges primary (the WAD being edited, already loaded) with every
// other WAD found on disk and indexes their textures.
func LoadBank(primary *wad.WAD, primaryPath string) (*Bank, error) {
	wads := []*wad.WAD{primary}
	seen := map[string]bool{}
	if primaryPath != "" {
		if abs, err := filepath.Abs(primaryPath); err == nil {
			seen[strings.ToLower(abs)] = true
		}
	}
	for _, d := range scanDirs {
		_ = filepath.WalkDir(d, func(p string, e os.DirEntry, err error) error {
			if err != nil || e.IsDir() {
				return nil
			}
			if !strings.EqualFold(filepath.Ext(p), ".wad") {
				return nil
			}
			abs, _ := filepath.Abs(p)
			if seen[strings.ToLower(abs)] {
				return nil
			}
			seen[strings.ToLower(abs)] = true
			if w, err := wad.Load(p); err == nil {
				wads = append(wads, w)
			}
			return nil
		})
	}

	merged := primary
	if len(wads) > 1 {
		if m := wad.Merge(wads...); m != nil {
			merged = m
		}
	}
	tex, err := assets.New(merged)
	if err != nil {
		return nil, err
	}
	b := &Bank{
		Merged: merged, Tex: tex,
		thumbs: map[string]*image.RGBA{}, flatThmb: map[string]*image.RGBA{},
	}
	b.Walls = dedupeSorted(merged.WallTextureNames())
	b.Flats = dedupeSorted(merged.FlatNames())
	return b, nil
}

// WallThumb returns a cached *image.RGBA for a composite wall texture.
func (b *Bank) WallThumb(name string) *image.RGBA {
	if name == "" || name == "-" {
		return nil
	}
	if im, ok := b.thumbs[name]; ok {
		return im
	}
	var out *image.RGBA
	if rgba, ok := b.Tex.WallTexture(name); ok {
		out = toImage(rgba)
	}
	b.thumbs[name] = out
	return out
}

// FlatThumb returns a cached *image.RGBA for a flat.
func (b *Bank) FlatThumb(name string) *image.RGBA {
	if name == "" {
		return nil
	}
	if im, ok := b.flatThmb[name]; ok {
		return im
	}
	var out *image.RGBA
	if rgba, ok := b.Tex.Flat(name); ok {
		out = toImage(rgba)
	}
	b.flatThmb[name] = out
	return out
}

func toImage(r *assets.RGBA) *image.RGBA {
	if r == nil || r.Width == 0 || r.Height == 0 {
		return nil
	}
	return &image.RGBA{Pix: r.Pix, Stride: r.Width * 4, Rect: image.Rect(0, 0, r.Width, r.Height)}
}

func dedupeSorted(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, s := range in {
		if s == "" || s == "-" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
