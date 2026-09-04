package raster

// Crosshair styles and sizes — the keyword strings config.json's
// "crosshair" / "crosshairSize" accept, duplicated here so package raster
// stays independent of package config.
const (
	CrosshairOff   = "off"
	CrosshairDot   = "dot"
	CrosshairCross = "cross"

	CrosshairTiny   = "tiny"
	CrosshairSmall  = "small"
	CrosshairMedium = "medium"
	CrosshairLarge  = "large"
)

// crosshairUnit is the base size, in render pixels, the marker is built
// from. "medium" is exactly the pre-existing size (height/240 — ~4 px at
// 1080p, 3 at 720p); the other keywords scale that. At least 1.
func crosshairUnit(height int, size string) int {
	base := height / 240 // "medium" — unchanged from the original crosshair
	var u int
	switch size {
	case CrosshairTiny:
		u = base / 2
	case CrosshairSmall:
		u = base * 3 / 4
	case CrosshairLarge:
		u = base * 7 / 4
	default: // "medium" and anything unrecognised
		u = base
	}
	if u < 1 {
		u = 1
	}
	return u
}

// DrawCrosshair plots a small white aiming marker into the 2D overlay at the
// exact centre of the frame — a dead-simple targeting aid, opaque white,
// composited like the rest of the HUD (so it shows in both the vanilla and
// the enhanced pipelines). It scales with the vertical render resolution so
// it looks the same on any monitor, then by size (one of Crosshair{Tiny,
// Small,Medium,Large}). style is one of the Crosshair{Off,Dot,Cross}
// constants; anything unrecognised (including "off") draws nothing. Call
// once per frame, after the weapon draw.
func (r *Renderer) DrawCrosshair(style, size string) {
	if r.Width < 3 || r.Height < 3 {
		return
	}
	cx, cy := r.Width/2, r.Height/2
	unit := crosshairUnit(r.Height, size)

	put := func(x, y int) {
		if x < 0 || x >= r.Width || y < 0 || y >= r.Height {
			return
		}
		i := (y*r.Width + x) * 4
		r.overlay[i], r.overlay[i+1], r.overlay[i+2], r.overlay[i+3] = 255, 255, 255, 255
	}

	switch style {
	case CrosshairDot:
		rad := unit
		for dy := -rad; dy <= rad; dy++ {
			for dx := -rad; dx <= rad; dx++ {
				put(cx+dx, cy+dy)
			}
		}
		r.markOverlay(cy-rad, cy+rad+1)

	case CrosshairCross:
		arm := unit * 3
		thick := unit / 2
		gap := unit
		for d := gap; d <= arm; d++ {
			for t := -thick; t <= thick; t++ {
				put(cx+d, cy+t)
				put(cx-d, cy+t)
				put(cx+t, cy+d)
				put(cx+t, cy-d)
			}
		}
		r.markOverlay(cy-arm, cy+arm+1)
	}
}
