package engine

import (
	"log"

	"twopointfive/assets/vfs"
	"twopointfive/engine/mapinfo"
	"twopointfive/wad"
)

// UMAPINFO support — the community per-map metadata lump. When a loaded WAD
// (or a mounted mod) carries one, its entries override the level name,
// music, sky, next-map and par-time the engine otherwise derives from the
// map lump's name. Vanilla IWADs have no UMAPINFO, so nothing changes.
//
// The parser lives in engine/mapinfo (clean-room, from the doomwiki grammar).
// Full ZDoom-style MAPINFO is a different, larger format and is not read.

// loadMapInfo concatenates every UMAPINFO lump from the WAD (file order, so
// a later PWAD layers over the IWAD) then from the mounted mods, and parses
// the result. Returns nil when nothing provides one.
func loadMapInfo(w *wad.WAD, mods *vfs.FS) map[string]*mapinfo.Entry {
	var blob []byte
	count := 0
	add := func(b []byte) {
		blob = append(blob, b...)
		blob = append(blob, '\n')
		count++
	}

	if w != nil {
		for i := range w.Entries {
			if w.Entries[i].Name == "UMAPINFO" {
				add(w.Lump(i))
			}
		}
	}
	if mods != nil {
		for _, b := range mods.Lumps("UMAPINFO") {
			add(b)
		}
	}
	if count == 0 {
		return nil
	}

	m, err := mapinfo.Parse(blob)
	if err != nil {
		log.Printf("umapinfo: %v", err)
	}
	log.Printf("umapinfo: loaded from %d lump(s) — %d map override(s)", count, len(m))
	return m
}

// mapEntry returns the UMAPINFO overrides for map `name` (upper-cased), or
// nil when there are none.
func (g *Game) mapEntry(name string) *mapinfo.Entry {
	if g.mapInfo == nil {
		return nil
	}
	return g.mapInfo[upperMapName(name)]
}

func upperMapName(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'a' && b[i] <= 'z' {
			b[i] -= 32
		}
	}
	return string(b)
}

// levelDisplayName is the name to show for the current level: the UMAPINFO
// levelname if set, else a DEHACKED [STRINGS] level name, else the map lump
// name.
func (g *Game) levelDisplayName() string {
	if g.Level == nil {
		return ""
	}
	if e := g.mapEntry(g.Level.Name); e != nil && e.LevelName != "" {
		return e.LevelName
	}
	if n, ok := g.dehLevelNames[upperMapName(g.Level.Name)]; ok && n != "" {
		return n
	}
	return g.Level.Name
}

// MapMusicLump returns the music lump a UMAPINFO entry names for `mapName`,
// or "" for none. cmd/engine tries this before the conventional name.
func (g *Game) MapMusicLump(mapName string) string {
	if e := g.mapEntry(mapName); e != nil {
		return e.Music
	}
	return ""
}

// ApplyMapInfo pushes the current level's UMAPINFO overrides that the
// renderers own — the sky texture — to the raster path and the hardware
// geometry builder, and caches the par time. Called on every level load
// (NewGame, loadMap) and once by cmd/engine after it wires WorldBuilder.
// Safe to call repeatedly and with no UMAPINFO loaded (clears any override).
func (g *Game) ApplyMapInfo() {
	sky, par := "", 0
	if g.Level != nil {
		if e := g.mapEntry(g.Level.Name); e != nil {
			sky, par = e.SkyTexture, e.ParTime
		}
		if par == 0 {
			if p, ok := g.dehPars[upperMapName(g.Level.Name)]; ok {
				par = p // DEHACKED [PARS] fallback
			}
		}
	}
	g.parTime = par
	if g.Raster != nil {
		g.Raster.SetSkyName(sky)
	}
	if g.WorldBuilder != nil {
		g.WorldBuilder.SetSkyName(sky)
	}
}

func (g *Game) applyMapInfo() { g.ApplyMapInfo() }

