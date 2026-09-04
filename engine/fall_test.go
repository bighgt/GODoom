package engine

import (
	"math"
	"testing"
)

const tic = 1.0 / ticsPerSecond

func TestFallConstantsMatchVanilla(t *testing.T) {
	// GRAVITY = 1 unit/tic² -> 1225 unit/sec²; the kick is 2 units/tic; the
	// hard-landing threshold is 8 units/tic.
	if gravityAccel != 1225 {
		t.Errorf("gravityAccel = %v, want 1225 (1 unit/tic²)", gravityAccel)
	}
	if fallStartKick != 70 {
		t.Errorf("fallStartKick = %v, want 70 (2 units/tic)", fallStartKick)
	}
	if hardLandingSpeed != 280 {
		t.Errorf("hardLandingSpeed = %v, want 280 (8 units/tic)", hardLandingSpeed)
	}
}

func TestFallStepLeaveLedge(t *testing.T) {
	// Feet at 128 over a floor at 0, standing still, on the ground: the
	// first frame off the edge doesn't move (vanilla applies gravity after
	// the z-move within a tic) but arms the -GRAVITY*2 kick.
	nz, nvz, ground, hard := fallStep(128, 0, true, 0, 1000, tic)
	if nz != 128 {
		t.Errorf("first airborne frame moved to %v, want 128 (no move yet)", nz)
	}
	if nvz != -fallStartKick {
		t.Errorf("VelZ after leaving the ledge = %v, want %v", nvz, -fallStartKick)
	}
	if ground || hard {
		t.Errorf("ground=%v hard=%v, want false/false", ground, hard)
	}
}

func TestFallStepGravityAccumulates(t *testing.T) {
	// Already falling: descend by VelZ*dt, then VelZ gains playerFallGravity*dt
	// (softer than vanilla gravityAccel — see the constant).
	nz, nvz, ground, _ := fallStep(100, -70, false, 0, 1000, tic)
	if math.Abs(nz-(100-70*tic)) > 1e-9 {
		t.Errorf("nz = %v, want %v", nz, 100-70*tic)
	}
	if math.Abs(nvz-(-70-playerFallGravity*tic)) > 1e-9 {
		t.Errorf("nvz = %v, want %v", nvz, -70-playerFallGravity*tic)
	}
	if playerFallGravity >= gravityAccel {
		t.Errorf("playerFallGravity %v should be softer than vanilla %v", playerFallGravity, gravityAccel)
	}
	if ground {
		t.Error("reported grounded while still above the floor")
	}
}

func TestFallStepLanding(t *testing.T) {
	// A gentle landing: overshoots the floor, snaps to it, zeroes VelZ, no oof.
	nz, nvz, ground, hard := fallStep(1, -100, false, 0, 1000, tic)
	if nz != 0 || nvz != 0 || !ground {
		t.Errorf("soft landing: nz=%v nvz=%v ground=%v, want 0/0/true", nz, nvz, ground)
	}
	if hard {
		t.Error("100 u/s landing flagged as hard (< 280)")
	}
	// A fast landing grunts.
	if _, _, _, hard := fallStep(1, -400, false, 0, 1000, tic); !hard {
		t.Error("400 u/s landing not flagged as hard")
	}
}

func TestFallStepStepUp(t *testing.T) {
	// Grounded, the floor under (x,y) is now 16 higher (walked onto a step):
	// snap straight up, still grounded, not a hard landing.
	nz, nvz, ground, hard := fallStep(0, 0, true, 16, 1000, tic)
	if nz != 16 || nvz != 0 || !ground || hard {
		t.Errorf("step-up: nz=%v nvz=%v ground=%v hard=%v, want 16/0/true/false", nz, nvz, ground, hard)
	}
}

func TestFallStepHeadBump(t *testing.T) {
	// Rising (a lift pushing up), a small dt so gravity doesn't flip VelZ
	// first: the head hits the ceiling, feet clamp under it, upward VelZ dies.
	head := 56.0 - playerHeight // ceil 56, playerHeight 56 -> head room 0
	nz, nvz, _, _ := fallStep(0, 100, false, -100, 56, 0.001)
	if nz != head {
		t.Errorf("head-bumped feet at %v, want %v", nz, head)
	}
	if nvz != 0 {
		t.Errorf("upward VelZ %v not killed by the ceiling", nvz)
	}
}

// Integrating the fall: it takes several frames (never instant), and a
// runner clears twice the horizontal ground a walker does because no
// friction acts while airborne.
func TestLedgeBoost(t *testing.T) {
	// A real run-up gets the commit kick...
	vx, vy := ledgeBoosted(400, 0)
	if math.Abs(vx-400*ledgeLaunchBoost) > 1e-9 || vy != 0 {
		t.Errorf("ledgeBoosted(400,0) = (%.1f,%.1f), want (%.1f,0)", vx, vy, 400*ledgeLaunchBoost)
	}
	if ledgeLaunchBoost <= 1 {
		t.Errorf("ledgeLaunchBoost = %v, must be > 1 to extend the jump", ledgeLaunchBoost)
	}
	// ...a slow step-off doesn't.
	if bx, by := ledgeBoosted(40, 20); bx != 40 || by != 20 {
		t.Errorf("slow step-off got boosted: (%.1f,%.1f)", bx, by)
	}
	// Direction is preserved, magnitude scales.
	sx, sy := ledgeBoosted(120, -160) // |v| = 200 >= min
	if got := math.Hypot(sx, sy); math.Abs(got-200*ledgeLaunchBoost) > 1e-6 {
		t.Errorf("boosted magnitude %.1f, want %.1f", got, 200*ledgeLaunchBoost)
	}
	if math.Abs(sx/sy-(120.0/-160.0)) > 1e-9 {
		t.Error("ledge boost changed the run direction")
	}
}

// The whole point of the two knobs: a run-off now clears materially more
// ground than the plain vanilla-gravity, no-boost arc did. Closed-form
// horizontal reach for a drop of h at horizontal speed v under gravity g,
// starting the fall at the -fallStartKick kick: t solves
// h = kick*t + g*t^2/2, and reach = v*t.
func TestJumpDistanceIncreasedOverVanilla(t *testing.T) {
	reach := func(h, v, g float64) float64 {
		k := fallStartKick
		tt := (-k + math.Sqrt(k*k+2*g*h)) / g
		return v * tt
	}
	const h, runSpeed = 128.0, 600.0
	vanilla := reach(h, runSpeed, gravityAccel)
	improved := reach(h, runSpeed*ledgeLaunchBoost, playerFallGravity)
	t.Logf("run-off gap from %.0fu: vanilla %.0f -> improved %.0f (%.2fx)", h, vanilla, improved, improved/vanilla)
	if improved <= vanilla*1.3 {
		t.Errorf("run-off reach only %.2fx vanilla, want >= 1.3x", improved/vanilla)
	}
}

func TestJumpArcRisesClearsStepAndLands(t *testing.T) {
	// A jump (Space) sets VelZ = jumpImpulse from the ground; fallStep then
	// runs the arc. It must rise, clear more than a maxStepUp ledge at the
	// apex, and come back down to land.
	feetZ, velZ, onGround := 0.0, jumpImpulse, false
	apex := 0.0
	landed := false
	for frame := 0; frame < 2000; frame++ {
		var ground bool
		feetZ, velZ, ground, _ = fallStep(feetZ, velZ, onGround, 0, 4000, tic)
		onGround = ground
		if feetZ > apex {
			apex = feetZ
		}
		if ground && frame > 2 {
			landed = true
			break
		}
	}
	if !landed {
		t.Fatalf("jump never landed (apex %.1f, feet %.1f)", apex, feetZ)
	}
	if apex <= maxStepUp {
		t.Errorf("jump apex %.1f <= maxStepUp %.0f — a jump should clear a step", apex, maxStepUp)
	}
	if apex > 120 {
		t.Errorf("jump apex %.1f absurdly high — check jumpImpulse/playerFallGravity", apex)
	}
	if math.Abs(feetZ) > 1e-6 {
		t.Errorf("after landing feet at %.3f, want 0", feetZ)
	}
}

func TestFallArcRunnerTravelsFurther(t *testing.T) {
	fallDistance := func(hSpeed float64) (frames int, horiz float64) {
		feetZ, velZ, onGround := 160.0, 0.0, true
		for {
			var ground bool
			feetZ, velZ, ground, _ = fallStep(feetZ, velZ, onGround, 0, 4000, tic)
			onGround = ground
			if ground {
				return frames, horiz
			}
			horiz += hSpeed * tic
			frames++
			if frames > 1000 {
				t.Fatal("fall never landed")
			}
		}
	}

	walkFrames, walkX := fallDistance(150) // ~walk speed
	runFrames, runX := fallDistance(300)   // ~run speed

	if walkFrames < 5 {
		t.Errorf("fall from 160 landed in %d frames — too close to instant", walkFrames)
	}
	if walkFrames != runFrames {
		t.Errorf("fall time changed with horizontal speed (%d vs %d) — it shouldn't", walkFrames, runFrames)
	}
	if ratio := runX / walkX; ratio < 1.9 || ratio > 2.1 {
		t.Errorf("runner cleared %.0f vs walker %.0f (ratio %.2f), want ~2x", runX, walkX, ratio)
	}
	// Rough sanity on the arc: free-fall time from 160u under the softened
	// playerFallGravity is ~0.55s; allow slack for the wasted first frame
	// + the kick.
	if secs := float64(walkFrames) * tic; secs < 0.4 || secs > 0.95 {
		t.Errorf("fall from 160u took %.2fs, expected ~0.55s", secs)
	}
}
