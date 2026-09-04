package raster

import "testing"

// px returns the RGBA of the overlay pixel at (x, y).
func overlayPx(r *Renderer, x, y int) (rr, gg, bb, aa byte) {
	i := (y*r.Width + x) * 4
	return r.overlay[i], r.overlay[i+1], r.overlay[i+2], r.overlay[i+3]
}

func TestDrawCrosshairDot(t *testing.T) {
	_, _, tex := loadTestLevel(t)
	r := New(tex, 1920, 1080)
	cx, cy := r.Width/2, r.Height/2

	if _, _, _, a := overlayPx(r, cx, cy); a != 0 {
		t.Fatalf("centre pixel not clear before draw (alpha %d)", a)
	}
	r.DrawCrosshair(CrosshairDot, CrosshairMedium)
	if rr, gg, bb, a := overlayPx(r, cx, cy); rr != 255 || gg != 255 || bb != 255 || a != 255 {
		t.Fatalf("dot centre pixel = %d,%d,%d,%d, want opaque white", rr, gg, bb, a)
	}
	// The dirty-row span must cover the centre so the enhanced pipeline
	// re-uploads it.
	if !(r.ovY0 <= cy && cy < r.ovY1) {
		t.Fatalf("markOverlay span [%d,%d) does not cover centre row %d", r.ovY0, r.ovY1, cy)
	}
}

func TestDrawCrosshairCrossHasCentreGap(t *testing.T) {
	_, _, tex := loadTestLevel(t)
	r := New(tex, 1920, 1080)
	cx, cy := r.Width/2, r.Height/2
	unit := crosshairUnit(r.Height, CrosshairMedium)

	r.DrawCrosshair(CrosshairCross, CrosshairMedium)
	if _, _, _, a := overlayPx(r, cx, cy); a != 0 {
		t.Fatalf("cross should leave a centre gap, but centre pixel alpha = %d", a)
	}
	// A pixel one gap out along the arm is drawn.
	if _, _, _, a := overlayPx(r, cx+unit, cy); a != 255 {
		t.Fatalf("cross arm pixel at +%d not drawn (alpha %d)", unit, a)
	}
}

func TestDrawCrosshairOffAndUnknown(t *testing.T) {
	_, _, tex := loadTestLevel(t)
	for _, style := range []string{CrosshairOff, "spinner", ""} {
		r := New(tex, 1280, 720)
		r.DrawCrosshair(style, CrosshairMedium)
		cx, cy := r.Width/2, r.Height/2
		if _, _, _, a := overlayPx(r, cx, cy); a != 0 {
			t.Fatalf("style %q drew something (centre alpha %d), want nothing", style, a)
		}
	}
}

func TestCrosshairUnitSizeOrdering(t *testing.T) {
	const h = 1080
	tiny := crosshairUnit(h, CrosshairTiny)
	small := crosshairUnit(h, CrosshairSmall)
	medium := crosshairUnit(h, CrosshairMedium)
	large := crosshairUnit(h, CrosshairLarge)

	if !(tiny < small && small < medium && medium < large) {
		t.Fatalf("sizes not strictly increasing: tiny=%d small=%d medium=%d large=%d", tiny, small, medium, large)
	}
	// "medium" is the current/reference size (r.Height/240).
	if medium != h/240 {
		t.Fatalf("medium unit = %d, want %d (the pre-existing size)", medium, h/240)
	}
	// Unknown falls back to medium; never below 1 even at tiny sizes.
	if crosshairUnit(h, "huge") != medium {
		t.Fatal("unknown size did not fall back to medium")
	}
	if crosshairUnit(120, CrosshairTiny) < 1 {
		t.Fatal("unit dropped below 1")
	}
}

func TestDrawCrosshairSizeAffectsExtent(t *testing.T) {
	_, _, tex := loadTestLevel(t)
	// A larger size must light a pixel farther from centre than a smaller one.
	drawnRadius := func(size string) int {
		r := New(tex, 1920, 1080)
		r.DrawCrosshair(CrosshairDot, size)
		cx, cy := r.Width/2, r.Height/2
		far := 0
		for d := 0; d < 64; d++ {
			if _, _, _, a := overlayPx(r, cx+d, cy); a == 255 {
				far = d
			}
		}
		return far
	}
	if !(drawnRadius(CrosshairTiny) < drawnRadius(CrosshairMedium) &&
		drawnRadius(CrosshairMedium) < drawnRadius(CrosshairLarge)) {
		t.Fatalf("dot extent not increasing with size: tiny=%d medium=%d large=%d",
			drawnRadius(CrosshairTiny), drawnRadius(CrosshairMedium), drawnRadius(CrosshairLarge))
	}
}
