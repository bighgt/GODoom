package engine

import (
	"math"
	"testing"

	"twopointfive/raster"
	"twopointfive/wad"
)

// A single vertical one-sided wall on the Y axis, from (0,-100) to (0,100).
// No BSP: canMoveTo then reduces to the nearby-line scan (PointSector on a
// nil tree returns nil, so the sector step-up check is skipped), which is
// all these cases exercise.
func wallOnlyGame() *Game {
	lvl := &wad.Level{
		Vertexes: []wad.Vertex{{X: 0, Y: -100}, {X: 0, Y: 100}},
		Linedefs: []wad.Linedef{{StartVertex: 0, EndVertex: 1, FrontSidedef: 0, BackSidedef: wad.NoSidedef}},
	}
	g := &Game{Level: lvl}
	g.blockGrid = buildCollisionGrid(lvl)
	return g
}

func TestLineBlocksPlayer(t *testing.T) {
	if !lineBlocksPlayer(wallOpening{}, true, 0) {
		t.Error("one-sided line should always block")
	}
	if !lineBlocksPlayer(wallOpening{}, false, 0) {
		t.Error("one-sided line should block even when the box isn't straddling")
	}
	tall := wallOpening{twoSided: true, bottom: 0, top: 200}
	if lineBlocksPlayer(tall, false, 0) {
		t.Error("two-sided line the box doesn't straddle should not block")
	}
	if lineBlocksPlayer(tall, true, 0) {
		t.Error("straddled two-sided line with a tall flush opening should not block")
	}
	if !lineBlocksPlayer(wallOpening{twoSided: true, bottom: 40, top: 200}, true, 0) {
		t.Error("straddled two-sided line with a 40u step up (> maxStepUp) should block")
	}
	if !lineBlocksPlayer(wallOpening{twoSided: true, bottom: 0, top: 40}, true, 0) {
		t.Error("straddled two-sided line with a 40u opening (< playerHeight) should block")
	}
	if lineBlocksPlayer(wallOpening{twoSided: true, bottom: 20, top: 200}, true, 0) {
		t.Error("straddled two-sided line with a 20u step up (<= maxStepUp) should not block")
	}
}

func TestPlayerWallClearance(t *testing.T) {
	g := wallOnlyGame()
	if c := g.playerWallClearance(50, 0, 0); !math.IsInf(c, 1) {
		t.Errorf("50u from the wall (> playerRadius): clearance %v, want +Inf", c)
	}
	if c := g.playerWallClearance(10, 0, 0); math.Abs(c-10) > 1e-9 {
		t.Errorf("10u from the wall: clearance %v, want 10", c)
	}
	if c := g.playerWallClearance(4, 0, 0); math.Abs(c-4) > 1e-9 {
		t.Errorf("4u from the wall (more wedged): clearance %v, want 4", c)
	}
}

func TestStepMoveRecoversWhenWedged(t *testing.T) {
	g := wallOnlyGame()
	// Wedged: centre 10u from a one-sided wall — inside playerRadius, so the
	// normal clip refuses every direction.
	g.Camera = raster.Camera{X: 10, Y: 0}
	if g.canMoveTo(g.Camera.X, g.Camera.Y, 0) {
		t.Fatal("test setup: player at x=10 should fail the clip")
	}

	// Pushing away from the wall frees them.
	for i := 0; i < 20 && !g.canMoveTo(g.Camera.X, g.Camera.Y, 0); i++ {
		g.tryMove(6, 0)
	}
	if !g.canMoveTo(g.Camera.X, g.Camera.Y, 0) {
		t.Fatalf("still wedged after pushing away from the wall (x=%.1f)", g.Camera.X)
	}
	if g.Camera.X < playerRadius {
		t.Errorf("freed but still inside the wall radius (x=%.1f, want >= %.0f)", g.Camera.X, playerRadius)
	}

	// From a wedged spot, pushing further INTO the wall must not crawl deeper.
	g.Camera = raster.Camera{X: 10, Y: 0}
	before := g.Camera.X
	g.tryMove(-6, 0)
	if g.Camera.X < before-1e-9 {
		t.Errorf("recovery let the player crawl deeper into the wall: x %.3f -> %.3f", before, g.Camera.X)
	}
}
