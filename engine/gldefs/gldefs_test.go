package gldefs

import (
	"math"
	"testing"
)

const sample = `
// Doom-style GLDEFS sample exercising every light kind + an attachment.

pointlight REDTORCH_SMALL
{
	color 1.0 0.45 0.2
	size 96
	offset 0 40 0
	attenuate 1
}

pulselight LAMP_HUM
{
	color 0.6 0.7 1.0
	size 80
	secondarySize 110
	interval 2.0
}

flickerlight FIRE_FLICK
{
	/* block comment inside a block */
	color 255 128 32          // authored 0..255 — must be scaled
	size 70
	secondarySize 100
	chance 0.3
}

flickerlight2 CANDLE
{
	color "ffcc88"
	size 40
	secondarySize 48
	interval 0.5
}

sectorlight STROBE
{
	color white
	size 200
}

// An unknown top-level block must be skipped without eating what follows.
glow
{
	flats { LAVA1 NUKAGE1 }
	texture { AASHITTY 128 64 fullbright }
}

object RedTorch
{
	frame TREDA { light REDTORCH_SMALL }
	frame TREDB { light REDTORCH_SMALL light FIRE_FLICK }
}

object EvilEye
{
	frame CEYE  { light STROBE }   // 4-char sprite: all frames
}
`

func approx(a, b float32) bool { return math.Abs(float64(a-b)) < 1e-4 }

func TestParseAllLightKinds(t *testing.T) {
	defs, errs := Parse([]byte(sample), nil)
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	if defs.NumLights() != 5 {
		t.Fatalf("want 5 lights, got %d", defs.NumLights())
	}

	rt, ok := defs.Light("redtorch_small")
	if !ok {
		t.Fatal("REDTORCH_SMALL not found (case-insensitive lookup)")
	}
	if rt.Type != Point || rt.Size != 96 || !rt.Attenuate {
		t.Errorf("REDTORCH_SMALL: %+v", rt)
	}
	if rt.Offset != [3]float32{0, 40, 0} {
		t.Errorf("REDTORCH_SMALL offset: %v", rt.Offset)
	}

	hum, _ := defs.Light("LAMP_HUM")
	if hum.Type != Pulse || hum.Size != 80 || hum.SecondarySize != 110 || hum.Interval != 2.0 {
		t.Errorf("LAMP_HUM: %+v", hum)
	}

	// 0..255 integer triple gets scaled to 0..1.
	fire, _ := defs.Light("FIRE_FLICK")
	if fire.Type != Flicker || !approx(fire.Color[0], 1.0) || !approx(fire.Color[1], 128.0/255) || !approx(fire.Color[2], 32.0/255) {
		t.Errorf("FIRE_FLICK colour not scaled: %v", fire.Color)
	}
	if fire.Chance != 0.3 {
		t.Errorf("FIRE_FLICK chance: %v", fire.Chance)
	}

	// Hex-string colour.
	cand, _ := defs.Light("CANDLE")
	if cand.Type != Flicker2 || !approx(cand.Color[0], 1.0) || !approx(cand.Color[1], 0xcc/255.0) || !approx(cand.Color[2], 0x88/255.0) {
		t.Errorf("CANDLE colour: %v", cand.Color)
	}

	// Named colour.
	strobe, _ := defs.Light("STROBE")
	if strobe.Type != Sector || strobe.Color != [3]float32{1, 1, 1} {
		t.Errorf("STROBE: %+v", strobe)
	}
}

func TestFrameAttachments(t *testing.T) {
	defs, errs := Parse([]byte(sample), nil)
	if len(errs) != 0 {
		t.Fatalf("parse errors: %v", errs)
	}

	// TRED frame A (0) -> just REDTORCH_SMALL.
	a := defs.Frame("TRED", 0)
	if len(a) != 1 || a[0].Name != "REDTORCH_SMALL" {
		t.Fatalf("TRED frame A: %v", names(a))
	}
	// TRED frame B (1) -> REDTORCH_SMALL + FIRE_FLICK.
	b := defs.Frame("tred", 1)
	if len(b) != 2 || b[0].Name != "REDTORCH_SMALL" || b[1].Name != "FIRE_FLICK" {
		t.Fatalf("TRED frame B: %v", names(b))
	}
	// TRED frame C (2) -> nothing (only A and B were attached).
	if got := defs.Frame("TRED", 2); got != nil {
		t.Fatalf("TRED frame C should be empty, got %v", names(got))
	}
	// CEYE with no frame letter -> every frame matches.
	for _, f := range []int{0, 3, 25} {
		if got := defs.Frame("CEYE", f); len(got) != 1 || got[0].Name != "STROBE" {
			t.Fatalf("CEYE frame %d: %v", f, names(got))
		}
	}
	// Unknown sprite -> nil, no panic.
	if got := defs.Frame("ZZZZ", 0); got != nil {
		t.Fatalf("unknown sprite: %v", names(got))
	}
}

func TestNilDefsSafe(t *testing.T) {
	var d *Defs
	if d.NumLights() != 0 || d.NumAttachments() != 0 || d.Frame("TRED", 0) != nil {
		t.Fatal("nil *Defs methods must be safe no-ops")
	}
	if _, ok := d.Light("X"); ok {
		t.Fatal("nil *Defs Light() must report not-found")
	}
}

func TestUnterminatedBlockDoesNotHang(t *testing.T) {
	// Missing closing brace at EOF — must terminate and still yield the light.
	defs, _ := Parse([]byte("pointlight A { color 1 1 1 size 50"), nil)
	if _, ok := defs.Light("A"); !ok {
		t.Fatal("light before an unterminated block was lost")
	}
}

func TestUnknownLightPropertySkipped(t *testing.T) {
	src := `pointlight A { color 1 1 1 size 64 zzz 1 2 3 subtractive 1 }`
	defs, errs := Parse([]byte(src), nil)
	if len(errs) != 0 {
		t.Fatalf("errs: %v", errs)
	}
	l, _ := defs.Light("A")
	if l.Size != 64 || !l.Subtractive {
		t.Errorf("unknown prop derailed parsing: %+v", l)
	}
}

func TestInclude(t *testing.T) {
	main := `#include "MORELITE"` + "\n" + `object RedTorch { frame TREDA { light INCLUDED_LIGHT } }`
	lib := map[string][]byte{
		"MORELITE": []byte(`pointlight INCLUDED_LIGHT { color 0 1 0 size 120 }`),
	}
	defs, errs := Parse([]byte(main), func(n string) ([]byte, bool) {
		b, ok := lib[n]
		return b, ok
	})
	if len(errs) != 0 {
		t.Fatalf("errs: %v", errs)
	}
	if defs.NumLights() != 1 {
		t.Fatalf("include not merged: %d lights", defs.NumLights())
	}
	got := defs.Frame("TRED", 0)
	if len(got) != 1 || got[0].Name != "INCLUDED_LIGHT" || got[0].Size != 120 {
		t.Fatalf("attachment did not resolve across #include: %v", names(got))
	}
}

func TestUnresolvedLightReported(t *testing.T) {
	src := `object X { frame TREDA { light GHOST } }`
	defs, errs := Parse([]byte(src), nil)
	if len(errs) == 0 {
		t.Fatal("referencing an undefined light should be reported")
	}
	if got := defs.Frame("TRED", 0); got != nil {
		t.Fatalf("undefined light must not attach: %v", names(got))
	}
}

func TestBrightmapCaptured(t *testing.T) {
	src := `
brightmap texture COMPUTE1 { map "brightmaps/compute1.png" disablefullbright }
pointlight A { color 1 1 1 size 10 }
`
	defs, errs := Parse([]byte(src), nil)
	if len(errs) != 0 {
		t.Fatalf("errs: %v", errs)
	}
	bms := defs.Brightmaps()
	if len(bms) != 1 || bms[0].Kind != "texture" || bms[0].Name != "COMPUTE1" ||
		bms[0].Map != "brightmaps/compute1.png" || !bms[0].DisableFullbright {
		t.Fatalf("brightmap: %+v", bms)
	}
	if defs.NumLights() != 1 {
		t.Fatalf("brightmap block ate the following light: %d", defs.NumLights())
	}
}

func names(ls []*Light) []string {
	out := make([]string, len(ls))
	for i, l := range ls {
		out[i] = l.Name
	}
	return out
}
