// Command importquake converts a Quake 1 texture pack (a GZDoom-style .pk3,
// really a zip of .tga files) into the loose PNG tree the engine's hi-res
// override loader reads, renaming each Quake texture to the Doom lump name
// it stands in for according to a curated table (mapping.txt). Output goes
// to assets/textures/quake1/walls|flats/<DOOM_LUMP>.png — a directory the
// engine only consults when config's quake1Textures switch is on, and one
// that is deliberately separate from assets/textures/walls|flats so the
// existing hi-res PNG set is never touched.
//
// Usage (from the repo root):
//
//	go run ./tools/importquake                 # pk3 + mapping from defaults
//	go run ./tools/importquake -max 0          # keep native TGA resolution
//	go run ./tools/importquake -mapping my.txt # a different pairing table
//
// The pack itself is not committed (it is id-derived art with its own
// terms — see game_design.txt sections 11 and 18); drop it in
// assets/import/ and run this. Re-running overwrites the output.
package main

import (
	"archive/zip"
	_ "embed"
	"flag"
	"fmt"
	"image"
	"image/png"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"

	xdraw "golang.org/x/image/draw"
)

//go:embed mapping.txt
var defaultMapping string

func main() {
	log.SetFlags(0)
	log.SetPrefix("importquake: ")

	pk3Path := flag.String("pk3", filepath.Join("assets", "import", "Quake1_Textures.pk3"), "Quake texture pack (.pk3/.zip of .tga)")
	mappingPath := flag.String("mapping", "", "Doom->Quake table (default: the embedded tools/importquake/mapping.txt)")
	outDir := flag.String("out", filepath.Join("assets", "textures", "quake1"), "output root; walls/ and flats/ are created under it")
	maxEdge := flag.Int("max", 1024, "cap the longest edge of each texture (Catmull-Rom downscale); 0 = keep the TGA's native size")
	flag.Parse()

	mappingText := defaultMapping
	mappingSrc := "embedded mapping.txt"
	if *mappingPath != "" {
		b, err := os.ReadFile(*mappingPath)
		if err != nil {
			log.Fatalf("read mapping %s: %v", *mappingPath, err)
		}
		mappingText, mappingSrc = string(b), *mappingPath
	}
	rows, err := parseMapping(mappingText)
	if err != nil {
		log.Fatalf("%s: %v", mappingSrc, err)
	}
	log.Printf("%s: %d entries", mappingSrc, len(rows))

	zr, err := zip.OpenReader(*pk3Path)
	if err != nil {
		log.Fatalf("open %s: %v", *pk3Path, err)
	}
	defer zr.Close()

	// Index the archive by lower-cased file stem ("ikbwall02", "+0lvfall"),
	// last one wins — the pack has a couple of duplicated names in
	// subfolders and mapping.txt refers to the top-level textures/ copy.
	byStem := map[string]*zip.File{}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		base := path.Base(f.Name)
		if !strings.EqualFold(filepath.Ext(base), ".tga") {
			continue
		}
		stem := strings.ToLower(strings.TrimSuffix(base, filepath.Ext(base)))
		if _, dup := byStem[stem]; !dup || path.Dir(f.Name) == "textures" {
			byStem[stem] = f
		}
	}
	log.Printf("%s: %d .tga entries", *pk3Path, len(byStem))

	wallsOut := filepath.Join(*outDir, "walls")
	flatsOut := filepath.Join(*outDir, "flats")
	must(os.MkdirAll(wallsOut, 0o755))
	must(os.MkdirAll(flatsOut, 0o755))

	decoded := map[string]*image.NRGBA{} // stem -> decoded (+ scaled) image, reused across rows
	var written, skipped int
	missing := map[string]bool{}

	for _, r := range rows {
		src, ok := byStem[strings.ToLower(r.quake)]
		if !ok {
			if !missing[r.quake] {
				log.Printf("  %-10s <- %-14s  MISSING in pack, skipped", r.doom, r.quake)
				missing[r.quake] = true
			}
			skipped++
			continue
		}

		img := decoded[strings.ToLower(r.quake)]
		if img == nil {
			img, err = readTGAFromZip(src)
			if err != nil {
				log.Printf("  %s: decode %s: %v", r.doom, src.Name, err)
				skipped++
				continue
			}
			if *maxEdge > 0 {
				img = capLongestEdge(img, *maxEdge)
			}
			decoded[strings.ToLower(r.quake)] = img
		}

		dstDir := wallsOut
		if r.kind == "flat" {
			dstDir = flatsOut
		}
		dst := filepath.Join(dstDir, r.doom+".png")
		if err := writePNG(dst, img); err != nil {
			log.Printf("  %s: %v", dst, err)
			skipped++
			continue
		}
		written++
	}

	log.Printf("done: %d PNG(s) written to %s, %d skipped (%d Quake name(s) not in the pack)",
		written, *outDir, skipped, len(missing))
	if written == 0 {
		os.Exit(1)
	}
}

type row struct {
	doom  string
	quake string
	kind  string // "wall" | "flat"
}

func parseMapping(text string) ([]row, error) {
	var out []row
	seen := map[string]int{} // doom+kind -> line, to catch accidental double maps
	for i, ln := range strings.Split(text, "\n") {
		line := strings.TrimSpace(ln)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 3 {
			return nil, fmt.Errorf("line %d: want 3 fields <DOOM> <quake> <wall|flat>, got %d: %q", i+1, len(fields), line)
		}
		r := row{doom: strings.ToUpper(fields[0]), quake: fields[1], kind: strings.ToLower(fields[2])}
		if r.kind != "wall" && r.kind != "flat" {
			return nil, fmt.Errorf("line %d: kind must be wall or flat, got %q", i+1, fields[2])
		}
		key := r.doom + "/" + r.kind
		if prev, dup := seen[key]; dup {
			log.Printf("  note: %s (%s) mapped again on line %d, overriding line %d", r.doom, r.kind, i+1, prev)
		}
		seen[key] = i + 1
		out = append(out, r)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no entries")
	}
	return out, nil
}

func readTGAFromZip(f *zip.File) (*image.NRGBA, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return decodeTGA(rc)
}

// capLongestEdge returns src unchanged if it already fits within max on
// both axes, otherwise a Catmull-Rom downscale that brings the longer edge
// to max and keeps the aspect ratio (rounding the short edge, min 1).
func capLongestEdge(src *image.NRGBA, max int) *image.NRGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= max && h <= max {
		return src
	}
	nw, nh := w, h
	if w >= h {
		nw = max
		nh = h * max / w
	} else {
		nh = max
		nw = w * max / h
	}
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	dst := image.NewNRGBA(image.Rect(0, 0, nw, nh))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, b, xdraw.Over, nil)
	return dst
}

func writePNG(dstPath string, img image.Image) error {
	f, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(f, img); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
