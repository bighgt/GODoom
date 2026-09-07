package engine

import "math"

// minIsoAimDist is how close Game.IsoAimX/Y may sit to the player before
// IsoMovementSystem stops updating Camera.Angle from it — atan2 of a
// near-zero vector is numerically unstable (the facing would jitter
// wildly), and it's also just not meaningful for the mouse to be sitting
// right on top of the player's own screen position.
const minIsoAimDist = 4.0

// IsoMovementSystem is movementSystem's isometric-viewpoint counterpart:
// the exact same momentum/friction/collision resolution handleMovement
// uses (ported from PrBoom's P_Thrust/P_XYMovement — see the constants
// above handleMovement's own doc comment), but the thrust basis comes from
// Game.IsoCameraAzimuth instead of Camera.Angle, and Camera.Angle itself
// (the player's facing, read by weapon fire and sprite/rotation selection)
// turns to face Game.IsoAimX/Y instead of following mouse-look deltas.
// Jump, the "wedged" recovery, and vertical resolution are identical to
// handleMovement's.
//
// A Game defaults to first-person movementSystem (see NewGame); an
// isometric front end swaps it in via game.ReplaceMovementSystem(engine.
// IsoMovementSystem{}) and updates Game.IsoCameraAzimuth/IsoAimX/IsoAimY
// itself each frame before calling Game.AdvanceSimulation.
type IsoMovementSystem struct{}

func (IsoMovementSystem) Name() string { return "iso-movement" }

func (IsoMovementSystem) Update(g *Game, dt float32) {
	w := g.Window

	if dx, dy := g.IsoAimX-g.Camera.X, g.IsoAimY-g.Camera.Y; math.Hypot(dx, dy) >= minIsoAimDist {
		g.Camera.Angle = math.Atan2(dy, dx)
	}
	// runTic mirrors Camera.Angle onto playerMobj.Angle once per 35Hz
	// simulation tic (see its own doc comment) — fine for the FPS path,
	// where a whole render frame's worth of turning happens between tics
	// anyway, but combatSystem (which fires this same frame, reading
	// playerMobj.Angle for fireHitscan/spawnProjectile's direction) runs
	// before stepSimulation ever gets there. Without this, a shot fired the
	// instant the mouse snaps onto a new target can go out up to a couple
	// of frames stale — sync it here too so aim-then-click is exact.
	if g.playerMobj != nil {
		g.playerMobj.Angle = g.Camera.Angle
	}

	if g.playerDead {
		g.VelX, g.VelY = 0, 0
		g.resolvePlayerZ(float64(dt))
		return
	}

	hudKey := w.HUDCyclePressed()
	if hudKey && !g.hudKeyDown {
		g.cycleHUD()
	}
	g.hudKeyDown = hudKey

	jumpDown := w.JumpPressed()
	if jumpDown && !g.jumpWasDown && g.onGround {
		g.VelZ = jumpImpulse
		g.onGround = false
	}
	g.jumpWasDown = jumpDown

	wedged := !g.canMoveTo(g.Camera.X, g.Camera.Y, g.PlayerZ)

	// Thrust basis: screen-relative to the camera's azimuth, not the
	// player's own facing (Camera.Angle) — the whole point of decoupling
	// movement from aim in an isometric view.
	fx, fy := math.Cos(g.IsoCameraAzimuth), math.Sin(g.IsoCameraAzimuth)
	rx, ry := math.Sin(g.IsoCameraAzimuth), -math.Cos(g.IsoCameraAzimuth)

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
	if (dx != 0 || dy != 0) && (g.onGround || wedged) {
		accel := thrustAccel
		if w.RunPressed() != g.AlwaysRun {
			accel *= runMultiplier
		}
		n := math.Hypot(dx, dy)
		g.VelX += dx / n * accel * float64(dt)
		g.VelY += dy / n * accel * float64(dt)
	}

	if g.onGround || wedged {
		decay := math.Pow(frictionPerTic, float64(dt)*ticsPerSecond)
		g.VelX *= decay
		g.VelY *= decay
		if math.Hypot(g.VelX, g.VelY) < stopSpeed {
			g.VelX, g.VelY = 0, 0
		}
	}

	g.tryMove(g.VelX*float64(dt), g.VelY*float64(dt))
	g.resolvePlayerZ(float64(dt))
}

// ReplaceMovementSystem swaps out the default first-person movementSystem
// (added first by NewGame, before use/combat/cheat/flashlight) for s — for
// an alternate front end (cmd/isoengine) that wants Game's simulation
// (physics, monster AI, sector specials, combat) but drives player thrust/
// facing from a different input scheme (see IsoMovementSystem). s runs in
// movementSystem's original slot, so ordering among the other systems is
// unchanged. A no-op if called before NewGame has registered any systems.
func (g *Game) ReplaceMovementSystem(s System) {
	if len(g.systems) > 0 {
		g.systems[0] = s
	}
}
