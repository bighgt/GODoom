package engine

// Keeping the hardware renderers' static level mesh (worldgeo.Builder) in
// step with the simulation, cheaply.
//
// The static mesh — every wall and flat in the level — lives in one GPU
// buffer, and rebuilding it means re-emitting the whole level and a
// blocking re-upload. Heights, lights and wall offsets are baked into it,
// so nothing that changes those often goes through a rebuild:
//
//   - Live sectors: a sector joins the live set the tic its floor, ceiling
//     or light changes (trackLiveSectors) or a wall scroller on it ticks
//     (markLive), and leaves after liveHoldTics without changing. The
//     builder draws the set from the per-frame stream from the sectors'
//     current state (Builder.SetLiveSectors) — doors and lifts at their
//     interpolated heights, flickering lights at full rate — so a door
//     costs two rebuilds (as it starts, once it has settled) instead of
//     one per drawn height change, and a strobe costs none.
//   - Animated flats and walls stepping a frame only repoint the draws'
//     textures (Builder.Retexture) — the geometry doesn't change.
//
// What remains are rare one-off rebuilds: a height changing outside the
// live set, a switch flipping (geomDirty), a new level.

// liveHoldTics is how long a sector stays live after its last change: long
// enough to span a normal door's open-wait-close cycle (150-tic wait)
// without leaving and re-entering the set, short enough that a finished
// lift soon goes back into the static mesh.
const liveHoldTics = 5 * ticsPerSecond

// trackLiveSectors updates the live set from this tic's changes — every
// sector whose floor, ceiling or light differs from its start-of-tic
// snapshot is (re)armed for liveHoldTics — and counts the rest down.
// Called at the end of runTic.
func (g *Game) trackLiveSectors() {
	if g.Level == nil {
		return
	}
	secs := g.Level.Sectors
	if g.liveLevel != g.Level || len(g.liveHold) != len(secs) {
		g.liveLevel = g.Level
		g.liveHold = make([]int16, len(secs))
		g.liveJoined = true
		// Sectors that will change all level long join together now,
		// rather than one static rebuild each as their first change comes.
		for _, th := range g.thinkers {
			switch t := th.(type) {
			case *lightThinker:
				g.liveHold[t.sec] = liveHoldTics
			case *scroller:
				g.liveHold[g.Level.Sidedefs[t.side].Sector] = liveHoldTics
			}
		}
	}
	changed := g.liveJoined
	g.liveJoined = false
	snap := len(g.prevSectorH) == len(secs)*2
	moved := false
	for i := range secs {
		h := g.liveHold[i]
		if snap && (secs[i].FloorHeight != g.prevSectorH[i*2] || secs[i].CeilingHeight != g.prevSectorH[i*2+1]) {
			moved = true
		}
		switch {
		case snap && (secs[i].FloorHeight != g.prevSectorH[i*2] || secs[i].CeilingHeight != g.prevSectorH[i*2+1] ||
			secs[i].LightLevel != g.prevSectorLight[i]):
			changed = changed || h == 0
			g.liveHold[i] = liveHoldTics
		case h > 0:
			g.liveHold[i] = h - 1
			changed = changed || h == 1
		}
	}
	if moved {
		g.sectorHeightEpoch++ // lightreach.go re-floods
	}
	if !changed {
		return
	}
	g.liveList = g.liveList[:0]
	for i, h := range g.liveHold {
		if h > 0 {
			g.liveList = append(g.liveList, i)
		}
	}
	g.liveDirty = true
}

// markLive (re)arms sector sec in the live set — for changes the per-tic
// snapshot doesn't see, such as a wall scroller moving a sidedef's offset.
func (g *Game) markLive(sec int) {
	if sec < 0 || sec >= len(g.liveHold) {
		return
	}
	if g.liveHold[sec] == 0 {
		g.liveJoined = true
	}
	g.liveHold[sec] = liveHoldTics + 1 // trackLiveSectors counts this tic off
}

// isLiveSector reports whether sector i is in the live set.
func (g *Game) isLiveSector(i int) bool { return i < len(g.liveHold) && g.liveHold[i] > 0 }

// syncWorldGeometry brings WorldBuilder up to date for this frame — the
// live set, a static rebuild only if something baked into the mesh
// changed, animated textures — and applies the moving sectors' sub-tic
// heights (applySectorInterp). Call WorldBuilder.Build next (it emits the
// live sectors at those heights), then the returned restore before
// anything else reads sector heights. Shared by both hardware front ends
// (buildWorldFrame, SyncIsoWorldGeometry).
func (g *Game) syncWorldGeometry() (restore func()) {
	b := g.WorldBuilder
	resync := b != g.geomBuilder // a new level / builder, or new animations
	if resync {
		g.geomBuilder = b
		g.liveDirty = true
		g.geomHeights = nil
		g.resetAnimShown()
	}
	rebuilt := false
	if g.liveDirty {
		g.liveDirty = false
		rebuilt = b.SetLiveSectors(g.liveList)
	}
	geomChanged := g.worldGeomChanged()
	animRebuild := g.syncAnimTextures(rebuilt || resync || geomChanged || g.geomDirty)
	if !rebuilt && (resync || geomChanged || g.geomDirty || animRebuild) {
		b.Invalidate()
	}
	g.geomDirty = false
	return g.applySectorInterp()
}

// worldGeomChanged reports whether a sector outside the live set changed
// height since the last call (the static mesh would then be stale),
// refreshing the snapshot for every sector — live ones included, so one
// leaving the set (SetLiveSectors rebuilt it at its final height) isn't
// flagged again. False on the first call after a resync.
func (g *Game) worldGeomChanged() bool {
	secs := g.Level.Sectors
	if len(g.geomHeights) != len(secs)*2 {
		g.geomHeights = make([]int16, len(secs)*2)
		for i := range secs {
			g.geomHeights[i*2], g.geomHeights[i*2+1] = secs[i].FloorHeight, secs[i].CeilingHeight
		}
		return false
	}
	changed := false
	for i := range secs {
		f, c := secs[i].FloorHeight, secs[i].CeilingHeight
		if g.geomHeights[i*2] != f || g.geomHeights[i*2+1] != c {
			g.geomHeights[i*2], g.geomHeights[i*2+1] = f, c
			changed = changed || !g.isLiveSector(i)
		}
	}
	return changed
}

// resetAnimShown records every animated surface's current texture as what
// the static mesh shows (a resync rebuilds it from these names), and
// preloads every animation frame so none first uploads mid-game.
func (g *Game) resetAnimShown() {
	secs := g.Level.Sectors
	g.animShownFlat = g.animShownFlat[:0]
	for i := range secs {
		g.animShownFlat = append(g.animShownFlat, secs[i].FloorTexture, secs[i].CeilingTexture)
	}
	g.animShownWall = g.animShownWall[:0]
	for _, s := range g.wallAnimSlots {
		g.animShownWall = append(g.animShownWall, *s.texture(g))
	}
	flats := make([]string, 0, len(g.flatAnimByName))
	for name := range g.flatAnimByName {
		flats = append(flats, name)
	}
	walls := make([]string, 0, len(g.wallAnimByName))
	for name := range g.wallAnimByName {
		walls = append(walls, name)
	}
	g.WorldBuilder.PreloadTextures(flats, walls)
}

// syncAnimTextures follows animated flats/walls that stepped a frame since
// the last call: the shown→current name pairs go to Builder.Retexture.
// Reports whether that couldn't follow them (the caller then rebuilds).
// rebuilding skips the retexture (a rebuild is happening anyway) but still
// records the new names.
func (g *Game) syncAnimTextures(rebuilding bool) (needRebuild bool) {
	secs := g.Level.Sectors
	if len(g.animShownFlat) != len(secs)*2 || len(g.animShownWall) != len(g.wallAnimSlots) {
		g.resetAnimShown()
		return !rebuilding
	}
	if g.animFlatSwap == nil {
		g.animFlatSwap, g.animWallSwap = map[string]string{}, map[string]string{}
	}
	clear(g.animFlatSwap)
	clear(g.animWallSwap)
	conflict := false
	note := func(swaps map[string]string, shown *string, cur string) {
		if *shown == cur {
			return
		}
		if prev, ok := swaps[*shown]; ok && prev != cur {
			conflict = true // one frame became two: Retexture can't express it
		}
		swaps[*shown] = cur
		*shown = cur
	}
	for i := range g.animBaseFloor {
		if g.animBaseFloor[i] != "" {
			note(g.animFlatSwap, &g.animShownFlat[i*2], secs[i].FloorTexture)
		}
		if g.animBaseCeil[i] != "" {
			note(g.animFlatSwap, &g.animShownFlat[i*2+1], secs[i].CeilingTexture)
		}
	}
	for k, s := range g.wallAnimSlots {
		note(g.animWallSwap, &g.animShownWall[k], *s.texture(g))
	}
	if rebuilding || len(g.animFlatSwap)+len(g.animWallSwap) == 0 {
		return false
	}
	return conflict || !g.WorldBuilder.Retexture(g.animFlatSwap, g.animWallSwap)
}
