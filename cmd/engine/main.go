// Command engine is TwoPointFive's entry point: it asks for a WAD file,
// parses it, builds the first level's BSP tree, opens a window, stands up
// the Vulkan hardware renderer (world geometry) and the CPU rasterizer
// (the 2D weapon/HUD/crosshair overlay only), positions a camera at the
// level's player start, and runs the main loop. See game_design.txt for
// the full engine design and roadmap.
package main

import (
	"bufio"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"twopointfive/assets"
	"twopointfive/audio"
	"twopointfive/bsp"
	"twopointfive/config"
	"twopointfive/engine"
	"twopointfive/launcher"
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
	// window.HoldConsoleOpen) before letting the process — and, if we own
	// it, the console — exit.
	window.HoldConsoleOpen(err != nil)
	if err != nil {
		os.Exit(1)
	}
}

// stageTimer logs how long one startup stage took. Picking a WAD and a map
// happens through native dialogs the OS keeps responsive on its own, but
// everything after that — parsing, BSP build, texture indexing, GPU/pipeline
// setup, static-geometry build, voxel meshing — is synchronous, one-time
// work with no progress bar of its own; a slow one of these is exactly what
// a "not responding" window-manager warning during load looks like from the
// outside. Logging each stage's duration turns "it hangs on load" into "it
// spent N seconds in this specific stage" for the next person (or profiling
// pass) to act on. Call the returned func when the stage finishes.
func stageTimer(name string) func() {
	start := time.Now()
	return func() { log.Printf("engine: [load] %s: %s", name, time.Since(start).Round(time.Millisecond)) }
}

// run is the process entry point's real body: either the headless/scripted
// single-shot path (TPF_WAD / a .wad command-line argument — see
// window.WADFromCommandLine) or the normal one, which loops the pre-game
// launcher (package launcher) and a play session back and forth — Quit in
// the launcher is the only way out; ending a game session (Escape, or the
// game window's close button — the only two ways engine.Game.Run returns
// today) returns to the launcher instead of exiting the process, so
// choosing another WAD/level or tweaking a setting doesn't mean relaunching
// the executable.
func run() error {
	if headless := window.WADFromCommandLine(); headless != "" {
		return runHeadless(headless)
	}

	// Sound effects and music are process-wide resources — see audio.Setup's
	// doc comment — stood up once here and handed to every play session.
	dev, music := audio.Setup("engine")
	if music != nil {
		defer music.Stop()
	}

	for {
		// A separate pre-game window for picking the WAD/level and editing
		// every config.json setting, shown before any of the engine's own
		// windows or rendering stand up. It already did the WAD load +
		// IWAD merge (needed to populate its own map list) and already
		// wrote config.json, so there's nothing left to do here but take
		// its result. config.Load() is re-read each time round so a
		// session that just saved new settings starts the next launcher
		// screen from them.
		res, ok, err := launcher.Run(config.Load())
		if err != nil {
			return err
		}
		if !ok {
			log.Println("engine: launcher cancelled, exiting")
			return nil
		}
		log.Printf("engine: loaded %s, playing %s", res.WADPath, res.MapName)

		if err := playSession(res.WADPath, res.MapName, res.Config, res.WAD, dev, music); err != nil {
			return err
		}
		log.Println("engine: game session ended — back to the launcher")
	}
}

// runHeadless is the TPF_WAD / .wad-argument escape hatch's whole flow —
// exactly what run() did before the launcher existed, kept as a single-shot
// path with no GUI beyond chooseStartMap's own console/native fallback, for
// scripted runs, cmd/engine's own tests, and ad-hoc diagnostics.
func runHeadless(path string) error {
	log.Printf("engine: loading %s", path)
	cfg := config.Load()

	// Loads path and, if it's a PWAD (or any WAD missing the core
	// palette), finds and merges its base IWAD — load order
	// IWAD-then-patch, exactly like Doom's `-file` (see wad.LoadWithIWAD).
	loaded, err := wad.LoadWithIWAD(path, cfg.IWAD)
	if err != nil {
		return err
	}
	log.Printf("engine: loaded %s WAD, %d lumps", loaded.PickedMagic, loaded.PickedLumps)
	if loaded.IWADPath != "" {
		log.Printf("engine: %s is a patch WAD — merging base IWAD %s", filepath.Base(path), loaded.IWADPath)
	}
	w := loaded.WAD

	maps := w.ListMaps()
	if len(maps) == 0 {
		return fmt.Errorf("engine: %s contains no map lumps (THINGS/LINEDEFS/... under a map marker)", path)
	}
	mapName := chooseStartMap(maps, cfg.Map)
	log.Printf("engine: found %d map(s), loading %s", len(maps), mapName)

	dev, music := audio.Setup("engine")
	if music != nil {
		defer music.Stop()
	}
	return playSession(path, mapName, cfg, w, dev, music)
}

// playSession loads mapName from w and runs one full game session — every
// window, GPU, and CPU-rasterizer resource it creates is torn down (its
// defers run) when this returns, whether that's a normal game-window close
// or an error. dev/music are the process-wide audio resources audio.Setup
// created; cfg.Music still gates whether THIS session actually starts a
// track (see audio.Setup's doc comment on why the player itself isn't
// recreated per session).
func playSession(path, mapName string, cfg config.Config, w *wad.WAD, dev *audio.Device, music *audio.MusicPlayer) error {
	// From here on the window (once created below) is up but the game isn't
	// running its event/render loop yet — see stageTimer's doc comment. Time
	// it all so a slow load is diagnosable instead of just looking hung.
	loadStart := time.Now()

	done := stageTimer("parse level lump")
	level, err := w.LoadLevel(mapName)
	if err != nil {
		return err
	}
	done()
	log.Printf("engine: %s: %d vertexes, %d linedefs, %d sidedefs, %d sectors, %d things, %d segs, %d subsectors, %d nodes",
		mapName, len(level.Vertexes), len(level.Linedefs), len(level.Sidedefs), len(level.Sectors),
		len(level.Things), len(level.Segs), len(level.Subsectors), len(level.Nodes))

	done = stageTimer("build BSP tree")
	tree, err := bsp.Build(level)
	if err != nil {
		return err
	}
	done()
	innerCount, leafCount := bsp.CountNodesAndLeaves(tree)
	log.Printf("engine: BSP tree built: %d inner nodes, %d leaves (subsectors)", innerCount, leafCount)

	done = stageTimer("index texture packs")
	textures, err := assets.New(w)
	if err != nil {
		return err
	}
	textures.SetQuake1Textures(cfg.Quake1Textures)
	done()

	// config's switchWeapons: play with the OTHER game's arsenal. The
	// weapon-table swap itself is in engine.NewGame (it reads cfg); here we
	// give the renderer the other IWAD for its weapon / projectile sprites
	// and the game the same for its fire sounds.
	var altWAD *wad.WAD
	if cfg.SwitchWeapons {
		if ow, op, ok := wad.OtherGameIWAD(path, w); ok {
			altWAD = ow
			textures.SetSpriteFallback(ow)
			which := "Heretic"
			if w.IsHeretic() {
				which = "Doom"
			}
			log.Printf("engine: switchWeapons — %s weapon art/sounds from %s", which, filepath.Base(op))
		} else {
			log.Printf("engine: switchWeapons on, but the other game's IWAD wasn't found next to %s — the arsenal still swaps, the new guns just draw/sound blank", filepath.Base(path))
		}
	}

	wm := "fullscreen"
	if cfg.Windowed {
		wm = fmt.Sprintf("windowed %dx%d", cfg.WindowWidth, cfg.WindowHeight)
	}
	runMode := "hold-Shift-to-run"
	if cfg.AlwaysRun {
		runMode = "always-run"
	}
	log.Printf("engine: config in effect — %s, resolution=%s x%.2f, fov=%.0f, vsync=%v, hudScale=%.2f, weaponScale=%.2f, lightScale=%.2f exposure=%.2f, "+
		"invertMouseY=%v, mouseSensitivity=%.4f, turnSpeed=%.2f, %s, skill=%d, damageScale=%d, music=%v, musicVolume=%.2f, weaponVolume=%.2f, worldVolume=%.2f, debugInfo=%v, hud=%s, crosshair=%s/%s, voxels=%v x%.2f, quake1Textures=%v",
		wm, cfg.Resolution, cfg.RenderScale, cfg.FOVDegrees, cfg.VSync, cfg.HUDScale, cfg.WeaponScale, cfg.LightScale, cfg.Exposure,
		cfg.InvertMouseY, cfg.MouseSensitivity, cfg.TurnSpeed, runMode, cfg.Skill, cfg.DamageScale, cfg.Music, cfg.MusicVolume, cfg.WeaponVolume, cfg.WorldVolume, cfg.ShowDebugInfo, cfg.HUDStyle, cfg.Crosshair, cfg.CrosshairSize,
		cfg.Voxels, cfg.VoxelScale, cfg.Quake1Textures)

	cam := engine.StartCamera(level, tree)
	cam.FOVDeg = cfg.FOVDegrees
	log.Printf("engine: player start at (%.0f, %.0f, %.0f), facing %.0f°", cam.X, cam.Y, cam.Z, cam.Angle*180/math.Pi)

	done = stageTimer("open window")
	win, err := window.New(cfg.WindowWidth, cfg.WindowHeight, "TwoPointFive — "+mapName, !cfg.Windowed)
	if err != nil {
		return err
	}
	defer win.Destroy()
	win.EnableMouseLook()
	done()
	// The window exists now but nothing pumps its event queue again until
	// game.Run's loop starts — every remaining stage below is a candidate
	// for the desktop's compositor to decide the window has hung (an
	// unanswered _NET_WM_PING) and offer to force-quit it, especially on a
	// slow disk or a bigger asset pack than this was tuned against. None of
	// them takes long enough on their own to trip that today (see the
	// stageTimer log), but ping the window between them anyway rather than
	// assume that stays true.
	win.PollEvents()

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
	log.Printf("engine: window framebuffer %dx%d, render target %dx%d (%s x%.2f)",
		fbW, fbH, renderW, renderH, base, cfg.RenderScale)

	// ras is the 2D overlay compositor only (weapon / HUD / crosshair /
	// status bar) — the world itself is the GPU-geometry (hardware) path's
	// job below. SetLightScale/SetAmbientLight aren't overlay settings: the
	// hardware path reads them back (render/vulkan/world.go) as shader
	// uniforms, so the sector-light fade and ambient floor stay identical
	// to what raster/light.go's tables define.
	ras := raster.New(textures, renderW, renderH)
	raster.SetLightScale(cfg.LightScale)
	raster.SetAmbientLight(cfg.AmbientLight)
	ras.SetHUDScale(cfg.HUDScale)
	ras.SetWeaponScale(cfg.WeaponScale)

	// Vulkan validation layers cost double-digit percent and add per-call
	// latency; keep them off unless explicitly asked for (TPF_VK_DEBUG=1).
	vkDebug := os.Getenv("TPF_VK_DEBUG") != ""
	done = stageTimer("create GPU renderer (device + pipelines + shaders)")
	renderer, err := vulkan.NewWorld(win, renderW, renderH, cfg.VSync, vkDebug)
	if err != nil {
		return fmt.Errorf("engine: hardware renderer failed to initialise: %w", err)
	}
	log.Printf("engine: renderer active (GPU geometry + HDR/bloom/dynamic lights)")
	done()
	win.PollEvents()
	defer renderer.Destroy()
	log.Printf("engine: rendering on %s (%s)", renderer.DeviceName, renderer.DeviceTypeName)
	if renderer.DeviceIsSoftware {
		log.Println("engine: WARNING — Vulkan selected a CPU/software device; this is not real hardware rendering")
	}

	game := engine.NewGame(win, renderer, ras, w, level, tree, cam, cfg)
	game.AltWAD = altWAD // switchWeapons fire-sound fallback (nil otherwise)
	done = stageTimer("build static world geometry (BSP walk + texture decode)")
	game.WorldBuilder = worldgeo.New(level, tree, textures)
	done()
	win.PollEvents()
	game.ApplyMapInfo() // push the starting level's UMAPINFO sky to the fresh WorldBuilder
	// HDR tonemap exposure (config) + a fixed bloom strength for the glow
	// around dynamic lights / bright texels. The mip-pyramid bloom
	// accumulates every scale, so it preserves more energy than a tight
	// single blur — this is dialled back so normally-lit level geometry
	// isn't washed, only genuine highlights glow.
	renderer.SetHDR(float32(cfg.Exposure), 0.55)

	if cfg.Voxels {
		// DoomVoxelPack.pk3 (assets/voxel/) is an updated/alternate monster
		// + item set assembled from a user-supplied .kvx drop — it covers
		// every Doom1/Ultimate Doom monster (troo/poss/sarg/skul/head/boss/
		// cybr/spid) plus items/decorations, but NOT Doom II's exclusive
		// roster (mancubus/revenant/arch-vile/pain elemental/arachnotron/
		// chaingunner), so it only replaces Doom1_Voxel.pk3 here — Doom II
		// maps keep Doom2_Voxel.pk3, which already covers its full roster.
		pk3 := "DoomVoxelPack.pk3"
		switch {
		case w.IsHeretic():
			pk3 = "reikallhereticvoxels.pk3"
		case strings.HasPrefix(mapName, "MAP"):
			pk3 = "Doom2_Voxel.pk3"
		}
		done = stageTimer("load voxel pack " + pk3)
		vs, err := assets.LoadVoxelPackDir(pk3)
		done()
		win.PollEvents()
		if err != nil {
			log.Printf("engine: voxel models unavailable (%v) — using sprites", err)
		} else {
			game.Voxels = vs
			log.Printf("engine: voxel models active — %s, %d sprite frames", pk3, vs.Frames())
			// Mesh + upload every model up front so no first-sight GPU stall
			// hits the render loop mid-game.
			done = stageTimer("mesh + upload voxel models")
			renderer.WarmVoxels(vs.Models())
			done()
			win.PollEvents()
		}
	}

	game.AudioDevice = dev // nil is fine — see audio.Setup's doc comment

	if !cfg.Music || music == nil {
		if !cfg.Music {
			log.Println("engine: music disabled (config.json)")
			// music is shared across trips through the launcher↔game loop
			// (audio.Setup runs once, outside this per-session function), so
			// a previous session's track needs to be silenced explicitly —
			// there's no fresh player this time to start silent instead.
			// Play(nil) is musicLoop.restart's documented no-op-events path:
			// it halts (and all-notes-offs) whatever was playing without
			// touching the backend device/synth, unlike Stop() (which is a
			// one-way teardown — see audio.PlayLevelMusic's doc comment).
			if music != nil {
				music.Play(nil)
			}
		}
	} else {
		music.SetVolume(cfg.MusicVolume)
		audio.PlayLevelMusic(w, music, game.MapMusicLump(mapName), mapName, "engine")
		// Restart music when the engine advances to the next level, and when
		// the idmus cheat asks for a specific map's track.
		game.OnLevelChange = func(m string) { audio.PlayLevelMusic(w, music, game.MapMusicLump(m), m, "engine") }
		game.OnMusicChange = func(m string) { audio.PlayLevelMusic(w, music, game.MapMusicLump(m), m, "engine") }
	}

	log.Printf("engine: [load] total (map chosen -> first frame): %s", time.Since(loadStart).Round(time.Millisecond))
	log.Println("engine: entering main loop (WASD/mouse to move+look, Space to jump, E to use doors/switches, 1-7 weapons, left click to fire, F1 cycles HUD, Esc to quit)")
	return game.Run()
}

// pickStartMap prefers the conventional first level of whichever format the
// WAD uses (E1M1 for Doom/Ultimate Doom, MAP01 for Doom II and most PWADs),
// falling back to whatever map was listed first.
func pickStartMap(maps []string) string {
	return wad.PreferredStartMap(maps)
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

