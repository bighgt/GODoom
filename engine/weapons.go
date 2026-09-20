package engine

import (
	"math"
)

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
	// Prefix overrides the weapon's own default sprite prefix (SpritePrefix
	// for FireFrames/IdleFrames, FlashPrefix for FlashFrames) for just this
	// frame; "" (every frame before the HD smooth-weapon port) means "use
	// the default." A few of that port's animations switch sprite FAMILY
	// mid-sequence, not just frame letter — id's own info.c never needed
	// this (one sprite per weapon, or two counting the separate flash), so
	// this field didn't exist until that pack's fist/chainsaw/super
	// shotgun sequences did.
	Prefix string
}

// flashDelayFrame is FlashFrames' sentinel for "no muzzle flash yet, but
// hold this long before checking again" — Frame 0 isn't a valid sprite
// frame letter, so currentSprite treats it as hasFlash=false the same as
// running past the end of FlashFrames entirely. Some of the HD smooth-
// weapon port's sequences trigger their actual muzzle flash well after the
// shot starts (id's own A_GunFlash call site, mid fire-animation) — e.g.
// the shotgun's brief pump delay, or the BFG's ~30-tic charge-up — and
// without this, the flash would show for the FULL fire animation instead
// of just its authored window.
func flashDelayFrame(tics int) WeaponFrame { return WeaponFrame{Frame: 0, Tics: tics} }

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

	// Powered is used instead of Projectile while a Heretic Tome of Power is
	// active (engine/heretic_weapons.go). PoweredHitscanShots > 1 gives a
	// hitscan weapon (the Elven Wand) that many shots while empowered;
	// PoweredMeleeMul multiplies a melee weapon's damage (Staff / Gauntlets)
	// and, if PoweredLeech, heals the player for a quarter of it.
	Powered             *ProjectileDef
	PoweredHitscanShots int
	PoweredMeleeMul     int
	PoweredLeech        bool
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
	// Spray marks the BFG ball: on impact, every shootable thing the
	// shooter can see in a wide forward cone takes a second, independent
	// hit — see aBFGSpray. Unlike ExplodeRadius (a falloff blast centred on
	// where the shot landed), the spray is centred on the SHOOTER, so it
	// reaches every monster in the room the player can see, not just ones
	// clustered near the impact point — the BFG's actual signature effect.
	Spray bool
	// Damage is the direct-hit damage multiplier — id's mobjinfo.damage for
	// the missile. A shot that strikes a monster deals (P_Random()%8 + 1) *
	// Damage (so 8x the base at most), then, if ExplodeRadius > 0 or Spray
	// is set, the blast/spray on top. 0 = a purely cosmetic projectile.
	Damage int
	// Shots > 1 fires a fan of that many projectiles, SpreadRad radians
	// apart, centred on the aim — the Ethereal Crossbow's triple bolt and
	// several Tome-of-Power modes (engine/heretic_weapons.go). 0/1 = single.
	Shots     int
	SpreadRad float64
}

// numAmmoTypes sizes PlayerStats.Ammo/MaxAmmo. Doom uses 0..3 (bullets,
// shells, cells, rockets); Heretic adds 4 = phoenix flame orbs, 5 = mace
// spheres (see engine/heretic_weapons.go). A Doom session simply never
// touches 4/5.
const numAmmoTypes = 6

// Weapon indices — id's weapontype_t order exactly, so Weapons[] and
// PlayerStats.Weapons[] can be indexed by these directly. In a Heretic
// session applyHereticWeapons overwrites each slot's WeaponDef in place
// (staff/wand/crossbow/dragon-claw/hellstaff/phoenix/gauntlets/firemace) —
// see engine/heretic_weapons.go.
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
//
// Every viewmodel sequence below (SpritePrefix/FlashPrefix/FireFrames/
// FlashFrames/IdleFrames) is ported from "Smooth HD Weapons for Doom" (
// assets/weapons/HDSmoothWeapons.pk3, by BohdanZPM/jdrake — a GZDoom
// ZScript mod, zscript/SmoothWeapons*.zc) rather than id's own info.c —
// replacing this project's original vanilla-timed tables. Ported means:
// the actual sprite names, frame letters and tic durations from that
// mod's state chains, translated 1:1 where this engine's simpler model
// allows. Three deliberate departures from a byte-for-byte port, kept
// consistent across every weapon below:
//
//  1. ONE shot's worth of frames, not the mod's own multi-shot bundling.
//     GZDoom's Fire state can loop/rebranch internally (the chaingun's
//     literally fires two rounds and a "ready wiggle" per Fire visit); this
//     engine's model is "FireWeapon() plays FireFrames once, then this
//     project's own auto-refire replays it if the trigger's still held" —
//     unchanged by this port, so ammo cost/damage/fire rate stay exactly
//     what they were. Where the mod bundles more than one shot, only the
//     first is kept.
//  2. GZDoom-only mechanics this raster engine has no equivalent for are
//     dropped, not approximated: Decal, A_WeaponOffset recoil-kick
//     keyframing (beyond this project's own existing weaponBob sway), and
//     the custom additive-blend/rescaled plasma+BFG projectile actors —
//     Projectile below still points at this project's original sprite/
//     speed/damage data, untouched.
//  3. A weapon's trailing "wind back down to the resting pose" tail is
//     dropped where it's just idle art repeated (this project's own
//     IdleFrames/default frame 'A' already takes over the instant
//     phase != weaponFiring, so replaying the wind-down every single shot
//     of a sustained burst would only slow the loop down for no visual
//     gain).
//
// A handful of sequences switch sprite FAMILY mid-animation (not just
// frame letter) — WeaponFrame.Prefix carries that; see its own doc
// comment. flashDelayFrame marks a muzzle flash that doesn't start at the
// very first fire frame (the shotgun's brief pump delay, the BFG's long
// charge-up) — see its own doc comment.
var Weapons = [numWeapons]WeaponDef{
	wpFist: {
		Name: "Fist", SpritePrefix: "FIS0", FireSound: "DSPUNCH",
		AmmoType: -1, AmmoCost: 0,
		// SmoothFist Fire: FIS0 A 1; FI10 BCD 1; FIS0 E 3 (A_Punch); FIS0
		// FGHIJ 2 — the A_WeaponOffset recoil-settle tail after A_Refire is
		// dropped (frame 'A' repeated at different offsets only, no new art).
		FireFrames: []WeaponFrame{
			{Frame: 'A', Tics: 1},
			{Frame: 'B', Tics: 1, Prefix: "FI10"},
			{Frame: 'C', Tics: 1, Prefix: "FI10"},
			{Frame: 'D', Tics: 1, Prefix: "FI10"},
			{Frame: 'E', Tics: 3},
			{Frame: 'F', Tics: 2},
			{Frame: 'G', Tics: 2},
			{Frame: 'H', Tics: 2},
			{Frame: 'I', Tics: 2},
			{Frame: 'J', Tics: 2},
		},
		Melee: true,
	},
	wpPistol: {
		Name: "Pistol", SpritePrefix: "PI0G", FlashPrefix: "PI0L", FireSound: "DSPISTOL",
		AmmoType: 0, AmmoCost: 1,
		// SmoothPistol Fire: PI0F A 3 (recoil, A_GunFlash+A_FireBullets);
		// PI0G ABCD 1; PI0G EF 2; PI0G A 3 (A_ReFire point); PI0G A 5.
		FireFrames: []WeaponFrame{
			{Frame: 'A', Tics: 3, Prefix: "PI0F"},
			{Frame: 'A', Tics: 1},
			{Frame: 'B', Tics: 1},
			{Frame: 'C', Tics: 1},
			{Frame: 'D', Tics: 1},
			{Frame: 'E', Tics: 2},
			{Frame: 'F', Tics: 2},
			{Frame: 'A', Tics: 3},
			{Frame: 'A', Tics: 5},
		},
		// SmoothPistol Flash randomly picks one of 4 single-tic PI0L frames
		// (A-D, all equivalent bright light bumps) — simplified to a fixed
		// PI0L A held for the recoil frame's own duration.
		FlashFrames: []WeaponFrame{{Frame: 'A', Tics: 4}},
	},
	wpShotgun: {
		Name: "Shotgun", SpritePrefix: "SHOG", FlashPrefix: "SHOL", FireSound: "DSSHOTGN",
		AmmoType: 1, AmmoCost: 1,
		// SmoothShotgun Fire: SHOG A 3 (pump-ready pose, no action yet);
		// SHOG A 3 (A_GunFlash+A_FireBullets — the actual shot, 3 tics into
		// the animation); SHOG BDFHIJKL 1; SHOG M 2; SHOG NO 3; SHOG
		// NLKJIHGFE 1; SHOG DCB 2; SHOG A 7 (A_ReFire point).
		FireFrames: []WeaponFrame{
			{Frame: 'A', Tics: 3},
			{Frame: 'A', Tics: 3},
			{Frame: 'B', Tics: 1}, {Frame: 'D', Tics: 1}, {Frame: 'F', Tics: 1}, {Frame: 'H', Tics: 1},
			{Frame: 'I', Tics: 1}, {Frame: 'J', Tics: 1}, {Frame: 'K', Tics: 1}, {Frame: 'L', Tics: 1},
			{Frame: 'M', Tics: 2},
			{Frame: 'N', Tics: 3}, {Frame: 'O', Tics: 3},
			{Frame: 'N', Tics: 1}, {Frame: 'L', Tics: 1}, {Frame: 'K', Tics: 1}, {Frame: 'J', Tics: 1},
			{Frame: 'I', Tics: 1}, {Frame: 'H', Tics: 1}, {Frame: 'G', Tics: 1}, {Frame: 'F', Tics: 1}, {Frame: 'E', Tics: 1},
			{Frame: 'D', Tics: 2}, {Frame: 'C', Tics: 2}, {Frame: 'B', Tics: 2},
			{Frame: 'A', Tics: 7},
		},
		// A_GunFlash isn't called until the 2nd SHOG-A-3 state (see above) —
		// flashDelayFrame holds "no flash yet" for that first 3-tic pump
		// pose so the muzzle flash doesn't show a beat early.
		FlashFrames: []WeaponFrame{
			flashDelayFrame(3),
			{Frame: 'A', Tics: 4},
			{Frame: 'B', Tics: 3},
		},
	},
	wpChaingun: {
		Name: "Chaingun", SpritePrefix: "CHNG", FlashPrefix: "CHNF", FireSound: "DSPISTOL",
		AmmoType: 0, AmmoCost: 1,
		// SmoothChaingun fires two rounds per Fire visit (its own
		// A_CheckReload mid-sequence loop); one shot's worth is CHNG A 1
		// (A_GunFlash+A_FireBullets) + CHNG BCD 1 — 4 tics total, which
		// happens to land on exactly this project's own already-doubled
		// firing rate (see the previous vanilla-timed table's own note:
		// 2 tics/frame, 4 tics/shot, ~8.75/sec — unchanged by this port).
		FireFrames: []WeaponFrame{
			{Frame: 'A', Tics: 1},
			{Frame: 'B', Tics: 1},
			{Frame: 'C', Tics: 1},
			{Frame: 'D', Tics: 1},
		},
		FlashFrames: []WeaponFrame{{Frame: 'A', Tics: 2}},
	},
	wpMissile: {
		Name: "Rocket Launcher", SpritePrefix: "RLAN", FlashPrefix: "RLAF", FireSound: "DSRLAUNC",
		AmmoType: 3, AmmoCost: 1,
		// SmoothRocketLauncher Fire: RLAN A 4 (A_GunFlash); B 2; D 3
		// (A_FireMissile); C 3; BE 2; FG 2 (A_ReFire point) — 20 tics total,
		// same overall duration as the original single-frame 20-tic hold.
		FireFrames: []WeaponFrame{
			{Frame: 'A', Tics: 4},
			{Frame: 'B', Tics: 2},
			{Frame: 'D', Tics: 3},
			{Frame: 'C', Tics: 3},
			{Frame: 'B', Tics: 2},
			{Frame: 'E', Tics: 2},
			{Frame: 'F', Tics: 2},
			{Frame: 'G', Tics: 2},
		},
		// SmoothRocketLauncher Flash: RLAF A 2; B 2; C 2; DE 3 — ported
		// whole, no branching/simplification needed.
		FlashFrames: []WeaponFrame{
			{Frame: 'A', Tics: 2},
			{Frame: 'B', Tics: 2},
			{Frame: 'C', Tics: 2},
			{Frame: 'D', Tics: 3},
			{Frame: 'E', Tics: 3},
		},
		// MT_ROCKET's canon speed is 20*35=700 units/sec; per user
		// feedback this project's own rocket runs 20% faster than that.
		// deathsound sfx_barexp (DSBAREXP) — rockets and exploding barrels
		// share an explosion SOUND in vanilla, not an approximation. The
		// SPRITE is not shared, though: the rocket's own S_EXPLODE1..3
		// states play its flying MISL sprite's own B/C/D frames (a fireball
		// baked into the same lump as the flying rocket), not the barrel's
		// separate BEXP animation — using BEXP here was this project's own
		// mistake, not a vanilla quirk, and showed the barrel's fireball
		// shape on every rocket impact. All verified present in
		// testdata/DOOM1.WAD (MISLA1, MISLB0-D0, DSBAREXP). Untouched by
		// the HD viewmodel port above — this is the flying projectile's own
		// sprite, not the held weapon's.
		Projectile: &ProjectileDef{
			Sprite: "MISLA1", Speed: 700 * 1.2,
			ExplodePrefix: "MISL", ExplodeFrames: []byte{'B', 'C', 'D'}, ExplodeTics: 4,
			ExplodeSound: "DSBAREXP", ExplodeRadius: 128,
			Damage: 20, // MT_ROCKET: direct hit deals (P_Random%8+1)*20, plus the 128u blast
		},
	},
	wpPlasma: {
		Name: "Plasma Rifle", SpritePrefix: "PLGG", FlashPrefix: "PLSF", FireSound: "DSPLASMA",
		AmmoType: 2, AmmoCost: 1,
		// SmoothPlasmaRifle's Fire1/2/3 phases each play one PLGF frame (A/
		// B/C respectively, all "Bright A_FireProjectile") then either
		// re-enter for the next phase (held fire) or fall into the PLGC
		// idle-wobble tail — that tail is dropped (rule 3 above), and since
		// this engine always restarts FireFrames at the same point, only
		// PLGF A is used rather than cycling A/B/C; the visual difference
		// between those three is minor (all the same "bright fire" pose).
		// 3 tics/shot matches this project's own already-tuned held-fire
		// cadence (see the previous vanilla-timed table's note: trimmed
		// from a literal 3+20 to land near vanilla's real ~3-tic A_ReFire
		// loop rate) — unchanged by this port.
		FireFrames: []WeaponFrame{{Frame: 'A', Tics: 3, Prefix: "PLGF"}},
		// SmoothPlasmaRifle's own Flash is TNT1 (invisible, a pure
		// A_Light1 brightness bump with no separate flash sprite) — this
		// project keeps the original vanilla PLSF flash sprite instead of
		// dropping the muzzle flash light entirely (lights.go's
		// muzzleFlashLight gates the dynamic flash-light spawn on
		// hasFlash), since the mod doesn't provide a replacement asset for
		// it either way.
		FlashFrames: []WeaponFrame{{Frame: 'A', Tics: 4}},
		// MT_PLASMA: speed 25*35=875 units/sec. Sprite/explosion/sound
		// names (PLSS the flying ball, PLSE its splat, DSPLASMA reused for
		// no distinct impact sound in vanilla) are NOT verified against
		// any WAD — the shareware IWAD this project was built and tested
		// against has no plasma rifle assets at all (it's Episode 2/3-only
		// content), so this has never actually been exercised. Untouched
		// by the HD viewmodel port above.
		Projectile: &ProjectileDef{
			Sprite: "PLSSA0", Speed: 875,
			ExplodePrefix: "PLSE", ExplodeFrames: []byte{'A', 'B', 'C', 'D', 'E'}, ExplodeTics: 4,
			ExplodeSound: "DSFIRXPL",
			Damage:       5, // MT_PLASMA: direct hit only, (P_Random%8+1)*5
		},
	},
	wpBFG: {
		Name: "BFG9000", SpritePrefix: "BBGF", FlashPrefix: "BBGF", FireSound: "DSBFG",
		AmmoType: 2, AmmoCost: 40,
		// SmoothBFG9000 Fire: BBGN ABCDE 3 (windup pose, no action yet);
		// BBGF AABBCCDDDDEEEEE 1 (the charge-up "crackle" loop, 15 tics);
		// A_GunFlash; BBGF F 5 Bright (A_FireProjectile — the actual
		// discharge); BBGF EEBBA 1 (A_ReFire point). The trailing
		// BBGN-wind-down-to-ready tail is dropped (rule 3). Kept the full
		// ~40-tic charge-then-fire drama on purpose — it's the BFG's most
		// iconic beat and this engine can afford it (the projectile itself
		// still launches instantly on trigger-pull, same as every other
		// projectile weapon here; only the viewmodel animation takes its
		// time).
		FireFrames: []WeaponFrame{
			{Frame: 'A', Tics: 3, Prefix: "BBGN"}, {Frame: 'B', Tics: 3, Prefix: "BBGN"}, {Frame: 'C', Tics: 3, Prefix: "BBGN"},
			{Frame: 'D', Tics: 3, Prefix: "BBGN"}, {Frame: 'E', Tics: 3, Prefix: "BBGN"},
			{Frame: 'A', Tics: 1}, {Frame: 'A', Tics: 1},
			{Frame: 'B', Tics: 1}, {Frame: 'B', Tics: 1},
			{Frame: 'C', Tics: 1}, {Frame: 'C', Tics: 1},
			{Frame: 'D', Tics: 1}, {Frame: 'D', Tics: 1}, {Frame: 'D', Tics: 1}, {Frame: 'D', Tics: 1},
			{Frame: 'E', Tics: 1}, {Frame: 'E', Tics: 1}, {Frame: 'E', Tics: 1}, {Frame: 'E', Tics: 1}, {Frame: 'E', Tics: 1},
			{Frame: 'F', Tics: 5},
			{Frame: 'E', Tics: 1}, {Frame: 'E', Tics: 1},
			{Frame: 'B', Tics: 1}, {Frame: 'B', Tics: 1},
			{Frame: 'A', Tics: 1},
		},
		// The muzzle flash doesn't start until A_GunFlash fires — 30 tics
		// into the sequence (the windup + charge-up above) — then holds for
		// the same 5 tics as the BBGF-F discharge frame.
		FlashFrames: []WeaponFrame{
			flashDelayFrame(30),
			{Frame: 'F', Tics: 3},
			{Frame: 'F', Tics: 2},
		},
		// MT_BFG: speed 25*35=875 units/sec. Same caveat as Plasma Rifle
		// above — BFS1/BFE1 names are unverified, no BFG assets exist in
		// the shareware IWAD this project was tested against. Untouched by
		// the HD viewmodel port above.
		Projectile: &ProjectileDef{
			Sprite: "BFS1A0", Speed: 875,
			ExplodePrefix: "BFE1", ExplodeFrames: []byte{'A', 'B', 'C', 'D'}, ExplodeTics: 8,
			ExplodeSound: "DSRXPLOD",
			Damage:       100,  // MT_BFG: direct hit (P_Random%8+1)*100
			Spray:        true, // plus the room-clearing spray on impact — see aBFGSpray
		},
	},
	wpChainsaw: {
		Name: "Chainsaw", SpritePrefix: "SAWN", FireSound: "DSSAWFUL",
		AmmoType: -1, AmmoCost: 0,
		// SmoothChainsaw Fire (after dropping the 0-tic A_Saw/A_Refire
		// action states and the SAWN-CBA wind-down tail, rule 3): SAWN ABC
		// 1 (windup); SAWF A 1 (loop sound + A_Saw start); SAWF ABAB 1
		// (the actual cutting jitter). 8 tics total — happens to match the
		// idle loop's own 8-tic cycle below.
		FireFrames: []WeaponFrame{
			{Frame: 'A', Tics: 1},
			{Frame: 'B', Tics: 1},
			{Frame: 'C', Tics: 1},
			{Frame: 'A', Tics: 1, Prefix: "SAWF"},
			{Frame: 'A', Tics: 1, Prefix: "SAWF"},
			{Frame: 'B', Tics: 1, Prefix: "SAWF"},
			{Frame: 'A', Tics: 1, Prefix: "SAWF"},
			{Frame: 'B', Tics: 1, Prefix: "SAWF"},
		},
		// SmoothChainsaw Ready: SAWB BCDC 2, looped — a different sprite
		// family (SAWB) than Fire's SAWN/SAWF, unlike the vanilla single-
		// sprite SAWG this replaces.
		IdleFrames: []WeaponFrame{
			{Frame: 'B', Tics: 2, Prefix: "SAWB"},
			{Frame: 'C', Tics: 2, Prefix: "SAWB"},
			{Frame: 'D', Tics: 2, Prefix: "SAWB"},
			{Frame: 'C', Tics: 2, Prefix: "SAWB"},
		},
		IdleSound: "DSSAWIDL",
		Melee:     true,
	},
	wpSuperShotgun: {
		Name: "Super Shotgun", SpritePrefix: "SH2N", FlashPrefix: "SH2P", FireSound: "DSDSHTGN",
		AmmoType: 1, AmmoCost: 2,
		// SmoothSuperShotgun Fire: SH2N A 3 (A_GunFlash) + A 2
		// (A_FireBullets) + A 6 (the double-barrel discharge); then the
		// break-action reload, all SH2R — ABCDEFGH 1, I 1, JKL 1, MNOPQX 2,
		// Y 2, Z 2; then the snap-shut, all SH2S — ABCD 1, EFGH 2, H 1;
		// then SH2N A 5 (A_ReFire point). 57 tics, comparable to the
		// original vanilla-timed table's own 54.
		FireFrames: []WeaponFrame{
			{Frame: 'A', Tics: 3},
			{Frame: 'A', Tics: 2},
			{Frame: 'A', Tics: 6},
			{Frame: 'A', Tics: 1, Prefix: "SH2R"}, {Frame: 'B', Tics: 1, Prefix: "SH2R"},
			{Frame: 'C', Tics: 1, Prefix: "SH2R"}, {Frame: 'D', Tics: 1, Prefix: "SH2R"},
			{Frame: 'E', Tics: 1, Prefix: "SH2R"}, {Frame: 'F', Tics: 1, Prefix: "SH2R"},
			{Frame: 'G', Tics: 1, Prefix: "SH2R"}, {Frame: 'H', Tics: 1, Prefix: "SH2R"},
			{Frame: 'I', Tics: 1, Prefix: "SH2R"},
			{Frame: 'J', Tics: 1, Prefix: "SH2R"}, {Frame: 'K', Tics: 1, Prefix: "SH2R"}, {Frame: 'L', Tics: 1, Prefix: "SH2R"},
			{Frame: 'M', Tics: 2, Prefix: "SH2R"}, {Frame: 'N', Tics: 2, Prefix: "SH2R"}, {Frame: 'O', Tics: 2, Prefix: "SH2R"},
			{Frame: 'P', Tics: 2, Prefix: "SH2R"}, {Frame: 'Q', Tics: 2, Prefix: "SH2R"}, {Frame: 'X', Tics: 2, Prefix: "SH2R"},
			{Frame: 'Y', Tics: 2, Prefix: "SH2R"},
			{Frame: 'Z', Tics: 2, Prefix: "SH2R"},
			{Frame: 'A', Tics: 1, Prefix: "SH2S"}, {Frame: 'B', Tics: 1, Prefix: "SH2S"},
			{Frame: 'C', Tics: 1, Prefix: "SH2S"}, {Frame: 'D', Tics: 1, Prefix: "SH2S"},
			{Frame: 'E', Tics: 2, Prefix: "SH2S"}, {Frame: 'F', Tics: 2, Prefix: "SH2S"},
			{Frame: 'G', Tics: 2, Prefix: "SH2S"}, {Frame: 'H', Tics: 2, Prefix: "SH2S"},
			{Frame: 'H', Tics: 1, Prefix: "SH2S"},
			{Frame: 'A', Tics: 5},
		},
		// SmoothSuperShotgun's own Flash randomly picks a branch through
		// SH2P A-F; simplified to a fixed 2-frame flash, the same
		// simplification the original vanilla-timed table already made
		// (id's own SSG flash is likewise a random pick between two
		// frames).
		FlashFrames: []WeaponFrame{{Frame: 'A', Tics: 4}, {Frame: 'B', Tics: 3}},
		// A_OpenShotgun2 / A_LoadShotgun2 / A_CloseShotgun2 sounds, retimed
		// to this port's own break-open (SH2R start), shell-load (SH2R Y),
		// and snap-shut (SH2S H) points.
		FireSoundSeq: []timedSound{
			{AtTic: 19, Sound: "DSDBOPN"},
			{AtTic: 35, Sound: "DSDBLOAD"},
			{AtTic: 51, Sound: "DSDBCLS"},
		},
	},
}

// weaponRaiseSpeed is id's own RAISESPEED/LOWERSPEED (6 fixed-point units
// per tic, over a WEAPONBOTTOM-WEAPONTOP span of 96 units — see p_pspr.c),
// converted into this project's 0..1 Offset space and real seconds:
// 96/6 = 16 tics to fully raise or lower.
const weaponRaiseSpeed = ticsPerSecond / 16.0

// Weapon bob: a side-to-side sway, an up-and-down bob, and a slight
// toward/away-from-camera motion, all of the viewmodel while walking — like
// a held object swinging on an arc with each step rather than sliding flat
// left-right. How WIDE/HIGH/CLOSE it gets and how FAST it cycles both track
// the player's actual speed (0 standing still, full bobCycleHz/
// weaponBobMaxX/weaponBobMaxY/weaponBobDepth at moveTopSpeed) — a run
// doesn't just move further than a walk, it cycles quicker too, the way a
// faster stride actually looks.
//
// All three motions share bobPhase (see updateWeapon), just at different
// multiples of it, which is what turns three independent sine waves into
// one coherent arc instead of three unrelated wobbles:
//   - X (weaponBobX):     sin(phase)          — side to side.
//   - Y (weaponBobY):     abs(sin(2*phase))   — up, twice a cycle, never down.
//   - scale (weaponBobScale): cos(2*phase)    — closest at dead centre
//     (moving fastest sideways), farthest at each side's extreme (paused
//     an instant before swinging back) — so the RETURN half of every swing
//     reads as the gun coming back toward the camera as it re-centres, the
//     "half circular" arc this was asked to trace rather than a flat slide.
const (
	bobCycleHz     = 0.7  // side-to-side sway cycles/sec at top speed
	weaponBobMaxX  = 12.0 // sway's full horizontal amplitude in HUD pixels (raster/weapon.go's 320x200 space) at top speed
	weaponBobMaxY  = 5.0  // bob's full vertical amplitude, same pixel space
	weaponBobDepth = 0.06 // toward/away-from-camera size swing, +/- a fraction of the gun's normal size
)

// moveSpeedFrac is the player's current horizontal speed as a 0..1 fraction
// of moveTopSpeed, clamped so a speed boost (e.g. a ledge-launch) can't
// push weapon bob past its intended range.
func (g *Game) moveSpeedFrac() float64 {
	f := math.Hypot(g.VelX, g.VelY) / moveTopSpeed
	if f > 1 {
		f = 1
	}
	return f
}

// weaponBob computes all three per-frame weapon-bob values together,
// sharing the one moveSpeedFrac() call and the one sin/cos of 2*bobPhase
// (math.Sincos) that weaponBobX/Y/Scale used to each independently redo —
// they're always read back-to-back for the same frame (see game.go's
// renderFrame), so three passes over the same handful of trig calls was
// pure waste. weaponBobX/Y/Scale remain as the named per-axis accessors
// (kept for their own doc comments and so existing single-value callers/
// tests don't need to change) but now just read one field of this.
func (g *Game) weaponBob() (x, y, scale float64) {
	frac := g.moveSpeedFrac()
	sin2, cos2 := math.Sincos(2 * g.Weapon.bobPhase)
	x = weaponBobMaxX * frac * math.Sin(g.Weapon.bobPhase)
	y = weaponBobMaxY * frac * math.Abs(sin2)
	scale = 1 + weaponBobDepth*frac*cos2
	return x, y, scale
}

// weaponBobX is the current horizontal sway offset (HUD pixels,
// raster/weapon.go's 320x200 space): a sine wave whose amplitude scales
// with moveSpeedFrac, so the gun sits dead still at rest and sways with
// increasing width up to weaponBobMaxX at a run. The sway's own phase (see
// updateWeapon) is advanced by that same fraction, so the cycle itself
// speeds up right along with it — standing still freezes the phase as well
// as zeroing the amplitude, rather than a silent sine wave ticking away at
// full speed underneath a 0-width sway.
func (g *Game) weaponBobX() float64 { x, _, _ := g.weaponBob(); return x }

// weaponBobY is the current vertical bob offset (same HUD pixel space,
// positive = risen above rest — see raster.DrawWeapon): abs(sin(2*phase))
// so it rises and settles twice per horizontal sway cycle (once per
// footfall) and never dips below the weapon's resting position, scaled by
// the same moveSpeedFrac as weaponBobX.
func (g *Game) weaponBobY() float64 { _, y, _ := g.weaponBob(); return y }

// weaponBobScale is the current toward/away-from-camera size multiplier
// (1 = normal, raster.DrawWeapon's bobScale): cos(2*phase) so the gun is
// biggest (closest) every time the sway passes back through centre and
// smallest (farthest) at each side's extreme, tracing the "half circular"
// arc described above rather than a flat left-right slide. Scaled by
// moveSpeedFrac like the other two, so it's exactly 1 (no size change) at
// rest.
func (g *Game) weaponBobScale() float64 { _, _, s := g.weaponBob(); return s }

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

	// bobPhase is a free-running angle (radians) driving the weapon's side-
	// to-side sway while walking — see weaponBobX. Advances at a constant
	// rate regardless of speed (only the sway's AMPLITUDE depends on how
	// fast the player is moving); kept wrapped to [0, 2*pi) so it never
	// grows unbounded over a long play session.
	bobPhase float64
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

// weaponCycleOrder lists every weapon index in mouse-wheel cycle order:
// weaponSlots flattened slot 1 through 7 (each slot's own toggle order),
// so scrolling steps through weapons in the same layout the 1-7 keys
// already teach the player, just without needing to know which slot a
// weapon lives in.
var weaponCycleOrder = func() []int {
	var order []int
	for slot := 1; slot < len(weaponSlots); slot++ {
		order = append(order, weaponSlots[slot]...)
	}
	return order
}()

// CycleWeapon switches to the next (dir > 0) or previous (dir < 0) owned
// weapon in weaponCycleOrder, wrapping around — the mouse-wheel weapon
// switch (combatSystem.Update). A no-op if dir is 0 or the player owns
// fewer than two weapons.
func (g *Game) CycleWeapon(dir int) {
	if dir == 0 {
		return
	}
	owned := make([]int, 0, len(weaponCycleOrder))
	for _, w := range weaponCycleOrder {
		if g.Player.Weapons[w] {
			owned = append(owned, w)
		}
	}
	if len(owned) < 2 {
		return
	}
	cur := g.Weapon.Current
	if g.Weapon.pending >= 0 {
		cur = g.Weapon.pending
	}
	idx := 0
	for i, w := range owned {
		if w == cur {
			idx = i
			break
		}
	}
	n := len(owned)
	next := owned[((idx+dir)%n+n)%n]
	g.SwitchWeapon(next)
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

	// Heretic Tome of Power: swap in the powered projectile / behaviour.
	tome := g.heretic && g.Player.Powers[pwTome] > 0
	proj := def.Projectile
	if tome && def.Powered != nil {
		proj = def.Powered
	}
	switch {
	case proj != nil:
		shots := g.spawnProjectile(proj)
		// RocketFollow: firing the rocket launcher with the right mouse
		// button held rides the camera along with the shot until it
		// explodes — see rocketFollowCamera (projectiles.go) and the
		// g.rocketCam swap in renderFrame.
		if g.Weapon.Current == wpMissile && len(shots) > 0 && g.Window != nil && g.Window.MouseRightDown() {
			g.rocketCam = shots[0]
		}
	case def.Melee:
		g.fireMelee()
	default:
		g.fireHitscan(g.Weapon.Current)
	}
	return true
}

// hitscanSpread is bulletSpread, except it returns 0 while the isometric
// front end has the crosshair snapped onto a specific target
// (Game.IsoAimLocked — see its own doc comment): playerMobj.Angle is
// already the exact angle to that target's centre, so dropping the random
// deviation this shot would otherwise get turns "aimed precisely" into "hits
// whatever's there," modulo an actual wall or another mobj blocking the
// line — which still correctly blocks it, since this only removes the
// random-spread term, not pLineAttack's own trace. Unused by the
// first-person front end (IsoAimLocked is always false there).
func (g *Game) hitscanSpread() float64 {
	if g.IsoAimLocked {
		return 0
	}
	return bulletSpread()
}

// fireMelee handles the two melee weapons — fist (A_Punch) and chainsaw
// (A_Saw): one very short-range hit per fire frame, no ammo, id's damage of
// 2*(P_Random%10+1), x10 for a berserk fist. The small angle jitter is
// their own "spray" so a swing catches things slightly off-centre.
func (g *Game) fireMelee() {
	if g.playerMobj == nil {
		return
	}
	def := Weapons[g.Weapon.Current]
	damage := 2 * (pRandom()%10 + 1)
	rng := meleeRange
	if g.Weapon.Current == wpFist {
		if g.Player.Powers[pwStrength] > 0 {
			damage *= 10 // berserk
		}
	} else {
		rng = meleeRange + 1 // A_Saw reaches one unit further
	}
	// Heretic Tome of Power: the Staff / Gauntlets hit harder and reach
	// further while empowered, and the Gauntlets leech a little life on any
	// swing that lands on a monster.
	leech := false
	if g.heretic && g.Player.Powers[pwTome] > 0 && def.PoweredMeleeMul > 0 {
		damage *= def.PoweredMeleeMul
		rng += 32
		leech = def.PoweredLeech
	}
	// Heretic melee spark: green for the Gauntlets, the tan staff spark
	// otherwise. Ignored outside a Heretic session.
	hpuff := "PUF3"
	if g.Weapon.Current == wpChainsaw {
		hpuff = "PUF1"
	}
	ang := g.playerMobj.Angle + g.hitscanSpread()/4
	g.pLineAttack(g.playerMobj, ang, g.playerAimSlope(), rng, damage, hpuff)
	if leech && g.aimTargetInFront(rng+16) != nil && g.Player.Health < g.dm().maxHealth {
		g.Player.Health += 2 // powered Gauntlets drink life
		if g.Player.Health > g.dm().maxHealth {
			g.Player.Health = g.dm().maxHealth
		}
		g.syncPlayerMobjHealth()
	}
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
	// In Heretic every hitscan weapon here is the Elven Wand — its impact
	// spark is the little yellow flame (PUF2). Ignored for Doom.
	hpuff := "PUF2"

	// Heretic Tome of Power — the Elven Wand fires a wide horizontal fan.
	if g.heretic && g.Player.Powers[pwTome] > 0 {
		if n := Weapons[weapon].PoweredHitscanShots; n > 1 {
			for i := 0; i < n; i++ {
				ang := g.playerMobj.Angle + (float64(i)-float64(n-1)/2)*(6*math.Pi/180)
				g.pLineAttack(g.playerMobj, ang, baseSlope, missileRange, dmg(), hpuff)
			}
			return
		}
	}

	if weapon == wpSuperShotgun {
		for i := 0; i < 20; i++ {
			ang := g.playerMobj.Angle + g.hitscanSpread()/2
			slope := baseSlope
			if !g.IsoAimLocked {
				slope += float64(pRandomSpread()) / 2048.0
			}
			g.pLineAttack(g.playerMobj, ang, slope, missileRange, dmg(), hpuff)
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
			ang += g.hitscanSpread() / 4
		}
		g.pLineAttack(g.playerMobj, ang, baseSlope, missileRange, dmg(), hpuff)
	}
}

// shootableInCone reports whether mo is a live, shootable target within
// maxDist of (x,y) and within halfAngle of angle — the target-acquisition
// shape playerAimSlope and aBFGSpray (attack.go) both need (shootable/alive,
// range, facing cone), factored out so the two can't drift apart on how a
// candidate target is filtered. Each caller still layers its own extra
// condition on top: playerAimSlope wants the closest match (so it passes
// its shrinking best-so-far as maxDist and still re-checks d against it
// strictly), aBFGSpray excludes the shooter itself and requires a clear
// line of sight before it counts as hit.
func shootableInCone(mo *Mobj, x, y, angle, maxDist, halfAngle float64) (dist float64, ok bool) {
	if mo == nil || mo.removed || mo.Flags&MF_SHOOTABLE == 0 || mo.Health <= 0 {
		return 0, false
	}
	dx, dy := mo.X-x, mo.Y-y
	d := math.Hypot(dx, dy)
	if d > maxDist {
		return 0, false
	}
	if math.Abs(normAngle(math.Atan2(dy, dx)-angle)) > halfAngle {
		return 0, false
	}
	return d, true
}

// playerAimSlope is a lightweight auto-aim: if a shootable thing sits
// within ~6° of where the player is looking, lock the vertical slope onto
// it (Doom's assist); otherwise use the view pitch.
func (g *Game) playerAimSlope() float64 {
	pm := g.playerMobj
	var best *Mobj
	bestDist := missileRange
	for _, mo := range g.mobjs {
		d, ok := shootableInCone(mo, pm.X, pm.Y, pm.Angle, bestDist, 6*math.Pi/180)
		if !ok || d >= bestDist {
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
	w.bobPhase += dt * bobCycleHz * 2 * math.Pi * g.moveSpeedFrac()
	if w.bobPhase >= 2*math.Pi {
		w.bobPhase = math.Mod(w.bobPhase, 2*math.Pi)
	}
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

// currentSprite reports which viewmodel sprite prefix+frame letter is
// showing right now, and which flash prefix+frame (if any) is overlaid.
// gunPrefix/flashPrefix are def.SpritePrefix/def.FlashPrefix unless the
// active WeaponFrame sets its own Prefix (see that field's doc comment —
// a few weapons' fire sequences switch sprite family mid-animation).
//
// Not firing: frame 'A' (id's ready/raise/lower states) unless the weapon
// defines IdleFrames — the chainsaw, whose C/D idle loops forever to give
// the running saw its vibration. Firing: FireFrames / FlashFrames indexed
// by the time since the shot started.
func (g *Game) currentSprite() (gunPrefix string, gunFrame byte, flashPrefix string, flashFrame byte, hasFlash bool) {
	def := Weapons[g.Weapon.Current]
	gunPrefix, flashPrefix = def.SpritePrefix, def.FlashPrefix

	if g.Weapon.phase != weaponFiring {
		if f, ok := frameAtLooped(def.IdleFrames, g.Weapon.idleTics); ok {
			if f.Prefix != "" {
				gunPrefix = f.Prefix
			}
			return gunPrefix, f.Frame, flashPrefix, 0, false
		}
		return gunPrefix, 'A', flashPrefix, 0, false
	}

	gunFrame = 'A'
	if f, ok := frameAt(def.FireFrames, g.Weapon.fireElapsedTics); ok {
		gunFrame = f.Frame
		if f.Prefix != "" {
			gunPrefix = f.Prefix
		}
	}
	if f, ok := frameAt(def.FlashFrames, g.Weapon.fireElapsedTics); ok && f.Frame != 0 {
		if f.Prefix != "" {
			flashPrefix = f.Prefix
		}
		return gunPrefix, gunFrame, flashPrefix, f.Frame, true
	}
	return gunPrefix, gunFrame, flashPrefix, 0, false
}
