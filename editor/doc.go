package editor

import (
	"log"
	"math"
	"sort"
	"strings"

	"twopointfive/bsp"
	"twopointfive/engine/slopes"
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

	// extraFloors are this map's room-over-room links — target sector
	// index to the control sector(s) whose floor/ceiling become a
	// floating platform inside it (see engine/slopes' own doc comment for
	// the concept, and AddExtraFloor/RemoveExtraFloor below). Populated
	// from any SLOPES lump in the source WAD on load (loadSlopes), written
	// back out by SavePWAD (see encodeSlopes in save.go), and read by
	// view3d (extraFloorMap) to feed worldgeo.Builder.SetExtraFloors.
	extraFloors []slopes.ExtraFloorDef
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
	d.loadSlopes()
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

// ---- sector/linedef properties -----------------------------------------

// SetSectorHeights sets sector idx's floor/ceiling height — geometry, so
// this marks the render tree dirty the same way SetSectorFlat/
// SetLineTexture do.
func (d *Doc) SetSectorHeights(idx int, floor, ceiling int16) bool {
	if idx < 0 || idx >= len(d.Level.Sectors) {
		return false
	}
	d.Level.Sectors[idx].FloorHeight = floor
	d.Level.Sectors[idx].CeilingHeight = ceiling
	d.markDirty()
	return true
}

// SetSectorLight sets sector idx's light level (0..255) — also baked into
// the render tree's per-vertex shading, so this marks it dirty too.
func (d *Doc) SetSectorLight(idx int, light int16) bool {
	if idx < 0 || idx >= len(d.Level.Sectors) {
		return false
	}
	d.Level.Sectors[idx].LightLevel = light
	d.markDirty()
	return true
}

// SetSectorSpecial/SetSectorTag set sector idx's special-behavior type and
// tag — pure gameplay data with no effect on the built render tree, so
// unlike the geometry/light setters above these don't markDirty.
func (d *Doc) SetSectorSpecial(idx int, special uint16) bool {
	if idx < 0 || idx >= len(d.Level.Sectors) {
		return false
	}
	d.Level.Sectors[idx].SpecialType = special
	return true
}

func (d *Doc) SetSectorTag(idx int, tag uint16) bool {
	if idx < 0 || idx >= len(d.Level.Sectors) {
		return false
	}
	d.Level.Sectors[idx].Tag = tag
	return true
}

// SetLineFlags/SetLineSpecial/SetLineTag set linedef idx's Flags/
// SpecialType/SectorTag — like the sector special/tag setters above, pure
// gameplay data, no render-tree effect.
func (d *Doc) SetLineFlags(idx int, flags uint16) bool {
	if idx < 0 || idx >= len(d.Level.Linedefs) {
		return false
	}
	d.Level.Linedefs[idx].Flags = flags
	return true
}

func (d *Doc) SetLineSpecial(idx int, special uint16) bool {
	if idx < 0 || idx >= len(d.Level.Linedefs) {
		return false
	}
	d.Level.Linedefs[idx].SpecialType = special
	return true
}

func (d *Doc) SetLineTag(idx int, tag uint16) bool {
	if idx < 0 || idx >= len(d.Level.Linedefs) {
		return false
	}
	d.Level.Linedefs[idx].SectorTag = tag
	return true
}

// ---- slopes & room-over-room ---------------------------------------------

// loadSlopes reads this WAD's SLOPES lump (if any — see engine/slopes' own
// doc comment for the grammar) and applies any plane/extra-floor entries
// for this map: tilts go straight onto Level.Sectors (the same fields
// wad.Sector.FloorZAt/CeilZAt already read — no separate render-side step
// needed, unlike extra floors), and extra-floor links populate
// d.extraFloors (read by view3d's extraFloorMap). A no-op, not an error, if
// the WAD has no SLOPES lump or none of its entries are for this map,
// matching every other optional-sidecar convention in this codebase
// (GLDEFS, ANIMDEFS, ...).
func (d *Doc) loadSlopes() {
	if d.WAD == nil {
		return
	}
	data, ok := d.WAD.Find("SLOPES")
	if !ok {
		return
	}
	planes, floors, errs := slopes.Parse(data)
	for _, e := range errs {
		log.Printf("editor: SLOPES: %v", e)
	}
	mapName := strings.ToUpper(d.MapName)
	for _, def := range planes[mapName] {
		applySlopePlane(d.Level, def)
	}
	d.extraFloors = append([]slopes.ExtraFloorDef(nil), floors[mapName]...)
}

// applySlopePlane mutates sec's tilt fields from def — the same math as
// engine/slopes_load.go's applySlopes/planeTilt, duplicated here (not
// imported: that lives in package engine, which the editor doesn't
// otherwise depend on) following this codebase's own established
// convention of each package keeping its own tiny copy of a small, stable
// helper like this rather than pulling in a whole unrelated package for it.
func applySlopePlane(lvl *wad.Level, def slopes.Def) {
	if def.SectorIndex < 0 || def.SectorIndex >= len(lvl.Sectors) {
		return
	}
	sec := &lvl.Sectors[def.SectorIndex]
	tiltX, tiltY, ok := planeTilt(def.P1, def.P2, def.P3)
	if !ok {
		return
	}
	sec.SlopePivotX, sec.SlopePivotY = def.P1[0], def.P1[1]
	if def.Ceiling {
		sec.CeilingHeight = int16(math.Round(def.P1[2]))
		sec.CeilTiltX, sec.CeilTiltY = tiltX, tiltY
	} else {
		sec.FloorHeight = int16(math.Round(def.P1[2]))
		sec.FloorTiltX, sec.FloorTiltY = tiltX, tiltY
	}
}

// planeTilt solves for the (tiltX, tiltY) of the plane through p1, p2, p3
// that passes through p1 at that exact height — see
// engine/slopes_load.go's own copy of this for the full derivation.
func planeTilt(p1, p2, p3 [3]float64) (tiltX, tiltY float64, ok bool) {
	dx2, dy2, dz2 := p2[0]-p1[0], p2[1]-p1[1], p2[2]-p1[2]
	dx3, dy3, dz3 := p3[0]-p1[0], p3[1]-p1[1], p3[2]-p1[2]
	det := dx2*dy3 - dx3*dy2
	if det == 0 {
		return 0, 0, false
	}
	tiltX = (dz2*dy3 - dz3*dy2) / det
	tiltY = (dx2*dz3 - dx3*dz2) / det
	return tiltX, tiltY, true
}

// SetSectorTilt sets sector idx's floor/ceiling plane tilt directly — the
// same FloorTiltX/Y, CeilTiltX/Y, SlopePivotX/Y fields wad.Sector.FloorZAt/
// CeilZAt already read, so a markDirty (the plane shape is part of the
// built geometry) is the only render-side step needed. tiltX/tiltY are
// rise per map unit along X/Y, evaluated relative to (pivotX, pivotY); 0/0
// is flat, matching every vanilla sector.
func (d *Doc) SetSectorTilt(idx int, floorTiltX, floorTiltY, ceilTiltX, ceilTiltY, pivotX, pivotY float64) bool {
	if idx < 0 || idx >= len(d.Level.Sectors) {
		return false
	}
	sec := &d.Level.Sectors[idx]
	sec.FloorTiltX, sec.FloorTiltY = floorTiltX, floorTiltY
	sec.CeilTiltX, sec.CeilTiltY = ceilTiltX, ceilTiltY
	sec.SlopePivotX, sec.SlopePivotY = pivotX, pivotY
	d.markDirty()
	return true
}

// AddExtraFloor links control's floor/ceiling as a room-over-room platform
// floating inside target's footprint — see engine/slopes' own doc comment
// for the concept and engine/roomoverroom.go for the runtime side. Refuses
// a self-link (a sector can't float inside itself) or an out-of-range
// index. markDirty rebuilds the render tree, which is when view3d
// re-installs the full extraFloors map on worldgeo.Builder — see
// extraFloorMap.
func (d *Doc) AddExtraFloor(target, control int) bool {
	if target < 0 || target >= len(d.Level.Sectors) || control < 0 || control >= len(d.Level.Sectors) || target == control {
		return false
	}
	d.extraFloors = append(d.extraFloors, slopes.ExtraFloorDef{TargetSector: target, ControlSector: control})
	d.markDirty()
	return true
}

// RemoveExtraFloor removes the i-th extra-floor link on target — i indexes
// only THAT sector's own links, in the order ExtraFloorsFor lists them,
// not the whole d.extraFloors slice.
func (d *Doc) RemoveExtraFloor(target, i int) bool {
	n := -1
	for j := range d.extraFloors {
		if d.extraFloors[j].TargetSector != target {
			continue
		}
		n++
		if n == i {
			d.extraFloors = append(d.extraFloors[:j], d.extraFloors[j+1:]...)
			d.markDirty()
			return true
		}
	}
	return false
}

// ExtraFloorsFor returns target's own extra-floor links, for the inspector
// to list.
func (d *Doc) ExtraFloorsFor(target int) []slopes.ExtraFloorDef {
	var out []slopes.ExtraFloorDef
	for _, ef := range d.extraFloors {
		if ef.TargetSector == target {
			out = append(out, ef)
		}
	}
	return out
}

// extraFloorMap flattens d.extraFloors into worldgeo.Builder.SetExtraFloors'
// own shape (target sector -> control sector indices) — the exact
// transform engine/mapinfo_load.go's ApplyMapInfo does for live gameplay,
// read by view3d each time it rebuilds its Builder.
func (d *Doc) extraFloorMap() map[int][]int {
	if len(d.extraFloors) == 0 {
		return nil
	}
	m := make(map[int][]int, len(d.extraFloors))
	for _, ef := range d.extraFloors {
		m[ef.TargetSector] = append(m[ef.TargetSector], ef.ControlSector)
	}
	return m
}

// ---- geometry: move/delete ----------------------------------------------

// MoveThing relocates thing idx. Unlike MoveVertex this doesn't markDirty:
// Things are drawn as per-frame billboards (view3d's thingSprites,
// engine/mobj.go's real-game equivalent), rebuilt fresh every frame
// regardless of the BSP tree's own identity, not baked into the cached
// static geometry the way wall/flat textures and sector heights are.
func (d *Doc) MoveThing(idx int, x, y int16) bool {
	if idx < 0 || idx >= len(d.Level.Things) {
		return false
	}
	d.Level.Things[idx].X = x
	d.Level.Things[idx].Y = y
	return true
}

// DeleteSelection deletes whatever is currently selected, across all four
// element kinds — selectOne (see the selection type) always clears every
// other set before selecting, so in practice at most one kind ever holds
// entries, but this doesn't assume that: it simply acts on whichever sets
// aren't empty, so it behaves correctly regardless of which pane (2D or
// 3D) made the selection. Each per-kind Delete method already refuses
// (silently, a no-op) an element still referenced by something else.
func (d *Doc) DeleteSelection() {
	for idx := range d.Sel.Verts {
		d.DeleteVertex(idx)
	}
	for idx := range d.Sel.Lines {
		d.DeleteLinedef(idx)
	}
	for idx := range d.Sel.Sectors {
		d.DeleteSector(idx)
	}
	for idx := range d.Sel.Things {
		d.DeleteThing(idx)
	}
}

// MoveVertex relocates vertex idx.
func (d *Doc) MoveVertex(idx int, x, y int16) bool {
	if idx < 0 || idx >= len(d.Level.Vertexes) {
		return false
	}
	d.Level.Vertexes[idx].X = x
	d.Level.Vertexes[idx].Y = y
	d.markDirty()
	return true
}

// DeleteVertex removes vertex idx, refusing if any linedef still
// references it (matching the rest of this file's "refuse rather than
// silently corrupt state" convention — the caller deletes the bordering
// linedefs first). Every OTHER linedef's vertex index above idx is
// decremented to track the slice shift.
func (d *Doc) DeleteVertex(idx int) bool {
	if idx < 0 || idx >= len(d.Level.Vertexes) {
		return false
	}
	for _, ld := range d.Level.Linedefs {
		if int(ld.StartVertex) == idx || int(ld.EndVertex) == idx {
			return false
		}
	}
	d.Level.Vertexes = append(d.Level.Vertexes[:idx], d.Level.Vertexes[idx+1:]...)
	for i := range d.Level.Linedefs {
		if int(d.Level.Linedefs[i].StartVertex) > idx {
			d.Level.Linedefs[i].StartVertex--
		}
		if int(d.Level.Linedefs[i].EndVertex) > idx {
			d.Level.Linedefs[i].EndVertex--
		}
	}
	d.Sel.clearAll() // indices shifted under it — a stale selection could now point at the wrong element
	d.markDirty()
	return true
}

// removeSidedef deletes sidedef sdIdx and re-indexes every linedef's
// Front/BackSidedef reference above it — shared by DeleteLinedef, which may
// need to remove two sidedefs (front and back) for the one linedef it's
// deleting.
func (d *Doc) removeSidedef(sdIdx uint16) {
	d.Level.Sidedefs = append(d.Level.Sidedefs[:sdIdx], d.Level.Sidedefs[sdIdx+1:]...)
	for i := range d.Level.Linedefs {
		ld := &d.Level.Linedefs[i]
		if ld.FrontSidedef != wad.NoSidedef && ld.FrontSidedef > sdIdx {
			ld.FrontSidedef--
		}
		if ld.BackSidedef != wad.NoSidedef && ld.BackSidedef > sdIdx {
			ld.BackSidedef--
		}
	}
}

// DeleteLinedef removes linedef idx and its own sidedef(s) — a Doom
// sidedef always belongs to exactly one linedef, so nothing else can still
// reference them. Vertices are left in place (a separate, explicit
// DeleteVertex call), matching classic Doom editors' convention of never
// deleting geometry implicitly.
func (d *Doc) DeleteLinedef(idx int) bool {
	if idx < 0 || idx >= len(d.Level.Linedefs) {
		return false
	}
	ld := d.Level.Linedefs[idx]
	var sidedefs []uint16
	if ld.FrontSidedef != wad.NoSidedef {
		sidedefs = append(sidedefs, ld.FrontSidedef)
	}
	if ld.BackSidedef != wad.NoSidedef {
		sidedefs = append(sidedefs, ld.BackSidedef)
	}
	d.Level.Linedefs = append(d.Level.Linedefs[:idx], d.Level.Linedefs[idx+1:]...)
	// Highest index first, so removing one doesn't shift the index of
	// another still queued for removal.
	sort.Sort(sort.Reverse(sortUint16(sidedefs)))
	for _, sd := range sidedefs {
		d.removeSidedef(sd)
	}
	d.Sel.clearAll()
	d.markDirty()
	return true
}

type sortUint16 []uint16

func (s sortUint16) Len() int           { return len(s) }
func (s sortUint16) Less(i, j int) bool { return s[i] < s[j] }
func (s sortUint16) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }

// DeleteThing removes thing idx.
func (d *Doc) DeleteThing(idx int) bool {
	if idx < 0 || idx >= len(d.Level.Things) {
		return false
	}
	d.Level.Things = append(d.Level.Things[:idx], d.Level.Things[idx+1:]...)
	d.Sel.clearAll()
	return true
}

// DeleteSector removes sector idx, refusing if any sidedef still
// references it (the caller reassigns or deletes those linedefs first, the
// same "refuse rather than corrupt" convention DeleteVertex uses). Every
// OTHER sidedef's Sector index above idx is decremented to track the slice
// shift.
func (d *Doc) DeleteSector(idx int) bool {
	if idx < 0 || idx >= len(d.Level.Sectors) {
		return false
	}
	for _, sd := range d.Level.Sidedefs {
		if int(sd.Sector) == idx {
			return false
		}
	}
	d.Level.Sectors = append(d.Level.Sectors[:idx], d.Level.Sectors[idx+1:]...)
	for i := range d.Level.Sidedefs {
		if int(d.Level.Sidedefs[i].Sector) > idx {
			d.Level.Sidedefs[i].Sector--
		}
	}
	// Drop any extra-floor link touching the deleted sector (as either
	// target or control — neither makes sense once it's gone), and shift
	// every other link's own indices down to track the slice removal, the
	// same reindexing done above for Sidedef.Sector.
	kept := d.extraFloors[:0]
	for _, ef := range d.extraFloors {
		if ef.TargetSector == idx || ef.ControlSector == idx {
			continue
		}
		if ef.TargetSector > idx {
			ef.TargetSector--
		}
		if ef.ControlSector > idx {
			ef.ControlSector--
		}
		kept = append(kept, ef)
	}
	d.extraFloors = kept
	d.Sel.clearAll()
	d.markDirty()
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
