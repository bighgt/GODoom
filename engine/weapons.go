package engine

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
	AmmoType     int // index into raster.PlayerStats.Ammo
	AmmoCost     int
	FireFrames   []WeaponFrame
	FlashFrames  []WeaponFrame // nil/empty if the weapon has no muzzle flash
	// Projectile is non-nil for the three weapons that fire a true,
	// visible, traveling shot (Rocket Launcher, Plasma Rifle, BFG9000) —
	// nil for the three hitscan weapons (Pistol, Shotgun, Chaingun),
	// which fire instantly with nothing to simulate or draw at all,
	// exactly matching the original: real Doom never gives a bullet a
	// travel time, only rockets/plasma/BFG bolts are actual moving things.
	Projectile *ProjectileDef
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
}

// Weapons are the six switchable weapons, keys 1-6 in that order. Vanilla
// Doom's fist and chainsaw (its own key 1) aren't counted among these —
// the user-specified "six weapons, keys 1-6" layout this project targets
// deliberately differs from vanilla's seven-slot one; see game_design.txt.
// The chaingun firing DSPISTOL and the BFG's 40-cell cost aren't
// approximations — that's the original's actual data (a well-known bit of
// Doom trivia in the chaingun's case).
var Weapons = [6]WeaponDef{
	{
		Name: "Pistol", SpritePrefix: "PISG", FlashPrefix: "PISF", FireSound: "DSPISTOL",
		AmmoType: 0, AmmoCost: 1,
		// S_PISTOL1..4
		FireFrames: []WeaponFrame{{'A', 4}, {'B', 6}, {'C', 4}, {'B', 5}},
		// S_PISTOLFLASH
		FlashFrames: []WeaponFrame{{'A', 7}},
	},
	{
		Name: "Shotgun", SpritePrefix: "SHTG", FlashPrefix: "SHTF", FireSound: "DSSHOTGN",
		AmmoType: 1, AmmoCost: 1,
		// S_SGUN1..9 — the pump-action cycle: A A B C D C B A A
		FireFrames: []WeaponFrame{
			{'A', 3}, {'A', 7}, {'B', 5}, {'C', 5}, {'D', 4}, {'C', 5}, {'B', 5}, {'A', 3}, {'A', 7},
		},
		// S_SGUNFLASH1..2
		FlashFrames: []WeaponFrame{{'A', 4}, {'B', 3}},
	},
	{
		Name: "Chaingun", SpritePrefix: "CHGG", FlashPrefix: "CHGF", FireSound: "DSPISTOL",
		AmmoType: 0, AmmoCost: 1,
		// S_CHAIN1..2 (S_CHAIN3 is the same frame held 0 tics — an
		// instant passthrough back to ready, so it contributes nothing visible)
		FireFrames: []WeaponFrame{{'A', 4}, {'B', 4}},
		// S_CHAINFLASH1 (vanilla picks between two near-identical flash
		// states at random each shot for subtle variety; always using the
		// first is this project's simplification)
		FlashFrames: []WeaponFrame{{'A', 5}},
	},
	{
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
			ExplodeSound: "DSBAREXP",
		},
	},
	{
		Name: "Plasma Rifle", SpritePrefix: "PLSG", FlashPrefix: "PLSF", FireSound: "DSPLASMA",
		AmmoType: 2, AmmoCost: 1,
		// S_PLASMA1..2
		FireFrames: []WeaponFrame{{'A', 3}, {'B', 20}},
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
		},
	},
	{
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
}

// NewWeaponState starts the first weapon (Pistol) already raising in.
func NewWeaponState() WeaponState {
	return WeaponState{Current: 0, pending: -1, phase: weaponRaising, Offset: 1}
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

// SwitchWeapon requests weapon index (0-5) become current, via the same
// lower-then-raise animation Doom used — a no-op if it's already
// current/pending, or firing (Doom doesn't let you switch mid-shot).
// Selecting a weapon also marks it "owned" in the status bar's arms
// widget (Player.Weapons[index+2] lights up yellow instead of gray) —
// there's no real pickup system yet to have granted it earlier (see
// game_design.txt), so switching to a weapon is this project's stand-in
// for having it.
func (g *Game) SwitchWeapon(index int) {
	if index < 0 || index >= len(Weapons) {
		return
	}
	g.Player.Weapons[index+2] = true
	if index == g.Weapon.Current || index == g.Weapon.pending {
		return
	}
	g.Weapon.pending = index
	if g.Weapon.phase == weaponReady {
		g.Weapon.phase = weaponLowering
	}
}

// FireWeapon fires the current weapon if it's ready and there's enough
// ammo: plays its sound, starts its fire animation (see WeaponDef's
// FireFrames/FlashFrames), deducts ammo, and — only for the three weapons
// with a ProjectileDef (Rocket Launcher, Plasma Rifle, BFG9000) — launches
// a real visible shot from the camera along its facing/pitch direction
// (see projectiles.go). The other three (Pistol, Shotgun, Chaingun) are
// hitscan: nothing travels or gets simulated at all, matching how
// instantaneous a real Doom bullet is. There's no damage dealt yet (no
// monsters exist to damage — see game_design.txt), just the shot itself
// and what it hits.
func (g *Game) FireWeapon() {
	if g.Weapon.phase != weaponReady {
		return
	}
	def := Weapons[g.Weapon.Current]
	if def.AmmoType >= 0 {
		if g.Player.Ammo[def.AmmoType] < def.AmmoCost {
			return
		}
		g.Player.Ammo[def.AmmoType] -= def.AmmoCost
	}
	g.Weapon.phase = weaponFiring
	g.Weapon.fireElapsedTics = 0
	g.playSound(def.FireSound)
	if def.Projectile != nil {
		g.spawnProjectile(def.Projectile)
	}
}

// updateWeapon advances the raise/lower/fire animation by dt seconds and
// keeps Player.CurrentAmmo (what the status bar's big ammo number shows)
// pointed at whichever ammo type the current weapon actually uses.
func (g *Game) updateWeapon(dt float64) {
	w := &g.Weapon
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
		w.fireElapsedTics += dt * ticsPerSecond
		if _, ok := frameAt(Weapons[w.Current].FireFrames, w.fireElapsedTics); !ok {
			w.phase = weaponReady
		}
	case weaponReady:
		if w.pending >= 0 {
			w.phase = weaponLowering
		}
	}
	g.Player.CurrentAmmo = Weapons[w.Current].AmmoType
}

// currentSprite reports which viewmodel sprite frame letter is showing
// right now ('A' — Doom's own ready/raise/lower states are always frame
// A — unless mid-fire, per FireFrames), and which flash frame (if any) is
// overlaid, per FlashFrames.
func (g *Game) currentSprite() (gunFrame byte, flashFrame byte, hasFlash bool) {
	gunFrame = 'A'
	if g.Weapon.phase != weaponFiring {
		return gunFrame, 0, false
	}
	def := Weapons[g.Weapon.Current]
	if f, ok := frameAt(def.FireFrames, g.Weapon.fireElapsedTics); ok {
		gunFrame = f.Frame
	}
	if f, ok := frameAt(def.FlashFrames, g.Weapon.fireElapsedTics); ok {
		return gunFrame, f.Frame, true
	}
	return gunFrame, 0, false
}
