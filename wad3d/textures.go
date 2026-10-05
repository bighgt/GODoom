package wad3d

import (
	"archive/zip"
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os"
	"sort"

	xdraw "golang.org/x/image/draw"

	"twopointfive/assets"
	"twopointfive/engine"
	"twopointfive/wad"
)

// nameSet is a set of upper-case texture or flat lump names.
type nameSet map[string]bool

// usedNames collects every wall texture and flat the given levels reference.
func usedNames(levels []*wad.Level) (walls, flats nameSet) {
	walls, flats = nameSet{}, nameSet{}
	for _, lvl := range levels {
		for _, sd := range lvl.Sidedefs {
			for _, n := range [3]string{sd.UpperTexture, sd.LowerTexture, sd.MiddleTexture} {
				if n != "" && n != "-" {
					walls[n] = true
				}
			}
		}
		for _, sec := range lvl.Sectors {
			flats[sec.FloorTexture] = true
			flats[sec.CeilingTexture] = true
		}
	}
	return walls, flats
}

// addFamilies grows walls/flats with everything the engine may swap a used
// name for at runtime: the other frames of its animation (the WAD's
// ANIMATED/ANIMDEFS, or id's built-in table) and the other face of a
// switch.
func addFamilies(w *wad.WAD, walls, flats nameSet) {
	wallGroups, flatGroups := engine.TextureAnimGroups(w)
	grow := func(set nameSet, groups [][]string) {
		for _, g := range groups {
			for _, n := range g {
				if set[n] {
					for _, m := range g {
						set[m] = true
					}
					break
				}
			}
		}
	}
	grow(walls, wallGroups)
	grow(flats, flatGroups)
	for n := range walls {
		if p, ok := engine.SwitchPartner(n); ok {
			walls[p] = true
		}
	}
}

// textureSource says which override set to embed and where its files are.
type textureSource struct {
	label        string // manifest name: "hires" or "heretic"
	walls, flats map[string]string
}

func pickTextureSource(w *wad.WAD) textureSource {
	if w.IsHeretic() {
		walls, flats := assets.HereticOverrideFiles()
		return textureSource{label: "heretic", walls: walls, flats: flats}
	}
	walls, flats := assets.HiresOverrideFiles()
	return textureSource{label: "hires", walls: walls, flats: flats}
}

// textureZip is the built TPFHIRES container plus what went into it.
type textureZip struct {
	data         []byte
	walls, flats int
	missing      int // wanted names with no hi-res file (the WAD's own art is used)
}

// buildTextureZip packs every wanted name that the WAD actually defines and
// that src has a PNG for into a TPFHIRES zip (assets/embedded.go's layout).
// PNGs are stored, not deflated — they are already compressed. maxDim > 0
// downscales anything larger (Catmull-Rom, aspect kept); the engine derives
// the texel density from the PNG's own width, so a smaller file simply
// samples at a lower density.
func buildTextureZip(w *wad.WAD, src textureSource, wantWalls, wantFlats nameSet, maxDim int) (textureZip, error) {
	var out textureZip
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	add := func(dir string, want nameSet, defined []string, files map[string]string) (int, error) {
		def := nameSet{}
		for _, n := range defined {
			def[n] = true
		}
		names := make([]string, 0, len(want))
		for n := range want {
			if def[n] {
				names = append(names, n)
			}
		}
		sort.Strings(names)
		count := 0
		for _, n := range names {
			path, ok := files[n]
			if !ok {
				out.missing++
				continue
			}
			data, err := readPNG(path, maxDim)
			if err != nil {
				return count, err
			}
			fw, err := zw.CreateHeader(&zip.FileHeader{Name: dir + "/" + n + ".png", Method: zip.Store})
			if err != nil {
				return count, err
			}
			if _, err := fw.Write(data); err != nil {
				return count, err
			}
			count++
		}
		return count, nil
	}

	var err error
	if out.walls, err = add(assets.EmbeddedWallsDir, wantWalls, w.WallTextureNames(), src.walls); err != nil {
		return out, err
	}
	if out.flats, err = add(assets.EmbeddedFlatsDir, wantFlats, w.FlatNames(), src.flats); err != nil {
		return out, err
	}
	if err := zw.Close(); err != nil {
		return out, err
	}
	out.data = buf.Bytes()
	return out, nil
}

// readPNG returns path's bytes as-is when no downscale is needed, else a
// re-encoded, downscaled copy.
func readPNG(path string, maxDim int) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if maxDim <= 0 {
		return raw, nil
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if cfg.Width <= maxDim && cfg.Height <= maxDim {
		return raw, nil
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	b := img.Bounds()
	scale := float64(maxDim) / float64(max(b.Dx(), b.Dy()))
	dw, dh := max(1, int(float64(b.Dx())*scale+0.5)), max(1, int(float64(b.Dy())*scale+0.5))
	dst := image.NewNRGBA(image.Rect(0, 0, dw, dh))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, b, xdraw.Over, nil)
	var enc bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&enc, dst); err != nil {
		return nil, err
	}
	return enc.Bytes(), nil
}
