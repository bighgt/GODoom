package editor

import (
	"math"

	"twopointfive/bsp"
	"twopointfive/nodebuild"
	"twopointfive/wad"
)

// Doc is the map being edited. Editing mutates Doc.Level's raw wad slices
// directly. The 3D view renders a SEPARATE render view (Doc.render) built by
// the node builder — a shallow copy of Level with the built Segs /
// Subsectors / (Level.Vertexes + split points). Keeping them apart means an
// edit never has to reason about which vertices the builder appended.
type Doc struct {
	WADPath string
	MapName string
	WAD     *wad.WAD // nil for a New Level that isn't from a WAD
	Level   *wad.Level

	render     wad.Level // what view3d renders (built geometry)
	tree       bsp.Node  // BSP over `render`; nil if the build failed with no fallback
	treeErr    error
	useShipped bool
	geomDirty  bool // geometry changed since the last rebuild

	Sel selection
}

// selection holds selected element indices per kind.
type selection struct {
	Verts   map[int]bool
	Lines   map[int]bool
	Sectors map[int]bool
	Things  map[int]bool
}

func newSelection() selection {
	return selection{Verts: map[int]bool{}, Lines: map[int]bool{}, Sectors: map[int]bool{}, Things: map[int]bool{}}
}

func (s *selection) clearAll() {
	clear(s.Verts)
	clear(s.Lines)
	clear(s.Sectors)
	clear(s.Things)
}

// selectOne clears every selection set and selects a single element of the
// given kind.
func (s *selection) selectOne(m editMode, idx int) {
	s.clearAll()
	switch m {
	case modeVerts:
		s.Verts[idx] = true
	case modeLines:
		s.Lines[idx] = true
	case modeSectors:
		s.Sectors[idx] = true
	case modeThings:
		s.Things[idx] = true
	}
}

// NewDoc loads mapName from w for editing.
func NewDoc(w *wad.WAD, wadPath, mapName string) (*Doc, error) {
	lvl, err := w.LoadLevel(mapName)
	if err != nil {
		return nil, err
	}
	d := &Doc{WADPath: wadPath, MapName: mapName, WAD: w, Level: lvl, Sel: newSelection()}
	d.rebuildTree()
	return d, nil
}

// NewEmptyDoc makes a fresh map: one square room (roomSize x roomSize
// centred on the origin), textured, lit, with a player-1 start in the
// middle. bank supplies default texture names that actually exist in the
// asset set. It's a real, immediately playable level — the starting point
// for building from scratch.
func NewEmptyDoc(bank *Bank, wadPath string) *Doc {
	const roomSize = 512
	h := roomSize / 2
	wallTex := firstOf(bank.Walls, "STARTAN2", "STARTAN3", "BROWN96", "BRICK7")
	floorTex := firstOf(bank.Flats, "FLOOR4_8", "FLAT5_4", "FLOOR0_1", "CEIL1_1")
	ceilTex := firstOf(bank.Flats, "CEIL3_5", "FLAT2", "CEIL1_1", "FLOOR4_8")

	lvl := &wad.Level{
		Name: "MAP01",
		// Clockwise winding (in y-up map space) so each one-sided linedef's
		// front sidedef — Doom's "right of start→end" — faces INTO the room.
		Vertexes: []wad.Vertex{
			{X: int16(-h), Y: int16(-h)}, {X: int16(-h), Y: int16(h)},
			{X: int16(h), Y: int16(h)}, {X: int16(h), Y: int16(-h)},
		},
		Sectors: []wad.Sector{{
			FloorHeight: 0, CeilingHeight: 128,
			FloorTexture: floorTex, CeilingTexture: ceilTex,
			LightLevel: 192,
		}},
		Sidedefs: []wad.Sidedef{
			{MiddleTexture: wallTex, Sector: 0},
			{MiddleTexture: wallTex, Sector: 0},
			{MiddleTexture: wallTex, Sector: 0},
			{MiddleTexture: wallTex, Sector: 0},
		},
		Linedefs: []wad.Linedef{
			{StartVertex: 0, EndVertex: 1, Flags: 1, FrontSidedef: 0, BackSidedef: wad.NoSidedef},
			{StartVertex: 1, EndVertex: 2, Flags: 1, FrontSidedef: 1, BackSidedef: wad.NoSidedef},
			{StartVertex: 2, EndVertex: 3, Flags: 1, FrontSidedef: 2, BackSidedef: wad.NoSidedef},
			{StartVertex: 3, EndVertex: 0, Flags: 1, FrontSidedef: 3, BackSidedef: wad.NoSidedef},
		},
		Things: []wad.Thing{{X: 0, Y: 0, Angle: 90, Type: 1, Flags: 7}},
	}
	d := &Doc{WADPath: wadPath, MapName: "MAP01", Level: lvl, Sel: newSelection()}
	d.rebuildTree()
	return d
}

// markDirty flags the geometry as changed; the app rebuilds the BSP after
// the frame (debounced by the caller if it wants).
func (d *Doc) markDirty() { d.geomDirty = true }

// RebuildIfDirty rebuilds the render/BSP when geometry changed. Cheap to
// call every frame — it's a no-op unless markDirty ran.
func (d *Doc) RebuildIfDirty() {
	if d.geomDirty {
		d.rebuildTree()
	}
}

// rebuildTree runs the node builder over Level into a fresh render view and
// its BSP. On builder failure it tries the WAD's shipped NODES; failing
// that it keeps the last good tree and records the error.
func (d *Doc) rebuildTree() {
	d.geomDirty = false
	res, err := nodebuild.Build(d.Level)
	if err == nil {
		d.render = *d.Level
		d.render.Vertexes = res.Verts
		d.render.Segs = res.Segs
		d.render.Subsectors = res.Subsectors
		d.tree, d.treeErr, d.useShipped = res.Root, nil, false
		return
	}
	if len(d.Level.Nodes) > 0 {
		if t, serr := bsp.Build(d.Level); serr == nil {
			d.render = *d.Level
			d.tree, d.treeErr, d.useShipped = t, err, true
			return
		}
	}
	d.treeErr = err
}

// RenderLevel is the geometry the 3D view should draw.
func (d *Doc) RenderLevel() *wad.Level { return &d.render }

// Bounds returns the map's vertex bounding box (a default box if empty).
func (d *Doc) Bounds() (minX, minY, maxX, maxY int) {
	if len(d.Level.Vertexes) == 0 {
		return -512, -512, 512, 512
	}
	minX, minY, maxX, maxY = 1<<30, 1<<30, -(1 << 30), -(1 << 30)
	for _, v := range d.Level.Vertexes {
		minX = min(minX, int(v.X))
		minY = min(minY, int(v.Y))
		maxX = max(maxX, int(v.X))
		maxY = max(maxY, int(v.Y))
	}
	return
}

// ---- element geometry / centroids (for the 2D→3D "jump camera" and hit-testing)

// ElementCenter returns the world-space centre of element idx of the given
// kind, and its floor height, for moving the 3D camera to it.
func (d *Doc) ElementCenter(m editMode, idx int) (x, y, floorZ float64, ok bool) {
	lvl := d.Level
	switch m {
	case modeVerts:
		if idx < 0 || idx >= len(lvl.Vertexes) {
			return
		}
		v := lvl.Vertexes[idx]
		x, y = float64(v.X), float64(v.Y)
	case modeLines:
		if idx < 0 || idx >= len(lvl.Linedefs) {
			return
		}
		ld := lvl.Linedefs[idx]
		if int(ld.StartVertex) >= len(lvl.Vertexes) || int(ld.EndVertex) >= len(lvl.Vertexes) {
			return
		}
		a, b := lvl.Vertexes[ld.StartVertex], lvl.Vertexes[ld.EndVertex]
		x, y = float64(a.X+b.X)/2, float64(a.Y+b.Y)/2
	case modeThings:
		if idx < 0 || idx >= len(lvl.Things) {
			return
		}
		t := lvl.Things[idx]
		x, y = float64(t.X), float64(t.Y)
	case modeSectors:
		cx, cy, n := 0.0, 0.0, 0
		for li := range lvl.Linedefs {
			ld := lvl.Linedefs[li]
			if !d.lineTouchesSector(ld, idx) {
				continue
			}
			if int(ld.StartVertex) < len(lvl.Vertexes) {
				v := lvl.Vertexes[ld.StartVertex]
				cx += float64(v.X)
				cy += float64(v.Y)
				n++
			}
		}
		if n == 0 {
			return
		}
		x, y = cx/float64(n), cy/float64(n)
	}
	floorZ = d.floorAt(x, y)
	return x, y, floorZ, true
}

func (d *Doc) lineTouchesSector(ld wad.Linedef, sec int) bool {
	for _, sdIdx := range [2]uint16{ld.FrontSidedef, ld.BackSidedef} {
		if sdIdx != wad.NoSidedef && int(sdIdx) < len(d.Level.Sidedefs) &&
			int(d.Level.Sidedefs[sdIdx].Sector) == sec {
			return true
		}
	}
	return false
}

func (d *Doc) floorAt(x, y float64) float64 {
	if d.tree == nil {
		return 0
	}
	if s := bsp.PointSector(d.tree, &d.render, float32(x), float32(y)); s != nil {
		return float64(s.FloorHeight)
	}
	if len(d.Level.Sectors) > 0 {
		return float64(d.Level.Sectors[0].FloorHeight)
	}
	return 0
}

// lightAt is floorAt's sibling for a sector's light level, 0..1 — used to
// shade a thing's preview sprite in the 3D view about as bright as it'll
// actually look in-game.
func (d *Doc) lightAt(x, y float64) float64 {
	const fallback = 176.0 / 255.0 // NewEmptyDoc's own default sector light
	if d.tree == nil {
		return fallback
	}
	if s := bsp.PointSector(d.tree, &d.render, float32(x), float32(y)); s != nil {
		return float64(s.LightLevel) / 255
	}
	if len(d.Level.Sectors) > 0 {
		return float64(d.Level.Sectors[0].LightLevel) / 255
	}
	return fallback
}

// ---- editing operations ------------------------------------------------

// AddThing places a THING of doomednum num at (x, y), facing angle
// (degrees), and selects it. Returns its index.
func (d *Doc) AddThing(num, x, y, angle int) int {
	d.Level.Things = append(d.Level.Things, wad.Thing{
		X: int16(x), Y: int16(y), Angle: uint16(angle & 0x1ff), Type: uint16(num), Flags: 7,
	})
	i := len(d.Level.Things) - 1
	d.Sel.selectOne(modeThings, i)
	return i
}

// SetLineTexture assigns a wall-texture name to linedef idx. For a
// one-sided line it sets the middle texture on the front sidedef; for a
// two-sided line it sets the lower+upper on the near side. flat is ignored
// here (flats go on sectors — see SetSectorFlat).
func (d *Doc) SetLineTexture(idx int, name string) bool {
	lvl := d.Level
	if idx < 0 || idx >= len(lvl.Linedefs) {
		return false
	}
	ld := lvl.Linedefs[idx]
	sd := ld.FrontSidedef
	if sd == wad.NoSidedef || int(sd) >= len(lvl.Sidedefs) {
		return false
	}
	s := &lvl.Sidedefs[sd]
	if ld.BackSidedef == wad.NoSidedef {
		s.MiddleTexture = name
	} else {
		s.LowerTexture = name
		s.UpperTexture = name
	}
	// view3d's hardware renderer bakes textures into a static-geometry cache
	// keyed off the BSP tree's identity (see rebuildTree); without this, a
	// texture-only edit never gets a new tree, so the change never shows.
	d.markDirty()
	return true
}

// SetSectorFlat assigns a flat name to sector idx (floor by default,
// ceiling if ceil is true).
func (d *Doc) SetSectorFlat(idx int, name string, ceil bool) bool {
	if idx < 0 || idx >= len(d.Level.Sectors) {
		return false
	}
	if ceil {
		d.Level.Sectors[idx].CeilingTexture = name
	} else {
		d.Level.Sectors[idx].FloorTexture = name
	}
	d.markDirty() // see SetLineTexture's comment on why this is needed
	return true
}

// firstOf returns the first of prefs that appears in have, else prefs[0]
// (so a New Level still has *some* name even against an odd asset set).
func firstOf(have []string, prefs ...string) string {
	set := map[string]bool{}
	for _, h := range have {
		set[h] = true
	}
	for _, p := range prefs {
		if set[p] {
			return p
		}
	}
	if len(prefs) > 0 {
		return prefs[0]
	}
	return "-"
}

var _ = math.Hypot
