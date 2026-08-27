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

// turnSpeed (keyboard turning, radians/sec) and mouseSensitivity (radians
// of look per pixel of raw mouse motion — see window.Window.MouseDelta)
// are this project's own tuning; turning in vanilla Doom isn't momentum-
// based, so there's nothing to port for it. Translational movement below
// is momentum-based, ported from PrBoom's p_mobj.c/p_user.c.
const (
	turnSpeed        = 2.5
	mouseSensitivity = 0.0022
)

// Movement momentum, ported from PrBoom's p_mobj.c (P_XYMovement) and
// p_user.c (P_MovePlayer/P_Thrust): id's own engine never sets a player's
// velocity directly. Instead, holding a direction key adds a fixed thrust
// to it every tic, and a multiplicative friction is applied every tic
// regardless — the two settle at whatever speed makes them cancel out,
// which is how Doom's movement gets its slight "spin-up" on starting and
// "slide to a stop" on releasing instead of the instant-velocity feel a
// naive implementation has.
const (
	// moveTopSpeed is the steady-state speed (map units/sec) holding a
	// direction reaches once thrust and friction settle — this project's
	// own choice of "how fast is fast," not a ported value.
	moveTopSpeed = 300.0
	// frictionPerTic is id's ORIG_FRICTION (0xE800 in 16.16 fixed point =
	// 59392/65536), applied multiplicatively to momentum every 1/35s tic.
	frictionPerTic = 59392.0 / 65536.0
	// stopSpeed is id's STOPSPEED (0x1000 fixed point = 4096/65536 map
	// units/tic, converted to units/sec): below this, momentum snaps to
	// zero instead of asymptotically approaching it forever.
	stopSpeed = (4096.0 / 65536.0) * ticsPerSecond
	// thrustAccel (units/sec²) is derived, not ported: the acceleration
	// that makes holding a direction key settle at exactly moveTopSpeed
	// once thrust and per-tic friction reach equilibrium (v = v*friction +
	// thrustPerTic  =>  thrustPerTic = v*(1-friction), converted from
	// per-tic to per-second).
	thrustAccel = moveTopSpeed * (1 - frictionPerTic) * ticsPerSecond
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
	// VelX/VelY is the player's current horizontal momentum (map
	// units/sec) — see the movement-momentum constants above and
	// handleMovement below.
	VelX, VelY float64
	Player     raster.PlayerStats
	Weapon     WeaponState

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
		// Held, not edge-triggered: real Doom refires every weapon
		// automatically for as long as the fire button stays down, at
		// whatever cadence that weapon's own animation allows —
		// FireWeapon already no-ops unless the weapon is fully back in
		// its ready state (weaponReady), so this alone reproduces each
		// gun's actual rate of fire (e.g. the chaingun's ~4.4 rounds/sec
		// from its 8-tic cycle) instead of capping every weapon at one
		// shot per mouse click.
		if g.Window.FirePressed() {
			g.FireWeapon()
		}
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
		// Only the three true-projectile weapons (Rocket Launcher, Plasma
		// Rifle, BFG9000) ever populate g.projectiles at all — the three
		// hitscan weapons never spawn one, so this loop simply does
		// nothing for them, matching how instant a real bullet is.
		for _, p := range g.projectiles {
			if name, ok := p.CurrentSprite(); ok {
				g.Raster.DrawWorldSprite(g.Camera, p.X, p.Y, p.Z, p.SizeMultiplier(), name)
			}
		}
		def := Weapons[g.Weapon.Current]
		gunFrame, flashFrame, hasFlash := g.currentSprite()
		g.Raster.DrawWeapon(def.SpritePrefix, gunFrame, def.FlashPrefix, flashFrame, hasFlash, g.Weapon.Offset)
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
	// Keep Angle bounded to (-2π, 2π] rather than letting it accumulate
	// without limit over a long session — sin/cos handle a large argument
	// correctly, but there's no reason to let it grow forever either.
	g.Camera.Angle = math.Mod(g.Camera.Angle, 2*math.Pi)

	fx, fy := math.Cos(g.Camera.Angle), math.Sin(g.Camera.Angle)
	rx, ry := math.Sin(g.Camera.Angle), -math.Cos(g.Camera.Angle)

	// Thrust: add to momentum rather than set it, exactly like id's own
	// P_Thrust — see the movement-momentum constants above.
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
		g.VelX += dx / n * thrustAccel * float64(dt)
		g.VelY += dy / n * thrustAccel * float64(dt)
	}

	// Friction: applied every frame regardless of input, converting id's
	// per-tic multiplicative decay (frictionPerTic every 1/35s) into
	// continuous time so it's frame-rate independent.
	decay := math.Pow(frictionPerTic, float64(dt)*ticsPerSecond)
	g.VelX *= decay
	g.VelY *= decay
	if math.Hypot(g.VelX, g.VelY) < stopSpeed {
		g.VelX, g.VelY = 0, 0
	}

	g.tryMove(g.VelX*float64(dt), g.VelY*float64(dt))

	if sec := bsp.PointSector(g.BSP, g.Level, float32(g.Camera.X), float32(g.Camera.Y)); sec != nil {
		g.Camera.Z = float64(sec.FloorHeight) + eyeHeight
	}
}
