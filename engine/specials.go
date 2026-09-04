package engine

import (
	"math"

	"twopointfive/bsp"
	"twopointfive/wad"
)

// Sector effects, ported from id's p_spec.c / p_doors.c / p_floor.c /
// p_plats.c / p_ceilng.c / p_lights.c. Every active mover or light
// animation is a sectorThinker stepped once per 35Hz tic (tickSpecials,
// from runTic); each writes Level.Sectors[i] heights/light in place — the
// same slice the renderer and collision read, so an opening door is
// immediately visible and walkable. A sector runs at most one height mover
// at a time (sectorActive), mirroring id's sec->specialdata.

type sectorThinker interface {
	tick(g *Game) (done bool)
	sectorIndex() int // -1 for whole-map effects (scrollers)
}

// Speeds are map units per tic (id's *SPEED constants, FRACUNIT-scaled).
const (
	vdoorSpeed  = 2.0
	vdoorSpeedB = 8.0 // blazing
	vdoorWait   = 150 // tics fully open before auto-close
	floorSpeed  = 1.0
	floorSpeedF = 4.0
	ceilSpeed   = 1.0
	ceilSpeedF  = 4.0
	platSpeed   = 4.0
	platWait    = 105
)

func (g *Game) addThinker(t sectorThinker) {
	if s := t.sectorIndex(); s >= 0 {
		if g.sectorActive[s] {
			return // already has a mover
		}
		g.sectorActive[s] = true
	}
	g.thinkers = append(g.thinkers, t)
}

func (g *Game) freeSector(s int) {
	if s >= 0 {
		delete(g.sectorActive, s)
	}
}

// tickSpecials advances every active thinker one tic and compacts finished
// ones. Called from runTic.
func (g *Game) tickSpecials() {
	if len(g.thinkers) == 0 {
		return
	}
	alive := g.thinkers[:0]
	for _, t := range g.thinkers {
		if t.tick(g) {
			g.freeSector(t.sectorIndex())
			continue
		}
		alive = append(alive, t)
	}
	g.thinkers = alive
}

// ---------- doors ----------

type doorKind int

const (
	dkOpenWaitClose doorKind = iota
	dkOpenStay
	dkCloseWaitOpen
	dkClose
	dkBlazeRaise
	dkBlazeOpen
	dkBlazeClose
	dkRaiseIn5Mins
)

type vldoor struct {
	sec      int
	kind     doorKind
	top      float64
	speed    float64
	dir      int // 1 up, -1 down, 0 waiting, 2 initial delay
	count    int // wait countdown
	openSnd  string
	closeSnd string
}

func (d *vldoor) sectorIndex() int { return d.sec }

func (d *vldoor) tick(g *Game) bool {
	s := &g.Level.Sectors[d.sec]
	switch d.dir {
	case 2:
		if d.count--; d.count <= 0 {
			d.dir = 1
			g.playWorldSound(d.openSnd)
		}
	case 0:
		if d.count--; d.count <= 0 {
			d.dir = -1
			g.playWorldSound(d.closeSnd)
		}
	case -1:
		h := float64(s.CeilingHeight) - d.speed
		floor := float64(s.FloorHeight)
		if h <= floor {
			s.CeilingHeight = int16(math.Round(floor))
			if d.kind == dkCloseWaitOpen {
				d.dir = 2
				d.count = 30 * 35
				return false
			}
			return true
		}
		s.CeilingHeight = int16(math.Round(h))
	case 1:
		h := float64(s.CeilingHeight) + d.speed
		if h >= d.top {
			s.CeilingHeight = int16(math.Round(d.top))
			switch d.kind {
			case dkOpenWaitClose, dkBlazeRaise, dkCloseWaitOpen:
				d.dir = 0
				d.count = vdoorWait
				return false
			default: // open-stay
				return true
			}
		}
		s.CeilingHeight = int16(math.Round(h))
	}
	return false
}

// startDoor creates a door thinker on sector sec.
func (g *Game) startDoor(sec int, kind doorKind) {
	speed := vdoorSpeed
	openSnd, closeSnd := "DSDOROPN", "DSDORCLS"
	switch kind {
	case dkBlazeRaise, dkBlazeOpen, dkBlazeClose:
		speed = vdoorSpeedB
		openSnd, closeSnd = "DSBDOPN", "DSBDCLS"
	}
	d := &vldoor{sec: sec, kind: kind, speed: speed, openSnd: openSnd, closeSnd: closeSnd}
	switch kind {
	case dkClose, dkBlazeClose:
		d.dir = -1
		g.playWorldSound(closeSnd)
	case dkCloseWaitOpen:
		d.dir = -1
		g.playWorldSound(closeSnd)
		d.top = float64(g.Level.Sectors[sec].CeilingHeight)
	case dkRaiseIn5Mins:
		d.dir = 2
		d.count = 5 * 60 * 35
		d.top = g.findLowestCeilingSurrounding(sec) - 4
	default:
		d.dir = 1
		d.top = g.findLowestCeilingSurrounding(sec) - 4
		g.playWorldSound(openSnd)
	}
	g.addThinker(d)
}

// ---------- floors ----------

type floorTarget int

const (
	ftLowestFloor floorTarget = iota
	ftHighestFloor
	ft8AboveHighest
	ftLowestCeiling
	ftNextHigher
	ftBy24
	ftBy32
	ftByValue
)

type floorMove struct {
	sec   int
	dir   int // 1 up, -1 down
	speed float64
	dest  float64
}

func (f *floorMove) sectorIndex() int { return f.sec }

func (f *floorMove) tick(g *Game) bool {
	s := &g.Level.Sectors[f.sec]
	h := float64(s.FloorHeight) + float64(f.dir)*f.speed
	if (f.dir > 0 && h >= f.dest) || (f.dir < 0 && h <= f.dest) {
		s.FloorHeight = int16(math.Round(f.dest))
		g.playWorldSound("DSPSTOP")
		return true
	}
	s.FloorHeight = int16(math.Round(h))
	if g.levelTime&7 == 0 {
		g.playWorldSound("DSSTNMOV")
	}
	return false
}

func (g *Game) startFloor(sec int, target floorTarget, fast bool, value float64) {
	s := &g.Level.Sectors[sec]
	cur := float64(s.FloorHeight)
	f := &floorMove{sec: sec, speed: floorSpeed}
	if fast {
		f.speed = floorSpeedF
	}
	switch target {
	case ftLowestFloor:
		f.dest = g.findLowestFloorSurrounding(sec)
		f.dir = -1
	case ftHighestFloor:
		f.dest = g.findHighestFloorSurrounding(sec)
		f.dir = -1
	case ft8AboveHighest:
		f.dest = g.findHighestFloorSurrounding(sec) + 8
		f.dir = -1
		if f.dest > cur {
			f.dest = cur
		}
	case ftLowestCeiling:
		f.dest = g.findLowestCeilingSurrounding(sec)
		f.dir = 1
	case ftNextHigher:
		f.dest = g.findNextHighestFloor(sec, cur)
		f.dir = 1
	case ftBy24:
		f.dest, f.dir = cur+24, 1
	case ftBy32:
		f.dest, f.dir = cur+32, 1
	case ftByValue:
		f.dest = cur + value
		if value < 0 {
			f.dir = -1
		} else {
			f.dir = 1
		}
	}
	g.addThinker(f)
}

// ---------- platforms / lifts ----------

type platKind int

const (
	pkDownWaitUpStay platKind = iota
	pkPerpetualRaise
	pkRaiseToNearestChange
)

type plat struct {
	sec       int
	kind      platKind
	speed     float64
	low, high float64
	dir       int // 1 up, -1 down, 0 waiting
	count     int
}

func (p *plat) sectorIndex() int { return p.sec }

func (p *plat) tick(g *Game) bool {
	s := &g.Level.Sectors[p.sec]
	switch p.dir {
	case 0:
		if p.count--; p.count <= 0 {
			if float64(s.FloorHeight) == p.low {
				p.dir = 1
			} else {
				p.dir = -1
			}
			g.playWorldSound("DSPSTART")
		}
	case -1:
		h := float64(s.FloorHeight) - p.speed
		if h <= p.low {
			s.FloorHeight = int16(math.Round(p.low))
			g.playWorldSound("DSPSTOP")
			if p.kind == pkDownWaitUpStay {
				p.dir = 0
				p.count = platWait
			} else {
				p.dir = 0
				p.count = platWait
			}
			return false
		}
		s.FloorHeight = int16(math.Round(h))
	case 1:
		h := float64(s.FloorHeight) + p.speed
		if h >= p.high {
			s.FloorHeight = int16(math.Round(p.high))
			g.playWorldSound("DSPSTOP")
			if p.kind == pkPerpetualRaise {
				p.dir = 0
				p.count = platWait
				return false
			}
			return true // down-wait-up-stay / raise-change: done at top
		}
		s.FloorHeight = int16(math.Round(h))
	}
	return false
}

func (g *Game) startPlat(sec int, kind platKind, fast bool) {
	s := &g.Level.Sectors[sec]
	cur := float64(s.FloorHeight)
	p := &plat{sec: sec, kind: kind, speed: platSpeed, high: cur, low: cur}
	if fast {
		p.speed = platSpeed * 2
	}
	switch kind {
	case pkDownWaitUpStay:
		p.low = g.findLowestFloorSurrounding(sec)
		if p.low > cur {
			p.low = cur
		}
		p.high = cur
		p.dir = -1
		g.playWorldSound("DSPSTART")
	case pkPerpetualRaise:
		p.low = g.findLowestFloorSurrounding(sec)
		if p.low > cur {
			p.low = cur
		}
		p.high = g.findHighestFloorSurrounding(sec)
		if p.high < cur {
			p.high = cur
		}
		p.dir = -1
		g.playWorldSound("DSPSTART")
	case pkRaiseToNearestChange:
		p.high = g.findNextHighestFloor(sec, cur)
		p.dir = 1
		g.playWorldSound("DSPSTART")
	}
	g.addThinker(p)
}

// stopPlats halts every perpetual platform with the given tag.
func (g *Game) stopPlats(tag uint16) {
	for _, t := range g.thinkers {
		if p, ok := t.(*plat); ok && g.Level.Sectors[p.sec].Tag == tag {
			p.dir = 0
			p.count = math.MaxInt32
		}
	}
}

// ---------- ceilings / crushers ----------

type ceilKind int

const (
	ckLowerToFloor ceilKind = iota
	ckLowerToCrush
	ckCrushAndRaise
)

type ceilMove struct {
	sec   int
	kind  ceilKind
	speed float64
	dir   int
	low   float64
	high  float64
}

func (c *ceilMove) sectorIndex() int { return c.sec }

func (c *ceilMove) tick(g *Game) bool {
	s := &g.Level.Sectors[c.sec]
	h := float64(s.CeilingHeight) + float64(c.dir)*c.speed
	if c.dir < 0 && h <= c.low {
		s.CeilingHeight = int16(math.Round(c.low))
		if c.kind == ckCrushAndRaise {
			c.dir = 1
			return false
		}
		g.playWorldSound("DSPSTOP")
		return true
	}
	if c.dir > 0 && h >= c.high {
		s.CeilingHeight = int16(math.Round(c.high))
		if c.kind == ckCrushAndRaise {
			c.dir = -1
			return false
		}
		return true
	}
	s.CeilingHeight = int16(math.Round(h))
	if g.levelTime&7 == 0 {
		g.playWorldSound("DSSTNMOV")
	}
	// A player caught under the descending ceiling is killed once the gap
	// closes past their height — handled centrally in checkPlayerCrush.
	return false
}

func (g *Game) startCeiling(sec int, kind ceilKind, fast bool) {
	s := &g.Level.Sectors[sec]
	c := &ceilMove{sec: sec, kind: kind, speed: ceilSpeed, high: float64(s.CeilingHeight)}
	if fast {
		c.speed = ceilSpeedF
	}
	switch kind {
	case ckLowerToFloor:
		c.low = float64(s.FloorHeight)
		c.dir = -1
	case ckLowerToCrush, ckCrushAndRaise:
		c.low = float64(s.FloorHeight) + 8
		c.dir = -1
		g.playWorldSound("DSPSTART")
	}
	g.addThinker(c)
}

func (g *Game) stopCeilings(tag uint16) {
	alive := g.thinkers[:0]
	for _, t := range g.thinkers {
		if c, ok := t.(*ceilMove); ok && g.Level.Sectors[c.sec].Tag == tag {
			g.freeSector(c.sec)
			continue
		}
		alive = append(alive, t)
	}
	g.thinkers = alive
}

// ---------- stairs ----------

// buildStairs raises a run of stairs from every sector tagged tag: each
// tagged sector's floor rises by step, then the search walks through
// two-sided lines to the next sector sharing the same floor texture and
// raises it step higher, and so on (id's EV_BuildStairs).
func (g *Game) buildStairs(tag uint16, step float64, fast bool) {
	speed := floorSpeed
	if fast {
		speed = floorSpeedF
	}
	g.ensureSectorIndex()
	for start := range g.Level.Sectors {
		if g.Level.Sectors[start].Tag != tag || g.sectorActive[start] {
			continue
		}
		height := float64(g.Level.Sectors[start].FloorHeight) + step
		g.addThinker(&floorMove{sec: start, dir: 1, speed: speed, dest: height})
		tex := g.Level.Sectors[start].FloorTexture
		cur := start
		for {
			next := -1
			for _, li := range g.sectorLines[cur] {
				ld := &g.Level.Linedefs[li]
				if ld.BackSidedef == wad.NoSidedef {
					continue
				}
				fs := int(g.Level.Sidedefs[ld.FrontSidedef].Sector)
				bs := int(g.Level.Sidedefs[ld.BackSidedef].Sector)
				if fs == cur && g.Level.Sectors[bs].FloorTexture == tex && !g.sectorActive[bs] {
					next = bs
					break
				}
			}
			if next < 0 {
				break
			}
			height += step
			g.addThinker(&floorMove{sec: next, dir: 1, speed: speed, dest: height})
			cur = next
		}
	}
}

// ---------- lights ----------

type lightMode int

const (
	lmFlash lightMode = iota // random flicker between max and min
	lmStrobeSlow
	lmStrobeFast
	lmGlow
	lmFireFlicker
)

type lightThinker struct {
	sec        int
	mode       lightMode
	maxL, minL int16
	count      int
	brightT    int
	darkT      int
	glowUp     bool
}

func (l *lightThinker) sectorIndex() int { return -1 } // lights never block a mover

func (l *lightThinker) tick(g *Game) bool {
	s := &g.Level.Sectors[l.sec]
	switch l.mode {
	case lmFlash:
		if l.count--; l.count <= 0 {
			if s.LightLevel == l.maxL {
				s.LightLevel = l.minL
				l.count = (pRandom() & 7) + 1
			} else {
				s.LightLevel = l.maxL
				l.count = (pRandom() & 31) + 1
			}
		}
	case lmStrobeSlow, lmStrobeFast:
		if l.count--; l.count <= 0 {
			if s.LightLevel == l.maxL {
				s.LightLevel = l.minL
				l.count = l.darkT
			} else {
				s.LightLevel = l.maxL
				l.count = l.brightT
			}
		}
	case lmGlow:
		if l.glowUp {
			s.LightLevel += 8
			if s.LightLevel >= l.maxL {
				s.LightLevel = l.maxL
				l.glowUp = false
			}
		} else {
			s.LightLevel -= 8
			if s.LightLevel <= l.minL {
				s.LightLevel = l.minL
				l.glowUp = true
			}
		}
	case lmFireFlicker:
		if l.count--; l.count <= 0 {
			l.count = 4
			amt := int16((pRandom() & 3) * 16)
			if s.LightLevel-amt < l.minL {
				s.LightLevel = l.minL
			} else {
				s.LightLevel = l.maxL - amt
			}
		}
	}
	return false
}

// minSurroundingLight is id's P_FindMinSurroundingLight.
func (g *Game) minSurroundingLight(sec int, fallback int16) int16 {
	min := fallback
	found := false
	g.forEachAdjacentSector(sec, func(o int) {
		if l := g.Level.Sectors[o].LightLevel; !found || l < min {
			min, found = l, true
		}
	})
	return min
}

func (g *Game) maxSurroundingLight(sec int) int16 {
	var max int16
	g.forEachAdjacentSector(sec, func(o int) {
		if l := g.Level.Sectors[o].LightLevel; l > max {
			max = l
		}
	})
	return max
}

// ---------- scrollers ----------

type scroller struct {
	side int // sidedef index
	dx   int16
}

func (s *scroller) sectorIndex() int { return -1 }

func (s *scroller) tick(g *Game) bool {
	g.Level.Sidedefs[s.side].XOffset += s.dx
	return false
}

// ---------- level-load spawn ----------

// spawnSpecials starts the always-on sector-light thinkers, registers wall
// scrollers, and counts secret sectors — id's P_SpawnSpecials. Called from
// NewGame after the level is loaded.
func (g *Game) spawnSpecials() {
	g.sectorActive = map[int]bool{}
	g.thinkers = g.thinkers[:0]
	g.totalSecrets, g.secretCount = 0, 0
	g.buildSectorIndex()

	for i := range g.Level.Sectors {
		s := &g.Level.Sectors[i]
		switch s.SpecialType {
		case 1:
			g.addThinker(&lightThinker{sec: i, mode: lmFlash, maxL: s.LightLevel,
				minL: g.minSurroundingLight(i, s.LightLevel), count: (pRandom() & 63) + 1})
		case 2:
			g.addThinker(g.newStrobe(i, false, false))
		case 3:
			g.addThinker(g.newStrobe(i, true, false))
		case 4:
			g.addThinker(g.newStrobe(i, false, false))
		case 8:
			g.addThinker(&lightThinker{sec: i, mode: lmGlow, maxL: s.LightLevel,
				minL: g.minSurroundingLight(i, s.LightLevel), glowUp: false})
		case 12:
			g.addThinker(g.newStrobe(i, true, true))
		case 13:
			g.addThinker(g.newStrobe(i, false, true))
		case 17:
			g.addThinker(&lightThinker{sec: i, mode: lmFireFlicker, maxL: s.LightLevel,
				minL: g.minSurroundingLight(i, s.LightLevel) + 16, count: 4})
		case 9:
			g.totalSecrets++
		}
	}

	for i := range g.Level.Linedefs {
		switch g.Level.Linedefs[i].SpecialType {
		case 48: // scroll wall left
			g.thinkers = append(g.thinkers, &scroller{side: int(g.Level.Linedefs[i].FrontSidedef), dx: 1})
		case 85: // scroll wall right
			g.thinkers = append(g.thinkers, &scroller{side: int(g.Level.Linedefs[i].FrontSidedef), dx: -1})
		}
	}

	g.initTexAnims()
}

func (g *Game) newStrobe(sec int, slow, sync bool) *lightThinker {
	s := &g.Level.Sectors[sec]
	l := &lightThinker{sec: sec, mode: lmStrobeFast, maxL: s.LightLevel,
		minL: g.minSurroundingLight(sec, s.LightLevel), brightT: 5, darkT: 15}
	if slow {
		l.mode, l.darkT = lmStrobeSlow, 35
	}
	if l.minL == l.maxL {
		l.minL = 0
	}
	if !sync {
		l.count = (pRandom() & 7) + 1
	} else {
		l.count = 1
	}
	return l
}

// checkPlayerCrush kills the player when the sector they're in has closed
// to less than they are tall — a closing door, a crusher ceiling, or a
// rising floor pinning them against the ceiling. Collision keeps the player
// out of any opening shorter than playerHeight, so the only way to be in
// one is to have had it shut on you. Always lethal, regardless of config
// damageScale. Called from runTic after tickSpecials.
func (g *Game) checkPlayerCrush() {
	if g.playerDead || g.playerMobj == nil {
		return
	}
	sec := bsp.PointSector(g.BSP, g.Level, float32(g.Camera.X), float32(g.Camera.Y))
	if sec == nil {
		return
	}
	if float64(sec.CeilingHeight)-float64(sec.FloorHeight) < playerHeight-1 {
		g.crushPlayer()
	}
}

// playerInSpecialSector applies per-tic damage floors and secret discovery
// for whichever sector the player is standing in — id's
// P_PlayerInSpecialSector. Called from runTic.
func (g *Game) playerInSpecialSector() {
	if g.playerMobj == nil || g.Player.Health <= 0 {
		return
	}
	sec := bsp.PointSector(g.BSP, g.Level, float32(g.Camera.X), float32(g.Camera.Y))
	if sec == nil {
		return
	}
	// Only when actually standing on the floor.
	if g.Camera.Z-EyeHeight > float64(sec.FloorHeight)+1 {
		return
	}
	radsuit := g.Player.Powers[pwIronFeet] > 0 || g.Player.godMode()

	switch sec.SpecialType {
	case 5:
		if !radsuit && g.levelTime&31 == 0 {
			g.pDamageMobj(g.playerMobj, nil, nil, 10)
		}
	case 7:
		if !radsuit && g.levelTime&31 == 0 {
			g.pDamageMobj(g.playerMobj, nil, nil, 5)
		}
	case 16, 4:
		if (!radsuit || pRandom() < 5) && g.levelTime&31 == 0 {
			g.pDamageMobj(g.playerMobj, nil, nil, 20)
		}
	case 9: // secret
		g.secretCount++
		sec.SpecialType = 0
	case 11: // 20% damage, end level at <=10 health
		if g.levelTime&31 == 0 {
			g.pDamageMobj(g.playerMobj, nil, nil, 20)
		}
		if g.Player.Health <= 10 {
			g.exitLevel = 1
		}
	}
}

// ---------- surrounding-sector queries (id's P_Find*) ----------

func (g *Game) forEachAdjacentSector(sec int, fn func(other int)) {
	g.ensureSectorIndex()
	if sec < 0 || sec >= len(g.sectorNeighbors) {
		return
	}
	for _, o := range g.sectorNeighbors[sec] {
		fn(int(o))
	}
}

func (g *Game) findLowestFloorSurrounding(sec int) float64 {
	min := float64(g.Level.Sectors[sec].FloorHeight)
	g.forEachAdjacentSector(sec, func(o int) {
		if h := float64(g.Level.Sectors[o].FloorHeight); h < min {
			min = h
		}
	})
	return min
}

func (g *Game) findHighestFloorSurrounding(sec int) float64 {
	max := -32000.0
	g.forEachAdjacentSector(sec, func(o int) {
		if h := float64(g.Level.Sectors[o].FloorHeight); h > max {
			max = h
		}
	})
	if max == -32000.0 {
		max = float64(g.Level.Sectors[sec].FloorHeight)
	}
	return max
}

func (g *Game) findNextHighestFloor(sec int, cur float64) float64 {
	best := math.MaxFloat64
	found := false
	g.forEachAdjacentSector(sec, func(o int) {
		h := float64(g.Level.Sectors[o].FloorHeight)
		if h > cur && h < best {
			best, found = h, true
		}
	})
	if !found {
		return cur
	}
	return best
}

func (g *Game) findLowestCeilingSurrounding(sec int) float64 {
	min := math.MaxFloat64
	g.forEachAdjacentSector(sec, func(o int) {
		if h := float64(g.Level.Sectors[o].CeilingHeight); h < min {
			min = h
		}
	})
	if min == math.MaxFloat64 {
		return float64(g.Level.Sectors[sec].CeilingHeight)
	}
	return min
}

// findSectorsFromTag returns every sector index whose Tag == tag.
func (g *Game) findSectorsFromTag(tag uint16) []int {
	var out []int
	for i := range g.Level.Sectors {
		if g.Level.Sectors[i].Tag == tag {
			out = append(out, i)
		}
	}
	return out
}
