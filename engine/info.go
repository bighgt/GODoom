package engine

// Ported from id Software's info.c: mobjinfo[] — the per-type template
// every Mobj is spawned from. Fields mirror id's mobjinfo_t so values can
// be checked against the original line by line. Phase 1 populates
// doomednum / spawnstate / spawnhealth / radius / height / mass / flags
// accurately (enough to spawn and render every thing); the see/pain/
// attack/death state links and sounds are filled in the phases that use
// them.

// MF_* mobj flags (id's p_mobj.h).
const (
	MF_SPECIAL      = 1 << 0  // call P_TouchSpecialThing when touched
	MF_SOLID        = 1 << 1  // blocks movement
	MF_SHOOTABLE    = 1 << 2  // can be hit
	MF_NOSECTOR     = 1 << 3  // not linked into a sector
	MF_NOBLOCKMAP   = 1 << 4  // not linked into the blockmap
	MF_AMBUSH       = 1 << 5  // deaf — only wakes on sight
	MF_JUSTHIT      = 1 << 6  // try to attack right back
	MF_JUSTATTACKED = 1 << 7  // take at least one step before attacking again
	MF_SPAWNCEILING = 1 << 8  // hang from the ceiling
	MF_NOGRAVITY    = 1 << 9  // don't apply gravity
	MF_DROPOFF      = 1 << 10 // allowed to move off high ledges
	MF_PICKUP       = 1 << 11 // for players — pick up items
	MF_NOCLIP       = 1 << 12 // no clipping at all
	MF_FLOAT        = 1 << 14 // floats up/down toward its target
	MF_TELEPORT     = 1 << 15 // don't cross lines or check heights while teleporting
	MF_MISSILE      = 1 << 16 // a flying missile — explode on hit, no gravity
	MF_DROPPED      = 1 << 17 // dropped by a monster, not spawned (half ammo)
	MF_SHADOW       = 1 << 18 // spectre — draw with fuzz
	MF_NOBLOOD      = 1 << 19 // spawns a puff, not blood (barrel)
	MF_CORPSE       = 1 << 20 // stop grinding under a crusher once flat
	MF_INFLOAT      = 1 << 21 // floating to a target height — don't auto-adjust
	MF_COUNTKILL    = 1 << 22 // counts toward the kill total
	MF_COUNTITEM    = 1 << 23 // counts toward the item total
	MF_SKULLFLY     = 1 << 24 // lost soul in its charge attack
	MF_NOTDMATCH    = 1 << 25 // don't spawn in deathmatch
)

// MobjInfo is one entry of id's mobjinfo[].
type MobjInfo struct {
	Name         string // for logs/tests only
	DoomedNum    int    // THINGS type number; -1 for things spawned by code
	SpawnState   stateNum
	SpawnHealth  int
	SeeState     stateNum
	SeeSound     string
	ReactionTime int
	AttackSound  string
	PainState    stateNum
	PainChance   int
	PainSound    string
	MeleeState   stateNum
	MissileState stateNum
	DeathState   stateNum
	XDeathState  stateNum
	DeathSound   string
	Speed        float64
	Radius       float64
	Height       float64
	Mass         int
	Damage       int
	ActiveSound  string
	Flags        int
	RaiseState   stateNum
}

// mobjType indexes mobjInfo.
type mobjType int

const (
	MT_PLAYER mobjType = iota
	MT_POSSESSED
	MT_SHOTGUY
	MT_CHAINGUY
	MT_TROOP
	MT_SERGEANT
	MT_SHADOWS // spectre
	MT_HEAD
	MT_SKULL
	MT_BRUISER // baron
	MT_KNIGHT  // hell knight
	MT_BARREL
	MT_MISC0  // green armor
	MT_MISC1  // blue armor
	MT_MISC2  // health bonus
	MT_MISC3  // armor bonus
	MT_MISC4  // blue keycard
	MT_MISC5  // red keycard
	MT_MISC6  // yellow keycard
	MT_MISC7  // yellow skull
	MT_MISC8  // red skull
	MT_MISC9  // blue skull
	MT_MISC10 // stimpack
	MT_MISC11 // medikit
	MT_MISC12 // soulsphere
	MT_INV    // invulnerability
	MT_MISC13 // berserk
	MT_INS    // partial invisibility
	MT_MISC14 // radiation suit
	MT_MISC15 // computer map
	MT_MISC16 // light amp visor
	MT_MEGA   // megasphere
	MT_CLIP
	MT_MISC17 // box of ammo
	MT_MISC18 // rocket
	MT_MISC19 // box of rockets
	MT_MISC20 // cell
	MT_MISC21 // cell pack
	MT_MISC22 // shells
	MT_MISC23 // box of shells
	MT_MISC24 // backpack
	MT_MISC25 // BFG9000
	MT_CHAINGUN
	MT_MISC26 // chainsaw
	MT_MISC27 // rocket launcher
	MT_MISC28 // plasma rifle
	MT_SHOTGUN
	MT_SUPERSHOTGUN
	MT_COLUMN
	MT_DEADTORSO
	MT_GIBS
	MT_MISC_CANDLESTIK
	MT_MISC_CANDELABRA

	// Map decorations — every static thing the voxel pack has a model for
	// (see game_design.txt section 20). Purely visual: no AI, no pickup.
	MT_TALLGREENCOL
	MT_SHORTGREENCOL
	MT_TALLREDCOL
	MT_SHORTREDCOL
	MT_SKULLCOL
	MT_HEARTCOL
	MT_STALAGMITE
	MT_TECHPILLAR
	MT_EVILEYE
	MT_FLOATSKULL
	MT_TORCHTREE
	MT_BIGTREE
	MT_TALLTECHLAMP
	MT_SHORTTECHLAMP
	MT_BLUETORCH
	MT_GREENTORCH
	MT_REDTORCH
	MT_SHORTBLUETORCH
	MT_SHORTGREENTORCH
	MT_SHORTREDTORCH
	MT_BURNINGBARREL
	MT_MISC_BLOODYTWITCH
	MT_MISC_MEAT2
	MT_MISC_MEAT3
	MT_MISC_MEAT4
	MT_MISC_MEAT5
	MT_MISC_BLOODYTWITCH_NB
	MT_MISC_MEAT2_NB
	MT_MISC_MEAT3_NB
	MT_MISC_MEAT4_NB
	MT_MISC_MEAT5_NB
	MT_MISC_STAKE
	MT_MISC_LIVESTICK
	MT_MISC_HEADSTICK
	MT_MISC_HEADSONSTICK
	MT_MISC_HEADCANDLES
	MT_HANGNOGUTS
	MT_HANGBNOBRAIN
	MT_HANGTLOOKINGDOWN
	MT_HANGTSKULL
	MT_HANGTLOOKINGUP
	MT_HANGTNOBRAIN
	MT_POOLBLOOD1
	MT_POOLBLOOD2
	MT_POOLBRAINS

	// Code-spawned (DoomedNum -1): missiles, puffs, blood, teleport fog.
	MT_TROOPSHOT
	MT_HEADSHOT
	MT_BRUISERSHOT
	MT_PUFF
	MT_BLOOD
	MT_TFOG

	MT_TELEPORTMAN // doomednum 14 — teleport landing spot

	NUMMOBJTYPES
)

// mono is a monster's common flag set.
const monsterFlags = MF_SOLID | MF_SHOOTABLE | MF_COUNTKILL

var mobjInfo = [NUMMOBJTYPES]MobjInfo{
	MT_PLAYER: {
		Name: "player", DoomedNum: -1, SpawnState: S_PLAY, SpawnHealth: 100,
		PainChance: 255, Speed: 0, Radius: 16, Height: 56, Mass: 100,
		Flags: MF_SOLID | MF_SHOOTABLE | MF_DROPOFF | MF_PICKUP,
	},
	MT_POSSESSED: {
		Name: "zombieman", DoomedNum: 3004, SpawnState: S_POSS_STND, SpawnHealth: 20,
		SeeState: S_POSS_RUN1, MissileState: S_POSS_ATK1, PainState: S_POSS_PAIN,
		DeathState: S_POSS_DIE1, XDeathState: S_POSS_XDIE1,
		ReactionTime: 8, PainChance: 200, Speed: 8, Radius: 20, Height: 56, Mass: 100,
		SeeSound: "DSPOSIT1", PainSound: "DSPOPAIN", DeathSound: "DSPODTH1", ActiveSound: "DSPOSACT",
		Flags: monsterFlags,
	},
	MT_SHOTGUY: {
		Name: "shotgunguy", DoomedNum: 9, SpawnState: S_SPOS_STND, SpawnHealth: 30,
		SeeState: S_SPOS_RUN1, MissileState: S_SPOS_ATK1, PainState: S_SPOS_PAIN,
		DeathState: S_SPOS_DIE1, XDeathState: S_SPOS_XDIE1,
		ReactionTime: 8, PainChance: 170, Speed: 8, Radius: 20, Height: 56, Mass: 100,
		SeeSound: "DSPOSIT2", PainSound: "DSPOPAIN", DeathSound: "DSPODTH2", ActiveSound: "DSPOSACT",
		Flags: monsterFlags,
	},
	MT_CHAINGUY: {
		Name: "chaingunner", DoomedNum: 65, SpawnState: S_CPOS_STND, SpawnHealth: 70,
		SeeState: S_CPOS_RUN1, MissileState: S_CPOS_ATK1, PainState: S_CPOS_PAIN, DeathState: S_CPOS_DIE1,
		ReactionTime: 8, PainChance: 170, Speed: 8, Radius: 20, Height: 56, Mass: 100,
		SeeSound: "DSPOSIT2", PainSound: "DSPOPAIN", DeathSound: "DSPODTH2", ActiveSound: "DSPOSACT",
		Flags: monsterFlags,
	},
	MT_TROOP: {
		Name: "imp", DoomedNum: 3001, SpawnState: S_TROO_STND, SpawnHealth: 60,
		SeeState: S_TROO_RUN1, MeleeState: S_TROO_ATK1, MissileState: S_TROO_ATK1, PainState: S_TROO_PAIN,
		DeathState: S_TROO_DIE1, XDeathState: S_TROO_XDIE1,
		ReactionTime: 8, PainChance: 200, Speed: 8, Radius: 20, Height: 56, Mass: 100,
		SeeSound: "DSBGSIT1", PainSound: "DSPOPAIN", DeathSound: "DSBGDTH1", ActiveSound: "DSBGACT",
		Flags: monsterFlags,
	},
	MT_SERGEANT: {
		Name: "demon", DoomedNum: 3002, SpawnState: S_SARG_STND, SpawnHealth: 150,
		SeeState: S_SARG_RUN1, MeleeState: S_SARG_ATK1, PainState: S_SARG_PAIN, DeathState: S_SARG_DIE1,
		ReactionTime: 8, PainChance: 180, Speed: 10, Radius: 30, Height: 56, Mass: 400,
		SeeSound: "DSSGTSIT", AttackSound: "DSSGTATK", PainSound: "DSDMPAIN", DeathSound: "DSSGTDTH", ActiveSound: "DSDMACT",
		Flags: monsterFlags,
	},
	MT_SHADOWS: {
		Name: "spectre", DoomedNum: 58, SpawnState: S_SARG_STND, SpawnHealth: 150,
		SeeState: S_SARG_RUN1, MeleeState: S_SARG_ATK1, PainState: S_SARG_PAIN, DeathState: S_SARG_DIE1,
		ReactionTime: 8, PainChance: 180, Speed: 10, Radius: 30, Height: 56, Mass: 400,
		SeeSound: "DSSGTSIT", AttackSound: "DSSGTATK", PainSound: "DSDMPAIN", DeathSound: "DSSGTDTH", ActiveSound: "DSDMACT",
		Flags: monsterFlags | MF_SHADOW,
	},
	MT_HEAD: {
		Name: "cacodemon", DoomedNum: 3005, SpawnState: S_HEAD_STND, SpawnHealth: 400,
		SeeState: S_HEAD_RUN1, MissileState: S_HEAD_ATK1, PainState: S_HEAD_PAIN, DeathState: S_HEAD_DIE1,
		ReactionTime: 8, PainChance: 128, Speed: 8, Radius: 31, Height: 56, Mass: 400,
		SeeSound: "DSCACSIT", PainSound: "DSDMPAIN", DeathSound: "DSCACDTH", ActiveSound: "DSDMACT",
		Flags: monsterFlags | MF_FLOAT | MF_NOGRAVITY,
	},
	MT_SKULL: {
		Name: "lostsoul", DoomedNum: 3006, SpawnState: S_SKULL_STND, SpawnHealth: 100,
		SeeState: S_SKULL_RUN1, MissileState: S_SKULL_ATK1, PainState: S_SKULL_PAIN, DeathState: S_SKULL_DIE1,
		ReactionTime: 8, PainChance: 256, Speed: 8, Radius: 16, Height: 56, Mass: 50, Damage: 3,
		AttackSound: "DSSKLATK", PainSound: "DSDMPAIN", DeathSound: "DSFIRXPL", ActiveSound: "DSDMACT",
		Flags: MF_SOLID | MF_SHOOTABLE | MF_FLOAT | MF_NOGRAVITY,
	},
	MT_BRUISER: {
		Name: "baron", DoomedNum: 3003, SpawnState: S_BOSS_STND, SpawnHealth: 1000,
		SeeState: S_BOSS_RUN1, MeleeState: S_BOSS_ATK1, MissileState: S_BOSS_ATK1, PainState: S_BOSS_PAIN, DeathState: S_BOSS_DIE1,
		ReactionTime: 8, PainChance: 50, Speed: 8, Radius: 24, Height: 64, Mass: 1000,
		SeeSound: "DSBRSSIT", PainSound: "DSDMPAIN", DeathSound: "DSBRSDTH", ActiveSound: "DSDMACT",
		Flags: monsterFlags,
	},
	MT_KNIGHT: {
		Name: "knight", DoomedNum: 69, SpawnState: S_BOS2_STND, SpawnHealth: 500,
		SeeState: S_BOS2_RUN1, MeleeState: S_BOS2_ATK1, MissileState: S_BOS2_ATK1, PainState: S_BOS2_PAIN, DeathState: S_BOS2_DIE1,
		ReactionTime: 8, PainChance: 50, Speed: 8, Radius: 24, Height: 64, Mass: 1000,
		SeeSound: "DSKNTSIT", PainSound: "DSDMPAIN", DeathSound: "DSKNTDTH", ActiveSound: "DSDMACT",
		Flags: monsterFlags,
	},
	MT_BARREL: {
		Name: "barrel", DoomedNum: 2035, SpawnState: S_BAR1, SpawnHealth: 20,
		DeathState: S_BEXP1, ReactionTime: 8, Radius: 10, Height: 42, Mass: 100,
		DeathSound: "DSBAREXP",
		Flags:      MF_SOLID | MF_SHOOTABLE | MF_NOBLOOD,
	},

	MT_TROOPSHOT: {
		Name: "impshot", DoomedNum: -1, SpawnState: S_TBALL1, SpawnHealth: 1000,
		DeathState: S_TBALLX1, Speed: 10, Radius: 6, Height: 8, Mass: 100, Damage: 3,
		SeeSound: "DSFIRSHT", DeathSound: "DSFIRXPL",
		Flags: MF_NOBLOCKMAP | MF_MISSILE | MF_DROPOFF | MF_NOGRAVITY,
	},
	MT_HEADSHOT: {
		Name: "cacoshot", DoomedNum: -1, SpawnState: S_RBALL1, SpawnHealth: 1000,
		DeathState: S_RBALLX1, Speed: 10, Radius: 6, Height: 8, Mass: 100, Damage: 5,
		SeeSound: "DSFIRSHT", DeathSound: "DSFIRXPL",
		Flags: MF_NOBLOCKMAP | MF_MISSILE | MF_DROPOFF | MF_NOGRAVITY,
	},
	MT_BRUISERSHOT: {
		Name: "baronshot", DoomedNum: -1, SpawnState: S_BRBALL1, SpawnHealth: 1000,
		DeathState: S_BRBALLX1, Speed: 15, Radius: 6, Height: 8, Mass: 100, Damage: 8,
		SeeSound: "DSFIRSHT", DeathSound: "DSFIRXPL",
		Flags: MF_NOBLOCKMAP | MF_MISSILE | MF_DROPOFF | MF_NOGRAVITY,
	},
	MT_PUFF: {
		Name: "puff", DoomedNum: -1, SpawnState: S_PUFF1, SpawnHealth: 1000,
		Radius: 20, Height: 16, Mass: 100,
		Flags: MF_NOBLOCKMAP | MF_NOGRAVITY,
	},
	MT_BLOOD: {
		Name: "blood", DoomedNum: -1, SpawnState: S_BLOOD1, SpawnHealth: 1000,
		Radius: 20, Height: 16, Mass: 100,
		Flags: MF_NOBLOCKMAP | MF_NOGRAVITY,
	},
	MT_TFOG: {
		Name: "tfog", DoomedNum: -1, SpawnState: S_TFOG1, SpawnHealth: 1000,
		Radius: 20, Height: 16, Mass: 100,
		Flags: MF_NOBLOCKMAP | MF_NOGRAVITY,
	},
	MT_TELEPORTMAN: {
		Name: "teleportdest", DoomedNum: 14, SpawnState: S_NULL, SpawnHealth: 1000,
		Radius: 20, Height: 16, Mass: 100,
		Flags: MF_NOBLOCKMAP | MF_NOSECTOR | MF_NOGRAVITY,
	},

	// Pickups. MF_SPECIAL = can be picked up; MF_COUNTITEM = counts as an item.
	MT_MISC0:        item(2018, S_ARM1, 0),
	MT_MISC1:        item(2019, S_ARM2, 0),
	MT_MISC2:        item(2014, S_BON1, MF_COUNTITEM),
	MT_MISC3:        item(2015, S_BON2, MF_COUNTITEM),
	MT_MISC4:        item(5, S_BKEY, 0),
	MT_MISC5:        item(13, S_RKEY, 0),
	MT_MISC6:        item(6, S_YKEY, 0),
	MT_MISC7:        item(39, S_YSKULL, 0),
	MT_MISC8:        item(38, S_RSKULL, 0),
	MT_MISC9:        item(40, S_BSKULL, 0),
	MT_MISC10:       item(2011, S_STIM, 0),
	MT_MISC11:       item(2012, S_MEDI, 0),
	MT_MISC12:       item(2013, S_SOUL, MF_COUNTITEM),
	MT_INV:          item(2022, S_PINV, MF_COUNTITEM),
	MT_MISC13:       item(2023, S_PSTR, MF_COUNTITEM),
	MT_INS:          item(2024, S_PINS, MF_COUNTITEM),
	MT_MISC14:       item(2025, S_SUIT, 0),
	MT_MISC15:       item(2026, S_PMAP, MF_COUNTITEM),
	MT_MISC16:       item(2045, S_PVIS, MF_COUNTITEM),
	MT_MEGA:         item(83, S_MEGA, MF_COUNTITEM),
	MT_CLIP:         item(2007, S_CLIP, 0),
	MT_MISC17:       item(2048, S_AMMO, 0),
	MT_MISC18:       item(2010, S_ROCK, 0),
	MT_MISC19:       item(2046, S_BROK, 0),
	MT_MISC20:       item(2047, S_CELL, 0),
	MT_MISC21:       item(17, S_CELP, 0),
	MT_MISC22:       item(2008, S_SHEL, 0),
	MT_MISC23:       item(2049, S_SBOX, 0),
	MT_MISC24:       item(8, S_BPAK, 0),
	MT_MISC25:       item(2006, S_BFUG, 0),
	MT_CHAINGUN:     item(2002, S_MGUN, 0),
	MT_MISC26:       item(2005, S_CSAW, 0),
	MT_MISC27:       item(2003, S_LAUN, 0),
	MT_MISC28:       item(2004, S_PLAS, 0),
	MT_SHOTGUN:      item(2001, S_SHOT, 0),
	MT_SUPERSHOTGUN: item(82, S_SHOT2, 0),

	// Decorations.
	MT_COLUMN:          deco(2028, S_COLU, 16, 48, MF_SOLID),
	MT_DEADTORSO:       deco(10, S_DEADTORSO, 20, 16, 0),
	MT_GIBS:            deco(24, S_GIBS, 20, 16, 0),
	MT_MISC_CANDLESTIK: deco(34, S_CANDLESTIK, 20, 14, 0),
	MT_MISC_CANDELABRA: deco(35, S_CANDELABRA, 16, 60, MF_SOLID),

	// Solid standing decorations.
	MT_TALLGREENCOL:    deco(30, S_TALLGRNCOL, 16, 52, MF_SOLID),
	MT_SHORTGREENCOL:   deco(31, S_SHRTGRNCOL, 16, 40, MF_SOLID),
	MT_TALLREDCOL:      deco(32, S_TALLREDCOL, 16, 52, MF_SOLID),
	MT_SHORTREDCOL:     deco(33, S_SHRTREDCOL, 16, 40, MF_SOLID),
	MT_SKULLCOL:        deco(37, S_SKULLCOL, 16, 40, MF_SOLID),
	MT_HEARTCOL:        deco(36, S_HEARTCOL, 16, 40, MF_SOLID),
	MT_STALAGMITE:      deco(47, S_STALAGMITE, 16, 40, MF_SOLID),
	MT_TECHPILLAR:      deco(48, S_TECHPILLAR, 16, 128, MF_SOLID),
	MT_EVILEYE:         deco(41, S_EVILEYE, 16, 54, MF_SOLID),
	MT_FLOATSKULL:      deco(42, S_FLOATSKULL, 16, 26, MF_SOLID),
	MT_TORCHTREE:       deco(43, S_TORCHTREE, 16, 56, MF_SOLID),
	MT_BIGTREE:         deco(54, S_BIGTREE, 32, 108, MF_SOLID),
	MT_TALLTECHLAMP:    deco(85, S_TALLTECHLAMP1, 16, 80, MF_SOLID),
	MT_SHORTTECHLAMP:   deco(86, S_SHRTTECHLAMP1, 16, 60, MF_SOLID),
	MT_BLUETORCH:       deco(44, S_BTORCH1, 16, 68, MF_SOLID),
	MT_GREENTORCH:      deco(45, S_GTORCH1, 16, 68, MF_SOLID),
	MT_REDTORCH:        deco(46, S_RTORCH1, 16, 68, MF_SOLID),
	MT_SHORTBLUETORCH:  deco(55, S_BTORCHSHRT1, 16, 37, MF_SOLID),
	MT_SHORTGREENTORCH: deco(56, S_GTORCHSHRT1, 16, 37, MF_SOLID),
	MT_SHORTREDTORCH:   deco(57, S_RTORCHSHRT1, 16, 37, MF_SOLID),
	MT_BURNINGBARREL:   deco(70, S_BURNBARREL1, 16, 32, MF_SOLID),

	// Gore / gibs on the ground — non-solid.
	MT_MISC_STAKE:        deco(25, S_DEADSTICK, 16, 52, MF_SOLID),
	MT_MISC_LIVESTICK:    deco(26, S_LIVESTICK1, 16, 52, MF_SOLID),
	MT_MISC_HEADSTICK:    deco(27, S_HEAD_ON_STICK, 16, 52, MF_SOLID),
	MT_MISC_HEADSONSTICK: deco(28, S_HEADS_ON_STICK, 16, 52, MF_SOLID),
	MT_MISC_HEADCANDLES:  deco(29, S_HEADCANDLES1, 16, 42, MF_SOLID),
	MT_POOLBLOOD1:        deco(79, S_POOLBLOOD1, 20, 16, 0),
	MT_POOLBLOOD2:        deco(80, S_POOLBLOOD2, 20, 16, 0),
	MT_POOLBRAINS:        deco(81, S_POOLBRAINS, 20, 16, 0),

	// Hanging bodies — spawn at the ceiling, no gravity. The "block" (49-53)
	// and "no block" (59-63) doomednums point at the same art.
	MT_MISC_BLOODYTWITCH:    hang(49, S_BLOODYTWITCH1, 68, MF_SOLID),
	MT_MISC_MEAT2:           hang(50, S_MEAT2, 84, MF_SOLID),
	MT_MISC_MEAT3:           hang(51, S_MEAT3, 84, MF_SOLID),
	MT_MISC_MEAT4:           hang(52, S_MEAT4, 68, MF_SOLID),
	MT_MISC_MEAT5:           hang(53, S_MEAT5, 52, MF_SOLID),
	MT_MISC_BLOODYTWITCH_NB: hang(63, S_BLOODYTWITCH1, 68, 0),
	MT_MISC_MEAT2_NB:        hang(59, S_MEAT2, 84, 0),
	MT_MISC_MEAT3_NB:        hang(61, S_MEAT3, 52, 0),
	MT_MISC_MEAT4_NB:        hang(60, S_MEAT4, 68, 0),
	MT_MISC_MEAT5_NB:        hang(62, S_MEAT5, 52, 0),
	MT_HANGNOGUTS:           hang(73, S_HANGNOGUTS, 88, MF_SOLID),
	MT_HANGBNOBRAIN:         hang(74, S_HANGBNOBRAIN, 88, MF_SOLID),
	MT_HANGTLOOKINGDOWN:     hang(75, S_HANGTLOOKDN, 64, MF_SOLID),
	MT_HANGTSKULL:           hang(76, S_HANGTSKULL, 64, MF_SOLID),
	MT_HANGTLOOKINGUP:       hang(77, S_HANGTLOOKUP, 64, MF_SOLID),
	MT_HANGTNOBRAIN:         hang(78, S_HANGTNOBRAIN, 64, MF_SOLID),
}

// item builds a MobjInfo for a pickup: not solid, not counted as a kill,
// touched via MF_SPECIAL.
func item(doomednum int, spawn stateNum, extra int) MobjInfo {
	return MobjInfo{
		DoomedNum: doomednum, SpawnState: spawn, SpawnHealth: 1000,
		Radius: 20, Height: 16, Mass: 100,
		Flags: MF_SPECIAL | extra,
	}
}

// deco builds a MobjInfo for a static decoration (solid ones pass MF_SOLID
// in extra so they're linked into the blockmap and block movement).
func deco(doomednum int, spawn stateNum, radius, height float64, extra int) MobjInfo {
	return MobjInfo{
		DoomedNum: doomednum, SpawnState: spawn, SpawnHealth: 1000,
		Radius: radius, Height: height, Mass: 100,
		Flags: extra,
	}
}

// hang builds a MobjInfo for a hanging body: it spawns at the ceiling and
// never falls (id's MF_SPAWNCEILING | MF_NOGRAVITY). extra carries MF_SOLID
// for the "block" doomednum variants.
func hang(doomednum int, spawn stateNum, height float64, extra int) MobjInfo {
	return MobjInfo{
		DoomedNum: doomednum, SpawnState: spawn, SpawnHealth: 1000,
		Radius: 16, Height: height, Mass: 100,
		Flags: MF_SPAWNCEILING | MF_NOGRAVITY | extra,
	}
}

// doomedNumToType resolves a THINGS type number to a mobjType. Built once.
var doomedNumToType = func() map[int]mobjType {
	m := make(map[int]mobjType, NUMMOBJTYPES)
	for t := mobjType(0); t < NUMMOBJTYPES; t++ {
		if dn := mobjInfo[t].DoomedNum; dn > 0 {
			m[dn] = t
		}
	}
	return m
}()
