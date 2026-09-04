// Command genbright procedurally derives a brightmap mask for every wall
// texture and flat in one or more Doom IWADs — a greyscale image marking
// the texels that are self-illuminated (computer screens, wall lamps,
// warning lights, lava veins). The engine draws a masked texel at full
// brightness regardless of the sector's light level (assets/brightmap.go,
// raster.applyBright, light.frag). Not part of the main build.
//
// Usage (from the repo root):
//
//	go run ./tools/genbright [WAD ...]
//
// With no arguments it processes testdata/DOOM1.WAD. Outputs land in
// assets/textures/brightmaps/, keyed by lump name (gitignored, like the
// hi-res set). A drop-in pack in assets/mods/brightmaps/ overrides these.
//
// There is no authored "which texels glow" data in a Doom WAD, so this is a
// heuristic: a texel counts as emissive when it is bright AND either
// saturated (a coloured screen / light / lava) or near-white (a lamp); a
// texture that is *mostly* bright is skipped (a whole pale wall should not
// glow), and single-pixel speckle is eroded away. Tune the constants below
// if it over- or under-marks.
package main

import (
	"image"
	"image/png"
	"log"
	"os"
	"path/filepath"

	"twopointfive/wad"
)

var outDir = filepath.Join("assets", "textures", "brightmaps")

// Heuristic thresholds (0..1). Deliberately strict: a Doom emissive surface
// (a computer screen, a coloured light fixture, lava, an EXIT sign) is
// BRIGHT and STRONGLY COLOURED. Bright *grey* is a metal highlight, not a
// light — the old near-white rule caught those and speckled ordinary walls.
const (
	brightV = 0.86 // "bright" value cutoff
	minSat  = 0.55 // must be vividly coloured, not a grey highlight

	maxEmissiveFrac = 0.28  // eroded coverage above this -> false positive, skip the texture
	minEmissiveFrac = 0.004 // below this -> noise, skip
)

func main() {
	wads := os.Args[1:]
	if len(wads) == 0 {
		wads = []string{filepath.Join("testdata", "DOOM1.WAD")}
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		log.Fatalf("genbright: %v", err)
	}
	for _, path := range wads {
		w, err := wad.Load(path)
		if err != nil {
			log.Fatalf("genbright: %v", err)
		}
		log.Printf("genbright: === %s ===", path)
		processWAD(w)
	}
}

func processWAD(w *wad.WAD) {
	playpalRaw, ok := w.Find("PLAYPAL")
	if !ok {
		log.Fatal("genbright: no PLAYPAL")
	}
	palettes, err := wad.LoadPlaypal(playpalRaw)
	if err != nil {
		log.Fatalf("genbright: %v", err)
	}
	pal := palettes[0]

	var pnames []string
	if pn, ok := w.Find("PNAMES"); ok {
		if pnames, err = wad.LoadPnames(pn); err != nil {
			log.Fatalf("genbright: %v", err)
		}
	}

	walls, flats := 0, 0

	texDefs := map[string]wad.TextureDef{}
	for _, lump := range []string{"TEXTURE1", "TEXTURE2"} {
		raw, ok := w.Find(lump)
		if !ok {
			continue
		}
		defs, err := wad.LoadTextureDefs(raw)
		if err != nil {
			log.Fatalf("genbright: %s: %v", lump, err)
		}
		for _, d := range defs {
			texDefs[d.Name] = d
		}
	}
	for name, def := range texDefs {
		patch, err := w.ComposeTexture(def, pnames)
		if err != nil {
			continue
		}
		if mask, ok := buildMask(patch.Width, patch.Height, sampleFn(patch.Pixels, patch.Width, pal)); ok {
			savePNG(filepath.Join(outDir, fsName(name)+".png"), mask)
			walls++
		}
	}

	inFlats := false
	for _, e := range w.Entries {
		switch e.Name {
		case "F_START", "F1_START", "F2_START", "F3_START":
			inFlats = true
			continue
		case "F_END", "F1_END", "F2_END", "F3_END":
			inFlats = false
			continue
		}
		if !inFlats || e.Size == 0 || e.Name == "F_SKY1" {
			continue
		}
		flat, err := wad.DecodeFlat(w.Lump(w.IndexOf(e.Name)))
		if err != nil {
			continue
		}
		if mask, ok := buildMask(flat.Size, flat.Size, flatSampleFn(flat, pal)); ok {
			savePNG(filepath.Join(outDir, fsName(e.Name)+".png"), mask)
			flats++
		}
	}

	log.Printf("genbright: wrote %d wall + %d flat brightmaps to %s", walls, flats, outDir)
}

// sample returns the 0..255 RGB of texel (x,y), and whether it is opaque
// (a composite-texture hole is transparent -> never emissive).
type sample func(x, y int) (r, g, b uint8, opaque bool)

func sampleFn(indices []int16, w int, pal wad.Palette) sample {
	return func(x, y int) (uint8, uint8, uint8, bool) {
		idx := indices[y*w+x]
		if idx < 0 {
			return 0, 0, 0, false
		}
		c := pal[byte(idx)]
		return c.R, c.G, c.B, true
	}
}

func flatSampleFn(f *wad.Flat, pal wad.Palette) sample {
	return func(x, y int) (uint8, uint8, uint8, bool) {
		c := pal[f.Pixels[y*f.Size+x]]
		return c.R, c.G, c.B, true
	}
}

// buildMask evaluates the emissive heuristic per texel, erodes speckle, and
// returns a greyscale mask — or ok=false when the texture should carry no
// brightmap at all.
func buildMask(w, h int, at sample) (*image.Gray, bool) {
	if w <= 0 || h <= 0 {
		return nil, false
	}
	raw := make([]bool, w*h)
	val := make([]uint8, w*h)
	count := 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, g, b, opaque := at(x, y)
			if !opaque {
				continue
			}
			mx := max3(r, g, b)
			mn := min3(r, g, b)
			v := float64(mx) / 255
			sat := 0.0
			if mx > 0 {
				sat = float64(mx-mn) / float64(mx)
			}
			if v > brightV && sat > minSat {
				raw[y*w+x] = true
				val[y*w+x] = mx
				count++
			}
		}
	}
	if float64(count)/float64(w*h) < minEmissiveFrac {
		return nil, false
	}
	return maskFromKept(w, h, raw, val)
}

// maskFromKept keeps a candidate texel only when it sits in a solid cluster
// (>=5 of its 8 neighbours are candidates too) — a screen panel or a light
// block survives, a bright speck or a one-texel-wide highlight does not.
// Returns ok=false if nothing survives, or if the surviving coverage is
// still large enough to be a false positive.
func maskFromKept(w, h int, raw []bool, val []uint8) (*image.Gray, bool) {
	g := image.NewGray(image.Rect(0, 0, w, h))
	kept := 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if !raw[y*w+x] {
				continue
			}
			n := 0
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					if dx == 0 && dy == 0 {
						continue
					}
					nx, ny := x+dx, y+dy
					if nx >= 0 && nx < w && ny >= 0 && ny < h && raw[ny*w+nx] {
						n++
					}
				}
			}
			if n >= 5 {
				g.Pix[y*w+x] = val[y*w+x]
				kept++
			}
		}
	}
	if kept == 0 || float64(kept)/float64(w*h) > maxEmissiveFrac {
		return nil, false
	}
	return g, true
}

func max3(a, b, c uint8) uint8 {
	m := a
	if b > m {
		m = b
	}
	if c > m {
		m = c
	}
	return m
}

func min3(a, b, c uint8) uint8 {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}

func fsName(lump string) string {
	var b []byte
	for i := 0; i < len(lump); i++ {
		c := lump[i]
		if c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			b = append(b, c)
		} else {
			b = append(b, '%', "0123456789ABCDEF"[c>>4], "0123456789ABCDEF"[c&0xF])
		}
	}
	return string(b)
}

func savePNG(path string, img image.Image) {
	f, err := os.Create(path)
	if err != nil {
		log.Fatalf("genbright: %v", err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		log.Fatalf("genbright: %v", err)
	}
}
