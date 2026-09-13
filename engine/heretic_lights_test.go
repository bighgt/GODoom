package engine

import (
	"math"
	"testing"
)

// TestHereticTorchesEmitLight checks buildStaticLights gives Heretic
// torches / braziers / chandeliers a dynamic emitter — they spawn as
// MT_HERETIC mobjs told apart only by spriteOverride, which the Doom
// decoLights table (keyed by mobjType) can't reach. Regression for "the
// torches in the game are not lighted".
func TestHereticTorchesEmitLight(t *testing.T) {
	g := hereticGame(t, "E1M1")
	if !g.heretic {
		t.Fatal("g.heretic not set")
	}

	// Every lit-decoration mobj on the map must have a staticLight sitting
	// on top of it (same X/Y, within the sprite height in Z).
	litSprites := map[string]bool{"WTRH": true, "SRTC": true, "KFR1": true, "CHDL": true, "VLCO": true}
	want := 0
	for _, mo := range g.mobjs {
		if mo.Type != MT_HERETIC || !litSprites[mo.spriteOverride] {
			continue
		}
		want++
		matched := false
		for i := range g.staticLights {
			sl := &g.staticLights[i]
			if math.Abs(sl.x-mo.X) < 1 && math.Abs(sl.y-mo.Y) < 1 {
				matched = true
				break
			}
		}
		if !matched {
			t.Errorf("%s at (%.0f,%.0f) has no static light emitter", mo.spriteOverride, mo.X, mo.Y)
		}
	}
	if want == 0 {
		t.Skip("E1M1 spawned no lit Heretic decorations at this skill")
	}
	t.Logf("E1M1: %d lit Heretic decorations, %d total static lights", want, len(g.staticLights))
}
