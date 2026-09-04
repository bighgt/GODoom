package engine

import (
	"testing"

	"twopointfive/render"
)

// A light-animation sector's emitter must track the sector's live light
// level — dropping the level dims the light, zeroing it removes the light.
func TestSectorLightEmitterTracksLevel(t *testing.T) {
	g := loadRealLevel(t, "../testdata/DOOM1.WAD", "E1M1")
	g.buildStaticLights()

	var sl *staticLight
	for i := range g.staticLights {
		if g.staticLights[i].sector >= 0 {
			sl = &g.staticLights[i]
			break
		}
	}
	if sl == nil {
		t.Skip("E1M1 produced no sector-driven light emitter")
	}
	sec := sl.sector

	g.Level.Sectors[sec].LightLevel = 255
	bright, ok := sl.sample(g)
	if !ok {
		t.Fatalf("emitter reported dark at full light")
	}
	g.Level.Sectors[sec].LightLevel = 80
	dim, ok := sl.sample(g)
	if !ok {
		t.Fatalf("emitter reported dark at level 80")
	}
	if !(dim.Intensity < bright.Intensity && dim.Radius < bright.Radius) {
		t.Errorf("dim light not weaker: bright{i=%.2f r=%.0f} dim{i=%.2f r=%.0f}",
			bright.Intensity, bright.Radius, dim.Intensity, dim.Radius)
	}
	g.Level.Sectors[sec].LightLevel = 0
	if _, ok := sl.sample(g); ok {
		t.Errorf("emitter still lit with the sector fully dark")
	}
}

// Decoration emitters: a decoration-heavy Doom II map should yield some
// static lights, and at least one should carry a colour tint (a red/green/
// blue torch), not just neutral white.
func TestDecorationLightsAreColoured(t *testing.T) {
	g := loadRealLevel(t, "../wad/Doom2.wad", "MAP03")
	g.buildStaticLights()
	if len(g.staticLights) == 0 {
		t.Fatal("MAP03 produced no static lights")
	}
	tinted := false
	for i := range g.staticLights {
		sl := &g.staticLights[i]
		if sl.sector != -1 {
			continue
		}
		if sl.r != sl.g || sl.g != sl.b {
			tinted = true
		}
		l, ok := sl.sample(g)
		if !ok {
			t.Errorf("decoration light %d sampled dark", i)
		}
		if l.Radius <= 0 || l.Intensity <= 0 {
			t.Errorf("decoration light %d: radius=%.1f intensity=%.2f", i, l.Radius, l.Intensity)
		}
	}
	if !tinted {
		t.Error("no coloured decoration light found (all neutral white)")
	}
}

func TestFlickerWobbleBoundedAndDeterministic(t *testing.T) {
	seen := map[float32]bool{}
	for tic := 0; tic < 200; tic++ {
		w := flickerWobble(7, tic)
		if w < 0.80 || w > 1.06 {
			t.Fatalf("tic %d: wobble %.4f out of [0.80, 1.06]", tic, w)
		}
		if flickerWobble(7, tic) != w {
			t.Fatalf("tic %d: flickerWobble not deterministic", tic)
		}
		seen[w] = true
	}
	if len(seen) < 50 {
		t.Errorf("wobble barely varies over 200 tics (%d distinct values)", len(seen))
	}
}

// appendMapLights must never exceed activeLightBudget, and must actually
// contribute lights when there's room and emitters in range.
func TestAppendMapLightsRespectsBudget(t *testing.T) {
	g := loadRealLevel(t, "../testdata/DOOM1.WAD", "E1M1")
	g.buildStaticLights()
	if len(g.staticLights) == 0 {
		t.Skip("E1M1 produced no static lights")
	}
	// Park the camera at the first emitter so at least one is in range.
	g.Camera.X, g.Camera.Y, g.Camera.Z = g.staticLights[0].x, g.staticLights[0].y, g.staticLights[0].z

	got := g.appendMapLights(nil)
	if len(got) == 0 {
		t.Error("no map lights contributed with the camera sat on an emitter")
	}
	if len(got) > activeLightBudget {
		t.Fatalf("appendMapLights returned %d > activeLightBudget %d", len(got), activeLightBudget)
	}

	// An already-full input is returned untouched.
	full := make([]render.Light, activeLightBudget)
	if out := g.appendMapLights(full); len(out) != activeLightBudget {
		t.Errorf("appendMapLights grew an already-full slice to %d", len(out))
	}
}
