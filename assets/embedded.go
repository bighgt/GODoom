package assets

import (
	"archive/zip"
	"bufio"
	"bytes"
	"fmt"
	"image"
	"image/png"
	"io"
	"log"
	"path"
	"sort"
	"strconv"
	"strings"

	"twopointfive/wad"
)

// A cmd/wad3d WAD carries its hi-res wall/flat textures inside itself, in
// one TPFHIRES lump (wad.TPFHiresLump): a zip holding walls/<NAME>.png and
// flats/<NAME>.png. One container lump rather than a lump per texture
// because wad.Merge lets a later lump shadow an earlier one by name — a PNG
// lump called NUKAGE1 would hide the IWAD's real flat of that name from
// every w.Find — and because a zip path has no 8-character limit.
//
// The set is indexed once at New and each PNG decoded lazily on first use,
// exactly like the disk hi-res set (hires.go); WallTexture/Flat consult it
// after the explicit opt-in packs (Heretic, Quake 1) and before the disk
// hi-res PNGs — a recompiled WAD's own textures win over whatever happens
// to be in assets/textures/.

// EmbeddedWallsDir / EmbeddedFlatsDir are the zip directories a TPFHIRES
// container files its textures under.
const (
	EmbeddedWallsDir = "walls"
	EmbeddedFlatsDir = "flats"
)

// indexEmbedded opens w's TPFHIRES lump (if any) and returns its wall and
// flat entries keyed by upper-cased lump name. Both maps are nil when the
// WAD carries no container. A malformed container is logged and ignored —
// the textures then resolve exactly as if it weren't there.
func indexEmbedded(w *wad.WAD) (walls, flats map[string]*zip.File) {
	zr, ok := w.ZipLump(wad.TPFHiresLump)
	if !ok {
		return nil, nil
	}
	walls, flats = map[string]*zip.File{}, map[string]*zip.File{}
	for _, f := range zr.File {
		dir, file := path.Split(f.Name)
		if !strings.EqualFold(path.Ext(file), ".png") {
			continue
		}
		name := strings.ToUpper(strings.TrimSuffix(file, path.Ext(file)))
		switch strings.TrimSuffix(dir, "/") {
		case EmbeddedWallsDir:
			walls[name] = f
		case EmbeddedFlatsDir:
			flats[name] = f
		}
	}
	log.Printf("assets: %s: %d embedded wall texture(s), %d flat(s)", wad.TPFHiresLump, len(walls), len(flats))
	return walls, flats
}

// loadEmbedded decodes one embedded PNG, sized against origWidthUnits like
// loadHiresOverride. Returns nil on failure (the caller falls through).
func loadEmbedded(f *zip.File, origWidthUnits float64) *RGBA {
	rc, err := f.Open()
	if err != nil {
		log.Printf("assets: %s %s: %v", wad.TPFHiresLump, f.Name, err)
		return nil
	}
	defer rc.Close()
	return decodeHiresOverride(rc, wad.TPFHiresLump+":"+f.Name, origWidthUnits)
}

// HiresOverrideFiles returns the disk hi-res override index — wall and flat
// lump name -> PNG path, the same set assets.New indexes from
// assets/textures/walls|flats. For cmd/wad3d, which packs exactly these
// files into a TPFHIRES container. Either map is nil if its directory
// isn't found.
func HiresOverrideFiles() (walls, flats map[string]string) {
	return indexHiresWalls(), indexHiresFlats()
}

// EmbeddedCount reports how many wall textures and flats this Textures is
// serving from a WAD-embedded TPFHIRES container (0, 0 when none).
func (t *Textures) EmbeddedCount() (walls, flats int) {
	return len(t.embeddedWalls), len(t.embeddedFlats)
}

// HereticOverrideFiles is HiresOverrideFiles for the optional Heretic HD
// pack (assets/textures/heretic/walls|flats) — the set a Heretic session
// prefers, so the one cmd/wad3d embeds for a Heretic WAD.
func HereticOverrideFiles() (walls, flats map[string]string) {
	return indexHereticWalls(), indexHereticFlats()
}

// EmbeddedSpritesDir / EmbeddedSoundsDir / EmbeddedSpriteMeta are a
// TPFWEAPN weapon container's layout (see wad.TPFWeaponsLump).
const (
	EmbeddedSpritesDir = "sprites"
	EmbeddedSoundsDir  = "sounds"
	EmbeddedSpriteMeta = "sprites/offsets.txt"
)

// embeddedSprite is one TPFWEAPN sprite: its PNG plus the RGBA fields a
// PNG can't carry.
type embeddedSprite struct {
	f                  *zip.File
	offX, offY, bottom int
	tpu                float64
}

// indexEmbeddedSprites reads w's TPFWEAPN container's sprite set, keyed by
// lump name. nil when the WAD carries none.
func indexEmbeddedSprites(w *wad.WAD) map[string]embeddedSprite {
	zr, ok := w.ZipLump(wad.TPFWeaponsLump)
	if !ok {
		return nil
	}
	meta := map[string]embeddedSprite{}
	files := map[string]*zip.File{}
	for _, f := range zr.File {
		if f.Name == EmbeddedSpriteMeta {
			rc, err := f.Open()
			if err != nil {
				log.Printf("assets: %s %s: %v", wad.TPFWeaponsLump, f.Name, err)
				continue
			}
			meta = parseSpriteMeta(rc)
			rc.Close()
			continue
		}
		dir, file := path.Split(f.Name)
		if strings.TrimSuffix(dir, "/") == EmbeddedSpritesDir && strings.EqualFold(path.Ext(file), ".png") {
			files[strings.TrimSuffix(file, path.Ext(file))] = f
		}
	}
	out := make(map[string]embeddedSprite, len(files))
	for name, f := range files {
		m, ok := meta[name]
		if !ok {
			m = embeddedSprite{tpu: 1}
		}
		m.f = f
		out[name] = m
	}
	log.Printf("assets: %s: %d embedded weapon sprite(s)", wad.TPFWeaponsLump, len(out))
	return out
}

// parseSpriteMeta reads offsets.txt: "LUMP offX offY tpu bottomPad".
func parseSpriteMeta(r io.Reader) map[string]embeddedSprite {
	out := map[string]embeddedSprite{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 5 {
			continue
		}
		ox, e1 := strconv.Atoi(f[1])
		oy, e2 := strconv.Atoi(f[2])
		tpu, e3 := strconv.ParseFloat(f[3], 64)
		bp, e4 := strconv.Atoi(f[4])
		if e1 != nil || e2 != nil || e3 != nil || e4 != nil || tpu <= 0 {
			continue
		}
		out[f[0]] = embeddedSprite{offX: ox, offY: oy, tpu: tpu, bottom: bp}
	}
	return out
}

// loadEmbeddedSprite decodes one TPFWEAPN sprite back into the exact RGBA
// the tool froze. nil on failure.
func loadEmbeddedSprite(es embeddedSprite) *RGBA {
	rc, err := es.f.Open()
	if err != nil {
		log.Printf("assets: %s %s: %v", wad.TPFWeaponsLump, es.f.Name, err)
		return nil
	}
	defer rc.Close()
	img, err := png.Decode(rc)
	if err != nil {
		log.Printf("assets: %s %s: %v", wad.TPFWeaponsLump, es.f.Name, err)
		return nil
	}
	im := ImageToRGBA(img, es.tpu)
	im.OffsetX, im.OffsetY, im.BottomPad = es.offX, es.offY, es.bottom
	return im
}

// EncodeEmbeddedSprite is loadEmbeddedSprite's inverse, for cmd/wad3d:
// im's pixels as a PNG, and its offsets.txt line.
func EncodeEmbeddedSprite(name string, im *RGBA) (pngData []byte, metaLine string, err error) {
	img := &image.NRGBA{Pix: im.Pix, Stride: im.Width * 4, Rect: image.Rect(0, 0, im.Width, im.Height)}
	var b bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.DefaultCompression}).Encode(&b, img); err != nil {
		return nil, "", err
	}
	tpu := im.TexelsPerUnit
	if tpu <= 0 {
		tpu = 1
	}
	return b.Bytes(), fmt.Sprintf("%s %d %d %s %d", name, im.OffsetX, im.OffsetY,
		strconv.FormatFloat(tpu, 'g', -1, 64), im.BottomPad), nil
}

// WeaponPackFrames lists every frame name the on-disk HD weapon pack
// (assets/weapons/*.pk3) provides, sorted — the viewmodel frames Doom's
// weapon table names that no IWAD ships (FIS0, PI0G, SHOG, ...).
func (t *Textures) WeaponPackFrames() []string {
	out := make([]string, 0, len(t.weaponSprites))
	for n := range t.weaponSprites {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// EmbeddedSpriteCount reports how many weapon sprites this Textures serves
// from a WAD-embedded TPFWEAPN container.
func (t *Textures) EmbeddedSpriteCount() int { return len(t.embeddedSprites) }
