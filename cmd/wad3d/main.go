// Command wad3d is the real-3D WAD converter: point it at a standard Doom,
// Doom II or Heretic WAD (IWAD or PWAD) and it writes a TwoPointFive
// "real 3D" WAD — staircases rebuilt as continuous ramps, the hi-res
// textures the maps use packed inside, all three games' weapons (Doom, Doom
// II's Super Shotgun, Heretic) embedded, and a TPF3D marker so the engine
// and launcher know what it is. See package twopointfive/wad3d and
// game_design.txt section 26.
//
// Pointing it at a WAD:
//
//	wad3d                         # opens a file picker
//	wad3d path/to/doom2.wad       # or drag a .wad onto the program
//	wad3d -in heretic.wad -maps E1M1,E1M2 -max 1024
//
// Where it's saved: run without flags (double-clicked, file picker, or a
// WAD dragged onto it) it asks with a Save As dialog, starting at
// <name>_3d.wad in the wad/ folder the launcher lists; -out FILE or -out
// FOLDER names it on the command line; -choose-out forces the dialog. A
// scripted run with flags but no -out writes that default silently. It's a
// PWAD: the engine still merges it over the base IWAD recorded inside it,
// so it can be saved anywhere.
package main

import (
	"bufio"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"twopointfive/assets"
	"twopointfive/wad"
	"twopointfive/wad3d"
	"twopointfive/window"
)

func main() {
	log.SetFlags(0)
	in := flag.String("in", "", "source WAD (IWAD or PWAD); may also be given as the first argument; none = file picker")
	out := flag.String("out", "", "output WAD, or a folder to write <name>_3d.wad into (default: ask with a Save As dialog when run without flags, else <name>_3d.wad in the wad/ folder, else next to the source)")
	chooseOut := flag.Bool("choose-out", false, "always ask where to save with a Save As dialog (starting from -out, if given)")
	iwad := flag.String("iwad", "", "base IWAD for a PWAD source (default: auto-search, like the engine)")
	doom2 := flag.String("doom2", "", "Doom II-family IWAD to take the Doom/Doom II weapons from (default: auto-search)")
	heretic := flag.String("heretic", "", "Heretic IWAD to take the Heretic weapons from (default: auto-search)")
	passes := flag.String("passes", strings.Join(wad3d.DefaultPasses, ","), "comma-separated passes to run (known: "+strings.Join(wad3d.KnownPasses, ", ")+"; empty = none)")
	maps := flag.String("maps", "", "comma-separated maps to convert (default: all)")
	textures := flag.Bool("textures", true, "embed the hi-res texture set")
	allTex := flag.Bool("alltex", false, "embed every texture the WAD defines, not only those the maps use (much larger)")
	maxTex := flag.Int("max", 1024, "downscale embedded textures larger than this many pixels (0 = native size — the Heretic HD pack is up to 2048px, ~4x the WAD size)")
	minSteps := flag.Int("minsteps", 3, "shortest staircase (in steps) the stairs pass converts")
	weapons := flag.Bool("weapons", true, "embed the Doom, Doom II and Heretic weapons (needs both IWADs)")
	allowMissing := flag.Bool("allow-missing-weapons", false, "write the WAD even if some weapon art/sounds weren't found (they draw/sound blank)")
	flag.Parse()

	picked := false
	if *in == "" && flag.NArg() > 0 {
		*in = flag.Arg(0)
	}
	if *in == "" {
		fmt.Println("wad3d: choose a WAD to convert...")
		p, err := window.PickWADFile()
		if errors.Is(err, window.ErrPickCancelled) {
			fmt.Println("wad3d: cancelled")
			return
		}
		if err != nil {
			fail(false, err)
		}
		*in, picked = p, true
	}
	// Launched without arguments (double-clicked / file picker): keep the
	// console open at the end so the summary can be read.
	interactive := picked || flag.NFlag() == 0

	// Where to save: asked up front, before the (long) conversion, whenever
	// the program is used interactively without an -out, or -choose-out is
	// given. Scripted runs (any flag, no -out) keep the silent default.
	if (*out == "" && interactive) || *chooseOut {
		def := resolveOut(*in, *out)
		p, err := window.PickSaveWADFile("Save the converted 3D WAD as", def)
		switch {
		case errors.Is(err, window.ErrPickCancelled):
			fmt.Println("wad3d: cancelled — nothing written")
			pause(interactive)
			return
		case err != nil:
			log.Printf("wad3d: no save dialog (%v) — saving to %s", err, def)
			*out = def
		default:
			*out = p
		}
	}

	if err := run(*in, *out, *iwad, *doom2, *heretic, *passes, *maps, *textures, *allTex, *maxTex, *minSteps, *weapons, *allowMissing); err != nil {
		fail(interactive, err)
	}
	pause(interactive)
}

func run(in, out, iwadPath, doom2Path, hereticPath, passes, maps string, textures, allTex bool, maxTex, minSteps int, weapons, allowMissing bool) error {
	start := time.Now()
	raw, err := os.ReadFile(in)
	if err != nil {
		return err
	}
	sum := sha1.Sum(raw)
	pickedWAD, err := wad.Parse(raw)
	if err != nil {
		return fmt.Errorf("%s: %w", in, err)
	}
	res, err := wad.LoadWithIWAD(in, iwadPath)
	if err != nil {
		return err
	}
	game := "Doom"
	if res.WAD.IsHeretic() {
		game = "Heretic"
	}
	if res.IWADPath != "" {
		log.Printf("source: %s (%s PWAD, %d lumps) over IWAD %s", in, game, len(pickedWAD.Entries), res.IWADPath)
	} else {
		log.Printf("source: %s (%s %s, %d lumps)", in, game, pickedWAD.Header.Magic, len(pickedWAD.Entries))
	}

	out = resolveOut(in, out)
	if a, b := absPath(in), absPath(out); a == b {
		return fmt.Errorf("output %s would overwrite the source — pass -out", out)
	}

	src := wad3d.Source{Name: filepath.Base(in), SHA1: hex.EncodeToString(sum[:]), Picked: pickedWAD, Merged: res.WAD}
	// The base IWAD the output is merged over at load time: the source
	// itself for an IWAD, else the one LoadWithIWAD just found for it.
	if res.IWADPath == "" {
		src.IWADPath, src.IWADSHA1 = absPath(in), src.SHA1
	} else {
		ib, err := os.ReadFile(res.IWADPath)
		if err != nil {
			return err
		}
		isum := sha1.Sum(ib)
		src.IWADPath, src.IWADSHA1 = absPath(res.IWADPath), hex.EncodeToString(isum[:])
	}
	log.Printf("base IWAD (recorded in the output): %s", src.IWADPath)
	if weapons {
		ws, err := wad3d.FindWeaponIWADs(in, pickedWAD, doom2Path, hereticPath)
		if err != nil {
			return err
		}
		src.Weapons = ws
	}

	opt := wad3d.Options{
		Passes:      splitList(passes),
		Maps:        splitList(maps),
		Textures:    textures,
		AllTextures: allTex,
		MaxTex:      maxTex,
		Weapons:     weapons,
		Stairs:      wad3d.StairOptions{MinSteps: minSteps},
	}
	if opt.Passes == nil {
		opt.Passes = []string{} // an explicit empty list: no passes, not the default set
	}
	log.Printf("converting...")
	lumps, rep, err := wad3d.Convert(src, opt)
	if err != nil {
		return err
	}
	report(rep, textures, weapons)
	if weapons && len(rep.MissingWeapons) > 0 && !allowMissing {
		return fmt.Errorf("some weapon assets weren't found, so those guns would be invisible or silent in this WAD. " +
			"Doom's first-person weapons come from the HD weapon pack in assets/weapons/*.pk3 — run wad3d from the " +
			"repo (or from bin/, which finds it) — and the rest from the Doom II and Heretic IWADs (-doom2, -heretic). " +
			"Nothing was written; pass -allow-missing-weapons to write it anyway")
	}

	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	if err := wad.WritePWAD(out, lumps); err != nil {
		return err
	}
	size := int64(0)
	if st, err := os.Stat(out); err == nil {
		size = st.Size()
	}
	fmt.Printf("\nwrote %s (%d lumps, %.1f MB) in %.1fs\n", out, len(lumps), float64(size)/(1<<20), time.Since(start).Seconds())
	if dir, ok := assets.FindDir("wad"); !ok || absPath(dir) != absPath(filepath.Dir(out)) {
		fmt.Println("(saved outside the launcher's wad/ folder — open it with the launcher's Browse... button)")
	}
	return nil
}

func report(rep wad3d.Report, textures, weapons bool) {
	runs, ramps := 0, 0
	for _, m := range rep.Maps {
		runs += m.StairRuns
		ramps += m.RampSectors
		if m.StairRuns > 0 {
			log.Printf("  %-6s %2d stair run(s) -> ramps (%d sectors, steepest %.0f%%)", m.Name, m.StairRuns, m.RampSectors, m.MaxSlope*100)
		} else {
			log.Printf("  %-6s no change", m.Name)
		}
	}
	log.Printf("maps: %d converted — %d stair run(s), %d ramp sector(s)", len(rep.Maps), runs, ramps)
	if textures {
		if rep.TextureSet == "" {
			log.Printf("textures: none embedded (no hi-res set found under assets/textures/)")
		} else {
			log.Printf("textures: %d wall + %d flat from the %s set, %.1f MB (%d used name(s) have no hi-res file)",
				rep.Walls, rep.Flats, rep.TextureSet, float64(rep.TextureBytes)/(1<<20), rep.MissingTextures)
		}
	}
	log.Printf("voxels: %s — %d sprite frames, %.1f MB (map objects are drawn voxel-only)",
		strings.Join(rep.VoxelPacks, " + "), rep.VoxelSprites, float64(rep.VoxelBytes)/(1<<20))
	if len(rep.UnmodelledTypes) > 0 {
		log.Printf("voxels: WARNING — no voxel model for %d object type(s); they will be INVISIBLE in play: %s",
			len(rep.UnmodelledTypes), strings.Join(rep.UnmodelledTypes, ", "))
	}
	if len(rep.ModelPacks) > 0 {
		log.Printf("3D models: %s — %d sprite frame(s), %d file(s), %.1f MB (drawn instead of voxels where present)",
			strings.Join(rep.ModelPacks, " + "), rep.ModelFrames, rep.ModelFiles, float64(rep.ModelBytes)/(1<<20))
		for _, e := range rep.ModelErrors {
			log.Printf("3D models: skipped — %v", e)
		}
	} else {
		log.Printf("3D models: none (no model pack in assets/models/ or assets/import/, or a Heretic WAD)")
	}
	if weapons {
		log.Printf("weapons: %s — %d sprite(s), %d sound(s), %.1f MB", strings.Join(rep.WeaponGames, " + "),
			rep.WeaponSprites, rep.WeaponSounds, float64(rep.WeaponBytes)/(1<<20))
		if len(rep.MissingWeapons) > 0 {
			log.Printf("weapons: not found in any source (these draw/sound blank): %s", strings.Join(rep.MissingWeapons, " "))
		}
	}
}

// defaultOutput is <name>_3d.wad in the launcher's wad/ folder, else next
// to the source.
func defaultOutput(in string) string {
	base := strings.TrimSuffix(filepath.Base(in), filepath.Ext(in)) + "_3d.wad"
	if dir, ok := assets.FindDir("wad"); ok {
		return filepath.Join(dir, base)
	}
	return filepath.Join(filepath.Dir(in), base)
}

// resolveOut turns the -out value into the file to write: "" is
// defaultOutput, an existing folder gets <name>_3d.wad inside it, and a
// name without an extension gets ".wad".
func resolveOut(in, out string) string {
	if out == "" {
		return defaultOutput(in)
	}
	if st, err := os.Stat(out); err == nil && st.IsDir() {
		return filepath.Join(out, filepath.Base(defaultOutput(in)))
	}
	if filepath.Ext(out) == "" {
		out += ".wad"
	}
	return out
}

func absPath(p string) string {
	a, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return a
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func fail(interactive bool, err error) {
	fmt.Fprintf(os.Stderr, "wad3d: %v\n", err)
	pause(interactive)
	os.Exit(1)
}

// pause waits for Enter when the program was double-clicked / used the
// file picker, so its console window doesn't vanish with the result.
func pause(interactive bool) {
	if !interactive || !window.StdinInteractive() {
		return
	}
	fmt.Print("\nPress Enter to close...")
	bufio.NewReader(os.Stdin).ReadString('\n')
}
