package assets

import (
	"image"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// HiresTexelsPerUnit is the shared upscale factor between tools/gentex
// (which generates assets/textures/walls|flats/*.png at this many texels
// per original texel/map-unit) and the renderer (which must sample these
// overrides at this same density — see RGBA.TexelsPerUnit and
// game_design.txt). Both sides read this one constant so they can never
// drift apart.
const HiresTexelsPerUnit = 4.0

// hiresWallsDir/hiresFlatsDir are where tools/gentex writes its output,
// and where loadHiresDir looks for it at startup — see findAssetDir for
// how the base directory itself is located.
const (
	hiresWallsDir   = "assets/textures/walls"
	hiresFlatsDir   = "assets/textures/flats"
	hiresSpritesDir = "assets/textures/sprites"

	// quakeWallsDir/quakeFlatsDir hold an optional Quake 1 texture pack that
	// tools/importquake has converted to PNG and renamed from Quake texture
	// names to the Doom lump names they stand in for (a curated table — the
	// two games share no naming at all). Indexed exactly like the hi-res set
	// above, but WallTexture/Flat only consult them when the config's
	// quake1Textures switch is on (see Textures.SetQuake1Textures); the
	// directory is deliberately separate so enabling the pack never disturbs
	// the assets/textures/walls|flats overrides.
	quakeWallsDir = "assets/textures/quake1/walls"
	quakeFlatsDir = "assets/textures/quake1/flats"
)

// loadHiresWalls/loadHiresFlats load the procedurally upscaled wall
// texture / flat replacements (see tools/gentex, game_design.txt) from
// disk — deliberately *not* embedded into the binary via go:embed: this
// set is ~16MB of PNGs, and baking it into every copy of the executable
// is exactly the kind of bloat that doesn't belong in the binary itself.
// Loaded once by assets.New and cached on the Textures it returns.
//
// Non-fatal if missing entirely (an executable copied without its
// assets/ directory alongside it still runs, just without the upscaled
// textures — WallTexture/Flat fall back to the WAD's own data exactly as
// if no override existed) or if an individual file fails to decode (that
// one texture just isn't overridden; every other file still loads).
func indexHiresWalls() map[string]string   { return indexHiresDir(hiresWallsDir) }
func indexHiresFlats() map[string]string   { return indexHiresDir(hiresFlatsDir) }
func indexHiresSprites() map[string]string { return indexHiresDir(hiresSpritesDir) }
func indexQuakeWalls() map[string]string   { return indexHiresDir(quakeWallsDir) }
func indexQuakeFlats() map[string]string   { return indexHiresDir(quakeFlatsDir) }

// indexHiresDir walks relDir (recursively) and returns a lump-name ->
// PNG-path map *without* decoding anything — the override set can be well
// over a hundred megabytes of PNGs once sprites are included, far too much
// to eagerly decode into RAM at startup. Each file is decoded lazily on
// first use (see loadHiresOverride) and then cached like any other
// resolved texture.
//
// The walk is recursive so a third-party pack's own directory layout can
// be copied in wholesale (see game_design.txt section 11). A lump name
// that appears in more than one file is resolved to the last one seen and
// logged, since the outcome then depends on walk order.
func indexHiresDir(relDir string) map[string]string {
	dir, ok := findAssetDir(relDir)
	if !ok {
		return nil
	}
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil // skip unreadable entries, keep walking
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".png") {
			return nil
		}
		base := strings.TrimSuffix(d.Name(), filepath.Ext(d.Name()))
		name := strings.ToUpper(decodeFSName(base))
		if prev, dup := out[name]; dup && prev != path {
			log.Printf("assets: hi-res override %q: %s shadows %s", name, path, prev)
		}
		out[name] = path
		return nil
	})
	if err != nil {
		log.Printf("assets: walk %s: %v", dir, err)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// loadHiresOverride decodes one indexed PNG into an RGBA. origWidthUnits is
// the width, in map units (= original pixels, since the WAD is 1 texel/
// unit), of the texture/flat/sprite this override replaces: TexelsPerUnit
// is then the ratio of the PNG's own width to that, so a pack whose images
// are 2x, 4x, 8x (or an odd fraction) all sample at the right density
// rather than assuming tools/gentex's fixed 4x. origWidthUnits <= 0 (an
// override for a name the loaded WAD doesn't define) falls back to
// HiresTexelsPerUnit. Returns nil on any failure — the caller then uses
// the WAD's own version exactly as if no override existed.
func loadHiresOverride(path string, origWidthUnits float64) *RGBA {
	f, err := os.Open(path)
	if err != nil {
		log.Printf("assets: open %s: %v", path, err)
		return nil
	}
	img, err := png.Decode(f)
	f.Close()
	if err != nil {
		log.Printf("assets: decode %s: %v", path, err)
		return nil
	}

	tpu := HiresTexelsPerUnit
	if w := img.Bounds().Dx(); origWidthUnits > 0 && w > 0 {
		tpu = float64(w) / origWidthUnits
		switch {
		case tpu < 0.5:
			tpu = 0.5
		case tpu > 16:
			tpu = 16
		}
	}
	return imageToRGBA(img, tpu)
}

// decodeFSName reverses tools/gentex's fsName: %XX hex escapes (used for
// bytes that aren't filename-safe, e.g. the backslash in a sprite frame
// like "VILE\1") become their literal byte again. A name with no escapes
// passes straight through.
func decodeFSName(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	var b []byte
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+3 <= len(s) {
			if v, err := strconv.ParseUint(s[i+1:i+3], 16, 8); err == nil {
				b = append(b, byte(v))
				i += 2
				continue
			}
		}
		b = append(b, s[i])
	}
	return string(b)
}

// findAssetDir looks for relDir (a path like "assets/textures/walls"):
// first relative to the current working directory (how this project is run
// during development — see game_design.txt's build instructions), then
// relative to the running executable's own directory AND up to four parents
// of it. The exe-dir parent walk is what lets bin\engine.exe find the
// repo's assets/ folder when it's launched from bin\ or double-clicked with
// an unknown working directory — matching LoadVoxelPackDir, so every disk
// asset (skybox, hi-res textures, HD weapons) resolves the same way. The
// walk is only up from the *executable*, never the working directory, so a
// `go test` run (cwd = the package dir, exe = a temp binary) doesn't
// suddenly pick up the repo's optional asset packs and slow every test.
// Returns ok=false if none exists — the optional asset just isn't there.
func findAssetDir(relDir string) (string, bool) {
	isDir := func(p string) bool {
		st, err := os.Stat(p)
		return err == nil && st.IsDir()
	}
	if isDir(relDir) {
		return relDir, true
	}
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}
	dir := filepath.Dir(exe)
	for i := 0; i < 5 && dir != ""; i++ {
		if cand := filepath.Join(dir, relDir); isDir(cand) {
			return cand, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", false
}

// imageToRGBA converts a decoded image.Image into this package's RGBA
// format at the given texel density — shared by loadHiresDir above and
// skybox.go's LoadSkybox (which always passes 1, since the sky isn't
// sampled in map-unit space at all).
func imageToRGBA(img image.Image, texelsPerUnit float64) *RGBA {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	pix := make([]byte, w*h*4)
	if nrgba, ok := img.(*image.NRGBA); ok && nrgba.Stride == w*4 && b.Min.X == 0 && b.Min.Y == 0 {
		copy(pix, nrgba.Pix)
	} else {
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				r, g, bch, a := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
				o := (y*w + x) * 4
				pix[o], pix[o+1], pix[o+2], pix[o+3] = byte(r>>8), byte(g>>8), byte(bch>>8), byte(a>>8)
			}
		}
	}
	return &RGBA{Width: w, Height: h, Pix: pix, TexelsPerUnit: texelsPerUnit}
}
