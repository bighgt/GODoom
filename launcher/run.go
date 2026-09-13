package launcher

import (
	"fmt"
	"log"
	"time"

	"twopointfive/config"
	"twopointfive/render/vulkan"
	"twopointfive/wad"
	"twopointfive/window"
)

// Result is what Run hands back on Play — everything cmd/engine needs to
// skip straight to bsp.Build(level) onward without repeating the WAD
// load/IWAD-merge/map-choice work the launcher already did.
type Result struct {
	WAD     *wad.WAD
	WADPath string
	MapName string
	Config  config.Config
}

// Run shows the pre-game launcher: a separate, fixed-size window (created
// and torn down entirely independently of the game window cmd/engine opens
// afterward — see window.NewFixed's doc comment on that GLFW lifecycle)
// where a WAD and level are picked and every config.json setting can be
// edited. ok is false for Quit or the window being closed (Result is then
// the zero value and config.json is left untouched); true for Play, after
// which config.json has already been updated (config.SaveMerge) with
// whatever was edited plus the chosen WAD path and map name.
func Run(initial config.Config) (Result, bool, error) {
	win, err := window.NewFixed(windowW, windowH, "TwoPointFive — Launcher")
	if err != nil {
		return Result{}, false, fmt.Errorf("launcher: %w", err)
	}
	defer win.Destroy()

	// The vanilla (lit=false, hw=false) blit renderer: exactly "present an
	// RGBA8 CPU buffer," precisely what this package's Canvas is (see
	// canvas.go's doc comment) — no new rendering path for the launcher.
	renderer, err := vulkan.New(win, windowW, windowH, true, false, false)
	if err != nil {
		return Result{}, false, fmt.Errorf("launcher: %w", err)
	}
	defer renderer.Destroy()

	canvas := NewCanvas(windowW, windowH)
	ui := NewUI(canvas)
	poller := NewPoller(win)
	m := NewModel(initial)

	last := time.Now()
	for !win.ShouldClose() && !m.Done {
		now := time.Now()
		dt := now.Sub(last).Seconds()
		last = now

		win.PollEvents()
		in := poller.Poll(dt)

		ui.Begin(in)
		Draw(ui, m)
		ui.End()

		if err := renderer.DrawFrame(canvas.Img.Pix); err != nil {
			return Result{}, false, fmt.Errorf("launcher: %w", err)
		}

		// Browse… opens a native, blocking file dialog (window.PickWADFile)
		// — done after presenting this frame so the window has already
		// shown the click landing, not mid-draw.
		if m.BrowseRequested {
			m.BrowseRequested = false
			if path, err := window.PickWADFile(); err != nil {
				if err != window.ErrPickCancelled {
					log.Printf("launcher: browse: %v", err)
				}
			} else {
				m.PickWAD(path)
			}
		}
	}

	if !m.Ok {
		return Result{}, false, nil
	}

	m.Cfg.Map = m.MapName
	m.Cfg.WADPath = m.WADPath
	// The Music Volume slider is the only music control (no checkbox): a
	// slider at 0 means "music off". Keep the Music bool — the engine's real
	// gate, and what a hand-edited config.json still uses — in sync with it.
	// (config.repaired then bumps a literal musicVolume 0 back to 1.0, which
	// is fine: Music=false is what silences it, and NewModel re-derives the
	// slider's 0 from Music next time.)
	m.Cfg.Music = m.Cfg.MusicVolume > 0
	path := config.ResolvePath()
	if err := config.SaveMerge(m.Cfg, path); err != nil {
		return Result{}, false, fmt.Errorf("launcher: save %s: %w", path, err)
	}
	log.Printf("launcher: saved %s", path)

	return Result{WAD: m.WAD, WADPath: m.WADPath, MapName: m.MapName, Config: m.Cfg}, true, nil
}
