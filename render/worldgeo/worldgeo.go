// Package worldgeo turns a BSP-walked level into a flat triangle list with
// per-vertex texture / light / kind attributes — the backend-agnostic
// input the hardware (GPU-geometry) renderer needs, the same shape
// PrBoom-GL and GZDoom build from their BSP walk. It does no drawing: the
// GPU (package render/vulkan) does the actual rasterization from this data.
//
// The BSP is still walked on the CPU (front-to-back, for early-Z), but only
// to *collect* geometry; the GPU then does the per-pixel rasterization,
// perspective-correct texturing, filtering and depth testing.
package worldgeo

import (
	"math"
	"slices"

	"twopointfive/assets"
	"twopointfive/bsp"
	"twopointfive/wad"
)

// skyFlatName is Doom's hardcoded sky flat — a ceiling (or, rarely, floor)
// with this texture is painted as sky, not a normal flat (see raster/sky.go).
const skyFlatName = "F_SKY1"

// fakeContrast is vanilla's light nudge on perfectly axis-aligned walls
// (r_segs.c) — kept identical to raster/light.go so the hardware path
// shades a wall the same as the software one.
const fakeContrast = 16

// Vert kinds — the shader keys texturing / alpha-test / sky off this.
const (
	KindWall         uint16 = iota // opaque wall piece: sample Tex, sector fade
	KindMasked                     // 2-sided middle texture: alpha-test, one copy (no vertical tile)
	KindFlat                       // floor / ceiling: sample Tex, sector fade
	KindSky                        // F_SKY1 flat or a sky wall-step: shader paints sky, still writes depth
	KindSprite                     // camera-facing billboard (thing / monster / item): alpha-test, sector fade
	KindSpriteFull                 // billboard, full bright (projectile / muzzle flash / lit powerup)
	KindLiquidWater                // water floor flat: fresnel sky reflection (world.frag)
	KindLiquidLava                 // lava floor flat: warm emissive glow
	KindLiquidNukage               // nukage/slime floor flat: green emissive tint
	KindSpriteTrans                // billboard, real alpha blend (Vert.Alpha) — see AddTransparentSprites
	KindTeleport                   // teleporter-pad floor flat: procedural shimmering blue water, ignores Tex entirely
)

// liquidKindOfFlat classifies a floor flat's lump name for the liquid
// surface treatments world.frag applies (a mirrored-sky reflection on
// water, a self-lit glow on lava/nukage). It keys off the family prefix so
// it still matches whichever animation frame is showing — mirrors
// raster.classifyFloorLiquid and engine's liquidKindOfFlat. Returns
// KindFlat for anything that is not a liquid.
func liquidKindOfFlat(name string) uint16 {
	has := func(p string) bool { return len(name) >= len(p) && name[:len(p)] == p }
	switch {
	case has("FWATER"), has("FLTWAWA"), has("FLTFLWW"): // Doom / Heretic water
		return KindLiquidWater
	case has("LAVA") || name == "FIRELAVA" || has("FLTLAVA"): // Doom / Heretic lava
		return KindLiquidLava
	case has("NUKAGE"), has("SLIME"), has("FLTSLUD"): // Doom / Heretic sludge
		return KindLiquidNukage
	}
	return KindFlat
}

// isTeleportFlat reports whether name is one of vanilla Doom's animated
// teleporter-pad floor flats (GATE1-4, cycled by the WAD's own ANIMATED
// lump — see tools/importquake/mapping.txt for the same four names). No
// other flat family starts with this prefix. Classified separately from
// liquidKindOfFlat since a teleporter pad isn't a liquid: emitFlatPlane
// swaps in KindTeleport's own fully procedural shimmer (world.frag ignores
// Tex entirely for it — see KindTeleport's own doc comment), not a
// treatment layered on top of the real texture the way the liquids are.
func isTeleportFlat(name string) bool {
	return len(name) >= 4 && name[:4] == "GATE"
}

// SkyTex is Vert.Tex for a KindSky vertex (it indexes nothing in Textures).
const SkyTex = 0xFFFF

// Vert is one triangle-list vertex: world position (map units, Z up), the
// texture coordinate (0..1 per texture tile — a REPEAT sampler tiles it),
// the sector light 0..1 (fake-contrast already folded in for walls), the
// unit surface normal (world space — walls face into their sector, floors
// up, ceilings down, billboards at the camera) that the dynamic point
// lights use for a Lambert term, the texture slot (builder bookkeeping; not
// in the packed GPU layout), the kind, and Alpha — only meaningful for
// KindSpriteTrans (every other kind is either opaque or alpha-tested, and
// ignores it): the blend weight a real translucent draw uses instead of
// the hard tex.a<0.5 cutout the other sprite kinds get.
type Vert struct {
	X, Y, Z    float32
	U, V       float32
	Light      float32
	Nx, Ny, Nz float32
	Alpha      float32
	Tex        uint16
	Kind       uint16
	// Sector is the level sector this wall/flat surface belongs to, plus
	// one — 0 means "none" (sprites, particles). The dynamic lights use it
	// to light only the sectors each one can actually reach
	// (render.Light.Reach), so a light doesn't shine through walls.
	Sector uint16
}

// Draw is one contiguous run of Geometry.Tris that shares a texture: the
// backend binds Tex (nil = the sky texture) once and issues a single
// vkCmdDraw of Count vertices starting at First. Runs are in Textures
// order, sky last. Bright is the run's brightmap mask (nil = none — the
// backend binds a black dummy), resolved once per texture like Tex. Cell is
// which visGrid cell this run's geometry was bucketed under (StaticDraws
// only — a per-frame Draws run's Cell is always 0 and unused) — see
// visgrid.go; it groups draws for batching but is not itself what gets
// culled. MinX/MinY/MaxX/MaxY is this run's actual world-space vertex
// bounding box (StaticDraws only), computed once in Invalidate:
// UpdateVisibility tests THIS box rather than the grid cell's nominal
// bounds, because a single subsector — and so a single cell's worth of
// geometry — can span well past one visGridCellSize on a large outdoor
// map (a wide subsector is still assigned by its centroid, see
// visGrid.cellOf), which would otherwise let the fixed cell box cull
// geometry the camera is standing right next to.
type Draw struct {
	Tex                    *assets.RGBA
	Bright                 *assets.RGBA
	First                  int
	Count                  int
	Cell                   int32
	MinX, MinY, MaxX, MaxY float32
}

// Geometry splits the world into a static part (all the level's walls and
// flats — built once, rebuilt only when a door/lift changes a sector
// height) and a per-frame dynamic part (the sprite / voxel-billboard
// quads). A backend keeps the static triangle list in a device-local
// buffer, re-uploading only when StaticVersion changes, and streams just
// the small dynamic list each frame.
//
// StaticDraws index StaticTris; Draws index Tris. Textures is append-only
// and shared by both (Draw.Tex nil = the sky). Sky is the texture those
// nil-Tex draws paint.
type Geometry struct {
	StaticTris    []Vert
	StaticDraws   []Draw
	StaticVersion uint32 // bumped by New / Invalidate
	// StaticDrawVisible is parallel to StaticDraws: whether Builder's last
	// UpdateVisibility call found that draw's own bounding box (Draw.MinX/
	// MinY/MaxX/MaxY) within the camera's view. A backend's colour pass
	// skips a StaticDraws entry whose
	// StaticDrawVisible is false; nil (before the first UpdateVisibility
	// call) means "treat everything as visible" — see UpdateVisibility.
	StaticDrawVisible []bool
	// StaticOpaqueCount is the vertex index in StaticTris where the alpha-
	// tested 2-sided middle textures begin (opaque walls/flats/sky come
	// first). A backend depth pre-pass draws StaticTris[:StaticOpaqueCount]
	// only, so grate holes don't write depth.
	StaticOpaqueCount int

	// WaterDraws is KindLiquidWater's own Draw list, indexing the same
	// StaticTris buffer but kept separate from StaticDraws (unlike
	// lava/nukage, which stay interleaved in StaticDraws like any other
	// opaque flat) — a backend wanting real refraction needs to draw
	// "everything except water" first, then water in its own pass step, so
	// water can't just be batched wherever its texture slot happens to
	// fall. WaterDrawVisible parallels it the same way StaticDrawVisible
	// parallels StaticDraws — see UpdateVisibility.
	WaterDraws       []Draw
	WaterDrawVisible []bool

	Tris  []Vert // dynamic: sprite + voxel-billboard quads, refilled per frame
	Draws []Draw

	// LiveDraws is the geometry of the "live" sectors (Builder.
	// SetLiveSectors — ones currently moving, flickering or scrolling):
	// their floors, ceilings and every wall touching them, re-emitted by
	// each Build from the sectors' current state (interpolated heights,
	// light, offsets) into Tris — so a moving door or a strobing light
	// costs a few quads a frame instead of rebuilding and re-uploading the
	// whole static mesh. Kinds are world kinds (wall/flat/sky/masked/
	// liquid), drawn with the world pipeline right after StaticDraws. The
	// first LiveOpaqueDraws entries are the solid walls/flats (what a
	// depth-only pass such as shadow casting wants); sky, masked and
	// water runs follow.
	LiveDraws       []Draw
	LiveOpaqueDraws int

	// ParticleDraws is AddParticleSprites' own output — same Tris buffer,
	// same texture slots as Draws, but meant for a backend's separate
	// additive, no-depth-write pipeline (see render/vulkan's pipeParticleAdd)
	// so a dense burst of overlapping billboards glows brighter where they
	// overlap instead of alpha-cutout-occluding each other. Kept apart from
	// Draws because that distinction lives at the pipeline-bind level, not
	// per-vertex.
	ParticleDraws []Draw

	// TransparentDraws is AddTransparentSprites' own output — real alpha
	// blending (KindSpriteTrans), for a backend's separate blend pipeline
	// (render/vulkan's pipeParticleTrans): SrcAlpha/OneMinusSrcAlpha, not
	// additive. Unlike Draws/ParticleDraws this is NOT batched by texture:
	// unlike additive, translucent blending is order-dependent, so
	// AddTransparentSprites sorts its input back-to-front by camera
	// distance first and emits one Draw per sprite in that order, even
	// where consecutive sprites happen to share a texture — unrelated
	// sprites of the same texture may not be adjacent after the sort.
	TransparentDraws []Draw

	Textures []*assets.RGBA
	// Brightmaps is parallel to Textures (same slot index): the brightmap
	// mask for that texture, or nil. Populated by slot().
	Brightmaps []*assets.RGBA
	Sky        *assets.RGBA

	// Voxels is this frame's voxel-model instances (AddVoxels), drawn by the
	// backend with their own pipeline after the textured geometry.
	Voxels []VoxelInstance
	// Models are this frame's textured 3D model parts (modelinst.go) —
	// drawn by the backend's model pipeline, like Voxels.
	Models []ModelInstance
}

// skyTextureForMap mirrors raster.skyTextureForMap / PrBoom's P_SetupLevel:
// SKY1..4 by episode for Doom, SKY1/2/3 by MAP range for Doom II.
func skyTextureForMap(mapName string) string {
	m := mapName
	if len(m) >= 4 && (m[0] == 'E' || m[0] == 'e') && (m[2] == 'M' || m[2] == 'm') {
		switch m[1] {
		case '2':
			return "SKY2"
		case '3':
			return "SKY3"
		case '4':
			return "SKY4"
		}
		return "SKY1"
	}
	if len(m) >= 5 && (m[0] == 'M' || m[0] == 'm') {
		n := 0
		for _, c := range m[3:] {
			if c < '0' || c > '9' {
				n = 0
				break
			}
			n = n*10 + int(c-'0')
		}
		switch {
		case n >= 21:
			return "SKY3"
		case n >= 12:
			return "SKY2"
		}
	}
	return "SKY1"
}

// SetSky overrides the resolved WAD sky with a panorama (assets.LoadSkybox).
// Currently unused by any caller — see assets.LoadSkybox's doc comment.
func (b *Builder) SetSky(t *assets.RGBA) {
	if t != nil {
		b.g.Sky = t
	}
}

// SetSkyName forces the WAD sky to the given texture name (a UMAPINFO
// `skytexture`), or "" to fall back to the per-episode default. Resolving
// to nothing leaves the current sky untouched.
func (b *Builder) SetSkyName(name string) {
	if name == "" {
		name = skyTextureForMap(b.lvl.Name)
	}
	if t, ok := b.tex.WallTexture(name); ok && t != nil {
		b.g.Sky = t
	}
}

// Builder walks one level's BSP into Geometry, reusing its buffers so a
// per-frame Build allocates nothing once warm.
type Builder struct {
	lvl              *wad.Level
	tree             bsp.Node
	tex              *assets.Textures
	texID            map[*assets.RGBA]uint16
	g                Geometry
	polys            [][]bsp.Point               // per subsector: the closed flat polygon (nil for a few slivers)
	fan              []bsp.Point                 // reused scratch for the seg-fan fallback
	grid             *visGrid                    // Stage 1 visibility-culling scoping — see visgrid.go
	curCell          int32                       // this subsector's grid cell, set once per emitSubsectorIdx call
	curSector        uint16                      // this subsector's sector + 1 (Vert.Sector), set with curCell
	buckets          map[uint16]map[int32][]Vert // opaque static: per texture slot (SkyTex for sky), per grid cell
	masked           map[uint16]map[int32][]Vert // static KindMasked (2-sided middles): per texture slot, per grid cell
	waterBuckets     map[uint16]map[int32][]Vert // static KindLiquidWater: per texture slot, per grid cell — kept apart from buckets so it can become its own Draw list (Geometry.WaterDraws)
	sprites          map[uint16][]Vert           // per texture slot, AddSprites billboards — reused across calls
	particleSprites  map[uint16][]Vert           // per texture slot, AddParticleSprites billboards — reused across calls
	transparentItems []transparentItem           // AddTransparentSprites' own sort scratch — reused across calls

	// Live sectors (SetLiveSectors): live marks them by sector index;
	// liveSS lists the subsectors with any live geometry (their own
	// sector live, or a seg whose other side is); liveBuckets is the
	// per-frame emission scratch, the same shape as buckets/masked/
	// waterBuckets (emitLive swaps it in).
	live        []bool
	liveSS      []int
	liveBuckets [3]map[uint16]map[int32][]Vert
	// The live geometry is cached: emitLive re-emits it only when one of
	// its inputs changed — liveSecs (the live sectors and their
	// neighbours) or liveSides (the involved walls' sidedefs), compared
	// against liveSnapSec/liveSnapSide. liveVerts is its length at the
	// head of Tris; liveValid is false until the first emission and after
	// any rebuild.
	liveSecs     []int
	liveSides    []int
	liveSnapSec  []wad.Sector
	liveSnapSide []wad.Sidedef
	liveVerts    int
	liveValid    bool

	// SkipCeilings, when true, omits every ceiling flat from the static
	// geometry (floors, walls, and sky are unaffected) — a top-down/
	// isometric viewer that never wants a room's roof occluding its own
	// interior sets this instead of emitting ceiling geometry it would then
	// need separate per-frame visibility logic to hide. false (what New
	// leaves it at) keeps cmd/engine's existing behaviour: every FPS-path
	// caller renders ceilings normally. New already calls Invalidate once
	// before returning, so a caller wanting this set must do so and call
	// Invalidate again itself: `b := New(...); b.SkipCeilings = true;
	// b.Invalidate()`.
	SkipCeilings bool

	// extraFloors is this level's room-over-room "3D floor" bands — target
	// sector index -> the control sector indices it borrows a platform
	// from (engine.ExtraFloor, one per entry) — set via SetExtraFloors.
	// nil (New's default) draws every sector as the plain vanilla single
	// floor/ceiling pair, unchanged from before this feature existed.
	extraFloors map[int][]int
}

// SetExtraFloors installs this level's room-over-room bands for the static
// mesh builder to emit (see emitExtraFloors) and invalidates the cached
// geometry so the next Build picks them up. m is target sector index ->
// control sector indices (engine.Game.extraFloors, flattened); nil clears
// it back to "no room-over-room platforms." The engine calls this once
// per level load, right after (re)wiring WorldBuilder — see
// engine.ApplyMapInfo's doc comment for why that's a separate step from
// NewGame/loadMap themselves (WorldBuilder isn't always built yet then).
func (b *Builder) SetExtraFloors(m map[int][]int) {
	b.extraFloors = m
	b.Invalidate()
}

// New binds a builder to a level. tex resolves texture / flat names to the
// shared *assets.RGBA bitmaps (stable pointers — used as the Textures key).
// The per-subsector flat polygons are reconstructed here, and the whole
// level's static geometry is built once (see Invalidate).
func New(lvl *wad.Level, tree bsp.Node, tex *assets.Textures) *Builder {
	b := &Builder{
		lvl: lvl, tree: tree, tex: tex,
		texID:           make(map[*assets.RGBA]uint16),
		polys:           bsp.SubsectorPolygons(tree, lvl),
		buckets:         make(map[uint16]map[int32][]Vert),
		masked:          make(map[uint16]map[int32][]Vert),
		waterBuckets:    make(map[uint16]map[int32][]Vert),
		sprites:         make(map[uint16][]Vert),
		particleSprites: make(map[uint16][]Vert),
		liveBuckets: [3]map[uint16]map[int32][]Vert{
			make(map[uint16]map[int32][]Vert), make(map[uint16]map[int32][]Vert), make(map[uint16]map[int32][]Vert),
		},
	}
	b.grid = newLevelVisGrid(lvl)
	// Resolve the per-map WAD sky (a wall texture named SKY1..4) once; a
	// caller can still override it with a panorama via SetSky.
	if sky, ok := tex.WallTexture(skyTextureForMap(lvl.Name)); ok && sky != nil {
		b.g.Sky = sky
	}
	b.Invalidate()
	return b
}

// newLevelVisGrid builds a visGrid spanning lvl's vertex bounds — the same
// bounds-from-vertices pattern engine/collisiongrid.go's buildCollisionGrid
// uses, just for a coarser purpose.
func newLevelVisGrid(lvl *wad.Level) *visGrid {
	minX, minY := float32(math.Inf(1)), float32(math.Inf(1))
	maxX, maxY := float32(math.Inf(-1)), float32(math.Inf(-1))
	for _, v := range lvl.Vertexes {
		x, y := float32(v.X), float32(v.Y)
		if x < minX {
			minX = x
		}
		if x > maxX {
			maxX = x
		}
		if y < minY {
			minY = y
		}
		if y > maxY {
			maxY = y
		}
	}
	if minX > maxX { // no vertices at all — degenerate but shouldn't happen for a real level
		minX, minY, maxX, maxY = 0, 0, 1, 1
	}
	return newVisGrid(minX, minY, maxX, maxY)
}

// Invalidate rebuilds the static world geometry (all subsectors' walls and
// flats, less the live sectors' — see SetLiveSectors) and bumps
// StaticVersion so the backend re-uploads it. Call after something baked
// into it changes outside a live sector (a height, a light, a texture
// Retexture can't follow) — otherwise the per-frame Build reuses the
// cached list untouched.
func (b *Builder) Invalidate() {
	b.liveValid = false
	for _, m := range [...]map[uint16]map[int32][]Vert{b.buckets, b.masked, b.waterBuckets} {
		truncateBuckets(m)
	}
	for i := range b.lvl.Subsectors {
		b.emitSubsectorIdx(i, passStatic)
	}
	// Concatenate into one triangle list: opaque per-texture runs, then the
	// sky run, then (after StaticOpaqueCount) the alpha-tested 2-sided
	// middle-texture runs, then water (its own Draw list, Geometry.
	// WaterDraws — a backend wanting real refraction draws everything else
	// first). One Draw per (texture, grid cell) run — see visgrid.go — in
	// sorted cell order, so the Draw order is the same build to build.
	g := &b.g
	g.StaticTris, g.StaticDraws, g.WaterDraws = g.StaticTris[:0], g.StaticDraws[:0], g.WaterDraws[:0]
	for slot := range uint16(len(g.Textures)) {
		b.appendRuns(&g.StaticDraws, &g.StaticTris, b.buckets, slot, true)
	}
	b.appendRuns(&g.StaticDraws, &g.StaticTris, b.buckets, SkyTex, true)
	g.StaticOpaqueCount = len(g.StaticTris)
	for slot := range uint16(len(g.Textures)) {
		b.appendRuns(&g.StaticDraws, &g.StaticTris, b.masked, slot, true)
	}
	for slot := range uint16(len(g.Textures)) {
		b.appendRuns(&g.WaterDraws, &g.StaticTris, b.waterBuckets, slot, true)
	}
	// Process-monotonic so a fresh builder (level change) and a door move
	// both hand the backend a version it hasn't uploaded yet.
	staticVersionSeq++
	g.StaticVersion = staticVersionSeq
}

// truncateBuckets empties every (slot, cell) bucket of m in place, keeping
// the backing arrays for the next emission.
func truncateBuckets(m map[uint16]map[int32][]Vert) {
	for _, byCell := range m {
		for cell := range byCell {
			byCell[cell] = byCell[cell][:0]
		}
	}
}

// appendRuns moves texture slot's buckets in m onto *tris as one Draw per
// non-empty grid cell (appended to *draws), each carrying its vertices'
// bounding box for UpdateVisibility. sorted orders the cells (the static
// mesh, rebuilt rarely, wants a deterministic Draw order); the per-frame
// live pass skips the sort.
func (b *Builder) appendRuns(draws *[]Draw, tris *[]Vert, m map[uint16]map[int32][]Vert, slot uint16, sorted bool) {
	byCell := m[slot]
	if len(byCell) == 0 {
		return
	}
	var tex, bright *assets.RGBA // nil Tex = the sky
	if slot != SkyTex {
		tex, bright = b.g.Textures[slot], b.g.Brightmaps[slot]
	}
	add := func(cell int32) {
		bk := byCell[cell]
		if len(bk) == 0 {
			return
		}
		minX, minY, maxX, maxY := bucketBounds(bk)
		*draws = append(*draws, Draw{Tex: tex, Bright: bright, First: len(*tris), Count: len(bk), Cell: cell, MinX: minX, MinY: minY, MaxX: maxX, MaxY: maxY})
		*tris = append(*tris, bk...)
	}
	if !sorted {
		for cell := range byCell {
			add(cell)
		}
		return
	}
	for _, cell := range sortedCells(byCell) {
		add(cell)
	}
}

// SetLiveSectors marks the sectors whose baked-in state is changing right
// now — moving (doors, lifts, crushers), flickering (light specials) or
// scrolling (wall scrollers); secs are sector indices, nil for none.
// Their floors, ceilings and every wall touching them leave the static
// mesh and are re-emitted by each Build from their current state instead
// (Geometry.LiveDraws), so a change costs a few quads per frame rather
// than a full static rebuild and GPU re-upload. A room-over-room sector
// whose platform's control sector is live counts as live too. Rebuilds
// the static mesh (Invalidate) only when the set actually changes;
// reports whether it did.
func (b *Builder) SetLiveSectors(secs []int) bool {
	n := len(b.lvl.Sectors)
	next := make([]bool, n)
	marked := false
	for _, s := range secs {
		if s >= 0 && s < n {
			next[s], marked = true, true
		}
	}
	for target, ctrls := range b.extraFloors {
		for _, c := range ctrls {
			if c >= 0 && c < n && next[c] && target >= 0 && target < n {
				next[target] = true
			}
		}
	}
	if !marked {
		next = nil
	}
	if slices.Equal(next, b.live) {
		return false
	}
	b.live = next
	b.liveSS = b.liveSS[:0]
	if next != nil {
		for i := range b.lvl.Subsectors {
			if b.subsectorHasLive(i) {
				b.liveSS = append(b.liveSS, i)
			}
		}
	}
	b.collectLiveInputs()
	b.Invalidate()
	return true
}

// collectLiveInputs lists what the live geometry is built from: every
// sector on either side of a live subsector's segs (plus the control
// sectors of its room-over-room platforms) and every such seg's front
// sidedef — so emitLive can tell when nothing it reads has changed.
func (b *Builder) collectLiveInputs() {
	secSeen, sideSeen := map[int]bool{}, map[int]bool{}
	b.liveSecs, b.liveSides = b.liveSecs[:0], b.liveSides[:0]
	addSec := func(s int) {
		if s < 0 || s >= len(b.lvl.Sectors) || secSeen[s] {
			return
		}
		secSeen[s] = true
		b.liveSecs = append(b.liveSecs, s)
		for _, c := range b.extraFloors[s] {
			if c >= 0 && c < len(b.lvl.Sectors) && !secSeen[c] {
				secSeen[c] = true
				b.liveSecs = append(b.liveSecs, c)
			}
		}
	}
	for _, i := range b.liveSS {
		ss := b.lvl.Subsectors[i]
		for _, seg := range b.lvl.Segs[ss.FirstSeg : int(ss.FirstSeg)+int(ss.SegCount)] {
			if int(seg.Linedef) >= len(b.lvl.Linedefs) {
				continue
			}
			ld := &b.lvl.Linedefs[seg.Linedef]
			for side, sd := range [2]uint16{ld.FrontSidedef, ld.BackSidedef} {
				if sd == wad.NoSidedef || int(sd) >= len(b.lvl.Sidedefs) {
					continue
				}
				addSec(int(b.lvl.Sidedefs[sd].Sector))
				// The seg's own (front) side supplies its textures/offsets.
				if (side == 0) == (seg.Direction == 0) && !sideSeen[int(sd)] {
					sideSeen[int(sd)] = true
					b.liveSides = append(b.liveSides, int(sd))
				}
			}
		}
	}
	b.liveSnapSec = slices.Grow(b.liveSnapSec[:0], len(b.liveSecs))[:len(b.liveSecs)]
	b.liveSnapSide = slices.Grow(b.liveSnapSide[:0], len(b.liveSides))[:len(b.liveSides)]
}

// liveInputsUnchanged reports whether every input of the live geometry is
// as it was at the last emission.
func (b *Builder) liveInputsUnchanged() bool {
	for i, s := range b.liveSecs {
		if b.lvl.Sectors[s] != b.liveSnapSec[i] {
			return false
		}
	}
	for i, s := range b.liveSides {
		if b.lvl.Sidedefs[s] != b.liveSnapSide[i] {
			return false
		}
	}
	return true
}

// isLive reports whether sector index sec is in the SetLiveSectors set.
func (b *Builder) isLive(sec int) bool { return sec >= 0 && sec < len(b.live) && b.live[sec] }

// segTouchesLive reports whether either side of seg is a live sector — its
// wall pieces then depend on that sector's current height (or, on its own
// side, light and offsets).
func (b *Builder) segTouchesLive(seg wad.Seg) bool {
	if b.live == nil || int(seg.Linedef) >= len(b.lvl.Linedefs) {
		return false
	}
	ld := &b.lvl.Linedefs[seg.Linedef]
	for _, sd := range [2]uint16{ld.FrontSidedef, ld.BackSidedef} {
		if sd != wad.NoSidedef && int(sd) < len(b.lvl.Sidedefs) && b.isLive(int(b.lvl.Sidedefs[sd].Sector)) {
			return true
		}
	}
	return false
}

// subsectorHasLive reports whether subsector idx has any live geometry: its
// own sector live, or a seg bordering one.
func (b *Builder) subsectorHasLive(idx int) bool {
	ss := b.lvl.Subsectors[idx]
	first, n := int(ss.FirstSeg), int(ss.SegCount)
	if n <= 0 || first < 0 || first+n > len(b.lvl.Segs) {
		return false
	}
	for _, s := range b.lvl.Segs[first : first+n] {
		if b.segTouchesLive(s) {
			return true
		}
	}
	return false
}

// emitLive puts every live sector's geometry, from its current state, at
// the head of Tris / LiveDraws — Build's per-frame half of SetLiveSectors.
// It's re-emitted only when an input changed (a door's drawn height, a
// light stepping, a scroller ticking); otherwise last frame's copy,
// already in place, is kept.
// Emission reuses the static path by swapping the live scratch buckets in
// for the static ones for the duration.
func (b *Builder) emitLive() {
	g := &b.g
	if b.liveValid && b.liveInputsUnchanged() {
		g.Tris = g.Tris[:b.liveVerts] // last frame's, still at the head of Tris
		return
	}
	g.LiveDraws, g.LiveOpaqueDraws = g.LiveDraws[:0], 0
	b.liveVerts, b.liveValid = 0, false
	if len(b.liveSS) == 0 {
		return
	}
	mb := &b.liveBuckets
	for _, m := range mb {
		truncateBuckets(m)
	}
	b.buckets, b.masked, b.waterBuckets, mb[0], mb[1], mb[2] = mb[0], mb[1], mb[2], b.buckets, b.masked, b.waterBuckets
	for _, i := range b.liveSS {
		b.emitSubsectorIdx(i, passLive)
	}
	b.buckets, b.masked, b.waterBuckets, mb[0], mb[1], mb[2] = mb[0], mb[1], mb[2], b.buckets, b.masked, b.waterBuckets

	for slot := range uint16(len(g.Textures)) {
		b.appendRuns(&g.LiveDraws, &g.Tris, mb[0], slot, false)
	}
	g.LiveOpaqueDraws = len(g.LiveDraws)
	b.appendRuns(&g.LiveDraws, &g.Tris, mb[0], SkyTex, false)
	for slot := range uint16(len(g.Textures)) {
		b.appendRuns(&g.LiveDraws, &g.Tris, mb[1], slot, false)
		b.appendRuns(&g.LiveDraws, &g.Tris, mb[2], slot, false)
	}
	for i, s := range b.liveSecs {
		b.liveSnapSec[i] = b.lvl.Sectors[s]
	}
	for i, s := range b.liveSides {
		b.liveSnapSide[i] = b.lvl.Sidedefs[s]
	}
	b.liveVerts, b.liveValid = len(g.Tris), true
}

// Retexture follows animated textures stepping a frame without rebuilding
// the static mesh: every static draw showing the flat named by a key of
// flats (or the wall texture named by a key of walls) is repointed at the
// value's texture. Only the bound texture changes — the GPU reads no
// per-vertex texture — so this is valid while the old and new frames share
// a size and texel density (UVs are baked from those). Reports false,
// changing nothing, when that doesn't hold or a texture would map to two
// different ones; the caller then rebuilds with Invalidate. The live
// sectors' geometry needs nothing: it's re-emitted from the current names
// every Build.
func (b *Builder) Retexture(flats, walls map[string]string) bool {
	type swap struct{ tex, bright *assets.RGBA }
	remap := make(map[*assets.RGBA]swap, len(flats)+len(walls))
	add := func(oldName, newName string, lookup func(string) (*assets.RGBA, bool)) bool {
		o, ok1 := lookup(oldName)
		n, ok2 := lookup(newName)
		if !ok1 || !ok2 || o == nil || n == nil {
			return ok1 == ok2 && o == n // both unresolved: nothing drawn either way
		}
		if o.Width != n.Width || o.Height != n.Height || o.TexelsPerUnit != n.TexelsPerUnit {
			return false
		}
		if prev, dup := remap[o]; dup && prev.tex != n {
			return false
		}
		bm, _ := b.tex.Brightmap(newName, n)
		remap[o] = swap{n, bm}
		return true
	}
	for o, n := range flats {
		if !add(o, n, b.tex.Flat) {
			return false
		}
	}
	for o, n := range walls {
		if !add(o, n, b.tex.WallTexture) {
			return false
		}
	}
	for _, sw := range remap {
		b.slot(sw.tex, sw.bright) // registered, so the backend uploads it
	}
	for _, draws := range [2][]Draw{b.g.StaticDraws, b.g.WaterDraws} {
		for i := range draws {
			if sw, ok := remap[draws[i].Tex]; ok && draws[i].Tex != nil {
				draws[i].Tex, draws[i].Bright = sw.tex, sw.bright
			}
		}
	}
	return true
}

// PreloadTextures registers the named flats and wall textures with the
// geometry (Geometry.Textures), so a backend uploads them up front — the
// frames of every animated texture at level load, rather than a GPU stall
// the first time each frame is shown.
func (b *Builder) PreloadTextures(flats, walls []string) {
	for _, set := range [2]struct {
		names  []string
		lookup func(string) (*assets.RGBA, bool)
	}{{flats, b.tex.Flat}, {walls, b.tex.WallTexture}} {
		for _, name := range set.names {
			if t, ok := set.lookup(name); ok && t != nil {
				bm, _ := b.tex.Brightmap(name, t)
				b.slot(t, bm)
			}
		}
	}
}

// bucketBounds is the world-space (X, Y) bounding box of a bucket's
// vertices — Invalidate calls this once per (texture, cell) run so
// UpdateVisibility can test a Draw's actual footprint instead of assuming
// it fits inside its nominal visGrid cell (see Draw's doc comment). bk is
// never empty here (callers already skip that case).
func bucketBounds(bk []Vert) (minX, minY, maxX, maxY float32) {
	minX, minY = bk[0].X, bk[0].Y
	maxX, maxY = minX, minY
	for _, v := range bk[1:] {
		if v.X < minX {
			minX = v.X
		}
		if v.X > maxX {
			maxX = v.X
		}
		if v.Y < minY {
			minY = v.Y
		}
		if v.Y > maxY {
			maxY = v.Y
		}
	}
	return
}

// sortedCells returns m's keys in ascending order — map iteration order is
// randomized in Go, and Invalidate runs rarely enough (door/lift moves)
// that a deterministic Draw order is worth a small sort rather than
// leaving build-to-build Draw ordering to chance.
func sortedCells(m map[int32][]Vert) []int32 {
	if len(m) == 0 {
		return nil
	}
	cells := make([]int32, 0, len(m))
	for c := range m {
		cells = append(cells, c)
	}
	slices.Sort(cells)
	return cells
}

// UpdateVisibility is Stage 2 of the static-geometry visibility-culling
// scoping (see the plan doc): it tests every StaticDraws entry's grid cell
// against cam's view and records the result in Geometry.StaticDrawVisible
// (reused/resized in place, so a warm level allocates nothing here) —
// render/vulkan/world.go's colour pass reads that array to skip drawing
// what it says is out of view. Call once per frame, after Build, before
// DrawWorld. Returns the same (visible, total) draw/vertex counts as
// before purely for the existing periodic diagnostic log — the counting is
// a free byproduct of the loop this now has to run anyway.
func (b *Builder) UpdateVisibility(cam Camera) (visibleDraws, totalDraws, visibleVerts, totalVerts int) {
	visibleDraws, totalDraws, visibleVerts, totalVerts = cullDraws(cam, b.g.StaticDraws, &b.g.StaticDrawVisible)
	cullDraws(cam, b.g.WaterDraws, &b.g.WaterDrawVisible)
	return
}

// cullDraws tests each of draws' own bounding box against cam's view,
// filling *visible (reused/resized in place, so a warm level allocates
// nothing here) — the shared body behind UpdateVisibility's StaticDraws
// pass (whose counts it returns, for the existing periodic diagnostic log)
// and its WaterDraws pass (counts discarded — water has no such log line).
func cullDraws(cam Camera, draws []Draw, visible *[]bool) (visibleDraws, totalDraws, visibleVerts, totalVerts int) {
	totalDraws = len(draws)
	if cap(*visible) < totalDraws {
		*visible = make([]bool, totalDraws)
	} else {
		*visible = (*visible)[:totalDraws]
	}
	cone := NewViewCone(cam)
	for i, d := range draws {
		totalVerts += d.Count
		vis := cone.boxVisible(d.MinX, d.MinY, d.MaxX, d.MaxY)
		(*visible)[i] = vis
		if vis {
			visibleDraws++
			visibleVerts += d.Count
		}
	}
	return
}

var staticVersionSeq uint32

// Build clears the per-frame dynamic geometry (AddSprites / AddVoxels refill
// it), emits the live sectors (SetLiveSectors) from their current state,
// and returns the Geometry. The static world part is untouched — no BSP
// walk, no per-frame triangle rebuild. The camera args are unused now (the
// whole level's geometry is submitted and the GPU depth-tests it); kept for
// call-site stability.
func (b *Builder) Build(_, _ float32) *Geometry {
	b.g.Tris = b.g.Tris[:0]
	b.g.Draws = b.g.Draws[:0]
	b.g.ParticleDraws = b.g.ParticleDraws[:0]
	b.g.TransparentDraws = b.g.TransparentDraws[:0]
	b.g.Voxels = b.g.Voxels[:0]
	b.emitLive()
	return &b.g
}

// Sprite is one thing the caller wants billboarded this frame: a
// camera-facing quad textured with Tex (already the right rotation frame),
// alpha-tested, shaded to sector Light (0..1) unless FullBright. X,Y,Z is
// the thing's position (Z = feet for a ground monster/item). Lift is the
// extra vertical seat raster's drawThingSprite applies so Doom art whose
// topoffset falls short of the sprite height doesn't poke through the floor
// (0 for the emissive projectile path). SizeMul scales past native size
// (projectiles/explosions; 1 for a plain thing). Flip mirrors U for Doom's
// shared mirrored rotations. The billboard's world size comes from Tex's
// pixel dimensions / TexelsPerUnit, its horizontal seat from Tex.OffsetX,
// exactly like raster.blitBillboard.
type Sprite struct {
	X, Y, Z    float32
	Lift       float32
	SizeMul    float32
	Tex        *assets.RGBA
	Light      float32
	FullBright bool
	Flip       bool
	// Alpha is only read by AddTransparentSprites (real alpha blend); 0
	// there means fully invisible, not "unset" — the caller always sets it.
	Alpha float32
}

// AddSprites appends camera-facing billboard quads for sprs to the geometry
// the most recent Build produced — one extra Draw per sprite texture, after
// the world (and sky) Draws. camAngle is the view yaw in radians. Call once
// per frame, after Build, before the backend consumes the *Geometry. The
// GPU depth test (the world pass already filled the depth buffer) sorts the
// billboards against the level and each other, so submission order here
// doesn't matter.
func (b *Builder) AddSprites(camAngle float64, sprs []Sprite) {
	b.addBillboardSprites(camAngle, sprs, b.sprites, &b.g.Draws)
}

// AddParticleSprites is AddSprites' twin for a backend's separate additive,
// no-depth-write pipeline (pipeParticleAdd in render/vulkan) — cosmetic
// billboards (currently engine.Particle's explosion sparks) meant to glow
// brighter where a dense burst overlaps rather than alpha-cutout-occlude
// each other the way KindSprite/KindSpriteFull do. Same math as AddSprites,
// writing into ParticleDraws instead of Draws; call it once per frame,
// alongside AddSprites.
func (b *Builder) AddParticleSprites(camAngle float64, sprs []Sprite) {
	b.addBillboardSprites(camAngle, sprs, b.particleSprites, &b.g.ParticleDraws)
}

// addBillboardSprites is the shared quad-building and per-texture-batching
// tail for AddSprites and AddParticleSprites — the two differ only in which
// scratch map (reused across calls, like AddTransparentSprites' own
// b.transparentItems) they batch quads through and which Draws list they
// append to, not in how a Sprite becomes a quad or how quads get batched by
// texture. scratch is reset (its slots truncated to length 0, not
// reallocated) on every call, same as before this was split out.
func (b *Builder) addBillboardSprites(camAngle float64, sprs []Sprite, scratch map[uint16][]Vert, dst *[]Draw) {
	for k := range scratch {
		scratch[k] = scratch[k][:0]
	}
	if len(sprs) == 0 {
		return
	}
	// Camera right vector in world XY (view dir rotated -90°): U=0 lands on
	// the viewer's left, matching raster's screen-space blit.
	rx := float32(math.Sin(camAngle))
	ry := float32(-math.Cos(camAngle))
	// Billboard normal faces the camera (opposite the view direction), so a
	// point light behind the viewer doesn't light a sprite's back.
	snx := float32(-math.Cos(camAngle))
	sny := float32(-math.Sin(camAngle))

	for i := range sprs {
		s := &sprs[i]
		if s.Tex == nil || s.Tex.Width <= 0 || s.Tex.Height <= 0 {
			continue
		}
		tpu := s.Tex.TexelsPerUnit
		if tpu <= 0 {
			tpu = 1
		}
		sm := s.SizeMul
		if sm <= 0 {
			sm = 1
		}
		worldW := float32(float64(s.Tex.Width) / tpu * float64(sm))
		worldH := float32(float64(s.Tex.Height) / tpu * float64(sm))
		offX := float32(float64(s.Tex.OffsetX) / tpu * float64(sm))
		offY := float32(float64(s.Tex.OffsetY) / tpu * float64(sm))

		topZ := s.Z + s.Lift + offY
		botZ := topZ - worldH
		oL, oR := -offX, worldW-offX // world offsets from (X,Y) along the right vector

		u0, u1 := float32(0), float32(1)
		if s.Flip {
			u0, u1 = 1, 0
		}
		kind := KindSprite
		if s.FullBright {
			kind = KindSpriteFull
		}
		slot := b.slot(s.Tex, nil)

		lx, ly := s.X+rx*oL, s.Y+ry*oL
		hx, hy := s.X+rx*oR, s.Y+ry*oR
		mk := func(x, y, z, u, v float32) Vert {
			return Vert{X: x, Y: y, Z: z, U: u, V: v, Light: s.Light, Nx: snx, Ny: sny, Tex: slot, Kind: kind}
		}
		bl := mk(lx, ly, botZ, u0, 1)
		br := mk(hx, hy, botZ, u1, 1)
		tr := mk(hx, hy, topZ, u1, 0)
		tl := mk(lx, ly, topZ, u0, 0)
		scratch[slot] = append(scratch[slot], bl, br, tr, bl, tr, tl)
	}

	for slot := uint16(0); int(slot) < len(b.g.Textures); slot++ {
		bk := scratch[slot]
		if len(bk) == 0 {
			continue
		}
		*dst = append(*dst, Draw{Tex: b.g.Textures[slot], Bright: b.g.Brightmaps[slot], First: len(b.g.Tris), Count: len(bk)})
		b.g.Tris = append(b.g.Tris, bk...)
	}
}

// transparentDepthBucketSize is how far apart (in world units) two
// translucent sprites' distances from the camera can be while still
// counting as "the same depth" for AddTransparentSprites' batching — see
// its own doc comment. Small enough that reordering sprites within one
// bucket is visually negligible (a handful of overlapping, similarly-sized
// soft particles), large enough that a typical burst of particles spans
// only a few buckets rather than one per sprite.
const transparentDepthBucketSize = 96.0

// transparentItem is AddTransparentSprites' own per-sprite sort key: idx
// indexes back into the caller's sprs slice rather than copying the whole
// Sprite, so sorting only moves a few bytes per item.
type transparentItem struct {
	idx    uint32
	bucket int32
	slot   uint16
	valid  bool
}

// AddTransparentSprites appends real-alpha-blended billboard quads for sprs
// (KindSpriteTrans — see pipeParticleTrans in render/vulkan). Blending here
// is order-dependent, unlike AddSprites/AddParticleSprites, so sprites
// still can't be freely batched by texture the way those are — but strict
// per-sprite back-to-front order is stricter than actually needed: two
// sprites close enough in depth to be in the same transparentDepthBucketSize
// band read the same however they're ordered against each other (they're
// small, similar, and mostly overlap the same background either way).
// So this sorts by (depth bucket descending, texture slot ascending)
// instead of pure distance, then coalesces consecutive same-bucket
// same-texture quads into one Draw (appendTransparentQuad's coalesce
// flag) — batched within a bucket, still strictly ordered bucket-to-bucket.
// A big burst that used to cost one draw call per sprite now costs
// roughly (bucket count × distinct textures per bucket), which for this
// engine's small set of procedural particle textures is a large reduction.
func (b *Builder) AddTransparentSprites(camX, camY, camZ float32, camAngle float64, sprs []Sprite) {
	if len(sprs) == 0 {
		return
	}
	// b.transparentItems is reused across calls (like b.sprites/
	// b.particleSprites above) instead of allocating a fresh scratch slice
	// every frame — this runs on every frame with any live fire/smoke
	// particle. Each item stores an index into sprs rather than a copy of
	// the whole Sprite, so sorting swaps a few bytes instead of the full
	// ~40-byte struct.
	items := b.transparentItems[:0]
	if cap(items) < len(sprs) {
		items = make([]transparentItem, 0, len(sprs))
	}
	for i := range sprs {
		s := &sprs[i]
		it := transparentItem{idx: uint32(i)}
		if s.Tex != nil && s.Tex.Width > 0 && s.Tex.Height > 0 && s.Alpha > 0 {
			d := float32(math.Sqrt(float64(sqDist(*s, camX, camY, camZ))))
			it.bucket = int32(d / transparentDepthBucketSize)
			it.slot = b.slot(s.Tex, nil)
			it.valid = true
		}
		items = append(items, it)
	}
	slices.SortFunc(items, func(a, c transparentItem) int {
		if a.valid != c.valid {
			if a.valid {
				return -1 // invalid (skipped) entries sink to the end
			}
			return 1
		}
		if a.bucket != c.bucket {
			return int(c.bucket - a.bucket) // farther bucket first
		}
		return int(a.slot) - int(c.slot)
	})
	b.transparentItems = items

	rx := float32(math.Sin(camAngle))
	ry := float32(-math.Cos(camAngle))
	snx := float32(-math.Cos(camAngle))
	sny := float32(-math.Sin(camAngle))

	haveLast := false
	var lastBucket int32
	var lastSlot uint16
	for _, it := range items {
		if !it.valid {
			continue
		}
		s := sprs[it.idx]
		tpu := s.Tex.TexelsPerUnit
		if tpu <= 0 {
			tpu = 1
		}
		sm := s.SizeMul
		if sm <= 0 {
			sm = 1
		}
		worldW := float32(float64(s.Tex.Width) / tpu * float64(sm))
		worldH := float32(float64(s.Tex.Height) / tpu * float64(sm))
		offX := float32(float64(s.Tex.OffsetX) / tpu * float64(sm))
		offY := float32(float64(s.Tex.OffsetY) / tpu * float64(sm))

		topZ := s.Z + s.Lift + offY
		botZ := topZ - worldH
		oL, oR := -offX, worldW-offX

		u0, u1 := float32(0), float32(1)
		if s.Flip {
			u0, u1 = 1, 0
		}

		lx, ly := s.X+rx*oL, s.Y+ry*oL
		hx, hy := s.X+rx*oR, s.Y+ry*oR
		coalesce := haveLast && it.bucket == lastBucket && it.slot == lastSlot
		b.appendTransparentQuad(s.Tex, it.slot, [4]quadCorner{
			{X: lx, Y: ly, Z: botZ, U: u0, V: 1},
			{X: hx, Y: hy, Z: botZ, U: u1, V: 1},
			{X: hx, Y: hy, Z: topZ, U: u1, V: 0},
			{X: lx, Y: ly, Z: topZ, U: u0, V: 0},
		}, snx, sny, 0, s.Light, s.Alpha, coalesce)
		lastBucket, lastSlot, haveLast = it.bucket, it.slot, true
	}
}

// quadCorner is one vertex's world position + UV for appendTransparentQuad.
type quadCorner struct {
	X, Y, Z float32
	U, V    float32
}

// appendTransparentQuad appends one real-alpha-blended (KindSpriteTrans)
// quad — 4 corners in order (bottom-left, bottom-right, top-right,
// top-left), triangulated as (0,1,2)+(0,2,3) — to g.Tris/g.TransparentDraws.
// Shared tail for AddTransparentSprites (camera-facing billboards) and
// AddGroundSplats (flat floor decals): the two differ only in how the 4
// corners/UVs and normal are computed, not in how the resulting quad
// reaches the GPU. slot is the caller's already-resolved b.slot(tex, nil)
// (AddTransparentSprites needs it early anyway, for its sort key, so
// resolving it again here would be redundant). If coalesce is true, this
// extends the immediately-preceding Draw's Count instead of starting a new
// one — callers set it when this quad shares both texture and (if
// relevant) depth bucket with the previous one; AddGroundSplats always
// passes false since it doesn't batch (see AddTransparentSprites for why
// splats don't need to: their count is always small).
func (b *Builder) appendTransparentQuad(tex *assets.RGBA, slot uint16, c [4]quadCorner, nx, ny, nz, light, alpha float32, coalesce bool) {
	mk := func(q quadCorner) Vert {
		return Vert{X: q.X, Y: q.Y, Z: q.Z, U: q.U, V: q.V, Light: light, Alpha: alpha, Nx: nx, Ny: ny, Nz: nz, Tex: slot, Kind: KindSpriteTrans}
	}
	first := len(b.g.Tris)
	b.g.Tris = append(b.g.Tris, mk(c[0]), mk(c[1]), mk(c[2]), mk(c[0]), mk(c[2]), mk(c[3]))
	if coalesce && len(b.g.TransparentDraws) > 0 {
		b.g.TransparentDraws[len(b.g.TransparentDraws)-1].Count += 6
		return
	}
	b.g.TransparentDraws = append(b.g.TransparentDraws, Draw{Tex: tex, Bright: b.g.Brightmaps[slot], First: first, Count: 6})
}

// sqDist is the squared distance from a Sprite's anchor to a camera
// position — cheap and order-preserving, which is all the back-to-front
// sort in AddTransparentSprites needs.
func sqDist(s Sprite, camX, camY, camZ float32) float32 {
	dx, dy, dz := s.X-camX, s.Y-camY, s.Z-camZ
	return dx*dx + dy*dy + dz*dz
}

// GroundSplat is a flat, floor-aligned quad — a shrinking puddle of liquid
// burning on the floor, for instance — unlike every other sprite type in
// this file, which are camera-facing billboards. A billboard viewed nearly
// edge-on (looking down at a floor puddle, which is the common case for
// something lying on the ground) collapses toward a thin line; a real flat
// decal reads correctly from any angle, the same way the floor's own
// texture does. Real alpha-blended (KindSpriteTrans) so it fades out and
// blends into whatever floor it's sitting on rather than a hard cutout.
type GroundSplat struct {
	X, Y, Z float32 // Z should already be nudged slightly above the floor by the caller to avoid z-fighting
	Radius  float32
	Rot     float32 // radians — vary per splat so a cluster of puddles doesn't look identical
	Light   float32
	Alpha   float32
	Tex     *assets.RGBA
}

// AddGroundSplats emits one flat quad per splat, lying on the XY plane
// (not billboarded) — see GroundSplat's doc comment for why floor decals
// need this instead of the billboard path every other Add* here uses.
// Appends to the same TransparentDraws list AddTransparentSprites does;
// call this first if both run the same frame so puddles land underneath
// whatever fire/smoke billboards are rising off them, not on top.
func (b *Builder) AddGroundSplats(splats []GroundSplat) {
	corners := [4][2]float32{{-1, -1}, {1, -1}, {1, 1}, {-1, 1}}
	uvs := [4][2]float32{{0, 1}, {1, 1}, {1, 0}, {0, 0}}
	for i := range splats {
		s := &splats[i]
		if s.Tex == nil || s.Tex.Width <= 0 || s.Tex.Height <= 0 || s.Alpha <= 0 || s.Radius <= 0 {
			continue
		}
		cr, sr := float32(math.Cos(float64(s.Rot))), float32(math.Sin(float64(s.Rot)))
		var c [4]quadCorner
		for k, o := range corners {
			lx, ly := o[0]*s.Radius, o[1]*s.Radius
			c[k] = quadCorner{X: s.X + lx*cr - ly*sr, Y: s.Y + lx*sr + ly*cr, Z: s.Z, U: uvs[k][0], V: uvs[k][1]}
		}
		b.appendTransparentQuad(s.Tex, b.slot(s.Tex, nil), c, 0, 0, 1, s.Light, s.Alpha, false)
	}
}

// AddVoxels sets this frame's voxel-model instances (replacing the last
// frame's). Call after Build, alongside AddSprites; the backend draws them
// with a dedicated pipeline (vertex colour, no texture) after the textured
// geometry, depth-tested against it.
func (b *Builder) AddVoxels(insts []VoxelInstance) {
	b.g.Voxels = append(b.g.Voxels[:0], insts...)
}

// slot returns t's index in g.Textures, adding it on first sight.
func (b *Builder) slot(t, bright *assets.RGBA) uint16 {
	if id, ok := b.texID[t]; ok {
		return id
	}
	id := uint16(len(b.g.Textures))
	b.g.Textures = append(b.g.Textures, t)
	b.g.Brightmaps = append(b.g.Brightmaps, bright)
	b.texID[t] = id
	return id
}

// emit appends one triangle to the bucket for its (texture slot, grid cell)
// — the masked set for KindMasked (alpha-tested 2-sided middles, kept out
// of the depth pre-pass), the water set for KindLiquidWater (kept apart so
// it can become its own Draw list — see Geometry.WaterDraws; lava/nukage
// are NOT split out, they stay in the opaque set like any other flat), the
// opaque set otherwise. b.curCell / b.curSector are set once per subsector
// by emitSubsectorIdx before any of its triangles reach here.
func (b *Builder) emit(a, c, d Vert) {
	a.Sector, c.Sector, d.Sector = b.curSector, b.curSector, b.curSector
	m := b.buckets
	switch a.Kind {
	case KindMasked:
		m = b.masked
	case KindLiquidWater:
		m = b.waterBuckets
	}
	byCell := m[a.Tex]
	if byCell == nil {
		byCell = make(map[int32][]Vert)
		m[a.Tex] = byCell
	}
	byCell[b.curCell] = append(byCell[b.curCell], a, c, d)
}

// emitPass picks which of a subsector's geometry emitSubsectorIdx emits:
// the static mesh (everything not tied to a SetLiveSectors sector) or the
// per-frame live geometry (exactly the rest).
type emitPass uint8

const (
	passStatic emitPass = iota
	passLive
)

func (b *Builder) emitSubsectorIdx(idx int, pass emitPass) {
	if idx < 0 || idx >= len(b.lvl.Subsectors) {
		return
	}
	ss := b.lvl.Subsectors[idx]
	first, n := int(ss.FirstSeg), int(ss.SegCount)
	if n <= 0 || first < 0 || first+n > len(b.lvl.Segs) {
		return
	}
	segs := b.lvl.Segs[first : first+n]

	frontSD, frontSec, _ := bsp.SegSides(b.lvl, segs[0])
	if frontSD == nil || frontSec == nil {
		return
	}

	// Flat polygon for this whole subsector (one convex region, one sector):
	// the BSP-reconstructed closed cell (bsp.SubsectorPolygons), falling
	// back to a fan of the seg boundary for the few slivers it can't form.
	poly := b.polys[idx]
	if poly == nil {
		b.fan = b.fan[:0]
		b.fan = append(b.fan, pt(b.lvl.Vertexes[segs[0].StartVertex]))
		for _, s := range segs {
			b.fan = append(b.fan, pt(b.lvl.Vertexes[s.EndVertex]))
		}
		if k := len(b.fan); k >= 2 && b.fan[k-1] == b.fan[0] {
			b.fan = b.fan[:k-1]
		}
		poly = b.fan
	}

	// This subsector's geometry all lands in one visGrid cell, chosen by its
	// polygon centroid — see visGrid.cellOf's doc for why a whole-subsector
	// assignment instead of per-vertex.
	var cx, cy float32
	for _, p := range poly {
		cx += p.X
		cy += p.Y
	}
	if n := float32(len(poly)); n > 0 {
		cx, cy = cx/n, cy/n
	}
	b.curCell = b.grid.cellOf(cx, cy)
	b.curSector = SectorTag(int(frontSD.Sector))

	if b.isLive(int(frontSD.Sector)) == (pass == passLive) {
		b.emitFlat(poly, frontSec, false) // floor, faces up
		if !b.SkipCeilings {
			b.emitFlat(poly, frontSec, true) // ceiling, faces down
		}
		if len(b.extraFloors) > 0 {
			b.emitExtraFloors(poly, int(frontSD.Sector))
		}
	}

	for _, s := range segs {
		if b.segTouchesLive(s) == (pass == passLive) {
			b.emitSegWalls(s)
		}
	}
}

// SectorTag is Vert.Sector / VoxelInstance.Sector for level sector sec:
// sec+1, or 0 (unmasked) past what the packed 16 bits can hold.
func SectorTag(sec int) uint16 {
	if sec < 0 || sec >= 0xFFFF {
		return 0
	}
	return uint16(sec + 1)
}

// pt converts a wad vertex to a bsp.Point (the seg-fan fallback path).
func pt(v wad.Vertex) bsp.Point { return bsp.Point{X: float32(v.X), Y: float32(v.Y)} }

// emitFlat fans poly into triangles at the sector's floor (ceiling=false)
// or ceiling (ceiling=true) height — a per-vertex plane evaluation
// (Sector.FloorZAt/CeilZAt), so a sloped sector fans into a tilted mesh
// automatically; every vanilla (untilted) sector still gets exactly the
// same flat polygon as before, since FloorZAt/CeilZAt degenerate to the
// scalar height there. Thin wrapper over emitFlatPlane — see its own doc
// comment for why room-over-room needs that lower-level shape directly.
func (b *Builder) emitFlat(poly []bsp.Point, sec *wad.Sector, ceiling bool) {
	name := sec.FloorTexture
	zAt := sec.FloorZAt
	tiltX, tiltY := sec.FloorTiltX, sec.FloorTiltY
	if ceiling {
		name = sec.CeilingTexture
		zAt = sec.CeilZAt
		tiltX, tiltY = sec.CeilTiltX, sec.CeilTiltY
	}
	light := float32(clampI(int(sec.LightLevel), 0, 255)) / 255
	b.emitFlatPlane(poly, name, zAt, tiltX, tiltY, !ceiling, light)
}

// emitFlatPlane fans poly into triangles at a given plane (zAt, tilted by
// tiltX/tiltY), textured by texName, facing up (faceUp — a floor-like
// surface, normal winding) or down (a ceiling-like surface, reversed
// winding so both faces point into the room). This is emitFlat's shared
// core, generalized so a room-over-room platform's top/bottom (below) can
// reuse it too: a platform's walkable TOP pairs a control sector's
// CEILING texture/height with an UP-facing surface, and its underside
// pairs the control's FLOOR with a DOWN-facing one — exactly the
// texture/facing mismatch emitFlat's own sec-plus-ceiling-bool shape can't
// express, since it always pairs a sector's floor with "up" and its
// ceiling with "down".
func (b *Builder) emitFlatPlane(poly []bsp.Point, texName string, zAt func(x, y float64) float64, tiltX, tiltY float64, faceUp bool, light float32) {
	if len(poly) < 3 {
		return
	}
	kind, tex := KindFlat, uint16(0)
	var tpu, tw, th float64 = 1, 64, 64
	if texName == skyFlatName {
		kind, tex = KindSky, SkyTex
	} else if rgba, ok := b.tex.Flat(texName); ok && rgba != nil {
		bm, _ := b.tex.Brightmap(texName, rgba)
		tex = b.slot(rgba, bm)
		tpu, tw, th = rgba.TexelsPerUnit, float64(rgba.Width), float64(rgba.Height)
		if faceUp {
			// Only an up-facing (floor-like) surface gets the liquid
			// treatment — a liquid "ceiling" would be an overhead pool,
			// which vanilla has none of and a platform's underside isn't.
			if lk := liquidKindOfFlat(texName); lk != KindFlat {
				kind = lk
			}
		}
		// Unlike liquids, a teleporter pad's ceiling is a real, common
		// setup — maps often sandwich the effect with the same GATE flat
		// on both floor and ceiling so the shimmer reads as a bounded
		// column, not just a lit floor — so this applies regardless of
		// facing (kind is still KindFlat here if the liquid check above
		// didn't already claim it).
		if kind == KindFlat && isTeleportFlat(texName) {
			kind = KindTeleport
		}
	} else {
		return // unresolved flat — nothing to draw (matches raster's nil-flat skip)
	}

	// Plane normal: (0,0,1) facing up / (0,0,-1) facing down (tiltX==
	// tiltY==0, the vanilla case) — otherwise the actual tilted plane's
	// normal, so a sloped surface lights correctly instead of reading as
	// flat-lit. The up hemisphere is (-tiltX,-tiltY,1) (unnormalized); the
	// down one is the sign-flipped (tiltX,tiltY,-1) — both reduce to the
	// vanilla ±Z normal at zero tilt.
	nx64, ny64, nz64 := -tiltX, -tiltY, 1.0
	if !faceUp {
		nx64, ny64, nz64 = tiltX, tiltY, -1.0
	}
	nlen := math.Sqrt(nx64*nx64 + ny64*ny64 + nz64*nz64)
	nx, ny, nz := float32(nx64/nlen), float32(ny64/nlen), float32(nz64/nlen)

	mk := func(p bsp.Point) Vert {
		x, y := float64(p.X), float64(p.Y)
		return Vert{
			X: p.X, Y: p.Y, Z: float32(zAt(x, y)),
			U: float32(x * tpu / tw), V: float32(y * tpu / th),
			Light: light, Nx: nx, Ny: ny, Nz: nz, Tex: tex, Kind: kind,
		}
	}
	v0 := mk(poly[0])
	for i := 1; i+1 < len(poly); i++ {
		a, c := mk(poly[i]), mk(poly[i+1])
		if faceUp {
			b.emit(v0, a, c)
		} else {
			b.emit(v0, c, a) // reversed
		}
	}
}

// emitExtraFloors emits secIdx's room-over-room platforms (if it has any —
// b.extraFloors[secIdx], set via SetExtraFloors): for each control sector,
// its ceiling as a walkable TOP (faces up, like a floor) and its floor as
// the underside acting as a CEILING (faces down) — both fanned over poly,
// the TARGET sector's own footprint, since a platform's horizontal extent
// is the whole target sector (the control sector only lends its vertical
// span + texture/light, not its own shape). SkipCeilings only suppresses
// the underside, matching how it treats an ordinary ceiling — the
// walkable top is never optional. See engine.ExtraFloor/planeAt for the
// runtime (collision/gameplay) side of this same convention.
func (b *Builder) emitExtraFloors(poly []bsp.Point, secIdx int) {
	for _, ctrlIdx := range b.extraFloors[secIdx] {
		if ctrlIdx < 0 || ctrlIdx >= len(b.lvl.Sectors) {
			continue
		}
		ctrl := &b.lvl.Sectors[ctrlIdx]
		light := float32(clampI(int(ctrl.LightLevel), 0, 255)) / 255
		b.emitFlatPlane(poly, ctrl.CeilingTexture, ctrl.CeilZAt, ctrl.CeilTiltX, ctrl.CeilTiltY, true, light)
		if !b.SkipCeilings {
			b.emitFlatPlane(poly, ctrl.FloorTexture, ctrl.FloorZAt, ctrl.FloorTiltX, ctrl.FloorTiltY, false, light)
		}
	}
}

func (b *Builder) emitSegWalls(seg wad.Seg) {
	frontSD, frontSec, backSec := bsp.SegSides(b.lvl, seg)
	if frontSD == nil || frontSec == nil {
		return
	}
	if int(seg.StartVertex) >= len(b.lvl.Vertexes) || int(seg.EndVertex) >= len(b.lvl.Vertexes) {
		return
	}
	v1 := b.lvl.Vertexes[seg.StartVertex]
	v2 := b.lvl.Vertexes[seg.EndVertex]
	x1, y1 := float64(v1.X), float64(v1.Y)
	x2, y2 := float64(v2.X), float64(v2.Y)
	segLen := math.Hypot(x2-x1, y2-y1)

	var flags uint16
	if int(seg.Linedef) < len(b.lvl.Linedefs) {
		flags = b.lvl.Linedefs[seg.Linedef].Flags
	}

	// Fake contrast: N-S faces a touch brighter than E-W (r_segs.c).
	wl := int(frontSec.LightLevel)
	switch {
	case y1 == y2:
		wl -= fakeContrast
	case x1 == x2:
		wl += fakeContrast
	}
	light := float32(clampI(wl, 0, 255)) / 255

	xBase := float64(frontSD.XOffset) + float64(seg.Offset)
	yOff := float64(frontSD.YOffset)
	// Per-endpoint plane evaluation: for a vanilla (untilted) sector these
	// two values are always equal, so every existing map's walls are
	// unaffected; a sloped sector's wall quad tapers between them instead
	// of assuming one shared height for the whole seg.
	fFloor1, fFloor2 := frontSec.FloorZAt(x1, y1), frontSec.FloorZAt(x2, y2)
	fCeil1, fCeil2 := frontSec.CeilZAt(x1, y1), frontSec.CeilZAt(x2, y2)

	if backSec == nil {
		if rgba, ok := b.tex.WallTexture(frontSD.MiddleTexture); ok && rgba != nil {
			bm, _ := b.tex.Brightmap(frontSD.MiddleTexture, rgba)
			anchor := fCeil1
			if flags&wad.LinedefLowerUnpegged != 0 {
				anchor = fFloor1 + texHeightUnits(rgba)
			}
			b.wallQuad(x1, y1, x2, y2, fFloor1, fFloor2, fCeil1, fCeil2, rgba, bm, anchor, yOff, xBase, segLen, light, KindWall)
		}
		return
	}

	bFloor1, bFloor2 := backSec.FloorZAt(x1, y1), backSec.FloorZAt(x2, y2)
	bCeil1, bCeil2 := backSec.CeilZAt(x1, y1), backSec.CeilZAt(x2, y2)

	// Upper: front ceiling steps down to back ceiling (needed at either end).
	if bCeil1 < fCeil1 || bCeil2 < fCeil2 {
		if rgba, ok := b.tex.WallTexture(frontSD.UpperTexture); ok && rgba != nil {
			bm, _ := b.tex.Brightmap(frontSD.UpperTexture, rgba)
			anchor := bCeil1 + texHeightUnits(rgba)
			if flags&wad.LinedefUpperUnpegged != 0 {
				anchor = fCeil1
			}
			b.wallQuad(x1, y1, x2, y2, bCeil1, bCeil2, fCeil1, fCeil2, rgba, bm, anchor, yOff, xBase, segLen, light, KindWall)
		} else if frontSec.CeilingTexture == skyFlatName {
			// Sky ceiling with no upper texture: the sky wraps over the step.
			b.wallQuad(x1, y1, x2, y2, bCeil1, bCeil2, fCeil1, fCeil2, nil, nil, 0, 0, xBase, segLen, light, KindSky)
		}
	}

	// Lower: back floor steps up above front floor (needed at either end).
	if bFloor1 > fFloor1 || bFloor2 > fFloor2 {
		if rgba, ok := b.tex.WallTexture(frontSD.LowerTexture); ok && rgba != nil {
			bm, _ := b.tex.Brightmap(frontSD.LowerTexture, rgba)
			anchor := bFloor1
			if flags&wad.LinedefLowerUnpegged != 0 {
				anchor = fCeil1
			}
			b.wallQuad(x1, y1, x2, y2, fFloor1, fFloor2, bFloor1, bFloor2, rgba, bm, anchor, yOff, xBase, segLen, light, KindWall)
		}
	}

	// Middle: a 2-sided line's masked texture (a fence, a grate), drawn as
	// one copy over the shared opening — no vertical tiling (KindMasked
	// discards V outside 0..1 in the shader).
	if rgba, ok := b.tex.WallTexture(frontSD.MiddleTexture); ok && rgba != nil {
		bm, _ := b.tex.Brightmap(frontSD.MiddleTexture, rgba)
		openBot1, openBot2 := math.Max(fFloor1, bFloor1), math.Max(fFloor2, bFloor2)
		openTop1, openTop2 := math.Min(fCeil1, bCeil1), math.Min(fCeil2, bCeil2)
		if openTop1 > openBot1 || openTop2 > openBot2 {
			anchor := openTop1
			if flags&wad.LinedefLowerUnpegged != 0 {
				anchor = openBot1 + texHeightUnits(rgba)
			}
			b.wallQuad(x1, y1, x2, y2, openBot1, openBot2, openTop1, openTop2, rgba, bm, anchor, yOff, xBase, segLen, light, KindMasked)
		}
	}
}

// wallQuad appends the two triangles for a wall piece: the seg (x1,y1)->
// (x2,y2) extruded from botZ1/botZ2 to topZ1/topZ2 — each endpoint's own
// bottom/top, so a wall against a sloped sector tapers between them
// instead of assuming one shared height (every vanilla, untilted sector
// always passes botZ1==botZ2 and topZ1==topZ2, so this is the same
// rectangle as before there). anchorZ is the world height the texture's
// row 0 maps to (id's rw_*texturemid); tex==nil means a sky quad (kind
// must be KindSky). U runs along the seg by map units * TPU / pixel width;
// V is (anchor - worldZ + yOff) * TPU / pixel height, evaluated at each
// vertex's own worldZ.
func (b *Builder) wallQuad(x1, y1, x2, y2, botZ1, botZ2, topZ1, topZ2 float64, tex, bright *assets.RGBA,
	anchorZ, yOff, xBase, segLen float64, light float32, kind uint16) {

	if topZ1 <= botZ1 && topZ2 <= botZ2 {
		return // degenerate at both ends — nothing to draw anywhere along the seg
	}
	slot := uint16(SkyTex)
	var tpu, tw, th float64 = 1, 1, 1
	if tex != nil {
		slot = b.slot(tex, bright)
		tpu, tw, th = tex.TexelsPerUnit, float64(tex.Width), float64(tex.Height)
	}
	u1 := float32(xBase * tpu / tw)
	u2 := float32((xBase + segLen) * tpu / tw)
	vAt := func(wz float64) float32 { return float32((anchorZ - wz + yOff) * tpu / th) }

	// Face normal: horizontal, perpendicular to the seg, pointing into the
	// front sector (toward the viewer). Doom subsector segs wind clockwise,
	// so the interior is the seg's right-hand side — normalize(dy, -dx). This
	// matches bsp.SubsectorPolygons' own interior test. A sloped wall's true
	// normal would also tilt slightly along the seg, but a wall quad is
	// still vertical at each end (only its top/bottom Z tapers) so this
	// horizontal normal remains exactly correct.
	ndx, ndy := x2-x1, y2-y1
	nlen := math.Hypot(ndx, ndy)
	var nx, ny float32
	if nlen > 0 {
		nx = float32(ndy / nlen)
		ny = float32(-ndx / nlen)
	}

	bz1, bz2, tz1, tz2 := float32(botZ1), float32(botZ2), float32(topZ1), float32(topZ2)
	A := Vert{X: float32(x1), Y: float32(y1), Z: bz1, U: u1, V: vAt(botZ1), Light: light, Nx: nx, Ny: ny, Tex: slot, Kind: kind}
	B := Vert{X: float32(x2), Y: float32(y2), Z: bz2, U: u2, V: vAt(botZ2), Light: light, Nx: nx, Ny: ny, Tex: slot, Kind: kind}
	C := Vert{X: float32(x2), Y: float32(y2), Z: tz2, U: u2, V: vAt(topZ2), Light: light, Nx: nx, Ny: ny, Tex: slot, Kind: kind}
	D := Vert{X: float32(x1), Y: float32(y1), Z: tz1, U: u1, V: vAt(topZ1), Light: light, Nx: nx, Ny: ny, Tex: slot, Kind: kind}
	b.emit(A, B, C)
	b.emit(A, C, D)
}

func texHeightUnits(t *assets.RGBA) float64 {
	if t.TexelsPerUnit == 0 {
		return float64(t.Height)
	}
	return float64(t.Height) / t.TexelsPerUnit
}

func clampI(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
