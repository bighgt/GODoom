package engine

import (
	"log"
	"math"
	"strconv"
	"strings"
)

// Data-driven Heretic monsters. Heretic's mobjs behave like Doom's
// (wake/chase/melee/missile/pain/death), so this reuses the ported Doom AI
// wholesale — aLook, aChase, pMove, pNewChaseDir, checkMeleeRange,
// checkMissileRange, pDamageMobj, pKillMobj all run unchanged. What's new
// is only the DATA: each monster is a hereticActorDef with GZDoom-style
// animation strings, and a synthesized *MobjInfo whose state links point at
// the S_HERETIC_* sentinels (see states.go). setMobjState intercepts a
// sentinel and calls hSetPhase, so a Doom action calling
// setMobjState(mo, mo.Info.MeleeState) transparently drives this FSM.
//
// State strings use GZDoom's DECORATE shorthand, taken from gzdoom's
// wadsrc/static/zscript/actors/heretic/*.zs: "MUMM ABCD 4 chase" is four
// frames (A,B,C,D) of 4 tics each, each running the `chase` action; a
// trailing "P -1" frame freezes (the last death frame). "Bright" after the
// letters makes the run full-bright.

// hPhase is which animation list a Heretic actor is currently playing.
type hPhase int

const (
	hpSpawn hPhase = iota // idle + A_Look
	hpSee                 // chase
	hpMelee
	hpMissile
	hpPain
	hpDeath
	hpXDeath
)

func hPhaseForSentinel(st stateNum) hPhase {
	switch st {
	case S_HERETIC_SEE:
		return hpSee
	case S_HERETIC_MELEE:
		return hpMelee
	case S_HERETIC_MISSILE:
		return hpMissile
	case S_HERETIC_PAIN:
		return hpPain
	case S_HERETIC_DEATH:
		return hpDeath
	case S_HERETIC_XDEATH:
		return hpXDeath
	default:
		return hpSpawn
	}
}

// hFrame is one animation frame: a 0-based sprite frame index (A=0), a tic
// duration (-1 = freeze), full-bright, and an optional action.
type hFrame struct {
	frame  int
	tics   int
	bright bool
	act    func(g *Game, mo *Mobj)
}

// hActions maps a DECORATE action token to the engine function that stands
// in for it. Unknown tokens are ignored (logged once).
var hActions = map[string]func(g *Game, mo *Mobj){
	"look":    aLook,
	"chase":   aChase,
	"face":    aFaceTarget,
	"melee":   hDoMelee,
	"missile": hDoMissile,
	"combo":   hDoCombo,
	"boss":    hDoBossAttack,
	"scream":  func(g *Game, mo *Mobj) { hPlay(g, mo.hact.deathSound) },
	"pain":    func(g *Game, mo *Mobj) { hPlay(g, mo.hact.painSound) },
	"noblock": func(g *Game, mo *Mobj) { mo.Flags &^= MF_SOLID },
	"active":  func(g *Game, mo *Mobj) { hPlay(g, mo.hact.activeSound) },
}

func hPlay(g *Game, s string) {
	if s != "" {
		g.playSound(s)
	}
}

var loggedHAction = map[string]bool{}

// parseHFrames turns "SPR ABCD 4 act; SPR E -1" into a flat []hFrame.
func parseHFrames(spec string) []hFrame {
	var out []hFrame
	for _, seg := range strings.Split(spec, ";") {
		f := strings.Fields(seg)
		if len(f) < 3 {
			continue
		}
		letters := f[1]
		bright := false
		tokIdx := 2
		// optional "Bright" between the letters and the tic count
		if strings.EqualFold(f[2], "bright") {
			bright = true
			tokIdx = 3
		}
		if tokIdx >= len(f) {
			continue
		}
		tics, err := strconv.Atoi(f[tokIdx])
		if err != nil {
			continue
		}
		var act func(g *Game, mo *Mobj)
		if tokIdx+1 < len(f) {
			name := strings.ToLower(f[tokIdx+1])
			a, ok := hActions[name]
			if !ok && !loggedHAction[name] {
				loggedHAction[name] = true
				log.Printf("engine: heretic: unknown frame action %q (ignored)", name)
			}
			act = a
		}
		for i := 0; i < len(letters); i++ {
			c := letters[i]
			idx := int(c - 'A')
			if c >= 'a' && c <= 'z' {
				idx = int(c-'a') + 26
			}
			out = append(out, hFrame{frame: idx, tics: tics, bright: bright, act: act})
		}
	}
	return out
}

// hereticActorDef is one monster's full behaviour + data.
type hereticActorDef struct {
	name           string
	doomednum      int
	sprite         string
	health         int
	radius, height float64
	speed          float64
	mass           int
	painChance     int
	flags          int // extra MF_* (MF_NOGRAVITY flier, MF_SHADOW ghost, MF_NOBLOOD)
	meleeLo        int // A_CustomMeleeAttack random(lo,hi); 0 = no melee
	meleeHi        int
	missile        *hMissileDef
	// bossMissiles, when non-empty, makes the "boss" frame action pick a
	// random one of these to fire (Iron Lich / Maulotaur / D'Sparil, which
	// each have several attack types). Overrides missile for that action.
	bossMissiles []*hMissileDef

	seeSound, attackSound, painSound, deathSound, activeSound string

	// animation specs (parsed once into the slices below)
	sSpawn, sSee, sMelee, sMissile, sPain, sDeath, sXDeath string
	aSpawn, aSee, aMelee, aMissile, aPain, aDeath, aXDeath []hFrame

	info *MobjInfo // synthesized once (see buildHereticInfo)
}

// hMissileDef is a Heretic monster projectile.
type hMissileDef struct {
	sprite         string
	speed          float64
	radius, height float64
	damage         int
	frames         int // flight-loop frame count (cycled off the level clock)
	tics           int // tics per flight frame
	sound          string
	// impact animation: on a wall/thing hit the missile freezes, swaps to
	// deathSprite and plays deathFrames*deathTics tics before vanishing.
	deathSprite string
	deathFrames int
	deathTics   int
	deathSound  string
}

func (d *hereticActorDef) phaseFrames(p hPhase) []hFrame {
	switch p {
	case hpSee:
		return d.aSee
	case hpMelee:
		return d.aMelee
	case hpMissile:
		return d.aMissile
	case hpPain:
		return d.aPain
	case hpDeath:
		return d.aDeath
	case hpXDeath:
		if len(d.aXDeath) > 0 {
			return d.aXDeath
		}
		return d.aDeath
	default:
		return d.aSpawn
	}
}

// --- the FSM ---------------------------------------------------------------

// hSetPhase switches a Heretic actor to phase p and seats its first frame.
func (g *Game) hSetPhase(mo *Mobj, p hPhase) {
	mo.hPhase = p
	mo.hFrameIdx = 0
	g.hEnterFrame(mo)
}

// hEnterFrame applies the current (hPhase, hFrameIdx) frame — sprite frame,
// brightness, tic timer — then runs its action. If the action changed the
// phase (a Doom AI call routed through setMobjState), it already seated the
// new frame, so we don't clobber it.
func (g *Game) hEnterFrame(mo *Mobj) {
	anim := mo.hact.phaseFrames(mo.hPhase)
	if mo.hFrameIdx < 0 || mo.hFrameIdx >= len(anim) {
		return
	}
	f := anim[mo.hFrameIdx]
	mo.Frame = f.frame
	mo.Fullbright = f.bright
	if f.tics < 0 {
		mo.hFrameTic = 1 << 30
	} else {
		mo.hFrameTic = f.tics
	}
	if f.act != nil {
		before := mo.hPhase
		f.act(g, mo)
		if mo.hPhase != before || mo.removed {
			return
		}
	}
}

// hereticActorTick advances one Heretic actor by a tic: floor re-seat, then
// the phase animation (looping idle/see, "goto see" after
// melee/missile/pain, freeze on the last death frame).
func (g *Game) hereticActorTick(mo *Mobj) {
	g.mobjZClip(mo) // fliers (MF_NOGRAVITY) are exempt inside

	if mo.hFrameTic > 0 {
		mo.hFrameTic--
		if mo.hFrameTic > 0 {
			return
		}
	}
	anim := mo.hact.phaseFrames(mo.hPhase)
	mo.hFrameIdx++
	if mo.hFrameIdx >= len(anim) {
		switch mo.hPhase {
		case hpSpawn, hpSee:
			mo.hFrameIdx = 0 // loop
		case hpMelee, hpMissile, hpPain:
			g.setMobjState(mo, mo.Info.SeeState) // "Goto See"
			return
		default: // death / xdeath — hold the last frame forever
			mo.hFrameIdx = len(anim) - 1
			mo.hFrameTic = 1 << 30
			return
		}
	}
	g.hEnterFrame(mo)
}

// hDoMelee is A_CustomMeleeAttack: face, and if the target is in reach deal
// random(meleeLo, meleeHi) damage.
func hDoMelee(g *Game, mo *Mobj) {
	if mo.Target == nil {
		return
	}
	aFaceTarget(g, mo)
	if !g.checkMeleeRange(mo) {
		return
	}
	hPlay(g, mo.hact.attackSound)
	lo, hi := mo.hact.meleeLo, mo.hact.meleeHi
	dmg := lo
	if hi > lo {
		dmg = lo + pRandom()%(hi-lo+1)
	}
	g.pDamageMobj(mo.Target, mo, mo, dmg)
}

// hDoMissile is A_...Attack: face and launch the def's projectile.
func hDoMissile(g *Game, mo *Mobj) {
	if mo.Target == nil || mo.hact.missile == nil {
		return
	}
	aFaceTarget(g, mo)
	g.spawnHereticMissile(mo, mo.Target, mo.hact.missile)
}

// hDoBossAttack fires a random one of the boss's attack types (or its
// melee, if it has one and the target is close).
func hDoBossAttack(g *Game, mo *Mobj) {
	if mo.Target == nil {
		return
	}
	aFaceTarget(g, mo)
	if mo.hact.meleeHi > 0 && g.checkMeleeRange(mo) {
		hDoMelee(g, mo)
		return
	}
	m := mo.hact.bossMissiles
	if len(m) == 0 {
		hDoMissile(g, mo)
		return
	}
	g.spawnHereticMissile(mo, mo.Target, m[pRandom()%len(m)])
}

// hDoCombo is A_CustomComboAttack: melee if in range, else a missile.
func hDoCombo(g *Game, mo *Mobj) {
	if mo.Target == nil {
		return
	}
	aFaceTarget(g, mo)
	if mo.hact.meleeHi > 0 && g.checkMeleeRange(mo) {
		hDoMelee(g, mo)
		return
	}
	hDoMissile(g, mo)
}

// spawnHereticMissile launches md from source toward dest (id's
// P_SpawnMissile arc), as an MT_HERETIC mobj — moveMissile drives it and
// explodeMissile just removes it on impact.
func (g *Game) spawnHereticMissile(source, dest *Mobj, md *hMissileDef) *Mobj {
	th := g.P_SpawnMobj(source.X, source.Y, source.Z+32, MT_HERETIC)
	th.Flags = MF_MISSILE | MF_NOGRAVITY | MF_NOBLOCKMAP | MF_DROPOFF
	th.Radius, th.Height = md.radius, md.height
	th.spriteOverride = md.sprite
	th.overrideFrames, th.overrideTics = md.frames, md.tics
	th.Fullbright = true
	th.hDamage = md.damage
	th.hMissile = md
	th.Target = source
	th.Tics = -1
	hPlay(g, md.sound)

	an := math.Atan2(dest.Y-source.Y, dest.X-source.X)
	if dest.Flags&MF_SHADOW != 0 {
		an += (prng.Float64()*2 - 1) * (math.Pi / 16)
	}
	th.Angle = an
	sp := md.speed
	th.MomX, th.MomY = sp*math.Cos(an), sp*math.Sin(an)
	dist := math.Hypot(dest.X-source.X, dest.Y-source.Y) / sp
	if dist < 1 {
		dist = 1
	}
	th.MomZ = (dest.Z + dest.Height/2 - (source.Z + 32)) / dist
	return th
}

// spawnHereticMonster spawns the actor for doomednum dn at (x,y), angle in
// degrees. Returns false if dn isn't a Heretic monster.
func (g *Game) spawnHereticMonster(dn int, x, y, angleDeg float64, ambush bool) bool {
	d := hereticMonsters[dn]
	if d == nil {
		return false
	}
	mo := g.P_SpawnMobj(x, y, onFloorZ, MT_HERETIC)
	mo.Info = d.info
	mo.hact = d
	mo.Type = MT_HERETIC
	mo.spriteOverride = d.sprite
	mo.Health = d.health
	mo.Radius, mo.Height = d.radius, d.height
	mo.Flags = monsterFlags | d.flags
	mo.ReactionTime = 8
	mo.Angle = angleDeg * math.Pi / 180
	if ambush {
		mo.Flags |= MF_AMBUSH
	}
	mo.Tics = -1
	g.hSetPhase(mo, hpSpawn)
	return true
}

// buildHereticInfo synthesizes the *MobjInfo a Heretic actor's Mobj points
// at, so the ported Doom AI sees real numbers and sentinel state links.
func buildHereticInfo(d *hereticActorDef) *MobjInfo {
	melee := stateNum(S_NULL)
	if d.meleeHi > 0 {
		melee = S_HERETIC_MELEE
	}
	missile := stateNum(S_NULL)
	if d.missile != nil || len(d.bossMissiles) > 0 {
		missile = S_HERETIC_MISSILE
	}
	xdeath := stateNum(S_NULL)
	if d.sXDeath != "" {
		xdeath = S_HERETIC_XDEATH
	}
	return &MobjInfo{
		Name: d.name, DoomedNum: -1,
		SpawnState: S_HERETIC, SeeState: S_HERETIC_SEE,
		MeleeState: melee, MissileState: missile,
		PainState: S_HERETIC_PAIN, DeathState: S_HERETIC_DEATH, XDeathState: xdeath,
		SpawnHealth: d.health, ReactionTime: 8, PainChance: d.painChance,
		Speed: d.speed, Radius: d.radius, Height: d.height, Mass: d.mass,
		SeeSound: d.seeSound, AttackSound: d.attackSound, PainSound: d.painSound,
		DeathSound: d.deathSound, ActiveSound: d.activeSound,
		Flags: monsterFlags | d.flags,
	}
}

func init() {
	for _, d := range hereticMonsters {
		d.aSpawn = parseHFrames(d.sSpawn)
		d.aSee = parseHFrames(d.sSee)
		d.aMelee = parseHFrames(d.sMelee)
		d.aMissile = parseHFrames(d.sMissile)
		d.aPain = parseHFrames(d.sPain)
		d.aDeath = parseHFrames(d.sDeath)
		d.aXDeath = parseHFrames(d.sXDeath)
		d.info = buildHereticInfo(d)
	}
}

// hereticMonsters — every Heretic monster in the retail IWAD, keyed by
// GZDoom Heretic DoomEdNum. Ghost variants share a def with +MF_SHADOW.
// Numbers/frames from gzdoom's zscript/actors/heretic/*.zs.
var hereticMonsters = map[int]*hereticActorDef{
	// Gargoyle (Heretic Imp) — flies, claw only.
	66: {
		name: "gargoyle", sprite: "IMPX", health: 40, radius: 16, height: 36,
		speed: 10, mass: 50, painChance: 200, flags: MF_NOGRAVITY,
		meleeLo: 5, meleeHi: 5,
		seeSound: "impsit", attackSound: "impat1", painSound: "imppai", deathSound: "impdth",
		sSpawn:  "IMPX ABCB 10 look",
		sSee:    "IMPX AABBCCBB 3 chase",
		sMelee:  "IMPX DE 6 face; IMPX F 6 melee",
		sPain:   "IMPX G 3; IMPX G 3 pain",
		sDeath:  "IMPX G 4 scream; IMPX H 5 noblock; IMPX I 5; IMPX J -1",
		sXDeath: "IMPX S 5 scream; IMPX TU 5; IMPX V 5 noblock; IMPX W -1",
	},
	// Fire Gargoyle (Heretic Imp Leader) — flies, fireball.
	5: {
		name: "fire gargoyle", sprite: "IMPX", health: 80, radius: 16, height: 36,
		speed: 10, mass: 50, painChance: 200, flags: MF_NOGRAVITY,
		missile:  &hMissileDef{sprite: "FX10", speed: 10 * 35 / 8, radius: 8, height: 8, damage: 1, frames: 3, tics: 6, sound: "impat1"},
		seeSound: "impsit", attackSound: "impat1", painSound: "imppai", deathSound: "impdth",
		sSpawn:   "IMPX ABCB 10 look",
		sSee:     "IMPX AABBCCBB 3 chase",
		sMissile: "IMPX DE 6 face; IMPX F 6 combo",
		sPain:    "IMPX G 3; IMPX G 3 pain",
		sDeath:   "IMPX G 4 scream; IMPX H 5 noblock; IMPX I 5; IMPX J -1",
	},
	// Golem (Mummy) — melee.
	68: {
		name: "golem", sprite: "MUMM", health: 80, radius: 22, height: 62,
		speed: 12, mass: 75, painChance: 128,
		meleeLo: 2, meleeHi: 14,
		seeSound: "mumsit", attackSound: "mumat1", painSound: "mumpai", deathSound: "mumdth", activeSound: "mumact",
		sSpawn: "MUMM AB 10 look",
		sSee:   "MUMM ABCD 4 chase",
		sMelee: "MUMM E 6 face; MUMM F 6 melee; MUMM G 6",
		sPain:  "MUMM H 4; MUMM H 4 pain",
		sDeath: "MUMM I 5; MUMM J 5 scream; MUMM K 5; MUMM L 5; MUMM M 5 noblock; MUMM NO 5; MUMM P -1",
	},
	// Golem Ghost.
	69: {
		name: "golem ghost", sprite: "MUMM", health: 80, radius: 22, height: 62,
		speed: 12, mass: 75, painChance: 128, flags: MF_SHADOW,
		meleeLo: 2, meleeHi: 14,
		seeSound: "mumsit", attackSound: "mumat1", painSound: "mumpai", deathSound: "mumdth", activeSound: "mumact",
		sSpawn: "MUMM AB 10 look",
		sSee:   "MUMM ABCD 4 chase",
		sMelee: "MUMM E 6 face; MUMM F 6 melee; MUMM G 6",
		sPain:  "MUMM H 4; MUMM H 4 pain",
		sDeath: "MUMM I 5; MUMM J 5 scream; MUMM K 5; MUMM L 5; MUMM M 5 noblock; MUMM NO 5; MUMM P -1",
	},
	// Nitrogolem (Mummy Leader) — melee + homing skull missile.
	45: {
		name: "nitrogolem", sprite: "MUMM", health: 100, radius: 22, height: 62,
		speed: 12, mass: 75, painChance: 64,
		meleeLo: 2, meleeHi: 14,
		missile:  &hMissileDef{sprite: "FX15", speed: 9 * 35 / 8, radius: 8, height: 14, damage: 4, frames: 3, tics: 5, sound: "mumhed"},
		seeSound: "mumsit", attackSound: "mumat1", painSound: "mumpai", deathSound: "mumdth", activeSound: "mumact",
		sSpawn:   "MUMM AB 10 look",
		sSee:     "MUMM ABCD 4 chase",
		sMelee:   "MUMM E 6 face; MUMM F 6 melee; MUMM G 6",
		sMissile: "MUMM X 5 face; MUMM Y 5 face; MUMM X 5 face; MUMM Y 5 combo",
		sPain:    "MUMM H 4; MUMM H 4 pain",
		sDeath:   "MUMM I 5; MUMM J 5 scream; MUMM K 5; MUMM L 5; MUMM M 5 noblock; MUMM NO 5; MUMM P -1",
	},
	// Nitrogolem Ghost.
	46: {
		name: "nitrogolem ghost", sprite: "MUMM", health: 100, radius: 22, height: 62,
		speed: 12, mass: 75, painChance: 64, flags: MF_SHADOW,
		meleeLo: 2, meleeHi: 14,
		missile:  &hMissileDef{sprite: "FX15", speed: 9 * 35 / 8, radius: 8, height: 14, damage: 4, frames: 3, tics: 5, sound: "mumhed"},
		seeSound: "mumsit", attackSound: "mumat1", painSound: "mumpai", deathSound: "mumdth", activeSound: "mumact",
		sSpawn:   "MUMM AB 10 look",
		sSee:     "MUMM ABCD 4 chase",
		sMelee:   "MUMM E 6 face; MUMM F 6 melee; MUMM G 6",
		sMissile: "MUMM X 5 face; MUMM Y 5 face; MUMM X 5 face; MUMM Y 5 combo",
		sPain:    "MUMM H 4; MUMM H 4 pain",
		sDeath:   "MUMM I 5; MUMM J 5 scream; MUMM K 5; MUMM L 5; MUMM M 5 noblock; MUMM NO 5; MUMM P -1",
	},
	// Undead Warrior (Knight) — melee + axe.
	64: {
		name: "undead warrior", sprite: "KNIG", health: 200, radius: 24, height: 78,
		speed: 12, mass: 150, painChance: 100,
		meleeLo: 6, meleeHi: 18,
		missile:  &hMissileDef{sprite: "SPAX", speed: 9 * 35 / 8, radius: 10, height: 8, damage: 2, frames: 3, tics: 3, sound: "hedat1"},
		seeSound: "kgtsit", attackSound: "kgtat1", painSound: "kgtpai", deathSound: "kgtdth", activeSound: "kgtatk",
		sSpawn:   "KNIG AB 10 look",
		sSee:     "KNIG ABCD 4 chase",
		sMissile: "KNIG E 10 face; KNIG F 8 face; KNIG G 8 combo",
		sPain:    "KNIG H 3; KNIG H 3 pain",
		sDeath:   "KNIG I 6; KNIG J 6 scream; KNIG K 6; KNIG L 6 noblock; KNIG MN 6; KNIG O -1",
	},
	// Undead Warrior Ghost.
	65: {
		name: "undead warrior ghost", sprite: "KNIG", health: 200, radius: 24, height: 78,
		speed: 12, mass: 150, painChance: 100, flags: MF_SHADOW,
		meleeLo: 6, meleeHi: 18,
		missile:  &hMissileDef{sprite: "RAXE", speed: 9 * 35 / 8, radius: 10, height: 8, damage: 7, frames: 2, tics: 5, sound: "hedat1"},
		seeSound: "kgtsit", attackSound: "kgtat1", painSound: "kgtpai", deathSound: "kgtdth", activeSound: "kgtatk",
		sSpawn:   "KNIG AB 10 look",
		sSee:     "KNIG ABCD 4 chase",
		sMissile: "KNIG E 10 face; KNIG F 8 face; KNIG G 8 combo",
		sPain:    "KNIG H 3; KNIG H 3 pain",
		sDeath:   "KNIG I 6; KNIG J 6 scream; KNIG K 6; KNIG L 6 noblock; KNIG MN 6; KNIG O -1",
	},
	// Weredragon (Beast) — fireball.
	70: {
		name: "weredragon", sprite: "BEAS", health: 220, radius: 32, height: 74,
		speed: 14, mass: 200, painChance: 100,
		missile:  &hMissileDef{sprite: "FRB1", speed: 12 * 35 / 8, radius: 9, height: 8, damage: 4, frames: 3, tics: 4, sound: "bstatk"},
		seeSound: "bstsit", attackSound: "bstatk", painSound: "bstpai", deathSound: "bstdth", activeSound: "bstact",
		sSpawn:   "BEAS AB 10 look",
		sSee:     "BEAS ABCDEF 3 chase",
		sMissile: "BEAS H 10 face; BEAS I 10 combo",
		sPain:    "BEAS G 3; BEAS G 3 pain",
		sDeath:   "BEAS RS 5 scream; BEAS TUVW 5; BEAS X 5 noblock; BEAS YZ 5; BEAS [ -1",
	},
	// Ophidian (Snake) — twin projectiles.
	92: {
		name: "ophidian", sprite: "SNKE", health: 280, radius: 22, height: 70,
		speed: 10, mass: 100, painChance: 48,
		missile:  &hMissileDef{sprite: "SNFX", speed: 14 * 35 / 8, radius: 12, height: 8, damage: 3, frames: 4, tics: 5, sound: "snkatk"},
		seeSound: "snksit", attackSound: "snkatk", painSound: "snkpai", deathSound: "snkdth", activeSound: "snkact",
		sSpawn:   "SNKE AB 10 look",
		sSee:     "SNKE ABCD 4 chase",
		sMissile: "SNKE FF 5 face; SNKE FFF 4 missile; SNKE FFF 5 face; SNKE F 4 missile",
		sPain:    "SNKE E 3; SNKE E 3 pain",
		sDeath:   "SNKE G 5; SNKE H 5 scream; SNKE IJKL 5; SNKE M 5 noblock; SNKE NO 5; SNKE P -1",
	},
	// Sabreclaw (Clink) — melee, no blood.
	90: {
		name: "sabreclaw", sprite: "CLNK", health: 150, radius: 20, height: 64,
		speed: 14, mass: 75, painChance: 32, flags: MF_NOBLOOD,
		meleeLo: 3, meleeHi: 27,
		seeSound: "clksit", attackSound: "clkatk", painSound: "clkpai", deathSound: "clkdth", activeSound: "clkact",
		sSpawn: "CLNK AB 10 look",
		sSee:   "CLNK ABCD 3 chase",
		sMelee: "CLNK E 5 face; CLNK F 4 face; CLNK G 7 melee",
		sPain:  "CLNK H 3; CLNK H 3 pain",
		sDeath: "CLNK IJ 6; CLNK K 5 scream; CLNK L 5 noblock; CLNK MN 5; CLNK O -1",
	},
	// Disciple of D'Sparil (Wizard) — flies, fast projectile.
	15: {
		name: "disciple", sprite: "WZRD", health: 180, radius: 16, height: 68,
		speed: 12, mass: 100, painChance: 64, flags: MF_NOGRAVITY,
		missile:  &hMissileDef{sprite: "FX11", speed: 18 * 35 / 8, radius: 10, height: 6, damage: 3, frames: 2, tics: 6, sound: "wizatk"},
		seeSound: "wizsit", attackSound: "wizatk", painSound: "wizpai", deathSound: "wizdth", activeSound: "wizact",
		sSpawn:   "WZRD AB 10 look",
		sSee:     "WZRD AABB 3 chase",
		sMissile: "WZRD C 4 face; WZRD D 4 face; WZRD C 4 face; WZRD D 4 missile; WZRD D 12 face",
		sPain:    "WZRD E 3; WZRD E 3 pain",
		sDeath:   "WZRD F 6 scream; WZRD GHI 6; WZRD J 6 noblock; WZRD KL 6; WZRD M -1",
	},
	// Iron Lich (boss) — ice ball / ice shards / fire column, high HP.
	6: {
		name: "iron lich", sprite: "LICH", health: 700, radius: 40, height: 72,
		speed: 6, mass: 325, painChance: 32, flags: MF_NOGRAVITY | MF_NOBLOOD,
		bossMissiles: []*hMissileDef{
			{sprite: "FX05", speed: 13 * 35 / 8, radius: 12, height: 6, damage: 3, frames: 3, tics: 6, sound: "hedat1", deathSprite: "FX05", deathFrames: 4, deathTics: 5},
			{sprite: "FX05", speed: 8 * 35 / 8, radius: 12, height: 6, damage: 3, frames: 3, tics: 6, sound: "hedat1"},
			{sprite: "FX06", speed: 10 * 35 / 8, radius: 14, height: 12, damage: 5, frames: 3, tics: 4, sound: "hedat1", deathSprite: "FX06", deathFrames: 4, deathTics: 5},
		},
		seeSound: "hedsit", attackSound: "hedat1", painSound: "hedpai", deathSound: "heddth", activeSound: "hedact",
		sSpawn:   "LICH A 10 look",
		sSee:     "LICH A 4 chase",
		sMissile: "LICH A 5 face; LICH B 20 boss",
		sPain:    "LICH A 4; LICH A 4 pain",
		sDeath:   "LICH C 7; LICH D 7 scream; LICH EF 7; LICH G 7 noblock; LICH H 7; LICH I -1",
	},
	// Maulotaur (boss) — hammer melee + fireball / floor-fire, huge HP.
	9: {
		name: "maulotaur", sprite: "MNTR", health: 3000, radius: 28, height: 100,
		speed: 16, mass: 800, painChance: 25, flags: MF_NOBLOOD,
		meleeLo: 4, meleeHi: 28,
		bossMissiles: []*hMissileDef{
			{sprite: "FX13", speed: 20 * 35 / 8, radius: 10, height: 6, damage: 4, frames: 3, tics: 4, sound: "minat1", deathSprite: "FX13", deathFrames: 3, deathTics: 4},
			{sprite: "FX13", speed: 14 * 35 / 8, radius: 12, height: 8, damage: 5, frames: 3, tics: 4, sound: "minat3"},
		},
		seeSound: "minsit", attackSound: "minat1", painSound: "minpai", deathSound: "mindth", activeSound: "minact",
		sSpawn:   "MNTR AB 10 look",
		sSee:     "MNTR ABCD 5 chase",
		sMelee:   "MNTR V 10 face; MNTR W 7 face; MNTR X 12 melee",
		sMissile: "MNTR E 10 face; MNTR F 15 face; MNTR G 15 boss",
		sPain:    "MNTR Y 25; MNTR Y 25 pain",
		sDeath:   "MNTR YZ 6 scream; MNTR [ 6; MNTR \\ 6 noblock; MNTR ] 6; MNTR ^ 6; MNTR _ -1",
	},
	// D'Sparil on serpent (Sorcerer1, boss) — triple fireball spread.
	7: {
		name: "d'sparil", sprite: "SRCR", health: 2000, radius: 28, height: 100,
		speed: 16, mass: 800, painChance: 56, flags: MF_NOBLOOD,
		bossMissiles: []*hMissileDef{
			{sprite: "FX16", speed: 20 * 35 / 8, radius: 10, height: 10, damage: 3, frames: 4, tics: 3, sound: "sbtsit", deathSprite: "FX16", deathFrames: 3, deathTics: 4},
			{sprite: "FX16", speed: 14 * 35 / 8, radius: 12, height: 10, damage: 4, frames: 4, tics: 3, sound: "sbtatk"},
		},
		seeSound: "sbtsit", attackSound: "sbtatk", painSound: "sbtpai", deathSound: "sbtdth", activeSound: "sbtact",
		sSpawn:   "SRCR AB 10 look",
		sSee:     "SRCR ABCD 5 chase",
		sMissile: "SRCR E 6 face; SRCR F 6 face; SRCR G 6 boss; SRCR F 6 face; SRCR G 6 boss",
		sPain:    "SRCR Q 6; SRCR Q 6 pain",
		sDeath:   "SRCR H 7; SRCR I 7 scream; SRCR JKL 7; SRCR M 7 noblock; SRCR NOP 7",
	},
}
