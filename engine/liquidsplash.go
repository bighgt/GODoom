package engine

import "twopointfive/bsp"

// Liquid-crossing detection for the player: g.playerLiquidKind (liqNone/
// liqWater/liqLava/liqAcid) tracks which liquid sector's floor the player
// is currently touching, for other systems to react to — see
// engine/liquidripple.go's wading wake-ripple. This used to also fire a
// droplet/ember splash particle burst on crossing (reusing raw WAD
// spark-sprite art for lack of dedicated splash art) — removed: next to the
// hardware renderer's real water shading, those flat, hard-edged 2D
// billboards read as an obvious visual glitch rather than a splash.
// liquidCrossingHook is kept purely as a test observation point for the
// crossing-detection logic itself (see liquidsplash_test.go) — it no
// longer gates or represents any visual effect.

// liquidContactTolerance is how far above a sector's floor (x, y, z) can
// still be read as "touching" it. Without some slack, jumping in place
// while standing in water would spuriously "exit" the instant Space is
// pressed — g.onGround flips false right then (see the jump-impulse code in
// game.go), well before the feet have actually risen off the surface in
// any way a player would call "leaving the water" — then "enter" again on
// landing, all for a hop that never left the pool. jumpImpulse's own apex
// against playerFallGravity is ~46 units (see its doc comment); this sits
// comfortably above that so an ordinary in-place jump never triggers a
// false exit/enter pair, while a genuine long jump clearing a chasm still
// spends enough time well above this margin to correctly read as never
// having touched whatever hazard is at the bottom.
const liquidContactTolerance = 56.0

// liquidKindAtPoint resolves (x, y, z)'s active sector — room-over-room
// aware, the same technique playerInSpecialSector (specials.go) uses via
// planeAt, so a catwalk over a lava pit is never misread as standing in it
// — and classifies its floor flat, but only if (x, y, z) is actually within
// liquidContactTolerance of that sector's floor (otherwise this is a jump
// or fall passing over/through the sector's airspace, not contact with its
// floor). liqBlood counts as liqNone: a decorative blood pool isn't a
// liquid this feature should react to.
func (g *Game) liquidKindAtPoint(x, y, z float64) int {
	secIdx := bsp.PointSectorIndex(g.BSP, g.Level, float32(x), float32(y))
	if secIdx < 0 {
		return liqNone
	}
	floor, _, activeIdx := g.planeAt(secIdx, x, y, z)
	if z > floor+liquidContactTolerance {
		return liqNone
	}
	kind := liquidKindOfFlat(g.Level.Sectors[activeIdx].FloorTexture)
	if kind == liqBlood {
		return liqNone
	}
	return kind
}

// checkPlayerLiquidCrossing updates g.playerLiquidKind whenever it changes
// since last tic — entering (hook fires with the new kind) or leaving
// (hook fires with the kind just left). liquidKindAtPoint's own height
// check (not g.onGround — see liquidContactTolerance) is what keeps a
// player jumping OVER a lava pit from reading as touching it.
func (g *Game) checkPlayerLiquidCrossing() {
	if g.playerMobj == nil {
		return
	}
	kind := g.liquidKindAtPoint(g.Camera.X, g.Camera.Y, g.PlayerZ)
	if kind != g.playerLiquidKind && g.liquidCrossingHook != nil {
		if kind != liqNone {
			g.liquidCrossingHook(g.Camera.X, g.Camera.Y, g.PlayerZ, kind)
		} else if g.playerLiquidKind != liqNone {
			g.liquidCrossingHook(g.Camera.X, g.Camera.Y, g.PlayerZ, g.playerLiquidKind)
		}
	}
	g.playerLiquidKind = kind
}
