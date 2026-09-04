package engine

import "math"

// WeaponFrame is one frame of a weapon's animation, straight from id's own
// info.c states[] table: which sprite frame letter to show, and how many
// Doom tics (1/35 sec, the engine's fixed simulation rate) to hold it
// before advancing. The original expresses this as a linked chain of named
// states (S_PISTOL1 -> S_PISTOL2 -> ...); this project flattens each
// weapon's chain into a plain slice, which is all a chain of
// frame+duration pairs with no branching ever really was.
type WeaponFrame struct {
	Frame byte // 'A', 'B', 'C', ... -> sprite lump suffix, e.g. "PISG" + 'B' + "0" = PISGB0
	Tics  int
}

// timedSound is a DS* lump played once when the fire animation reaches
// AtTic tics from the start of the shot (see WeaponDef.FireSoundSeq).
type timedSound struct {
	AtTic float64
	Sound string
}

// ticsPerSecond is Doom's native simulation rate — every Tics value below
// is taken directly from info.c and only makes sense against this constant.
const ticsPerSecond = 35.0

// WeaponDef describes one of the six switchable weapons: its viewmodel/
// flash sprite prefixes, fire sound, ammo cost, and its fire animation —
// FireFrames (the gun sprite sequence) and FlashFrames (the muzzle-flash
// overlay sequence, running in parallel from the same moment firing
// starts) — both ported from info.c's actual per-weapon state chains; see
// game_design.txt for exactly which simplifications were made translating
// id's linked *state* graph (each with its own action-function pointers)
// into these flat timing tables, and which original behavior (e.g. two
// weapons' flash frame occasionally chosen at random) wasn't replicated.
type WeaponDef struct {
	Name         string
	SpritePrefix string
	FlashPrefix  string
	FireSound    string
	AmmoType     int // index into PlayerStats.Ammo
	AmmoCost     int
	FireFrames   []WeaponFrame
	FlashFrames  []WeaponFrame // nil/empty if the weapon has no muzzle flash
	// FireSoundSeq is extra one-shot sounds triggered partway through the
	// fire animation, at a tic offset from the start of the shot — id's
	// mid-chain action functions that only call S_StartSound. The Super
	// Shotgun's break-open reload (A_OpenShotgun2 / A_LoadShotgun2 /
	// A_CloseShotgun2 -> DSDBOPN / DSDBLOAD / DSDBCLS) is the only user.
	FireSoundSeq []timedSound
	// IdleFrames is the looping viewmodel animation shown while the weapon
	// is up but not firing (and while raising/lowering). Empty means "just
	// hold frame 'A'", which is every weapon in id's data except the
	// chainsaw — whose S_SAW/S_SAWB idle alternates SAWG C/D every 4 tics
	// to give the running chainsaw its constant vibration.
	IdleFrames []WeaponFrame
	// IdleSound is a DS* lump re-triggered on a loop while the weapon sits
	// in the ready phase — the chainsaw's DSSAWIDL whir (id's A_WeaponReady
	// restarts sfx_sawidl every time the psprite re-enters S_SAW). Empty
	// for every other weapon.
	IdleSound string
	// Projectile is non-nil for the three weapons that fire a true,
	// visible, traveling shot (Rocket Launcher, Plasma Rifle, BFG9000) —
	// nil for the three hitscan weapons (Pistol, Shotgun, Chaingun),
	// which fire instantly with nothing to simulate or draw at all,
	// exactly matching the original: real Doom never gives a bullet a
	// travel time, only rockets/plasma/BFG bolts are actual moving things.
	Projectile *ProjectileDef
	// Melee marks the chainsaw: no travel, no bullet cone — a very
	// short-range repeated hit (id's A_Saw). Mutually exclusive with
	// Projectile.
	Melee bool
}

// ProjectileDef is a weapon's true traveling projectile (id's "missile"
// thing): a single flying sprite (this project always shows the same
// view of it rather than the original's full set of rotation frames —
// see projectiles.go) that travels at Speed until it hits a wall, then
// plays ExplodeFrames (one letter per ExplodePrefix-lump frame, held
// ExplodeTics each) and plays ExplodeSound once, in place, before
// disappearing.
type ProjectileDef struct {
	Sprite        string
	Speed         float64 // map units/sec
	ExplodePrefix string
	ExplodeFrames []byte
	ExplodeTics   int
	ExplodeSound  string
	ExplodeRadius int // >0: blast-radius damage on impact (pRadiusAttack)
	// Damage is the direct-hit damage multiplier — id's mobjinfo.damage for
	// the missile. A shot that strikes a monster deals (P_Random()%8 + 1) *
	// Damage (so 8x the base at most), then, if ExplodeRadius > 0, the blast
	// on top. 0 = a purely cosmetic projectile.
	Damage int
}

// Weapon indices — id's weapontype_t order exactly, so Weapons[] and
// PlayerStats.Weapons[] can be indexed by these directly.
const (
	wpFist = iota
	wpPistol
	wpShotgun
	wpChaingun
	wpMissile
	wpPlasma
	wpBFG
	wpChainsaw
	wpSuperShotgun
	numWeapons
)

// weaponSlots maps a keyboard slot number (1-7, the classic Doom layout) to
// the weapon indices it selects, in press-again-to-toggle order. Slot 1 is
// Fist/Chainsaw, slot 3 is Shotgun/Super Shotgun; the rest hold one weapon.
// Doom 1 simply never gives you a Super Shotgun, so slot 3 there only ever
// resolves to the plain Shotgun.
var weaponSlots = [8][]int{
	1: {wpFist, wpChainsaw},
	2: {wpPistol},
	3: {wpShotgun, wpSuperShotgun},
	4: {wpChaingun},
	5: {wpMissile},
	6: {wpPlasma},
	7: {wpBFG},
}

// Weapons is indexed by the wp* constants above (id's weapontype_t). The
// chaingun firing DSPISTOL and the BFG's 40-cell cost aren't approximations
// — that's the original's actual data (a well-known bit of Doom trivia in
// the chaingun's case).
var Weapons = [numWeapons]WeaponDef{
	wpFist: {
		Name: "Fist", SpritePrefix: "PUNG", FireSound: "DSPUNCH",
		AmmoType: -1, AmmoCost: 0,
		// S_PUNCH1..5: B C D C B (ready/raise/lower use frame A).
		FireFrames: []WeaponFrame{{'B', 4}, {'C', 4}, {'D', 5}, {'C', 4}, {'B', 5}},
		Melee:      true,
	},
	wpPistol: {
		Name: "Pistol", SpritePrefix: "PISG", FlashPrefix: "PISF", FireSound: "DSPISTOL",
		AmmoType: 0, AmmoCost: 1,
		// S_PISTOL1..4
		FireFrames: []WeaponFrame{{'A', 4}, {'B', 6}, {'C', 4}, {'B', 5}},
		// S_PISTOLFLASH
		FlashFrames: []WeaponFrame{{'A', 7}},
	},
	wpShotgun: {
		Name: "Shotgun", SpritePrefix: "SHTG", FlashPrefix: "SHTF", FireSound: "DSSHOTGN",
		AmmoType: 1, AmmoCost: 1,
		// S_SGUN1..9 — the pump-action cycle: A A B C D C B A A
		FireFrames: []WeaponFrame{
			{'A', 3}, {'A', 7}, {'B', 5}, {'C', 5}, {'D', 4}, {'C', 5}, {'B', 5}, {'A', 3}, {'A', 7},
		},
		// S_SGUNFLASH1..2
		FlashFrames: []WeaponFrame{{'A', 4}, {'B', 3}},
	},
	wpChaingun: {
		Name: "Chaingun", SpritePrefix: "CHGG", FlashPrefix: "CHGF", FireSound: "DSPISTOL",
		AmmoType: 0, AmmoCost: 1,
		// Vanilla S_CHAIN1..2 hold 4 tics each (8 tics/shot, ~4.4/sec). Per
		// user request this project's chaingun fires at twice that rate:
		// 2 tics/frame, 4 tics/shot (~8.75/sec). The muzzle flash is pulled
		// in from 5 to 3 tics so it doesn't smear across two whole shots.
		FireFrames:  []WeaponFrame{{'A', 2}, {'B', 2}},
		FlashFrames: []WeaponFrame{{'A', 3}},
	},
	wpMissile: {
		Name: "Rocket Launcher", SpritePrefix: "MISG", FlashPrefix: "MISF", FireSound: "DSRLAUNC",
		AmmoType: 3, AmmoCost: 1,
		// S_MISSILE1..2 (S_MISSILE3 is another 0-tic instant passthrough);
		// both real frames are the same letter, so they're merged into one
		FireFrames: []WeaponFrame{{'B', 20}},
		// S_MISSILEFLASH1..4
		FlashFrames: []WeaponFrame{{'A', 3}, {'B', 4}, {'C', 4}, {'D', 4}},
		// MT_ROCKET's canon speed is 20*35=700 units/sec; per user
		// feedback this project's own rocket runs 20% faster than that.
		// deathsound sfx_barexp (DSBAREXP) — rockets and exploding barrels
		// share an explosion sound in vanilla, not an approximation. All
		// verified present in testdata/DOOM1.WAD (MISLA1, BEXPA0-E0, DSBAREXP).
		Projectile: &ProjectileDef{
			Sprite: "MISLA1", Speed: 700 * 1.2,
			ExplodePrefix: "BEXP", ExplodeFrames: []byte{'A', 'B', 'C', 'D', 'E'}, ExplodeTics: 4,
			ExplodeSound: "DSBAREXP", ExplodeRadius: 128,
			Damage: 20, // MT_ROCKET: direct hit deals (P_Random%8+1)*20, plus the 128u blast
		},
	},
	wpPlasma: {
		Name: "Plasma Rifle", SpritePrefix: "PLSG", FlashPrefix: "PLSF", FireSound: "DSPLASMA",
		AmmoType: 2, AmmoCost: 1,
		// S_PLASMA1..2. Vanilla's S_PLASMA2 is 20 tics but its A_ReFire
		// bounces straight back to S_PLASMA1 the instant fire is held, so the
		// real held-fire cadence is ~3 tics/shot (~12/sec). This engine
		// always plays the whole FireFrames sequence before re-firing, so
		// frame B is trimmed to 1 tic to land near that rate (4 tics/shot,
		// ~9/sec) instead of the ~1.5/sec a literal 3+20 would give.
		FireFrames: []WeaponFrame{{'A', 3}, {'B', 1}},
		// S_PLASMAFLASH1 (vanilla randomly picks S_PLASMAFLASH2 instead
		// about half the time; same simplification as the chaingun above)
		FlashFrames: []WeaponFrame{{'A', 4}},
		// MT_PLASMA: speed 25*35=875 units/sec. Sprite/explosion/sound
		// names (PLSS the flying ball, PLSE its splat, DSPLASMA reused for
		// no distinct impact sound in vanilla) are NOT verified against
		// any WAD — the shareware IWAD this project was built and tested
		// against has no plasma rifle assets at all (it's Episode 2/3-only
		// content), so this has never actually been exercised.
		Projectile: &ProjectileDef{
			Sprite: "PLSSA0", Speed: 875,
			ExplodePrefix: "PLSE", ExplodeFrames: []byte{'A', 'B', 'C', 'D', 'E'}, ExplodeTics: 4,
			ExplodeSound: "DSFIRXPL",
			Damage:       5, // MT_PLASMA: direct hit only, (P_Random%8+1)*5
		},
	},
	wpBFG: {
		Name: "BFG9000", SpritePrefix: "BFGG", FlashPrefix: "BFGF", FireSound: "DSBFG",
		AmmoType: 2, AmmoCost: 40,
		// S_BFG1..4
		FireFrames: []WeaponFrame{{'A', 20}, {'B', 10}, {'B', 10}, {'B', 20}},
		// S_BFGFLASH1..2
		FlashFrames: []WeaponFrame{{'A', 11}, {'B', 6}},
		// MT_BFG: speed 25*35=875 units/sec. Same caveat as Plasma Rifle
		// above — BFS1/BFE1 names are unverified, no BFG assets exist in
		// the shareware IWAD this project was tested against.
		Projectile: &ProjectileDef{
			Sprite: "BFS1A0", Speed: 875,
			ExplodePrefix: "BFE1", ExplodeFrames: []byte{'A', 'B', 'C', 'D'}, ExplodeTics: 8,
			ExplodeSound: "DSRXPLOD",
			Damage:       100, // MT_BFG: direct hit (P_Random%8+1)*100 (the 40-ray spray isn't modelled)
		},
	},
	wpChainsaw: {
		Name: "Chainsaw", SpritePrefix: "SAWG", FireSound: "DSSAWFUL",
		AmmoType: -1, AmmoCost: 0,
		// S_SAW1..2 — the A/B grind, looped seamlessly while fire is held
		// (see updateWeapon's auto-refire). S_SAW/S_SAWB — the C/D idle
		// vibration. Vanilla also plays DSSAWIDL/DSSAWHIT; this uses the one
		// FireSound slot for the attack noise.
		FireFrames: []WeaponFrame{{'A', 4}, {'B', 4}},
		IdleFrames: []WeaponFrame{{'C', 4}, {'D', 4}},
		IdleSound:  "DSSAWIDL",
		Melee:      true,
	},
	wpSuperShotgun: {
		Name: "Super Shotgun", SpritePrefix: "SHT2", FlashPrefix: "SHT2", FireSound: "DSDSHTGN",
		AmmoType: 1, AmmoCost: 2,
		// S_DSGUN1..9 exactly — the break-action cycle: A A B C D E F A A
		// (id's SPR_SHT2 frames 0,0,1,2,3,4,5,0,0), ~54 tics / 1.54s, the
		// slowest firing weapon. Two shells a shot, 20-pellet spray, wide
		// (see fireHitscan). SHT2G0/H0 exist in the WAD but id's SSG never
		// shows them.
		FireFrames: []WeaponFrame{
			{'A', 3}, {'A', 7}, {'B', 7}, {'C', 7}, {'D', 7},
			{'E', 7}, {'F', 7}, {'A', 5}, {'A', 4},
		},
		// S_DSFLASH1..2
		FlashFrames: []WeaponFrame{{'I', 4}, {'J', 3}},
		// A_OpenShotgun2 / A_LoadShotgun2 / A_CloseShotgun2 — the break
		// open, shell load, and snap shut, at the C, D and F frames.
		FireSoundSeq: []timedSound{
			{AtTic: 17, Sound: "DSDBOPN"},
			{AtTic: 24, Sound: "DSDBLOAD"},
			{AtTic: 38, Sound: "DSDBCLS"},
		},
	},
}

// weaponRaiseSpeed is id's own RAISESPEED/LOWERSPEED (6 fixed-point units
// per tic, over a WEAPONBOTTOM-WEAPONTOP span of 96 units — see p_pspr.c),
// converted into this project's 0..1 Offset space and real seconds:
// 96/6 = 16 tics to fully raise or lower.
const weaponRaiseSpeed = ticsPerSecond / 16.0

type weaponPhase int

const (
	weaponLowering weaponPhase = iota
	weaponRaising
	weaponReady
	weaponFiring
)

// WeaponState is the equipped weapon's animation state.
type WeaponState struct {
	Current int
	pending int // weapon index queued to switch to once lowering completes; -1 if none
	phase   weaponPhase
	Offset  float64 // 0 = fully raised/ready, 1 = fully lowered/hidden — read by raster.DrawWeapon

	fireElapsedTics float64 // time since FireWeapon, in Doom tics — indexes into FireFrames/FlashFrames
	idleTics        float64 // free-running tic counter for the IdleFrames loop
	idleSoundTimer  float64 // seconds until the IdleSound is re-triggered (see updateWeapon)
}

// NewWeaponState starts with the Pistol already raising in (Doom's own
// starting weapon).
func NewWeaponState() WeaponState {
	return WeaponState{Current: wpPistol, pending: -1, phase: weaponRaising, Offset: 1}
}

// frameAt walks frames' cumulative durations and returns whichever is
// active at elapsedTics, or ok=false once elapsedTics has run past the end
// of the whole sequence.
func frameAt(frames []WeaponFrame, elapsedTics float64) (frame WeaponFrame, ok bool) {
	t := 0.0
	for _, f := range frames {
		t += float64(f.Tics)
		if elapsedTics < t {
			return f, true
		}
	}
	return WeaponFrame{}, false
}

// framesTotalTics is the summed duration of a frame sequence.
func framesTotalTics(frames []WeaponFrame) int {
	n := 0
	for _, f := range frames {
		n += f.Tics
	}
	return n
}

// frameAtLooped is frameAt over a sequence that repeats forever (the
// IdleFrames vibration).
func frameAtLooped(frames []WeaponFrame, tics float64) (WeaponFrame, bool) {
	total := framesTotalTics(frames)
	if total <= 0 {
		return WeaponFrame{}, false
	}
	return frameAt(frames, math.Mod(tics, float64(total)))
}

// SwitchWeapon requests weapon index (a wp* constant) become current, via
// the same lower-then-raise animation Doom used — a no-op if the weapon
// isn't owned (pick it up first — see pickup.go), if it's already
// current/pending, or while firing (Doom doesn't let you switch mid-shot).
func (g *Game) SwitchWeapon(index int) {
	if index < 0 || index >= len(Weapons) {
		return
	}
	if !g.Player.Weapons[index] {
		return // not owned
	}
	if index == g.Weapon.Current || index == g.Weapon.pending {
		return
	}
	g.Weapon.pending = index
	if g.Weapon.phase == weaponReady {
		g.Weapon.phase = weaponLowering
	}
}

// SelectWeaponSlot handles a number-key press (slot 1-7). If the weapon
// you're already holding (or switching to) is in that slot, it advances to
// the next owned weapon in the slot — the "press 1 again to toggle
// fist/chainsaw", "press 3 again to toggle shotgun/super shotgun"
// behaviour. Otherwise it selects the first owned weapon in the slot.
func (g *Game) SelectWeaponSlot(slot int) {
	if slot < 1 || slot >= len(weaponSlots) {
		return
	}
	list := weaponSlots[slot]
	cur := g.Weapon.Current
	if g.Weapon.pending >= 0 {
		cur = g.Weapon.pending
	}
	start := 0
	for i, w := range list {
		if w == cur {
			start = i + 1
			break
		}
	}
	for k := 0; k < len(list); k++ {
		w := list[(start+k)%len(list)]
		if g.Player.Weapons[w] {
			g.SwitchWeapon(w)
			return
		}
	}
}

// FireWeapon fires the current weapon if it's ready and there's enough
// ammo: plays its sound, starts its fire animation, deducts ammo, and
// resolves the shot. The three projectile weapons (Rocket Launcher, Plasma
// Rifle, BFG9000) launch a visible traveling shot (projectiles.go); the
// three hitscan weapons (Pistol, Shotgun, Chaingun) resolve instantly
// through pLineAttack against the world and monsters.
func (g *Game) FireWeapon() {
	if g.Weapon.phase != weaponReady {
		return
	}
	if !g.beginFire() {
		return
	}
	g.Weapon.phase = weaponFiring
	g.Weapon.fireElapsedTics = 0
}

// beginFire runs one shot of the current weapon: ammo check + deduct, fire
// sound, and the actual effect (projectile / melee / hitscan). Returns
// false without doing anything if there isn't enough ammo. Called by
// FireWeapon to start firing and by updateWeapon to auto-refire a held
// weapon at the end of its animation (id's A_ReFire re-entering atkstate),
// so a repeated shot doesn't need a visible ready frame between rounds.
func (g *Game) beginFire() bool {
	def := Weapons[g.Weapon.Current]
	if def.AmmoType >= 0 {
		if g.Player.Ammo[def.AmmoType] < def.AmmoCost {
			return false
		}
		g.Player.Ammo[def.AmmoType] -= def.AmmoCost
	}
	g.playWeaponSound(def.FireSound)
	// id's P_FireWeapon: the shot's noise floods the surrounding sectors and
	// wakes any monster there, seen or not (noise.go).
	g.pNoiseAlert(g.playerMobj)
	switch {
	case def.Projectile != nil:
		g.spawnProjectile(def.Projectile)
	case def.Melee:
		g.fireMelee()
	default:
		g.fireHitscan(g.Weapon.Current)
	}
	return true
}

// fireMelee handles the two melee weapons — fist (A_Punch) and chainsaw
// (A_Saw): one very short-range hit per fire frame, no ammo, id's damage of
// 2*(P_Random%10+1), x10 for a berserk fist. The small angle jitter is
// their own "spray" so a swing catches things slightly off-centre.
func (g *Game) fireMelee() {
	if g.playerMobj == nil {
		return
	}
	damage := 2 * (pRandom()%10 + 1)
	rng := meleeRange
	if g.Weapon.Current == wpFist {
		if g.Player.Powers[pwStrength] > 0 {
			damage *= 10 // berserk
		}
	} else {
		rng = meleeRange + 1 // A_Saw reaches one unit further
	}
	ang := g.playerMobj.Angle + bulletSpread()/4
	g.pLineAttack(g.playerMobj, ang, g.playerAimSlope(), rng, damage)
}

// fireHitscan resolves a bullet weapon. Damage per pellet is id's
// 5*(P_Random%3+1) throughout.
//
//   - pistol / chaingun (A_FirePistol / A_FireCGun): one round, the first
//     of a held burst dead-on, the rest in a tight cone.
//   - shotgun (A_FireShotgun): 7 pellets, each `angle + P_SubRandom()<<18`
//     (== bulletSpread()/4).
//   - super shotgun (A_FireShotgun2): 20 pellets, each spread twice as wide
//     horizontally (`<<19` == bulletSpread()/2) AND jittered vertically
//     (`bulletslope + P_SubRandom()<<5`, ~+-7 degrees) — the SSG's signature
//     wide, tall wall of lead.
func (g *Game) fireHitscan(weapon int) {
	if g.playerMobj == nil {
		return
	}
	baseSlope := g.playerAimSlope()
	dmg := func() int { return 5 * (pRandom()%3 + 1) }

	if weapon == wpSuperShotgun {
		for i := 0; i < 20; i++ {
			ang := g.playerMobj.Angle + bulletSpread()/2
			slope := baseSlope + float64(pRandomSpread())/2048.0
			g.pLineAttack(g.playerMobj, ang, slope, missileRange, dmg())
		}
		return
	}

	shots, spread := 1, true
	switch weapon {
	case wpShotgun:
		shots = 7
	case wpPistol, wpChaingun: // first of a burst is accurate
		spread = g.Weapon.fireElapsedTics > 0
	}
	for i := 0; i < shots; i++ {
		ang := g.playerMobj.Angle
		if spread || i > 0 {
			ang += bulletSpread() / 4
		}
		g.pLineAttack(g.playerMobj, ang, baseSlope, missileRange, dmg())
	}
}

// playerAimSlope is a lightweight auto-aim: if a shootable thing sits
// within ~6° of where the player is looking, lock the vertical slope onto
// it (Doom's assist); otherwise use the view pitch.
func (g *Game) playerAimSlope() float64 {
	pm := g.playerMobj
	var best *Mobj
	bestDist := missileRange
	for _, mo := range g.mobjs {
		if mo.removed || mo.Flags&MF_SHOOTABLE == 0 || mo.Health <= 0 {
			continue
		}
		d := math.Hypot(mo.X-pm.X, mo.Y-pm.Y)
		if d >= bestDist {
			continue
		}
		if math.Abs(normAngle(math.Atan2(mo.Y-pm.Y, mo.X-pm.X)-pm.Angle)) > 6*math.Pi/180 {
			continue
		}
		best, bestDist = mo, d
	}
	if best != nil {
		return pAimSlope(pm, best)
	}
	return math.Tan(g.Camera.Pitch)
}

// updateWeapon advances the raise/lower/fire animation by dt seconds and
// keeps Player.CurrentAmmo (what the status bar's big ammo number shows)
// pointed at whichever ammo type the current weapon actually uses.
func (g *Game) updateWeapon(dt float64) {
	w := &g.Weapon
	w.idleTics += dt * ticsPerSecond // free-running, drives the IdleFrames loop
	switch w.phase {
	case weaponLowering:
		w.Offset += weaponRaiseSpeed * dt
		if w.Offset >= 1 {
			w.Offset = 1
			if w.pending >= 0 {
				w.Current, w.pending = w.pending, -1
			}
			w.phase = weaponRaising
		}
	case weaponRaising:
		w.Offset -= weaponRaiseSpeed * dt
		if w.Offset <= 0 {
			w.Offset = 0
			w.phase = weaponReady
		}
	case weaponFiring:
		prevTics := w.fireElapsedTics
		w.fireElapsedTics += dt * ticsPerSecond
		// Mid-animation one-shots (the Super Shotgun's reload — see
		// FireSoundSeq): fire each as this frame's elapsed range crosses it.
		for _, ts := range Weapons[w.Current].FireSoundSeq {
			if prevTics < ts.AtTic && ts.AtTic <= w.fireElapsedTics {
				g.playWeaponSound(ts.Sound)
			}
		}
		total := framesTotalTics(Weapons[w.Current].FireFrames)
		if total <= 0 || w.fireElapsedTics >= float64(total) {
			// Sequence done. If fire is still held and there's ammo,
			// re-enter it seamlessly (carrying the overshoot) so a held
			// auto weapon — chaingun, plasma, chainsaw — loops its firing
			// frames with no ready frame flashing between rounds, the way
			// id's 0-tic A_ReFire state does.
			if total > 0 && g.Window != nil && g.Window.FirePressed() && g.beginFire() {
				w.fireElapsedTics -= float64(total)
			} else {
				w.phase = weaponReady
				w.fireElapsedTics = 0
			}
		}
	case weaponReady:
		if w.pending >= 0 {
			w.phase = weaponLowering
		}
	}

	// Idle sound: the chainsaw's DSSAWIDL whir, re-triggered on a loop
	// while it sits ready with nothing queued. Any other phase disarms the
	// timer so it fires again the instant the weapon settles. playWeaponSound
	// is a no-op without an audio device, so this costs nothing then.
	if def := Weapons[w.Current]; def.IdleSound != "" && w.phase == weaponReady && w.pending < 0 {
		if w.idleSoundTimer <= 0 {
			g.playWeaponSound(def.IdleSound)
			w.idleSoundTimer = g.idleSoundInterval(def.IdleSound)
		} else {
			w.idleSoundTimer -= dt
		}
	} else {
		w.idleSoundTimer = 0
	}

	g.Player.CurrentAmmo = Weapons[w.Current].AmmoType
}

// idleSoundInterval is how often to re-trigger a weapon's IdleSound: the
// clip's own length, so instances play back-to-back into a near-seamless
// loop without stacking (this engine's oto path mixes overlapping copies
// rather than replacing them, so re-triggering early would pile up and get
// louder). Clamped so a missing or oddly-sized lump can't spam the mixer
// or leave a very long silence — DSSAWIDL is ~0.68s.
func (g *Game) idleSoundInterval(name string) float64 {
	d := g.soundDuration(name)
	if d < 0.08 {
		return 0.229 // ~8 tics — id's own S_SAW/S_SAWB cadence
	}
	if d > 3.0 {
		return 3.0
	}
	return d
}

// currentSprite reports which viewmodel sprite frame letter is showing
// right now, and which flash frame (if any) is overlaid.
//
// Not firing: frame 'A' (id's ready/raise/lower states) unless the weapon
// defines IdleFrames — the chainsaw, whose C/D idle loops forever to give
// the running saw its vibration. Firing: FireFrames / FlashFrames indexed
// by the time since the shot started.
func (g *Game) currentSprite() (gunFrame byte, flashFrame byte, hasFlash bool) {
	def := Weapons[g.Weapon.Current]

	if g.Weapon.phase != weaponFiring {
		if f, ok := frameAtLooped(def.IdleFrames, g.Weapon.idleTics); ok {
			return f.Frame, 0, false
		}
		return 'A', 0, false
	}

	gunFrame = 'A'
	if f, ok := frameAt(def.FireFrames, g.Weapon.fireElapsedTics); ok {
		gunFrame = f.Frame
	}
	if f, ok := frameAt(def.FlashFrames, g.Weapon.fireElapsedTics); ok {
		return gunFrame, f.Frame, true
	}
	return gunFrame, 0, false
}
