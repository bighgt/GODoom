package engine

import (
	"testing"

	"twopointfive/wad"
)

func TestLiquidTintFor(t *testing.T) {
	if _, _, _, a := liquidTintFor(liqNone); a != 0 {
		t.Errorf("liqNone amt = %v, want 0", a)
	}
	if _, _, _, a := liquidTintFor(liqWater); a != 0 {
		t.Errorf("liqWater amt = %v, want 0 (not a hazard)", a)
	}
	if r, _, _, a := liquidTintFor(liqLava); a >= 0 || r < 0.9 {
		t.Errorf("liqLava = (r=%v, a=%v), want warm rgb + NEGATIVE amt (heat wobble)", r, a)
	}
	if _, gg, _, a := liquidTintFor(liqAcid); a <= 0 || gg < 0.5 {
		t.Errorf("liqAcid = (g=%v, a=%v), want green + positive amt", gg, a)
	}
	if r, _, _, a := liquidTintFor(liqBlood); a <= 0 || r < 0.5 {
		t.Errorf("liqBlood = (r=%v, a=%v), want red + positive amt", r, a)
	}
}

func TestApplySectorInterp(t *testing.T) {
	g := &Game{Level: &wad.Level{Sectors: []wad.Sector{
		{FloorHeight: 0, CeilingHeight: 128},  // 0: static
		{FloorHeight: 40, CeilingHeight: 128}, // 1: floor rose 40 this tic -> lerp
		{FloorHeight: 10, CeilingHeight: 0},   // 2: floor +10 (lerp), ceiling -200 (snap)
	}}}
	g.prevSectorH = []int16{
		0, 128,
		0, 128,
		0, 200,
	}
	g.ticAccum = ticDuration / 2 // renderLerp() == 0.5

	restore := g.applySectorInterp()
	if !g.sectorInterpActive {
		t.Fatal("sectorInterpActive not set")
	}
	if g.Level.Sectors[0].FloorHeight != 0 || g.Level.Sectors[0].CeilingHeight != 128 {
		t.Errorf("static sector 0 was touched: %+v", g.Level.Sectors[0])
	}
	if got := g.Level.Sectors[1].FloorHeight; got != 20 {
		t.Errorf("sector 1 floor lerp = %d, want 20 (0 + 0.5*40)", got)
	}
	if got := g.Level.Sectors[2].FloorHeight; got != 5 {
		t.Errorf("sector 2 floor lerp = %d, want 5", got)
	}
	if got := g.Level.Sectors[2].CeilingHeight; got != 0 {
		t.Errorf("sector 2 ceiling (200u snap) = %d, want 0 (no lerp)", got)
	}

	restore()
	want := []wad.Sector{
		{FloorHeight: 0, CeilingHeight: 128},
		{FloorHeight: 40, CeilingHeight: 128},
		{FloorHeight: 10, CeilingHeight: 0},
	}
	for i := range want {
		if g.Level.Sectors[i] != want[i] {
			t.Errorf("restore: sector %d = %+v, want %+v", i, g.Level.Sectors[i], want[i])
		}
	}
}

func TestApplySectorInterpNoOpAtTicBoundary(t *testing.T) {
	g := &Game{Level: &wad.Level{Sectors: []wad.Sector{{FloorHeight: 40, CeilingHeight: 128}}}}
	g.prevSectorH = []int16{0, 128}
	g.ticAccum = 0 // renderLerp() == 0

	g.applySectorInterp()()
	if g.sectorInterpActive {
		t.Error("sectorInterpActive set with renderLerp == 0")
	}
	if g.Level.Sectors[0].FloorHeight != 40 {
		t.Errorf("heights changed at tic boundary: %d", g.Level.Sectors[0].FloorHeight)
	}
}

func TestCollectGroundShadows(t *testing.T) {
	g := &Game{}
	g.Camera.X, g.Camera.Y = 0, 0

	mk := func(x, y, z, fz, r float64, flags int) *Mobj {
		return &Mobj{X: x, Y: y, Z: z, FloorZ: fz, Radius: r, Flags: flags, State: stateNum(1)}
	}
	g.mobjs = []*Mobj{
		mk(50, 0, 0, 0, 20, 0),                  // grounded barrel -> in
		mk(-40, 30, 8, 0, 16, 0),                // z within FloorZ+16 -> in
		mk(0, 0, 0, 0, 20, 0),                   // right on top of the camera -> in
		mk(100, 0, 200, 0, 20, 0),               // airborne (z >> floorZ+16) -> out
		mk(0, 60, 0, 0, 3, 0),                   // radius < 6 -> out
		mk(0, 0, 0, 0, 20, MF_NOGRAVITY),        // flyer -> out
		mk(3000, 0, 0, 0, 20, 0),                // > 1600u away -> out
	}
	got := g.collectGroundShadows()
	if len(got) != 3 {
		t.Fatalf("got %d shadow casters, want 3: %v", len(got), got)
	}
	for _, s := range got {
		if s[3] < 14 || s[3] > 60 {
			t.Errorf("blob radius %v out of [14,60]", s[3])
		}
	}

	// Cap: 40 grounded things -> exactly hwGroundShadowCap, nearest first.
	g.mobjs = g.mobjs[:0]
	for i := 0; i < 40; i++ {
		g.mobjs = append(g.mobjs, mk(float64(20*(i+1)), 0, 0, 0, 20, 0))
	}
	got = g.collectGroundShadows()
	if len(got) != hwGroundShadowCap {
		t.Fatalf("cap: got %d, want %d", len(got), hwGroundShadowCap)
	}
	if got[0][0] != 20 || got[len(got)-1][0] != float32(20*hwGroundShadowCap) {
		t.Errorf("not nearest-first: front=%v back=%v", got[0][0], got[len(got)-1][0])
	}
}
