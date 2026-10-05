package engine

import (
	"math"
	"strings"
)

// ZDoom's RenderStyle "Add" for the effects that glow. In ZDoom the
// monster fireballs (DoomImpBall, CacodemonBall, BaronBall), the player's
// plasma and BFG balls (Alpha 0.75) and the teleport fog are drawn
// additively — black is fully see-through and everything else brightens
// what's behind it — and GZDoom draws an actor's MODELDEF model in the
// actor's own style, so a pack's fireball mesh glows the same way. Here the
// same things go to the hardware path's additive passes: the model
// pipeline's additive variant (ModelInstance.Additive) and the particle
// billboard pass (AddParticleSprites) for sprites. Voxel models stay solid.

// mobjAddAlpha is mo's additive alpha, or 0 for an ordinary (opaque) thing.
func mobjAddAlpha(mo *Mobj) float32 {
	switch mo.Type {
	case MT_TROOPSHOT, MT_HEADSHOT, MT_BRUISERSHOT, MT_TFOG:
		return 1
	}
	return 0
}

// projectileAddAlpha is mobjAddAlpha for the player's own projectiles.
func projectileAddAlpha(p *Projectile) float32 {
	if p.def != nil && (strings.HasPrefix(p.def.Sprite, "PLSS") || strings.HasPrefix(p.def.Sprite, "BFS1")) {
		return 0.75
	}
	return 0
}

// Translucent glass for the powerup spheres. The soulsphere, megasphere and
// invulnerability sphere are see-through at sphereAlpha; the
// partial-invisibility sphere (Doom's blursphere, Heretic's Shadowsphere)
// is fainter and pulses, after ZDoom's +VISIBILITYPULSE. The renderer adds
// a Fresnel rim and a glossy highlight on top (voxel.frag / model.frag), so
// the edges stay solid while the middle shows what's behind.
const (
	sphereAlpha   = 0.6
	blurAlphaLow  = 0.18
	blurAlphaHigh = 0.45
	blurPulseHz   = 0.7
)

// mobjTransAlpha is mo's translucent-glass alpha, or 0 for a solid thing.
func (g *Game) mobjTransAlpha(mo *Mobj) float32 {
	switch {
	case mo.Type == MT_MISC12, mo.Type == MT_MEGA, mo.Type == MT_INV:
		return sphereAlpha
	case mo.Type == MT_INS, mo.Type == MT_HERETIC && strings.HasPrefix(mobjSpriteName(mo), "INVS"):
		pulse := 0.5 + 0.5*math.Sin(g.renderClock*2*math.Pi*blurPulseHz)
		return float32(blurAlphaLow + (blurAlphaHigh-blurAlphaLow)*pulse)
	}
	return 0
}
