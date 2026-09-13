// Command importhereticpbr pulls the base-colour (albedo) layer out of a
// Heretic HD/PBR texture pack — a GZDoom-style .pk3 whose PNGs are already
// named by Heretic lump name — and drops it into the loose PNG tree the
// engine's hi-res override loader reads:
//
//	assets/textures/heretic/walls/<LUMP>.png   (from Data/jHeretic/Textures/)
//	assets/textures/heretic/flats/<LUMP>.png   (from Data/jHeretic/Flats/)
//
// That directory is Heretic-only: WallTexture/Flat consult it (ahead of
// every other override) just when the engine has called SetHeretic(true),
// i.e. when the loaded IWAD IsHeretic(). Keeping it separate from
// assets/textures/walls|flats means the pack's Doom-name collisions
// (SW1OFF, DOORSTON, ...) never touch a Doom session.
//
// The PBR maps (normal / specular / metallic / roughness / displacement /
// AO) and the pack's extra-smooth 32-frame liquid animations are ignored —
// this engine's renderer has no use for the first and never references the
// second by name.
//
// Usage (from the repo root):
//
//	go run ./tools/importhereticpbr                                  # defaults
//	go run ./tools/importhereticpbr -pk3 ~/Downloads/heretic_hd_pbr_hq.pk3
//	go run ./tools/importhereticpbr -max 1024                        # downscale
//
// The pack is not committed (community HD art, hundreds of MB, its own
// terms — see game_design.txt section 11); drop it in assets/import/ or
// pass -pk3. Re-running overwrites the output.
package main

import (
	"archive/zip"
	"flag"
	"image"
	"image/png"
	"io"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"

	xdraw "golang.org/x/image/draw"
)

// route maps a pk3 directory prefix to the output subdirectory under -out.
// Only these are imported; everything else in the pack is skipped.
var route = []struct{ prefix, sub string }{
	{"data/jheretic/textures/", "walls"},
	{"data/jheretic/flats/", "flats"},
}

// skip drops names that are handled by a different subsystem (the sky is
// built by raster/sky.go, not the wall/flat override path).
var skip = map[string]bool{"SKY1": true, "SKY2": true, "SKY3": true, "SKY5": true}

func main() {
	log.SetFlags(0)
	log.SetPrefix("importhereticpbr: ")

	pk3Path := flag.String("pk3", filepath.Join("assets", "import", "heretic_hd_pbr_hq.pk3"), "Heretic HD texture pack (.pk3/.zip)")
	outDir := flag.String("out", filepath.Join("assets", "textures", "heretic"), "output root; walls/ and flats/ are created under it")
	maxEdge := flag.Int("max", 0, "cap the longest edge of each texture (Catmull-Rom downscale, re-encoded); 0 = copy the pack's PNG byte-for-byte")
	flag.Parse()

	zr, err := zip.OpenReader(*pk3Path)
	if err != nil {
		log.Fatalf("open %s: %v", *pk3Path, err)
	}
	defer zr.Close()
	log.Printf("pack: %s", *pk3Path)

	for _, r := range route {
		if err := os.MkdirAll(filepath.Join(*outDir, r.sub), 0o755); err != nil {
			log.Fatalf("mkdir %s: %v", filepath.Join(*outDir, r.sub), err)
		}
	}

	var nCopied, nResized, nSkipped int
	var bytesOut int64
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := strings.ToLower(filepath.ToSlash(f.Name))
		if !strings.HasSuffix(name, ".png") {
			continue
		}
		sub := ""
		for _, r := range route {
			if strings.HasPrefix(name, r.prefix) {
				sub = r.sub
				break
			}
		}
		if sub == "" {
			continue
		}
		lump := strings.ToUpper(strings.TrimSuffix(path.Base(name), ".png"))
		if skip[lump] {
			nSkipped++
			continue
		}
		dst := filepath.Join(*outDir, sub, lump+".png")

		if *maxEdge <= 0 {
			n, err := copyEntry(f, dst)
			if err != nil {
				log.Fatalf("%s -> %s: %v", f.Name, dst, err)
			}
			bytesOut += n
			nCopied++
			continue
		}
		n, err := resizeEntry(f, dst, *maxEdge)
		if err != nil {
			log.Fatalf("%s -> %s: %v", f.Name, dst, err)
		}
		bytesOut += n
		nResized++
	}

	switch {
	case *maxEdge <= 0:
		log.Printf("done: %d copied, %d sky lumps skipped, %.0f MB -> %s",
			nCopied, nSkipped, float64(bytesOut)/1e6, *outDir)
	default:
		log.Printf("done: %d re-encoded (<=%dpx), %d sky lumps skipped, %.0f MB -> %s",
			nResized, *maxEdge, nSkipped, float64(bytesOut)/1e6, *outDir)
	}
	if nCopied+nResized == 0 {
		log.Fatalf("no matching PNGs found in %s — is this a Heretic HD pack with a Data/jHeretic/ tree?", *pk3Path)
	}
}

func copyEntry(f *zip.File, dst string) (int64, error) {
	rc, err := f.Open()
	if err != nil {
		return 0, err
	}
	defer rc.Close()
	w, err := os.Create(dst)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(w, rc)
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	return n, err
}

func resizeEntry(f *zip.File, dst string, maxEdge int) (int64, error) {
	rc, err := f.Open()
	if err != nil {
		return 0, err
	}
	src, err := png.Decode(rc)
	rc.Close()
	if err != nil {
		return 0, err
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	long := w
	if h > long {
		long = h
	}
	out := src
	if long > maxEdge {
		scale := float64(maxEdge) / float64(long)
		nw, nh := int(float64(w)*scale+0.5), int(float64(h)*scale+0.5)
		dstImg := image.NewNRGBA(image.Rect(0, 0, nw, nh))
		xdraw.CatmullRom.Scale(dstImg, dstImg.Bounds(), src, b, xdraw.Over, nil)
		out = dstImg
	}
	wf, err := os.Create(dst)
	if err != nil {
		return 0, err
	}
	if err := (&png.Encoder{CompressionLevel: png.DefaultCompression}).Encode(wf, out); err != nil {
		wf.Close()
		return 0, err
	}
	if err := wf.Close(); err != nil {
		return 0, err
	}
	st, _ := os.Stat(dst)
	if st == nil {
		return 0, nil
	}
	return st.Size(), nil
}
