package main

import "testing"

// solidBlock returns a `sample` for a wxh texture that is dark grey
// everywhere except a rectangle [bx,bx+bw) x [by,by+bh) filled with colour
// (r,g,b).
func solidBlock(w, h, bx, by, bw, bh int, r, g, b uint8) sample {
	return func(x, y int) (uint8, uint8, uint8, bool) {
		if x >= bx && x < bx+bw && y >= by && y < by+bh {
			return r, g, b, true
		}
		return 40, 40, 40, true
	}
}

func maskCoverage(w, h int, at sample) (int, bool) {
	g, ok := buildMask(w, h, at)
	if !ok {
		return 0, false
	}
	n := 0
	for _, p := range g.Pix {
		if p > 0 {
			n++
		}
	}
	return n, true
}

func TestBuildMaskKeepsSolidColouredBlock(t *testing.T) {
	// A vivid cyan screen panel in the middle of a 64x64 wall.
	n, ok := maskCoverage(64, 64, solidBlock(64, 64, 24, 24, 16, 12, 40, 255, 255))
	if !ok {
		t.Fatal("a solid bright-cyan block produced no brightmap")
	}
	// Interior survives the 5/8 erosion; a 16x12 block keeps ~14x10.
	if n < 100 || n > 16*12 {
		t.Errorf("unexpected mask coverage %d for a 16x12 block", n)
	}
}

func TestBuildMaskRejectsGreyHighlights(t *testing.T) {
	// Bright but *grey* (a metal specular highlight) — must not be marked.
	if _, ok := buildMask(64, 64, solidBlock(64, 64, 20, 20, 20, 20, 235, 235, 235)); ok {
		t.Error("a bright grey block was marked emissive (should be rejected: not coloured)")
	}
}

func TestBuildMaskRejectsSpeckle(t *testing.T) {
	// Scattered single bright-cyan texels, no cluster.
	speckle := func(x, y int) (uint8, uint8, uint8, bool) {
		if (x*7+y*13)%23 == 0 {
			return 20, 255, 255, true
		}
		return 40, 40, 40, true
	}
	if _, ok := buildMask(64, 64, speckle); ok {
		t.Error("scattered speckle was marked emissive (should be eroded away)")
	}
}

func TestBuildMaskRejectsMostlyBright(t *testing.T) {
	// A whole vivid-orange wall — bright + saturated everywhere, but a wall
	// this uniformly bright is not a *light*.
	all := func(x, y int) (uint8, uint8, uint8, bool) { return 255, 140, 20, true }
	if _, ok := buildMask(64, 64, all); ok {
		t.Error("a fully-bright wall was marked emissive (should trip maxEmissiveFrac)")
	}
}
