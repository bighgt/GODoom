package engine

import "fmt"

// doomStateNames is id's statenum_t for Doom II v1.9, in order — a DeHackEd
// "Frame N" edits states[] entry N (S_NULL == frame 0). This is the fixed
// public enumeration the patch format is defined against (a data-format
// fact from info.h); it is NOT this engine's own stateNum order, which
// groups the chains differently. Names for actors this build doesn't
// implement (the weapon psprites, Arch-vile, Revenant, Mancubus, Spider
// Mastermind, Arachnotron, Cyberdemon, Pain Elemental, Wolf SS, Commander
// Keen, Icon of Sin, most Doom II decorations) are still listed so indices
// line up; a Frame edit that resolves to one of them is dropped with a log.
//
// Built in init() from sequence helpers so the long RUN1..12 / DIE1..N runs
// can't be mistyped; TestDoomStateAnchors pins the chunk boundaries against
// the known DeHackEd frame numbers.
var doomStateNames []string

// dehStateAlias bridges names this engine spells differently from id
// (left = id / DeHackEd, right = this engine's S_ const in states.go).
var dehStateAlias = map[string]string{
	"S_TFOG":         "S_TFOG1",
	"S_TFOG01":       "S_TFOG2",
	"S_TFOG02":       "S_TFOG3",
	"S_BEXP":         "S_BEXP1",
	"S_STALAG":       "S_STALAGMITE",
	"S_STALAGTITE":   "S_STALAGMITE",
	"S_HEADONASTICK": "S_HEAD_ON_STICK",
	"S_HEADSONSTICK": "S_HEADS_ON_STICK",
	"S_HEADCANDLES":  "S_HEADCANDLES1",
	"S_LIVESTICK":    "S_LIVESTICK1",
	"S_BLOODYTWITCH": "S_BLOODYTWITCH1",
	"S_TECHLAMP":     "S_TALLTECHLAMP1",
	"S_TECHLAMP2":    "S_TALLTECHLAMP2",
	"S_TECHLAMP3":    "S_TALLTECHLAMP3",
	"S_TECHLAMP4":    "S_TALLTECHLAMP4",
	"S_TECH2LAMP":    "S_SHRTTECHLAMP1",
	"S_TECH2LAMP2":   "S_SHRTTECHLAMP2",
	"S_TECH2LAMP3":   "S_SHRTTECHLAMP3",
	"S_TECH2LAMP4":   "S_SHRTTECHLAMP4",
	"S_BTORCHSHRT":   "S_BTORCHSHRT1",
	"S_GTORCHSHRT":   "S_GTORCHSHRT1",
	"S_RTORCHSHRT":   "S_RTORCHSHRT1",
	"S_BLUETORCH":    "S_BTORCH1",
	"S_BLUETORCH2":   "S_BTORCH2",
	"S_BLUETORCH3":   "S_BTORCH3",
	"S_BLUETORCH4":   "S_BTORCH4",
	"S_GREENTORCH":   "S_GTORCH1",
	"S_GREENTORCH2":  "S_GTORCH2",
	"S_GREENTORCH3":  "S_GTORCH3",
	"S_GREENTORCH4":  "S_GTORCH4",
	"S_REDTORCH":     "S_RTORCH1",
	"S_REDTORCH2":    "S_RTORCH2",
	"S_REDTORCH3":    "S_RTORCH3",
	"S_REDTORCH4":    "S_RTORCH4",
}

func init() {
	var out []string
	add := func(names ...string) { out = append(out, names...) }
	// seq: "PFX1", "PFX2", ... "PFXn"
	seq := func(pfx string, from, to int) {
		for i := from; i <= to; i++ {
			out = append(out, fmt.Sprintf("%s%d", pfx, i))
		}
	}
	// seq1: id's idiom "BASE", "BASE2", "BASE3", ... "BASEn" (first has no digit)
	seq1 := func(base string, n int) {
		out = append(out, base)
		for i := 2; i <= n; i++ {
			out = append(out, fmt.Sprintf("%s%d", base, i))
		}
	}
	// one standard monster tail: ATK1..na, PAIN + PAIN2, DIE1..nd,
	// [XDIE1..nx], [RAISE1..nr].
	tail := func(pfx string, na, nd, nx, nr int) {
		seq(pfx+"_ATK", 1, na)
		add(pfx+"_PAIN", pfx+"_PAIN2")
		seq(pfx+"_DIE", 1, nd)
		if nx > 0 {
			seq(pfx+"_XDIE", 1, nx)
		}
		if nr > 0 {
			seq(pfx+"_RAISE", 1, nr)
		}
	}

	// --- 0..89 : light + weapon psprites ---
	add("S_NULL", "S_LIGHTDONE")
	add("S_PUNCH", "S_PUNCHDOWN", "S_PUNCHUP")
	seq("S_PUNCH", 1, 5)
	add("S_PISTOL", "S_PISTOLDOWN", "S_PISTOLUP")
	seq("S_PISTOL", 1, 4)
	add("S_PISTOLFLASH")
	add("S_SGUN", "S_SGUNDOWN", "S_SGUNUP")
	seq("S_SGUN", 1, 9)
	add("S_SGUNFLASH1", "S_SGUNFLASH2")
	add("S_DSGUN", "S_DSGUNDOWN", "S_DSGUNUP")
	seq("S_DSGUN", 1, 10)
	add("S_DSNR1", "S_DSNR2", "S_DSGUNFLASH1", "S_DSGUNFLASH2")
	add("S_CHAIN", "S_CHAINDOWN", "S_CHAINUP")
	seq("S_CHAIN", 1, 3)
	add("S_CHAINFLASH1", "S_CHAINFLASH2")
	add("S_MISSILE", "S_MISSILEDOWN", "S_MISSILEUP")
	seq("S_MISSILE", 1, 3)
	seq("S_MISSILEFLASH", 1, 4)
	add("S_SAW", "S_SAWB", "S_SAWDOWN", "S_SAWUP")
	seq("S_SAW", 1, 3)
	add("S_PLASMA", "S_PLASMADOWN", "S_PLASMAUP")
	seq("S_PLASMA", 1, 2)
	add("S_PLASMAFLASH1", "S_PLASMAFLASH2")
	add("S_BFG", "S_BFGDOWN", "S_BFGUP")
	seq("S_BFG", 1, 4)
	add("S_BFGFLASH1", "S_BFGFLASH2")

	// --- 90..148 : effects / projectiles ---
	seq("S_BLOOD", 1, 3)
	seq("S_PUFF", 1, 4)
	add("S_TBALL1", "S_TBALL2", "S_TBALLX1", "S_TBALLX2", "S_TBALLX3")
	add("S_RBALL1", "S_RBALL2", "S_RBALLX1", "S_RBALLX2", "S_RBALLX3")
	add("S_PLASBALL", "S_PLASBALL2", "S_PLASEXP", "S_PLASEXP2", "S_PLASEXP3", "S_PLASEXP4", "S_PLASEXP5")
	add("S_ROCKET")
	add("S_BFGSHOT", "S_BFGSHOT2", "S_BFGLAND")
	seq("S_BFGLAND", 2, 6)
	add("S_BFGEXP", "S_BFGEXP2", "S_BFGEXP3", "S_BFGEXP4")
	seq("S_EXPLODE", 1, 3)
	add("S_TFOG", "S_TFOG01", "S_TFOG02")
	seq("S_TFOG", 2, 10)
	add("S_IFOG", "S_IFOG01", "S_IFOG02")
	seq("S_IFOG", 2, 5)

	// --- 149..173 : player ---
	add("S_PLAY")
	seq("S_PLAY_RUN", 1, 4)
	add("S_PLAY_ATK1", "S_PLAY_ATK2", "S_PLAY_PAIN", "S_PLAY_PAIN2")
	seq("S_PLAY_DIE", 1, 7)
	seq("S_PLAY_XDIE", 1, 9)

	// --- monsters, in mobjtype_t order ---
	add("S_POSS_STND", "S_POSS_STND2")
	seq("S_POSS_RUN", 1, 8)
	tail("S_POSS", 3, 5, 9, 4) // 174..206

	add("S_SPOS_STND", "S_SPOS_STND2")
	seq("S_SPOS_RUN", 1, 8)
	tail("S_SPOS", 3, 5, 9, 5)

	add("S_VILE_STND", "S_VILE_STND2")
	seq("S_VILE_RUN", 1, 12)
	seq("S_VILE_ATK", 1, 11)
	seq("S_VILE_HEAL", 1, 3)
	add("S_VILE_PAIN", "S_VILE_PAIN2")
	seq("S_VILE_DIE", 1, 10)

	seq("S_FIRE", 1, 30)
	seq("S_SMOKE", 1, 5)
	add("S_TRACER", "S_TRACER2", "S_TRACEEXP1", "S_TRACEEXP2", "S_TRACEEXP3")

	add("S_SKEL_STND", "S_SKEL_STND2")
	seq("S_SKEL_RUN", 1, 12)
	seq("S_SKEL_FIST", 1, 4)
	seq("S_SKEL_MISS", 1, 4)
	add("S_SKEL_PAIN", "S_SKEL_PAIN2")
	seq("S_SKEL_DIE", 1, 6)
	seq("S_SKEL_RAISE", 1, 6)

	add("S_FATSHOT1", "S_FATSHOT2", "S_FATSHOTX1", "S_FATSHOTX2", "S_FATSHOTX3")
	add("S_FATT_STND", "S_FATT_STND2")
	seq("S_FATT_RUN", 1, 12)
	seq("S_FATT_ATK", 1, 10)
	add("S_FATT_PAIN", "S_FATT_PAIN2")
	seq("S_FATT_DIE", 1, 10)
	seq("S_FATT_RAISE", 1, 8)

	add("S_CPOS_STND", "S_CPOS_STND2")
	seq("S_CPOS_RUN", 1, 8)
	tail("S_CPOS", 4, 7, 6, 7) // 406..441

	add("S_TROO_STND", "S_TROO_STND2") // 442..443
	seq("S_TROO_RUN", 1, 8)
	tail("S_TROO", 3, 5, 8, 5)

	add("S_SARG_STND", "S_SARG_STND2") // 475..476
	seq("S_SARG_RUN", 1, 8)
	seq("S_SARG_ATK", 1, 3)
	add("S_SARG_PAIN", "S_SARG_PAIN2")
	seq("S_SARG_DIE", 1, 6)
	seq("S_SARG_RAISE", 1, 6)

	add("S_HEAD_STND", "S_HEAD_RUN1") // 502..503
	seq("S_HEAD_ATK", 1, 3)
	add("S_HEAD_PAIN", "S_HEAD_PAIN2", "S_HEAD_PAIN3")
	seq("S_HEAD_DIE", 1, 6)
	seq("S_HEAD_RAISE", 1, 6)

	add("S_BRBALL1", "S_BRBALL2", "S_BRBALLX1", "S_BRBALLX2", "S_BRBALLX3") // 522..526

	add("S_BOSS_STND", "S_BOSS_STND2") // 527..528
	seq("S_BOSS_RUN", 1, 8)
	tail("S_BOSS", 3, 7, 0, 7)

	add("S_BOS2_STND", "S_BOS2_STND2") // 556..557
	seq("S_BOS2_RUN", 1, 8)
	tail("S_BOS2", 3, 7, 0, 7)

	add("S_SKULL_STND", "S_SKULL_STND2", "S_SKULL_RUN1", "S_SKULL_RUN2") // 585..588
	seq("S_SKULL_ATK", 1, 4)
	add("S_SKULL_PAIN", "S_SKULL_PAIN2")
	seq("S_SKULL_DIE", 1, 6)

	add("S_SPID_STND", "S_SPID_STND2") // 601..602
	seq("S_SPID_RUN", 1, 12)
	seq("S_SPID_ATK", 1, 4)
	add("S_SPID_PAIN", "S_SPID_PAIN2")
	seq("S_SPID_DIE", 1, 11)

	add("S_BSPI_STND", "S_BSPI_STND2", "S_BSPI_SIGHT") // 632..634
	seq("S_BSPI_RUN", 1, 12)
	seq("S_BSPI_ATK", 1, 4)
	add("S_BSPI_PAIN", "S_BSPI_PAIN2")
	seq("S_BSPI_DIE", 1, 7)
	seq("S_BSPI_RAISE", 1, 7)

	add("S_ARACH_PLAZ", "S_ARACH_PLAZ2") // 667..668
	seq1("S_ARACH_PLEX", 5)

	add("S_CYBER_STND", "S_CYBER_STND2") // 674..675
	seq("S_CYBER_RUN", 1, 8)
	seq("S_CYBER_ATK", 1, 6)
	add("S_CYBER_PAIN")
	seq("S_CYBER_DIE", 1, 10)

	add("S_PAIN_STND") // 701
	seq("S_PAIN_RUN", 1, 6)
	seq("S_PAIN_ATK", 1, 4)
	add("S_PAIN_PAIN", "S_PAIN_PAIN2")
	seq("S_PAIN_DIE", 1, 6)
	seq("S_PAIN_RAISE", 1, 6)

	add("S_SSWV_STND", "S_SSWV_STND2") // 726..727
	seq("S_SSWV_RUN", 1, 8)
	seq("S_SSWV_ATK", 1, 6)
	add("S_SSWV_PAIN", "S_SSWV_PAIN2")
	seq("S_SSWV_DIE", 1, 5)
	seq("S_SSWV_XDIE", 1, 9)
	seq("S_SSWV_RAISE", 1, 5)

	add("S_KEENSTND") // 763
	seq1("S_COMMKEEN", 12)
	add("S_KEENPAIN", "S_KEENPAIN2")

	add("S_BRAIN", "S_BRAIN_PAIN") // 778..779
	seq("S_BRAIN_DIE", 1, 4)
	add("S_BRAINEYE", "S_BRAINEYESEE", "S_BRAINEYE1")
	seq("S_SPAWN", 1, 4)
	seq("S_SPAWNFIRE", 1, 8)
	seq("S_BRAINEXPLODE", 1, 3)

	// --- 802..966 : items / decorations ---
	add("S_ARM1", "S_ARM1A", "S_ARM2", "S_ARM2A", "S_BAR1", "S_BAR2")
	add("S_BEXP", "S_BEXP2", "S_BEXP3", "S_BEXP4", "S_BEXP5", "S_BBAR1", "S_BBAR2", "S_BBAR3")
	add("S_BON1", "S_BON1A", "S_BON1B", "S_BON1C", "S_BON1D", "S_BON1E")
	add("S_BON2", "S_BON2A", "S_BON2B", "S_BON2C", "S_BON2D", "S_BON2E")
	add("S_BKEY", "S_BKEY2", "S_RKEY", "S_RKEY2", "S_YKEY", "S_YKEY2")
	add("S_BSKULL", "S_BSKULL2", "S_RSKULL", "S_RSKULL2", "S_YSKULL", "S_YSKULL2")
	add("S_STIM", "S_MEDI")
	seq1("S_SOUL", 6)
	seq1("S_PINV", 4)
	add("S_PSTR")
	seq1("S_PINS", 4)
	seq1("S_MEGA", 4)
	add("S_SUIT")
	seq1("S_PMAP", 6)
	seq1("S_PVIS", 2)
	add("S_CLIP", "S_AMMO", "S_ROCK", "S_BROK", "S_CELL", "S_CELP", "S_SHEL", "S_SBOX", "S_BPAK")
	add("S_BFUG", "S_MGUN", "S_CSAW", "S_LAUN", "S_PLAS", "S_SHOT", "S_SHOT2")
	add("S_COLU", "S_STALAG")
	seq1("S_BLOODYTWITCH", 4)
	add("S_DEADTORSO", "S_DEADBOTTOM", "S_HEADSONSTICK", "S_GIBS", "S_HEADONASTICK")
	add("S_HEADCANDLES", "S_HEADCANDLES2", "S_DEADSTICK")
	seq1("S_LIVESTICK", 2)
	add("S_MEAT2", "S_MEAT3", "S_MEAT4", "S_MEAT5", "S_STALAGTITE")
	add("S_TALLGRNCOL", "S_SHRTGRNCOL", "S_TALLREDCOL", "S_SHRTREDCOL", "S_CANDLESTIK", "S_CANDELABRA")
	add("S_SKULLCOL", "S_TORCHTREE", "S_BIGTREE", "S_TECHPILLAR")
	seq1("S_EVILEYE", 4)
	seq1("S_FLOATSKULL", 3)
	add("S_HEARTCOL", "S_HEARTCOL2")
	seq1("S_BLUETORCH", 4)
	seq1("S_GREENTORCH", 4)
	seq1("S_REDTORCH", 4)
	seq1("S_BTORCHSHRT", 4)
	seq1("S_GTORCHSHRT", 4)
	seq1("S_RTORCHSHRT", 4)
	add("S_HANGNOGUTS", "S_HANGBNOBRAIN", "S_HANGTLOOKDN", "S_HANGTSKULL", "S_HANGTLOOKUP", "S_HANGTNOBRAIN")
	add("S_COLONGIBS", "S_SMALLPOOL", "S_BRAINSTEM")
	seq1("S_TECHLAMP", 4)
	seq1("S_TECH2LAMP", 4)

	doomStateNames = out
}

// engineStateByName maps a state name to this engine's stateNum, built from
// engineStateOrder (which mirrors states.go's iota block 1:1).
var engineStateByName map[string]stateNum

func init() {
	engineStateByName = make(map[string]stateNum, len(engineStateOrder))
	for i, n := range engineStateOrder {
		engineStateByName[n] = stateNum(i)
	}
}

// dehState translates a DeHackEd frame number to this engine's stateNum.
// ok=false when the number is out of range or names a state this build
// doesn't have (weapon psprite frames, unimplemented monsters).
func dehState(n int) (stateNum, bool) {
	if n < 0 || n >= len(doomStateNames) {
		return 0, false
	}
	name := doomStateNames[n]
	if a, ok := dehStateAlias[name]; ok {
		name = a
	}
	st, ok := engineStateByName[name]
	return st, ok
}

// doomSpriteNames is id's spritenum_t for Doom II v1.9 — a DeHackEd
// "Sprite number = N" in a Frame block, and a BEX [SPRITES] key, index
// this list. Values are the 4-letter WAD sprite-lump prefixes (info.h's
// sprnames[], another data-format fact). 138 entries.
var doomSpriteNames = [...]string{
	"TROO", "SHTG", "PUNG", "PISG", "PISF", "SHTF", "SHT2", "CHGG", "CHGF",
	"MISG", "MISF", "SAWG", "PLSG", "PLSF", "BFGG", "BFGF", "BLUD", "PUFF",
	"BAL1", "BAL2", "PLSS", "PLSE", "MISL", "BFS1", "BFE1", "BFE2", "TFOG",
	"IFOG", "PLAY", "POSS", "SPOS", "VILE", "FIRE", "FATB", "FBXP", "SKEL",
	"MANF", "FATT", "CPOS", "SARG", "HEAD", "BAL7", "BOSS", "BOS2", "SKUL",
	"SPID", "BSPI", "APLS", "APBX", "CYBR", "PAIN", "SSWV", "KEEN", "BBRN",
	"BOSF", "ARM1", "ARM2", "BAR1", "BEXP", "FCAN", "BON1", "BON2", "BKEY",
	"RKEY", "YKEY", "BSKU", "RSKU", "YSKU", "STIM", "MEDI", "SOUL", "PINV",
	"PSTR", "PINS", "MEGA", "SUIT", "PMAP", "PVIS", "CLIP", "AMMO", "ROCK",
	"BROK", "CELL", "CELP", "SHEL", "SBOX", "BPAK", "BFUG", "MGUN", "CSAW",
	"LAUN", "PLAS", "SHOT", "SGN2", "COLU", "SMT2", "GOR1", "POL2", "POL5",
	"POL4", "POL3", "POL1", "POL6", "GOR2", "GOR3", "GOR4", "GOR5", "SMIT",
	"COL1", "COL2", "COL3", "COL4", "CAND", "CBRA", "COL6", "TRE1", "TRE2",
	"ELEC", "CEYE", "FSKU", "COL5", "TBLU", "TGRN", "TRED", "SMBT", "SMGT",
	"SMRT", "HDB1", "HDB2", "HDB3", "HDB4", "HDB5", "HDB6", "POB1", "POB2",
	"BRS1", "TLMP", "TLP2",
}

// engineSpriteByName is spriteNames reversed — a 4-letter prefix back to
// this engine's spriteNum. Built once from states.go's table.
var engineSpriteByName map[string]spriteNum

func init() {
	engineSpriteByName = make(map[string]spriteNum, len(spriteNames))
	for i, n := range spriteNames {
		if n != "" {
			engineSpriteByName[n] = spriteNum(i)
		}
	}
}

// dehSprite maps a DeHackEd sprite number to this engine's spriteNum.
// ok=false when the number is out of range or names a sprite this build
// doesn't carry.
func dehSprite(n int) (spriteNum, bool) {
	if n < 0 || n >= len(doomSpriteNames) {
		return 0, false
	}
	s, ok := engineSpriteByName[doomSpriteNames[n]]
	return s, ok
}
