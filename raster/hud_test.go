package raster

import (
	"math"
	"testing"
)

// baseHUDScale is the 320x200 -> framebuffer fit scale (hudFit).
func baseHUDScale(w, h int) float64 {
	return math.Min(float64(w)/InternalWidth, float64(h)/InternalHeight)
}

func TestHUDScaleFitAndMultiplier(t *testing.T) {
	_, _, tex := loadTestLevel(t)

	for _, sz := range [][2]int{{1280, 720}, {2560, 1440}, {960, 540}, {320, 200}} {
		r := New(tex, sz[0], sz[1])
		fit := baseHUDScale(sz[0], sz[1])

		// Default: no user multiplier — the overlay fills the frame height.
		if r.hudScale != fit {
			t.Errorf("%dx%d: default hudScale=%g, want fit %g", sz[0], sz[1], r.hudScale, fit)
		}

		// A multiplier scales it uniformly.
		r.SetHUDScale(0.5)
		if r.hudScale != fit*0.5 {
			t.Errorf("%dx%d: SetHUDScale(0.5) -> %g, want %g", sz[0], sz[1], r.hudScale, fit*0.5)
		}
		r.SetHUDScale(1.5)
		if r.hudScale != fit*1.5 {
			t.Errorf("%dx%d: SetHUDScale(1.5) -> %g, want %g", sz[0], sz[1], r.hudScale, fit*1.5)
		}

		// Non-positive resets to the fit (multiplier 1).
		r.SetHUDScale(0)
		if r.hudScale != fit {
			t.Errorf("%dx%d: SetHUDScale(0) -> %g, want fit %g", sz[0], sz[1], r.hudScale, fit)
		}
	}
}

// The weapon viewmodel has its own scale multiplier (SetWeaponScale),
// independent of the status bar's (SetHUDScale): each ignores the other's.
func TestWeaponScaleIndependentOfHUDScale(t *testing.T) {
	_, _, tex := loadTestLevel(t)
	for _, sz := range [][2]int{{1280, 720}, {2560, 1440}, {320, 200}} {
		r := New(tex, sz[0], sz[1])
		fit := baseHUDScale(sz[0], sz[1])

		// SetHUDScale never moves the weapon (weaponUserScale still 1).
		for _, mul := range []float64{1, 0.5, 1.5, 0.25} {
			r.SetHUDScale(mul)
			if r.weaponScale != fit {
				t.Errorf("%dx%d hud x%.2f: weaponScale=%g, want fit %g", sz[0], sz[1], mul, r.weaponScale, fit)
			}
		}
		hudNow := r.hudScale // whatever the last SetHUDScale left it at

		// SetWeaponScale scales the weapon and never moves the status bar.
		for _, mul := range []float64{1, 0.8, 1.5, 0.25} {
			r.SetWeaponScale(mul)
			if want := fit * mul; r.weaponScale != want {
				t.Errorf("%dx%d wpn x%.2f: weaponScale=%g, want %g", sz[0], sz[1], mul, r.weaponScale, want)
			}
			if r.hudScale != hudNow {
				t.Errorf("%dx%d wpn x%.2f: hudScale moved from %g to %g", sz[0], sz[1], mul, hudNow, r.hudScale)
			}
			// Bottom-anchored.
			if bottom := r.weaponOffY + int(hudLogicalH*r.weaponScale); bottom != r.Height {
				t.Errorf("%dx%d wpn x%.2f: weapon plane bottom at %d, want %d", sz[0], sz[1], mul, bottom, r.Height)
			}
			// Horizontally centred (offset can go negative when a >1x
			// weapon overflows the frame width — that's fine).
			if mul <= 1 && r.weaponOffX < 0 {
				t.Errorf("%dx%d wpn x%.2f: weaponOffX=%d, want >=0", sz[0], sz[1], mul, r.weaponOffX)
			}
		}
		// Non-positive resets to 1.
		r.SetWeaponScale(0)
		if r.weaponScale != fit {
			t.Errorf("%dx%d: SetWeaponScale(0) -> %g, want fit %g", sz[0], sz[1], r.weaponScale, fit)
		}
	}
}

func TestHUDPlaneBottomAnchored(t *testing.T) {
	_, _, tex := loadTestLevel(t)
	for _, sz := range [][2]int{{1280, 720}, {1920, 1080}, {2560, 1440}} {
		for _, mul := range []float64{1, 0.6, 0.3} {
			r := New(tex, sz[0], sz[1])
			r.SetHUDScale(mul)
			if bottom := r.hudOffY + int(hudLogicalH*r.hudScale); bottom != r.Height {
				t.Errorf("%dx%d x%.1f: overlay plane bottom edge at %d, want Height=%d",
					sz[0], sz[1], mul, bottom, r.Height)
			}
			if r.hudOffX < 0 {
				t.Errorf("%dx%d x%.1f: hudOffX=%d, want >=0 (horizontally centered)", sz[0], sz[1], mul, r.hudOffX)
			}
		}
	}
}
