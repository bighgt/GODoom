// Package worldgeo turns a BSP-walked level into a flat triangle list with
// per-vertex texture / light / kind attributes — the backend-agnostic
// input a hardware (GPU-geometry) renderer needs, the same shape PrBoom-GL
// and GZDoom build from their BSP walk. It does no drawing: the CPU
// software rasterizer (package raster) is untouched and stays the default.
//
// The BSP is still walked on the CPU (front-to-back, for early-Z), but only
// to *collect* geometry; the GPU then does the per-pixel rasterization,
// perspective-correct texturing, filtering and depth testing.
package worldgeo

import (
	"math"

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
	KindWall       uint16 = iota // opaque wall piece: sample Tex, sector fade
	KindMasked                   // 2-sided middle texture: alpha-test, one copy (no vertical tile)
	KindFlat                     // floor / ceiling: sample Tex, sector fade
	KindSky                      // F_SKY1 flat or a sky wall-step: shader paints sky, still writes depth
	KindSprite                   // camera-facing billboard (thing / monster / item): alpha-test, sector fade
	KindSpriteFull               // billboard, full bright (projectile / muzzle flash / lit powerup)
	KindLiquidWater              // water floor flat: fresnel sky reflection (world.frag)
	KindLiquidLava               // lava floor flat: warm emissive glow
	KindLiquidNukage             // nukage/slime floor flat: green emissive tint
)

// liquidKindOfFlat classifies a floor flat's lump name for the liquid
// surface treatments world.frag applies (a mirrored-sky reflection on
// water, a self-lit glow on lava/nukage). It keys off the family prefix so
// it still matches whichever animation frame is showing — mirrors
// raster.classifyFloorLiquid and engine's liquidKindOfFlat. Returns
// KindFlat for anything that is not a liquid.
func liquidKindOfFlat(name string) uint16 {
	switch {
	case len(name) >= 6 && name[:6] == "FWATER":
		return KindLiquidWater
	case (len(name) >= 4 && name[:4] == "LAVA") || name == "FIRELAVA":
		return KindLiquidLava
	case (len(name) >= 6 && name[:6] == "NUKAGE") || (len(name) >= 5 && name[:5] == "SLIME"):
		return KindLiquidNukage
	}
	return KindFlat
}

// SkyTex is Vert.Tex for a KindSky vertex (it indexes nothing in Textures).
const SkyTex = 0xFFFF

// Vert is one triangle-list vertex: world position (map units, Z up), the
// texture coordinate (0..1 per texture tile — a REPEAT sampler tiles it),
// the sector light 0..1 (fake-contrast already folded in for walls), the
// unit surface normal (world space — walls face into their sector, floors
// up, ceilings down, billboards at the camera) that the dynamic point
// lights use for a Lambert term, the texture slot (builder bookkeeping; not
// in the packed GPU layout), and the kind.
type Vert struct {
	X, Y, Z    float32
	U, V       float32
	Light      float32
	Nx, Ny, Nz float32
	Tex        uint16
	Kind       uint16
}

// Draw is one contiguous run of Geometry.Tris that shares a texture: the
// backend binds Tex (nil = the sky texture) once and issues a single
// vkCmdDraw of Count vertices starting at First. Runs are in Textures
// order, sky last. Bright is the run's brightmap mask (nil = none — the
// backend binds a black dummy), resolved once per texture like Tex.
type Draw struct {
	Tex    *assets.RGBA
	Bright *assets.RGBA
	First  int
	Count  int
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
	// StaticOpaqueCount is the vertex index in StaticTris where the alpha-
	// tested 2-sided middle textures begin (opaque walls/flats/sky come
	// first). A backend depth pre-pass draws StaticTris[:StaticOpaqueCount]
	// only, so grate holes don't write depth.
	StaticOpaqueCount int

	Tris  []Vert // dynamic: sprite + voxel-billboard quads, refilled per frame
	Draws []Draw

	Textures []*assets.RGBA
	// Brightmaps is parallel to Textures (same slot index): the brightmap
	// mask for that texture, or nil. Populated by slot().
	Brightmaps []*assets.RGBA
	Sky        *assets.RGBA

	// Voxels is this frame's voxel-model instances (AddVoxels), drawn by the
	// backend with their own pipeline after the textured geometry.
	Voxels []VoxelInstance
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
	lvl     *wad.Level
	tree    bsp.Node
	tex     *assets.Textures
	texID   map[*assets.RGBA]uint16
	g       Geometry
	polys   [][]bsp.Point     // per subsector: the closed flat polygon (nil for a few slivers)
	fan     []bsp.Point       // reused scratch for the seg-fan fallback
	buckets map[uint16][]Vert // opaque static: per texture slot (SkyTex for sky)
	masked  map[uint16][]Vert // static KindMasked (2-sided middles): per texture slot
	sprites map[uint16][]Vert // per texture slot, AddSprites billboards — reused across calls
}

// New binds a builder to a level. tex resolves texture / flat names to the
// shared *assets.RGBA bitmaps (stable pointers — used as the Textures key).
// The per-subsector flat polygons are reconstructed here, and the whole
// level's static geometry is built once (see Invalidate).
func New(lvl *wad.Level, tree bsp.Node, tex *assets.Textures) *Builder {
	b := &Builder{
		lvl: lvl, tree: tree, tex: tex,
		texID:   make(map[*assets.RGBA]uint16),
		polys:   bsp.SubsectorPolygons(tree, lvl),
		buckets: make(map[uint16][]Vert),
		masked:  make(map[uint16][]Vert),
		sprites: make(map[uint16][]Vert),
	}
	// Resolve the per-map WAD sky (a wall texture named SKY1..4) once; a
	// caller can still override it with a panorama via SetSky.
	if sky, ok := tex.WallTexture(skyTextureForMap(lvl.Name)); ok && sky != nil {
		b.g.Sky = sky
	}
	b.Invalidate()
	return b
}

// Invalidate rebuilds the static world geometry (all subsectors' walls and
// flats) and bumps StaticVersion so the backend re-uploads it. Call after a
// door / lift / floor mover changes a sector's height — otherwise the
// per-frame Build reuses the cached list untouched.
func (b *Builder) Invalidate() {
	for k := range b.buckets {
		b.buckets[k] = b.buckets[k][:0]
	}
	for k := range b.masked {
		b.masked[k] = b.masked[k][:0]
	}
	for i := range b.lvl.Subsectors {
		b.emitSubsectorIdx(i)
	}
	// Concatenate into one triangle list: opaque per-texture runs, then the
	// sky run, then (after StaticOpaqueCount) the alpha-tested 2-sided
	// middle-texture runs. One Draw per run.
	b.g.StaticTris = b.g.StaticTris[:0]
	b.g.StaticDraws = b.g.StaticDraws[:0]
	for slot := uint16(0); int(slot) < len(b.g.Textures); slot++ {
		bk := b.buckets[slot]
		if len(bk) == 0 {
			continue
		}
		b.g.StaticDraws = append(b.g.StaticDraws, Draw{Tex: b.g.Textures[slot], Bright: b.g.Brightmaps[slot], First: len(b.g.StaticTris), Count: len(bk)})
		b.g.StaticTris = append(b.g.StaticTris, bk...)
	}
	if sky := b.buckets[SkyTex]; len(sky) > 0 {
		b.g.StaticDraws = append(b.g.StaticDraws, Draw{Tex: nil, First: len(b.g.StaticTris), Count: len(sky)})
		b.g.StaticTris = append(b.g.StaticTris, sky...)
	}
	b.g.StaticOpaqueCount = len(b.g.StaticTris)
	for slot := uint16(0); int(slot) < len(b.g.Textures); slot++ {
		bk := b.masked[slot]
		if len(bk) == 0 {
			continue
		}
		b.g.StaticDraws = append(b.g.StaticDraws, Draw{Tex: b.g.Textures[slot], Bright: b.g.Brightmaps[slot], First: len(b.g.StaticTris), Count: len(bk)})
		b.g.StaticTris = append(b.g.StaticTris, bk...)
	}
	// Process-monotonic so a fresh builder (level change) and a door move
	// both hand the backend a version it hasn't uploaded yet.
	staticVersionSeq++
	b.g.StaticVersion = staticVersionSeq
}

var staticVersionSeq uint32

// Build clears the per-frame dynamic geometry (AddSprites / AddVoxels refill
// it) and returns the Geometry. The static world part is untouched — no BSP
// walk, no per-frame triangle rebuild. The camera args are unused now (the
// whole level's geometry is submitted and the GPU depth-tests it); kept for
// call-site stability.
func (b *Builder) Build(_, _ float32) *Geometry {
	b.g.Tris = b.g.Tris[:0]
	b.g.Draws = b.g.Draws[:0]
	b.g.Voxels = b.g.Voxels[:0]
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
}

// AddSprites appends camera-facing billboard quads for sprs to the geometry
// the most recent Build produced — one extra Draw per sprite texture, after
// the world (and sky) Draws. camAngle is the view yaw in radians. Call once
// per frame, after Build, before the backend consumes the *Geometry. The
// GPU depth test (the world pass already filled the depth buffer) sorts the
// billboards against the level and each other, so submission order here
// doesn't matter.
func (b *Builder) AddSprites(camAngle float64, sprs []Sprite) {
	for k := range b.sprites {
		b.sprites[k] = b.sprites[k][:0]
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
		b.sprites[slot] = append(b.sprites[slot], bl, br, tr, bl, tr, tl)
	}

	for slot := uint16(0); int(slot) < len(b.g.Textures); slot++ {
		bk := b.sprites[slot]
		if len(bk) == 0 {
			continue
		}
		b.g.Draws = append(b.g.Draws, Draw{Tex: b.g.Textures[slot], Bright: b.g.Brightmaps[slot], First: len(b.g.Tris), Count: len(bk)})
		b.g.Tris = append(b.g.Tris, bk...)
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

// emit appends one triangle to the bucket for its texture slot — the masked
// set for KindMasked (alpha-tested 2-sided middles, kept out of the depth
// pre-pass), the opaque set otherwise.
func (b *Builder) emit(a, c, d Vert) {
	if a.Kind == KindMasked {
		b.masked[a.Tex] = append(b.masked[a.Tex], a, c, d)
		return
	}
	b.buckets[a.Tex] = append(b.buckets[a.Tex], a, c, d)
}

func (b *Builder) emitSubsectorIdx(idx int) {
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

	b.emitFlat(poly, frontSec, false) // floor, faces up
	b.emitFlat(poly, frontSec, true)  // ceiling, faces down

	for _, s := range segs {
		b.emitSegWalls(s)
	}
}

// pt converts a wad vertex to a bsp.Point (the seg-fan fallback path).
func pt(v wad.Vertex) bsp.Point { return bsp.Point{X: float32(v.X), Y: float32(v.Y)} }

// emitFlat fans poly into triangles at the sector's floor (ceiling=false)
// or ceiling (ceiling=true) height. Winding is reversed for the ceiling so
// both faces point into the room (the pipeline can then backface-cull).
func (b *Builder) emitFlat(poly []bsp.Point, sec *wad.Sector, ceiling bool) {
	if len(poly) < 3 {
		return
	}
	name := sec.FloorTexture
	z := float32(sec.FloorHeight)
	if ceiling {
		name = sec.CeilingTexture
		z = float32(sec.CeilingHeight)
	}

	kind, tex := KindFlat, uint16(0)
	var tpu, tw, th float64 = 1, 64, 64
	if name == skyFlatName {
		kind, tex = KindSky, SkyTex
	} else if rgba, ok := b.tex.Flat(name); ok && rgba != nil {
		bm, _ := b.tex.Brightmap(name, rgba)
		tex = b.slot(rgba, bm)
		tpu, tw, th = rgba.TexelsPerUnit, float64(rgba.Width), float64(rgba.Height)
		if !ceiling {
			// Only floors get the liquid surface treatment (a liquid
			// "ceiling" would be an overhead pool — vanilla has none).
			if lk := liquidKindOfFlat(name); lk != KindFlat {
				kind = lk
			}
		}
	} else {
		return // unresolved flat — nothing to draw (matches raster's nil-flat skip)
	}
	light := float32(clampI(int(sec.LightLevel), 0, 255)) / 255
	nz := float32(1) // floor faces up
	if ceiling {
		nz = -1 // ceiling faces down
	}

	mk := func(p bsp.Point) Vert {
		x, y := float64(p.X), float64(p.Y)
		return Vert{
			X: p.X, Y: p.Y, Z: z,
			U: float32(x * tpu / tw), V: float32(y * tpu / th),
			Light: light, Nz: nz, Tex: tex, Kind: kind,
		}
	}
	v0 := mk(poly[0])
	for i := 1; i+1 < len(poly); i++ {
		a, c := mk(poly[i]), mk(poly[i+1])
		if ceiling {
			b.emit(v0, c, a) // reversed
		} else {
			b.emit(v0, a, c)
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
	fFloor, fCeil := float64(frontSec.FloorHeight), float64(frontSec.CeilingHeight)

	if backSec == nil {
		if rgba, ok := b.tex.WallTexture(frontSD.MiddleTexture); ok && rgba != nil {
			bm, _ := b.tex.Brightmap(frontSD.MiddleTexture, rgba)
			anchor := fCeil
			if flags&wad.LinedefLowerUnpegged != 0 {
				anchor = fFloor + texHeightUnits(rgba)
			}
			b.wallQuad(x1, y1, x2, y2, fFloor, fCeil, rgba, bm, anchor, yOff, xBase, segLen, light, KindWall)
		}
		return
	}

	bFloor, bCeil := float64(backSec.FloorHeight), float64(backSec.CeilingHeight)

	// Upper: front ceiling steps down to back ceiling.
	if bCeil < fCeil {
		if rgba, ok := b.tex.WallTexture(frontSD.UpperTexture); ok && rgba != nil {
			bm, _ := b.tex.Brightmap(frontSD.UpperTexture, rgba)
			anchor := bCeil + texHeightUnits(rgba)
			if flags&wad.LinedefUpperUnpegged != 0 {
				anchor = fCeil
			}
			b.wallQuad(x1, y1, x2, y2, bCeil, fCeil, rgba, bm, anchor, yOff, xBase, segLen, light, KindWall)
		} else if frontSec.CeilingTexture == skyFlatName {
			// Sky ceiling with no upper texture: the sky wraps over the step.
			b.wallQuad(x1, y1, x2, y2, bCeil, fCeil, nil, nil, 0, 0, xBase, segLen, light, KindSky)
		}
	}

	// Lower: back floor steps up above front floor.
	if bFloor > fFloor {
		if rgba, ok := b.tex.WallTexture(frontSD.LowerTexture); ok && rgba != nil {
			bm, _ := b.tex.Brightmap(frontSD.LowerTexture, rgba)
			anchor := bFloor
			if flags&wad.LinedefLowerUnpegged != 0 {
				anchor = fCeil
			}
			b.wallQuad(x1, y1, x2, y2, fFloor, bFloor, rgba, bm, anchor, yOff, xBase, segLen, light, KindWall)
		}
	}

	// Middle: a 2-sided line's masked texture (a fence, a grate), drawn as
	// one copy over the shared opening — no vertical tiling (KindMasked
	// discards V outside 0..1 in the shader).
	if rgba, ok := b.tex.WallTexture(frontSD.MiddleTexture); ok && rgba != nil {
		bm, _ := b.tex.Brightmap(frontSD.MiddleTexture, rgba)
		openBot := math.Max(fFloor, bFloor)
		openTop := math.Min(fCeil, bCeil)
		if openTop > openBot {
			anchor := openTop
			if flags&wad.LinedefLowerUnpegged != 0 {
				anchor = openBot + texHeightUnits(rgba)
			}
			b.wallQuad(x1, y1, x2, y2, openBot, openTop, rgba, bm, anchor, yOff, xBase, segLen, light, KindMasked)
		}
	}
}

// wallQuad appends the two triangles for a wall piece: the seg (x1,y1)->
// (x2,y2) extruded from botZ to topZ. anchorZ is the world height the
// texture's row 0 maps to (id's rw_*texturemid); tex==nil means a sky
// quad (kind must be KindSky). U runs along the seg by map units * TPU /
// pixel width; V is (anchor - worldZ + yOff) * TPU / pixel height.
func (b *Builder) wallQuad(x1, y1, x2, y2, botZ, topZ float64, tex, bright *assets.RGBA,
	anchorZ, yOff, xBase, segLen float64, light float32, kind uint16) {

	if topZ <= botZ {
		return
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
	vBot, vTop := vAt(botZ), vAt(topZ)

	// Face normal: horizontal, perpendicular to the seg, pointing into the
	// front sector (toward the viewer). Doom subsector segs wind clockwise,
	// so the interior is the seg's right-hand side — normalize(dy, -dx). This
	// matches bsp.SubsectorPolygons' own interior test.
	ndx, ndy := x2-x1, y2-y1
	nlen := math.Hypot(ndx, ndy)
	var nx, ny float32
	if nlen > 0 {
		nx = float32(ndy / nlen)
		ny = float32(-ndx / nlen)
	}

	bz, tz := float32(botZ), float32(topZ)
	A := Vert{X: float32(x1), Y: float32(y1), Z: bz, U: u1, V: vBot, Light: light, Nx: nx, Ny: ny, Tex: slot, Kind: kind}
	B := Vert{X: float32(x2), Y: float32(y2), Z: bz, U: u2, V: vBot, Light: light, Nx: nx, Ny: ny, Tex: slot, Kind: kind}
	C := Vert{X: float32(x2), Y: float32(y2), Z: tz, U: u2, V: vTop, Light: light, Nx: nx, Ny: ny, Tex: slot, Kind: kind}
	D := Vert{X: float32(x1), Y: float32(y1), Z: tz, U: u1, V: vTop, Light: light, Nx: nx, Ny: ny, Tex: slot, Kind: kind}
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
