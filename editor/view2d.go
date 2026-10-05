package editor

import (
	"fmt"
	"image/color"
	"math"
	"time"

	"twopointfive/bsp"
	"twopointfive/imkit"
	"twopointfive/wad"
)

// editMode is what the 2D view selects / edits.
type editMode int

const (
	modeVerts editMode = iota
	modeLines
	modeSectors
	modeThings
)

func (m editMode) String() string {
	return [...]string{"Vertices", "Linedefs", "Sectors", "Things"}[m]
}

// tool is the active 2D tool.
type tool int

const (
	toolSelect tool = iota
	toolDraw
)

// view2d is the left top-down map pane.
type view2d struct {
	camX, camY float64 // world point at the pane centre
	zoom       float64 // screen px per world unit
	grid       int
	mode       editMode
	tool       tool

	framed  bool
	panning bool
	panSX   float64
	panSY   float64
	rect    imkit.Rect

	// draw tool: grid-snapped world points placed so far
	draft []wad.Vertex

	// draggingVert is the vertex index currently being dragged in
	// modeVerts, or -1 when nothing is being dragged — see handleVertexDrag.
	draggingVert int
	// draggingThing is handleVertexDrag's counterpart for modeThings — see
	// handleThingDrag.
	draggingThing int

	// onPick is called when the user selects an element — the app moves the
	// 3D camera to (x, y, floorZ).
	onPick func(x, y, floorZ float64)
}

func (v *view2d) init(d *Doc) {
	v.zoom = 0.2
	v.grid = 32
	v.mode = modeLines
	v.tool = toolSelect
	v.framed = false // draw() frames on the first frame, once rect is known
	v.draft = v.draft[:0]
	v.draggingVert = -1
	v.draggingThing = -1
}

// frameMap centres and (once the pane rect is known) zooms so the whole map
// fits with a margin.
func (v *view2d) frameMap(d *Doc) {
	minX, minY, maxX, maxY := d.Bounds()
	v.camX = float64(minX+maxX) / 2
	v.camY = float64(minY+maxY) / 2
	if v.rect.W > 0 {
		w := float64(maxX-minX) + 256
		h := float64(maxY-minY) + 256
		v.zoom = math.Min(float64(v.rect.W)/math.Max(w, 1), float64(v.rect.H)/math.Max(h, 1))
		if v.zoom <= 0 || math.IsInf(v.zoom, 0) || math.IsNaN(v.zoom) {
			v.zoom = 0.2
		}
		v.framed = true
	}
}

func (v *view2d) w2s(wx, wy float64) (int, int) {
	cx := float64(v.rect.X) + float64(v.rect.W)/2
	cy := float64(v.rect.Y) + float64(v.rect.H)/2
	return int(math.Round(cx + (wx-v.camX)*v.zoom)), int(math.Round(cy - (wy-v.camY)*v.zoom))
}

func (v *view2d) s2w(sx, sy float64) (float64, float64) {
	cx := float64(v.rect.X) + float64(v.rect.W)/2
	cy := float64(v.rect.Y) + float64(v.rect.H)/2
	return (sx-cx)/v.zoom + v.camX, -(sy-cy)/v.zoom + v.camY
}

func (v *view2d) worldUnderCursor(in imkit.Input) (int, int) {
	wx, wy := v.s2w(in.MouseX, in.MouseY)
	return int(math.Round(wx)), int(math.Round(wy))
}

// snap returns the grid-snapped world point nearest (wx, wy), unless an
// existing vertex is within ~10 screen px, in which case that vertex.
func (v *view2d) snap(d *Doc, wx, wy float64) (int, int) {
	if vi := v.nearestVert(d, wx, wy, 10); vi >= 0 {
		return int(d.Level.Vertexes[vi].X), int(d.Level.Vertexes[vi].Y)
	}
	g := float64(v.grid)
	if g < 1 {
		g = 1
	}
	return int(math.Round(wx/g) * g), int(math.Round(wy/g) * g)
}

func (v *view2d) draw(ui *imkit.UI, area imkit.Rect, d *Doc, bank *Bank, pnl *panelState, camX, camY, camAngle float64) {
	v.rect = area
	if !v.framed {
		v.frameMap(d)
	}
	c := ui.C
	c.FillRect(area, imkit.Theme.Background)
	prev := c.Clip(area)
	defer c.SetClip(prev)

	over := ui.MouseIn(area)
	v.handleNav(ui, area, over)
	v.handleKeys(ui, d)

	v.drawGrid(c, area)
	v.drawGeometry(c, d)
	v.drawThings(c, d)
	v.drawCameraMarker(c, camX, camY, camAngle)
	v.drawDropHover(ui, d, bank, over)
	if v.tool == toolDraw {
		v.drawDraftAndPlace(ui, d, bank, pnl, over)
	} else if over {
		v.handleSelect(ui, d)
	}
	v.handleDrop(ui, d, bank, pnl)
	v.drawToolbar(ui, area)
	v.drawHintBar(ui, area)
}

// drawHintBar shows the current mode/grid plus a key-binding summary along
// the bottom edge — the 2D pane's counterpart to view3d's own status line,
// which this used to have no equivalent of at all.
func (v *view2d) drawHintBar(ui *imkit.UI, area imkit.Rect) {
	hint := fmt.Sprintf("Mode: %s (1-4)   G draw   [ ] grid: %d   Del delete   drag vertex/thing to move   MMB pan   wheel zoom",
		v.mode, v.grid)
	ui.C.Text(area.X+6, area.Y+area.H-10, hint, imkit.Theme.TextDim)
}

// ---- navigation --------------------------------------------------------

func (v *view2d) handleNav(ui *imkit.UI, area imkit.Rect, over bool) {
	if over && ui.In.MidClicked {
		v.panning, v.panSX, v.panSY = true, ui.In.MouseX, ui.In.MouseY
	}
	if v.panning {
		if ui.In.MidDown {
			v.camX -= (ui.In.MouseX - v.panSX) / v.zoom
			v.camY += (ui.In.MouseY - v.panSY) / v.zoom
			v.panSX, v.panSY = ui.In.MouseX, ui.In.MouseY
		} else {
			v.panning = false
		}
	}
	if over && ui.In.ScrollY != 0 {
		bx, by := v.s2w(ui.In.MouseX, ui.In.MouseY)
		v.zoom = math.Max(0.01, math.Min(v.zoom*math.Pow(1.2, ui.In.ScrollY), 8))
		ax, ay := v.s2w(ui.In.MouseX, ui.In.MouseY)
		v.camX += bx - ax
		v.camY += by - ay
	}
}

func (v *view2d) handleKeys(ui *imkit.UI, d *Doc) {
	if ui.In.Edge(imkit.Key1) {
		v.mode = modeVerts
	}
	if ui.In.Edge(imkit.Key2) {
		v.mode = modeLines
	}
	if ui.In.Edge(imkit.Key3) {
		v.mode = modeSectors
	}
	if ui.In.Edge(imkit.Key4) {
		v.mode = modeThings
	}
	if ui.In.Edge(imkit.KeyG) {
		if v.tool == toolDraw {
			v.tool, v.draft = toolSelect, v.draft[:0]
		} else {
			v.tool = toolDraw
		}
	}
	if ui.In.Edge(imkit.KeyLBracket) {
		v.grid = max(v.grid/2, 1)
	}
	if ui.In.Edge(imkit.KeyRBracket) {
		v.grid = min(v.grid*2, 1024)
	}
	if ui.In.Edge(imkit.KeyEscape) {
		v.draft = v.draft[:0]
	}
	if ui.In.Edge(imkit.KeyDelete) {
		d.DeleteSelection()
	}
}

// ---- selection -------------------------------------------------------

func (v *view2d) handleSelect(ui *imkit.UI, d *Doc) {
	if v.mode == modeVerts && v.handleVertexDrag(ui, d) {
		return
	}
	if v.mode == modeThings && v.handleThingDrag(ui, d) {
		return
	}
	if !ui.In.LeftClicked || int(ui.In.MouseY) < v.rect.Y+34 {
		return // no click, or the click is on the toolbar strip
	}
	wx, wy := v.s2w(ui.In.MouseX, ui.In.MouseY)
	idx := -1
	switch v.mode {
	case modeVerts:
		idx = v.nearestVert(d, wx, wy, 8)
	case modeLines:
		idx = v.nearestLine(d, wx, wy, 8)
	case modeSectors:
		idx = bsp.PointSectorIndex(d.tree, d.RenderLevel(), float32(wx), float32(wy))
	case modeThings:
		idx = v.nearestThing(d, wx, wy, 12)
	}
	if idx < 0 {
		d.Sel.clearAll()
		return
	}
	d.Sel.selectOne(v.mode, idx)
	if v.onPick != nil {
		if x, y, z, ok := d.ElementCenter(v.mode, idx); ok {
			v.onPick(x, y, z)
		}
	}
}

// handleVertexDrag drags the vertex under the cursor for as long as the
// left button stays down, snapping to the grid (or a nearby vertex) the
// same way the draw tool's own placement does. Returns true if it consumed
// this frame's input (either starting/continuing a drag, or ending one),
// so handleSelect's own click-to-select doesn't also fire on the same
// click.
func (v *view2d) handleVertexDrag(ui *imkit.UI, d *Doc) bool {
	return v.handleDrag(ui, d, &v.draggingVert, modeVerts,
		func(wx, wy float64) int { return v.nearestVert(d, wx, wy, 8) }, d.MoveVertex)
}

// handleThingDrag is handleVertexDrag's counterpart for a placed Thing.
func (v *view2d) handleThingDrag(ui *imkit.UI, d *Doc) bool {
	return v.handleDrag(ui, d, &v.draggingThing, modeThings,
		func(wx, wy float64) int { return v.nearestThing(d, wx, wy, 12) }, d.MoveThing)
}

// handleDrag is the shared grid-snapped drag-while-held body: *dragging is
// the index being dragged (-1 = none), pick finds the item under a world
// point (-1 = none), move applies a new position.
func (v *view2d) handleDrag(ui *imkit.UI, d *Doc, dragging *int, mode editMode,
	pick func(wx, wy float64) int, move func(idx int, x, y int16) bool) bool {
	if *dragging >= 0 {
		if ui.In.LeftDown {
			wx, wy := v.s2w(ui.In.MouseX, ui.In.MouseY)
			sx, sy := v.snap(d, wx, wy)
			move(*dragging, int16(sx), int16(sy))
			return true
		}
		*dragging = -1
		return true
	}
	if !ui.In.LeftClicked || int(ui.In.MouseY) < v.rect.Y+34 {
		return false
	}
	idx := pick(v.s2w(ui.In.MouseX, ui.In.MouseY))
	if idx < 0 {
		return false
	}
	*dragging = idx
	d.Sel.selectOne(mode, idx)
	return true
}

func (v *view2d) nearestVert(d *Doc, wx, wy float64, pxTol float64) int {
	best, bd := -1, pxTol*pxTol
	for i, vx := range d.Level.Vertexes {
		dx := (float64(vx.X) - wx) * v.zoom
		dy := (float64(vx.Y) - wy) * v.zoom
		if s := dx*dx + dy*dy; s < bd {
			best, bd = i, s
		}
	}
	return best
}

func (v *view2d) nearestLine(d *Doc, wx, wy, pxTol float64) int {
	best, bd := -1, pxTol
	for i, ld := range d.Level.Linedefs {
		if int(ld.StartVertex) >= len(d.Level.Vertexes) || int(ld.EndVertex) >= len(d.Level.Vertexes) {
			continue
		}
		a := d.Level.Vertexes[ld.StartVertex]
		b := d.Level.Vertexes[ld.EndVertex]
		dpx := pointSegDist(wx, wy, float64(a.X), float64(a.Y), float64(b.X), float64(b.Y)) * v.zoom
		if dpx < bd {
			best, bd = i, dpx
		}
	}
	return best
}

func (v *view2d) nearestThing(d *Doc, wx, wy, pxTol float64) int {
	best, bd := -1, pxTol*pxTol
	for i, t := range d.Level.Things {
		dx := (float64(t.X) - wx) * v.zoom
		dy := (float64(t.Y) - wy) * v.zoom
		if s := dx*dx + dy*dy; s < bd {
			best, bd = i, s
		}
	}
	return best
}

func pointSegDist(px, py, ax, ay, bx, by float64) float64 {
	dx, dy := bx-ax, by-ay
	l2 := dx*dx + dy*dy
	if l2 < 1e-9 {
		return math.Hypot(px-ax, py-ay)
	}
	t := math.Max(0, math.Min(1, ((px-ax)*dx+(py-ay)*dy)/l2))
	return math.Hypot(px-(ax+t*dx), py-(ay+t*dy))
}

// ---- drag & drop ----------------------------------------------------

// drawDropHover highlights the element that a texture/thing drag would land
// on if dropped now.
func (v *view2d) drawDropHover(ui *imkit.UI, d *Doc, bank *Bank, over bool) {
	if !over {
		return
	}
	pl, ok := ui.Dragging()
	if !ok {
		return
	}
	wx, wy := v.s2w(ui.In.MouseX, ui.In.MouseY)
	switch p := pl.(type) {
	case texPayload:
		if p.flat {
			if si := bsp.PointSectorIndex(d.tree, d.RenderLevel(), float32(wx), float32(wy)); si >= 0 {
				v.outlineSector(ui.C, d, si, colDrop)
			}
		} else if li := v.nearestLine(d, wx, wy, 10); li >= 0 {
			v.outlineLine(ui.C, d, li, colDrop, 3)
		}
	case thingPayload:
		sx, sy := v.w2s(math.Round(wx), math.Round(wy))
		ui.C.StrokeRect(imkit.Rect{X: sx - 5, Y: sy - 5, W: 11, H: 11}, colDrop, 1)
	}
}

func (v *view2d) handleDrop(ui *imkit.UI, d *Doc, bank *Bank, pnl *panelState) {
	pl, dropped := ui.DropTarget(v.rect)
	if !dropped {
		return
	}
	wx, wy := v.s2w(ui.In.MouseX, ui.In.MouseY)
	switch p := pl.(type) {
	case texPayload:
		if p.flat {
			if si := bsp.PointSectorIndex(d.tree, d.RenderLevel(), float32(wx), float32(wy)); si >= 0 {
				d.SetSectorFlat(si, p.name, p.ceil)
				d.Sel.selectOne(modeSectors, si)
			}
		} else if li := v.nearestLine(d, wx, wy, 12); li >= 0 {
			d.SetLineTexture(li, p.name)
			d.Sel.selectOne(modeLines, li)
		}
	case thingPayload:
		x, y := v.snap(d, wx, wy)
		d.AddThing(p.num, x, y, 90)
		d.markDirty()
	}
}

// ---- draw tool -----------------------------------------------------

func (v *view2d) drawDraftAndPlace(ui *imkit.UI, d *Doc, bank *Bank, pnl *panelState, over bool) {
	c := ui.C
	mx, my := int(ui.In.MouseX), int(ui.In.MouseY)
	sx, sy := 0, 0
	var cwx, cwy int
	if over {
		wx, wy := v.s2w(float64(mx), float64(my))
		cwx, cwy = v.snap(d, wx, wy)
		sx, sy = v.w2s(float64(cwx), float64(cwy))
	}

	// existing draft polyline
	for i := 0; i < len(v.draft); i++ {
		p := v.draft[i]
		px, py := v.w2s(float64(p.X), float64(p.Y))
		c.FillRect(imkit.Rect{X: px - 2, Y: py - 2, W: 5, H: 5}, colDraw)
		if i+1 < len(v.draft) {
			q := v.draft[i+1]
			qx, qy := v.w2s(float64(q.X), float64(q.Y))
			c.Line(px, py, qx, qy, colDraw)
		}
	}
	if over && len(v.draft) > 0 {
		last := v.draft[len(v.draft)-1]
		lx, ly := v.w2s(float64(last.X), float64(last.Y))
		c.Line(lx, ly, sx, sy, colDraw)
	}
	if over {
		c.StrokeRect(imkit.Rect{X: sx - 3, Y: sy - 3, W: 7, H: 7}, colDraw, 1)
	}

	// ignore clicks that land on the toolbar strip along the pane's top
	if !over || !ui.In.LeftClicked || my < v.rect.Y+34 {
		return
	}
	// closing click: near the first draft point and we have >= 3 points
	if len(v.draft) >= 3 {
		f := v.draft[0]
		fx, fy := v.w2s(float64(f.X), float64(f.Y))
		if (fx-sx)*(fx-sx)+(fy-sy)*(fy-sy) <= 100 {
			v.commitRoom(d, bank, pnl)
			return
		}
	}
	// otherwise add a point (dedupe an immediate double-click)
	if n := len(v.draft); n == 0 || int(v.draft[n-1].X) != cwx || int(v.draft[n-1].Y) != cwy {
		v.draft = append(v.draft, wad.Vertex{X: int16(cwx), Y: int16(cwy)})
	}
}

// commitRoom turns the closed draft polygon into a new sector: new
// vertices, a one-sided linedef per edge with a front sidedef, floor 0 /
// ceiling 128, textures from the panel's current selections.
func (v *view2d) commitRoom(d *Doc, bank *Bank, pnl *panelState) {
	lvl := d.Level
	n := len(v.draft)
	if n < 3 {
		return
	}
	// Doom's front sidedef sits on the RIGHT of a linedef (start→end). For a
	// one-sided room whose walls must face INWARD, the polygon has to wind
	// clockwise in y-up map space — shoelace area negative. Reverse if not.
	if polyArea(v.draft) > 0 {
		for i, j := 0, n-1; i < j; i, j = i+1, j-1 {
			v.draft[i], v.draft[j] = v.draft[j], v.draft[i]
		}
	}

	wallTex := pnl.wallSel
	if wallTex == "" {
		wallTex = firstOf(bank.Walls, "STARTAN2", "BROWN96")
	}
	floorTex := pnl.flatSel
	if floorTex == "" {
		floorTex = firstOf(bank.Flats, "FLOOR4_8", "FLAT5_4")
	}
	ceilTex := firstOf(bank.Flats, "CEIL3_5", "FLAT2", floorTex)

	secIdx := len(lvl.Sectors)
	lvl.Sectors = append(lvl.Sectors, wad.Sector{
		FloorHeight: 0, CeilingHeight: 128,
		FloorTexture: floorTex, CeilingTexture: ceilTex, LightLevel: 176,
	})

	vbase := len(lvl.Vertexes)
	lvl.Vertexes = append(lvl.Vertexes, v.draft...)
	for i := 0; i < n; i++ {
		a := vbase + i
		b := vbase + (i+1)%n
		sd := len(lvl.Sidedefs)
		lvl.Sidedefs = append(lvl.Sidedefs, wad.Sidedef{MiddleTexture: wallTex, Sector: uint16(secIdx)})
		lvl.Linedefs = append(lvl.Linedefs, wad.Linedef{
			StartVertex: uint16(a), EndVertex: uint16(b), Flags: 1,
			FrontSidedef: uint16(sd), BackSidedef: wad.NoSidedef,
		})
	}
	v.draft = v.draft[:0]
	d.Sel.selectOne(modeSectors, secIdx)
	d.markDirty()
}

func polyArea(p []wad.Vertex) float64 {
	a := 0.0
	for i := 0; i < len(p); i++ {
		j := (i + 1) % len(p)
		a += float64(p[i].X)*float64(p[j].Y) - float64(p[j].X)*float64(p[i].Y)
	}
	return a / 2
}

// ---- rendering ---------------------------------------------------------

var (
	colOneSided = color.RGBA{0xd0, 0xd0, 0xd8, 0xff}
	colTwoSided = color.RGBA{0x70, 0x78, 0x88, 0xff}
	colAction   = color.RGBA{0x5a, 0xb0, 0x6a, 0xff}
	colVert     = color.RGBA{0x9a, 0xc4, 0xe8, 0xff}
	colSel      = color.RGBA{0xe8, 0xa8, 0x4a, 0xff}
	colDrop     = color.RGBA{0x4a, 0xd9, 0x8a, 0xff}
	colDraw     = color.RGBA{0xf0, 0xd0, 0x50, 0xff}
	colGrid     = color.RGBA{0x2b, 0x2b, 0x36, 0xff}
	colAxis     = color.RGBA{0x50, 0x50, 0x60, 0xff}
	colCamera   = color.RGBA{0xff, 0x20, 0xc0, 0xff} // bright pink — see drawCameraMarker
)

func (v *view2d) drawGrid(c *imkit.Canvas, area imkit.Rect) {
	if v.grid > 0 && float64(v.grid)*v.zoom >= 5 {
		x0, y0 := v.s2w(float64(area.X), float64(area.Y+area.H))
		x1, y1 := v.s2w(float64(area.X+area.W), float64(area.Y))
		for gx := int(math.Floor(x0/float64(v.grid))) * v.grid; float64(gx) <= x1; gx += v.grid {
			sx, _ := v.w2s(float64(gx), 0)
			c.FillRect(imkit.Rect{X: sx, Y: area.Y, W: 1, H: area.H}, colGrid)
		}
		for gy := int(math.Floor(y0/float64(v.grid))) * v.grid; float64(gy) <= y1; gy += v.grid {
			_, sy := v.w2s(0, float64(gy))
			c.FillRect(imkit.Rect{X: area.X, Y: sy, W: area.W, H: 1}, colGrid)
		}
	}
	sx, sy := v.w2s(0, 0)
	c.FillRect(imkit.Rect{X: sx, Y: area.Y, W: 1, H: area.H}, colAxis)
	c.FillRect(imkit.Rect{X: area.X, Y: sy, W: area.W, H: 1}, colAxis)
}

func (v *view2d) drawGeometry(c *imkit.Canvas, d *Doc) {
	lvl := d.Level
	for i, ld := range lvl.Linedefs {
		if int(ld.StartVertex) >= len(lvl.Vertexes) || int(ld.EndVertex) >= len(lvl.Vertexes) {
			continue
		}
		a := lvl.Vertexes[ld.StartVertex]
		b := lvl.Vertexes[ld.EndVertex]
		x0, y0 := v.w2s(float64(a.X), float64(a.Y))
		x1, y1 := v.w2s(float64(b.X), float64(b.Y))
		col := colOneSided
		switch {
		case d.Sel.Lines[i]:
			col = colSel
		case ld.SpecialType != 0:
			col = colAction
		case ld.BackSidedef != wad.NoSidedef:
			col = colTwoSided
		}
		c.Line(x0, y0, x1, y1, col)
		// front-normal nub (points toward the front sidedef's sector)
		mx, my := (x0+x1)/2, (y0+y1)/2
		nx, ny := float64(-(y1 - y0)), float64(x1-x0) // screen-space left normal = map-space right (y is flipped)
		if l := math.Hypot(nx, ny); l > 0 {
			c.Line(mx, my, mx+int(nx/l*4), my+int(ny/l*4), col)
		}
	}
	if v.mode == modeVerts || v.zoom > 0.12 {
		for i, vx := range lvl.Vertexes {
			x, y := v.w2s(float64(vx.X), float64(vx.Y))
			col := colVert
			sz := 3
			if d.Sel.Verts[i] {
				col, sz = colSel, 5
			}
			c.FillRect(imkit.Rect{X: x - sz/2, Y: y - sz/2, W: sz, H: sz}, col)
		}
	}
	for si := range d.Sel.Sectors {
		v.outlineSector(c, d, si, colSel)
	}
}

func (v *view2d) outlineLine(c *imkit.Canvas, d *Doc, i int, col color.RGBA, w int) {
	if i < 0 || i >= len(d.Level.Linedefs) {
		return
	}
	ld := d.Level.Linedefs[i]
	if int(ld.StartVertex) >= len(d.Level.Vertexes) || int(ld.EndVertex) >= len(d.Level.Vertexes) {
		return
	}
	a := d.Level.Vertexes[ld.StartVertex]
	b := d.Level.Vertexes[ld.EndVertex]
	x0, y0 := v.w2s(float64(a.X), float64(a.Y))
	x1, y1 := v.w2s(float64(b.X), float64(b.Y))
	for o := -w / 2; o <= w/2; o++ {
		c.Line(x0, y0+o, x1, y1+o, col)
		c.Line(x0+o, y0, x1+o, y1, col)
	}
}

func (v *view2d) outlineSector(c *imkit.Canvas, d *Doc, sec int, col color.RGBA) {
	for i, ld := range d.Level.Linedefs {
		if d.lineTouchesSector(ld, sec) {
			v.outlineLine(c, d, i, col, 2)
		}
	}
}

func (v *view2d) drawThings(c *imkit.Canvas, d *Doc) {
	for i, t := range d.Level.Things {
		x, y := v.w2s(float64(t.X), float64(t.Y))
		col := thingCategory(int(t.Type)).color()
		sz := 7
		if d.Sel.Things[i] {
			col, sz = colSel, 9
		}
		c.FillRect(imkit.Rect{X: x - sz/2, Y: y - sz/2, W: sz, H: sz}, col)
		c.StrokeRect(imkit.Rect{X: x - sz/2, Y: y - sz/2, W: sz, H: sz}, color.RGBA{0, 0, 0, 255}, 1)
		ang := float64(t.Angle) * math.Pi / 180
		c.Line(x, y, x+int(9*math.Cos(ang)), y-int(9*math.Sin(ang)), col)
	}
}

// drawCameraMarker draws a bright pink blinking dot at the 3D view's
// current camera position, with a short facing tick — so it's always
// obvious where you are (and which way you're looking) in the level while
// flying around the 3D view, the same idea as a level editor's own
// "camera thing" marker. Blinks via a plain wall-clock toggle (visible for
// 300ms, hidden for 300ms) — purely cosmetic, so there's no need to plumb
// a shared level-time clock through just for this.
func (v *view2d) drawCameraMarker(c *imkit.Canvas, camX, camY, camAngle float64) {
	if time.Now().UnixMilli()/300%2 != 0 {
		return
	}
	x, y := v.w2s(camX, camY)
	const sz = 10
	c.FillRect(imkit.Rect{X: x - sz/2, Y: y - sz/2, W: sz, H: sz}, colCamera)
	c.StrokeRect(imkit.Rect{X: x - sz/2, Y: y - sz/2, W: sz, H: sz}, color.RGBA{0, 0, 0, 255}, 1)
	c.Line(x, y, x+int(16*math.Cos(camAngle)), y-int(16*math.Sin(camAngle)), colCamera)
}

func (v *view2d) drawToolbar(ui *imkit.UI, area imkit.Rect) {
	x := area.X + 6
	for _, md := range []struct {
		g, hint string
		m       editMode
	}{
		{"V", "Vertex mode: select and drag vertices", modeVerts},
		{"L", "Linedef mode: select walls, assign textures/flags", modeLines},
		{"S", "Sector mode: select floors/ceilings, assign flats/heights", modeSectors},
		{"T", "Thing mode: place, move, and delete monsters/items/decorations", modeThings},
	} {
		btn := imkit.Rect{X: x, Y: area.Y + 6, W: 26, H: 22}
		if ui.IconButton(btn, md.g, v.mode == md.m && v.tool == toolSelect) {
			v.mode, v.tool = md.m, toolSelect
		}
		ui.Tooltip(btn, md.hint)
		x += 28
	}
	x += 10
	drawBtn := imkit.Rect{X: x, Y: area.Y + 6, W: 40, H: 22}
	if ui.IconButton(drawBtn, "DRAW", v.tool == toolDraw) {
		if v.tool == toolDraw {
			v.tool, v.draft = toolSelect, v.draft[:0]
		} else {
			v.tool = toolDraw
		}
	}
	ui.Tooltip(drawBtn, "Draw a new room: click to place corners, click back near the first point to close it")
}
