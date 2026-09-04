package engine

import "math"

// Monster AI, ported from id's p_enemy.c. Phase 2 covers waking up
// (A_Look), target acquisition (P_LookForPlayers via line-of-sight),
// facing (A_FaceTarget), and pathfinding (A_Chase / P_NewChaseDir /
// P_Move). Attacks, pain, death and infighting are Phase 3 — A_Chase
// currently just walks toward the target.

// Move directions, id's dirtype_t. 8 = DI_NODIR ("no direction / stuck").
const (
	diEast = iota
	diNorthEast
	diNorth
	diNorthWest
	diWest
	diSouthWest
	diSouth
	diSouthEast
	diNoDir
)

// per-direction unit velocity (id's xspeed[]/yspeed[]; 0.7 ~= sin 45°).
var dirVX = [9]float64{1, 0.7, 0, -0.7, -1, -0.7, 0, 0.7, 0}
var dirVY = [9]float64{0, 0.7, 1, 0.7, 0, -0.7, -1, -0.7, 0}

// diagonal choices indexed by ((deltaY<0)<<1) | (deltaX>0).
var diags = [4]int{diNorthWest, diNorthEast, diSouthWest, diSouthEast}

const meleeRange = 64.0 // id's MELEERANGE (minus a bit), used by the "wake if adjacent" rule

// aLook is id's A_Look: an idle monster wakes if a recent gunshot's sound
// reached its sector (heardShot / noise.go), or failing that if it can see
// the player in its front arc (pLookForPlayers). On waking it plays its
// sight sound and enters its see (chase) state.
func aLook(g *Game, mo *Mobj) {
	mo.Threshold = 0 // any shot resets the "keep chasing this target" timer

	woke := g.heardShot(mo, g.sectorSoundTarget(mo.X, mo.Y))
	if !woke && !g.pLookForPlayers(mo, false) {
		return
	}
	if s := mo.Info.SeeSound; s != "" {
		g.playSound(s)
	}
	g.setMobjState(mo, mo.Info.SeeState)
}

// heardShot reports whether mo should wake because a gunshot's noise
// reached its sector: targ must exist and be shootable, and a deaf
// (MF_AMBUSH) monster additionally needs line of sight to it. Like id's
// A_Look it points mo.Target at targ even in the deaf-no-sight case (a
// harmless assignment — mo stays idle and pLookForPlayers may overwrite it).
func (g *Game) heardShot(mo, targ *Mobj) bool {
	if targ == nil || targ.Flags&MF_SHOOTABLE == 0 {
		return false
	}
	mo.Target = targ
	return mo.Flags&MF_AMBUSH == 0 || g.pCheckSight(mo, targ)
}

// aChase is id's A_Chase: keep the target, and each call take one step
// toward it (picking a fresh direction when the current one runs out or is
// blocked). Attack checks slot in here in Phase 3.
func aChase(g *Game, mo *Mobj) {
	if mo.ReactionTime > 0 {
		mo.ReactionTime--
	}
	if mo.Threshold > 0 {
		if mo.Target == nil || mo.Target.Health <= 0 {
			mo.Threshold = 0
		} else {
			mo.Threshold--
		}
	}

	turnToMoveDir(mo)

	if mo.Target == nil || mo.Target.Flags&MF_SHOOTABLE == 0 {
		if g.pLookForPlayers(mo, true) {
			return
		}
		g.setMobjState(mo, mo.Info.SpawnState)
		return
	}

	// Don't attack twice in a row — take a step first.
	if mo.Flags&MF_JUSTATTACKED != 0 {
		mo.Flags &^= MF_JUSTATTACKED
		g.pNewChaseDir(mo)
		return
	}

	if mo.Info.MeleeState != S_NULL && g.checkMeleeRange(mo) {
		if s := mo.Info.AttackSound; s != "" {
			g.playSound(s)
		}
		g.setMobjState(mo, mo.Info.MeleeState)
		return
	}
	if mo.Info.MissileState != S_NULL && mo.MoveCount == 0 && g.checkMissileRange(mo) {
		g.setMobjState(mo, mo.Info.MissileState)
		mo.Flags |= MF_JUSTATTACKED
		return
	}

	if mo.MoveCount--; mo.MoveCount < 0 || !g.pMove(mo) {
		g.pNewChaseDir(mo)
	}

	if s := mo.Info.ActiveSound; s != "" && pRandom() < 3 {
		g.playSound(s)
	}
}

// turnToMoveDir rotates mo one 45° step toward the heading its MoveDir
// implies — id's A_Chase "turn towards movement direction if not there yet"
// (actor->angle &= 7<<29; nudge by ANG90/2). This is what makes a chasing
// monster visibly face where it's walking; without it the sprite keeps its
// spawn facing and appears to moonwalk. No-op while stuck (DI_NODIR).
func turnToMoveDir(mo *Mobj) {
	if mo.MoveDir >= diNoDir {
		return
	}
	const step = math.Pi / 4
	want := float64(mo.MoveDir) * step

	a := math.Mod(mo.Angle, 2*math.Pi)
	if a < 0 {
		a += 2 * math.Pi
	}
	cur := math.Floor(a/step) * step // truncate to a 45° multiple

	switch d := normAngle(cur - want); {
	case d > 1e-9:
		mo.Angle = cur - step
	case d < -1e-9:
		mo.Angle = cur + step
	default:
		mo.Angle = cur
	}
}

// aFaceTarget turns mo to look straight at its target.
func aFaceTarget(g *Game, mo *Mobj) {
	if mo.Target == nil {
		return
	}
	mo.Flags &^= MF_AMBUSH
	mo.Angle = math.Atan2(mo.Target.Y-mo.Y, mo.Target.X-mo.X)
}

// pLookForPlayers is id's P_LookForPlayers for a single player: the monster
// notices the player if it has line of sight and the player is either in
// its ~180° front arc or close enough to feel.
func (g *Game) pLookForPlayers(mo *Mobj, allAround bool) bool {
	p := g.playerMobj
	if p == nil || p.Health <= 0 {
		return false
	}
	if !g.pCheckSight(mo, p) {
		return false
	}
	if !allAround {
		an := normAngle(math.Atan2(p.Y-mo.Y, p.X-mo.X) - mo.Angle)
		if an > math.Pi/2 || an < -math.Pi/2 {
			if math.Hypot(p.X-mo.X, p.Y-mo.Y) > meleeRange {
				return false // behind the monster and not right on top of it
			}
		}
	}
	mo.Target = p
	return true
}

// pMove is id's P_Move: step mo one move-distance along mo.MoveDir, if
// P_TryMove allows it. A grounded (non-floating) monster is re-seated on
// the floor afterward.
func (g *Game) pMove(mo *Mobj) bool {
	if mo.MoveDir == diNoDir {
		return false
	}
	speed := mo.Info.Speed
	tryX := mo.X + speed*dirVX[mo.MoveDir]
	tryY := mo.Y + speed*dirVY[mo.MoveDir]
	if !g.pTryMove(mo, tryX, tryY) {
		return false // (Phase 5: try to open a door across this line)
	}
	mo.Flags &^= MF_INFLOAT
	if mo.Flags&MF_FLOAT == 0 {
		mo.Z = mo.FloorZ
	}
	return true
}

// pTryWalk is id's P_TryWalk: attempt a move in the current MoveDir and, on
// success, schedule how long to keep going before reconsidering.
func (g *Game) pTryWalk(mo *Mobj) bool {
	if !g.pMove(mo) {
		return false
	}
	mo.MoveCount = pRandom() & 15
	return true
}

// pNewChaseDir is id's P_NewChaseDir: choose mo.MoveDir to head toward its
// target, trying the direct diagonal, then the two axis directions, then a
// broader search, then a turnaround, before giving up (DI_NODIR).
func (g *Game) pNewChaseDir(mo *Mobj) {
	if mo.Target == nil {
		return
	}
	oldDir := mo.MoveDir
	turnAround := opposite(oldDir)

	dx := mo.Target.X - mo.X
	dy := mo.Target.Y - mo.Y

	var dirX, dirY int = diNoDir, diNoDir
	if dx > 10 {
		dirX = diEast
	} else if dx < -10 {
		dirX = diWest
	}
	if dy < -10 {
		dirY = diSouth
	} else if dy > 10 {
		dirY = diNorth
	}

	// Direct diagonal.
	if dirX != diNoDir && dirY != diNoDir {
		bx := 0
		if dx > 0 {
			bx = 1
		}
		by := 0
		if dy < 0 {
			by = 1
		}
		mo.MoveDir = diags[(by<<1)|bx]
		if mo.MoveDir != turnAround && g.pTryWalk(mo) {
			return
		}
	}

	// Try the longer axis first (with a random tie-break, like id).
	if pRandom() > 200 || math.Abs(dy) > math.Abs(dx) {
		dirX, dirY = dirY, dirX
	}
	if dirX == turnAround {
		dirX = diNoDir
	}
	if dirY == turnAround {
		dirY = diNoDir
	}

	if dirX != diNoDir {
		mo.MoveDir = dirX
		if g.pTryWalk(mo) {
			return
		}
	}
	if dirY != diNoDir {
		mo.MoveDir = dirY
		if g.pTryWalk(mo) {
			return
		}
	}

	// No path toward the target — resume the old direction, then search.
	if oldDir != diNoDir {
		mo.MoveDir = oldDir
		if g.pTryWalk(mo) {
			return
		}
	}
	if pRandom()&1 != 0 {
		for d := diEast; d <= diSouthEast; d++ {
			if d != turnAround {
				mo.MoveDir = d
				if g.pTryWalk(mo) {
					return
				}
			}
		}
	} else {
		for d := diSouthEast; d >= diEast; d-- {
			if d != turnAround {
				mo.MoveDir = d
				if g.pTryWalk(mo) {
					return
				}
			}
		}
	}
	if turnAround != diNoDir {
		mo.MoveDir = turnAround
		if g.pTryWalk(mo) {
			return
		}
	}
	mo.MoveDir = diNoDir
}

// --- Phase 3: attack range checks and per-monster attack actions ---

// checkMeleeRange is id's P_CheckMeleeRange: target within a claw's reach
// and visible.
func (g *Game) checkMeleeRange(mo *Mobj) bool {
	t := mo.Target
	if t == nil {
		return false
	}
	if math.Hypot(t.X-mo.X, t.Y-mo.Y) >= meleeRange-20+t.Radius {
		return false
	}
	return g.pCheckSight(mo, t)
}

// checkMissileRange is id's P_CheckMissileRange, simplified: needs sight,
// then a distance-weighted random roll (closer = more likely to fire).
func (g *Game) checkMissileRange(mo *Mobj) bool {
	if mo.Target == nil || !g.pCheckSight(mo, mo.Target) {
		return false
	}
	if mo.Flags&MF_JUSTHIT != 0 {
		mo.Flags &^= MF_JUSTHIT
		return true // hit back
	}
	if mo.ReactionTime > 0 {
		return false
	}
	dist := math.Hypot(mo.Target.X-mo.X, mo.Target.Y-mo.Y) - 64
	if mo.Info.MeleeState == S_NULL {
		dist -= 128 // no melee — close the gap in its decision-making
	}
	if mo.Type == MT_SKULL {
		dist /= 2
	}
	if dist > 200 {
		dist = 200
	}
	return pRandom() >= int(dist)
}

// bulletSpread is id's (P_Random - P_Random) << 20 hitscan cone, in radians.
func bulletSpread() float64 { return float64(pRandomSpread()) / 4096.0 * 2 * math.Pi }

func aPosAttack(g *Game, mo *Mobj) {
	if mo.Target == nil {
		return
	}
	aFaceTarget(g, mo)
	g.playSound("DSPISTOL")
	g.pLineAttack(mo, mo.Angle+bulletSpread(), pAimSlope(mo, mo.Target), missileRange, (pRandom()%5+1)*3)
}

func aSPosAttack(g *Game, mo *Mobj) {
	if mo.Target == nil {
		return
	}
	aFaceTarget(g, mo)
	g.playSound("DSSHOTGN")
	slope := pAimSlope(mo, mo.Target)
	for i := 0; i < 3; i++ {
		g.pLineAttack(mo, mo.Angle+bulletSpread(), slope, missileRange, (pRandom()%5+1)*3)
	}
}

func aCPosAttack(g *Game, mo *Mobj) {
	if mo.Target == nil {
		return
	}
	g.playSound("DSSHOTGN")
	aFaceTarget(g, mo)
	g.pLineAttack(mo, mo.Angle+bulletSpread(), pAimSlope(mo, mo.Target), missileRange, (pRandom()%5+1)*3)
}

func aCPosRefire(g *Game, mo *Mobj) {
	aFaceTarget(g, mo)
	if pRandom() < 40 {
		return
	}
	if mo.Target == nil || mo.Target.Health <= 0 || !g.pCheckSight(mo, mo.Target) {
		g.setMobjState(mo, mo.Info.SeeState)
	}
}

func aTroopAttack(g *Game, mo *Mobj) {
	if mo.Target == nil {
		return
	}
	aFaceTarget(g, mo)
	if g.checkMeleeRange(mo) {
		g.playSound("DSCLAW")
		g.pDamageMobj(mo.Target, mo, mo, (pRandom()%8+1)*3)
		return
	}
	g.pSpawnMissile(mo, mo.Target, MT_TROOPSHOT)
}

func aSargAttack(g *Game, mo *Mobj) {
	if mo.Target == nil {
		return
	}
	aFaceTarget(g, mo)
	if g.checkMeleeRange(mo) {
		g.pDamageMobj(mo.Target, mo, mo, (pRandom()%10+1)*4)
	}
}

func aHeadAttack(g *Game, mo *Mobj) {
	if mo.Target == nil {
		return
	}
	aFaceTarget(g, mo)
	if g.checkMeleeRange(mo) {
		g.pDamageMobj(mo.Target, mo, mo, (pRandom()%6+1)*10)
		return
	}
	g.pSpawnMissile(mo, mo.Target, MT_HEADSHOT)
}

func aBruisAttack(g *Game, mo *Mobj) {
	if mo.Target == nil {
		return
	}
	if g.checkMeleeRange(mo) {
		g.playSound("DSCLAW")
		g.pDamageMobj(mo.Target, mo, mo, (pRandom()%8+1)*10)
		return
	}
	g.pSpawnMissile(mo, mo.Target, MT_BRUISERSHOT)
}

const skullSpeed = 20.0

func aSkullAttack(g *Game, mo *Mobj) {
	if mo.Target == nil {
		return
	}
	dest := mo.Target
	mo.Flags |= MF_SKULLFLY
	if mo.Info.AttackSound != "" {
		g.playSound(mo.Info.AttackSound)
	}
	aFaceTarget(g, mo)
	mo.MomX = skullSpeed * math.Cos(mo.Angle)
	mo.MomY = skullSpeed * math.Sin(mo.Angle)
	dist := math.Hypot(dest.X-mo.X, dest.Y-mo.Y) / skullSpeed
	if dist < 1 {
		dist = 1
	}
	mo.MomZ = (dest.Z + dest.Height/2 - mo.Z) / dist
}

// Death / misc actions.
func aScream(g *Game, mo *Mobj) {
	if mo.Info.DeathSound != "" {
		g.playSound(mo.Info.DeathSound)
	}
}
func aXScream(g *Game, mo *Mobj) { g.playSound("DSSLOP") }
func aPain(g *Game, mo *Mobj) {
	if mo.Info.PainSound != "" {
		g.playSound(mo.Info.PainSound)
	}
}
func aFall(g *Game, mo *Mobj)    { mo.Flags &^= MF_SOLID }
func aExplode(g *Game, mo *Mobj) { g.pRadiusAttack(mo, mo.Target, 128) }

// aBossDeath is id's A_BossDeath: on a designated "boss map", when the last
// living boss of mo's type dies (and a player is still alive), it triggers
// that map's scripted payoff — a tagged floor moving, or the level exit.
// Wired to the Baron of Hell's final death frame (S_BOSS_DIE7).
//
// Vanilla fires this from several bosses; only the ones this engine has a
// monster for are active here:
//
//   - E1M8  + Baron    -> EV_DoFloor(tag 666, lowerFloorToLowest)   [ported]
//   - E2M8  + Cyberdemon        -> G_ExitLevel                       (no MT_CYBORG yet)
//   - E3M8  + Spider Mastermind -> G_ExitLevel                       (no MT_SPIDER yet)
//   - E4M8  + Spider Mastermind -> EV_DoFloor(666, lowerToLowest)    (no MT_SPIDER yet)
//   - MAP07 + Mancubus     -> EV_DoFloor(tag 666, lowerToLowest)     (no MT_FATSO yet)
//   - MAP07 + Arachnotron  -> EV_DoFloor(tag 667, raiseToTexture)    (no MT_BABY yet)
func aBossDeath(g *Game, mo *Mobj) {
	if g.Level == nil {
		return
	}

	// Decide what this (map, boss type) pairing does; bail if it's not a
	// scripted combination.
	var trigger func()
	switch g.Level.Name {
	case "E1M8":
		if mo.Type == MT_BRUISER {
			trigger = func() { g.floorTagged(666, ftLowestFloor, false, 0) }
		}
	}
	if trigger == nil {
		return
	}

	// Need a live player for the "victory" to mean anything.
	if g.Player.Health <= 0 {
		return
	}
	// Every other boss of this exact type must already be dead.
	for _, other := range g.mobjs {
		if other != mo && !other.removed && other.Type == mo.Type && other.Health > 0 {
			return
		}
	}
	trigger()
}

// opposite returns the 180° direction, or DI_NODIR unchanged.
func opposite(d int) int {
	if d == diNoDir {
		return diNoDir
	}
	return (d + 4) & 7
}

// normAngle wraps a radian angle to (-π, π].
func normAngle(a float64) float64 {
	a = math.Mod(a, 2*math.Pi)
	if a > math.Pi {
		a -= 2 * math.Pi
	} else if a <= -math.Pi {
		a += 2 * math.Pi
	}
	return a
}
