package engine

import "math"

// Fixed-tic render interpolation.
//
// The simulation advances every map object exactly once per 35Hz tic
// (runTic), but renderFrame runs as often as the display allows. Without
// interpolation a walking monster or an enemy fireball visibly steps at
// 35Hz while the player's own camera — settled every frame in
// handleMovement — glides. Each mobj therefore keeps the position/facing it
// had at the start of the current tic (prevX..prevAngle), and the renderer
// draws it part-way between that and its post-tic state, by how far
// wall-clock time has carried into the tic that has not run yet.
//
// This is presentation only. Collision, AI, pickups and the specials all
// read the authoritative post-tic fields (X/Y/Z/Angle); an interpolated
// value never feeds back into the simulation.

// renderLerp is the fraction of a tic that has elapsed since the last one
// ran, 0..1 — g.ticAccum is that leftover real time (see stepSimulation).
// Clamped because a frame that hit maxTicsPerFrame leaves ticAccum above a
// whole tic.
func (g *Game) renderLerp() float64 {
	f := g.ticAccum / ticDuration
	if f <= 0 {
		return 0
	}
	if f >= 1 {
		return 1
	}
	return f
}

// interpTeleportDist2 is the squared single-tic move past which a mobj is
// taken to have teleported (a teleport line, a respawn) rather than moved
// under its own power, and is drawn at its post-tic position with no
// interpolation — lerping a teleport would streak the thing across the map
// for one frame.
const interpTeleportDist2 = 128.0 * 128.0

// renderState returns the position and facing to draw mo at this frame: its
// start-of-tic state advanced toward its current state by frac. A jump too
// large to be ordinary motion snaps straight to the current state.
func (mo *Mobj) renderState(frac float64) (x, y, z, angle float64) {
	dx, dy, dz := mo.X-mo.prevX, mo.Y-mo.prevY, mo.Z-mo.prevZ
	if frac <= 0 || dx*dx+dy*dy > interpTeleportDist2 {
		return mo.X, mo.Y, mo.Z, mo.Angle
	}
	return mo.prevX + dx*frac,
		mo.prevY + dy*frac,
		mo.prevZ + dz*frac,
		mo.prevAngle + shortestAngleDelta(mo.prevAngle, mo.Angle)*frac
}

// --- hardware-path sector-motion interpolation -------------------------

type savedSecH struct {
	idx         int
	floor, ceil int16
}

// snapshotSectorHeights records every sector's current floor/ceiling into
// g.prevSectorH at the top of a tic, before tickSpecials moves any of them.
func (g *Game) snapshotSectorHeights() {
	if g.Level == nil {
		return
	}
	secs := g.Level.Sectors
	if len(g.prevSectorH) != len(secs)*2 {
		g.prevSectorH = make([]int16, len(secs)*2)
	}
	for i := range secs {
		g.prevSectorH[i*2], g.prevSectorH[i*2+1] = secs[i].FloorHeight, secs[i].CeilingHeight
	}
}

// secInterpSnapThreshold: a per-tic height change larger than this is taken
// to be an instant snap (a "change height in zero time" special, a crusher
// bottoming out) rather than a mover gliding, and is shown at its final
// value with no lerp — lerping it would read as a fast slide.
const secInterpSnapThreshold = 48

// applySectorInterp overwrites the moving sectors' heights in g.Level.Sectors
// with their start-of-last-tic value advanced toward the current value by
// renderLerp(), for THIS frame's geometry build only. It returns a closure
// that puts the authoritative heights back; call it as soon as the static
// geometry has been (re)built. Sets g.sectorInterpActive if anything moved.
func (g *Game) applySectorInterp() func() {
	g.sectorInterpActive = false
	frac := g.renderLerp()
	if g.Level == nil {
		return func() {}
	}
	secs := g.Level.Sectors
	if frac <= 0 || frac >= 1 || len(g.prevSectorH) != len(secs)*2 {
		return func() {}
	}
	f32 := float32(frac)
	saved := g.secInterpSaved[:0]
	for i := range secs {
		pf, pc := g.prevSectorH[i*2], g.prevSectorH[i*2+1]
		cf, cc := secs[i].FloorHeight, secs[i].CeilingHeight
		df, dc := int(cf)-int(pf), int(cc)-int(pc)
		if df == 0 && dc == 0 {
			continue
		}
		nf, nc := cf, cc
		if df != 0 && absInt(df) <= secInterpSnapThreshold {
			nf = pf + int16(f32*float32(df))
		}
		if dc != 0 && absInt(dc) <= secInterpSnapThreshold {
			nc = pc + int16(f32*float32(dc))
		}
		if nf == cf && nc == cc {
			continue
		}
		saved = append(saved, savedSecH{i, cf, cc})
		secs[i].FloorHeight, secs[i].CeilingHeight = nf, nc
		g.sectorInterpActive = true
	}
	g.secInterpSaved = saved
	return func() {
		for _, s := range saved {
			g.Level.Sectors[s.idx].FloorHeight = s.floor
			g.Level.Sectors[s.idx].CeilingHeight = s.ceil
		}
	}
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// shortestAngleDelta is the signed rotation from `from` to `to` taking the
// short way round (result in [-pi, pi]) — so interpolating a monster
// turning past the +-pi wrap doesn't spin it the long way.
func shortestAngleDelta(from, to float64) float64 {
	d := math.Mod(to-from, 2*math.Pi)
	if d > math.Pi {
		d -= 2 * math.Pi
	} else if d < -math.Pi {
		d += 2 * math.Pi
	}
	return d
}
