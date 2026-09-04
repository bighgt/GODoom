package engine

import (
	"math"
	"testing"
)

func TestRenderLerpClamps(t *testing.T) {
	g := &Game{}
	g.ticAccum = 0
	if got := g.renderLerp(); got != 0 {
		t.Errorf("ticAccum 0 -> %v, want 0", got)
	}
	g.ticAccum = ticDuration / 2
	if got := g.renderLerp(); math.Abs(got-0.5) > 1e-9 {
		t.Errorf("half a tic -> %v, want 0.5", got)
	}
	g.ticAccum = ticDuration * 3 // a frame that hit maxTicsPerFrame
	if got := g.renderLerp(); got != 1 {
		t.Errorf("overrun -> %v, want clamp to 1", got)
	}
	g.ticAccum = -0.01
	if got := g.renderLerp(); got != 0 {
		t.Errorf("negative -> %v, want clamp to 0", got)
	}
}

func TestShortestAngleDelta(t *testing.T) {
	const eps = 1e-9
	cases := []struct{ from, to, want float64 }{
		{0, math.Pi / 2, math.Pi / 2},
		{0, -math.Pi / 2, -math.Pi / 2},
		// Just under a half turn each way — must take the short arc.
		{0.1, 0.1 + 2*math.Pi - 0.2, -0.2},
		{-3.0, 3.0, -(2*math.Pi - 6.0)},
	}
	for _, c := range cases {
		if got := shortestAngleDelta(c.from, c.to); math.Abs(got-c.want) > eps {
			t.Errorf("shortestAngleDelta(%v,%v) = %v, want %v", c.from, c.to, got, c.want)
		}
	}
	// The interpolated angle never sweeps more than half a turn for any pair.
	for i := 0; i < 360; i += 7 {
		for j := 0; j < 360; j += 13 {
			from, to := float64(i)*math.Pi/180, float64(j)*math.Pi/180
			if d := shortestAngleDelta(from, to); math.Abs(d) > math.Pi+eps {
				t.Fatalf("delta %v for %v->%v exceeds pi", d, from, to)
			}
		}
	}
}

func TestMobjRenderStateLerpAndTeleportSnap(t *testing.T) {
	mo := &Mobj{prevX: 10, prevY: 20, prevZ: 0, prevAngle: 0, X: 30, Y: 20, Z: 8, Angle: math.Pi / 2}

	// Half-way through the tic: half the delta on every axis.
	x, y, z, a := mo.renderState(0.5)
	if x != 20 || y != 20 || z != 4 || math.Abs(a-math.Pi/4) > 1e-9 {
		t.Errorf("mid-tic = (%v,%v,%v,%v), want (20,20,4,pi/4)", x, y, z, a)
	}

	// frac 0 draws the authoritative (post-tic) state, not the stale prev.
	x, y, _, _ = mo.renderState(0)
	if x != mo.X || y != mo.Y {
		t.Errorf("frac 0 = (%v,%v), want current (%v,%v)", x, y, mo.X, mo.Y)
	}

	// A jump larger than interpTeleportDist2 is not interpolated.
	tp := &Mobj{prevX: 0, prevY: 0, X: 900, Y: 900}
	x, y, _, _ = tp.renderState(0.5)
	if x != 900 || y != 900 {
		t.Errorf("teleport lerped to (%v,%v), want snap to (900,900)", x, y)
	}
}
