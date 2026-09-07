// Command engine is TwoPointFive's entry point: it asks for a WAD file,
// parses it, builds the first level's BSP tree, opens a window, stands up
// the Vulkan renderer and the CPU software rasterizer, positions a camera
// at the level's player start, and runs the main loop. See game_design.txt
// for the full engine design and roadmap.
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
	// holdConsoleOpen) before letting the process — and, if we own it, the
	// console — exit.
	holdConsoleOpen(err != nil)
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

	// Sound effects and music are process-wide resources (audio.Device/
	// MusicPlayer both join one shared, lazily-created mixer/output stream
	// — see audio/mixer.go's sharedAudio — and NewMusicPlayer's FluidSynth
	// setup on Linux is real, non-instant work), so they're stood up once
	// here and handed to every play session rather than recreated — and,
	// for MusicPlayer, torn down (Stop) — on each trip through the
	// launcher: MusicPlayer.Stop() deletes the underlying FluidSynth synth
	// outright (not a pause), so it must not run until the process is
	// actually exiting.
	dev, music := setupAudio()
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

	dev, music := setupAudio()
	if music != nil {
		defer music.Stop()
	}
	return playSession(path, mapName, cfg, w, dev, music)
}

// setupAudio brings up the shared sound-effect device and music player once
// — see run()'s doc comment on why these are process-wide rather than
// per-session. A nil return for either just means that subsystem is
// unavailable (already logged here); playSession treats a nil dev/music
// exactly like the original single-session code treated NewDevice/
// NewMusicPlayer failing.
func setupAudio() (*audio.Device, *audio.MusicPlayer) {
	dev, err := audio.NewDevice()
	if err != nil {
		log.Printf("engine: sound effects unavailable: %v", err)
		dev = nil
	}
	music, err := audio.NewMusicPlayer()
	if err != nil {
		log.Printf("engine: music unavailable: %v", err)
		music = nil
	}
	return dev, music
}

// playSession loads mapName from w and runs one full game session — every
// window, GPU, and CPU-rasterizer resource it creates is torn down (its
// defers run) when this returns, whether that's a normal game-window close
// or an error. dev/music are the process-wide audio resources setupAudio
// created; cfg.Music still gates whether THIS session actually starts a
// track (see setupAudio's doc comment on why the player itself isn't
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
	mpix := float64(renderW*renderH) / 1e6
	log.Printf("engine: window framebuffer %dx%d, CPU render target %dx%d (%.1f Mpix, %s x%.2f)",
		fbW, fbH, renderW, renderH, mpix, base, cfg.RenderScale)
	// The "large render target" / "filtered sampling is CPU-heavy" warnings
	// below (and the matching WarmTextureCache work) only mean anything for
	// the CPU rasterizer's own 3D-world pass — raster.Render/drawMobjs,
	// which the hardware (GPU-geometry) path never calls at all (renderFrame
	// just preps the small 2D overlay plane there; the world and its sprites
	// are the GPU's job, textured through its own always-on anisotropic
	// sampler, independent of this "textureQuality" setting entirely). Both
	// are deferred until hw's FINAL value is known (after the hardware
	// init/fallback below), not the configured intent — a hardware init
	// failure still needs the warm cache once it falls back to software.

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
	ras.SetGroundShadows(cfg.GroundShadows)
	if lit {
		ras.SetGBuffer(true)
	}

	// Vulkan validation layers cost double-digit percent and add per-call
	// latency; keep them off unless explicitly asked for (TPF_VK_DEBUG=1).
	vkDebug := os.Getenv("TPF_VK_DEBUG") != ""
	done = stageTimer("create GPU renderer (device + pipelines + shaders)")
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
	done()
	win.PollEvents()
	defer renderer.Destroy()
	log.Printf("engine: rendering on %s (%s)", renderer.DeviceName, renderer.DeviceTypeName)
	if renderer.DeviceIsSoftware {
		log.Println("engine: WARNING — Vulkan selected a CPU/software device; this is not real hardware rendering")
	}

	// Only the CPU rasterizer path (vanilla/enhanced, including a
	// hardware-init fallback that just landed here as hw=false) ever runs
	// raster.Render/drawMobjs against renderW*renderH pixels or filters a
	// texture through it — the hardware path's world/sprites are entirely
	// the GPU's job. Gating on the FINAL hw (post-fallback) rather than the
	// configured intent means a failed hardware init still gets its warm
	// cache instead of hitching mid-frame on the first room.
	if !hw {
		// The software rasterizer's cost is close to linear in render pixels
		// (section 18 of game_design.txt). Flag a heavy target so a low
		// frame rate is self-explanatory.
		if mpix > 2.4 {
			lever := "lower `resolution` or set `renderScale` below 1.0"
			if cfg.Windowed {
				lever = "use a smaller window or set `renderScale` below 1.0"
			}
			log.Printf("engine: that's a large render target for a CPU rasterizer — if the frame rate is low, %s", lever)
		}
		if cfg.TextureQuality != config.TextureNearest {
			log.Printf("engine: textureQuality=%s — filtered wall/flat sampling is CPU-heavy on the software rasterizer; set it to \"nearest\" if the frame rate suffers", cfg.TextureQuality)
			done = stageTimer("warm filtered-texture cache")
			ras.WarmTextureCache(level) // build mip chains now, not mid-frame on first sight of a room
			done()
			win.PollEvents()
		}
	}

	game := engine.NewGame(win, renderer, ras, w, level, tree, cam, cfg)
	game.LitMode = lit // may have been forced off by the fallback above
	if hw {
		game.HardwareMode = true
		done = stageTimer("build static world geometry (BSP walk + texture decode)")
		game.WorldBuilder = worldgeo.New(level, tree, textures)
		done()
		win.PollEvents()
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
		done = stageTimer("load voxel pack " + pk3)
		vs, err := assets.LoadVoxelPackDir(pk3)
		done()
		win.PollEvents()
		if err != nil {
			log.Printf("engine: voxel models unavailable (%v) — using sprites", err)
		} else {
			game.Voxels = vs
			ras.SetVoxelScale(cfg.VoxelScale)
			log.Printf("engine: voxel models active — %s, %d sprite frames", pk3, vs.Frames())
			if hw {
				// Mesh + upload every model up front so no first-sight GPU
				// stall hits the render loop mid-game.
				done = stageTimer("mesh + upload voxel models")
				renderer.WarmVoxels(vs.Models())
				done()
				win.PollEvents()
			}
		}
	}

	game.AudioDevice = dev // nil is fine — see setupAudio's doc comment

	if !cfg.Music || music == nil {
		if !cfg.Music {
			log.Println("engine: music disabled (config.json)")
			// music is shared across trips through the launcher↔game loop
			// (setupAudio runs once, outside this per-session function), so
			// a previous session's track needs to be silenced explicitly —
			// there's no fresh player this time to start silent instead.
			// Play(nil) is musicLoop.restart's documented no-op-events path:
			// it halts (and all-notes-offs) whatever was playing without
			// touching the backend device/synth, unlike Stop() (which is a
			// one-way teardown — see playLevelMusic's doc comment).
			if music != nil {
				music.Play(nil)
			}
		}
	} else {
		playLevelMusic(w, music, game.MapMusicLump(mapName), mapName)
		// Restart music when the engine advances to the next level, and when
		// the idmus cheat asks for a specific map's track.
		game.OnLevelChange = func(m string) { playLevelMusic(w, music, game.MapMusicLump(m), m) }
		game.OnMusicChange = func(m string) { playLevelMusic(w, music, game.MapMusicLump(m), m) }
	}

	log.Printf("engine: [load] total (map chosen -> first frame): %s", time.Since(loadStart).Round(time.Millisecond))
	log.Println("engine: entering main loop (WASD/mouse to move+look, Space to jump, E to use doors/switches, 1-7 weapons, left click to fire, F1 cycles HUD, Esc to quit)")
	return game.Run()
}

// playLevelMusic finds the level's music lump, decodes it, and starts it
// looping. A UMAPINFO music lump (override) is tried first; then the
// conventional name for the map's format (wad.MusicLumpName — "D_E1M1" for
// ExMy, "D_RUNNIN"-style for Doom II MAPxx), then "D_"+mapname as a fallback
// (some PWADs name Doom II music that way), then it silences whatever was
// playing and gives up quietly. That last step matters because music is a
// single player shared across level changes and across whole launcher↔game
// sessions (see setupAudio) — without it, a map/WAD with no track of its own
// would just let the previous one keep playing underneath it. It silences
// via Play(nil), not Stop(): Stop is a one-way teardown of the backend
// device/synth (see e.g. audio's fluidsynth_windows.go/midi_linux.go doc
// comments) meant to run once at process exit, not something safe to call
// and then keep playing through for the rest of the session.
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
			music.Play(nil)
			return
		}
		log.Printf("engine: playing music %s (%d events)", lumpName, len(events))
		music.Play(events)
		return
	}
	log.Printf("engine: no music lump found for %s (tried %v)", mapName, candidates)
	music.Play(nil)
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
