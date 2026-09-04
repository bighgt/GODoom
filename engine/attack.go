package engine

import (
	"math"

	"twopointfive/wad"
)

// Attacks, ported from id's p_map.c / p_enemy.c: hitscan (P_LineAttack),
// missiles (P_SpawnMissile), and blast radius (P_RadiusAttack).

const missileRange = 2048.0 // id's MISSILERANGE (32 * 64)

// eyeZ is the height a shot leaves a shooter from — id's z + height*3/4.
func eyeZ(mo *Mobj) float64 { return mo.Z + mo.Height*0.75 }

// pAimSlope is the vertical slope from shooter's eye to the centre of
// target, for monster auto-aim.
func pAimSlope(shooter, target *Mobj) float64 {
	dist := math.Hypot(target.X-shooter.X, target.Y-shooter.Y)
	if dist < 1 {
		dist = 1
	}
	return (target.Z + target.Height/2 - eyeZ(shooter)) / dist
}

// pLineAttack fires an instant shot from shooter along (angle, slope) for
// up to rangeU units. The first shootable thing in the way takes damage
// (blood, or a puff for MF_NOBLOOD things like the barrel); if nothing is
// hit before a wall, a puff marks the wall.
func (g *Game) pLineAttack(shooter *Mobj, angle, slope, rangeU float64, damage int) {
	dx, dy := math.Cos(angle), math.Sin(angle)
	ox, oy, oz := shooter.X, shooter.Y, eyeZ(shooter)

	// Nearest wall along the ray (caps the trace distance).
	wallT := rangeU
	wallLine := -1
	ex, ey := ox+dx*rangeU, oy+dy*rangeU
	g.blockGrid.forEachAlongSegment(ox, oy, ex, ey, func(i int32) bool {
		ld := &g.Level.Linedefs[i]
		v1 := g.Level.Vertexes[ld.StartVertex]
		v2 := g.Level.Vertexes[ld.EndVertex]
		t, ok := rayIntersectsSegment(ox, oy, dx, dy,
			float64(v1.X), float64(v1.Y), float64(v2.X), float64(v2.Y))
		if !ok || t <= 0 || t >= wallT {
			return true
		}
		if ld.BackSidedef == wad.NoSidedef {
			wallT, wallLine = t, int(i)
			return true
		}
		open := g.wallOpeningAt(ld)
		z := oz + slope*t
		if !open.twoSided || z <= open.bottom || z >= open.top {
			wallT, wallLine = t, int(i)
		}
		return true
	})

	// Nearest shootable thing before that wall.
	var best *Mobj
	bestT := wallT
	consider := func(mo *Mobj) {
		if mo == nil || mo == shooter || mo.removed || mo.Flags&MF_SHOOTABLE == 0 || mo.Health <= 0 {
			return
		}
		rx, ry := mo.X-ox, mo.Y-oy
		t := rx*dx + ry*dy
		if t <= 0 || t >= bestT {
			return
		}
		if math.Abs(rx*dy-ry*dx) > mo.Radius {
			return
		}
		z := oz + slope*t
		if z < mo.Z || z > mo.Z+mo.Height {
			return
		}
		best, bestT = mo, t
	}
	if shooter != g.playerMobj {
		consider(g.playerMobj)
	}
	for _, mo := range g.mobjs {
		consider(mo)
	}

	hx, hy, hz := ox+dx*bestT, oy+dy*bestT, oz+slope*bestT
	switch {
	case best == nil:
		g.P_SpawnMobj(hx-dx*4, hy-dy*4, hz, MT_PUFF)
		// Shot a wall — trip its gun (G1/GR) special, if any.
		if wallLine >= 0 && shooter == g.playerMobj {
			g.shotLine(wallLine, shooter)
		}
	case best.Flags&MF_NOBLOOD != 0:
		g.pDamageMobj(best, shooter, shooter, damage)
		g.P_SpawnMobj(hx, hy, hz, MT_PUFF)
	default:
		g.pDamageMobj(best, shooter, shooter, damage)
		g.P_SpawnMobj(hx, hy, hz, MT_BLOOD)
	}
}

// pSpawnMissile launches a missile of type typ from source toward dest,
// arcing so it converges on dest's centre — id's P_SpawnMissile.
func (g *Game) pSpawnMissile(source, dest *Mobj, typ mobjType) *Mobj {
	th := g.P_SpawnMobj(source.X, source.Y, source.Z+32, typ)
	if th.Info.SeeSound != "" {
		g.playSound(th.Info.SeeSound)
	}
	th.Target = source

	an := math.Atan2(dest.Y-source.Y, dest.X-source.X)
	if dest.Flags&MF_SHADOW != 0 {
		an += (prng.Float64()*2 - 1) * (math.Pi / 16) // spectre — spray wide
	}
	th.Angle = an

	sp := th.Info.Speed
	th.MomX = sp * math.Cos(an)
	th.MomY = sp * math.Sin(an)
	dist := math.Hypot(dest.X-source.X, dest.Y-source.Y) / sp
	if dist < 1 {
		dist = 1
	}
	th.MomZ = (dest.Z + dest.Height/2 - (source.Z + 32)) / dist
	return th
}

// pRadiusAttack damages every shootable thing within `damage` units of
// spot, falling off linearly with distance — id's P_RadiusAttack (barrels,
// rockets).
func (g *Game) pRadiusAttack(spot, source *Mobj, damage int) {
	rng := float64(damage)
	hit := func(mo *Mobj) {
		if mo == nil || mo.removed || mo.Flags&MF_SHOOTABLE == 0 || mo.Health <= 0 {
			return
		}
		d := math.Max(math.Abs(mo.X-spot.X), math.Abs(mo.Y-spot.Y)) - mo.Radius
		if d < 0 {
			d = 0
		}
		if d >= rng {
			return
		}
		if !g.pCheckSight(mo, spot) {
			return
		}
		g.pDamageMobj(mo, spot, source, int(rng-d))
	}
	hit(g.playerMobj)
	for _, mo := range g.mobjs {
		hit(mo)
	}
}
