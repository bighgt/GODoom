package engine

import (
	"math"
	"testing"
)

func TestOpposite(t *testing.T) {
	if opposite(diEast) != diWest || opposite(diWest) != diEast {
		t.Error("E/W opposite wrong")
	}
	if opposite(diNorthEast) != diSouthWest {
		t.Error("NE opposite should be SW")
	}
	if opposite(diNoDir) != diNoDir {
		t.Error("opposite(NODIR) should be NODIR")
	}
}

func TestNormAngle(t *testing.T) {
	for _, c := range []struct{ in, want float64 }{
		{0, 0},
		{math.Pi, math.Pi},
		{3 * math.Pi, math.Pi},
		{-3 * math.Pi / 2, math.Pi / 2},
	} {
		if got := normAngle(c.in); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("normAngle(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestSegCross(t *testing.T) {
	// A: (0,0)->(10,0) horizontal. B: (5,-5)->(5,5) vertical. Cross at t=0.5.
	hit, tv := segCross(0, 0, 10, 0, 5, -5, 5, 5)
	if !hit || math.Abs(tv-0.5) > 1e-9 {
		t.Errorf("crossing: hit=%v t=%v want true,0.5", hit, tv)
	}
	if hit, _ := segCross(0, 0, 10, 0, 0, 1, 10, 1); hit {
		t.Error("parallel segments reported crossing")
	}
	if hit, _ := segCross(0, 0, 10, 0, 20, -5, 20, 5); hit {
		t.Error("non-overlapping segments reported crossing")
	}
}

func TestTurnToMoveDir(t *testing.T) {
	const step = math.Pi / 4
	// Facing east (0), MoveDir west (4): should step one 45° increment
	// toward pi, not snap the whole way.
	mo := &Mobj{Angle: 0, MoveDir: diWest}
	turnToMoveDir(mo)
	if d := math.Abs(normAngle(mo.Angle - step)); d > 1e-6 && math.Abs(normAngle(mo.Angle+step)) > 1e-6 {
		t.Errorf("west turn from east: angle %v, want ±45°", mo.Angle)
	}
	// Already aligned: unchanged (to a 45° multiple).
	mo = &Mobj{Angle: diNorth * step, MoveDir: diNorth}
	turnToMoveDir(mo)
	if math.Abs(normAngle(mo.Angle-diNorth*step)) > 1e-6 {
		t.Errorf("aligned north: angle moved to %v", mo.Angle)
	}
	// Stuck: no change.
	mo = &Mobj{Angle: 1.234, MoveDir: diNoDir}
	turnToMoveDir(mo)
	if mo.Angle != 1.234 {
		t.Errorf("DI_NODIR should not turn; angle = %v", mo.Angle)
	}
	// Repeated calls converge onto the target heading.
	mo = &Mobj{Angle: 0, MoveDir: diSouth}
	for i := 0; i < 8; i++ {
		turnToMoveDir(mo)
	}
	if math.Abs(normAngle(mo.Angle-diSouth*step)) > 1e-6 {
		t.Errorf("after 8 steps toward south: angle %v want %v", mo.Angle, diSouth*step)
	}
}

// pNewChaseDir with an unobstructed path should head straight at the target.
func TestNewChaseDirHeadsTowardTarget(t *testing.T) {
	newGame := func(px, py float64) (*Game, *Mobj) {
		g := &Game{}
		g.playerMobj = &Mobj{X: px, Y: py, Health: 100, Flags: MF_SOLID | MF_SHOOTABLE, Radius: 16, Height: 56}
		mo := &Mobj{X: 0, Y: 0, Info: &mobjInfo[MT_TROOP], Flags: monsterFlags, Radius: 20, Height: 56, MoveDir: diNoDir}
		mo.Target = g.playerMobj
		return g, mo
	}
	cases := []struct {
		px, py float64
		want   int
	}{
		{500, 0, diEast},
		{-500, 0, diWest},
		{0, 500, diNorth},
		{0, -500, diSouth},
		{500, 500, diNorthEast},
		{-500, -500, diSouthWest},
	}
	for _, c := range cases {
		g, mo := newGame(c.px, c.py)
		g.pNewChaseDir(mo)
		if mo.MoveDir != c.want {
			t.Errorf("target (%g,%g): MoveDir %d, want %d", c.px, c.py, mo.MoveDir, c.want)
		}
	}
}
