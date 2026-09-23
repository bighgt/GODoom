package editor

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"twopointfive/imkit"
	"twopointfive/render/vulkan"
	"twopointfive/render/worldgeo"
	"twopointfive/wad"
	"twopointfive/window"
)

const (
	defaultWinW, defaultWinH = 1600, 900 // windowed-mode size
	windowTitle              = "TwoPointFive — Editor"
	menuH                    = 22
	statusH                  = 22
	panelW                   = 260 // right-docked browser/inspector panel
	splitH                   = 5
)

// App is the whole editor session.
type App struct {
	win        *window.Window
	blit       *vulkan.Renderer
	cv         *imkit.Canvas
	ui         *imkit.UI
	poll       *imkit.Poller
	fullscreen bool
	windowedW  int // remembered windowed size, restored when leaving fullscreen
	windowedH  int
	last       time.Time
	quit       bool
	status     string

	bank *Bank
	doc  *Doc

	split imkit.SplitState // between the 2D view and the (3D + panel) block
	v2    view2d
	v3    view3d
	panel panelState

	// deferred menu action to run after the frame (so we're not mutating
	// mid-draw — includes toggling fullscreen, which tears down and
	// recreates the window/renderer)
	pending func()
}

// emptyWorldGeom is fed to DrawWorld while no WAD is loaded (a.doc == nil).
// DrawWorld also presents the frame — it's the sole call that blits a.cv
// (the whole 2D UI) to the screen (see the loop below) — so it must run
// every frame even with nothing to render in 3D; an empty Geometry draws
// zero triangles and skips texture/static-buffer sync.
var emptyWorldGeom = &worldgeo.Geometry{}

// Run opens the editor. wadArg is a WAD path (empty = auto-discover the
// first WAD with maps under wad/ or assets/). If none is found (or wadArg
// names one that fails to load), the editor still opens with no map loaded
// — use File > Open WAD... (Ctrl+O) to pick one via the native file dialog.
// startNew begins on a fresh one-room level instead of the WAD's first map
// (only for this initial load); fullscreen opens borderless-fullscreen on
// the primary monitor instead of the default windowed size (F11 toggles it
// either way once running).
func Run(wadArg string, startNew, fullscreen bool) error {
	a := &App{
		windowedW: defaultWinW, windowedH: defaultWinH,
		last: time.Now(),
	}
	if err := a.openWindow(fullscreen); err != nil {
		return err
	}
	defer func() {
		a.blit.Destroy()
		a.win.Destroy()
	}()

	a.ui = imkit.New(a.cv)
	a.split.Pos = a.cv.W / 2 // 2D pane / (3D + panel) split
	a.v2.onPick = func(x, y, z float64) { a.v3.FocusOn(x, y, z) }
	a.v3.init(nil)
	a.panel.init()
	imkit.LabelWidth = 92

	if wadPath, err := resolveWAD(wadArg); err != nil {
		a.status = "no WAD found — File > Open WAD…  (Ctrl+O)"
		log.Printf("editor: %v", err)
	} else if err := a.loadWAD(wadPath, startNew); err != nil {
		a.status = "load " + wadPath + ": " + err.Error()
		log.Printf("editor: %v", err)
	}

	for !a.win.ShouldClose() && !a.quit {
		now := time.Now()
		dt := now.Sub(a.last).Seconds()
		a.last = now
		a.win.PollEvents()
		in := a.poll.Poll(dt)

		a.ui.Begin(in)
		a.frame(dt)
		a.ui.End()

		// The 3D preview pane cut a transparent hole in a.cv over its own
		// rect (view3d.draw); DrawWorld paints the GPU-rendered world
		// underneath the WHOLE canvas, invisible everywhere else stayed
		// opaque — see view3d's doc comment.
		geom, camv := a.v3.geom, a.v3.wc
		if a.doc == nil {
			geom = emptyWorldGeom
			camv = worldgeo.Camera{
				X: a.v3.cam.X, Y: a.v3.cam.Y, Z: a.v3.cam.Z,
				Angle: a.v3.cam.Angle, Pitch: a.v3.cam.Pitch, FOVDeg: a.v3.cam.FOVDeg,
			}
		}
		// 0, a.cv.H (the whole plane): the editor has no dirty-rect tracking
		// of its own canvas like raster.Renderer's HUD overlay does, and
		// preview redraws are already gated on actual edits, not every
		// frame — no need for the game loop's partial-upload optimization.
		if err := a.blit.DrawWorld(geom, camv, a.cv.W, a.cv.H, a.cv.Img.Pix, 0, a.cv.H, nil); err != nil {
			return err
		}
		if a.pending != nil {
			f := a.pending
			a.pending = nil
			f()
		}
		if a.doc != nil {
			a.doc.RebuildIfDirty() // pick up geometry edits made this frame
		}
	}
	return nil
}

// loadWAD opens wadPath fresh: loads it, rebuilds the merged texture bank
// (every WAD under wad/Game WADs, wad/, assets/, plus this one), and opens
// its preferred map — or, if startNew, a new one-room level using its
// textures. Used both for Run's initial load and the "Open WAD..." menu
// action, so a WAD can be loaded (or swapped) at any time, not just at
// startup.
func (a *App) loadWAD(wadPath string, startNew bool) error {
	w, err := wad.Load(wadPath)
	if err != nil {
		return fmt.Errorf("load %s: %w", wadPath, err)
	}
	maps := w.ListMaps()
	if len(maps) == 0 {
		return fmt.Errorf("%s has no maps", wadPath)
	}
	bank, err := LoadBank(w, wadPath)
	if err != nil {
		return fmt.Errorf("assets: %w", err)
	}
	var doc *Doc
	if startNew {
		doc = NewEmptyDoc(bank, "")
	} else {
		mapName := preferredMap(maps)
		if doc, err = NewDoc(w, wadPath, mapName); err != nil {
			return fmt.Errorf("load map %s: %w", mapName, err)
		}
	}

	a.bank = bank
	a.v3.bank = bank
	a.setDoc(doc)
	a.status = fmt.Sprintf("%s  —  %s", filepath.Base(wadPath), doc.MapName)
	log.Printf("editor: %s  map %s  (%d walls, %d flats)", filepath.Base(wadPath), doc.MapName, len(bank.Walls), len(bank.Flats))
	return nil
}

// openWADDialog pops the native "open file" dialog (blocking) and loads
// whatever WAD the user picks. Run from a.pending — after the frame has
// already presented, matching how the pre-game launcher times the same
// blocking dialog (see launcher/run.go).
func (a *App) openWADDialog() {
	path, err := window.PickWADFile()
	if err != nil {
		if err != window.ErrPickCancelled {
			a.status = "open WAD: " + err.Error()
		}
		return
	}
	if err := a.loadWAD(path, false); err != nil {
		a.status = "open " + path + ": " + err.Error()
	}
}

// openWindow (re)creates the window and its GPU-geometry renderer at
// fullscreen or windowed size, tearing down the previous ones first (both
// nil on the very first call, from Run). The CPU canvas is resized in
// place — same *Canvas, so a.ui and every widget's cross-frame state
// survive the switch — and the split position rescales to the new width so
// the 2D/3D balance the user set stays proportional across the toggle.
func (a *App) openWindow(fullscreen bool) error {
	if a.blit != nil {
		a.blit.Destroy()
	}
	if a.win != nil {
		a.win.Destroy()
	}
	var win *window.Window
	var err error
	if fullscreen {
		win, err = window.New(0, 0, windowTitle, true) // size ignored; monitor's native mode
	} else {
		win, err = window.NewFixed(a.windowedW, a.windowedH, windowTitle)
	}
	if err != nil {
		return fmt.Errorf("open %s window: %w", fullscreenWord(fullscreen), err)
	}
	fw, fh := win.FramebufferSize()
	if fw == 0 || fh == 0 {
		fw, fh = a.windowedW, a.windowedH
	}
	blit, err := vulkan.NewWorld(win, fw, fh, true, false, false, false) // waterRefraction/realShadows: editor preview doesn't need either
	if err != nil {
		win.Destroy()
		return fmt.Errorf("open %s renderer: %w", fullscreenWord(fullscreen), err)
	}

	oldW := 0
	if a.cv != nil {
		oldW = a.cv.W
		a.cv.Resize(fw, fh)
	} else {
		a.cv = imkit.NewCanvas(fw, fh)
	}
	if oldW > 0 && a.split.Pos > 0 {
		a.split.Pos = a.split.Pos * fw / oldW
	}

	a.win, a.blit, a.fullscreen = win, blit, fullscreen
	a.poll = imkit.NewPoller(win)
	return nil
}

func fullscreenWord(v bool) string {
	if v {
		return "fullscreen"
	}
	return "windowed"
}

// toggleFullscreen flips between fullscreen and the remembered windowed
// size. Run from a.pending, never mid-frame — it destroys and recreates
// the window and Vulkan renderer.
func (a *App) toggleFullscreen() {
	if err := a.openWindow(!a.fullscreen); err != nil {
		a.status = "fullscreen: " + err.Error()
	}
}

func (a *App) frame(dt float64) {
	c := a.cv
	c.Fill(imkit.Theme.Background)
	full := imkit.Rect{X: 0, Y: 0, W: c.W, H: c.H}

	body := a.ui.BeginMenuBar(full, menuH)
	a.drawMenus()

	statusRect := imkit.Rect{X: 0, Y: c.H - statusH, W: c.W, H: statusH}
	work := imkit.Rect{X: body.X, Y: body.Y, W: body.W, H: body.H - statusH}

	if a.doc == nil {
		c.TextCenter(imkit.Rect{X: work.X, Y: work.Y + work.H/2 - 8, W: work.W, H: 16},
			"No WAD loaded — File > Open WAD…  (Ctrl+O)", imkit.Theme.TextDim)
	} else {
		// Split: 2D view on the left, (3D view + docked panel) on the right.
		left, right := a.ui.VSplit("main", work, &a.split, splitH, 320)

		// The right block is the 3D view plus a fixed-width panel on its far
		// right edge.
		pnl := imkit.Rect{X: right.X + right.W - panelW, Y: right.Y, W: panelW, H: right.H}
		v3r := imkit.Rect{X: right.X, Y: right.Y, W: right.W - panelW - 1, H: right.H}
		a.cv.FillRect(imkit.Rect{X: v3r.X + v3r.W, Y: v3r.Y, W: 1, H: v3r.H}, imkit.Theme.Border)

		// a.v3.cam is last frame's pose (v3 hasn't drawn yet this frame) —
		// a one-frame-old camera position for the 2D view's marker, well
		// under a tenth of a second of lag and not perceptible.
		a.v2.draw(a.ui, left, a.doc, a.bank, &a.panel, a.v3.cam.X, a.v3.cam.Y, a.v3.cam.Angle)
		a.v3.draw(a.ui, v3r, a.doc, dt)
		a.panel.draw(a.ui, pnl, a.doc, a.bank)

		// The panel's Import buttons queue a blocking native dialog the same
		// way App's own menu actions do — run it after this frame presents.
		if p := a.panel.pending; p != nil {
			a.panel.pending = nil
			a.pending = func() {
				if msg, ok := p(); ok {
					a.status = msg
				}
			}
		}
	}

	// status bar
	c.FillRect(statusRect, imkit.Theme.Panel)
	c.FillRect(imkit.Rect{X: 0, Y: statusRect.Y, W: c.W, H: 1}, imkit.Theme.Border)
	msg := a.status
	if a.doc != nil {
		if a.doc.treeErr != nil {
			msg += "   [3D: " + a.doc.treeErr.Error() + "]"
		}
		wx, wy := a.v2.worldUnderCursor(a.ui.In)
		msg += fmt.Sprintf("   x %d  y %d   grid %d   mode %s", wx, wy, a.v2.grid, a.v2.mode.String())
	}
	c.TextLeft(statusRect, msg, imkit.Theme.TextDim)
}

func (a *App) drawMenus() {
	fileItems := []imkit.MenuItem{
		{Label: "Open WAD…", Shortcut: "Ctrl+O"},
		{Label: "New level", Shortcut: "Ctrl+N", Disabled: a.bank == nil},
		{Label: "Reload map", Shortcut: "Ctrl+R", Disabled: a.doc == nil || a.doc.WAD == nil},
		{Label: ""},
		{Label: "Save map as PWAD", Shortcut: "Ctrl+S", Disabled: a.doc == nil},
		{Label: "Play this map", Shortcut: "F5", Disabled: a.doc == nil},
		{Label: ""},
		{Label: "Quit", Shortcut: "Ctrl+Q"},
	}
	switch a.ui.Menu("File", fileItems) {
	case 0:
		a.pending = a.openWADDialog
	case 1:
		a.pending = a.newLevel
	case 2:
		a.pending = func() { a.reloadMap() }
	case 4:
		a.pending = a.saveMap
	case 5:
		a.pending = a.playMap
	case 7:
		a.quit = true
	}
	if a.ui.In.Ctrl() && a.ui.In.Edge(imkit.KeyO) {
		a.pending = a.openWADDialog
	}
	if a.bank != nil && a.ui.In.Ctrl() && a.ui.In.Edge(imkit.KeyN) {
		a.pending = a.newLevel
	}
	if a.doc != nil && a.ui.In.Ctrl() && a.ui.In.Edge(imkit.KeyR) {
		a.pending = func() { a.reloadMap() }
	}
	if a.doc != nil && a.ui.In.Ctrl() && a.ui.In.Edge(imkit.KeyS) {
		a.pending = a.saveMap
	}
	if a.doc != nil && a.ui.In.Edge(imkit.KeyF5) {
		a.pending = a.playMap
	}
	if a.ui.In.Ctrl() && a.ui.In.Edge(imkit.KeyQ) {
		a.quit = true
	}

	var maps []string
	if a.doc != nil && a.doc.WAD != nil {
		maps = a.doc.WAD.ListMaps()
	}
	mapItems := make([]imkit.MenuItem, 0, len(maps)+1)
	cur := -1
	if len(maps) == 0 {
		mapItems = append(mapItems, imkit.MenuItem{Label: "(new level — no source WAD)", Disabled: true})
	}
	for i, m := range maps {
		it := imkit.MenuItem{Label: m}
		if m == a.doc.MapName {
			it.Checked, cur = true, i
		}
		mapItems = append(mapItems, it)
	}
	if choice := a.ui.Menu("Map", mapItems); choice >= 0 && choice < len(maps) && choice != cur {
		name := maps[choice]
		a.pending = func() { a.openMap(name) }
	}

	viewItems := []imkit.MenuItem{
		{Label: "Frame map", Shortcut: "Home"},
		{Label: "Grid +", Shortcut: "]"},
		{Label: "Grid -", Shortcut: "["},
		{Label: ""},
		{Label: "Toggle Fullscreen", Shortcut: "F11", Checked: a.fullscreen},
	}
	if a.doc != nil && a.ui.In.Edge(imkit.KeyHome) {
		a.v2.frameMap(a.doc)
	}
	if a.ui.In.Edge(imkit.KeyF11) {
		a.pending = a.toggleFullscreen
	}
	switch a.ui.Menu("View", viewItems) {
	case 0:
		if a.doc != nil {
			a.v2.frameMap(a.doc)
		}
	case 1:
		a.v2.grid = min(a.v2.grid*2, 1024)
	case 2:
		a.v2.grid = max(a.v2.grid/2, 1)
	case 4:
		a.pending = a.toggleFullscreen
	}
}

func (a *App) openMap(name string) {
	if a.doc.WAD == nil {
		a.status = "no source WAD for this level"
		return
	}
	d, err := NewDoc(a.doc.WAD, a.doc.WADPath, name)
	if err != nil {
		a.status = "open " + name + ": " + err.Error()
		return
	}
	a.setDoc(d)
	a.status = fmt.Sprintf("%s  —  %s", filepath.Base(d.WADPath), name)
}

func (a *App) reloadMap() {
	if a.doc.WAD != nil {
		a.openMap(a.doc.MapName)
	}
}

func (a *App) newLevel() {
	a.setDoc(NewEmptyDoc(a.bank, ""))
	a.status = "new level — one 512×512 room, player start at origin.  Press G to draw walls."
}

func (a *App) setDoc(d *Doc) {
	a.doc = d
	a.v2.init(d)
	a.v2.frameMap(d)
	a.v3.reset()
}

func (a *App) saveMap() {
	p := a.doc.DefaultSavePath()
	if err := a.doc.SavePWAD(p); err != nil {
		a.status = "save failed: " + err.Error()
		return
	}
	a.status = "saved  " + p
	log.Printf("editor: saved %s", p)
}

func (a *App) playMap() {
	p, err := a.doc.Play()
	if err != nil {
		a.status = "play: " + err.Error() + "  (wrote " + p + ")"
		return
	}
	a.status = "playtest launched — " + p
	log.Printf("editor: playtest %s", p)
}

// ---- helpers -------------------------------------------------------------

func resolveWAD(arg string) (string, error) {
	if arg != "" {
		if _, err := os.Stat(arg); err != nil {
			return "", fmt.Errorf("%s: %w", arg, err)
		}
		return arg, nil
	}
	for _, dir := range []string{"wad/Game WADs", "wad", "assets"} {
		var found string
		_ = filepath.WalkDir(dir, func(p string, e os.DirEntry, err error) error {
			if err != nil || e.IsDir() || found != "" {
				return nil
			}
			if !strings.EqualFold(filepath.Ext(p), ".wad") {
				return nil
			}
			// only accept a WAD that actually contains at least one map
			if w, err := wad.Load(p); err == nil && len(w.ListMaps()) > 0 {
				found = p
			}
			return nil
		})
		if found != "" {
			return found, nil
		}
	}
	return "", fmt.Errorf("no WAD given and none with maps found under wad/ or assets/")
}

func preferredMap(maps []string) string {
	for _, want := range []string{"MAP01", "E1M1"} {
		for _, m := range maps {
			if strings.EqualFold(m, want) {
				return m
			}
		}
	}
	s := append([]string(nil), maps...)
	sort.Strings(s)
	return s[0]
}
