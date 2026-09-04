package assets

import (
	"archive/zip"
	"image"
	"image/png"
	"log"
	"os"
	"path"
	"path/filepath"
	"sort"
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

// weaponPackTexelsPerUnit is the fallback sampling density for an HD weapon
// frame whose original the loaded WAD doesn't define (so there's no patch
// width to derive it from) — the observed native scale of these packs is a
// clean 2x over the vanilla psprite.
const weaponPackTexelsPerUnit = 2.0

// weaponOverrideRGBA turns one decoded HD weapon PNG into an RGBA at the
// right sampling density and hotspot. When the WAD defines the original
// psprite, density is the exact PNG/original width ratio and the hotspot
// is the WAD patch's, scaled into the denser pixel space (matching the
// hi-res sprite path in textures.go). Without an original, it falls back
// to weaponPackTexelsPerUnit and a centred-bottom hotspot — DrawWeapon
// positions the gun by its own pixel size, so only the muzzle-flash-
// relative offset needs a plausible value.
func weaponOverrideRGBA(img image.Image, patch *wad.Patch) *RGBA {
	tpu := weaponPackTexelsPerUnit
	if patch != nil && patch.Width > 0 {
		if r := float64(img.Bounds().Dx()) / float64(patch.Width); r >= 0.5 && r <= 16 {
			tpu = r
		}
	}
	out := imageToRGBA(img, tpu)
	if patch != nil {
		out.OffsetX = int(float64(patch.LeftOffset) * tpu)
		out.OffsetY = int(float64(patch.TopOffset) * tpu)
	} else {
		out.OffsetX = out.Width / 2
		out.OffsetY = out.Height
	}
	return out
}

// loadWeaponPack finds weaponsDir (working directory first, then beside the
// exe — same as the skybox / hi-res set) and decodes every PNG inside every
// .pk3 there into a lump-name -> image map. The lump name is the file's
// stem, upper-cased (GZDoom HD packs lay them out as
// hires/sprites/<weapon>/<LUMP>0.png). Returns nil when the directory or a
// readable pack is absent — the game then just uses the WAD's own weapon
// sprites.
//
// Unlike the lazily-indexed hi-res texture set (hires.go), this pack is
// small (a few dozen frames, a few MB decoded) and every frame is drawn
// the instant its weapon is equipped, so it's decoded eagerly here rather
// than on first use.
func loadWeaponPack() map[string]image.Image {
	dir, ok := findAssetDir(weaponsDir)
	if !ok {
		log.Printf("assets: no HD weapon pack (looked for %s/*.pk3) — using the WAD's weapon sprites", weaponsDir)
		return nil
	}
	return loadWeaponPackDir(dir)
}

// loadWeaponPackDir is the testable core of loadWeaponPack: read every
// .pk3 / .zip in dir and merge their PNG frames.
func loadWeaponPackDir(dir string) map[string]image.Image {
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

	out := map[string]image.Image{}
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

func addWeaponPack(pk3Path string, out map[string]image.Image) {
	zr, err := zip.OpenReader(pk3Path)
	if err != nil {
		log.Printf("assets: open %s: %v", pk3Path, err)
		return
	}
	defer zr.Close()
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
		out[name] = img
	}
}
