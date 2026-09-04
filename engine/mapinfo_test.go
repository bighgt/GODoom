package engine

import (
	"testing"

	"twopointfive/engine/mapinfo"
)

func TestUMapInfoNextMapOverride(t *testing.T) {
	g := loadRealLevel(t, "../wad/Doom2.wad", "MAP01")
	g.mapInfo = map[string]*mapinfo.Entry{
		"MAP01": {Next: "MAP05", NextSecret: "MAP31"},
		"MAP05": {EndGame: true},
		"MAP07": {Next: "MAP99"}, // not in the WAD -> falls back
	}

	if got := g.nextMapName("MAP01", false); got != "MAP05" {
		t.Errorf("MAP01 normal exit -> %q, want MAP05 (UMAPINFO next)", got)
	}
	if got := g.nextMapName("MAP01", true); got != "MAP31" {
		t.Errorf("MAP01 secret exit -> %q, want MAP31 (UMAPINFO nextsecret)", got)
	}
	if got := g.nextMapName("MAP05", false); got != "MAP05" {
		t.Errorf("MAP05 endgame -> %q, want to stay on MAP05", got)
	}
	if got := g.nextMapName("MAP07", false); got != "MAP08" {
		t.Errorf("MAP07 bad UMAPINFO next -> %q, want the MAP08 default", got)
	}
	// No entry at all -> the vanilla progression.
	if got := g.nextMapName("MAP02", false); got != "MAP03" {
		t.Errorf("MAP02 (no entry) -> %q, want MAP03", got)
	}
}

func TestUMapInfoLevelNameAndMusic(t *testing.T) {
	g := loadRealLevel(t, "../wad/Doom2.wad", "MAP01")
	if g.levelDisplayName() != "MAP01" {
		t.Errorf("no UMAPINFO -> display name %q, want the lump name", g.levelDisplayName())
	}
	if g.MapMusicLump("MAP01") != "" {
		t.Errorf("no UMAPINFO -> music override %q, want empty", g.MapMusicLump("MAP01"))
	}

	g.mapInfo = map[string]*mapinfo.Entry{
		"MAP01": {LevelName: "The Foyer", Music: "D_CUSTOM", SkyTexture: "SKY3", ParTime: 45},
	}
	if g.levelDisplayName() != "The Foyer" {
		t.Errorf("display name = %q, want \"The Foyer\"", g.levelDisplayName())
	}
	if g.MapMusicLump("map01") != "D_CUSTOM" { // case-insensitive
		t.Errorf("music override = %q, want D_CUSTOM", g.MapMusicLump("map01"))
	}

	g.ApplyMapInfo()
	if g.parTime != 45 {
		t.Errorf("ApplyMapInfo -> parTime %d, want 45", g.parTime)
	}
}
