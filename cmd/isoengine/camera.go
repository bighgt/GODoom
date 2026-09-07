package main

import (
	"math"

	"twopointfive/render/worldgeo"
	"twopointfive/window"
)

// fixedAzimuths are the 8 snapped angles cameraController.Mode == "fixed"
// steps through — the same 45-degree resolution Doom's own sprite art is
// authored at (see raster.SpriteRotation), so a fixed-angle view always
// shows a thing's "as-intended" rotation frame face-on.
var fixedAzimuths = func() []float64 {
	a := make([]float64, 8)
	for i := range a {
		a[i] = float64(i) * math.Pi / 4
	}
	return a
}()

const (
	orbitRotateSpeed  = 2.0   // radians/sec, keyboard rotate (both modes)
	orbitDragSpeed    = 0.006 // radians per pixel of horizontal mouse drag (right button)
	zoomSpeedPerNotch = 0.1   // fraction of current Zoom per scroll notch
	minZoom           = 32.0
	maxZoom           = 1 << 16
)

// cameraController holds the isometric game's live camera plus the small
// amount of input-edge state (mode toggle, fixed-mode step) Poller-style
// polling needs — see engine/game.go's handleMovement and launcher/input.go's
// Poller for the same "edge-detect a key by comparing to last frame" idiom
// this follows. It only rotates/zooms the camera (never tilts it — see
// pollOrbit's own doc comment) — WASD is read by engine.IsoMovementSystem
// for player movement instead (unlike the Phase 1 static viewer, which had
// no player to move and used WASD to pan the camera itself); TargetX/Y/Z is
// instead overwritten every frame by the caller to follow the player (see
// main.go's loop), so this never touches it directly.
type cameraController struct {
	cam worldgeo.IsoCamera

	dragging bool
	dragX    float64

	tabWasDown   bool
	leftWasDown  bool
	rightWasDown bool
}

func newCameraController(cam worldgeo.IsoCamera) *cameraController {
	return &cameraController{cam: cam}
}

// poll advances the controller by dt seconds from win's current input state.
func (c *cameraController) poll(win *window.Window, dt float64) {
	if win.MenuKeyDown(window.MenuKeyTab) {
		if !c.tabWasDown {
			if c.cam.Mode == "fixed" {
				c.cam.Mode = "orbit"
			} else {
				c.cam.Mode = "fixed"
				c.cam.Azimuth = nearestFixedAzimuth(c.cam.Azimuth)
			}
		}
		c.tabWasDown = true
	} else {
		c.tabWasDown = false
	}

	if c.cam.Mode == "fixed" {
		c.pollFixed(win)
	} else {
		c.pollOrbit(win, dt)
	}

	// Zoom (both modes): scroll wheel.
	if _, sy := win.TakeScroll(); sy != 0 {
		c.cam.Zoom *= math.Pow(1-zoomSpeedPerNotch, sy)
		c.cam.Zoom = math.Max(minZoom, math.Min(maxZoom, c.cam.Zoom))
	}
}

// pollOrbit handles free rotate: Left/Right arrows rotate azimuth
// continuously, or hold the right mouse button and drag horizontally (left
// is Doom's fire button — see window.FirePressed/engine's combatSystem —
// so drag-rotate deliberately doesn't share it, or firing a shot would spin
// the camera along with the aim). Elevation is deliberately never touched
// here (or anywhere else in cameraController) — it's fixed at
// worldgeo.IsoTrueElevation for the controller's entire lifetime (set once
// in main.go and restored by the Tab mode-toggle above), so the view stays
// properly isometric always, never tilting up toward edge-on or down toward
// a flat top-down look.
func (c *cameraController) pollOrbit(win *window.Window, dt float64) {
	if win.TurnLeftPressed() {
		c.cam.Azimuth += orbitRotateSpeed * dt
	}
	if win.TurnRightPressed() {
		c.cam.Azimuth -= orbitRotateSpeed * dt
	}

	x, _ := win.CursorPos()
	down := win.MouseRightDown()
	if down && c.dragging {
		c.cam.Azimuth -= (x - c.dragX) * orbitDragSpeed
	}
	c.dragging = down
	c.dragX = x

	c.cam.Azimuth = math.Mod(c.cam.Azimuth, 2*math.Pi)
}

// pollFixed handles the classic snapped-angle mode: Left/Right arrows step
// to the next/previous of the 8 fixedAzimuths, edge-triggered so holding the
// key doesn't spin continuously. Elevation and drag-orbit are inert here.
func (c *cameraController) pollFixed(win *window.Window) {
	left, right := win.TurnLeftPressed(), win.TurnRightPressed()
	if left && !c.leftWasDown {
		c.stepFixedAzimuth(1)
	}
	if right && !c.rightWasDown {
		c.stepFixedAzimuth(-1)
	}
	c.leftWasDown, c.rightWasDown = left, right
}

func (c *cameraController) stepFixedAzimuth(dir int) {
	i := nearestFixedIndex(c.cam.Azimuth)
	i = (i + dir + len(fixedAzimuths)) % len(fixedAzimuths)
	c.cam.Azimuth = fixedAzimuths[i]
}

func nearestFixedIndex(azimuth float64) int {
	azimuth = math.Mod(azimuth, 2*math.Pi)
	if azimuth < 0 {
		azimuth += 2 * math.Pi
	}
	best, bestDist := 0, math.Inf(1)
	for i, a := range fixedAzimuths {
		d := math.Abs(azimuth - a)
		if d > math.Pi {
			d = 2*math.Pi - d
		}
		if d < bestDist {
			best, bestDist = i, d
		}
	}
	return best
}

func nearestFixedAzimuth(azimuth float64) float64 { return fixedAzimuths[nearestFixedIndex(azimuth)] }
