package engine

import (
	"math"
	"strings"

	"twopointfive/bsp"
)

// Liquid glow for the enhanced / hardware lighting paths. Vanilla Doom
// draws lava and nukage as plain animated flats — no light comes off them.
// Here every lava sector seeds one or more warm point lights so the pool
// throws orange light onto the walls, monsters and the player around it,
// and every nukage/slime sector seeds a dim sickly-green light so a toxic
// room reads green even in the dark. Water and blood get nothing (water's
// look is the reflective-flat effect in the software rasterizer, not a
// light; blood is just decor).
//
// Built once per level by buildStaticLights (after the decoration lights)
// and sampled every frame by collectLights like any other static emitter,
// so they cost nothing extra per frame beyond the shared budget cull.

// liquid kinds.
const (
	liqNone = iota
	liqWater
	liqLava
	liqAcid
	liqBlood
)

// liquidKindOfFlat classifies a floor-flat lump name. It keys off the
// family prefix so it still matches whichever animation frame the flat is
// currently showing (NUKAGE1..3, LAVA1..4, ...). SLIME01..16 (Doom II's
// toxic goo) count as acid; the Doom II "SLIME" *wall* textures never reach
// here since only floor flats are tested.
func liquidKindOfFlat(flat string) int {
	switch {
	case strings.HasPrefix(flat, "LAVA"), flat == "FIRELAVA":
		return liqLava
	case strings.HasPrefix(flat, "NUKAGE"), strings.HasPrefix(flat, "SLIME"):
		return liqAcid
	case strings.HasPrefix(flat, "FWATER"):
		return liqWater
	case strings.HasPrefix(flat, "BLOOD"):
		return liqBlood
	}
	return liqNone
}

// hwLiquidTint is the full-frame colour wash the hardware present pass
// applies while the player stands in a damaging liquid — id's palette
// flash, as an environmental tint. rgb 0..1, amt = strength (0 = none); a
// NEGATIVE amt additionally asks worldblit.frag for a slow lava heat
// wobble. Plain water gets nothing (it's not a hazard).
func (g *Game) hwLiquidTint() (r, gg, b, amt float32) {
	if g.Level == nil {
		return 0, 0, 0, 0
	}
	sec := bsp.PointSector(g.BSP, g.Level, float32(g.Camera.X), float32(g.Camera.Y))
	if sec == nil {
		return 0, 0, 0, 0
	}
	return liquidTintFor(liquidKindOfFlat(sec.FloorTexture))
}

// liquidTintFor maps a liquid kind (liquidKindOfFlat) to worldblit.frag's
// screen-tint push constant: rgb 0..1, amt = strength (0 = none, negative =
// also request the lava heat wobble). Water and "none" get nothing.
func liquidTintFor(kind int) (r, gg, b, amt float32) {
	switch kind {
	case liqLava:
		return 1.0, 0.42, 0.12, -0.32
	case liqAcid:
		return 0.26, 0.80, 0.16, 0.30
	case liqBlood:
		return 0.72, 0.06, 0.06, 0.34
	}
	return 0, 0, 0, 0
}

// maxLiquidLights caps how many liquid emitters a single level contributes,
// so a lava-heavy slaughtermap can't grow g.staticLights (and its per-frame
// pre-cull loop) without bound. Sectors are visited in index order.
const maxLiquidLights = 64

// appendLiquidLights adds the liquid glow emitters. firstSeed is the wobble
// seed for the first one; it returns the next unused seed.
func (g *Game) appendLiquidLights(firstSeed int) int {
	if g.Level == nil || len(g.Level.Sectors) == 0 {
		return firstSeed
	}
	g.ensureSectorIndex()
	seed := firstSeed
	added := 0

	for si := range g.Level.Sectors {
		if added >= maxLiquidLights {
			break
		}
		s := &g.Level.Sectors[si]
		kind := liquidKindOfFlat(s.FloorTexture)
		if kind != liqLava && kind != liqAcid {
			continue
		}
		// A floor above its own ceiling is a closed/degenerate sector — no
		// visible surface to glow.
		if int(s.CeilingHeight) <= int(s.FloorHeight) {
			continue
		}

		minX, minY, maxX, maxY, ok := g.sectorBBox(si)
		if !ok {
			continue
		}
		w, h := maxX-minX, maxY-minY
		if w < 24 || h < 24 {
			continue // a thin liquid trim strip — skip, it would just add noise
		}

		var r, gg, b, inten, baseRad float32
		var flicker bool
		switch kind {
		case liqLava:
			r, gg, b = 1.0, 0.42, 0.13
			inten, flicker = 1.7, true
			baseRad = 260
		case liqAcid:
			r, gg, b = 0.32, 0.85, 0.28
			inten, flicker = 0.75, true
			baseRad = 200
		}
		// Radius scales up for a big pool, clamped so one lake isn't lit by a
		// single sun.
		span := float32(math.Hypot(w, h))
		radius := baseRad + 0.20*span
		if radius > baseRad*1.9 {
			radius = baseRad * 1.9
		}
		z := float64(s.FloorHeight) + 16

		for _, p := range sampleSectorPoints(minX, minY, maxX, maxY) {
			if added >= maxLiquidLights {
				break
			}
			g.staticLights = append(g.staticLights, staticLight{
				x: p[0], y: p[1], z: z,
				r: r, g: gg, b: b,
				baseInten: inten, baseRadius: radius,
				sector: -1, flicker: flicker, seed: seed,
			})
			seed++
			added++
		}
	}
	return seed
}

// sectorBBox is the axis-aligned bounds of a sector's linedef vertices.
func (g *Game) sectorBBox(sec int) (minX, minY, maxX, maxY float64, ok bool) {
	if sec < 0 || sec >= len(g.sectorLines) {
		return 0, 0, 0, 0, false
	}
	minX, minY = math.Inf(1), math.Inf(1)
	maxX, maxY = math.Inf(-1), math.Inf(-1)
	n := 0
	for _, li := range g.sectorLines[sec] {
		ld := &g.Level.Linedefs[li]
		for _, vi := range [2]uint16{ld.StartVertex, ld.EndVertex} {
			if int(vi) >= len(g.Level.Vertexes) {
				continue
			}
			v := g.Level.Vertexes[vi]
			x, y := float64(v.X), float64(v.Y)
			minX, maxX = math.Min(minX, x), math.Max(maxX, x)
			minY, maxY = math.Min(minY, y), math.Max(maxY, y)
			n++
		}
	}
	if n == 0 {
		return 0, 0, 0, 0, false
	}
	return minX, minY, maxX, maxY, true
}

// sampleSectorPoints returns 1, 2 or 4 points inside a bbox: the centre for
// a small pool, the quarter points along the long axis for a long one, a
// 2x2 grid for a large one — enough coverage that a big lake lights evenly
// without a point-in-polygon test (a sample landing just outside an
// L-shaped sector only shifts a glow a little and is invisible in play).
func sampleSectorPoints(minX, minY, maxX, maxY float64) [][2]float64 {
	w, h := maxX-minX, maxY-minY
	cx, cy := (minX+maxX)/2, (minY+maxY)/2
	const big = 640.0
	switch {
	case w > big && h > big:
		qx, qy := w/4, h/4
		return [][2]float64{
			{cx - qx, cy - qy}, {cx + qx, cy - qy},
			{cx - qx, cy + qy}, {cx + qx, cy + qy},
		}
	case w > big && w >= h:
		q := w / 4
		return [][2]float64{{cx - q, cy}, {cx + q, cy}}
	case h > big:
		q := h / 4
		return [][2]float64{{cx, cy - q}, {cx, cy + q}}
	default:
		return [][2]float64{{cx, cy}}
	}
}
