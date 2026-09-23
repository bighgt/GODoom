package vulkan

import (
	"testing"

	"twopointfive/render"
	"twopointfive/render/worldgeo"
)

// TestCullLightsToViewDropsOutOfViewPreservesOrder pins the two properties
// DrawWorld's own light-shading loop and shadowBudget both depend on: lights
// outside worldgeo.LightVisible's cone are dropped (so dynamicDiffuse and
// the shadow-map budget never see them), and the survivors keep their
// original relative order (so "the leading lights" — collectLights' own
// gameplay-critical-first ordering — still means the same thing afterward).
func TestCullLightsToViewDropsOutOfViewPreservesOrder(t *testing.T) {
	r := &Renderer{wr: &worldResources{}}
	cam := worldgeo.Camera{X: 0, Y: 0, Angle: 0, FOVDeg: 90}

	lights := []render.Light{
		{X: 500, Y: 0, Radius: 200},   // ahead — visible
		{X: -600, Y: 0, Radius: 100},  // behind, out of reach — culled
		{X: 400, Y: 100, Radius: 200}, // ahead — visible
	}
	got := r.cullLightsToView(cam, lights)
	if len(got) != 2 {
		t.Fatalf("got %d surviving lights, want 2 (%+v)", len(got), got)
	}
	if got[0].X != 500 || got[1].X != 400 {
		t.Errorf("survivors out of order: got X=%v,%v want X=500,400", got[0].X, got[1].X)
	}
}

// TestCullLightsToViewReusesBuffer guards the "no allocation on a warm
// frame" claim in cullLightsToView's own doc comment: calling it twice with
// the same or a smaller light count must not grow wr.lightCullBuf's
// underlying array a second time.
func TestCullLightsToViewReusesBuffer(t *testing.T) {
	r := &Renderer{wr: &worldResources{}}
	cam := worldgeo.Camera{X: 0, Y: 0, Angle: 0, FOVDeg: 90}
	lights := []render.Light{{X: 100, Y: 0, Radius: 50}, {X: 200, Y: 0, Radius: 50}}

	first := r.cullLightsToView(cam, lights)
	if len(first) != 2 {
		t.Fatalf("first call: got %d, want 2", len(first))
	}
	buf := r.wr.lightCullBuf
	second := r.cullLightsToView(cam, lights[:1])
	if len(second) != 1 {
		t.Fatalf("second call: got %d, want 1", len(second))
	}
	if &r.wr.lightCullBuf[0] != &buf[0] {
		t.Error("cullLightsToView reallocated its scratch buffer on a warm (same-or-smaller) call")
	}
}
