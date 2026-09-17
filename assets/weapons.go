package assets

import (
	"archive/zip"
	"image"
	"image/png"
	"io"
	"log"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"twopointfive/wad"
)

// weaponsDir is where a locally-supplied HD weapon viewmodel pack lives —
// one or more .pk3 (zip) files, each holding replacement PNGs for the
// first-person weapon sprites (PISG*, SHTG*, SAWG*, CHGG*, MISG*, PLSG*,
// BFGG*, PUNG*, SHT2* and their muzzle-flash frames PISF/SHTF/CHGF/PLSF/
// MISF...). Keyed by lump name, these override the WAD's own psprite
// graphics through Textures.Sprite — see game_design.txt section 11.
const weaponsDir = "assets/weapons"

// weaponPackTexelsPerUnit is the LAST-RESORT sampling density for an HD
// weapon frame that has neither an authored scale (below) nor a same-named
// original in the loaded WAD to derive one from — the observed native
// scale of a plain "just PNGs" pack is a clean 2x over the vanilla psprite.
// Real packs bundling a TEXTURES lump (weaponTextureDefs) essentially never
// hit this path.
const weaponPackTexelsPerUnit = 2.0

// weaponFrame is one HD weapon-pack PNG plus whatever its pack's own
// TEXTURES lump declares for it (see weaponTextureDefs) — the authored
// display density (scale, 0 = none) and hotspot (offX/offY, valid only
// when hasOffset).
type weaponFrame struct {
	img       image.Image
	scale     float64
	offX      int
	offY      int
	hasOffset bool
}

// weaponOverrideRGBA turns one decoded HD weapon PNG into an RGBA at the
// right sampling density and hotspot.
//
// Density, in priority order: the pack's OWN authored scale (wf.scale,
// from its TEXTURES lump — this is what an artist actually intended a
// given frame's pixels-per-map-unit to be, and varies a lot frame to frame
// in a hand-painted HD pack, e.g. this project's "Smooth HD Weapons" pack
// runs anywhere from ~4x to ~9x depending on the sprite — a single flat
// guess can't fit all of them); else, when the WAD defines the original
// psprite, the exact PNG/original width ratio; else weaponPackTexelsPerUnit.
//
// Hotspot, in priority order: the pack's OWN authored offset (wf.offX/Y,
// same TEXTURES lump, already in this PNG's own native pixel space — no
// further scaling) — this is what makes a multi-sprite weapon's frames
// (gun body, recoil pose, muzzle-flash overlay) line up with each other at
// all, since id's own "huge negative LeftOffset/TopOffset shared origin"
// trick (see DrawWeapon's doc comment) only works when every frame agrees
// on where that origin is, and a per-frame independent guess can't
// reproduce an agreement it was never party to; else the WAD patch's own
// offset, scaled into the denser pixel space (matching the hi-res sprite
// path in textures.go); else a centred-bottom guess, which is only
// plausible for the GUN's own resting frame — used as a flash overlay's
// position it puts the flash wherever the gun sprite's bottom-center
// happens to be (often the grip, not the muzzle), not a real fallback so
// much as "at least draws something."
func weaponOverrideRGBA(img image.Image, patch *wad.Patch, wf weaponFrame) *RGBA {
	tpu := weaponPackTexelsPerUnit
	if patch != nil && patch.Width > 0 {
		if r := float64(img.Bounds().Dx()) / float64(patch.Width); r >= 0.5 && r <= 16 {
			tpu = r
		}
	}
	if wf.scale > 0 {
		tpu = wf.scale
	}
	out := imageToRGBA(img, tpu)
	switch {
	case wf.hasOffset:
		out.OffsetX = wf.offX
		out.OffsetY = wf.offY
	case patch != nil:
		out.OffsetX = int(float64(patch.LeftOffset) * tpu)
		out.OffsetY = int(float64(patch.TopOffset) * tpu)
	default:
		out.OffsetX = out.Width / 2
		out.OffsetY = out.Height
	}
	out.BottomPad = contentBottomPad(img)
	return out
}

// contentBottomPad scans up from img's bottom row and returns how many rows
// (in img's own native pixel space, before imageToRGBA's tpu scaling —
// callers scale it themselves the same way they scale OffsetX/OffsetY) are
// fully transparent below the lowest drawn pixel.
//
// A hand-cropped WAD patch is always tight (this is 0 for every vanilla
// sprite), but a modern HD pack's PNG canvas doesn't have to be — this
// project's "Smooth HD Weapons" rocket launcher, e.g., leaves roughly a
// quarter of its own canvas height empty below the gun art (room the
// pack's animator presumably wanted for a taller recoil frame elsewhere).
// DrawWeapon bottom-anchors the RESTING frame by its full canvas height, so
// that padding otherwise reads as a visible gap between the gun and the
// bottom of the screen; subtracting BottomPad from the anchoring height
// pushes the padding below the screen edge instead, without touching the
// canvas blit itself draws (still the full, unmodified image).
func contentBottomPad(img image.Image) int {
	b := img.Bounds()
	for y := b.Max.Y - 1; y >= b.Min.Y; y-- {
		for x := b.Min.X; x < b.Max.X; x++ {
			if _, _, _, a := img.At(x, y).RGBA(); a != 0 {
				return b.Max.Y - 1 - y
			}
		}
	}
	return b.Dy() // fully transparent frame — degenerate; don't let callers see a negative height
}

// loadWeaponPack finds weaponsDir (working directory first, then beside the
// exe — same as the skybox / hi-res set) and decodes every PNG inside every
// .pk3 there into a lump-name -> weaponFrame map. The lump name is the
// file's stem, upper-cased (GZDoom HD packs lay them out as
// hires/sprites/<weapon>/<LUMP>0.png). Returns nil when the directory or a
// readable pack is absent — the game then just uses the WAD's own weapon
// sprites.
//
// Unlike the lazily-indexed hi-res texture set (hires.go), this pack is
// small (a few dozen frames, a few MB decoded) and every frame is drawn
// the instant its weapon is equipped, so it's decoded eagerly here rather
// than on first use.
func loadWeaponPack() map[string]weaponFrame {
	dir, ok := findAssetDir(weaponsDir)
	if !ok {
		log.Printf("assets: no HD weapon pack (looked for %s/*.pk3) — using the WAD's weapon sprites", weaponsDir)
		return nil
	}
	return loadWeaponPackDir(dir)
}

// loadWeaponPackDir is the testable core of loadWeaponPack: read every
// .pk3 / .zip in dir and merge their PNG frames (plus any TEXTURES-
// declared scale/offset for them).
func loadWeaponPackDir(dir string) map[string]weaponFrame {
	ents, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("assets: read %s: %v", dir, err)
		}
		return nil
	}
	var packs []string
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		if ext := strings.ToLower(filepath.Ext(e.Name())); ext == ".pk3" || ext == ".zip" {
			packs = append(packs, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(packs) // deterministic shadowing when two packs define a frame

	out := map[string]weaponFrame{}
	for _, p := range packs {
		addWeaponPack(p, out)
	}
	if len(out) == 0 {
		return nil
	}
	names := make([]string, 0, len(out))
	for n := range out {
		names = append(names, n)
	}
	sort.Strings(names)
	log.Printf("assets: HD weapons — %d frames from %s (%s)", len(out), filepath.Base(packs[len(packs)-1]), strings.Join(names, " "))
	return out
}

// weaponTextureDef is one Sprite block's authored data — see
// weaponTextureDefs.
type weaponTextureDef struct {
	scale     float64
	offX      int
	offY      int
	hasOffset bool
}

// weaponTextureSpriteRE matches one ZDoom TEXTURES "Sprite" block — name,
// then everything up to its closing brace (these blocks don't nest, one
// Patch line each, so a non-greedy match to the next '}' is exact).
// weaponTextureScaleRE and weaponTextureOffsetRE then pull XScale/YScale
// and Offset out of that block's body.
var (
	weaponTextureSpriteRE = regexp.MustCompile(`(?is)Sprite\s+"([^"]+)"[^{]*\{(.*?)\}`)
	weaponTextureScaleRE  = regexp.MustCompile(`(?i)(X|Y)Scale\s+([0-9.]+)`)
	weaponTextureOffsetRE = regexp.MustCompile(`(?i)Offset\s+(-?[0-9]+)\s*,\s*(-?[0-9]+)`)
)

// weaponTextureDefs parses a GZDoom TEXTURES lump (as SLADE3 generates for
// an HD sprite-replacement pack) into lump name -> authored scale/offset.
// scale averages XScale/YScale when they differ slightly (this project's
// own "Smooth HD Weapons" pack's are usually within a few percent of each
// other — the same sprite's two axes independently fit to the original's
// aspect, not a real anisotropic intent). offX/offY are each Sprite
// block's own "Offset x, y" line, already in that block's declared (and
// actual PNG) pixel space — the shared-origin hotspot that makes a
// multi-frame weapon's gun/recoil/flash sprites line up with each other;
// see weaponOverrideRGBA's doc comment for why this matters more than the
// scale does. A "Graphic" block (a non-sprite image, e.g. a full-screen
// flash overlay) is not matched — those aren't looked up by weapon-frame
// name.
func weaponTextureDefs(data []byte) map[string]weaponTextureDef {
	out := map[string]weaponTextureDef{}
	for _, m := range weaponTextureSpriteRE.FindAllSubmatch(data, -1) {
		name := strings.ToUpper(string(m[1]))
		body := m[2]
		var d weaponTextureDef
		var x, y float64
		for _, sm := range weaponTextureScaleRE.FindAllSubmatch(body, -1) {
			v, err := strconv.ParseFloat(string(sm[2]), 64)
			if err != nil {
				continue
			}
			if strings.EqualFold(string(sm[1]), "X") {
				x = v
			} else {
				y = v
			}
		}
		switch {
		case x > 0 && y > 0:
			d.scale = (x + y) / 2
		case x > 0:
			d.scale = x
		case y > 0:
			d.scale = y
		}
		if om := weaponTextureOffsetRE.FindSubmatch(body); om != nil {
			ox, errX := strconv.Atoi(string(om[1]))
			oy, errY := strconv.Atoi(string(om[2]))
			if errX == nil && errY == nil {
				d.offX, d.offY, d.hasOffset = ox, oy, true
			}
		}
		out[name] = d
	}
	return out
}

func addWeaponPack(pk3Path string, out map[string]weaponFrame) {
	zr, err := zip.OpenReader(pk3Path)
	if err != nil {
		log.Printf("assets: open %s: %v", pk3Path, err)
		return
	}
	defer zr.Close()

	// TEXTURES first (order-independent inside the zip, but the defs map
	// must be complete before any PNG below consults it).
	defs := map[string]weaponTextureDef{}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || !strings.EqualFold(path.Base(f.Name), "TEXTURES") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			log.Printf("assets: %s: open %s: %v", filepath.Base(pk3Path), f.Name, err)
			continue
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			log.Printf("assets: %s: read %s: %v", filepath.Base(pk3Path), f.Name, err)
			continue
		}
		for name, d := range weaponTextureDefs(data) {
			defs[name] = d
		}
	}

	for _, f := range zr.File {
		if f.FileInfo().IsDir() || !strings.EqualFold(path.Ext(f.Name), ".png") {
			continue
		}
		name := strings.ToUpper(strings.TrimSuffix(path.Base(f.Name), path.Ext(f.Name)))
		rc, err := f.Open()
		if err != nil {
			log.Printf("assets: %s: open %s: %v", filepath.Base(pk3Path), f.Name, err)
			continue
		}
		img, err := png.Decode(rc)
		rc.Close()
		if err != nil {
			log.Printf("assets: %s: decode %s: %v", filepath.Base(pk3Path), f.Name, err)
			continue
		}
		if _, dup := out[name]; dup {
			log.Printf("assets: HD weapon frame %q in %s shadows an earlier pack", name, filepath.Base(pk3Path))
		}
		d := defs[name]
		out[name] = weaponFrame{img: img, scale: d.scale, offX: d.offX, offY: d.offY, hasOffset: d.hasOffset}
	}
}
