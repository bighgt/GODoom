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
// start-of-tic state advanced toward its current state by frac, plus
// whatever is left of a walking monster's step glide. A jump too large to be
// ordinary motion snaps straight to the current state.
func (mo *Mobj) renderState(frac float64) (x, y, z, angle float64) {
	dx, dy, dz := mo.X-mo.prevX, mo.Y-mo.prevY, mo.Z-mo.prevZ
	if dx*dx+dy*dy > interpTeleportDist2 {
		return mo.X, mo.Y, mo.Z, mo.Angle
	}
	if frac <= 0 {
		x, y, z, angle = mo.X, mo.Y, mo.Z, mo.Angle
	} else {
		x, y, z = mo.prevX+dx*frac, mo.prevY+dy*frac, mo.prevZ+dz*frac
		angle = mo.prevAngle + shortestAngleDelta(mo.prevAngle, mo.Angle)*frac
	}
	if mo.glideTics > 0 {
		if k := 1 - (float64(mo.glideAge)+frac)/float64(mo.glideTics); k > 0 {
			x += mo.glideX * k
			y += mo.glideY * k
			z += mo.glideZ * k
			angle += mo.glideAng * k
		}
	}
	return x, y, z, angle
}

// --- walking-monster step glide ----------------------------------------
//
// The per-tic lerp above can't smooth a walking monster. id's A_Chase moves
// it its whole Speed (8-20 units) in ONE tic and then leaves it standing for
// the rest of its walk frame (3-4 tics), so even fully interpolated it goes
// step-pause-step — the familiar Doom "hop". ZDoom's renderer has the same
// limit: its interpolation (Prev -> current by the tic fraction) is also
// one tic wide, and the smoothness people know from GZDoom mods (Smooth
// Doom) comes from re-timing the actor so A_Chase runs every tic with a
// fraction of the speed.
//
// Re-timing changes the simulation (collision, door pushes, attack odds per
// tic), so this engine does the presentation-side equivalent: when the AI
// takes a step, the step is folded into the start-of-tic snapshot and kept
// as an offset (glideX..) that decays linearly to zero over the frame's
// tics. The sprite therefore walks continuously and arrives where the
// simulation put it exactly as the next A_Chase fires; it trails the real
// position by at most one step meanwhile. The 45° chase turn glides the same
// way. Gated by config smoothMonsterMovement.

// maxStepGlideTics caps the glide so a long-tic walk frame (a slow DEHACKED
// state) doesn't leave the sprite visibly far behind the real monster.
const maxStepGlideTics = 8

// beginStepGlide spreads an instant move of (dx, dy, dz) and turn of dang —
// which the simulation has ALREADY applied to mo this tic — over the tics
// until mo's next state action, i.e. its next A_Chase step. Any glide still
// in progress carries over, so back-to-back steps stay continuous.
func (g *Game) beginStepGlide(mo *Mobj, dx, dy, dz, dang float64) {
	if !g.smoothMonsterMovement {
		return
	}
	n := mo.Tics
	if mo.hact != nil {
		n = mo.hFrameTic
	}
	if n <= 1 {
		return // a 1-tic frame: the ordinary per-tic lerp already covers it
	}
	n = min(n, maxStepGlideTics)
	rem := 0.0 // unshown fraction of the previous glide, as of this tic's start
	if mo.glideTics > 0 {
		rem = 1 - float64(mo.glideAge)/float64(mo.glideTics)
	}
	mo.glideX = mo.glideX*rem - dx
	mo.glideY = mo.glideY*rem - dy
	mo.glideZ = mo.glideZ*rem - dz
	mo.glideAng = mo.glideAng*rem - dang
	mo.glideTics, mo.glideAge = n, 0
	// The step is now the glide's to show; keep the per-tic lerp from also
	// showing it.
	mo.prevX += dx
	mo.prevY += dy
	mo.prevZ += dz
	mo.prevAngle += dang
}

// ageStepGlide advances mo's glide by one tic; called with the start-of-tic
// snapshot in mobjTick.
func (mo *Mobj) ageStepGlide() {
	if mo.glideTics == 0 {
		return
	}
	if mo.glideAge++; mo.glideAge >= mo.glideTics {
		mo.clearStepGlide()
	}
}

// clearStepGlide drops any in-progress glide so mo is drawn exactly where it
// is — after a teleport, or when its facing snaps (aFaceTarget).
func (mo *Mobj) clearStepGlide() {
	mo.glideX, mo.glideY, mo.glideZ, mo.glideAng = 0, 0, 0, 0
	mo.glideTics, mo.glideAge = 0, 0
}

// chaseTurn is turnToMoveDir with the 45° turn glided over the walk frame
// rather than snapped in a single tic.
func (g *Game) chaseTurn(mo *Mobj) {
	a0 := mo.Angle
	turnToMoveDir(mo)
	if d := shortestAngleDelta(a0, mo.Angle); d != 0 {
		g.beginStepGlide(mo, 0, 0, 0, d)
	}
}

// --- hardware-path sector-motion interpolation -------------------------

type savedSecH struct {
	idx         int
	floor, ceil int16
}

// snapshotSectorHeights records every sector's current floor/ceiling into
// g.prevSectorH (and light into prevSectorLight, for trackLiveSectors) at
// the top of a tic, before tickSpecials changes any of them.
func (g *Game) snapshotSectorHeights() {
	if g.Level == nil {
		return
	}
	secs := g.Level.Sectors
	if len(g.prevSectorH) != len(secs)*2 {
		g.prevSectorH = make([]int16, len(secs)*2)
		g.prevSectorLight = make([]int16, len(secs))
	}
	for i := range secs {
		g.prevSectorH[i*2], g.prevSectorH[i*2+1] = secs[i].FloorHeight, secs[i].CeilingHeight
		g.prevSectorLight[i] = secs[i].LightLevel
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
// geometry has been built (Build emits the moving sectors from them).
func (g *Game) applySectorInterp() func() {
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
