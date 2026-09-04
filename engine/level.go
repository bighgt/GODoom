package engine

import (
	"fmt"
	"log"
	"math"
	"regexp"
	"strconv"

	"twopointfive/bsp"
	"twopointfive/render/worldgeo"
)

var mapNameRe = regexp.MustCompile(`^(?:E(\d)M(\d)|MAP(\d\d))$`)

// nextMapName returns the map to load after finishing cur, given whether a
// secret exit was taken. Falls back to cur if it can't work one out or the
// target isn't in the WAD.
func (g *Game) nextMapName(cur string, secret bool) string {
	// A UMAPINFO `next` / `nextsecret` wins outright (megawads reorder maps,
	// skip to hub levels, etc.). `endgame` -> stay put (no "next").
	if e := g.mapEntry(cur); e != nil {
		if e.EndGame {
			return cur
		}
		want := e.Next
		if secret && e.NextSecret != "" {
			want = e.NextSecret
		}
		if want != "" {
			if g.WAD.IndexOf(want) >= 0 {
				return want
			}
			log.Printf("engine: UMAPINFO next %q for %s not in WAD, using the default progression", want, cur)
		}
	}

	m := mapNameRe.FindStringSubmatch(cur)
	var cand string
	switch {
	case m == nil:
		return cur
	case m[3] != "": // MAPxx
		n, _ := strconv.Atoi(m[3])
		switch {
		case secret && n == 15:
			cand = "MAP31"
		case secret && n == 31:
			cand = "MAP32"
		case n == 31 || n == 32:
			cand = "MAP16"
		default:
			cand = fmt.Sprintf("MAP%02d", n+1)
		}
	default: // ExMy
		e, _ := strconv.Atoi(m[1])
		y, _ := strconv.Atoi(m[2])
		if secret {
			cand = fmt.Sprintf("E%dM9", e)
		} else if y == 9 {
			cand = fmt.Sprintf("E%dM1", e) // no episode-advance logic yet
		} else {
			cand = fmt.Sprintf("E%dM%d", e, y+1)
		}
	}
	if g.WAD.IndexOf(cand) < 0 {
		log.Printf("engine: next map %q not in WAD, staying on %s", cand, cur)
		return cur
	}
	return cand
}

// loadNextMap advances to the map after the current one (nextMapName picks
// the target, honouring a secret exit), then hands off to loadMap.
func (g *Game) loadNextMap() {
	next := g.nextMapName(g.Level.Name, g.exitLevel == 2)
	g.exitLevel = 0
	g.loadMap(next)
}

// restartLevel is the player's Space press on the death screen: reset to a
// fresh-spawn loadout and queue a reload of the current map, which
// stepSimulation performs at a safe point (like a level exit). The cheat
// toggles (god / noclip) are left as they were, same as an idclev warp.
func (g *Game) restartLevel() {
	if g.Level == nil {
		return
	}
	g.Player = DefaultPlayerStats()
	g.Weapon = NewWeaponState()
	g.pendingWarp = g.Level.Name
	log.Printf("engine: restarting %s", g.Level.Name)
}

// loadMap tears down the current level and loads next by name, keeping the
// player's weapons/ammo/health/armour but resetting keys, powers, and all
// map objects — id's G_DoLoadLevel, minus the intermission screen. Used by
// the normal level exit (loadNextMap) and by the idclev warp cheat
// (cheats.go). A missing/broken target is logged and the current level is
// left running.
func (g *Game) loadMap(next string) {
	lvl, err := g.WAD.LoadLevel(next)
	if err != nil {
		log.Printf("engine: load %s: %v (staying put)", next, err)
		return
	}
	tree, err := bsp.Build(lvl)
	if err != nil {
		log.Printf("engine: bsp %s: %v (staying put)", next, err)
		return
	}

	g.Level, g.BSP = lvl, tree
	if g.HardwareMode {
		// Rebuild the GPU-geometry builder for the new level's BSP; the Vulkan
		// backend uploads the new level's textures lazily on the first frame.
		g.WorldBuilder = worldgeo.New(lvl, tree, g.Raster.Textures())
	}
	g.blockGrid = buildCollisionGrid(lvl)
	g.sectorLines, g.sectorNeighbors = nil, nil // rebuilt by spawnSpecials below
	g.soundTarget, g.soundTraversed, g.soundValid = nil, nil, nil
	g.mobjs = g.mobjs[:0]
	g.projectiles = g.projectiles[:0]
	g.levelTime, g.secretCount, g.killCount = 0, 0, 0
	// g.thinkers and g.sectorActive are reset by spawnSpecials, called below.

	// Between levels: keep the arsenal, lose keys and powers.
	for i := range g.Player.Keys {
		g.Player.Keys[i] = false
	}
	for i := range g.Player.Powers {
		g.Player.Powers[i] = 0
	}
	if g.Player.Health <= 0 {
		g.Player.Health = 100
	}
	g.playerDead = false
	g.deadEye = 0

	// Reposition at the new player-1 start.
	const player1Start = 1
	var sx, sy, sang float64
	for _, t := range lvl.Things {
		if t.Type == player1Start {
			sx, sy, sang = float64(t.X), float64(t.Y), float64(t.Angle)*math.Pi/180
			break
		}
	}
	g.Camera.X, g.Camera.Y, g.Camera.Angle, g.Camera.Pitch = sx, sy, sang, 0
	g.Camera.Z = EyeHeight
	if sec := bsp.PointSector(tree, lvl, float32(sx), float32(sy)); sec != nil {
		g.Camera.Z = float64(sec.FloorHeight) + EyeHeight
	}
	g.VelX, g.VelY = 0, 0
	g.groundPlayer(g.Camera.Z - EyeHeight)
	g.playerMobj = &Mobj{
		Type: MT_PLAYER, Info: &mobjInfo[MT_PLAYER], Health: g.Player.Health,
		Flags: mobjInfo[MT_PLAYER].Flags, Radius: mobjInfo[MT_PLAYER].Radius,
		Height: mobjInfo[MT_PLAYER].Height, X: sx, Y: sy, Z: g.Camera.Z - EyeHeight, Angle: sang,
	}

	g.applyMapInfo() // UMAPINFO sky / par-time for the new level (needs g.Level + WorldBuilder set)
	g.spawnSpecials()
	g.spawnMapThings()
	g.buildStaticLights()
	if g.Raster != nil {
		g.Raster.WarmTextureCache(lvl) // prebuild filtered-texture mip chains for the new level
	}

	if dn := g.levelDisplayName(); dn != next {
		log.Printf("engine: entered %s — %q", next, dn)
	} else {
		log.Printf("engine: entered %s", next)
	}
	if g.OnLevelChange != nil {
		g.OnLevelChange(next)
	}
}
