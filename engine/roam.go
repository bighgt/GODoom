package engine

import (
	"math"

	"twopointfive/bsp"
)

// Roaming: a monster that hasn't noticed the player doesn't stand still.
// Every monster that can move (canRoam) spends its idle time walking to
// random places it can reach anywhere in the level — room to room, opening
// normal doors on the way (monsterCanOpen) — pausing a few seconds between
// trips. It keeps listening and looking exactly as an idle monster does
// (the same heardShot / pLookForPlayers checks as aLook), and the moment it
// notices the player it drops the trip and chases. A monster that loses its
// target goes back to idle, and so, after a pause, back to roaming.
//
// Ambush ("deaf") monsters roam too; they stay deaf — a gunshot still only
// wakes one that can see the shooter.
//
// While roaming the monster is in its see (walking) state with no Target:
// aChase's first check hands it to roamChase.

const (
	roamFirstPauseMin = 1 * 35 // tics before a monster's first trip...
	roamFirstPauseMax = 6 * 35 // ...spread so a level doesn't set off in lockstep
	roamPauseMin      = 2 * 35 // idle pause between trips
	roamPauseMax      = 7 * 35
	roamArriveDist    = 24.0 // counts as at a waypoint within radius + this
	roamStuckLimit    = 24   // chase calls without getting closer -> re-plan
	roamMaxReplans    = 3    // failed routes in a row before resting instead
)

// roamRoute is one trip: waypoints (a doorway midpoint per sector boundary
// crossed, then the destination) and progress bookkeeping.
type roamRoute struct {
	pts     [][2]float64
	idx     int
	best    float64 // closest distance to pts[idx] so far
	stuck   int
	replans int
}

// canRoam reports whether mo is a monster that can walk about: a live
// monster with a walking state and a non-zero speed. Stationary things
// (Commander Keen, the Icon of Sin's brain) never roam.
func canRoam(mo *Mobj) bool {
	return mo.Info != nil && mo.Health > 0 && mo.Flags&MF_COUNTKILL != 0 &&
		mo.Info.Speed > 0 && mo.Info.SeeState != S_NULL && mo.Info.SeeState != mo.Info.SpawnState
}

// idleRoam is aLook's fallback when the idle monster noticed nothing: once
// its pause is over, start a trip (the walking state, no target).
func (g *Game) idleRoam(mo *Mobj) {
	if !g.monsterRoaming || !canRoam(mo) { // classic: wait where placed
		return
	}
	if mo.roamReadyAt == 0 {
		mo.roamReadyAt = g.levelTime + roamFirstPauseMin + prng.Intn(roamFirstPauseMax-roamFirstPauseMin+1)
	}
	if g.levelTime < mo.roamReadyAt {
		return
	}
	mo.Target = nil
	mo.roaming = true
	mo.roam = nil
	g.setMobjState(mo, mo.Info.SeeState)
}

// roamChase is aChase for a roaming monster: notice the player the way an
// idle one would, else take one step of the trip. Ending the trip (arrived,
// or no route) returns the monster to idle for a pause.
func (g *Game) roamChase(mo *Mobj) {
	woke := g.heardShot(mo, g.sectorSoundTarget(mo.X, mo.Y))
	if woke || g.pLookForPlayers(mo, false) {
		mo.roaming, mo.roam = false, nil
		if s := mo.Info.SeeSound; s != "" {
			g.playSound(s)
		}
		return // chasing from the next call on; mo.Target is set
	}
	mo.Target = nil // heardShot aims at the shooter even when a deaf monster didn't wake

	g.chaseTurn(mo)
	if !g.roamStep(mo) {
		g.stopRoaming(mo)
		return
	}
	if s := mo.Info.ActiveSound; s != "" && pRandom() < 3 {
		g.playSound(s)
	}
}

// stopRoaming ends the current trip and idles mo for a pause.
func (g *Game) stopRoaming(mo *Mobj) {
	mo.roaming, mo.roam = false, nil
	mo.roamReadyAt = g.levelTime + roamPauseMin + prng.Intn(roamPauseMax-roamPauseMin+1)
	g.setMobjState(mo, mo.Info.SpawnState)
}

// roamStep advances mo one step along its trip, planning one first if it
// has none. Reports false when the trip is over (arrived, or no route).
func (g *Game) roamStep(mo *Mobj) bool {
	r := mo.roam
	if r == nil {
		if r = g.planRoam(mo); r == nil {
			return false
		}
		mo.roam = r
	}
	wp := r.pts[r.idx]
	d := math.Hypot(wp[0]-mo.X, wp[1]-mo.Y)
	if d <= mo.Radius+roamArriveDist {
		r.idx++
		if r.idx >= len(r.pts) {
			return false // arrived
		}
		r.best, r.stuck = math.Inf(1), 0
		mo.MoveCount = 0 // head for the next waypoint straight away
		wp = r.pts[r.idx]
		d = math.Hypot(wp[0]-mo.X, wp[1]-mo.Y)
	}
	if d < r.best-0.5 {
		r.best, r.stuck = d, 0
	} else if r.stuck++; r.stuck > roamStuckLimit {
		// Blocked (a monster in the doorway, a lift that's up): try another
		// destination, a few times, before resting.
		replans := r.replans + 1
		if replans > roamMaxReplans {
			return false
		}
		if mo.roam = g.planRoam(mo); mo.roam == nil {
			return false
		}
		mo.roam.replans = replans
		return true
	}
	if mo.MoveCount--; mo.MoveCount < 0 || !g.pMove(mo) {
		g.pNewChaseDirTo(mo, wp[0], wp[1])
		mo.MoveCount = min(mo.MoveCount, 4) // re-aim often: waypoints are close together
	}
	return true
}

// planRoam picks a random sector mo can reach from where it stands and
// routes there — a breadth-first search over the level's portals that mo
// can pass right now (navCanPass). nil if nowhere is reachable.
func (g *Game) planRoam(mo *Mobj) *roamRoute {
	ng := g.navGraphFor()
	start := bsp.PointSectorIndex(g.BSP, g.Level, float32(mo.X), float32(mo.Y))
	if start < 0 || start >= len(ng.adj) {
		return nil
	}
	type hop struct{ from, portal int }
	parent := map[int]hop{start: {-1, -1}}
	queue := []int{start}
	var reached []int
	for len(queue) > 0 {
		s := queue[0]
		queue = queue[1:]
		for _, pi := range ng.adj[s] {
			p := &ng.portals[pi]
			to := p.b
			if to == s {
				to = p.a
			}
			if _, seen := parent[to]; seen || !g.navCanPass(mo, s, to, p) {
				continue
			}
			parent[to] = hop{s, pi}
			queue = append(queue, to)
			if len(ng.spots[to]) > 0 {
				reached = append(reached, to)
			}
		}
	}
	if len(reached) == 0 {
		return nil
	}
	goal := reached[prng.Intn(len(reached))]
	spot := ng.spots[goal][prng.Intn(len(ng.spots[goal]))]

	var rev [][2]float64
	rev = append(rev, spot)
	for s := goal; parent[s].portal >= 0; s = parent[s].from {
		p := &ng.portals[parent[s].portal]
		rev = append(rev, [2]float64{p.mx, p.my})
	}
	pts := make([][2]float64, len(rev))
	for i := range rev {
		pts[i] = rev[len(rev)-1-i]
	}
	return &roamRoute{pts: pts, best: math.Inf(1)}
}

// monsterOpenDoor is the door half of id's P_Move: a monster whose step was
// blocked pushes on any door line it's touching that monsters may open
// (monsterCanOpen), exactly as the player's Use would — the door opens
// (or, if it was closing, reopens) and the monster waits for it. Reports
// whether it opened (or is already opening) one.
func (g *Game) monsterOpenDoor(mo *Mobj, x, y float64) bool {
	opened := false
	g.blockGrid.forEachNear(x, y, mo.Radius, func(i int32) bool {
		ld := &g.Level.Linedefs[i]
		if !monsterCanOpen(ld) {
			return true
		}
		v1, v2 := g.Level.Vertexes[ld.StartVertex], g.Level.Vertexes[ld.EndVertex]
		if distancePointToSegment(x, y, float64(v1.X), float64(v1.Y), float64(v2.X), float64(v2.Y)) >= mo.Radius+1 {
			return true
		}
		if g.activateLine(int(i), mo, trUse) {
			opened = true
			return false
		}
		return true
	})
	return opened
}
