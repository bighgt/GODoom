package engine

import (
	"math"

	"twopointfive/bsp"
	"twopointfive/wad"
)

// Monster route planning for roaming (roam.go). The level's sectors are the
// graph's nodes; two sectors are joined by a portal wherever two-sided
// lines that monsters may cross (not ML_BLOCKING / ML_BLOCKMONSTERS) run
// between them. Whether a particular monster can actually use a portal is
// decided at planning time against the CURRENT sector heights (doors and
// lifts move) and that monster's size — see navCanPass. A door a monster is
// allowed to open (monsterCanOpen) counts as open, since it will open it on
// arrival (pMove -> monsterOpenDoor).
//
// Sector-level routing is coarse on purpose: it gets a monster to the
// right doorway, room after room; the step-by-step walking between
// waypoints is still id's 8-direction P_Move/P_NewChaseDir, which already
// slides around pillars and furniture. A route that turns out blocked
// (another monster in a doorway, a closed lift) is simply re-planned.

// navPortal is the connection between sectors a and b.
type navPortal struct {
	a, b   int
	length float64 // total length of every crossable line between a and b
	mx, my float64 // midpoint of the longest of them — the waypoint
}

// navGraph is built once per level (navGraphFor).
type navGraph struct {
	lvl     *wad.Level
	portals []navPortal
	adj     [][]int        // sector -> indices into portals
	spots   [][][2]float64 // sector -> interior points (subsector centroids)
	door    []bool         // sector is a door monsters may open
}

// navGraphFor returns this level's graph, building it on first use after a
// level (re)load.
func (g *Game) navGraphFor() *navGraph {
	if g.nav != nil && g.nav.lvl == g.Level {
		return g.nav
	}
	g.nav = g.buildNavGraph()
	return g.nav
}

func (g *Game) buildNavGraph() *navGraph {
	lvl := g.Level
	n := len(lvl.Sectors)
	ng := &navGraph{lvl: lvl, adj: make([][]int, n), spots: make([][][2]float64, n), door: make([]bool, n)}

	sectorOf := func(sd uint16) int {
		if sd == wad.NoSidedef || int(sd) >= len(lvl.Sidedefs) {
			return -1
		}
		if s := int(lvl.Sidedefs[sd].Sector); s < n {
			return s
		}
		return -1
	}
	type pairKey struct{ a, b int }
	index := map[pairKey]int{}
	best := map[pairKey]float64{}
	for i := range lvl.Linedefs {
		ld := &lvl.Linedefs[i]
		if monsterCanOpen(ld) {
			if s := g.doorSectorForLine(ld); s >= 0 && s < n {
				ng.door[s] = true
			}
		}
		fa, fb := sectorOf(ld.FrontSidedef), sectorOf(ld.BackSidedef)
		if fa < 0 || fb < 0 || fa == fb || ld.Flags&(mlBlocking|mlBlockMonsters) != 0 {
			continue
		}
		v1, v2 := lvl.Vertexes[ld.StartVertex], lvl.Vertexes[ld.EndVertex]
		l := math.Hypot(float64(v2.X-v1.X), float64(v2.Y-v1.Y))
		k := pairKey{min(fa, fb), max(fa, fb)}
		pi, ok := index[k]
		if !ok {
			pi = len(ng.portals)
			index[k] = pi
			ng.portals = append(ng.portals, navPortal{a: k.a, b: k.b})
			ng.adj[k.a] = append(ng.adj[k.a], pi)
			ng.adj[k.b] = append(ng.adj[k.b], pi)
		}
		p := &ng.portals[pi]
		p.length += l
		if l > best[k] {
			best[k] = l
			p.mx, p.my = (float64(v1.X)+float64(v2.X))/2, (float64(v1.Y)+float64(v2.Y))/2
		}
	}

	// Interior points: each subsector is convex, so the average of its seg
	// endpoints lies inside it.
	for _, ss := range lvl.Subsectors {
		first, cnt := int(ss.FirstSeg), int(ss.SegCount)
		if cnt == 0 || first+cnt > len(lvl.Segs) {
			continue
		}
		sd, _, _ := bsp.SegSides(lvl, lvl.Segs[first])
		if sd == nil || int(sd.Sector) >= n {
			continue
		}
		si := int(sd.Sector)
		var cx, cy float64
		for _, sg := range lvl.Segs[first : first+cnt] {
			v := lvl.Vertexes[sg.StartVertex]
			cx += float64(v.X)
			cy += float64(v.Y)
		}
		ng.spots[si] = append(ng.spots[si], [2]float64{cx / float64(cnt), cy / float64(cnt)})
	}
	return ng
}

// monsterCanOpen reports whether a monster may open the door behind line
// ld by pushing on it — id's rule (P_UseSpecialLine for a non-player): a
// plain manual door that closes again behind it (DR, special 1, and the
// blazing 117). Never a key-locked door, a one-way "open and stay" door,
// a switch- or trigger-operated door, or a line marked secret — so key
// progression and secret areas stay the player's to open.
func monsterCanOpen(ld *wad.Linedef) bool {
	if ld.Flags&mlSecret != 0 {
		return false
	}
	kind, ok := manualDoor[ld.SpecialType]
	if !ok || keyForDoor(ld.SpecialType) >= 0 {
		return false
	}
	return kind == dkOpenWaitClose || kind == dkBlazeRaise
}

// navCanPass reports whether mo could walk from sector from into sector to
// through portal p right now: wide enough, no step up or drop taller than
// it can manage, and enough headroom — a door it may open counting as open
// to its full height.
func (g *Game) navCanPass(mo *Mobj, from, to int, p *navPortal) bool {
	ng := g.nav
	if p.length < 1.6*mo.Radius {
		return false // too narrow for this monster
	}
	floorCeil := func(s int) (float64, float64) {
		sec := &g.Level.Sectors[s]
		f, c := sec.FloorZAt(p.mx, p.my), sec.CeilZAt(p.mx, p.my)
		if ng.door[s] {
			c = math.Max(c, g.findLowestCeilingSurrounding(s)-4)
		}
		return f, c
	}
	ff, fc := floorCeil(from)
	tf, tc := floorCeil(to)
	if mo.Flags&MF_FLOAT == 0 {
		if tf-ff > maxStepUp {
			return false // a ledge too tall to step up
		}
		if ff-tf > maxStepUp && mo.Flags&MF_DROPOFF == 0 {
			return false // won't step off a drop, same as P_TryMove
		}
	}
	return math.Min(fc, tc)-math.Max(ff, tf) >= mo.Height
}
