// Command isoengine is a standalone, Vulkan-hardware-rendered top-down
// isometric Doom/Doom II game — a second, separate rendering + control
// scheme alongside cmd/engine's first-person one, built on the exact same
// engine.Game simulation (physics, monster AI, sector specials, combat,
// pickups). Movement is WASD, screen-relative to the camera; the player
// faces (and fires toward) the mouse cursor's position on the ground. It
// shows the same pre-game launcher (package launcher) cmd/engine does — a
// WAD/level picker plus a full config.json settings editor — before any
// iso-specific window or renderer stands up; see run's doc comment.
package main

import (
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/go-gl/glfw/v3.3/glfw"

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
	// GLFW's window/event functions must run on the thread that called
	// glfw.Init() — see cmd/engine/main.go's identical init() for why.
	runtime.LockOSThread()
}

func main() {
	err := run()
	if err != nil {
		log.Printf("isoengine: %v", err)
	}
	// Keep the debug console readable after the window is gone (see
	// window.HoldConsoleOpen) before letting the process — and, if we own
	// it, the console — exit.
	window.HoldConsoleOpen(err != nil)
	if err != nil {
		os.Exit(1)
	}
}

// run is the process entry point's real body — it mirrors cmd/engine's own
// run() exactly: either the headless/scripted single-shot path (TPF_WAD / a
// .wad command-line argument — see window.WADFromCommandLine) or the normal
// one, which loops the pre-game launcher (package launcher) and a play
// session back and forth. Quit in the launcher is the only way out; ending a
// game session (Escape, or the window's close button) returns to the
// launcher instead of exiting the process, so choosing another WAD/level or
// tweaking a setting doesn't mean relaunching the executable. The launcher
// itself is entirely unmodified from cmd/engine's — some of the settings it
// edits (FOV, mouse-look sensitivity, ...) are first-person-only and simply
// don't apply to this camera scheme; playSession only reads the ones that
// do.
func run() error {
	cameraMode := flag.String("camera", "orbit", `initial camera mode: "orbit" (free rotate/tilt/zoom) or "fixed" (classic snapped-angle isometric)`)
	flag.Parse()
	if *cameraMode != "orbit" && *cameraMode != "fixed" {
		log.Printf("isoengine: unknown -camera %q, using \"orbit\"", *cameraMode)
		*cameraMode = "orbit"
	}

	if headless := window.WADFromCommandLine(); headless != "" {
		return runHeadless(headless, *cameraMode)
	}

	// Sound effects and music are process-wide resources — see audio.Setup's
	// doc comment — stood up once here and handed to every play session.
	dev, music := audio.Setup("isoengine")
	if music != nil {
		defer music.Stop()
	}

	for {
		// config.Load() is re-read each time round so a session that just
		// saved new settings starts the next launcher screen from them — see
		// cmd/engine's run() for the identical reasoning.
		res, ok, err := launcher.Run(config.Load())
		if err != nil {
			return err
		}
		if !ok {
			log.Println("isoengine: launcher cancelled, exiting")
			return nil
		}
		log.Printf("isoengine: loaded %s, playing %s", res.WADPath, res.MapName)

		if err := playSession(res.WADPath, res.MapName, res.Config, res.WAD, dev, music, *cameraMode); err != nil {
			return err
		}
		log.Println("isoengine: game session ended — back to the launcher")
	}
}

// runHeadless is the TPF_WAD / .wad-argument escape hatch: no launcher, no
// map-picker dialog — just load path and go straight to a play session, the
// same single-shot shape cmd/engine's own runHeadless uses for scripted
// runs, tests, and ad-hoc diagnostics.
func runHeadless(path, cameraMode string) error {
	log.Printf("isoengine: loading %s", path)
	cfg := config.Load()

	loaded, err := wad.LoadWithIWAD(path, cfg.IWAD)
	if err != nil {
		return err
	}
	log.Printf("isoengine: loaded %s WAD, %d lumps", loaded.PickedMagic, loaded.PickedLumps)
	if loaded.IWADPath != "" {
		log.Printf("isoengine: %s is a patch WAD — merging base IWAD %s", filepath.Base(path), loaded.IWADPath)
	}
	w := loaded.WAD
	if err := wad.CheckPlayable(w, filepath.Base(path)); err != nil {
		return err
	}

	maps := w.ListMaps()
	if len(maps) == 0 {
		return fmt.Errorf("isoengine: %s has no maps (no THINGS/LINEDEFS lumps under a map marker)", path)
	}
	mapName := wad.PreferredStartMap(maps)
	for _, m := range maps {
		if strings.EqualFold(m, cfg.Map) {
			mapName = m
			break
		}
	}
	log.Printf("isoengine: found %d map(s), loading %s", len(maps), mapName)

	dev, music := audio.Setup("isoengine")
	if music != nil {
		defer music.Stop()
	}
	return playSession(path, mapName, cfg, w, dev, music, cameraMode)
}

// playSession loads mapName from w and runs one full game session — every
// window, GPU, and CPU-rasterizer resource it creates is torn down (its
// defers run) when this returns, whether that's a normal game-window close
// or an error. dev/music are the process-wide audio resources audio.Setup
// created; cfg.Music still gates whether THIS session actually starts a
// track — mirrors cmd/engine's own playSession exactly (see its doc
// comment).
func playSession(path, mapName string, cfg config.Config, w *wad.WAD, dev *audio.Device, music *audio.MusicPlayer, cameraMode string) error {
	// Only real-3D WADs (cmd/wad3d output, with baked voxels) are playable.
	if err := wad.CheckPlayable(w, filepath.Base(path)); err != nil {
		return err
	}
	level, err := w.LoadLevel(mapName)
	if err != nil {
		return fmt.Errorf("load level %s: %w", mapName, err)
	}
	tree, err := bsp.Build(level)
	if err != nil {
		return fmt.Errorf("build BSP for %s: %w", mapName, err)
	}
	textures, err := assets.New(w)
	if err != nil {
		return fmt.Errorf("index textures: %w", err)
	}
	if d := w.DescribeTPF3D(); d != "" {
		log.Printf("isoengine: %s", d)
	}

	// config's switchWeapons: play with the other game's arsenal — see
	// engine.NewGame for the table swap; here for the cross-game sprites.
	var altWAD *wad.WAD
	if cfg.SwitchWeapons {
		if ow, op, ok := wad.OtherGameIWAD(path, w); ok {
			altWAD = ow
			textures.SetSpriteFallback(ow)
			log.Printf("isoengine: switchWeapons — weapon art/sounds from %s", filepath.Base(op))
		} else if textures.EmbeddedSpriteCount() > 0 {
			log.Printf("isoengine: switchWeapons — weapon art/sounds from this WAD's embedded weapon set")
		} else {
			log.Printf("isoengine: switchWeapons on, but the other game's IWAD wasn't found — the arsenal still swaps, art may be blank")
		}
	}

	win, err := window.New(1280, 720, "TwoPointFive - Isometric", false)
	if err != nil {
		return fmt.Errorf("open window: %w", err)
	}
	defer win.Destroy()
	// The mouse aims (see IsoAimX/Y below) rather than moving the camera, so
	// the system arrow cursor is misleading — a crosshair reads as "this is
	// where you're aiming." GLFW ships this as a standard shape; no image
	// asset or custom Vulkan overlay needed.
	win.Handle().SetCursor(glfw.CreateStandardCursor(glfw.CrosshairCursor))

	rw, rh := win.FramebufferSize()
	if rw <= 0 || rh <= 0 {
		rw, rh = 1280, 720
	}
	renderer, err := vulkan.NewIso(win, rw, rh, cfg.VSync, false)
	if err != nil {
		return fmt.Errorf("start Vulkan (iso): %w", err)
	}
	defer renderer.Destroy()
	log.Printf("isoengine: GPU %s (%s)", renderer.DeviceName, renderer.DeviceTypeName)

	// raster.Renderer is never asked to rasterize the 3D world here — only
	// its Textures()/ThingSprite() (see engine.CollectIsoWorldObjects) and
	// its 2D overlay plane (DrawStatusBar/DrawQuakeHUD via engine.Game.
	// DrawHUD, composited by the Vulkan iso renderer — see DrawIso's
	// overlay parameter) are used — but engine.NewGame requires a
	// *raster.Renderer regardless of render path. Sized to match the
	// offscreen scene resolution 1:1, so the HUD composites without a
	// separate scale step.
	ras := raster.New(textures, rw, rh)

	cam := engine.StartCamera(level, tree)
	game := engine.NewGame(win, renderer, ras, w, level, tree, cam, cfg)
	game.AudioDevice = dev
	game.AltWAD = altWAD // switchWeapons fire-sound fallback (nil otherwise)
	game.WorldBuilder = worldgeo.New(level, tree, textures)
	game.WorldBuilder.SkipCeilings = true
	game.WorldBuilder.Invalidate()
	game.ReplaceMovementSystem(engine.IsoMovementSystem{})
	game.ReplaceUseSystem(engine.IsoUseSystem{})
	game.OnLevelChange = func(next string) {
		// loadMap rebuilds WorldBuilder from scratch for the new level (see
		// engine/level.go), which resets SkipCeilings to false; reapply it
		// and rebuild once more.
		game.WorldBuilder.SkipCeilings = true
		game.WorldBuilder.Invalidate()
		audio.PlayLevelMusic(w, music, game.MapMusicLump(next), next, "isoengine")
	}
	if cfg.Music && music != nil {
		music.SetVolume(cfg.MusicVolume)
		audio.PlayLevelMusic(w, music, game.MapMusicLump(mapName), mapName, "isoengine")
	}
	if err := loadVoxels(game, cfg); err != nil {
		return err
	}

	ctl := newCameraController(worldgeo.IsoCamera{
		TargetX: cam.X, TargetY: cam.Y, TargetZ: cam.Z,
		Azimuth: math.Pi / 4, Elevation: worldgeo.IsoTrueElevation,
		Zoom: 700, Mode: cameraMode,
	})

	lastFrame := time.Now()

	for !win.ShouldClose() {
		win.PollEvents()
		if win.EscapePressed() {
			win.RequestClose()
			break
		}
		now := time.Now()
		dt := float32(now.Sub(lastFrame).Seconds())
		lastFrame = now
		if dt > 0.1 {
			dt = 0.1 // matches engine.Game's own maxFrameDelta clamp
		}

		// Camera follows the player; the controller only rotates/tilts/
		// zooms it (see camera.go).
		ctl.cam.TargetX, ctl.cam.TargetY, ctl.cam.TargetZ = game.Camera.X, game.Camera.Y, game.PlayerZ
		ctl.poll(win, float64(dt))

		// Aim: the mouse's raw ground-plane point, unless the crosshair is
		// close enough on screen to a live shootable target to snap onto it
		// instead (see aimSnapRadius) — that's what makes a shot the
		// crosshair is visually on reliably hit (engine.Game.IsoAimLocked,
		// read by weapons.go's hitscanSpread).
		mx, my := win.CursorPos()
		if ax, ay, ok := worldgeo.IsoUnprojectToGround(ctl.cam, rw, rh, mx, my, game.PlayerZ); ok {
			game.IsoAimX, game.IsoAimY = ax, ay
		}
		game.IsoAimLocked = false
		if tx, ty, ok := nearestAimTarget(game, ctl.cam, rw, rh, mx, my); ok {
			game.IsoAimX, game.IsoAimY = tx, ty
			game.IsoAimLocked = true
		}
		game.IsoCameraAzimuth = ctl.cam.Azimuth

		game.AdvanceSimulation(dt)

		// Sprites are rebuilt every frame (unlike the Phase 1 static viewer):
		// monsters/projectiles move every tic, so there is no "camera didn't
		// change" shortcut available here the way there was for a level with
		// nothing moving in it.
		restore := game.SyncIsoWorldGeometry()
		geo := game.WorldBuilder.Build(0, 0)
		sprites, voxels := game.CollectIsoWorldObjects(ctl.cam.Azimuth)

		// Reveal points: the monsters/items/barrels/etc a wall should fade
		// for if one stands between it and the camera (see DrawIso's own doc
		// comment and iso.frag's occluderFade), built from the sprites/voxels
		// this frame already collected — but only those NEAR THE PLAYER
		// (isoRevealWorldRadius). A thing off in a far room behind a dozen
		// walls isn't something you're about to fight, and fading a wall for
		// each of dozens of them would stipple the whole map. The player
		// itself needs no entry: the camera tracks it, so iso.frag's
		// playerFade covers screen centre for free.
		reveal := make([][3]float32, 0, len(sprites)+len(voxels))
		add := func(x, y, z float32) {
			dx, dy := float64(x)-game.Camera.X, float64(y)-game.Camera.Y
			if dx*dx+dy*dy <= isoRevealWorldRadius*isoRevealWorldRadius {
				reveal = append(reveal, [3]float32{x, y, z})
			}
		}
		for _, s := range sprites {
			add(s.X, s.Y, s.Z)
		}
		for _, v := range voxels {
			add(v.X, v.Y, v.Z)
		}

		if ps, ok := playerSprite(game, ctl.cam.Azimuth, dt); ok {
			sprites = append(sprites, ps)
		}
		game.WorldBuilder.AddSprites(ctl.cam.Azimuth, sprites)
		game.WorldBuilder.AddVoxels(voxels)
		restore()

		// HUD: health/armour/ammo and the selected weapon (config hudStyle /
		// F1 cycles it, same as cmd/engine) — drawn onto ras's overlay plane
		// by the exact same package-raster code the FPS hardware path uses,
		// composited by the Vulkan iso renderer (see DrawIso's own doc
		// comment).
		ras.BeginOverlayFrame()
		game.DrawHUD()

		if err := renderer.DrawIso(geo, ctl.cam, ras.Overlay(), reveal); err != nil {
			return fmt.Errorf("draw: %w", err)
		}
	}
	return nil
}

// loadVoxels loads the voxel models baked into the real-3D WAD (TPFVOXEL —
// see assets.LoadVoxelsFromWAD; map objects are voxel-only, and never read
// from disk). engine.CollectIsoWorldObjects draws every map object from
// them. Meshing/GPU upload happens lazily, the first time a given model
// comes into view (render/vulkan's isoworld.go syncIsoVoxelMeshes), rather
// than warmed up front the way cmd/engine's hardware path does.
func loadVoxels(game *engine.Game, cfg config.Config) error {
	vs, packs, err := assets.LoadVoxelsFromWAD(game.WAD)
	if err != nil {
		return fmt.Errorf("isoengine: voxel models: %w — convert the original WAD again with %s", err, wad.ConvertTool)
	}
	game.Voxels = vs
	game.ModelScale = cfg.ModelScale
	log.Printf("isoengine: voxel models — %s, %d sprite frames (map objects are voxel-only)", strings.Join(packs, " + "), vs.Frames())
	return nil
}

// playerViewerDist mirrors engine.isoViewerDist: how far along -camAzimuth
// the virtual viewpoint playerSprite builds sits when picking the player's
// own rotation frame, far enough that raster.SpriteRotation's quantisation
// matches an actually-infinitely-distant orthographic viewer.
const playerViewerDist = 1 << 20

// isoRevealWorldRadius is how far from the player (map units) a thing can be
// and still drive the wall occluder-fade / occluded-silhouette (see the
// frame loop's reveal list and iso.frag). ~10 tiles — the stuff you'd
// actually be engaging; anything past it dotting the map would be noise.
const isoRevealWorldRadius = 460.0

// playerWalkPhase animates the player's own PLAY sprite through Doom's
// 4-frame walk cycle (A/B/C/D — the same frames a netgame shows other
// players in) while moving, held on frame A (standing) at rest.
// Package-level rather than a local: playerSprite is called once per frame
// from one goroutine, so this is just persistent state across those calls,
// the same shape Mobj.Frame would be if the player were an ordinary Mobj.
//
// The player's own mobj is deliberately not one of Game.mobjs (see
// engine's playerMobj doc comment — in first-person you never see
// yourself), so engine.CollectIsoWorldObjects never builds this sprite;
// an isometric view has to build it itself.
var playerWalkPhase float64

// loggedNoPlayerSprite rate-limits playerSprite's "no PLAY art" diagnostic
// to once — every one of this level's frames would otherwise fail the
// same way.
var loggedNoPlayerSprite bool

// playerWalkCyclesPerUnit is how many walk-cycle frames one map unit of
// travel advances — chosen to look reasonable at moveTopSpeed, not ported
// from anything (vanilla Doom never renders the local player's own body).
const playerWalkCyclesPerUnit = 1.0 / 40.0

// playerSprite builds this frame's billboard for the player's own body —
// Doom's PLAY sprite, rotated to face game.Camera.Angle (which
// IsoMovementSystem keeps pointed at the mouse's aim point) as seen from a
// virtual viewpoint along -camAzimuth, exactly the same rotation-frame
// convention engine.CollectIsoWorldObjects uses for every other thing.
func playerSprite(game *engine.Game, camAzimuth float64, dt float32) (worldgeo.Sprite, bool) {
	frame := 0
	if speed := math.Hypot(game.VelX, game.VelY); speed > 4 {
		playerWalkPhase += speed * float64(dt) * playerWalkCyclesPerUnit
		frame = int(playerWalkPhase) % 4
	}
	viewCam := raster.Camera{
		X: math.Cos(camAzimuth) * -playerViewerDist,
		Y: math.Sin(camAzimuth) * -playerViewerDist,
	}
	sp, flip, lift, ok := game.Raster.ThingSprite(viewCam, game.Camera.X, game.Camera.Y, game.Camera.Angle, "PLAY", frame)
	if !ok {
		if !loggedNoPlayerSprite {
			loggedNoPlayerSprite = true
			log.Printf("isoengine: no PLAY sprite art found (frame %d) — the player's own body won't be visible", frame)
		}
		return worldgeo.Sprite{}, false
	}
	light := 1.0
	if sec := bsp.PointSector(game.BSP, game.Level, float32(game.Camera.X), float32(game.Camera.Y)); sec != nil {
		light = float64(sec.LightLevel) / 255
	}
	return worldgeo.Sprite{
		X: float32(game.Camera.X), Y: float32(game.Camera.Y), Z: float32(game.PlayerZ),
		Lift: float32(lift), SizeMul: 1,
		Tex: sp, Light: float32(light), Flip: flip,
	}, true
}

// aimSnapRadius is how close (in the offscreen render target's own pixel
// space — see rw/rh in playSession) the mouse must be to a shootable
// target's projected screen position for nearestAimTarget to snap onto it.
const aimSnapRadius = 40.0

// nearestAimTarget finds whichever of game.AimTargetPositions() projects
// (worldgeo.IsoProjectToScreen) nearest the mouse at (mx, my), within
// aimSnapRadius pixels. ok is false if nothing shootable is that close —
// the caller then falls back to the mouse's raw ground-plane point.
func nearestAimTarget(game *engine.Game, cam worldgeo.IsoCamera, w, h int, mx, my float64) (x, y float64, ok bool) {
	best := aimSnapRadius
	for _, t := range game.AimTargetPositions() {
		sx, sy := worldgeo.IsoProjectToScreen(cam, w, h, t.X, t.Y, t.Z)
		if d := math.Hypot(sx-mx, sy-my); d < best {
			best, x, y, ok = d, t.X, t.Y, true
		}
	}
	return x, y, ok
}
