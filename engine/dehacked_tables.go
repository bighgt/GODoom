package engine

import "strconv"

// DeHackEd numbering tables — the fixed orderings a DEH/BEX patch addresses
// tables by. These are the public enumeration orders from Doom's info.h /
// sounds.h (data-format facts, like the WAD directory layout), not
// behaviour. engine/dehacked parses; this file translates its numbers to
// the engine's own mobjType / MF_* / sound names.

// dehThingNames is id's mobjtype_t order for Thing numbers 1..79 (a
// DeHackEd "Thing N" edits mobjtype_t N-1). Stops at MT_SUPERSHOTGUN — the
// numbers past it are Doom II decorations this engine indexes under its own
// names, and DEH almost never retunes a decoration's stats.
var dehThingNames = []string{
	"MT_PLAYER", "MT_POSSESSED", "MT_SHOTGUY", "MT_VILE", "MT_FIRE", "MT_UNDEAD",
	"MT_TRACER", "MT_SMOKE", "MT_FATSO", "MT_FATSHOT", "MT_CHAINGUY", "MT_TROOP",
	"MT_SERGEANT", "MT_SHADOWS", "MT_HEAD", "MT_BRUISER", "MT_BRUISERSHOT", "MT_KNIGHT",
	"MT_SKULL", "MT_SPIDER", "MT_BABY", "MT_CYBORG", "MT_PAIN", "MT_WOLFSS",
	"MT_KEEN", "MT_BOSSBRAIN", "MT_BOSSSPIT", "MT_BOSSTARGET", "MT_SPAWNSHOT", "MT_SPAWNFIRE",
	"MT_BARREL", "MT_TROOPSHOT", "MT_HEADSHOT", "MT_ROCKET", "MT_PLASMA", "MT_BFG",
	"MT_ARACHPLAZ", "MT_PUFF", "MT_BLOOD", "MT_TFOG", "MT_IFOG", "MT_TELEPORTMAN",
	"MT_EXTRABFG", "MT_MISC0", "MT_MISC1", "MT_MISC2", "MT_MISC3", "MT_MISC4", "MT_MISC5",
	"MT_MISC6", "MT_MISC7", "MT_MISC8", "MT_MISC9", "MT_MISC10", "MT_MISC11", "MT_MISC12",
	"MT_INV", "MT_MISC13", "MT_INS", "MT_MISC14", "MT_MISC15", "MT_MISC16", "MT_MEGA",
	"MT_CLIP", "MT_MISC17", "MT_MISC18", "MT_MISC19", "MT_MISC20", "MT_MISC21", "MT_MISC22",
	"MT_MISC23", "MT_MISC24", "MT_MISC25", "MT_CHAINGUN", "MT_MISC26", "MT_MISC27", "MT_MISC28",
	"MT_SHOTGUN", "MT_SUPERSHOTGUN",
}

// dehTypeByName is every mobjType this engine actually defines, keyed by its
// id name — the target of a dehThingNames lookup.
var dehTypeByName = map[string]mobjType{
	"MT_PLAYER": MT_PLAYER, "MT_POSSESSED": MT_POSSESSED, "MT_SHOTGUY": MT_SHOTGUY,
	"MT_CHAINGUY": MT_CHAINGUY, "MT_TROOP": MT_TROOP, "MT_SERGEANT": MT_SERGEANT,
	"MT_SHADOWS": MT_SHADOWS, "MT_HEAD": MT_HEAD, "MT_SKULL": MT_SKULL,
	"MT_BRUISER": MT_BRUISER, "MT_KNIGHT": MT_KNIGHT, "MT_BARREL": MT_BARREL,
	"MT_TROOPSHOT": MT_TROOPSHOT, "MT_HEADSHOT": MT_HEADSHOT, "MT_BRUISERSHOT": MT_BRUISERSHOT,
	"MT_PUFF": MT_PUFF, "MT_BLOOD": MT_BLOOD, "MT_TFOG": MT_TFOG, "MT_TELEPORTMAN": MT_TELEPORTMAN,
	"MT_MISC0": MT_MISC0, "MT_MISC1": MT_MISC1, "MT_MISC2": MT_MISC2, "MT_MISC3": MT_MISC3,
	"MT_MISC4": MT_MISC4, "MT_MISC5": MT_MISC5, "MT_MISC6": MT_MISC6, "MT_MISC7": MT_MISC7,
	"MT_MISC8": MT_MISC8, "MT_MISC9": MT_MISC9, "MT_MISC10": MT_MISC10, "MT_MISC11": MT_MISC11,
	"MT_MISC12": MT_MISC12, "MT_INV": MT_INV, "MT_MISC13": MT_MISC13, "MT_INS": MT_INS,
	"MT_MISC14": MT_MISC14, "MT_MISC15": MT_MISC15, "MT_MISC16": MT_MISC16, "MT_MEGA": MT_MEGA,
	"MT_CLIP": MT_CLIP, "MT_MISC17": MT_MISC17, "MT_MISC18": MT_MISC18, "MT_MISC19": MT_MISC19,
	"MT_MISC20": MT_MISC20, "MT_MISC21": MT_MISC21, "MT_MISC22": MT_MISC22, "MT_MISC23": MT_MISC23,
	"MT_MISC24": MT_MISC24, "MT_MISC25": MT_MISC25, "MT_CHAINGUN": MT_CHAINGUN,
	"MT_MISC26": MT_MISC26, "MT_MISC27": MT_MISC27, "MT_MISC28": MT_MISC28,
	"MT_SHOTGUN": MT_SHOTGUN, "MT_SUPERSHOTGUN": MT_SUPERSHOTGUN,
}

// dehThingType maps a 1-based DeHackEd Thing number to this engine's
// mobjType. ok=false when the number is out of the mapped range or names an
// actor this build doesn't implement.
func dehThingType(n int) (mobjType, bool) {
	if n < 1 || n > len(dehThingNames) {
		return 0, false
	}
	t, ok := dehTypeByName[dehThingNames[n-1]]
	return t, ok
}

// dehFlagBits turns a DeHackEd `Bits` flag-name list into an MF_* bitmask.
// Names this engine doesn't model (SLIDE, the TRANSLATION colour bits,
// Boom/MBF extras) are ignored. A single numeric element is taken as a raw
// mask and kept only for the bits the engine knows.
func dehFlagBits(names []string) int {
	byName := map[string]int{
		"SPECIAL": MF_SPECIAL, "SOLID": MF_SOLID, "SHOOTABLE": MF_SHOOTABLE,
		"NOSECTOR": MF_NOSECTOR, "NOBLOCKMAP": MF_NOBLOCKMAP, "AMBUSH": MF_AMBUSH,
		"JUSTHIT": MF_JUSTHIT, "JUSTATTACKED": MF_JUSTATTACKED, "SPAWNCEILING": MF_SPAWNCEILING,
		"NOGRAVITY": MF_NOGRAVITY, "DROPOFF": MF_DROPOFF, "PICKUP": MF_PICKUP,
		"NOCLIP": MF_NOCLIP, "FLOAT": MF_FLOAT, "TELEPORT": MF_TELEPORT, "MISSILE": MF_MISSILE,
		"DROPPED": MF_DROPPED, "SHADOW": MF_SHADOW, "NOBLOOD": MF_NOBLOOD, "CORPSE": MF_CORPSE,
		"INFLOAT": MF_INFLOAT, "COUNTKILL": MF_COUNTKILL, "COUNTITEM": MF_COUNTITEM,
		"SKULLFLY": MF_SKULLFLY, "NOTDMATCH": MF_NOTDMATCH,
	}
	known := 0
	for _, b := range byName {
		known |= b
	}
	if len(names) == 1 {
		if n, err := strconv.Atoi(names[0]); err == nil {
			return n & known
		}
	}
	out := 0
	for _, nm := range names {
		if b, ok := byName[nm]; ok {
			out |= b
		}
	}
	return out
}
