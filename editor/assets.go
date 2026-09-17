package editor

import (
	"fmt"
	"image"
	"io"
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

// ---- importing new textures --------------------------------------------
//
// Imported files are copied into assets/textures/walls|flats/ under a
// derived name and registered live — the exact same loose-file, lazy-load
// convention the hi-res/Quake/Heretic packs already use (see assets/hires.go),
// so an imported texture also shows up unmodified on the next run and in
// an actual play session, not just this editor session.

// ImportWallTexture copies srcPath in as a new (or replacement) wall
// texture and adds it to the wall browser, returning the name it was
// given.
func (b *Bank) ImportWallTexture(srcPath string) (string, error) {
	name := textureNameFromFile(srcPath)
	dst, err := copyIntoAssetDir(assets.HiresWallsDir, name, srcPath)
	if err != nil {
		return name, err
	}
	if err := b.Tex.ImportWallTexture(name, dst); err != nil {
		return name, err
	}
	delete(b.thumbs, name)
	b.Walls = addSorted(b.Walls, name)
	return name, nil
}

// ImportFlat is ImportWallTexture's flat counterpart.
func (b *Bank) ImportFlat(srcPath string) (string, error) {
	name := textureNameFromFile(srcPath)
	dst, err := copyIntoAssetDir(assets.HiresFlatsDir, name, srcPath)
	if err != nil {
		return name, err
	}
	if err := b.Tex.ImportFlat(name, dst); err != nil {
		return name, err
	}
	delete(b.flatThmb, name)
	b.Flats = addSorted(b.Flats, name)
	return name, nil
}

// ImportFolder imports every *.png found recursively under dir, as wall
// textures or, if flat, flats. Returns how many imported cleanly; one bad
// file is logged in firstErr but doesn't stop the rest of the folder.
func (b *Bank) ImportFolder(dir string, flat bool) (n int, firstErr error) {
	_ = filepath.WalkDir(dir, func(p string, e os.DirEntry, err error) error {
		if err != nil || e.IsDir() || !strings.EqualFold(filepath.Ext(p), ".png") {
			return nil
		}
		var ierr error
		if flat {
			_, ierr = b.ImportFlat(p)
		} else {
			_, ierr = b.ImportWallTexture(p)
		}
		if ierr != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", p, ierr)
			}
			return nil
		}
		n++
		return nil
	})
	return n, firstErr
}

// textureNameFromFile derives a lump name from an imported file's base
// name: uppercased and cut to 8 characters, the on-disk PWAD limit
// (wad.putName truncates the same way) — so what the browser shows always
// matches what a saved level can actually store. A name that collides with
// an existing texture replaces it, the same "last one seen wins" rule the
// hi-res packs use for a duplicate name.
func textureNameFromFile(path string) string {
	base := filepath.Base(path)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	name := strings.ToUpper(base)
	if len(name) > 8 {
		name = name[:8]
	}
	return name
}

// copyIntoAssetDir copies srcPath into dir (created if needed) as name+".png",
// returning the destination path.
func copyIntoAssetDir(dir, name, srcPath string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dst := filepath.Join(dir, name+".png")
	in, err := os.Open(srcPath)
	if err != nil {
		return "", err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return "", err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return "", err
	}
	return dst, nil
}

// addSorted inserts name into the sorted, deduped list if it isn't already
// present.
func addSorted(list []string, name string) []string {
	i := sort.SearchStrings(list, name)
	if i < len(list) && list[i] == name {
		return list
	}
	list = append(list, "")
	copy(list[i+1:], list[i:])
	list[i] = name
	return list
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
