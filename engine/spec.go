package engine

import (
	"log"
	"math"

	"twopointfive/bsp"
	"twopointfive/wad"
)

// Line-special dispatch, ported from id's p_spec.c (P_UseSpecialLine /
// P_CrossSpecialLine / P_ShootSpecialLine). activateLine runs the effect
// for one linedef given how it was triggered; a non-repeatable ("1")
// special clears itself afterwards, a repeatable ("R") one stays.

type lineTrigger int

const (
	trUse lineTrigger = iota // S1 / SR / D1 / DR
	trCross
	trShoot // G1 / GR
)

// makeSet turns a list of special numbers into a lookup set.
func makeSet(nums ...uint16) map[uint16]bool {
	m := make(map[uint16]bool, len(nums))
	for _, n := range nums {
		m[n] = true
	}
	return m
}

// Which specials fire on which trigger (id's p_spec.c switch statements).
// A manual door (see manualDoor) is checked separately.
var (
	walkSpecials = makeSet(2, 3, 4, 5, 6, 7, 8, 10, 12, 13, 16, 17, 19, 22, 25, 30, 35, 36,
		37, 38, 39, 40, 44, 52, 53, 54, 56, 57, 58, 59, 72, 73, 74, 75, 76, 77, 79, 80, 81,
		82, 83, 84, 85, 86, 87, 88, 89, 90, 91, 92, 93, 94, 95, 96, 97, 98, 100, 104, 105,
		106, 107, 109, 110, 120, 121, 124, 125, 126, 128, 129, 130, 141, 197, 198)
	switchSpecials = makeSet(1, 7, 9, 11, 14, 15, 18, 20, 21, 23, 29, 31, 41, 42, 43, 45, 47,
		49, 50, 51, 55, 60, 61, 62, 63, 64, 65, 66, 67, 68, 69, 70, 71, 99, 101, 102, 103,
		111, 112, 113, 114, 115, 116, 117, 118, 122, 123, 127, 131, 132, 133, 134, 135, 136,
		137, 138, 139, 140, 175, 196)
	gunSpecials = makeSet(24, 46, 47)
)

// repeatableSpecials: the SR / WR / GR / DR numbers — these keep working.
var repeatableSpecials = map[uint16]bool{
	26: true, 27: true, 28: true, 42: true, 43: true, 45: true, 46: true, 60: true,
	61: true, 62: true, 63: true, 64: true, 65: true, 66: true, 67: true, 68: true,
	69: true, 70: true, 72: true, 73: true, 74: true, 75: true, 76: true, 77: true,
	79: true, 80: true, 81: true, 82: true, 83: true, 84: true, 86: true, 87: true,
	88: true, 89: true, 90: true, 91: true, 92: true, 93: true, 94: true, 95: true,
	96: true, 97: true, 98: true, 99: true, 105: true, 106: true, 107: true,
	114: true, 115: true, 116: true, 117: true, 120: true, 123: true, 126: true,
	128: true, 129: true, 132: true, 134: true, 136: true, 138: true, 139: true,
	147: true, 148: true,
}

// manualDoor maps a use-activated door special to its door kind.
var manualDoor = map[uint16]doorKind{
	1: dkOpenWaitClose, 26: dkOpenWaitClose, 27: dkOpenWaitClose, 28: dkOpenWaitClose,
	117: dkBlazeRaise,
	31:  dkOpenStay, 32: dkOpenStay, 33: dkOpenStay, 34: dkOpenStay, 118: dkBlazeOpen,
}

// keyForDoor: which player key a locked manual (D1/DR) door needs (index
// into PlayerStats.Keys), or -1. Note id numbered the two families
// inconsistently: DR 26/27/28 = blue/yellow/red, but D1 32/33/34 =
// blue/red/yellow.
func keyForDoor(num uint16) int {
	switch num {
	case 26, 32:
		return 0 // blue
	case 27, 34:
		return 1 // yellow
	case 28, 33:
		return 2 // red
	}
	return -1
}

// keyForSwitchDoor: the key a locked switch-operated (S1/SR) blazing door
// needs — id's 99/133/134/135/136/137, handled via EV_DoLockedDoor.
// 99/133 = blue, 134/135 = red, 136/137 = yellow.
func keyForSwitchDoor(num uint16) int {
	switch num {
	case 99, 133:
		return 0 // blue
	case 134, 135:
		return 2 // red
	case 136, 137:
		return 1 // yellow
	}
	return -1
}

// activateLine performs linedef lineIdx's special, triggered by mo via trig.
// Returns true if it did something (so the caller can flip a switch texture).
func (g *Game) activateLine(lineIdx int, mo *Mobj, trig lineTrigger) bool {
	ld := &g.Level.Linedefs[lineIdx]
	num := ld.SpecialType
	if num == 0 {
		return false
	}
	tag := ld.SectorTag
	byPlayer := mo == g.playerMobj

	// Manual (D1/DR) doors act on the sector behind the used line.
	if kind, ok := manualDoor[num]; ok {
		if trig != trUse {
			return false
		}
		if k := keyForDoor(num); k >= 0 && byPlayer && !g.Player.Keys[k] && !g.Player.Keys[k+3] {
			g.playSound("DSOOF")
			return false
		}
		sec := g.doorSectorForLine(ld)
		if sec < 0 {
			return false
		}
		if g.reopenIfClosing(sec) {
			return true
		}
		g.startDoor(sec, kind)
		return true
	}

	// Trigger-class gate: a switch line ignores a walkover, etc.
	switch trig {
	case trCross:
		if !walkSpecials[num] {
			return false
		}
	case trUse:
		if !switchSpecials[num] {
			return false
		}
	case trShoot:
		if !gunSpecials[num] {
			return false
		}
	}

	did := false
	switch num {
	// ---- remote doors ----
	case 2, 46, 61, 86, 103, 175:
		did = g.doorTagged(tag, dkOpenStay)
	case 3, 42, 50, 75, 107, 110, 113, 116, 196:
		did = g.doorTagged(tag, dkClose)
	case 4, 29, 63, 90:
		did = g.doorTagged(tag, dkOpenWaitClose)
	case 16, 76:
		did = g.doorTagged(tag, dkCloseWaitOpen)
	case 105, 106, 108, 111, 112, 114, 115:
		did = g.doorTagged(tag, dkBlazeRaise)
	case 109:
		did = g.doorTagged(tag, dkBlazeOpen)

	// ---- locked switch doors (EV_DoLockedDoor: key-check, then blaze open) ----
	case 99, 133, 134, 135, 136, 137:
		k := keyForSwitchDoor(num)
		if k >= 0 && byPlayer && !g.Player.Keys[k] && !g.Player.Keys[k+3] {
			g.playSound("DSOOF")
			return false
		}
		did = g.doorTagged(tag, dkBlazeOpen)

	// ---- floors ----
	case 5, 24, 64, 91, 101:
		did = g.floorTagged(tag, ftLowestCeiling, false, 0)
	case 19, 45, 83, 102:
		did = g.floorTagged(tag, ftHighestFloor, false, 0)
	case 9, 23, 37, 38, 60, 82, 84:
		// 9 is really a donut (inner pillar drops to the pool, the ring
		// rises + retextures); lowering the tagged pillar is the visible,
		// gameplay-relevant part.
		did = g.floorTagged(tag, ftLowestFloor, false, 0)
	case 36, 70, 71, 98:
		did = g.floorTagged(tag, ft8AboveHighest, true, 0)
	case 18, 20, 22, 47, 68, 69, 95, 119, 128:
		// 20/47/95 also swap the floor texture in vanilla — not replicated.
		did = g.floorTagged(tag, ftNextHigher, false, 0)
	case 129, 130, 131, 132:
		did = g.floorTagged(tag, ftNextHigher, true, 0)
	case 30, 58, 59, 66, 92, 93:
		// 30 raises by the shortest lower texture on the sector; approximated
		// as +24. 59/93 also retexture.
		did = g.floorTagged(tag, ftBy24, false, 0)
	case 14, 67:
		// 14 (raise 32 + change texture) approximated as a plain +32.
		did = g.floorTagged(tag, ftBy32, false, 0)
	case 140:
		did = g.floorTagged(tag, ftByValue, false, 512)
	case 55, 56, 65, 94:
		did = g.floorTagged(tag, ftLowestCeiling, false, 0) // raise-crush -> approximate

	// ---- lifts / plats ----
	case 10, 21, 62, 88, 122, 123:
		did = g.platTagged(tag, pkDownWaitUpStay, false)
	case 120, 121:
		did = g.platTagged(tag, pkDownWaitUpStay, true)
	case 53, 87:
		did = g.platTagged(tag, pkPerpetualRaise, false)
	case 54, 89:
		g.stopPlats(tag)
		did = true
	case 15, 143: // raise-to-nearest-and-change (approximate)
		did = g.platTagged(tag, pkRaiseToNearestChange, false)

	// ---- ceilings / crushers ----
	case 40:
		did = g.ceilTaggedRaise(tag)
	case 41, 43, 72:
		did = g.ceilTagged(tag, ckLowerToFloor, false)
	case 44:
		did = g.ceilTagged(tag, ckLowerToCrush, false)
	case 6, 25, 49, 73:
		did = g.ceilTagged(tag, ckCrushAndRaise, false)
	case 77, 141:
		did = g.ceilTagged(tag, ckCrushAndRaise, true)
	case 57, 74:
		g.stopCeilings(tag)
		did = true

	// ---- stairs ----
	case 7, 8:
		g.buildStairs(tag, 8, false)
		did = true
	case 100, 127:
		g.buildStairs(tag, 16, true)
		did = true

	// ---- teleport ----
	case 39, 97, 125, 126:
		did = g.evTeleport(lineIdx, mo, num == 125 || num == 126)

	// ---- lights ----
	case 12, 80:
		did = g.lightTagged(tag, g.maxSurroundingLightFor)
	case 13, 81, 138:
		did = g.lightTagged(tag, func(int) int16 { return 255 })
	case 35, 79, 139:
		did = g.lightTagged(tag, func(s int) int16 { return g.minSurroundingLight(s, 0) })
	case 104:
		did = g.lightTagged(tag, func(s int) int16 { return g.minSurroundingLight(s, g.Level.Sectors[s].LightLevel) })
	case 17:
		for _, s := range g.findSectorsFromTag(tag) {
			g.addThinker(g.newStrobe(s, false, false))
		}
		did = true

	// ---- exits ----
	case 11, 52, 197:
		g.exitLevel = 1
		did = true
	case 51, 124, 198:
		g.exitLevel = 2
		did = true

	default:
		log.Printf("engine: unhandled line special %d", num)
		return false
	}

	if !did {
		return false
	}
	if !repeatableSpecials[num] {
		ld.SpecialType = 0
	}
	return true
}

// --- tag-scoped helpers ---

func (g *Game) doorTagged(tag uint16, kind doorKind) bool {
	did := false
	for _, s := range g.findSectorsFromTag(tag) {
		if g.sectorActive[s] {
			continue
		}
		g.startDoor(s, kind)
		did = true
	}
	return did
}

func (g *Game) floorTagged(tag uint16, target floorTarget, fast bool, val float64) bool {
	did := false
	for _, s := range g.findSectorsFromTag(tag) {
		if g.sectorActive[s] {
			continue
		}
		g.startFloor(s, target, fast, val)
		did = true
	}
	return did
}

func (g *Game) platTagged(tag uint16, kind platKind, fast bool) bool {
	did := false
	for _, s := range g.findSectorsFromTag(tag) {
		if g.sectorActive[s] {
			continue
		}
		g.startPlat(s, kind, fast)
		did = true
	}
	return did
}

func (g *Game) ceilTagged(tag uint16, kind ceilKind, fast bool) bool {
	did := false
	for _, s := range g.findSectorsFromTag(tag) {
		if g.sectorActive[s] {
			continue
		}
		g.startCeiling(s, kind, fast)
		did = true
	}
	return did
}

func (g *Game) ceilTaggedRaise(tag uint16) bool {
	did := false
	for _, s := range g.findSectorsFromTag(tag) {
		if g.sectorActive[s] {
			continue
		}
		sec := &g.Level.Sectors[s]
		dest := g.maxSurroundingCeiling(s)
		g.addThinker(&ceilMove{sec: s, kind: ckLowerToFloor, speed: ceilSpeed, dir: 1, high: dest, low: float64(sec.FloorHeight)})
		did = true
	}
	return did
}

func (g *Game) lightTagged(tag uint16, level func(sec int) int16) bool {
	did := false
	for _, s := range g.findSectorsFromTag(tag) {
		g.Level.Sectors[s].LightLevel = level(s)
		did = true
	}
	return did
}

func (g *Game) maxSurroundingLightFor(sec int) int16 { return g.maxSurroundingLight(sec) }

func (g *Game) maxSurroundingCeiling(sec int) float64 {
	max := float64(g.Level.Sectors[sec].CeilingHeight)
	g.forEachAdjacentSector(sec, func(o int) {
		if h := float64(g.Level.Sectors[o].CeilingHeight); h > max {
			max = h
		}
	})
	return max
}

// --- manual door helpers (kept from the old doors.go) ---

// doorSectorForLine picks the sector on the far side of a used manual-door
// line — id's EV_VerticalDoor: `sides[line->sidenum[side^1]].sector`, where
// `side` is which side of the LINE the user stands on. Keyed on the line,
// not on which sector the player is located in: a thin threshold/step
// sector (or just standing a pace back) can put the player in a sector
// that is neither of the line's two, which the old point-location check got
// wrong — it then opened the corridor's own ceiling instead of the door,
// so the exit never opened.
func (g *Game) doorSectorForLine(ld *wad.Linedef) int {
	if ld.BackSidedef == wad.NoSidedef {
		return -1
	}
	// id's EV_VerticalDoor: a manual (D1/DR) door ALWAYS acts on the sector
	// behind the line — sides[line->sidenum[1]].sector. Doom's own maps wind
	// a door line so its back sidedef faces the door sector, and a two-
	// linedef door (openable from both sides) gives both lines the SAME
	// back sector, so this is right from either approach. An earlier
	// player-side heuristic here opened the FRONT sector for some windings
	// (e.g. E1M1's last door, lines 324/325) and the door never moved.
	return int(g.Level.Sidedefs[ld.BackSidedef].Sector)
}

// reopenIfClosing: if a door on sec is mid-close or waiting, send it back
// up (or refresh its wait) instead of starting a second thinker.
func (g *Game) reopenIfClosing(sec int) bool {
	for _, t := range g.thinkers {
		d, ok := t.(*vldoor)
		if !ok || d.sec != sec {
			continue
		}
		switch d.dir {
		case -1:
			d.dir = 1
			g.playWorldSound(d.openSnd)
		case 0:
			d.count = vdoorWait
		}
		return true
	}
	return false
}

// evTeleport moves mo to the teleport landing (an MT_TELEPORTMAN) in a
// sector tagged the same as the line, spawning fog and playing the sound.
func (g *Game) evTeleport(lineIdx int, mo *Mobj, monsterOnly bool) bool {
	if mo == nil || (monsterOnly && mo == g.playerMobj) {
		return false
	}
	tag := g.Level.Linedefs[lineIdx].SectorTag
	tags := map[int]bool{}
	for _, s := range g.findSectorsFromTag(tag) {
		tags[s] = true
	}
	for _, dest := range g.mobjs {
		if dest.Type != MT_TELEPORTMAN {
			continue
		}
		ds := bsp.PointSector(g.BSP, g.Level, float32(dest.X), float32(dest.Y))
		if ds == nil {
			continue
		}
		idx := -1
		for i := range g.Level.Sectors {
			if &g.Level.Sectors[i] == ds {
				idx = i
				break
			}
		}
		if idx < 0 || !tags[idx] {
			continue
		}
		g.P_SpawnMobj(mo.X, mo.Y, mo.Z, MT_TFOG)
		if mo == g.playerMobj {
			g.Camera.X, g.Camera.Y = dest.X, dest.Y
			g.Camera.Angle = dest.Angle
			g.VelX, g.VelY = 0, 0
			if sec := bsp.PointSector(g.BSP, g.Level, float32(dest.X), float32(dest.Y)); sec != nil {
				g.Camera.Z = float64(sec.FloorHeight) + EyeHeight
			}
			g.groundPlayer(g.Camera.Z - EyeHeight)
		} else {
			mo.X, mo.Y, mo.Angle = dest.X, dest.Y, dest.Angle
			mo.Z = float64(ds.FloorHeight)
			mo.MomX, mo.MomY, mo.MomZ = 0, 0, 0
		}
		g.P_SpawnMobj(dest.X, dest.Y, float64(ds.FloorHeight), MT_TFOG)
		g.playWorldSound("DSTELEPT")
		return true
	}
	return false
}

// --- trigger entry points ---

// tryUseLine is the "use" (Space) action: raycast from the camera and
// activate the nearest special linedef within reach, flipping its switch
// texture if it did something.
func (g *Game) tryUseLine() {
	ux, uy := math.Cos(g.Camera.Angle), math.Sin(g.Camera.Angle)
	best, bestDist := -1, math.Inf(1)
	for i := range g.Level.Linedefs {
		if g.Level.Linedefs[i].SpecialType == 0 {
			continue
		}
		v1 := g.Level.Vertexes[g.Level.Linedefs[i].StartVertex]
		v2 := g.Level.Vertexes[g.Level.Linedefs[i].EndVertex]
		if d, ok := rayIntersectsSegment(g.Camera.X, g.Camera.Y, ux, uy,
			float64(v1.X), float64(v1.Y), float64(v2.X), float64(v2.Y)); ok && d <= useRange && d < bestDist {
			best, bestDist = i, d
		}
	}
	if best < 0 {
		return
	}
	if g.activateLine(best, g.playerMobj, trUse) {
		g.changeSwitchTexture(best, repeatableSpecials[g.Level.Linedefs[best].SpecialType] ||
			manualDoor[g.Level.Linedefs[best].SpecialType] != 0)
	}
}

// crossedLine fires a walkover (W1/WR) special when mo steps across linedef
// lineIdx. Called from stepMove for the player.
func (g *Game) crossedLine(lineIdx int, mo *Mobj) {
	g.activateLine(lineIdx, mo, trCross)
}

// checkCrossedSpecials fires the walkover special of every special linedef
// the segment (ox,oy)->(nx,ny) crossed this move.
func (g *Game) checkCrossedSpecials(ox, oy, nx, ny float64) {
	// forEachAlongSegment can hand the same line back more than once; a
	// walkover special must fire at most once per move, so track the ones
	// already triggered (almost always zero or one).
	var fired []int32
	g.blockGrid.forEachAlongSegment(ox, oy, nx, ny, func(i int32) bool {
		ld := &g.Level.Linedefs[i]
		if ld.SpecialType == 0 {
			return true
		}
		v1 := g.Level.Vertexes[ld.StartVertex]
		v2 := g.Level.Vertexes[ld.EndVertex]
		if hit, _ := segCross(ox, oy, nx, ny, float64(v1.X), float64(v1.Y), float64(v2.X), float64(v2.Y)); hit {
			for _, f := range fired {
				if f == i {
					return true
				}
			}
			fired = append(fired, i)
			g.crossedLine(int(i), g.playerMobj)
		}
		return true
	})
}

// shotLine fires a gun (G1/GR) special when a hitscan hits linedef lineIdx.
func (g *Game) shotLine(lineIdx int, mo *Mobj) {
	if g.activateLine(lineIdx, mo, trShoot) {
		g.changeSwitchTexture(lineIdx, repeatableSpecials[g.Level.Linedefs[lineIdx].SpecialType])
	}
}
