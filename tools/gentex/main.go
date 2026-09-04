// Command gentex procedurally upscales every wall texture, flat, and sprite
// (weapons, monsters, items, projectiles, HUD graphics) in one or more Doom
// IWADs into larger, more detailed replacements under assets/textures/,
// loaded by the engine in place of the WAD's own low-resolution versions —
// see game_design.txt. Not part of the main build.
//
// Usage (from the repo root):
//
//	go run ./tools/gentex [WAD ...]
//
// With no arguments it processes testdata/DOOM1.WAD. Pass several WADs to
// cover more content (e.g. DOOM1.WAD and DOOM2.WAD) — outputs are keyed by
// lump name, so a later WAD's version of a shared name wins.
//
// There's no real "HD source" to draw from (see game_design.txt for why a
// third-party texture pack wasn't used) — instead, each original image is
// decoded from the WAD at its real, authored pixel data, upscaled, and then
// given synthetic fine detail (grain, edge-aware beveling) so it reads as
// "more detailed" rather than just a blurrier, bigger version. The upscale
// factor (see UpscaleFactor) is shared with the engine's renderer
// (assets.RGBA's TexelsPerUnit), which is what makes the extra resolution
// actually visible in-game rather than just wasted pixels.
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"
	"path/filepath"

	"twopointfive/assets"
	"twopointfive/wad"
)

// UpscaleFactor is how many output texels a single original texel becomes,
// per axis (so UpscaleFactor^2 the pixel area) — assets.HiresTexelsPerUnit
// is the single source of truth the renderer also reads, so the two can
// never drift apart.
const UpscaleFactor = int(assets.HiresTexelsPerUnit)

var (
	wallDir   = filepath.Join("assets", "textures", "walls")
	flatDir   = filepath.Join("assets", "textures", "flats")
	spriteDir = filepath.Join("assets", "textures", "sprites")
)

func main() {
	wads := os.Args[1:]
	if len(wads) == 0 {
		wads = []string{filepath.Join("testdata", "DOOM1.WAD")}
	}

	must(os.MkdirAll(wallDir, 0o755))
	must(os.MkdirAll(flatDir, 0o755))
	must(os.MkdirAll(spriteDir, 0o755))

	for _, path := range wads {
		w, err := wad.Load(path)
		if err != nil {
			log.Fatalf("gentex: %v", err)
		}
		log.Printf("gentex: === %s ===", path)
		processWAD(w)
	}
}

func processWAD(w *wad.WAD) {
	playpalRaw, ok := w.Find("PLAYPAL")
	if !ok {
		log.Fatal("gentex: no PLAYPAL")
	}
	palettes, err := wad.LoadPlaypal(playpalRaw)
	if err != nil {
		log.Fatalf("gentex: %v", err)
	}
	pal := palettes[0]

	var pnames []string
	if pn, ok := w.Find("PNAMES"); ok {
		if pnames, err = wad.LoadPnames(pn); err != nil {
			log.Fatalf("gentex: %v", err)
		}
	}

	genWalls(w, pal, pnames)
	genFlats(w, pal)
	genSprites(w, pal)
}

func genWalls(w *wad.WAD, pal wad.Palette, pnames []string) {
	texDefs := map[string]wad.TextureDef{}
	for _, lump := range []string{"TEXTURE1", "TEXTURE2"} {
		raw, ok := w.Find(lump)
		if !ok {
			continue
		}
		defs, err := wad.LoadTextureDefs(raw)
		if err != nil {
			log.Fatalf("gentex: %s: %v", lump, err)
		}
		for _, d := range defs {
			texDefs[d.Name] = d
		}
	}

	n := 0
	for name, def := range texDefs {
		patch, err := w.ComposeTexture(def, pnames)
		if err != nil {
			log.Printf("gentex: skip wall %s: %v", name, err)
			continue
		}
		src := indexedToRGBA(patch.Width, patch.Height, patch.Pixels, pal)
		savePNG(filepath.Join(wallDir, fsName(name)+".png"), enhance(src, name))
		n++
	}
	log.Printf("gentex: wrote %d wall textures", n)
}

func genFlats(w *wad.WAD, pal wad.Palette) {
	n := 0
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
			continue // F_SKY1 is handled by tools/gensky, not a real flat image
		}
		flat, err := wad.DecodeFlat(w.Lump(w.IndexOf(e.Name)))
		if err != nil {
			log.Printf("gentex: skip flat %s: %v", e.Name, err)
			continue
		}
		src := flatToRGBA(flat, pal)
		savePNG(filepath.Join(flatDir, fsName(e.Name)+".png"), enhance(src, e.Name))
		n++
	}
	log.Printf("gentex: wrote %d flats", n)
}

// genSprites upscales every lump in the S_START..S_END namespace (all
// weapon/monster/item/projectile frames) plus the status-bar graphics
// (ST*) — everything the engine draws through assets.Textures.Sprite.
// Sprites have 1-bit transparency, so the source's edge colours are bled
// outward before the bilinear upscale (no black halo) and the result's
// alpha is re-thresholded to hard edges.
func genSprites(w *wad.WAD, pal wad.Palette) {
	names := spriteLumpNames(w)
	n, skipped := 0, 0
	for i, name := range names {
		raw, ok := w.Find(name)
		if !ok || len(raw) == 0 {
			continue
		}
		patch, err := wad.DecodePatch(raw)
		if err != nil {
			skipped++
			continue
		}
		src := indexedToRGBA(patch.Width, patch.Height, patch.Pixels, pal)
		savePNG(filepath.Join(spriteDir, fsName(name)+".png"), enhanceSprite(src, name))
		n++
		if (i+1)%200 == 0 {
			log.Printf("gentex: ... %d/%d sprites", i+1, len(names))
		}
	}
	log.Printf("gentex: wrote %d sprites (%d unreadable)", n, skipped)
}

// spriteLumpNames collects the picture-format lumps the engine renders as
// sprites: the S_START..S_END (also SS_START/SS_END in some IWADs)
// namespace, plus the fixed set of status-bar graphic prefixes.
func spriteLumpNames(w *wad.WAD) []string {
	seen := map[string]bool{}
	var out []string
	add := func(name string) {
		if name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}

	inSprites := false
	for _, e := range w.Entries {
		switch e.Name {
		case "S_START", "SS_START":
			inSprites = true
			continue
		case "S_END", "SS_END":
			inSprites = false
			continue
		}
		if inSprites && e.Size > 0 {
			add(e.Name)
		}
	}

	// Status-bar graphics live outside the sprite namespace. Enumerate by
	// the prefixes st_stuff.c uses; a name that isn't in this WAD is just
	// skipped by the caller.
	for _, pfx := range []string{"STBAR", "STARMS", "STTPRCNT", "STTMINUS", "STCFN", "STDISK", "STCDROM"} {
		add(pfx)
	}
	for d := 0; d <= 9; d++ {
		add(fmt.Sprintf("STTNUM%d", d))
		add(fmt.Sprintf("STYSNUM%d", d))
		add(fmt.Sprintf("STGNUM%d", d))
	}
	for i := 0; i <= 5; i++ {
		add(fmt.Sprintf("STKEYS%d", i))
	}
	// Faces: STFST<tier><variant>, plus the special states.
	for tier := 0; tier <= 4; tier++ {
		for v := 0; v <= 2; v++ {
			add(fmt.Sprintf("STFST%d%d", tier, v))
		}
		add(fmt.Sprintf("STFTL%d0", tier))
		add(fmt.Sprintf("STFTR%d0", tier))
		add(fmt.Sprintf("STFOUCH%d", tier))
		add(fmt.Sprintf("STFEVL%d", tier))
		add(fmt.Sprintf("STFKILL%d", tier))
	}
	add("STFGOD0")
	add("STFDEAD0")
	return out
}

func must(err error) {
	if err != nil {
		log.Fatalf("gentex: %v", err)
	}
}

// fsName percent-encodes any byte of a lump name that isn't safe in a
// filename — Doom sprite frames run past 'Z' into '[', '\\', ']', and a
// backslash in particular would be read as a path separator on Windows.
// assets/hires.go decodes this back to the exact lump name at load time.
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

func indexedToRGBA(w, h int, indices []int16, pal wad.Palette) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			idx := indices[y*w+x]
			if idx < 0 {
				continue // leave transparent
			}
			c := pal[byte(idx)]
			img.SetRGBA(x, y, color.RGBA{c.R, c.G, c.B, 255})
		}
	}
	return img
}

func flatToRGBA(f *wad.Flat, pal wad.Palette) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, f.Size, f.Size))
	for y := 0; y < f.Size; y++ {
		for x := 0; x < f.Size; x++ {
			c := pal[f.Pixels[y*f.Size+x]]
			img.SetRGBA(x, y, color.RGBA{c.R, c.G, c.B, 255})
		}
	}
	return img
}

func savePNG(path string, img *image.RGBA) {
	f, err := os.Create(path)
	if err != nil {
		log.Fatalf("gentex: %v", err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		log.Fatalf("gentex: %v", err)
	}
}

// enhance upscales src by UpscaleFactor with bilinear interpolation, then
// layers on synthetic fine detail so it reads as genuinely more detailed
// rather than a soft, blurred-up copy: fine grain (material texture), and
// edge-aware darkening (a cheap ambient-occlusion-like bevel at panel
// seams / brick mortar / any real edge in the source art).
//
// Both passes work from a *blurred* copy of the upscaled image, not the raw
// upscale — a small/blocky source's own pixel grid, bilinearly stretched,
// is full of hard little ramps that a naive edge detector reads as "edges"
// everywhere, which just re-emphasizes the low-res source's own blockiness
// instead of adding anything new. Blurring first means only real,
// larger-scale feature boundaries (a panel seam, a mortar line) survive to
// be beveled, and the grain field is smooth, low-frequency mottling rather
// than per-pixel static.
func enhance(src *image.RGBA, seedName string) *image.RGBA {
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	dw, dh := sw*UpscaleFactor, sh*UpscaleFactor
	up := bilinearUpscale(src, dw, dh)
	smooth := boxBlur(up, UpscaleFactor) // ~1 original-texel radius

	edges := sobelMagnitude(smooth)
	grain := newSmoothGrain(seedName, dw, dh)

	out := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		for x := 0; x < dw; x++ {
			c := up.RGBAAt(x, y)
			if c.A == 0 {
				out.SetRGBA(x, y, c)
				continue
			}
			// Edge bevel: darken close to a detected edge, proportional to
			// how strong it is, with a threshold so only genuinely strong
			// boundaries (not general shading gradients) bevel at all.
			e := edges[y*dw+x]
			e = math.Max(0, e-0.3) / 0.7 // remap [0.3,1]->[0,1], clip below
			mul := 1.0 - e*0.30

			// Gentle low-frequency mottling — reads as material grain
			// without looking like scratches or sensor noise.
			mul += grain.at(x, y) * 0.05

			r := clampByte(float64(c.R) * mul)
			gr := clampByte(float64(c.G) * mul)
			b := clampByte(float64(c.B) * mul)
			out.SetRGBA(x, y, color.RGBA{r, gr, b, c.A})
		}
	}
	return out
}

// enhanceSprite is enhance for images with 1-bit transparency (every
// sprite / status-bar graphic). Two extra steps bracket the shared
// enhance pass:
//   - edgeBleed first, so the bilinear upscale near a sprite's silhouette
//     samples real edge colours instead of the transparent void's black —
//     otherwise every sprite picks up a dark fringe.
//   - alpha re-thresholding after, so the feathered edge the upscale
//     produces snaps back to the hard cut-out Doom sprites are drawn with.
func enhanceSprite(src *image.RGBA, name string) *image.RGBA {
	out := enhance(edgeBleed(src, 2), name)
	w, h := out.Bounds().Dx(), out.Bounds().Dy()
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := out.RGBAAt(x, y)
			if c.A >= 128 {
				out.SetRGBA(x, y, color.RGBA{c.R, c.G, c.B, 255})
			} else {
				out.SetRGBA(x, y, color.RGBA{}) // fully clear
			}
		}
	}
	return out
}

// edgeBleed returns a copy of src where every transparent pixel within
// `iterations` steps of an opaque one takes the average RGB of its opaque
// neighbours (alpha stays 0). Filling the transparent margin with plausible
// colour stops a later bilinear upscale from blending silhouette pixels
// toward (0,0,0,0) and haloing the sprite.
func edgeBleed(src *image.RGBA, iterations int) *image.RGBA {
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	copy(out.Pix, src.Pix)

	// valid[i] marks a pixel that carries a usable colour — opaque to begin
	// with, or filled by a previous bleed pass.
	valid := make([]bool, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			valid[y*w+x] = src.RGBAAt(x, y).A != 0
		}
	}

	for it := 0; it < iterations; it++ {
		type fill struct {
			x, y    int
			r, g, b byte
		}
		var fills []fill
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				if valid[y*w+x] {
					continue
				}
				var sr, sg, sb, n float64
				for dy := -1; dy <= 1; dy++ {
					yy := y + dy
					if yy < 0 || yy >= h {
						continue
					}
					for dx := -1; dx <= 1; dx++ {
						xx := x + dx
						if xx < 0 || xx >= w || !valid[yy*w+xx] {
							continue
						}
						nc := out.RGBAAt(xx, yy)
						sr += float64(nc.R)
						sg += float64(nc.G)
						sb += float64(nc.B)
						n++
					}
				}
				if n > 0 {
					fills = append(fills, fill{x, y, clampByte(sr / n), clampByte(sg / n), clampByte(sb / n)})
				}
			}
		}
		if len(fills) == 0 {
			break
		}
		for _, f := range fills {
			out.SetRGBA(f.x, f.y, color.RGBA{f.r, f.g, f.b, 0}) // colour only, still transparent
			valid[f.y*w+f.x] = true
		}
	}
	return out
}

// boxBlur returns a copy of img blurred with a (2*radius+1)-wide square box
// filter, alpha-aware (transparent source pixels don't contribute).
func boxBlur(img *image.RGBA, radius int) *image.RGBA {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var sr, sg, sb, sa, n float64
			for dy := -radius; dy <= radius; dy++ {
				yy := clampInt(y+dy, 0, h-1)
				for dx := -radius; dx <= radius; dx++ {
					xx := clampInt(x+dx, 0, w-1)
					c := img.RGBAAt(xx, yy)
					if c.A == 0 {
						continue
					}
					sr += float64(c.R)
					sg += float64(c.G)
					sb += float64(c.B)
					sa += float64(c.A)
					n++
				}
			}
			if n == 0 {
				continue
			}
			out.SetRGBA(x, y, color.RGBA{clampByte(sr / n), clampByte(sg / n), clampByte(sb / n), clampByte(sa / n)})
		}
	}
	return out
}

// smoothGrain is low-frequency value noise (a coarse random grid,
// bilinearly interpolated) — material mottling, not per-pixel static.
type smoothGrain struct {
	grid           [][]float64
	cellsX, cellsY int
	w, h           int
}

func newSmoothGrain(seedName string, w, h int) *smoothGrain {
	seed := fnv32(seedName)
	cellsX := max(2, w/24)
	cellsY := max(2, h/24)
	grid := make([][]float64, cellsY+1)
	for y := range grid {
		grid[y] = make([]float64, cellsX+1)
		for x := range grid[y] {
			hv := grainAt(seed, x, y)
			grid[y][x] = hv
		}
	}
	return &smoothGrain{grid: grid, cellsX: cellsX, cellsY: cellsY, w: w, h: h}
}

func (g *smoothGrain) at(px, py int) float64 {
	fx := float64(px) / float64(g.w) * float64(g.cellsX)
	fy := float64(py) / float64(g.h) * float64(g.cellsY)
	x0 := clampInt(int(fx), 0, g.cellsX-1)
	y0 := clampInt(int(fy), 0, g.cellsY-1)
	x1, y1 := x0+1, y0+1
	tx, ty := fx-float64(x0), fy-float64(y0)
	tx = tx * tx * (3 - 2*tx) // smoothstep
	ty = ty * ty * (3 - 2*ty)
	v00, v10 := g.grid[y0][x0], g.grid[y0][x1]
	v01, v11 := g.grid[y1][x0], g.grid[y1][x1]
	top := v00 + (v10-v00)*tx
	bot := v01 + (v11-v01)*tx
	return top + (bot-top)*ty
}

func clampByte(v float64) byte {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return byte(v)
}

// bilinearUpscale scales src up to exactly dw x dh.
func bilinearUpscale(src *image.RGBA, dw, dh int) *image.RGBA {
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	out := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		sy := (float64(y)+0.5)/float64(dh)*float64(sh) - 0.5
		y0 := clampInt(int(math.Floor(sy)), 0, sh-1)
		y1 := clampInt(y0+1, 0, sh-1)
		ty := sy - float64(y0)
		for x := 0; x < dw; x++ {
			sx := (float64(x)+0.5)/float64(dw)*float64(sw) - 0.5
			x0 := clampInt(int(math.Floor(sx)), 0, sw-1)
			x1 := clampInt(x0+1, 0, sw-1)
			tx := sx - float64(x0)

			c00 := src.RGBAAt(x0, y0)
			c10 := src.RGBAAt(x1, y0)
			c01 := src.RGBAAt(x0, y1)
			c11 := src.RGBAAt(x1, y1)

			r := lerp2(float64(c00.R), float64(c10.R), float64(c01.R), float64(c11.R), tx, ty)
			g := lerp2(float64(c00.G), float64(c10.G), float64(c01.G), float64(c11.G), tx, ty)
			b := lerp2(float64(c00.B), float64(c10.B), float64(c01.B), float64(c11.B), tx, ty)
			a := lerp2(float64(c00.A), float64(c10.A), float64(c01.A), float64(c11.A), tx, ty)
			out.SetRGBA(x, y, color.RGBA{clampByte(r), clampByte(g), clampByte(b), clampByte(a)})
		}
	}
	return out
}

func lerp2(v00, v10, v01, v11, tx, ty float64) float64 {
	top := v00 + (v10-v00)*tx
	bot := v01 + (v11-v01)*tx
	return top + (bot-top)*ty
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// sobelMagnitude returns a per-pixel gradient magnitude, normalized so most
// real edges land near 1.0 (the vast majority of pixels, being flat
// interior fill, sit far below that).
func sobelMagnitude(img *image.RGBA) []float64 {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	lum := make([]float64, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := img.RGBAAt(x, y)
			lum[y*w+x] = 0.299*float64(c.R) + 0.587*float64(c.G) + 0.114*float64(c.B)
		}
	}
	get := func(x, y int) float64 {
		x = clampInt(x, 0, w-1)
		y = clampInt(y, 0, h-1)
		return lum[y*w+x]
	}
	out := make([]float64, w*h)
	maxMag := 0.0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			gx := -get(x-1, y-1) - 2*get(x-1, y) - get(x-1, y+1) + get(x+1, y-1) + 2*get(x+1, y) + get(x+1, y+1)
			gy := -get(x-1, y-1) - 2*get(x, y-1) - get(x+1, y-1) + get(x-1, y+1) + 2*get(x, y+1) + get(x+1, y+1)
			mag := math.Hypot(gx, gy)
			out[y*w+x] = mag
			if mag > maxMag {
				maxMag = mag
			}
		}
	}
	if maxMag > 0 {
		for i := range out {
			out[i] = math.Min(1, out[i]/maxMag*3)
		}
	}
	return out
}

// grainAt returns a pseudo-random value in [-1, 1] for pixel (x, y) under
// seed — a cheap integer hash rather than a sequential RNG, so it's stable
// and independent of scan order.
func grainAt(seed uint32, x, y int) float64 {
	h := uint32(x)*374761393 + uint32(y)*668265263 + seed*2246822519
	h = (h ^ (h >> 13)) * 1274126177
	h ^= h >> 16
	return (float64(h%2000) / 1000.0) - 1.0
}

func fnv32(s string) uint32 {
	const prime = 16777619
	h := uint32(2166136261)
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= prime
	}
	return h
}
