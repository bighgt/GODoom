package engine

import (
	"twopointfive/bsp"
	"twopointfive/wad"
)

// Sound alerting — id's P_NoiseAlert / P_RecursiveSound (p_enemy.c).
//
// In the original engine a monster's idle A_Look does two things: it reads
// its sector's soundtarget (set by the last nearby gunshot) and, failing
// that, runs P_LookForPlayers, a line-of-sight + front-180-arc test. This
// port had only the second half, so a monster facing away never noticed
// you. This file adds the first half.
//
// P_FireWeapon calls P_NoiseAlert(player, player) on every shot (all
// weapons, initial fire and auto-refire). The alert floods outward from
// the shooter's sector through open two-sided lines, marking each reached
// sector's soundtarget. The flood passes through at most one ML_SOUNDBLOCK
// line (0x40) — a mapper can wall a corridor of sound off with one, or
// seal it with two. A closed door (no vertical opening) stops it dead.
//
// soundtarget persists after the flood (it is not cleared between alerts),
// so one shot in a connected area wakes every monster there on its next
// A_Look tic — the classic "you can't fire without the whole room hearing
// it" behaviour. aLook still requires line of sight for a deaf (MF_AMBUSH)
// monster.

// pNoiseAlert floods a fresh sound alert from emitter's current sector.
// Called from beginFire; a no-op without a built sector index or a
// resolvable emitter position.
func (g *Game) pNoiseAlert(emitter *Mobj) {
	if emitter == nil || g.Level == nil || len(g.soundTarget) == 0 {
		return
	}
	start := bsp.PointSectorIndex(g.BSP, g.Level, float32(emitter.X), float32(emitter.Y))
	g.noiseFloodFrom(start, emitter)
}

// noiseFloodFrom is P_RecursiveSound as an explicit work queue (id
// recurses; a hostile map's sector graph shouldn't be able to blow the Go
// stack). Each queue entry packs sector<<1 | soundblocks (0 or 1).
func (g *Game) noiseFloodFrom(start int, emitter *Mobj) {
	if start < 0 || start >= len(g.soundTarget) {
		return
	}
	g.soundValidCount++
	vc := g.soundValidCount

	q := append(g.soundQueue[:0], start<<1) // soundblocks 0
	for len(q) > 0 {
		item := q[len(q)-1]
		q = q[:len(q)-1]
		sec, blocks := item>>1, item&1

		// id's revisit guard: skip a sector already reached this alert at
		// least this cleanly (fewer or equal sound-blocking lines crossed).
		if g.soundValid[sec] == vc && g.soundTraversed[sec] <= blocks+1 {
			continue
		}
		g.soundValid[sec] = vc
		g.soundTraversed[sec] = blocks + 1
		g.soundTarget[sec] = emitter

		for _, li := range g.sectorLines[sec] {
			ld := &g.Level.Linedefs[li]
			if ld.BackSidedef == wad.NoSidedef {
				continue // one-sided: solid
			}
			if open := g.wallOpeningAt(ld); !open.twoSided || open.top-open.bottom <= 0 {
				continue // closed door / no vertical gap — sound can't pass
			}
			fs, fok := g.sideSector(ld.FrontSidedef)
			bs, bok := g.sideSector(ld.BackSidedef)
			if !fok || !bok {
				continue
			}
			other := bs
			if bs == sec {
				other = fs
			}
			if other == sec || other < 0 || other >= len(g.soundTarget) {
				continue
			}
			next := blocks
			if ld.Flags&wad.LinedefBlockSound != 0 {
				if blocks != 0 {
					continue // already crossed one sound-block line — stop here
				}
				next = 1
			}
			q = append(q, other<<1|next)
		}
	}
	g.soundQueue = q
}

// sectorSoundTarget returns the mobj whose gunfire last reached the sector
// at (x, y), or nil if none ever has. aLook consults it before the
// sight/FOV path, exactly as id's A_Look reads sector->soundtarget.
func (g *Game) sectorSoundTarget(x, y float64) *Mobj {
	if len(g.soundTarget) == 0 || g.Level == nil {
		return nil
	}
	s := bsp.PointSectorIndex(g.BSP, g.Level, float32(x), float32(y))
	if s < 0 || s >= len(g.soundTarget) {
		return nil
	}
	return g.soundTarget[s]
}
