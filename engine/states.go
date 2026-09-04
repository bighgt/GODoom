package engine

// Ported from id Software's info.c: the finite-state machine every map
// object (Mobj) animates through. A State is one animation frame — a sprite
// + frame letter to show, how many 35Hz tics to hold it, an optional
// action function run on entry, and the next state to advance to.
//
// This file currently carries the *spawn/idle* states for the things
// Phase 1 spawns and renders; the see/pain/attack/death chains are added in
// the phases that implement those behaviours (see the plan), so each chain
// is transcribed and tested next to the code that drives it rather than as
// one large unverified block.

// mobjAction is a state's on-entry action (id's actionf_t). nil = none.
type mobjAction func(g *Game, mo *Mobj)

// State is one entry of id's states[] table.
type State struct {
	Sprite     spriteNum
	Frame      int // 0-based frame index (0 = 'A')
	Tics       int // 35Hz tics to hold; -1 = forever
	Action     mobjAction
	Next       stateNum
	fullbright bool // drawn at full brightness regardless of sector light
}

// stateNum indexes states. S_NULL (0) is the terminal "remove me" state
// for missiles and the like.
type stateNum int

// spriteNum indexes spriteNames — the 4-letter sprite prefixes, in the
// same order R_InitSprites expects (only the ones referenced below).
type spriteNum int

const (
	SPR_TROO spriteNum = iota
	SPR_SHTG
	SPR_PUNG
	SPR_PISG
	SPR_PISF
	SPR_SHTF
	SPR_SHT2
	SPR_CHGG
	SPR_CHGF
	SPR_MISG
	SPR_MISF
	SPR_SAWG
	SPR_PLSG
	SPR_PLSF
	SPR_BFGG
	SPR_BFGF
	SPR_BLUD
	SPR_PUFF
	SPR_BAL1
	SPR_BAL2
	SPR_BAL7
	SPR_PLSS
	SPR_PLSE
	SPR_MISL
	SPR_BFS1
	SPR_BFE1
	SPR_BFE2
	SPR_TFOG
	SPR_IFOG
	SPR_PLAY
	SPR_POSS
	SPR_SPOS
	SPR_SARG
	SPR_HEAD
	SPR_BOSS
	SPR_BOS2
	SPR_SKUL
	SPR_FATB
	SPR_FBXP
	SPR_SKEL
	SPR_MANF
	SPR_FATT
	SPR_CPOS
	SPR_TRCR
	SPR_APLS
	SPR_APBX
	SPR_ARM1
	SPR_ARM2
	SPR_BAR1
	SPR_BEXP
	SPR_BON1
	SPR_BON2
	SPR_BKEY
	SPR_RKEY
	SPR_YKEY
	SPR_BSKU
	SPR_RSKU
	SPR_YSKU
	SPR_STIM
	SPR_MEDI
	SPR_SOUL
	SPR_PINV
	SPR_PSTR
	SPR_PINS
	SPR_MEGA
	SPR_SUIT
	SPR_PMAP
	SPR_PVIS
	SPR_CLIP
	SPR_AMMO
	SPR_ROCK
	SPR_BROK
	SPR_CELL
	SPR_CELP
	SPR_SHEL
	SPR_SBOX
	SPR_BPAK
	SPR_BFUG
	SPR_MGUN
	SPR_CSAW
	SPR_LAUN
	SPR_PLAS
	SPR_SHOT
	SPR_SGN2
	SPR_COLU
	SPR_POL2
	SPR_POL5
	SPR_GOR2
	SPR_CAND
	SPR_CBRA

	// Decoration sprites (columns, trees, torches, hanging bodies, gore,
	// lamps) — added so every static map decoration spawns and can be drawn
	// as a voxel model (see game_design.txt section 20). The order here only
	// has to stay stable; spriteNames is keyed.
	SPR_COL1
	SPR_COL2
	SPR_COL3
	SPR_COL4
	SPR_COL5
	SPR_COL6
	SPR_TRE1
	SPR_TRE2
	SPR_SMIT
	SPR_ELEC
	SPR_CEYE
	SPR_FSKU
	SPR_GOR1
	SPR_GOR3
	SPR_GOR4
	SPR_GOR5
	SPR_POL1
	SPR_POL3
	SPR_POL4
	SPR_POL6
	SPR_TLMP
	SPR_TLP2
	SPR_TBLU
	SPR_TGRN
	SPR_TRED
	SPR_SMBT
	SPR_SMGT
	SPR_SMRT
	SPR_HDB1
	SPR_HDB2
	SPR_HDB3
	SPR_HDB4
	SPR_HDB5
	SPR_HDB6
	SPR_POB1
	SPR_POB2
	SPR_BRS1
	SPR_FCAN

	SPR_NumSprites
)

// spriteNames is the WAD 4-letter prefix for each spriteNum, aligned with
// the const block above. raster.SpriteIndex maps these to the WAD's
// S_START..S_END lumps.
var spriteNames = [...]string{
	SPR_TROO: "TROO", SPR_SHTG: "SHTG", SPR_PUNG: "PUNG", SPR_PISG: "PISG",
	SPR_PISF: "PISF", SPR_SHTF: "SHTF", SPR_SHT2: "SHT2", SPR_CHGG: "CHGG",
	SPR_CHGF: "CHGF", SPR_MISG: "MISG", SPR_MISF: "MISF", SPR_SAWG: "SAWG",
	SPR_PLSG: "PLSG", SPR_PLSF: "PLSF", SPR_BFGG: "BFGG", SPR_BFGF: "BFGF",
	SPR_BLUD: "BLUD", SPR_PUFF: "PUFF", SPR_BAL1: "BAL1", SPR_BAL2: "BAL2", SPR_BAL7: "BAL7",
	SPR_PLSS: "PLSS", SPR_PLSE: "PLSE", SPR_MISL: "MISL", SPR_BFS1: "BFS1",
	SPR_BFE1: "BFE1", SPR_BFE2: "BFE2", SPR_TFOG: "TFOG", SPR_IFOG: "IFOG",
	SPR_PLAY: "PLAY", SPR_POSS: "POSS", SPR_SPOS: "SPOS", SPR_SARG: "SARG",
	SPR_HEAD: "HEAD", SPR_BOSS: "BOSS", SPR_BOS2: "BOS2", SPR_SKUL: "SKUL",
	SPR_FATB: "FATB", SPR_FBXP: "FBXP", SPR_SKEL: "SKEL", SPR_MANF: "MANF",
	SPR_FATT: "FATT", SPR_CPOS: "CPOS", SPR_TRCR: "TRCR", SPR_APLS: "APLS",
	SPR_APBX: "APBX", SPR_ARM1: "ARM1", SPR_ARM2: "ARM2", SPR_BAR1: "BAR1",
	SPR_BEXP: "BEXP", SPR_BON1: "BON1", SPR_BON2: "BON2", SPR_BKEY: "BKEY",
	SPR_RKEY: "RKEY", SPR_YKEY: "YKEY", SPR_BSKU: "BSKU", SPR_RSKU: "RSKU",
	SPR_YSKU: "YSKU", SPR_STIM: "STIM", SPR_MEDI: "MEDI", SPR_SOUL: "SOUL",
	SPR_PINV: "PINV", SPR_PSTR: "PSTR", SPR_PINS: "PINS", SPR_MEGA: "MEGA",
	SPR_SUIT: "SUIT", SPR_PMAP: "PMAP", SPR_PVIS: "PVIS", SPR_CLIP: "CLIP",
	SPR_AMMO: "AMMO", SPR_ROCK: "ROCK", SPR_BROK: "BROK", SPR_CELL: "CELL",
	SPR_CELP: "CELP", SPR_SHEL: "SHEL", SPR_SBOX: "SBOX", SPR_BPAK: "BPAK",
	SPR_BFUG: "BFUG", SPR_MGUN: "MGUN", SPR_CSAW: "CSAW", SPR_LAUN: "LAUN",
	SPR_PLAS: "PLAS", SPR_SHOT: "SHOT", SPR_SGN2: "SGN2", SPR_COLU: "COLU",
	SPR_POL2: "POL2", SPR_POL5: "POL5", SPR_GOR2: "GOR2", SPR_CAND: "CAND",
	SPR_CBRA: "CBRA",

	SPR_COL1: "COL1", SPR_COL2: "COL2", SPR_COL3: "COL3", SPR_COL4: "COL4",
	SPR_COL5: "COL5", SPR_COL6: "COL6", SPR_TRE1: "TRE1", SPR_TRE2: "TRE2",
	SPR_SMIT: "SMIT", SPR_ELEC: "ELEC", SPR_CEYE: "CEYE", SPR_FSKU: "FSKU",
	SPR_GOR1: "GOR1", SPR_GOR3: "GOR3", SPR_GOR4: "GOR4", SPR_GOR5: "GOR5",
	SPR_POL1: "POL1", SPR_POL3: "POL3", SPR_POL4: "POL4", SPR_POL6: "POL6",
	SPR_TLMP: "TLMP", SPR_TLP2: "TLP2", SPR_TBLU: "TBLU", SPR_TGRN: "TGRN",
	SPR_TRED: "TRED", SPR_SMBT: "SMBT", SPR_SMGT: "SMGT", SPR_SMRT: "SMRT",
	SPR_HDB1: "HDB1", SPR_HDB2: "HDB2", SPR_HDB3: "HDB3", SPR_HDB4: "HDB4",
	SPR_HDB5: "HDB5", SPR_HDB6: "HDB6", SPR_POB1: "POB1", SPR_POB2: "POB2",
	SPR_BRS1: "BRS1", SPR_FCAN: "FCAN",
}

// State index constants. Kept flat and hand-numbered so a state's Next can
// forward-reference. Grouped by thing.
const (
	S_NULL stateNum = iota

	// Player (idle only for now).
	S_PLAY

	// Zombieman.
	S_POSS_STND
	S_POSS_STND2
	S_POSS_RUN1
	S_POSS_RUN2
	S_POSS_RUN3
	S_POSS_RUN4
	S_POSS_RUN5
	S_POSS_RUN6
	S_POSS_RUN7
	S_POSS_RUN8

	// Shotgun guy.
	S_SPOS_STND
	S_SPOS_STND2
	S_SPOS_RUN1
	S_SPOS_RUN2
	S_SPOS_RUN3
	S_SPOS_RUN4
	S_SPOS_RUN5
	S_SPOS_RUN6
	S_SPOS_RUN7
	S_SPOS_RUN8

	// Chaingunner.
	S_CPOS_STND
	S_CPOS_STND2
	S_CPOS_RUN1
	S_CPOS_RUN2
	S_CPOS_RUN3
	S_CPOS_RUN4
	S_CPOS_RUN5
	S_CPOS_RUN6
	S_CPOS_RUN7
	S_CPOS_RUN8

	// Imp.
	S_TROO_STND
	S_TROO_STND2
	S_TROO_RUN1
	S_TROO_RUN2
	S_TROO_RUN3
	S_TROO_RUN4
	S_TROO_RUN5
	S_TROO_RUN6
	S_TROO_RUN7
	S_TROO_RUN8

	// Demon / Spectre.
	S_SARG_STND
	S_SARG_STND2
	S_SARG_RUN1
	S_SARG_RUN2
	S_SARG_RUN3
	S_SARG_RUN4
	S_SARG_RUN5
	S_SARG_RUN6
	S_SARG_RUN7
	S_SARG_RUN8

	// Cacodemon.
	S_HEAD_STND
	S_HEAD_RUN1

	// Lost soul.
	S_SKULL_STND
	S_SKULL_STND2
	S_SKULL_RUN1
	S_SKULL_RUN2

	// Baron of Hell.
	S_BOSS_STND
	S_BOSS_STND2
	S_BOSS_RUN1
	S_BOSS_RUN2
	S_BOSS_RUN3
	S_BOSS_RUN4
	S_BOSS_RUN5
	S_BOSS_RUN6
	S_BOSS_RUN7
	S_BOSS_RUN8

	// Hell knight.
	S_BOS2_STND
	S_BOS2_STND2
	S_BOS2_RUN1
	S_BOS2_RUN2
	S_BOS2_RUN3
	S_BOS2_RUN4
	S_BOS2_RUN5
	S_BOS2_RUN6
	S_BOS2_RUN7
	S_BOS2_RUN8

	// --- Phase 3: attack / pain / death / xdeath / raise chains ---

	S_POSS_ATK1
	S_POSS_ATK2
	S_POSS_ATK3
	S_POSS_PAIN
	S_POSS_PAIN2
	S_POSS_DIE1
	S_POSS_DIE2
	S_POSS_DIE3
	S_POSS_DIE4
	S_POSS_DIE5
	S_POSS_XDIE1
	S_POSS_XDIE2
	S_POSS_XDIE3
	S_POSS_XDIE4
	S_POSS_XDIE5
	S_POSS_XDIE6
	S_POSS_XDIE7
	S_POSS_XDIE8
	S_POSS_XDIE9

	S_SPOS_ATK1
	S_SPOS_ATK2
	S_SPOS_ATK3
	S_SPOS_PAIN
	S_SPOS_PAIN2
	S_SPOS_DIE1
	S_SPOS_DIE2
	S_SPOS_DIE3
	S_SPOS_DIE4
	S_SPOS_DIE5
	S_SPOS_XDIE1
	S_SPOS_XDIE2
	S_SPOS_XDIE3
	S_SPOS_XDIE4
	S_SPOS_XDIE5
	S_SPOS_XDIE6
	S_SPOS_XDIE7
	S_SPOS_XDIE8
	S_SPOS_XDIE9

	S_CPOS_ATK1
	S_CPOS_ATK2
	S_CPOS_ATK3
	S_CPOS_ATK4
	S_CPOS_PAIN
	S_CPOS_PAIN2
	S_CPOS_DIE1
	S_CPOS_DIE2
	S_CPOS_DIE3
	S_CPOS_DIE4
	S_CPOS_DIE5
	S_CPOS_DIE6
	S_CPOS_DIE7

	S_TROO_ATK1
	S_TROO_ATK2
	S_TROO_ATK3
	S_TROO_PAIN
	S_TROO_PAIN2
	S_TROO_DIE1
	S_TROO_DIE2
	S_TROO_DIE3
	S_TROO_DIE4
	S_TROO_DIE5
	S_TROO_XDIE1
	S_TROO_XDIE2
	S_TROO_XDIE3
	S_TROO_XDIE4
	S_TROO_XDIE5
	S_TROO_XDIE6
	S_TROO_XDIE7
	S_TROO_XDIE8

	S_SARG_ATK1
	S_SARG_ATK2
	S_SARG_ATK3
	S_SARG_PAIN
	S_SARG_PAIN2
	S_SARG_DIE1
	S_SARG_DIE2
	S_SARG_DIE3
	S_SARG_DIE4
	S_SARG_DIE5
	S_SARG_DIE6

	S_HEAD_ATK1
	S_HEAD_ATK2
	S_HEAD_ATK3
	S_HEAD_PAIN
	S_HEAD_PAIN2
	S_HEAD_PAIN3
	S_HEAD_DIE1
	S_HEAD_DIE2
	S_HEAD_DIE3
	S_HEAD_DIE4
	S_HEAD_DIE5
	S_HEAD_DIE6

	S_SKULL_ATK1
	S_SKULL_ATK2
	S_SKULL_ATK3
	S_SKULL_ATK4
	S_SKULL_PAIN
	S_SKULL_PAIN2
	S_SKULL_DIE1
	S_SKULL_DIE2
	S_SKULL_DIE3
	S_SKULL_DIE4
	S_SKULL_DIE5
	S_SKULL_DIE6

	S_BOSS_ATK1
	S_BOSS_ATK2
	S_BOSS_ATK3
	S_BOSS_PAIN
	S_BOSS_PAIN2
	S_BOSS_DIE1
	S_BOSS_DIE2
	S_BOSS_DIE3
	S_BOSS_DIE4
	S_BOSS_DIE5
	S_BOSS_DIE6
	S_BOSS_DIE7

	S_BOS2_ATK1
	S_BOS2_ATK2
	S_BOS2_ATK3
	S_BOS2_PAIN
	S_BOS2_PAIN2
	S_BOS2_DIE1
	S_BOS2_DIE2
	S_BOS2_DIE3
	S_BOS2_DIE4
	S_BOS2_DIE5
	S_BOS2_DIE6
	S_BOS2_DIE7

	// Missiles.
	S_TBALL1
	S_TBALL2
	S_TBALLX1
	S_TBALLX2
	S_TBALLX3
	S_RBALL1
	S_RBALL2
	S_RBALLX1
	S_RBALLX2
	S_RBALLX3
	S_BRBALL1
	S_BRBALL2
	S_BRBALLX1
	S_BRBALLX2
	S_BRBALLX3

	// Bullet puff & blood.
	S_PUFF1
	S_PUFF2
	S_PUFF3
	S_PUFF4
	S_BLOOD1
	S_BLOOD2
	S_BLOOD3

	// Teleport fog.
	S_TFOG1
	S_TFOG2
	S_TFOG3
	S_TFOG4
	S_TFOG5
	S_TFOG6
	S_TFOG7
	S_TFOG8
	S_TFOG9
	S_TFOG10

	// Exploding barrel.
	S_BAR1
	S_BAR2
	S_BEXP1
	S_BEXP2
	S_BEXP3
	S_BEXP4
	S_BEXP5

	// Static / simple-cycle pickups & decorations.
	S_ARM1
	S_ARM1A
	S_ARM2
	S_ARM2A
	S_BON1
	S_BON1A
	S_BON1B
	S_BON1C
	S_BON1D
	S_BON1E
	S_BON2
	S_BON2A
	S_BON2B
	S_BON2C
	S_BON2D
	S_BON2E
	S_BKEY
	S_BKEY2
	S_RKEY
	S_RKEY2
	S_YKEY
	S_YKEY2
	S_BSKULL
	S_BSKULL2
	S_RSKULL
	S_RSKULL2
	S_YSKULL
	S_YSKULL2
	S_STIM
	S_MEDI
	S_SOUL
	S_SOUL2
	S_SOUL3
	S_SOUL4
	S_SOUL5
	S_SOUL6
	S_PINV
	S_PINV2
	S_PINV3
	S_PINV4
	S_PSTR
	S_PINS
	S_PINS2
	S_PINS3
	S_PINS4
	S_MEGA
	S_MEGA2
	S_MEGA3
	S_MEGA4
	S_SUIT
	S_PMAP
	S_PMAP2
	S_PMAP3
	S_PMAP4
	S_PMAP5
	S_PMAP6
	S_PVIS
	S_PVIS2
	S_CLIP
	S_AMMO
	S_ROCK
	S_BROK
	S_CELL
	S_CELP
	S_SHEL
	S_SBOX
	S_BPAK
	S_BFUG
	S_MGUN
	S_CSAW
	S_LAUN
	S_PLAS
	S_SHOT
	S_SHOT2
	S_COLU
	S_DEADTORSO
	S_GIBS
	S_CANDLESTIK
	S_CANDELABRA

	// Map decorations (see game_design.txt section 20). Static ones are a
	// single frozen frame; the torches and tech lamps flicker through four
	// frames, the burning barrel through three.
	S_TALLGRNCOL
	S_SHRTGRNCOL
	S_TALLREDCOL
	S_SHRTREDCOL
	S_SKULLCOL
	S_HEARTCOL
	S_STALAGMITE
	S_TECHPILLAR
	S_EVILEYE
	S_FLOATSKULL
	S_TORCHTREE
	S_BIGTREE
	S_HANGNOGUTS
	S_HANGBNOBRAIN
	S_HANGTLOOKDN
	S_HANGTSKULL
	S_HANGTLOOKUP
	S_HANGTNOBRAIN
	S_HEAD_ON_STICK
	S_HEADS_ON_STICK
	S_HEADCANDLES1
	S_HEADCANDLES2
	S_DEADSTICK
	S_LIVESTICK1
	S_LIVESTICK2
	S_MEAT2
	S_MEAT3
	S_MEAT4
	S_MEAT5
	S_BLOODYTWITCH1
	S_BLOODYTWITCH2
	S_BLOODYTWITCH3
	S_POOLBLOOD1
	S_POOLBLOOD2
	S_POOLBRAINS
	S_TALLTECHLAMP1
	S_TALLTECHLAMP2
	S_TALLTECHLAMP3
	S_TALLTECHLAMP4
	S_SHRTTECHLAMP1
	S_SHRTTECHLAMP2
	S_SHRTTECHLAMP3
	S_SHRTTECHLAMP4
	S_BTORCHSHRT1
	S_BTORCHSHRT2
	S_BTORCHSHRT3
	S_BTORCHSHRT4
	S_GTORCHSHRT1
	S_GTORCHSHRT2
	S_GTORCHSHRT3
	S_GTORCHSHRT4
	S_RTORCHSHRT1
	S_RTORCHSHRT2
	S_RTORCHSHRT3
	S_RTORCHSHRT4
	S_BTORCH1
	S_BTORCH2
	S_BTORCH3
	S_BTORCH4
	S_GTORCH1
	S_GTORCH2
	S_GTORCH3
	S_GTORCH4
	S_RTORCH1
	S_RTORCH2
	S_RTORCH3
	S_RTORCH4
	S_BURNBARREL1
	S_BURNBARREL2
	S_BURNBARREL3

	S_NUMSTATES
)

// bright marks a state fullbright (id's frame bit 0x8000).
func bright(s State) State { s.fullbright = true; return s }

// states is id's states[] for the subset above. A -1 Tics on the last
// frame of a loop is replaced by an explicit self-Next here. It's populated
// in init() rather than as a package-var literal because its entries name
// action functions (aLook/aChase/...) whose bodies reach back to states
// via setMobjState — a literal initializer would be a compile-time
// initialization cycle.
var states [S_NUMSTATES]State

func init() {
	states = [S_NUMSTATES]State{
		S_NULL: {SPR_TROO, 0, -1, nil, S_NULL, false},

		S_PLAY: {SPR_PLAY, 0, -1, nil, S_PLAY, false},

		S_POSS_STND:  {SPR_POSS, 0, 10, aLook, S_POSS_STND2, false},
		S_POSS_STND2: {SPR_POSS, 1, 10, aLook, S_POSS_STND, false},
		S_POSS_RUN1:  {SPR_POSS, 0, 4, aChase, S_POSS_RUN2, false},
		S_POSS_RUN2:  {SPR_POSS, 0, 4, aChase, S_POSS_RUN3, false},
		S_POSS_RUN3:  {SPR_POSS, 1, 4, aChase, S_POSS_RUN4, false},
		S_POSS_RUN4:  {SPR_POSS, 1, 4, aChase, S_POSS_RUN5, false},
		S_POSS_RUN5:  {SPR_POSS, 2, 4, aChase, S_POSS_RUN6, false},
		S_POSS_RUN6:  {SPR_POSS, 2, 4, aChase, S_POSS_RUN7, false},
		S_POSS_RUN7:  {SPR_POSS, 3, 4, aChase, S_POSS_RUN8, false},
		S_POSS_RUN8:  {SPR_POSS, 3, 4, aChase, S_POSS_RUN1, false},

		S_SPOS_STND:  {SPR_SPOS, 0, 10, aLook, S_SPOS_STND2, false},
		S_SPOS_STND2: {SPR_SPOS, 1, 10, aLook, S_SPOS_STND, false},
		S_SPOS_RUN1:  {SPR_SPOS, 0, 3, aChase, S_SPOS_RUN2, false},
		S_SPOS_RUN2:  {SPR_SPOS, 0, 3, aChase, S_SPOS_RUN3, false},
		S_SPOS_RUN3:  {SPR_SPOS, 1, 3, aChase, S_SPOS_RUN4, false},
		S_SPOS_RUN4:  {SPR_SPOS, 1, 3, aChase, S_SPOS_RUN5, false},
		S_SPOS_RUN5:  {SPR_SPOS, 2, 3, aChase, S_SPOS_RUN6, false},
		S_SPOS_RUN6:  {SPR_SPOS, 2, 3, aChase, S_SPOS_RUN7, false},
		S_SPOS_RUN7:  {SPR_SPOS, 3, 3, aChase, S_SPOS_RUN8, false},
		S_SPOS_RUN8:  {SPR_SPOS, 3, 3, aChase, S_SPOS_RUN1, false},

		S_CPOS_STND:  {SPR_CPOS, 0, 10, aLook, S_CPOS_STND2, false},
		S_CPOS_STND2: {SPR_CPOS, 1, 10, aLook, S_CPOS_STND, false},
		S_CPOS_RUN1:  {SPR_CPOS, 0, 3, aChase, S_CPOS_RUN2, false},
		S_CPOS_RUN2:  {SPR_CPOS, 0, 3, aChase, S_CPOS_RUN3, false},
		S_CPOS_RUN3:  {SPR_CPOS, 1, 3, aChase, S_CPOS_RUN4, false},
		S_CPOS_RUN4:  {SPR_CPOS, 1, 3, aChase, S_CPOS_RUN5, false},
		S_CPOS_RUN5:  {SPR_CPOS, 2, 3, aChase, S_CPOS_RUN6, false},
		S_CPOS_RUN6:  {SPR_CPOS, 2, 3, aChase, S_CPOS_RUN7, false},
		S_CPOS_RUN7:  {SPR_CPOS, 3, 3, aChase, S_CPOS_RUN8, false},
		S_CPOS_RUN8:  {SPR_CPOS, 3, 3, aChase, S_CPOS_RUN1, false},

		S_TROO_STND:  {SPR_TROO, 0, 10, aLook, S_TROO_STND2, false},
		S_TROO_STND2: {SPR_TROO, 1, 10, aLook, S_TROO_STND, false},
		S_TROO_RUN1:  {SPR_TROO, 0, 3, aChase, S_TROO_RUN2, false},
		S_TROO_RUN2:  {SPR_TROO, 0, 3, aChase, S_TROO_RUN3, false},
		S_TROO_RUN3:  {SPR_TROO, 1, 3, aChase, S_TROO_RUN4, false},
		S_TROO_RUN4:  {SPR_TROO, 1, 3, aChase, S_TROO_RUN5, false},
		S_TROO_RUN5:  {SPR_TROO, 2, 3, aChase, S_TROO_RUN6, false},
		S_TROO_RUN6:  {SPR_TROO, 2, 3, aChase, S_TROO_RUN7, false},
		S_TROO_RUN7:  {SPR_TROO, 3, 3, aChase, S_TROO_RUN8, false},
		S_TROO_RUN8:  {SPR_TROO, 3, 3, aChase, S_TROO_RUN1, false},

		S_SARG_STND:  {SPR_SARG, 0, 10, aLook, S_SARG_STND2, false},
		S_SARG_STND2: {SPR_SARG, 1, 10, aLook, S_SARG_STND, false},
		S_SARG_RUN1:  {SPR_SARG, 0, 2, aChase, S_SARG_RUN2, false},
		S_SARG_RUN2:  {SPR_SARG, 0, 2, aChase, S_SARG_RUN3, false},
		S_SARG_RUN3:  {SPR_SARG, 1, 2, aChase, S_SARG_RUN4, false},
		S_SARG_RUN4:  {SPR_SARG, 1, 2, aChase, S_SARG_RUN5, false},
		S_SARG_RUN5:  {SPR_SARG, 2, 2, aChase, S_SARG_RUN6, false},
		S_SARG_RUN6:  {SPR_SARG, 2, 2, aChase, S_SARG_RUN7, false},
		S_SARG_RUN7:  {SPR_SARG, 3, 2, aChase, S_SARG_RUN8, false},
		S_SARG_RUN8:  {SPR_SARG, 3, 2, aChase, S_SARG_RUN1, false},

		S_HEAD_STND: {SPR_HEAD, 0, 10, aLook, S_HEAD_STND, false},
		S_HEAD_RUN1: {SPR_HEAD, 0, 3, aChase, S_HEAD_RUN1, false},

		S_SKULL_STND:  {SPR_SKUL, 0, 10, aLook, S_SKULL_STND2, true},
		S_SKULL_STND2: {SPR_SKUL, 1, 10, aLook, S_SKULL_STND, true},
		S_SKULL_RUN1:  bright(State{SPR_SKUL, 0, 6, aChase, S_SKULL_RUN2, true}),
		S_SKULL_RUN2:  bright(State{SPR_SKUL, 1, 6, aChase, S_SKULL_RUN1, true}),

		S_BOSS_STND:  {SPR_BOSS, 0, 10, aLook, S_BOSS_STND2, false},
		S_BOSS_STND2: {SPR_BOSS, 1, 10, aLook, S_BOSS_STND, false},
		S_BOSS_RUN1:  {SPR_BOSS, 0, 3, aChase, S_BOSS_RUN2, false},
		S_BOSS_RUN2:  {SPR_BOSS, 0, 3, aChase, S_BOSS_RUN3, false},
		S_BOSS_RUN3:  {SPR_BOSS, 1, 3, aChase, S_BOSS_RUN4, false},
		S_BOSS_RUN4:  {SPR_BOSS, 1, 3, aChase, S_BOSS_RUN5, false},
		S_BOSS_RUN5:  {SPR_BOSS, 2, 3, aChase, S_BOSS_RUN6, false},
		S_BOSS_RUN6:  {SPR_BOSS, 2, 3, aChase, S_BOSS_RUN7, false},
		S_BOSS_RUN7:  {SPR_BOSS, 3, 3, aChase, S_BOSS_RUN8, false},
		S_BOSS_RUN8:  {SPR_BOSS, 3, 3, aChase, S_BOSS_RUN1, false},

		S_BOS2_STND:  {SPR_BOS2, 0, 10, aLook, S_BOS2_STND2, false},
		S_BOS2_STND2: {SPR_BOS2, 1, 10, aLook, S_BOS2_STND, false},
		S_BOS2_RUN1:  {SPR_BOS2, 0, 3, aChase, S_BOS2_RUN2, false},
		S_BOS2_RUN2:  {SPR_BOS2, 0, 3, aChase, S_BOS2_RUN3, false},
		S_BOS2_RUN3:  {SPR_BOS2, 1, 3, aChase, S_BOS2_RUN4, false},
		S_BOS2_RUN4:  {SPR_BOS2, 1, 3, aChase, S_BOS2_RUN5, false},
		S_BOS2_RUN5:  {SPR_BOS2, 2, 3, aChase, S_BOS2_RUN6, false},
		S_BOS2_RUN6:  {SPR_BOS2, 2, 3, aChase, S_BOS2_RUN7, false},
		S_BOS2_RUN7:  {SPR_BOS2, 3, 3, aChase, S_BOS2_RUN8, false},
		S_BOS2_RUN8:  {SPR_BOS2, 3, 3, aChase, S_BOS2_RUN1, false},

		// --- Zombieman ---
		S_POSS_ATK1:  {SPR_POSS, 4, 10, aFaceTarget, S_POSS_ATK2, false},
		S_POSS_ATK2:  {SPR_POSS, 5, 8, aPosAttack, S_POSS_ATK3, false},
		S_POSS_ATK3:  {SPR_POSS, 4, 8, nil, S_POSS_RUN1, false},
		S_POSS_PAIN:  {SPR_POSS, 6, 3, nil, S_POSS_PAIN2, false},
		S_POSS_PAIN2: {SPR_POSS, 6, 3, aPain, S_POSS_RUN1, false},
		S_POSS_DIE1:  {SPR_POSS, 7, 5, nil, S_POSS_DIE2, false},
		S_POSS_DIE2:  {SPR_POSS, 8, 5, aScream, S_POSS_DIE3, false},
		S_POSS_DIE3:  {SPR_POSS, 9, 5, aFall, S_POSS_DIE4, false},
		S_POSS_DIE4:  {SPR_POSS, 10, 5, nil, S_POSS_DIE5, false},
		S_POSS_DIE5:  {SPR_POSS, 11, -1, nil, S_NULL, false},
		S_POSS_XDIE1: {SPR_POSS, 12, 5, nil, S_POSS_XDIE2, false},
		S_POSS_XDIE2: {SPR_POSS, 13, 5, aXScream, S_POSS_XDIE3, false},
		S_POSS_XDIE3: {SPR_POSS, 14, 5, aFall, S_POSS_XDIE4, false},
		S_POSS_XDIE4: {SPR_POSS, 15, 5, nil, S_POSS_XDIE5, false},
		S_POSS_XDIE5: {SPR_POSS, 16, 5, nil, S_POSS_XDIE6, false},
		S_POSS_XDIE6: {SPR_POSS, 17, 5, nil, S_POSS_XDIE7, false},
		S_POSS_XDIE7: {SPR_POSS, 18, 5, nil, S_POSS_XDIE8, false},
		S_POSS_XDIE8: {SPR_POSS, 19, 5, nil, S_POSS_XDIE9, false},
		S_POSS_XDIE9: {SPR_POSS, 20, -1, nil, S_NULL, false},

		// --- Shotgun guy ---
		S_SPOS_ATK1:  {SPR_SPOS, 4, 10, aFaceTarget, S_SPOS_ATK2, false},
		S_SPOS_ATK2:  {SPR_SPOS, 5, 10, aSPosAttack, S_SPOS_ATK3, false},
		S_SPOS_ATK3:  {SPR_SPOS, 5, 10, nil, S_SPOS_RUN1, false},
		S_SPOS_PAIN:  {SPR_SPOS, 6, 3, nil, S_SPOS_PAIN2, false},
		S_SPOS_PAIN2: {SPR_SPOS, 6, 3, aPain, S_SPOS_RUN1, false},
		S_SPOS_DIE1:  {SPR_SPOS, 7, 5, nil, S_SPOS_DIE2, false},
		S_SPOS_DIE2:  {SPR_SPOS, 8, 5, aScream, S_SPOS_DIE3, false},
		S_SPOS_DIE3:  {SPR_SPOS, 9, 5, aFall, S_SPOS_DIE4, false},
		S_SPOS_DIE4:  {SPR_SPOS, 10, 5, nil, S_SPOS_DIE5, false},
		S_SPOS_DIE5:  {SPR_SPOS, 11, -1, nil, S_NULL, false},
		S_SPOS_XDIE1: {SPR_SPOS, 12, 5, nil, S_SPOS_XDIE2, false},
		S_SPOS_XDIE2: {SPR_SPOS, 13, 5, aXScream, S_SPOS_XDIE3, false},
		S_SPOS_XDIE3: {SPR_SPOS, 14, 5, aFall, S_SPOS_XDIE4, false},
		S_SPOS_XDIE4: {SPR_SPOS, 15, 5, nil, S_SPOS_XDIE5, false},
		S_SPOS_XDIE5: {SPR_SPOS, 16, 5, nil, S_SPOS_XDIE6, false},
		S_SPOS_XDIE6: {SPR_SPOS, 17, 5, nil, S_SPOS_XDIE7, false},
		S_SPOS_XDIE7: {SPR_SPOS, 18, 5, nil, S_SPOS_XDIE8, false},
		S_SPOS_XDIE8: {SPR_SPOS, 19, 5, nil, S_SPOS_XDIE9, false},
		S_SPOS_XDIE9: {SPR_SPOS, 20, -1, nil, S_NULL, false},

		// --- Chaingunner ---
		S_CPOS_ATK1:  {SPR_CPOS, 4, 10, aFaceTarget, S_CPOS_ATK2, false},
		S_CPOS_ATK2:  {SPR_CPOS, 5, 4, aCPosAttack, S_CPOS_ATK3, false},
		S_CPOS_ATK3:  {SPR_CPOS, 4, 4, aCPosAttack, S_CPOS_ATK4, false},
		S_CPOS_ATK4:  {SPR_CPOS, 5, 1, aCPosRefire, S_CPOS_ATK2, false},
		S_CPOS_PAIN:  {SPR_CPOS, 6, 3, nil, S_CPOS_PAIN2, false},
		S_CPOS_PAIN2: {SPR_CPOS, 6, 3, aPain, S_CPOS_RUN1, false},
		S_CPOS_DIE1:  {SPR_CPOS, 7, 5, nil, S_CPOS_DIE2, false},
		S_CPOS_DIE2:  {SPR_CPOS, 8, 5, aScream, S_CPOS_DIE3, false},
		S_CPOS_DIE3:  {SPR_CPOS, 9, 5, aFall, S_CPOS_DIE4, false},
		S_CPOS_DIE4:  {SPR_CPOS, 10, 5, nil, S_CPOS_DIE5, false},
		S_CPOS_DIE5:  {SPR_CPOS, 11, 5, nil, S_CPOS_DIE6, false},
		S_CPOS_DIE6:  {SPR_CPOS, 12, 5, nil, S_CPOS_DIE7, false},
		S_CPOS_DIE7:  {SPR_CPOS, 13, -1, nil, S_NULL, false},

		// --- Imp ---
		S_TROO_ATK1:  {SPR_TROO, 4, 8, aFaceTarget, S_TROO_ATK2, false},
		S_TROO_ATK2:  {SPR_TROO, 5, 8, aFaceTarget, S_TROO_ATK3, false},
		S_TROO_ATK3:  {SPR_TROO, 6, 6, aTroopAttack, S_TROO_RUN1, false},
		S_TROO_PAIN:  {SPR_TROO, 7, 2, nil, S_TROO_PAIN2, false},
		S_TROO_PAIN2: {SPR_TROO, 7, 2, aPain, S_TROO_RUN1, false},
		S_TROO_DIE1:  {SPR_TROO, 8, 8, nil, S_TROO_DIE2, false},
		S_TROO_DIE2:  {SPR_TROO, 9, 8, aScream, S_TROO_DIE3, false},
		S_TROO_DIE3:  {SPR_TROO, 10, 6, aFall, S_TROO_DIE4, false},
		S_TROO_DIE4:  {SPR_TROO, 11, 6, nil, S_TROO_DIE5, false},
		S_TROO_DIE5:  {SPR_TROO, 12, -1, nil, S_NULL, false},
		S_TROO_XDIE1: {SPR_TROO, 13, 5, nil, S_TROO_XDIE2, false},
		S_TROO_XDIE2: {SPR_TROO, 14, 5, aXScream, S_TROO_XDIE3, false},
		S_TROO_XDIE3: {SPR_TROO, 15, 5, nil, S_TROO_XDIE4, false},
		S_TROO_XDIE4: {SPR_TROO, 16, 5, aFall, S_TROO_XDIE5, false},
		S_TROO_XDIE5: {SPR_TROO, 17, 5, nil, S_TROO_XDIE6, false},
		S_TROO_XDIE6: {SPR_TROO, 18, 5, nil, S_TROO_XDIE7, false},
		S_TROO_XDIE7: {SPR_TROO, 19, 5, nil, S_TROO_XDIE8, false},
		S_TROO_XDIE8: {SPR_TROO, 20, -1, nil, S_NULL, false},

		// --- Demon / Spectre ---
		S_SARG_ATK1:  {SPR_SARG, 4, 8, aFaceTarget, S_SARG_ATK2, false},
		S_SARG_ATK2:  {SPR_SARG, 5, 8, aFaceTarget, S_SARG_ATK3, false},
		S_SARG_ATK3:  {SPR_SARG, 6, 8, aSargAttack, S_SARG_RUN1, false},
		S_SARG_PAIN:  {SPR_SARG, 7, 2, nil, S_SARG_PAIN2, false},
		S_SARG_PAIN2: {SPR_SARG, 7, 2, aPain, S_SARG_RUN1, false},
		S_SARG_DIE1:  {SPR_SARG, 8, 8, nil, S_SARG_DIE2, false},
		S_SARG_DIE2:  {SPR_SARG, 9, 8, aScream, S_SARG_DIE3, false},
		S_SARG_DIE3:  {SPR_SARG, 10, 4, aFall, S_SARG_DIE4, false},
		S_SARG_DIE4:  {SPR_SARG, 11, 4, nil, S_SARG_DIE5, false},
		S_SARG_DIE5:  {SPR_SARG, 12, 4, nil, S_SARG_DIE6, false},
		S_SARG_DIE6:  {SPR_SARG, 13, -1, nil, S_NULL, false},

		// --- Cacodemon ---
		S_HEAD_ATK1:  {SPR_HEAD, 1, 5, aFaceTarget, S_HEAD_ATK2, false},
		S_HEAD_ATK2:  {SPR_HEAD, 2, 5, aFaceTarget, S_HEAD_ATK3, false},
		S_HEAD_ATK3:  bright(State{SPR_HEAD, 3, 5, aHeadAttack, S_HEAD_RUN1, true}),
		S_HEAD_PAIN:  {SPR_HEAD, 4, 3, nil, S_HEAD_PAIN2, false},
		S_HEAD_PAIN2: {SPR_HEAD, 4, 3, aPain, S_HEAD_PAIN3, false},
		S_HEAD_PAIN3: {SPR_HEAD, 5, 6, nil, S_HEAD_RUN1, false},
		S_HEAD_DIE1:  {SPR_HEAD, 6, 8, nil, S_HEAD_DIE2, false},
		S_HEAD_DIE2:  {SPR_HEAD, 7, 8, aScream, S_HEAD_DIE3, false},
		S_HEAD_DIE3:  {SPR_HEAD, 8, 8, nil, S_HEAD_DIE4, false},
		S_HEAD_DIE4:  {SPR_HEAD, 9, 8, aFall, S_HEAD_DIE5, false},
		S_HEAD_DIE5:  {SPR_HEAD, 10, 8, nil, S_HEAD_DIE6, false},
		S_HEAD_DIE6:  {SPR_HEAD, 11, -1, nil, S_NULL, false},

		// --- Lost soul ---
		S_SKULL_ATK1:  bright(State{SPR_SKUL, 2, 10, aFaceTarget, S_SKULL_ATK2, true}),
		S_SKULL_ATK2:  bright(State{SPR_SKUL, 3, 4, aSkullAttack, S_SKULL_ATK3, true}),
		S_SKULL_ATK3:  bright(State{SPR_SKUL, 2, 4, nil, S_SKULL_ATK4, true}),
		S_SKULL_ATK4:  bright(State{SPR_SKUL, 3, 4, nil, S_SKULL_ATK3, true}),
		S_SKULL_PAIN:  bright(State{SPR_SKUL, 4, 3, nil, S_SKULL_PAIN2, true}),
		S_SKULL_PAIN2: bright(State{SPR_SKUL, 4, 3, aPain, S_SKULL_RUN1, true}),
		S_SKULL_DIE1:  bright(State{SPR_SKUL, 5, 6, nil, S_SKULL_DIE2, true}),
		S_SKULL_DIE2:  bright(State{SPR_SKUL, 6, 6, aScream, S_SKULL_DIE3, true}),
		S_SKULL_DIE3:  bright(State{SPR_SKUL, 7, 6, nil, S_SKULL_DIE4, true}),
		S_SKULL_DIE4:  bright(State{SPR_SKUL, 8, 6, aFall, S_SKULL_DIE5, true}),
		S_SKULL_DIE5:  {SPR_SKUL, 9, 6, nil, S_SKULL_DIE6, false},
		S_SKULL_DIE6:  {SPR_SKUL, 10, 6, nil, S_NULL, false},

		// --- Baron of Hell ---
		S_BOSS_ATK1:  {SPR_BOSS, 4, 8, aFaceTarget, S_BOSS_ATK2, false},
		S_BOSS_ATK2:  {SPR_BOSS, 5, 8, aFaceTarget, S_BOSS_ATK3, false},
		S_BOSS_ATK3:  {SPR_BOSS, 6, 8, aBruisAttack, S_BOSS_RUN1, false},
		S_BOSS_PAIN:  {SPR_BOSS, 7, 2, nil, S_BOSS_PAIN2, false},
		S_BOSS_PAIN2: {SPR_BOSS, 7, 2, aPain, S_BOSS_RUN1, false},
		S_BOSS_DIE1:  {SPR_BOSS, 8, 8, nil, S_BOSS_DIE2, false},
		S_BOSS_DIE2:  {SPR_BOSS, 9, 8, aScream, S_BOSS_DIE3, false},
		S_BOSS_DIE3:  {SPR_BOSS, 10, 8, aFall, S_BOSS_DIE4, false},
		S_BOSS_DIE4:  {SPR_BOSS, 11, 8, nil, S_BOSS_DIE5, false},
		S_BOSS_DIE5:  {SPR_BOSS, 12, 8, nil, S_BOSS_DIE6, false},
		S_BOSS_DIE6:  {SPR_BOSS, 13, 8, nil, S_BOSS_DIE7, false},
		S_BOSS_DIE7:  {SPR_BOSS, 14, -1, aBossDeath, S_NULL, false},

		// --- Hell knight ---
		S_BOS2_ATK1:  {SPR_BOS2, 4, 8, aFaceTarget, S_BOS2_ATK2, false},
		S_BOS2_ATK2:  {SPR_BOS2, 5, 8, aFaceTarget, S_BOS2_ATK3, false},
		S_BOS2_ATK3:  {SPR_BOS2, 6, 8, aBruisAttack, S_BOS2_RUN1, false},
		S_BOS2_PAIN:  {SPR_BOS2, 7, 2, nil, S_BOS2_PAIN2, false},
		S_BOS2_PAIN2: {SPR_BOS2, 7, 2, aPain, S_BOS2_RUN1, false},
		S_BOS2_DIE1:  {SPR_BOS2, 8, 8, nil, S_BOS2_DIE2, false},
		S_BOS2_DIE2:  {SPR_BOS2, 9, 8, aScream, S_BOS2_DIE3, false},
		S_BOS2_DIE3:  {SPR_BOS2, 10, 8, aFall, S_BOS2_DIE4, false},
		S_BOS2_DIE4:  {SPR_BOS2, 11, 8, nil, S_BOS2_DIE5, false},
		S_BOS2_DIE5:  {SPR_BOS2, 12, 8, nil, S_BOS2_DIE6, false},
		S_BOS2_DIE6:  {SPR_BOS2, 13, 8, nil, S_BOS2_DIE7, false},
		S_BOS2_DIE7:  {SPR_BOS2, 14, -1, nil, S_NULL, false},

		// --- Missiles ---
		S_TBALL1:   bright(State{SPR_BAL1, 0, 4, nil, S_TBALL2, true}),
		S_TBALL2:   bright(State{SPR_BAL1, 1, 4, nil, S_TBALL1, true}),
		S_TBALLX1:  bright(State{SPR_BAL1, 2, 6, nil, S_TBALLX2, true}),
		S_TBALLX2:  bright(State{SPR_BAL1, 3, 6, nil, S_TBALLX3, true}),
		S_TBALLX3:  bright(State{SPR_BAL1, 4, 6, nil, S_NULL, true}),
		S_RBALL1:   bright(State{SPR_BAL2, 0, 4, nil, S_RBALL2, true}),
		S_RBALL2:   bright(State{SPR_BAL2, 1, 4, nil, S_RBALL1, true}),
		S_RBALLX1:  bright(State{SPR_BAL2, 2, 6, nil, S_RBALLX2, true}),
		S_RBALLX2:  bright(State{SPR_BAL2, 3, 6, nil, S_RBALLX3, true}),
		S_RBALLX3:  bright(State{SPR_BAL2, 4, 6, nil, S_NULL, true}),
		S_BRBALL1:  bright(State{SPR_BAL7, 0, 4, nil, S_BRBALL2, true}),
		S_BRBALL2:  bright(State{SPR_BAL7, 1, 4, nil, S_BRBALL1, true}),
		S_BRBALLX1: bright(State{SPR_BAL7, 2, 6, nil, S_BRBALLX2, true}),
		S_BRBALLX2: bright(State{SPR_BAL7, 3, 6, nil, S_BRBALLX3, true}),
		S_BRBALLX3: bright(State{SPR_BAL7, 4, 6, nil, S_NULL, true}),

		// --- Bullet puff / blood ---
		S_PUFF1:  bright(State{SPR_PUFF, 0, 4, nil, S_PUFF2, true}),
		S_PUFF2:  {SPR_PUFF, 1, 4, nil, S_PUFF3, false},
		S_PUFF3:  {SPR_PUFF, 2, 4, nil, S_PUFF4, false},
		S_PUFF4:  {SPR_PUFF, 3, 4, nil, S_NULL, false},
		S_BLOOD1: {SPR_BLUD, 2, 8, nil, S_BLOOD2, false},
		S_BLOOD2: {SPR_BLUD, 1, 8, nil, S_BLOOD3, false},
		S_BLOOD3: {SPR_BLUD, 0, 8, nil, S_NULL, false},

		S_TFOG1:  bright(State{SPR_TFOG, 0, 6, nil, S_TFOG2, true}),
		S_TFOG2:  bright(State{SPR_TFOG, 1, 6, nil, S_TFOG3, true}),
		S_TFOG3:  bright(State{SPR_TFOG, 2, 6, nil, S_TFOG4, true}),
		S_TFOG4:  bright(State{SPR_TFOG, 3, 6, nil, S_TFOG5, true}),
		S_TFOG5:  bright(State{SPR_TFOG, 4, 6, nil, S_TFOG6, true}),
		S_TFOG6:  bright(State{SPR_TFOG, 5, 6, nil, S_TFOG7, true}),
		S_TFOG7:  bright(State{SPR_TFOG, 6, 6, nil, S_TFOG8, true}),
		S_TFOG8:  bright(State{SPR_TFOG, 7, 6, nil, S_TFOG9, true}),
		S_TFOG9:  bright(State{SPR_TFOG, 8, 6, nil, S_TFOG10, true}),
		S_TFOG10: bright(State{SPR_TFOG, 9, 6, nil, S_NULL, true}),

		// --- Exploding barrel ---
		S_BAR1:  {SPR_BAR1, 0, 6, nil, S_BAR2, false},
		S_BAR2:  {SPR_BAR1, 1, 6, nil, S_BAR1, false},
		S_BEXP1: bright(State{SPR_BEXP, 0, 5, nil, S_BEXP2, true}),
		S_BEXP2: bright(State{SPR_BEXP, 1, 5, aScream, S_BEXP3, true}),
		S_BEXP3: bright(State{SPR_BEXP, 2, 5, nil, S_BEXP4, true}),
		S_BEXP4: bright(State{SPR_BEXP, 3, 10, aExplode, S_BEXP5, true}),
		S_BEXP5: bright(State{SPR_BEXP, 4, 10, nil, S_NULL, true}),

		S_ARM1:  {SPR_ARM1, 0, 6, nil, S_ARM1A, false},
		S_ARM1A: bright(State{SPR_ARM1, 1, 7, nil, S_ARM1, false}),
		S_ARM2:  {SPR_ARM2, 0, 6, nil, S_ARM2A, false},
		S_ARM2A: bright(State{SPR_ARM2, 1, 6, nil, S_ARM2, false}),

		S_BON1:  {SPR_BON1, 0, 6, nil, S_BON1A, false},
		S_BON1A: {SPR_BON1, 1, 6, nil, S_BON1B, false},
		S_BON1B: {SPR_BON1, 2, 6, nil, S_BON1C, false},
		S_BON1C: {SPR_BON1, 3, 6, nil, S_BON1D, false},
		S_BON1D: {SPR_BON1, 2, 6, nil, S_BON1E, false},
		S_BON1E: {SPR_BON1, 1, 6, nil, S_BON1, false},

		S_BON2:  {SPR_BON2, 0, 6, nil, S_BON2A, false},
		S_BON2A: {SPR_BON2, 1, 6, nil, S_BON2B, false},
		S_BON2B: {SPR_BON2, 2, 6, nil, S_BON2C, false},
		S_BON2C: {SPR_BON2, 3, 6, nil, S_BON2D, false},
		S_BON2D: {SPR_BON2, 2, 6, nil, S_BON2E, false},
		S_BON2E: {SPR_BON2, 1, 6, nil, S_BON2, false},

		S_BKEY:    {SPR_BKEY, 0, 10, nil, S_BKEY2, false},
		S_BKEY2:   bright(State{SPR_BKEY, 1, 10, nil, S_BKEY, false}),
		S_RKEY:    {SPR_RKEY, 0, 10, nil, S_RKEY2, false},
		S_RKEY2:   bright(State{SPR_RKEY, 1, 10, nil, S_RKEY, false}),
		S_YKEY:    {SPR_YKEY, 0, 10, nil, S_YKEY2, false},
		S_YKEY2:   bright(State{SPR_YKEY, 1, 10, nil, S_YKEY, false}),
		S_BSKULL:  {SPR_BSKU, 0, 10, nil, S_BSKULL2, false},
		S_BSKULL2: bright(State{SPR_BSKU, 1, 10, nil, S_BSKULL, false}),
		S_RSKULL:  {SPR_RSKU, 0, 10, nil, S_RSKULL2, false},
		S_RSKULL2: bright(State{SPR_RSKU, 1, 10, nil, S_RSKULL, false}),
		S_YSKULL:  {SPR_YSKU, 0, 10, nil, S_YSKULL2, false},
		S_YSKULL2: bright(State{SPR_YSKU, 1, 10, nil, S_YSKULL, false}),

		S_STIM: {SPR_STIM, 0, -1, nil, S_STIM, false},
		S_MEDI: {SPR_MEDI, 0, -1, nil, S_MEDI, false},

		S_SOUL:  bright(State{SPR_SOUL, 0, 6, nil, S_SOUL2, false}),
		S_SOUL2: bright(State{SPR_SOUL, 1, 6, nil, S_SOUL3, false}),
		S_SOUL3: bright(State{SPR_SOUL, 2, 6, nil, S_SOUL4, false}),
		S_SOUL4: bright(State{SPR_SOUL, 3, 6, nil, S_SOUL5, false}),
		S_SOUL5: bright(State{SPR_SOUL, 2, 6, nil, S_SOUL6, false}),
		S_SOUL6: bright(State{SPR_SOUL, 1, 6, nil, S_SOUL, false}),

		S_PINV:  bright(State{SPR_PINV, 0, 6, nil, S_PINV2, false}),
		S_PINV2: bright(State{SPR_PINV, 1, 6, nil, S_PINV3, false}),
		S_PINV3: bright(State{SPR_PINV, 2, 6, nil, S_PINV4, false}),
		S_PINV4: bright(State{SPR_PINV, 3, 6, nil, S_PINV, false}),

		S_PSTR: bright(State{SPR_PSTR, 0, -1, nil, S_PSTR, false}),

		S_PINS:  bright(State{SPR_PINS, 0, 6, nil, S_PINS2, false}),
		S_PINS2: bright(State{SPR_PINS, 1, 6, nil, S_PINS3, false}),
		S_PINS3: bright(State{SPR_PINS, 2, 6, nil, S_PINS4, false}),
		S_PINS4: bright(State{SPR_PINS, 3, 6, nil, S_PINS, false}),

		S_MEGA:  bright(State{SPR_MEGA, 0, 6, nil, S_MEGA2, false}),
		S_MEGA2: bright(State{SPR_MEGA, 1, 6, nil, S_MEGA3, false}),
		S_MEGA3: bright(State{SPR_MEGA, 2, 6, nil, S_MEGA4, false}),
		S_MEGA4: bright(State{SPR_MEGA, 3, 6, nil, S_MEGA, false}),

		S_SUIT: bright(State{SPR_SUIT, 0, -1, nil, S_SUIT, false}),

		S_PMAP:  bright(State{SPR_PMAP, 0, 6, nil, S_PMAP2, false}),
		S_PMAP2: bright(State{SPR_PMAP, 1, 6, nil, S_PMAP3, false}),
		S_PMAP3: bright(State{SPR_PMAP, 2, 6, nil, S_PMAP4, false}),
		S_PMAP4: bright(State{SPR_PMAP, 3, 6, nil, S_PMAP5, false}),
		S_PMAP5: bright(State{SPR_PMAP, 2, 6, nil, S_PMAP6, false}),
		S_PMAP6: bright(State{SPR_PMAP, 1, 6, nil, S_PMAP, false}),

		S_PVIS:  bright(State{SPR_PVIS, 0, 6, nil, S_PVIS2, false}),
		S_PVIS2: {SPR_PVIS, 1, 6, nil, S_PVIS, false},

		S_CLIP:  {SPR_CLIP, 0, -1, nil, S_CLIP, false},
		S_AMMO:  {SPR_AMMO, 0, -1, nil, S_AMMO, false},
		S_ROCK:  {SPR_ROCK, 0, -1, nil, S_ROCK, false},
		S_BROK:  {SPR_BROK, 0, -1, nil, S_BROK, false},
		S_CELL:  {SPR_CELL, 0, -1, nil, S_CELL, false},
		S_CELP:  {SPR_CELP, 0, -1, nil, S_CELP, false},
		S_SHEL:  {SPR_SHEL, 0, -1, nil, S_SHEL, false},
		S_SBOX:  {SPR_SBOX, 0, -1, nil, S_SBOX, false},
		S_BPAK:  {SPR_BPAK, 0, -1, nil, S_BPAK, false},
		S_BFUG:  {SPR_BFUG, 0, -1, nil, S_BFUG, false},
		S_MGUN:  {SPR_MGUN, 0, -1, nil, S_MGUN, false},
		S_CSAW:  {SPR_CSAW, 0, -1, nil, S_CSAW, false},
		S_LAUN:  {SPR_LAUN, 0, -1, nil, S_LAUN, false},
		S_PLAS:  {SPR_PLAS, 0, -1, nil, S_PLAS, false},
		S_SHOT:  {SPR_SHOT, 0, -1, nil, S_SHOT, false},
		S_SHOT2: {SPR_SGN2, 0, -1, nil, S_SHOT2, false},

		S_COLU:       bright(State{SPR_COLU, 0, -1, nil, S_COLU, false}),
		S_DEADTORSO:  {SPR_POL5, 0, -1, nil, S_DEADTORSO, false},
		S_GIBS:       {SPR_POL2, 0, -1, nil, S_GIBS, false},
		S_CANDLESTIK: bright(State{SPR_CAND, 0, -1, nil, S_CANDLESTIK, false}),
		S_CANDELABRA: bright(State{SPR_CBRA, 0, -1, nil, S_CANDELABRA, false}),

		// Static decorations — one frozen frame each.
		S_TALLGRNCOL:     {SPR_COL1, 0, -1, nil, S_TALLGRNCOL, false},
		S_SHRTGRNCOL:     {SPR_COL2, 0, -1, nil, S_SHRTGRNCOL, false},
		S_TALLREDCOL:     {SPR_COL3, 0, -1, nil, S_TALLREDCOL, false},
		S_SHRTREDCOL:     {SPR_COL4, 0, -1, nil, S_SHRTREDCOL, false},
		S_SKULLCOL:       {SPR_COL6, 0, -1, nil, S_SKULLCOL, false},
		S_HEARTCOL:       {SPR_COL5, 0, -1, nil, S_HEARTCOL, false},
		S_STALAGMITE:     {SPR_SMIT, 0, -1, nil, S_STALAGMITE, false},
		S_TECHPILLAR:     {SPR_ELEC, 0, -1, nil, S_TECHPILLAR, false},
		S_EVILEYE:        bright(State{SPR_CEYE, 0, -1, nil, S_EVILEYE, false}),
		S_FLOATSKULL:     bright(State{SPR_FSKU, 0, -1, nil, S_FLOATSKULL, false}),
		S_TORCHTREE:      {SPR_TRE1, 0, -1, nil, S_TORCHTREE, false},
		S_BIGTREE:        {SPR_TRE2, 0, -1, nil, S_BIGTREE, false},
		S_HANGNOGUTS:     {SPR_HDB1, 0, -1, nil, S_HANGNOGUTS, false},
		S_HANGBNOBRAIN:   {SPR_HDB2, 0, -1, nil, S_HANGBNOBRAIN, false},
		S_HANGTLOOKDN:    {SPR_HDB3, 0, -1, nil, S_HANGTLOOKDN, false},
		S_HANGTSKULL:     {SPR_HDB4, 0, -1, nil, S_HANGTSKULL, false},
		S_HANGTLOOKUP:    {SPR_HDB5, 0, -1, nil, S_HANGTLOOKUP, false},
		S_HANGTNOBRAIN:   {SPR_HDB6, 0, -1, nil, S_HANGTNOBRAIN, false},
		S_HEAD_ON_STICK:  {SPR_POL4, 0, -1, nil, S_HEAD_ON_STICK, false},
		S_HEADS_ON_STICK: {SPR_POL2, 0, -1, nil, S_HEADS_ON_STICK, false},
		S_DEADSTICK:      {SPR_POL1, 0, -1, nil, S_DEADSTICK, false},
		S_MEAT2:          {SPR_GOR2, 0, -1, nil, S_MEAT2, false},
		S_MEAT3:          {SPR_GOR3, 0, -1, nil, S_MEAT3, false},
		S_MEAT4:          {SPR_GOR4, 0, -1, nil, S_MEAT4, false},
		S_MEAT5:          {SPR_GOR5, 0, -1, nil, S_MEAT5, false},
		S_POOLBLOOD1:     {SPR_POB1, 0, -1, nil, S_POOLBLOOD1, false},
		S_POOLBLOOD2:     {SPR_POB2, 0, -1, nil, S_POOLBLOOD2, false},
		S_POOLBRAINS:     {SPR_BRS1, 0, -1, nil, S_POOLBRAINS, false},

		// Head candles — id's SPR_POL3 A/B two-frame flicker.
		S_HEADCANDLES1: bright(State{SPR_POL3, 0, 6, nil, S_HEADCANDLES2, false}),
		S_HEADCANDLES2: bright(State{SPR_POL3, 1, 6, nil, S_HEADCANDLES1, false}),
		// "Still alive on a stick" — SPR_POL6 A/B twitch.
		S_LIVESTICK1: {SPR_POL6, 0, 6, nil, S_LIVESTICK2, false},
		S_LIVESTICK2: {SPR_POL6, 1, 8, nil, S_LIVESTICK1, false},
		// Hanging twitching victim — SPR_GOR1 A/B/C.
		S_BLOODYTWITCH1: {SPR_GOR1, 0, 10, nil, S_BLOODYTWITCH2, false},
		S_BLOODYTWITCH2: {SPR_GOR1, 1, 15, nil, S_BLOODYTWITCH3, false},
		S_BLOODYTWITCH3: {SPR_GOR1, 2, 8, nil, S_BLOODYTWITCH1, false},

		// Torches / tech lamps — four-frame bright flicker, 4 tics each.
		S_TALLTECHLAMP1: bright(State{SPR_TLMP, 0, 4, nil, S_TALLTECHLAMP2, false}),
		S_TALLTECHLAMP2: bright(State{SPR_TLMP, 1, 4, nil, S_TALLTECHLAMP3, false}),
		S_TALLTECHLAMP3: bright(State{SPR_TLMP, 2, 4, nil, S_TALLTECHLAMP4, false}),
		S_TALLTECHLAMP4: bright(State{SPR_TLMP, 3, 4, nil, S_TALLTECHLAMP1, false}),
		S_SHRTTECHLAMP1: bright(State{SPR_TLP2, 0, 4, nil, S_SHRTTECHLAMP2, false}),
		S_SHRTTECHLAMP2: bright(State{SPR_TLP2, 1, 4, nil, S_SHRTTECHLAMP3, false}),
		S_SHRTTECHLAMP3: bright(State{SPR_TLP2, 2, 4, nil, S_SHRTTECHLAMP4, false}),
		S_SHRTTECHLAMP4: bright(State{SPR_TLP2, 3, 4, nil, S_SHRTTECHLAMP1, false}),
		S_BTORCHSHRT1:   bright(State{SPR_SMBT, 0, 4, nil, S_BTORCHSHRT2, false}),
		S_BTORCHSHRT2:   bright(State{SPR_SMBT, 1, 4, nil, S_BTORCHSHRT3, false}),
		S_BTORCHSHRT3:   bright(State{SPR_SMBT, 2, 4, nil, S_BTORCHSHRT4, false}),
		S_BTORCHSHRT4:   bright(State{SPR_SMBT, 3, 4, nil, S_BTORCHSHRT1, false}),
		S_GTORCHSHRT1:   bright(State{SPR_SMGT, 0, 4, nil, S_GTORCHSHRT2, false}),
		S_GTORCHSHRT2:   bright(State{SPR_SMGT, 1, 4, nil, S_GTORCHSHRT3, false}),
		S_GTORCHSHRT3:   bright(State{SPR_SMGT, 2, 4, nil, S_GTORCHSHRT4, false}),
		S_GTORCHSHRT4:   bright(State{SPR_SMGT, 3, 4, nil, S_GTORCHSHRT1, false}),
		S_RTORCHSHRT1:   bright(State{SPR_SMRT, 0, 4, nil, S_RTORCHSHRT2, false}),
		S_RTORCHSHRT2:   bright(State{SPR_SMRT, 1, 4, nil, S_RTORCHSHRT3, false}),
		S_RTORCHSHRT3:   bright(State{SPR_SMRT, 2, 4, nil, S_RTORCHSHRT4, false}),
		S_RTORCHSHRT4:   bright(State{SPR_SMRT, 3, 4, nil, S_RTORCHSHRT1, false}),
		S_BTORCH1:       bright(State{SPR_TBLU, 0, 4, nil, S_BTORCH2, false}),
		S_BTORCH2:       bright(State{SPR_TBLU, 1, 4, nil, S_BTORCH3, false}),
		S_BTORCH3:       bright(State{SPR_TBLU, 2, 4, nil, S_BTORCH4, false}),
		S_BTORCH4:       bright(State{SPR_TBLU, 3, 4, nil, S_BTORCH1, false}),
		S_GTORCH1:       bright(State{SPR_TGRN, 0, 4, nil, S_GTORCH2, false}),
		S_GTORCH2:       bright(State{SPR_TGRN, 1, 4, nil, S_GTORCH3, false}),
		S_GTORCH3:       bright(State{SPR_TGRN, 2, 4, nil, S_GTORCH4, false}),
		S_GTORCH4:       bright(State{SPR_TGRN, 3, 4, nil, S_GTORCH1, false}),
		S_RTORCH1:       bright(State{SPR_TRED, 0, 4, nil, S_RTORCH2, false}),
		S_RTORCH2:       bright(State{SPR_TRED, 1, 4, nil, S_RTORCH3, false}),
		S_RTORCH3:       bright(State{SPR_TRED, 2, 4, nil, S_RTORCH4, false}),
		S_RTORCH4:       bright(State{SPR_TRED, 3, 4, nil, S_RTORCH1, false}),
		S_BURNBARREL1:   bright(State{SPR_FCAN, 0, 4, nil, S_BURNBARREL2, false}),
		S_BURNBARREL2:   bright(State{SPR_FCAN, 1, 4, nil, S_BURNBARREL3, false}),
		S_BURNBARREL3:   bright(State{SPR_FCAN, 2, 4, nil, S_BURNBARREL1, false}),
	}
}
