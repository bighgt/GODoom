package wad

import (
	"strconv"
	"strings"
)

// doom2Music is the music lump for each Doom II / Final Doom map slot,
// MAP01..MAP32, in order — id's mus_runnin.. sequence from sounds.c. Unlike
// Doom/Ultimate Doom (where the lump is simply "D_" + the map name), Doom
// II's music lumps have their own names with no relation to "MAPxx".
var doom2Music = [32]string{
	"D_RUNNIN", "D_STALKS", "D_COUNTD", "D_BETWEE", "D_DOOM", "D_THE_DA",
	"D_SHAWN", "D_DDTBLU", "D_IN_CIT", "D_DEAD", "D_STLKS2", "D_THEDA2",
	"D_DOOM2", "D_DDTBL2", "D_RUNNI2", "D_DEAD2", "D_STLKS3", "D_ROMERO",
	"D_SHAWN2", "D_MESSAG", "D_COUNT2", "D_DDTBL3", "D_AMPIE", "D_THEDA3",
	"D_ADRIAN", "D_MESSG2", "D_ROMER2", "D_TENSE", "D_SHAWN3", "D_OPENIN",
	"D_EVIL", "D_ULTIMA",
}

// MusicLumpName returns the music lump name a given map marker conventionally
// uses. For "ExMy" (Doom / Ultimate Doom) that's just "D_ExMy"; for "MAPxx"
// (Doom II / Final Doom / most PWADs) it's the fixed per-slot name from
// doom2Music. The returned name is the *conventional* one — the caller
// should still check the lump actually exists (a PWAD may replace the music
// under the "D_"+map name instead, or ship none), and fall back accordingly.
func MusicLumpName(mapName string) string {
	m := strings.ToUpper(strings.TrimSpace(mapName))
	if strings.HasPrefix(m, "MAP") {
		if n, err := strconv.Atoi(m[3:]); err == nil && n >= 1 && n <= len(doom2Music) {
			return doom2Music[n-1]
		}
	}
	return "D_" + m
}
