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
//
// hpuff is the Heretic puff sprite the impact spark uses (PUF2 = the Gold
// Wand's little yellow flame, PUF3 = the staff spark, PUF1 = the green
// Gauntlets spark). It is only consulted for a Heretic session — the Doom
// MT_PUFF sprite name "PUFF" isn't in heretic.wad, so without this a
// Heretic hitscan/melee weapon lands with no visible effect at all. Doom
// callers pass "".
func (g *Game) pLineAttack(shooter *Mobj, angle, slope, rangeU float64, damage int, hpuff string) {
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
		g.spawnAttackPuff(hx-dx*4, hy-dy*4, hz, hpuff)
		// Shot a wall — trip its gun (G1/GR) special, if any.
		if wallLine >= 0 && shooter == g.playerMobj {
			g.shotLine(wallLine, shooter)
		}
	case best.Flags&MF_NOBLOOD != 0:
		g.pDamageMobj(best, shooter, shooter, damage)
		g.spawnAttackPuff(hx, hy, hz, hpuff)
	default:
		g.pDamageMobj(best, shooter, shooter, damage)
		g.spawnAttackBlood(hx, hy, hz)
	}
}

// hereticPuffFrames is how many animation frames of each Heretic puff
// sprite the impact spark plays (heretic.wad: PUFn A.. lumps) — the frame
// cycler in mobjTick needs it, and it must be > 1 to run at all. Some are
// deliberately shorter than the full lump run: PUF2's last two frames are a
// spread red cross-spark that reads as an explosion up close, so the Wand
// spark stops at the orange frames.
var hereticPuffFrames = map[string]int{
	"PUF1": 4, // Gauntlets — green sparks
	"PUF2": 3, // Gold Wand — a little yellow-to-orange flame (skips the red cross)
	"PUF3": 4, // Elven Staff — a small tan spark
	"PUF4": 6, // powered Staff — big blue flare
}

// spawnAttackPuff drops the little hit spark a hitscan/melee attack leaves
// at its impact point. Doom uses MT_PUFF as-is; Heretic re-skins it to a
// real Heretic puff sprite (see pLineAttack's hpuff doc) because "PUFF"
// isn't a lump in heretic.wad. Full-bright, animates once, then removes
// itself (hExpireIn) rather than running the S_PUFF state chain.
func (g *Game) spawnAttackPuff(x, y, z float64, hpuff string) {
	mo := g.P_SpawnMobj(x, y, z, MT_PUFF)
	if !g.heretic {
		return
	}
	if hpuff == "" {
		hpuff = "PUF3"
	}
	frames := hereticPuffFrames[hpuff]
	if frames < 2 {
		frames = 4
	}
	mo.spriteOverride = hpuff
	mo.overrideFrames, mo.overrideTics = frames, 3
	mo.Fullbright = true
	mo.Tics = -1 // hExpireIn owns the lifetime; keep the S_PUFF countdown out
	mo.hExpireIn = frames * 3
}

// spawnAttackBlood is spawnAttackPuff's blood-splat counterpart: MT_BLOOD
// for Doom, re-skinned to Heretic's BLOD sprite otherwise (heretic.wad has
// no "BLUD").
func (g *Game) spawnAttackBlood(x, y, z float64) {
	mo := g.P_SpawnMobj(x, y, z, MT_BLOOD)
	if !g.heretic {
		return
	}
	mo.spriteOverride = "BLOD"
	mo.overrideFrames, mo.overrideTics = 3, 3
	mo.Tics = -1
	mo.hExpireIn = 3 * 3
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

// bfgSprayHalfFanAngle/bfgSprayDamageDie are the BFG ball's signature
// "spray": on impact, a wide fan of tracers reaching out from the SHOOTER
// (not the ball's landing spot) so every monster the player can see in
// front of them takes a second hit — the reason the BFG can flatten a
// whole room instead of only the one monster the visible ball struck. Real
// Doom fires this as 40 discrete rays across a 90-degree fan and lets
// whichever ray reaches a target first damage it; aBFGSpray instead hits
// every shootable, visible thing in the fan once — equivalent in effect
// (40 rays densely cover a room-sized fan anyway) without needing to track
// which of 40 rays would have reached which target first.
const (
	bfgSprayHalfFanAngle = 45 * math.Pi / 180 // half of id's 90-degree total fan
	bfgSprayDamageDie    = 8                  // (P_Random()%8+1)*15 per target hit, id's own constant
)

// aBFGSpray damages every shootable, living thing within missileRange that
// shooter can both see and is looking roughly toward (within
// bfgSprayHalfFanAngle either side of its facing) — id's A_BFGSpray.
func (g *Game) aBFGSpray(shooter *Mobj) {
	hit := func(mo *Mobj) {
		if mo == nil || mo == shooter {
			return
		}
		dist, ok := shootableInCone(mo, shooter.X, shooter.Y, shooter.Angle, missileRange, bfgSprayHalfFanAngle)
		if !ok || dist <= 0 {
			return
		}
		if !g.pCheckSight(shooter, mo) {
			return
		}
		g.pDamageMobj(mo, shooter, shooter, (pRandom()%bfgSprayDamageDie+1)*15)
	}
	for _, mo := range g.mobjs {
		hit(mo)
	}
}
