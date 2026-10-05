package engine

import (
	"math"

	"twopointfive/bsp"
	"twopointfive/render"
	"twopointfive/wad"
)

// Dynamic lights don't shine through walls. The hardware shaders light a
// surface only if its sector is in the light's reach set
// (render.Light.Reach), which is the GZDoom approach: flood out from the
// light's own sector through two-sided lines that are within its radius
// and currently open (a closed door, or a step/lintel the light's sphere
// can't see past, stops it). Walls and one-sided lines are never crossed,
// so the room on the far side of a wall stays dark however close the
// light is. Doors are live: the sets are recomputed whenever any sector's
// floor or ceiling moves (sectorHeightEpoch), so light spills through a
// door as it opens.

// lightReachKey identifies one light for the reach cache: its sector and
// its position/radius quantised to lightReachQuantum — a flickering torch
// or burning puddle reuses its entry frame after frame; a moving rocket
// misses each frame (cheap: a few sectors' flood).
type lightReachKey struct {
	sec        int32
	x, y, z, r int32
}

const (
	lightReachQuantum = 4.0
	// lightReachRadiusQuantum buckets the radius (rounded up) coarser than
	// the position: a flickering light's radius wobbles every frame, and a
	// slightly generous flood only admits sectors the falloff barely reaches.
	lightReachRadiusQuantum = 16.0
	// lightReachCacheMax bounds the cache (moving lights add a key per
	// frame); past it the whole cache is dropped and refilled.
	lightReachCacheMax = 512
)

// lightReachCache holds the per-light sector bitsets, valid for one level
// and one sectorHeightEpoch.
type lightReachCache struct {
	level *wad.Level
	epoch uint64
	m     map[lightReachKey][]uint32
	queue []int32
}

// attachLightReach sets Reach on every light from its cached (or freshly
// flooded) sector set. Lights whose position isn't in any sector keep a
// nil Reach (unrestricted).
func (g *Game) attachLightReach(lights []render.Light) {
	if g.Level == nil || g.BSP == nil || len(g.sectorLines) != len(g.Level.Sectors) {
		return
	}
	c := &g.lightReach
	if c.level != g.Level || c.epoch != g.sectorHeightEpoch || len(c.m) > lightReachCacheMax {
		c.level, c.epoch = g.Level, g.sectorHeightEpoch
		c.m = make(map[lightReachKey][]uint32)
	}
	for i := range lights {
		l := &lights[i]
		sec := int32(bsp.PointSectorIndex(g.BSP, g.Level, l.X, l.Y))
		if sec < 0 || int(sec) >= len(g.Level.Sectors) {
			continue
		}
		q := func(v float32) int32 { return int32(math.Floor(float64(v) / lightReachQuantum)) }
		rq := int32(math.Ceil(float64(l.Radius) / lightReachRadiusQuantum))
		key := lightReachKey{sec, q(l.X), q(l.Y), q(l.Z), rq}
		reach, ok := c.m[key]
		if !ok {
			reach = g.floodLightReach(sec, float64(l.X), float64(l.Y), float64(l.Z), float64(rq)*lightReachRadiusQuantum)
			c.m[key] = reach
		}
		l.Reach = reach
	}
}

// floodLightReach returns the bitset of sectors a light at (x, y, z) with
// the given radius reaches from sector start (see the file comment).
func (g *Game) floodLightReach(start int32, x, y, z, radius float64) []uint32 {
	reach := make([]uint32, (len(g.Level.Sectors)+31)/32)
	set := func(s int32) { reach[s>>5] |= 1 << (s & 31) }
	has := func(s int32) bool { return reach[s>>5]&(1<<(s&31)) != 0 }
	set(start)
	queue := append(g.lightReach.queue[:0], start)
	for qi := 0; qi < len(queue); qi++ {
		s := queue[qi]
		for _, li := range g.sectorLines[s] {
			ld := &g.Level.Linedefs[li]
			if ld.BackSidedef == wad.NoSidedef {
				continue
			}
			other := int32(g.Level.Sidedefs[ld.FrontSidedef].Sector)
			if other == s {
				other = int32(g.Level.Sidedefs[ld.BackSidedef].Sector)
			}
			if other < 0 || int(other) >= len(g.Level.Sectors) || has(other) {
				continue
			}
			v1, v2 := g.Level.Vertexes[ld.StartVertex], g.Level.Vertexes[ld.EndVertex]
			if pointSegDist(x, y, float64(v1.X), float64(v1.Y), float64(v2.X), float64(v2.Y)) >= radius {
				continue
			}
			open := g.wallOpeningAt(ld)
			if !open.twoSided || open.top <= open.bottom || open.bottom >= z+radius || open.top <= z-radius {
				continue // closed door, or the opening is out of the light's vertical reach
			}
			set(other)
			queue = append(queue, other)
		}
	}
	g.lightReach.queue = queue
	return reach
}

// pointSegDist is the distance from (px, py) to the segment (ax,ay)-(bx,by).
func pointSegDist(px, py, ax, ay, bx, by float64) float64 {
	dx, dy := bx-ax, by-ay
	t := 0.0
	if l2 := dx*dx + dy*dy; l2 > 0 {
		t = max(0, min(1, ((px-ax)*dx+(py-ay)*dy)/l2))
	}
	return math.Hypot(px-(ax+t*dx), py-(ay+t*dy))
}
