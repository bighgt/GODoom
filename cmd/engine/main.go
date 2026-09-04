// Command engine is TwoPointFive's entry point: it asks for a WAD file,
// parses it, builds the first level's BSP tree, opens a window, stands up
// the Vulkan renderer and the CPU software rasterizer, positions a camera
// at the level's player start, and runs the main loop. See game_design.txt
// for the full engine design and roadmap.
package main

import (
	"bufio"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"twopointfive/assets"
	"twopointfive/audio"
	"twopointfive/bsp"
	"twopointfive/config"
	"twopointfive/engine"
	"twopointfive/raster"
	"twopointfive/render/vulkan"
	"twopointfive/render/worldgeo"
	"twopointfive/wad"
	"twopointfive/window"
)

func init() {
	// GLFW's window/event functions must be called from the thread that
	// called glfw.Init() — on Windows that has to be the OS thread the Go
	// runtime started on, so pin this goroutine to it before main runs.
	runtime.LockOSThread()
}

func main() {
	err := run()
	if err != nil {
		log.Printf("engine: %v", err)
	}
	// Keep the debug console readable after the window is gone (see
	// holdConsoleOpen) before letting the process — and, if we own it, the
	// console — exit.
	holdConsoleOpen(err != nil)
	if err != nil {
		os.Exit(1)
	}
}

func run() error {
	path, err := window.PickWADFile()
	if err != nil {
		if errors.Is(err, window.ErrPickCancelled) {
			log.Println("engine: no WAD file selected, exiting")
			return nil
		}
		return err
	}
	log.Printf("engine: loading %s", path)

	cfg := config.Load()

	picked, err := wad.Load(path)
	if err != nil {
		return err
	}
	log.Printf("engine: loaded %s WAD, %d lumps", picked.Header.Magic, picked.Header.NumLumps)

	// A PWAD (or any WAD missing the core palette) can't stand on its own —
	// it patches an IWAD. Find that IWAD and merge, load order base-then-
	// patch, exactly like Doom's `-file`.
	w := picked
	if picked.Header.Magic == "PWAD" || !hasLump(picked, "PLAYPAL") {
		iwad, iwadPath, err := resolveIWAD(path, picked, cfg.IWAD)
		if err != nil {
			return err
		}
		log.Printf("engine: %s is a patch WAD — merging base IWAD %s", filepath.Base(path), iwadPath)
		w = wad.Merge(iwad, picked)
	}

	maps := w.ListMaps()
	if len(maps) == 0 {
		return fmt.Errorf("engine: %s contains no map lumps (THINGS/LINEDEFS/... under a map marker)", path)
	}
	mapName := chooseStartMap(maps, cfg.Map)
	log.Printf("engine: found %d map(s), loading %s", len(maps), mapName)

	level, err := w.LoadLevel(mapName)
	if err != nil {
		return err
	}
	log.Printf("engine: %s: %d vertexes, %d linedefs, %d sidedefs, %d sectors, %d things, %d segs, %d subsectors, %d nodes",
		mapName, len(level.Vertexes), len(level.Linedefs), len(level.Sidedefs), len(level.Sectors),
		len(level.Things), len(level.Segs), len(level.Subsectors), len(level.Nodes))

	tree, err := bsp.Build(level)
	if err != nil {
		return err
	}
	innerCount, leafCount := bsp.CountNodesAndLeaves(tree)
	log.Printf("engine: BSP tree built: %d inner nodes, %d leaves (subsectors)", innerCount, leafCount)

	textures, err := assets.New(w)
	if err != nil {
		return err
	}
	textures.SetQuake1Textures(cfg.Quake1Textures)

	wm := "fullscreen"
	if cfg.Windowed {
		wm = fmt.Sprintf("windowed %dx%d", cfg.WindowWidth, cfg.WindowHeight)
	}
	runMode := "hold-Shift-to-run"
	if cfg.AlwaysRun {
		runMode = "always-run"
	}
	log.Printf("engine: config in effect — %s, renderer=%s, resolution=%s x%.2f, fov=%.0f, vsync=%v, hudScale=%.2f, weaponScale=%.2f, lighting=%s lightScale=%.2f exposure=%.2f shadows=%v groundShadows=%v, textureQuality=%s, "+
		"invertMouseY=%v, mouseSensitivity=%.4f, turnSpeed=%.2f, %s, skill=%d, damageScale=%d, music=%v, weaponVolume=%.2f, worldVolume=%.2f, debugInfo=%v, hud=%s, crosshair=%s/%s, voxels=%v x%.2f, quake1Textures=%v",
		wm, cfg.Renderer, cfg.Resolution, cfg.RenderScale, cfg.FOVDegrees, cfg.VSync, cfg.HUDScale, cfg.WeaponScale, cfg.LightingMode, cfg.LightScale, cfg.Exposure, cfg.Shadows, cfg.GroundShadows, cfg.TextureQuality,
		cfg.InvertMouseY, cfg.MouseSensitivity, cfg.TurnSpeed, runMode, cfg.Skill, cfg.DamageScale, cfg.Music, cfg.WeaponVolume, cfg.WorldVolume, cfg.ShowDebugInfo, cfg.HUDStyle, cfg.Crosshair, cfg.CrosshairSize,
		cfg.Voxels, cfg.VoxelScale, cfg.Quake1Textures)

	cam := startCamera(level, tree)
	cam.FOVDeg = cfg.FOVDegrees
	log.Printf("engine: player start at (%.0f, %.0f, %.0f), facing %.0f°", cam.X, cam.Y, cam.Z, cam.Angle*180/math.Pi)

	win, err := window.New(cfg.WindowWidth, cfg.WindowHeight, "TwoPointFive — "+mapName, !cfg.Windowed)
	if err != nil {
		return err
	}
	defer win.Destroy()
	win.EnableMouseLook()

	fbW, fbH := win.FramebufferSize()
	if cfg.Windowed && (fbW <= 0 || fbH <= 0) {
		// Some platforms don't report a framebuffer size until the window is
		// mapped; the requested window size is the right answer here anyway.
		fbW, fbH = cfg.WindowWidth, cfg.WindowHeight
	}
	renderW, renderH := cfg.RenderSize(cfg.Windowed, fbW, fbH)
	base := "resolution=" + cfg.Resolution
	if cfg.Windowed {
		base = "window size"
	}
	mpix := float64(renderW*renderH) / 1e6
	log.Printf("engine: window framebuffer %dx%d, CPU render target %dx%d (%.1f Mpix, %s x%.2f)",
		fbW, fbH, renderW, renderH, mpix, base, cfg.RenderScale)
	// The software rasterizer's cost is close to linear in render pixels
	// (section 18 of game_design.txt). Flag a heavy target so a low frame
	// rate is self-explanatory.
	if mpix > 2.4 {
		lever := "lower `resolution` or set `renderScale` below 1.0"
		if cfg.Windowed {
			lever = "use a smaller window or set `renderScale` below 1.0"
		}
		log.Printf("engine: that's a large render target for a CPU rasterizer — if the frame rate is low, %s", lever)
	}

	lit := cfg.LightingMode == config.LightingEnhanced
	hw := cfg.Renderer == config.RendererHardware
	if hw {
		// The hardware (GPU-geometry) path is its own renderer — it walks the
		// BSP into a triangle list and the GPU shades it. Enhanced lighting is
		// a property of the software pipeline and doesn't apply.
		lit = false
	}

	ras := raster.New(textures, renderW, renderH)
	raster.SetLightScale(cfg.LightScale)
	raster.SetAmbientLight(cfg.AmbientLight)
	ras.SetHUDScale(cfg.HUDScale)
	ras.SetWeaponScale(cfg.WeaponScale)
	ras.SetTextureQuality(cfg.TextureQuality)
	if cfg.TextureQuality != config.TextureNearest {
		log.Printf("engine: textureQuality=%s — filtered wall/flat sampling is CPU-heavy on the software rasterizer; set it to \"nearest\" if the frame rate suffers", cfg.TextureQuality)
		ras.WarmTextureCache(level) // build mip chains now, not mid-frame on first sight of a room
	}
	ras.SetGroundShadows(cfg.GroundShadows)
	if lit {
		ras.SetGBuffer(true)
	}

	// Vulkan validation layers cost double-digit percent and add per-call
	// latency; keep them off unless explicitly asked for (TPF_VK_DEBUG=1).
	vkDebug := os.Getenv("TPF_VK_DEBUG") != ""
	var renderer *vulkan.Renderer
	if hw {
		renderer, err = vulkan.NewWorld(win, renderW, renderH, cfg.VSync, vkDebug)
		if err != nil {
			// The GPU-geometry backend failed to stand up; don't make the game
			// unlaunchable over it — fall back to the software rasterizer. Make
			// this loud: it's the usual reason "hardware mode looks like vanilla
			// software with no dynamic lights".
			log.Printf("engine: ============================================================")
			log.Printf("engine: renderer=hardware FAILED to initialise: %v", err)
			log.Printf("engine: falling back to the SOFTWARE rasterizer (vanilla — no")
			log.Printf("engine: dynamic lights). Fix the error above to get the GPU path.")
			log.Printf("engine: ============================================================")
			hw = false
			renderer, err = vulkan.New(win, renderW, renderH, cfg.VSync, vkDebug, false)
		} else {
			log.Printf("engine: renderer=hardware active (GPU geometry + HDR/bloom/dynamic lights)")
		}
	} else {
		renderer, err = vulkan.New(win, renderW, renderH, cfg.VSync, vkDebug, lit)
		if err != nil && lit {
			// The enhanced lighting pipeline failed to stand up (an unsupported
			// format, an out-of-memory, a driver quirk). Don't make the game
			// unlaunchable over it — fall back to the vanilla path and say so.
			log.Printf("engine: enhanced lighting unavailable (%v) — falling back to lightingMode=vanilla", err)
			lit = false
			ras.SetGBuffer(false)
			renderer, err = vulkan.New(win, renderW, renderH, cfg.VSync, vkDebug, false)
		}
	}
	if err != nil {
		return err
	}
	defer renderer.Destroy()
	log.Printf("engine: rendering on %s (%s)", renderer.DeviceName, renderer.DeviceTypeName)
	if renderer.DeviceIsSoftware {
		log.Println("engine: WARNING — Vulkan selected a CPU/software device; this is not real hardware rendering")
	}

	game := engine.NewGame(win, renderer, ras, w, level, tree, cam, cfg)
	game.LitMode = lit // may have been forced off by the fallback above
	if hw {
		game.HardwareMode = true
		game.WorldBuilder = worldgeo.New(level, tree, textures)
		game.ApplyMapInfo() // push the starting level's UMAPINFO sky to the fresh WorldBuilder
		// HDR tonemap exposure (config) + a fixed bloom strength for the
		// glow around dynamic lights / bright texels. The mip-pyramid bloom
		// accumulates every scale, so it preserves more energy than a tight
		// single blur — this is dialled back so normally-lit level geometry
		// isn't washed, only genuine highlights glow.
		renderer.SetHDR(float32(cfg.Exposure), 0.55)
	}

	if cfg.Voxels {
		pk3 := "Doom1_Voxel.pk3"
		if strings.HasPrefix(mapName, "MAP") {
			pk3 = "Doom2_Voxel.pk3"
		}
		if vs, err := assets.LoadVoxelPackDir(pk3); err != nil {
			log.Printf("engine: voxel models unavailable (%v) — using sprites", err)
		} else {
			game.Voxels = vs
			ras.SetVoxelScale(cfg.VoxelScale)
			log.Printf("engine: voxel models active — %s, %d sprite frames", pk3, vs.Frames())
			if hw {
				// Mesh + upload every model up front so no first-sight GPU
				// stall hits the render loop mid-game.
				renderer.WarmVoxels(vs.Models())
			}
		}
	}

	if dev, err := audio.NewDevice(); err != nil {
		log.Printf("engine: sound effects unavailable: %v", err)
	} else {
		game.AudioDevice = dev
	}

	if !cfg.Music {
		log.Println("engine: music disabled (config.json)")
	} else if music, err := audio.NewMusicPlayer(); err != nil {
		log.Printf("engine: music unavailable: %v", err)
	} else {
		defer music.Stop()
		playLevelMusic(w, music, game.MapMusicLump(mapName), mapName)
		// Restart music when the engine advances to the next level, and when
		// the idmus cheat asks for a specific map's track.
		game.OnLevelChange = func(m string) { playLevelMusic(w, music, game.MapMusicLump(m), m) }
		game.OnMusicChange = func(m string) { playLevelMusic(w, music, game.MapMusicLump(m), m) }
	}

	log.Println("engine: entering main loop (WASD/mouse to move+look, Space to jump, E to use doors/switches, 1-7 weapons, left click to fire, F1 cycles HUD, Esc to quit)")
	return game.Run()
}

// playLevelMusic finds the level's music lump, decodes it, and starts it
// looping. A UMAPINFO music lump (override) is tried first; then the
// conventional name for the map's format (wad.MusicLumpName — "D_E1M1" for
// ExMy, "D_RUNNIN"-style for Doom II MAPxx), then "D_"+mapname as a fallback
// (some PWADs name Doom II music that way), then it gives up quietly.
func playLevelMusic(w *wad.WAD, music *audio.MusicPlayer, override, mapName string) {
	candidates := []string{override, wad.MusicLumpName(mapName), "D_" + mapName}
	for _, lumpName := range candidates {
		raw, ok := w.Find(lumpName)
		if !ok {
			continue
		}
		events, err := wad.DecodeMus(raw)
		if err != nil {
			log.Printf("engine: decode music %q: %v", lumpName, err)
			return
		}
		log.Printf("engine: playing music %s (%d events)", lumpName, len(events))
		music.Play(events)
		return
	}
	log.Printf("engine: no music lump found for %s (tried %v)", mapName, candidates)
}

// pickStartMap prefers the conventional first level of whichever format the
// WAD uses (E1M1 for Doom/Ultimate Doom, MAP01 for Doom II and most PWADs),
// falling back to whatever map was listed first.
func pickStartMap(maps []string) string {
	for _, want := range []string{"E1M1", "MAP01"} {
		for _, m := range maps {
			if m == want {
				return want
			}
		}
	}
	return maps[0]
}

// mapPickerDialog is the GUI list dialog for the WAD's maps (window.PickMap);
// a package var so tests can stub it.
var mapPickerDialog = window.PickMap

// chooseStartMap decides which map to load:
//   - the config "map" key wins if it names one of the WAD's maps;
//   - a single-map WAD just loads it;
//   - launched from a terminal, it prints a numbered menu and reads a
//     choice (a number, a map name, or blank for the conventional start);
//   - launched with no console (double-click, desktop launcher, IDE) it
//     pops a graphical list of the maps (window.PickMap);
//   - queued stdin input (`printf MAP07 | engine`, test harnesses) is
//     honoured even when stdin is not a tty.
//
// Anything unresolved — no dialog available, a cancel, EOF, repeated bad
// input — falls through to pickStartMap.
func chooseStartMap(maps []string, cfgMap string) string {
	def := pickStartMap(maps)

	if cfgMap != "" {
		for _, m := range maps {
			if strings.EqualFold(m, cfgMap) {
				log.Printf("engine: starting on map %s (config)", m)
				return m
			}
		}
		log.Printf("engine: config map=%q is not in this WAD (has: %s) — ignoring", cfgMap, strings.Join(maps, " "))
	}

	if len(maps) < 2 {
		return def
	}

	if !window.StdinInteractive() {
		// No interactive console. If something is queued on stdin (scripts,
		// `printf ... | engine`, `go test`), take that; otherwise pop the
		// graphical list.
		if sc := bufio.NewScanner(os.Stdin); sc.Scan() {
			if m, msg := resolveMapChoice(sc.Text(), maps, def); msg == "" {
				return m
			}
			return def
		}
		if m, err := mapPickerDialog(maps, def); err == nil && m != "" {
			log.Printf("engine: map %s chosen from the dialog", m)
			return m
		} else if err != nil && err != window.ErrPickMapUnavailable {
			log.Printf("engine: map dialog: %v — using %s", err, def)
		}
		return def
	}

	fmt.Fprintf(os.Stderr, "\nThis WAD has %d maps. Type a number or a name, or press Enter for %s:\n", len(maps), def)
	const perRow = 6
	for i, m := range maps {
		fmt.Fprintf(os.Stderr, "  %3d %-9s", i+1, m)
		if (i+1)%perRow == 0 {
			fmt.Fprintln(os.Stderr)
		}
	}
	if len(maps)%perRow != 0 {
		fmt.Fprintln(os.Stderr)
	}

	in := bufio.NewScanner(os.Stdin)
	for tries := 0; tries < 5; tries++ {
		fmt.Fprint(os.Stderr, "map> ")
		if !in.Scan() {
			break // EOF
		}
		if m, msg := resolveMapChoice(in.Text(), maps, def); msg == "" {
			return m
		} else {
			fmt.Fprintf(os.Stderr, "  %s\n", msg)
		}
	}
	fmt.Fprintf(os.Stderr, "  using %s\n", def)
	return def
}

// resolveMapChoice interprets one line of menu input: blank -> def; a
// 1-based number -> that entry; otherwise a case-insensitive map name. On
// success it returns the resolved map and an empty message; on bad input it
// returns "" and a message to show the user before re-prompting.
func resolveMapChoice(line string, maps []string, def string) (chosen, msg string) {
	s := strings.TrimSpace(line)
	if s == "" {
		return def, ""
	}
	if n, err := strconv.Atoi(s); err == nil {
		if n >= 1 && n <= len(maps) {
			return maps[n-1], ""
		}
		return "", fmt.Sprintf("enter 1..%d", len(maps))
	}
	for _, m := range maps {
		if strings.EqualFold(m, s) {
			return m, ""
		}
	}
	return "", fmt.Sprintf("%q is not one of the maps", s)
}

// startCamera builds the initial raster.Camera from the level's Player 1
// start Thing (falling back to the map origin, facing east, if none is
// present), with its eye height resolved from whichever Sector the BSP tree
// says that point is actually in.
func startCamera(level *wad.Level, tree bsp.Node) raster.Camera {
	const player1Start = 1

	x, y, angleDeg := 0.0, 0.0, 0.0
	for _, t := range level.Things {
		if t.Type == player1Start {
			x, y, angleDeg = float64(t.X), float64(t.Y), float64(t.Angle)
			break
		}
	}

	z := engine.EyeHeight
	if sec := bsp.PointSector(tree, level, float32(x), float32(y)); sec != nil {
		z = float64(sec.FloorHeight) + engine.EyeHeight
	}

	return raster.Camera{X: x, Y: y, Z: z, Angle: angleDeg * math.Pi / 180, FOVDeg: 90}
}

// hasLump reports whether w contains a lump of the given name.
func hasLump(w *wad.WAD, name string) bool {
	return w.IndexOf(name) >= 0
}

// iwadNames lists the base IWAD filenames to look for, by map format, most
// common first. All Doom II-format IWADs (Doom II, Final Doom, Freedoom
// Phase 2) share the MAPxx lump set, so any of them serves a Doom II PWAD.
var iwadNames = map[string][]string{
	"doom2": {"DOOM2.WAD", "doom2.wad", "Doom2.wad", "TNT.WAD", "tnt.wad",
		"PLUTONIA.WAD", "plutonia.wad", "freedoom2.wad", "FREEDOOM2.WAD"},
	"doom1": {"DOOM.WAD", "doom.wad", "DOOMU.WAD", "doomu.wad",
		"DOOM1.WAD", "doom1.wad", "freedoom1.wad", "FREEDOOM1.WAD"},
}

// resolveIWAD finds the base IWAD a patch WAD needs. An explicit path (from
// config.json's "iwad") wins; otherwise it works out the map format the
// PWAD uses and hunts for a matching IWAD next to the PWAD, up its parent
// directories, and beside the executable / working directory (also checking
// wad/ and testdata/ subdirs). The returned WAD is a real IWAD with a
// palette.
func resolveIWAD(pickedPath string, picked *wad.WAD, explicit string) (*wad.WAD, string, error) {
	usable := func(p string) (*wad.WAD, bool) {
		iw, err := wad.Load(p)
		if err != nil || iw.Header.Magic != "IWAD" || !hasLump(iw, "PLAYPAL") {
			return nil, false
		}
		return iw, true
	}

	if explicit != "" {
		if iw, ok := usable(explicit); ok {
			return iw, explicit, nil
		}
		return nil, "", fmt.Errorf("engine: config iwad %q is not a usable IWAD (missing, unreadable, or no PLAYPAL)", explicit)
	}

	format := "doom2"
	for _, m := range picked.ListMaps() {
		if len(m) > 0 && m[0] == 'E' {
			format = "doom1"
			break
		}
	}

	// Base directories to search, in priority order.
	var dirs []string
	seenDir := map[string]bool{}
	addDir := func(d string) {
		if d == "" {
			return
		}
		for _, sub := range []string{"", "wad", "wads", "IWADs", "iwads", "testdata"} {
			p := filepath.Join(d, sub)
			if !seenDir[p] {
				seenDir[p] = true
				dirs = append(dirs, p)
			}
		}
	}
	dir := filepath.Dir(pickedPath)
	for i := 0; i < 5 && dir != "" && dir != string(filepath.Separator); i++ {
		addDir(dir)
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	if wd, err := os.Getwd(); err == nil {
		addDir(wd)
	}
	if exe, err := os.Executable(); err == nil {
		addDir(filepath.Dir(exe))
	}

	// Try the format's IWADs first, then the other format's (a WAD may
	// mis-detect, and a Doom II IWAD still opens a Doom II PWAD).
	tryOrder := []string{format}
	if format == "doom2" {
		tryOrder = append(tryOrder, "doom1")
	} else {
		tryOrder = append(tryOrder, "doom2")
	}
	for _, fmtKey := range tryOrder {
		for _, name := range iwadNames[fmtKey] {
			for _, d := range dirs {
				p := filepath.Join(d, name)
				if _, err := os.Stat(p); err != nil {
					continue
				}
				if iw, ok := usable(p); ok {
					return iw, p, nil
				}
			}
		}
	}

	want := "DOOM2.WAD (or TNT.WAD / PLUTONIA.WAD / freedoom2.wad)"
	if format == "doom1" {
		want = "DOOM.WAD (or DOOM1.WAD / freedoom1.wad)"
	}
	return nil, "", fmt.Errorf("engine: %s is a patch WAD and needs its base IWAD — put %s next to it, or set \"iwad\": \"C:\\path\\to\\%s\" in config.json",
		filepath.Base(pickedPath), want, strings.SplitN(want, " ", 2)[0])
}
