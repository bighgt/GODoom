package engine

import (
	"fmt"
	"log"
	"math"
	"time"

	"twopointfive/audio"
	"twopointfive/bsp"
	"twopointfive/raster"
	"twopointfive/render"
	"twopointfive/wad"
	"twopointfive/window"
)

// eyeHeight is the classic Doom player eye height above the floor (map units).
const eyeHeight = 41.0

// moveSpeed and turnSpeed are Phase 2's placeholder movement tuning (map
// units/sec and radians/sec); a proper physics/collision-aware System is
// Phase 3 work — see game_design.txt. mouseSensitivity is radians of
// look per pixel of raw mouse motion (see window.Window.MouseDelta).
const (
	moveSpeed        = 300.0
	turnSpeed        = 2.5
	mouseSensitivity = 0.0022
)

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Game is the engine's core object: it owns the window, the GPU renderer
// (held as the render.Renderer interface — see game_design.txt section 6),
// the CPU software rasterizer that actually draws the level (package
// raster), the loaded WAD/level/BSP tree, the camera, and every live Entity
// and System. Run drives the main loop.
type Game struct {
	Window   *window.Window
	Renderer render.Renderer
	Raster   *raster.Renderer

	WAD   *wad.WAD
	Level *wad.Level
	BSP   bsp.Node

	Camera raster.Camera
	Player raster.PlayerStats
	Weapon WeaponState

	// AudioDevice plays one-shot sound effects (door open/close, weapon
	// fire); Music streams the level's background track. Both are nil-safe
	// (playSound/Music.Play no-op) so a Game still runs with sound
	// unavailable. Set by the caller after NewGame (see cmd/engine/main.go)
	// rather than threading two more constructor parameters through it.
	AudioDevice *audio.Device
	Music       *audio.MusicPlayer
	soundCache  map[string]*wad.Sound

	// ShowHUD toggles the debug text overlay (FPS, position, angle) drawn
	// each frame by Run — on by default. The real status bar (health,
	// ammo, face, ...) is drawn unconditionally; it's the game's actual
	// HUD, not a debug aid.
	ShowHUD bool
	fps     float32 // exponential moving average, smoothed in Run

	doorThinkers map[int]*doorThinker // by Sector index; see doors.go
	spaceWasDown bool                 // for edge-detecting the "use" key
	fireWasDown  bool                 // for edge-detecting the fire button
	projectiles  []*Projectile        // in-flight shots; see projectiles.go

	entities []Entity
	systems  []System
	nextID   uint32
}

// NewGame wires together an already-created window, GPU renderer, CPU
// rasterizer, loaded level, and starting camera into a running Game.
func NewGame(win *window.Window, renderer render.Renderer, ras *raster.Renderer, w *wad.WAD, level *wad.Level, tree bsp.Node, cam raster.Camera) *Game {
	return &Game{
		Window:   win,
		Renderer: renderer,
		Raster:   ras,
		WAD:      w,
		Level:    level,
		BSP:      tree,
		Camera:   cam,
		Player:   raster.DefaultPlayerStats(),
		Weapon:   NewWeaponState(),
		ShowHUD:  true,
	}
}

// AddEntity registers an entity with the game.
func (g *Game) AddEntity(e Entity) {
	g.entities = append(g.entities, e)
}

// AddSystem registers a system. Systems run every frame in the order they
// were added.
func (g *Game) AddSystem(s System) {
	g.systems = append(g.systems, s)
}

// NextEntityID returns a fresh, game-unique entity ID.
func (g *Game) NextEntityID() uint32 {
	g.nextID++
	return g.nextID
}

// Run drives the main loop until the window is closed: poll input, move the
// camera, run every System, update every Entity, rasterize a frame on the
// CPU, and present it via the GPU renderer. Returns cleanly on window close.
func (g *Game) Run() error {
	last := time.Now()

	for !g.Window.ShouldClose() {
		now := time.Now()
		dt := float32(now.Sub(last).Seconds())
		last = now

		g.Window.PollEvents()
		if g.Window.EscapePressed() {
			g.Window.RequestClose()
		}
		g.handleMovement(dt)

		spaceDown := g.Window.UsePressed()
		if spaceDown && !g.spaceWasDown {
			g.tryUseDoor()
		}
		g.spaceWasDown = spaceDown
		g.updateDoors(float64(dt))

		for i := 1; i <= len(Weapons); i++ {
			if g.Window.WeaponKeyPressed(i) {
				g.SwitchWeapon(i - 1)
			}
		}
		fireDown := g.Window.FirePressed()
		if fireDown && !g.fireWasDown {
			g.FireWeapon()
		}
		g.fireWasDown = fireDown
		g.updateWeapon(float64(dt))
		g.updateProjectiles(float64(dt))

		for _, s := range g.systems {
			s.Update(g, dt)
		}
		for _, e := range g.entities {
			e.Update(dt)
		}

		if dt > 0 {
			// Exponential moving average: smooths out frame-to-frame jitter
			// without needing a rolling sample window.
			const smoothing = 0.9
			g.fps = g.fps*smoothing + (1/dt)*(1-smoothing)
		}

		g.Raster.Render(g.Level, g.BSP, g.Camera)
		for _, p := range g.projectiles {
			g.Raster.DrawBillboard(g.Camera, p.X, p.Y, p.Z, projectileBillboardSize, projectileColor)
		}
		def := Weapons[g.Weapon.Current]
		g.Raster.DrawWeapon(def.SpritePrefix, def.FlashPrefix, g.Weapon.phase == weaponFiring, g.Weapon.Offset)
		g.Raster.DrawStatusBar(g.Player)
		if g.ShowHUD {
			g.Raster.DrawHUD([]string{
				fmt.Sprintf("%s  FPS %.0f", g.Level.Name, g.fps),
				fmt.Sprintf("XYZ %.0f %.0f %.0f", g.Camera.X, g.Camera.Y, g.Camera.Z),
				fmt.Sprintf("ANGLE %.0f PITCH %.0f", g.Camera.Angle*180/math.Pi, g.Camera.Pitch*180/math.Pi),
			})
		}
		if err := g.Renderer.DrawFrame(g.Raster.Pix); err != nil {
			return fmt.Errorf("engine: draw frame: %w", err)
		}
	}

	log.Println("engine: window closed, shutting down")
	return nil
}

// handleMovement is Phase 2's placeholder input scheme: WASD relative to
// facing, Left/Right arrows to turn, and the camera's eye height kept
// pinned to floorHeight+eyeHeight for whatever Sector it's currently in
// (via a BSP point-location query) — enough to walk around and confirm the
// level renders correctly, without yet being real physics or collision.
func (g *Game) handleMovement(dt float32) {
	w := g.Window
	if w.TurnLeftPressed() {
		g.Camera.Angle += turnSpeed * float64(dt)
	}
	if w.TurnRightPressed() {
		g.Camera.Angle -= turnSpeed * float64(dt)
	}

	if mdx, mdy := w.MouseDelta(); mdx != 0 || mdy != 0 {
		// Mouse moving right turns the view right, which is a *decreasing*
		// Camera.Angle (Angle increases counter-clockwise/turning left, the
		// same convention the Left/Right arrow keys above use). Mouse
		// moving up (GLFW's y decreases upward on screen) looks up, which
		// is an *increasing* Camera.Pitch — see raster.Camera.Pitch.
		g.Camera.Angle -= mdx * mouseSensitivity
		g.Camera.Pitch -= mdy * mouseSensitivity
		g.Camera.Pitch = clamp(g.Camera.Pitch, -raster.MaxPitch, raster.MaxPitch)
	}

	fx, fy := math.Cos(g.Camera.Angle), math.Sin(g.Camera.Angle)
	rx, ry := math.Sin(g.Camera.Angle), -math.Cos(g.Camera.Angle)

	dx, dy := 0.0, 0.0
	if w.ForwardPressed() {
		dx += fx
		dy += fy
	}
	if w.BackPressed() {
		dx -= fx
		dy -= fy
	}
	if w.StrafeRightPressed() {
		dx += rx
		dy += ry
	}
	if w.StrafeLeftPressed() {
		dx -= rx
		dy -= ry
	}
	if dx != 0 || dy != 0 {
		n := math.Hypot(dx, dy)
		g.tryMove(dx/n*moveSpeed*float64(dt), dy/n*moveSpeed*float64(dt))
	}

	if sec := bsp.PointSector(g.BSP, g.Level, float32(g.Camera.X), float32(g.Camera.Y)); sec != nil {
		g.Camera.Z = float64(sec.FloorHeight) + eyeHeight
	}
}
