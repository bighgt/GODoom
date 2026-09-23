package engine

// mobj-vs-mobj spatial index: the counterpart to collisiongrid.go's
// blockGrid (which only ever indexed Level.Linedefs — static geometry,
// built once). pCheckPosition's solid-thing check, projectileHitMobj, and
// missileThingHit each used to do their own full linear scan of g.mobjs —
// O(mobjs) every single call, paid once per moving monster per tic, once
// per in-flight projectile step, and once per in-flight missile step. A
// static build-once grid like blockGrid doesn't fit here (mobjs move every
// tic), so this rebuilds from scratch at the start of every runTic instead
// — still a large win (O(mobjs) once per tic, instead of O(mobjs) paid
// separately by every single query that tic) without the complexity of
// incrementally tracking which cell every moving mobj currently belongs to.
//
// One tic of staleness is accepted, same tradeoff blockGrid's own
// forEachNear/forEachAlongSegment already make ("a superset, not an exact
// query — callers do their own precise check"): a mobj that moves earlier
// in this SAME tic's g.mobjs loop is still bucketed at its pre-tic
// position for the rest of this tic's queries. Every caller still does its
// own precise distance/overlap check against the mobj's real, current X/Y
// on every candidate the grid hands back, so this can never produce a
// false positive — only a rare false negative (a query misses a mobj that
// moved out of the query's search box specifically because the grid hadn't
// caught up yet), which resolves itself on the very next tic's rebuild.

// rebuildMobjGrid re-buckets every live mobj (plus the player, who isn't
// in g.mobjs) by blockGrid's own cell geometry, reusing g.mobjGridCells'
// backing storage across tics rather than reallocating. Called once at the
// top of runTic, before any mobj has moved this tic. No-op if blockGrid
// itself is nil (see its own doc comment: doesn't happen in practice, a
// level with no Linedefs is rejected before this could run).
func (g *Game) rebuildMobjGrid() {
	bg := g.blockGrid
	if bg == nil {
		return
	}
	n := bg.cols * bg.rows
	if len(g.mobjGridCells) != n {
		g.mobjGridCells = make([][]*Mobj, n)
	}
	for i := range g.mobjGridCells {
		g.mobjGridCells[i] = g.mobjGridCells[i][:0]
	}
	g.mobjGridMaxR = 0

	place := func(mo *Mobj) {
		if mo == nil || mo.removed {
			return
		}
		idx := bg.cellY(mo.Y)*bg.cols + bg.cellX(mo.X)
		g.mobjGridCells[idx] = append(g.mobjGridCells[idx], mo)
		if mo.Radius > g.mobjGridMaxR {
			g.mobjGridMaxR = mo.Radius
		}
	}
	place(g.playerMobj)
	for _, mo := range g.mobjs {
		place(mo)
	}
}

// forEachMobjNear calls fn once for every mobj (the player included, if
// live) bucketed near (x, y) — a superset neighbourhood expanded by this
// tic's largest mobj radius (mobjGridMaxR), so nothing whose bounding
// circle could possibly reach a box of half-width radius centred at (x, y)
// is missed regardless of which cell its own centre happens to fall in.
// Every caller still runs its own precise overlap test on each candidate —
// same discipline blockGrid.forEachNear already asks of its own callers.
// Stops early if fn returns false. No-op (calls fn zero times) before the
// first rebuildMobjGrid of a level, or if blockGrid is nil.
func (g *Game) forEachMobjNear(x, y, radius float64, fn func(*Mobj) bool) {
	bg := g.blockGrid
	if bg == nil || g.mobjGridCells == nil {
		return
	}
	expand := radius + g.mobjGridMaxR
	c0, c1 := bg.cellX(x-expand), bg.cellX(x+expand)
	r0, r1 := bg.cellY(y-expand), bg.cellY(y+expand)
	for r := r0; r <= r1; r++ {
		base := r * bg.cols
		for c := c0; c <= c1; c++ {
			for _, mo := range g.mobjGridCells[base+c] {
				if !fn(mo) {
					return
				}
			}
		}
	}
}
