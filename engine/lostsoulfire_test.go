package engine

import (
	"math"
	"testing"
)

// A floating lost soul sheds flames from behind its skull (away from its
// face) and rising; a dead one, or the feature off, sheds none.
func TestLostSoulFireTrailsBehind(t *testing.T) {
	g := &Game{lostSoulFire: true}
	soul := g.P_SpawnMobj(0, 0, 40, MT_SKULL)
	soul.Angle = 0 // facing +X
	for i := 0; i < 50; i++ {
		g.updateMobjFire(0.01)
	}
	if n := len(g.particles); n < 23 || n > 27 {
		t.Fatalf("0.5s floating shed %d licks, want ~25", n)
	}
	for _, p := range g.particles {
		if p.X > 0 || p.DX > 10 || p.DZ <= 0 || p.Z < soul.Z+soulFireUp-4 {
			t.Fatalf("lick at (%.1f, %.1f) moving (%.1f, %.1f): want behind (-X) the skull and rising", p.X, p.Z, p.DX, p.DZ)
		}
	}
	if _, ok := g.lostSoulLight(soul, soul.X, soul.Y, soul.Z); !ok {
		t.Error("a burning lost soul casts no light")
	}

	// Charging: denser, and streaming opposite its flight (it flies +Y).
	g.particles = nil
	soul.Flags |= MF_SKULLFLY
	soul.MomX, soul.MomY = 0, 20
	for i := 0; i < 50; i++ {
		g.updateMobjFire(0.01)
	}
	if n := len(g.particles); n < 60 {
		t.Errorf("0.5s charging shed %d licks, want a thicker trail (~70)", n)
	}
	for _, p := range g.particles {
		if p.Y > 0 || math.Abs(p.X) > 6 {
			t.Fatalf("charging lick at (%.1f, %.1f): want behind its flight (-Y)", p.X, p.Y)
		}
	}

	// Dead, or off: nothing.
	soul.Flags &^= MF_SKULLFLY
	soul.Health = 0
	g.particles = nil
	g.updateMobjFire(0.5)
	off := &Game{}
	off.P_SpawnMobj(0, 0, 40, MT_SKULL)
	off.updateMobjFire(0.5)
	if len(g.particles)+len(off.particles) != 0 {
		t.Errorf("dead soul / feature off shed %d licks", len(g.particles)+len(off.particles))
	}
	if _, ok := g.lostSoulLight(soul, 0, 0, 0); ok {
		t.Error("a dead lost soul still glows")
	}
}
