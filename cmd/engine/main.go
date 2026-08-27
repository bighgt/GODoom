// Command engine is TwoPointFive's entry point: it asks for a WAD file,
// parses it, builds the first level's BSP tree, opens a window, stands up
// the Vulkan renderer and the CPU software rasterizer, positions a camera
// at the level's player start, and runs the main loop. See game_design.txt
// for the full engine design and roadmap.
package main

import (
	"errors"
	"fmt"
	"log"
	"math"
	"runtime"

	"twopointfive/assets"
	"twopointfive/audio"
	"twopointfive/bsp"
	"twopointfive/engine"
	"twopointfive/raster"
	"twopointfive/render/vulkan"
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
	if err := run(); err != nil {
		log.Fatalf("engine: %v", err)
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

	w, err := wad.Load(path)
	if err != nil {
		return err
	}
	log.Printf("engine: loaded %s WAD, %d lumps", w.Header.Magic, w.Header.NumLumps)

	maps := w.ListMaps()
	if len(maps) == 0 {
		return fmt.Errorf("engine: %s contains no map lumps (THINGS/LINEDEFS/... under a map marker)", path)
	}
	mapName := pickStartMap(maps)
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

	cam := startCamera(level, tree)
	log.Printf("engine: player start at (%.0f, %.0f, %.0f), facing %.0f°", cam.X, cam.Y, cam.Z, cam.Angle*180/math.Pi)

	win, err := window.New(1280, 720, "TwoPointFive — "+mapName)
	if err != nil {
		return err
	}
	defer win.Destroy()
	win.EnableMouseLook()

	ras := raster.New(textures)

	renderer, err := vulkan.New(win, raster.InternalWidth, raster.InternalHeight, true)
	if err != nil {
		return err
	}
	defer renderer.Destroy()
	log.Printf("engine: rendering on %s (%s)", renderer.DeviceName, renderer.DeviceTypeName)
	if renderer.DeviceTypeName == "CPU (software rasterizer — not real GPU rendering)" {
		log.Println("engine: WARNING — Vulkan selected a CPU/software device; this is not real hardware rendering")
	}

	game := engine.NewGame(win, renderer, ras, w, level, tree, cam)

	if dev, err := audio.NewDevice(); err != nil {
		log.Printf("engine: sound effects unavailable: %v", err)
	} else {
		game.AudioDevice = dev
	}

	if music, err := audio.NewMusicPlayer(); err != nil {
		log.Printf("engine: music unavailable: %v", err)
	} else {
		game.Music = music
		defer music.Stop()
		playLevelMusic(w, music, mapName)
	}

	log.Println("engine: entering main loop (WASD/mouse to move+look, 1-6 weapons, left click to fire, Space to use, Esc to quit)")
	return game.Run()
}

// playLevelMusic looks up the level's music lump (D_E1M1 for Doom/Ultimate
// Doom episodic maps; some PWADs also name Doom II-style maps this way,
// though vanilla Doom II's own D_RUNNIN-style names aren't mapped yet — see
// game_design.txt), decodes it, and starts it looping.
func playLevelMusic(w *wad.WAD, music *audio.MusicPlayer, mapName string) {
	lumpName := "D_" + mapName
	raw, ok := w.Find(lumpName)
	if !ok {
		log.Printf("engine: no music lump %q found for %s", lumpName, mapName)
		return
	}
	events, err := wad.DecodeMus(raw)
	if err != nil {
		log.Printf("engine: decode music %q: %v", lumpName, err)
		return
	}
	log.Printf("engine: playing music %s (%d events)", lumpName, len(events))
	music.Play(events)
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

// startCamera builds the initial raster.Camera from the level's Player 1
// start Thing (falling back to the map origin, facing east, if none is
// present), with its eye height resolved from whichever Sector the BSP tree
// says that point is actually in.
func startCamera(level *wad.Level, tree bsp.Node) raster.Camera {
	const player1Start = 1
	const eyeHeight = 41.0

	x, y, angleDeg := 0.0, 0.0, 0.0
	for _, t := range level.Things {
		if t.Type == player1Start {
			x, y, angleDeg = float64(t.X), float64(t.Y), float64(t.Angle)
			break
		}
	}

	z := eyeHeight
	if sec := bsp.PointSector(tree, level, float32(x), float32(y)); sec != nil {
		z = float64(sec.FloorHeight) + eyeHeight
	}

	return raster.Camera{X: x, Y: y, Z: z, Angle: angleDeg * math.Pi / 180, FOVDeg: 90}
}
