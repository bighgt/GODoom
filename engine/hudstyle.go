package engine

import (
	"log"

	"twopointfive/config"
)

// hudStyle is which in-game HUD renderFrame draws: the full Doom status bar,
// a minimal Quake II-style corner HUD, or nothing. Set from config hudStyle
// at load; F1 cycles it (see cycleHUD).
type hudStyle int

const (
	hudDoom hudStyle = iota
	hudQuake2
	hudNone
	hudStyleCount
)

func hudStyleFromConfig(s string) hudStyle {
	switch s {
	case config.HUDStyleQuake2:
		return hudQuake2
	case config.HUDStyleNone:
		return hudNone
	default:
		return hudDoom
	}
}

func (h hudStyle) String() string {
	switch h {
	case hudQuake2:
		return "quake2"
	case hudNone:
		return "none"
	default:
		return "doom"
	}
}

// cycleHUD advances the HUD to the next style (doom -> quake2 -> none ->
// doom), logging the new one — the F1 key's action.
func (g *Game) cycleHUD() {
	g.hudStyle = (g.hudStyle + 1) % hudStyleCount
	log.Printf("engine: HUD -> %s", g.hudStyle)
}

// drawHUD renders the selected HUD onto the overlay. Call from renderFrame
// after the world/weapon/crosshair, before the debug text.
func (g *Game) drawHUD() {
	hs := g.Player.toHUD()
	if g.heretic {
		hs.Arti, hs.ArtiCount = g.currentArtifactIcon()
	}
	switch g.hudStyle {
	case hudDoom:
		if g.heretic {
			g.Raster.DrawHereticStatusBar(hs)
		} else {
			g.Raster.DrawStatusBar(hs)
		}
	case hudQuake2:
		if g.heretic {
			g.Raster.DrawHereticQuakeHUD(hs)
		} else {
			g.Raster.DrawQuakeHUD(hs)
		}
	case hudNone:
		// nothing
	}
}

// DrawHUD is drawHUD, exported for a front end that doesn't go through
// renderFrame at all (cmd/isoengine): draws the currently-selected HUD
// style (config hudStyle / the F1 cycle — health, armour, ammo, keys, and
// which weapon is up) onto Raster's overlay plane. Call after
// Raster.BeginOverlayFrame, before reading Raster.Overlay().
func (g *Game) DrawHUD() { g.drawHUD() }
