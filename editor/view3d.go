package editor

import (
	"image/color"
	"math"

	"twopointfive/assets"
	"twopointfive/bsp"
	"twopointfive/imkit"
	"twopointfive/raster"
	"twopointfive/render/worldgeo"
	"twopointfive/wad"
)

// view3d is the right pane: a live 3D preview of the doc's built geometry,
// with a free-fly camera, rendered by the SAME hardware (GPU-geometry) path
// the game itself uses (render/worldgeo + render/vulkan's DrawWorld) rather
// than a separate CPU renderer. Since the editor composites its whole UI as
// one CPU-drawn 2D plane (see App.frame), the pane works the same way the
// game's own weapon/HUD overlay does, just inverted: draw() cuts a fully
// transparent hole in the 2D canvas over this pane's rect, and App's single
// per-frame DrawWorld call paints the GPU-rendered world underneath the
// WHOLE canvas — invisible everywhere the 2D canvas stays opaque (menus,
// the 2D map view, panels), showing through only here. The camera's FOV is
// still computed for the full window (DrawWorld has no notion of a
// sub-viewport), so this pane shows an undistorted but more "zoomed in"
// crop of that projection rather than one framed for its own aspect ratio —
// a fine trade for a preview pane, and it costs zero new Vulkan code.
type view3d struct {
	bank  *Bank
	cam   raster.Camera
	have  bool // camera has been placed
	glide *glide

	// builder/builtFor cache the doc's geometry builder, rebuilt only when
	// the doc's BSP tree actually changes (an edit — see Doc.RebuildIfDirty)
	// rather than every frame. geom/wc are this frame's DrawWorld inputs,
	// read by App's main loop after draw() returns.
	builder  *worldgeo.Builder
	builtFor bsp.Node
	geom     *worldgeo.Geometry
	wc       worldgeo.Camera

	// sprites is reused across frames (thingSprites resets it with [:0])
	// to avoid reallocating a Things billboard list every frame.
	sprites []worldgeo.Sprite

	// draggingThing is the Things index currently being dragged, or -1 —
	// view2d's handleThingDrag has the same shape, just projected onto the
	// floor plane by a screen-ray instead of the 2D view's own s2w.
	draggingThing int
}

// glide eases the camera from a start pose to a target pose over dur
// seconds (the 2D→3D "jump to selection").
type glide struct {
	sx, sy, sz, sang float64 // start
	tx, ty, tz, tang float64 // target
	t, dur           float64
}

func (v *view3d) init(bank *Bank) {
	v.bank = bank
	v.cam = raster.Camera{Z: 48, FOVDeg: 90}
	v.draggingThing = -1
}

// reset forces the camera to re-place itself on the next draw (map switch).
func (v *view3d) reset() { v.have = false; v.glide = nil }

func (v *view3d) placeCamera(d *Doc) {
	placed := false
	for _, t := range d.Level.Things {
		if t.Type == 1 {
			v.cam.X, v.cam.Y = float64(t.X), float64(t.Y)
			v.cam.Angle = float64(t.Angle) * math.Pi / 180
			placed = true
			break
		}
	}
	if !placed {
		minX, minY, maxX, maxY := d.Bounds()
		v.cam.X, v.cam.Y = float64(minX+maxX)/2, float64(minY+maxY)/2
	}
	v.cam.Z = d.floorAt(v.cam.X, v.cam.Y) + 48
	v.cam.Pitch = 0
	v.have = true
}

// FocusOn eases the camera to view world (x, y) on a floor at floorZ. It
// keeps roughly its current bearing to the point (approaches from where it
// already is), stops ~160 units short, rises to eye height, and turns to
// face the point — so a selected wall / thing / sector fills the view
// without the camera diving through a wall.
func (v *view3d) FocusOn(x, y, floorZ float64) {
	const standoff = 160
	dx, dy := v.cam.X-x, v.cam.Y-y
	dist := math.Hypot(dx, dy)
	var fx, fy float64
	if dist < 1 {
		fx, fy = x-standoff, y
	} else {
		fx, fy = x+dx/dist*standoff, y+dy/dist*standoff
	}
	tang := math.Atan2(y-fy, x-fx) // raster.Camera.Angle: 0 = +X, CCW
	v.glide = &glide{
		sx: v.cam.X, sy: v.cam.Y, sz: v.cam.Z, sang: v.cam.Angle,
		tx: fx, ty: fy, tz: floorZ + 52, tang: v.cam.Angle + wrapAngle(tang-v.cam.Angle),
		dur: 0.3,
	}
	v.have = true
}

// wrapAngle brings a into (-π, π] so the glide turns the short way.
func wrapAngle(a float64) float64 {
	for a > math.Pi {
		a -= 2 * math.Pi
	}
	for a <= -math.Pi {
		a += 2 * math.Pi
	}
	return a
}

// draw updates the camera and this frame's DrawWorld inputs (v.geom / v.wc)
// and cuts area to a transparent hole in the 2D canvas so the GPU world
// shows through it (see the type doc comment) — App.Run does the actual
// DrawWorld call once per frame, after every pane has drawn.
func (v *view3d) draw(ui *imkit.UI, area imkit.Rect, d *Doc, dt float64) {
	c := ui.C
	if !v.have {
		v.placeCamera(d)
	}
	v.stepGlide(dt)

	focused := ui.MouseIn(area)
	v.control(ui, dt, focused)

	if v.builder == nil || v.builtFor != d.tree {
		v.builder = worldgeo.New(d.RenderLevel(), d.tree, v.bank.Tex)
		// Room-over-room links (Doc.AddExtraFloor) aren't part of the BSP
		// tree itself, so they need their own install here — this only
		// runs when the builder is freshly (re)constructed anyway (after
		// any edit that called markDirty, extra-floor changes included),
		// matching SetExtraFloors' own doc comment on when it's meant to
		// be called (it invalidates the cached mesh, so not every frame).
		v.builder.SetExtraFloors(d.extraFloorMap())
		v.builtFor = d.tree
	}
	v.geom = v.builder.Build(float32(v.cam.X), float32(v.cam.Y))
	v.sprites = v.thingSprites(d, v.sprites[:0])
	v.builder.AddSprites(v.cam.Angle, v.sprites)
	v.wc = worldgeo.Camera{
		X: v.cam.X, Y: v.cam.Y, Z: v.cam.Z,
		Angle: v.cam.Angle, Pitch: v.cam.Pitch, FOVDeg: v.cam.FOVDeg,
	}

	if d.tree != nil {
		c.ClearRect(area, color.RGBA{})
	} else {
		c.FillRect(area, imkit.Theme.Background)
		c.TextCenter(imkit.Rect{X: area.X, Y: area.Y + area.H/2 - 8, W: area.W, H: 16},
			"3D preview unavailable — draw at least one closed room", imkit.Theme.TextDim)
	}

	if d.tree != nil {
		v.handleClickSelect(ui, area, d, focused)
		v.drawDropHover(ui, area, d)
		v.handleDrop(ui, area, d)
	}
	if focused && ui.In.Edge(imkit.KeyDelete) {
		d.DeleteSelection()
	}

	cx, cy := area.X+area.W/2, area.Y+area.H/2
	c.Line(cx-5, cy, cx+5, cy, imkit.Theme.Accent)
	c.Line(cx, cy-5, cx, cy+5, imkit.Theme.Accent)
	c.Text(area.X+6, area.Y+area.H-10,
		"RMB-drag look   WASD move   Q/E down/up   Shift fast   LMB select/drag wall or thing   Del delete", imkit.Theme.TextDim)
}

// ---- wall picking (ray cast from the camera through the mouse pixel) ------
//
// The projection App.Run's DrawWorld call uses is built for the FULL canvas
// (a.cv.W x a.cv.H, see the type doc comment), and this pane is just a
// transparent crop of it, so a screen pixel here maps to a ray using the
// full canvas size, not this pane's own — ui.C.W/H, not area.W/H.

// rayDir returns the (unnormalized) world-space direction of the ray from
// the camera through canvas pixel (mx, my), for a fw x h canvas.
func (v *view3d) rayDir(mx, my float64, fw, h int) (dx, dy, dz float64) {
	focal := worldgeo.FocalFor(v.wc, h)
	if focal == 0 {
		return 1, 0, 0
	}
	right, up, forward := worldgeo.CameraBasis(v.wc)
	ex := (mx - float64(fw)/2) / focal
	ey := (float64(h)/2 - my) / focal
	dx = ex*right[0] + ey*up[0] + forward[0]
	dy = ex*right[1] + ey*up[1] + forward[1]
	dz = ex*right[2] + ey*up[2] + forward[2]
	return
}

// project maps a world point to a canvas pixel under the current camera,
// mirroring rayDir's projection. ok is false if the point is behind (or too
// close to) the camera.
func (v *view3d) project(wx, wy, wz float64, fw, h int) (sx, sy int, ok bool) {
	right, up, forward := worldgeo.CameraBasis(v.wc)
	rx, ry, rz := wx-v.cam.X, wy-v.cam.Y, wz-v.cam.Z
	eyeX := rx*right[0] + ry*right[1] + rz*right[2]
	eyeY := rx*up[0] + ry*up[1] + rz*up[2]
	eyeZ := rx*forward[0] + ry*forward[1] + rz*forward[2]
	if eyeZ < worldgeo.NearPlane {
		return 0, 0, false
	}
	focal := worldgeo.FocalFor(v.wc, h)
	sx = int(math.Round(float64(fw)/2 + focal*eyeX/eyeZ))
	sy = int(math.Round(float64(h)/2 - focal*eyeY/eyeZ))
	return sx, sy, true
}

// rayLineHit intersects the ray from (ox,oy) along (dx,dy) with the segment
// from (ax,ay) spanning (ex,ey) (i.e. to (ax+ex, ay+ey)). t is the ray
// parameter (world units along (dx,dy), which callers use unnormalized —
// only its sign and relative ordering along one ray matter here), u is the
// segment parameter in [0,1] at the hit.
func rayLineHit(ox, oy, dx, dy, ax, ay, ex, ey float64) (t, u float64, ok bool) {
	denom := dx*ey - dy*ex
	if math.Abs(denom) < 1e-9 {
		return 0, 0, false
	}
	qpx, qpy := ax-ox, ay-oy
	t = (qpx*ey - qpy*ex) / denom
	u = (qpx*dy - qpy*dx) / denom
	return t, u, true
}

// lineZEnvelope is the vertical span a linedef's texturing can occupy: one
// sector's floor..ceiling for a one-sided line, or the union of both
// sectors' for a two-sided one (loose — it doesn't separate upper/lower/
// middle — good enough to pick "roughly this wall" in a preview pane).
func lineZEnvelope(d *Doc, ld wad.Linedef) (lo, hi float64, ok bool) {
	lvl := d.Level
	if ld.FrontSidedef == wad.NoSidedef || int(ld.FrontSidedef) >= len(lvl.Sidedefs) {
		return 0, 0, false
	}
	fsd := lvl.Sidedefs[ld.FrontSidedef]
	if int(fsd.Sector) >= len(lvl.Sectors) {
		return 0, 0, false
	}
	fs := lvl.Sectors[fsd.Sector]
	lo, hi = float64(fs.FloorHeight), float64(fs.CeilingHeight)
	if ld.BackSidedef != wad.NoSidedef && int(ld.BackSidedef) < len(lvl.Sidedefs) {
		bsd := lvl.Sidedefs[ld.BackSidedef]
		if int(bsd.Sector) < len(lvl.Sectors) {
			bs := lvl.Sectors[bsd.Sector]
			lo = math.Min(lo, float64(bs.FloorHeight))
			hi = math.Max(hi, float64(bs.CeilingHeight))
		}
	}
	return lo, hi, true
}

// pickLine casts a ray from the camera through canvas pixel (mx, my) and
// returns the nearest linedef whose wall envelope it hits — the 3D
// analogue of view2d's nearestLine, used both to highlight a drop target
// and to let a plain click select a wall.
func (v *view3d) pickLine(d *Doc, mx, my float64, fw, h int) (int, bool) {
	dx, dy, dz := v.rayDir(mx, my, fw, h)
	lvl := d.Level
	best, bestT := -1, math.MaxFloat64
	for i, ld := range lvl.Linedefs {
		if int(ld.StartVertex) >= len(lvl.Vertexes) || int(ld.EndVertex) >= len(lvl.Vertexes) {
			continue
		}
		a := lvl.Vertexes[ld.StartVertex]
		b := lvl.Vertexes[ld.EndVertex]
		t, u, ok := rayLineHit(v.cam.X, v.cam.Y, dx, dy, float64(a.X), float64(a.Y), float64(b.X-a.X), float64(b.Y-a.Y))
		if !ok || t <= 0 || t >= bestT || u < 0 || u > 1 {
			continue
		}
		lo, hi, ok := lineZEnvelope(d, ld)
		if !ok {
			continue
		}
		if hitZ := v.cam.Z + t*dz; hitZ < lo-1 || hitZ > hi+1 {
			continue
		}
		best, bestT = i, t
	}
	return best, best >= 0
}

// handleClickSelect lets a plain left click in the 3D pane pick up a Thing
// (and start dragging it — see handleThingDrag) or, failing that, select
// the wall under the cursor the way view2d's line mode does. Things are
// tried first: they're small, discrete points rendered on top of the
// geometry, so a tight on-screen radius around the cursor should win over
// a wall that happens to be behind them.
func (v *view3d) handleClickSelect(ui *imkit.UI, area imkit.Rect, d *Doc, focused bool) {
	if v.handleThingDrag(ui, focused, d) {
		return
	}
	if !focused || !ui.In.LeftClicked {
		return
	}
	if li, ok := v.pickLine(d, ui.In.MouseX, ui.In.MouseY, ui.C.W, ui.C.H); ok {
		d.Sel.selectOne(modeLines, li)
	}
}

// thingPickRadiusPx/thingPickHeight: pickThing's on-screen hit radius
// around a Thing's projected position, and how far above its floor height
// to project from — roughly chest height, so aiming at the visible middle
// of its billboard sprite hits rather than needing to click its feet.
const (
	thingPickRadiusPx = 14.0
	thingPickHeight   = 32.0
)

// pickThing returns the Thing whose projected screen position is nearest
// the cursor, among those within thingPickRadiusPx — the 3D analogue of
// view2d's nearestThing. When more than one Thing's icon overlaps on
// screen, the one nearer the camera in world space wins.
func (v *view3d) pickThing(d *Doc, mx, my float64, fw, h int) (int, bool) {
	const radius2 = thingPickRadiusPx * thingPickRadiusPx
	best, bestDepth := -1, math.MaxFloat64
	for i, t := range d.Level.Things {
		x, y := float64(t.X), float64(t.Y)
		z := d.floorAt(x, y) + thingPickHeight
		sx, sy, ok := v.project(x, y, z, fw, h)
		if !ok {
			continue
		}
		ddx, ddy := float64(sx)-mx, float64(sy)-my
		if ddx*ddx+ddy*ddy > radius2 {
			continue
		}
		if depth := math.Hypot(x-v.cam.X, y-v.cam.Y); depth < bestDepth {
			best, bestDepth = i, depth
		}
	}
	return best, best >= 0
}

// handleThingDrag mirrors view2d's handleVertexDrag/handleThingDrag: picks
// up the Thing under the cursor on a fresh click and slides it along the
// horizontal plane at its own floor height for as long as the left button
// stays down, ending the drag on release. Returns true if it consumed this
// frame's click/drag, so handleClickSelect's own wall-pick doesn't also
// fire on the same click.
func (v *view3d) handleThingDrag(ui *imkit.UI, focused bool, d *Doc) bool {
	if v.draggingThing >= 0 {
		if ui.In.LeftDown {
			v.dragThingTo(ui, d)
			return true
		}
		v.draggingThing = -1
		return true
	}
	if !focused || !ui.In.LeftClicked {
		return false
	}
	idx, ok := v.pickThing(d, ui.In.MouseX, ui.In.MouseY, ui.C.W, ui.C.H)
	if !ok {
		return false
	}
	v.draggingThing = idx
	d.Sel.selectOne(modeThings, idx)
	return true
}

// dragThingTo moves the currently-dragged Thing to wherever the camera-
// through-cursor ray crosses the horizontal plane at its own floor height.
// A no-op this frame if that ray is behind the camera or too close to
// parallel with the floor to give a stable intersection (looking near the
// horizon) — the drag just holds position until the ray is usable again.
func (v *view3d) dragThingTo(ui *imkit.UI, d *Doc) {
	idx := v.draggingThing
	if idx < 0 || idx >= len(d.Level.Things) {
		v.draggingThing = -1
		return
	}
	t := d.Level.Things[idx]
	planeZ := d.floorAt(float64(t.X), float64(t.Y))
	dx, dy, dz := v.rayDir(ui.In.MouseX, ui.In.MouseY, ui.C.W, ui.C.H)
	if math.Abs(dz) < 1e-6 {
		return
	}
	tParam := (planeZ - v.cam.Z) / dz
	if tParam <= 0 {
		return
	}
	wx := v.cam.X + tParam*dx
	wy := v.cam.Y + tParam*dy
	d.MoveThing(idx, int16(math.Round(wx)), int16(math.Round(wy)))
}

// drawDropHover outlines the wall a texture drag would land on if dropped
// now — the 3D counterpart of view2d's drawDropHover.
func (v *view3d) drawDropHover(ui *imkit.UI, area imkit.Rect, d *Doc) {
	pl, ok := ui.Dragging()
	if !ok || !ui.MouseIn(area) {
		return
	}
	tp, ok := pl.(texPayload)
	if !ok || tp.flat {
		return // flats land on sectors in the 2D view; no 3D floor/ceiling picking yet
	}
	li, ok := v.pickLine(d, ui.In.MouseX, ui.In.MouseY, ui.C.W, ui.C.H)
	if !ok {
		return
	}
	v.highlightLine(ui, area, d, li)
}

// handleDrop assigns a dropped texture to the wall under the drop point.
func (v *view3d) handleDrop(ui *imkit.UI, area imkit.Rect, d *Doc) {
	pl, dropped := ui.DropTarget(area)
	if !dropped {
		return
	}
	tp, ok := pl.(texPayload)
	if !ok || tp.flat {
		return
	}
	if li, ok := v.pickLine(d, ui.In.MouseX, ui.In.MouseY, ui.C.W, ui.C.H); ok {
		d.SetLineTexture(li, tp.name)
		d.Sel.selectOne(modeLines, li)
	}
}

// highlightLine draws the world-space wall quad for linedef li, projected
// to screen space under the current camera — an outline rather than a
// screen-space rect since a 3D wall isn't axis-aligned on screen.
func (v *view3d) highlightLine(ui *imkit.UI, area imkit.Rect, d *Doc, li int) {
	lvl := d.Level
	if li < 0 || li >= len(lvl.Linedefs) {
		return
	}
	ld := lvl.Linedefs[li]
	if int(ld.StartVertex) >= len(lvl.Vertexes) || int(ld.EndVertex) >= len(lvl.Vertexes) {
		return
	}
	lo, hi, ok := lineZEnvelope(d, ld)
	if !ok {
		return
	}
	a := lvl.Vertexes[ld.StartVertex]
	b := lvl.Vertexes[ld.EndVertex]
	corners := [4][3]float64{
		{float64(a.X), float64(a.Y), lo},
		{float64(b.X), float64(b.Y), lo},
		{float64(b.X), float64(b.Y), hi},
		{float64(a.X), float64(a.Y), hi},
	}
	var pts [4][2]int
	for i, cn := range corners {
		sx, sy, ok := v.project(cn[0], cn[1], cn[2], ui.C.W, ui.C.H)
		if !ok {
			return // a corner is behind the camera — skip rather than draw garbage
		}
		pts[i] = [2]int{sx, sy}
	}
	prev := ui.C.Clip(area)
	for i := 0; i < 4; i++ {
		j := (i + 1) % 4
		ui.C.Line(pts[i][0], pts[i][1], pts[j][0], pts[j][1], colDrop)
	}
	ui.C.SetClip(prev)
}

func (v *view3d) stepGlide(dt float64) {
	g := v.glide
	if g == nil {
		return
	}
	g.t += dt / math.Max(g.dur, 1e-3)
	if g.t >= 1 {
		g.t = 1
	}
	e := g.t * g.t * (3 - 2*g.t) // smoothstep
	v.cam.X = g.sx + (g.tx-g.sx)*e
	v.cam.Y = g.sy + (g.ty-g.sy)*e
	v.cam.Z = g.sz + (g.tz-g.sz)*e
	v.cam.Angle = g.sang + (g.tang-g.sang)*e
	if g.t >= 1 {
		v.glide = nil
	}
}

func (v *view3d) control(ui *imkit.UI, dt float64, focused bool) {
	in := &ui.In
	if focused && in.RightDown && !in.RightClicked { // skip the jump on the press frame
		const sens = 0.005
		v.cam.Angle -= in.DragDX * sens
		v.cam.Pitch = clampF(v.cam.Pitch-in.DragDY*sens, -worldgeo.MaxPitch, worldgeo.MaxPitch)
		v.glide = nil
	}
	if !focused {
		return
	}
	speed := 260.0
	if in.Shift() {
		speed = 1000.0
	}
	step := speed * dt
	var fwd, strafe float64
	if in.Down(imkit.KeyW) {
		fwd++
	}
	if in.Down(imkit.KeyS) {
		fwd--
	}
	if in.Down(imkit.KeyD) {
		strafe++
	}
	if in.Down(imkit.KeyA) {
		strafe--
	}
	if fwd != 0 || strafe != 0 {
		v.glide = nil
	}
	// angle 0 = +X, CCW positive; right-strafe (D) = forward rotated -90° = (sin,-cos)
	sn, cs := math.Sin(v.cam.Angle), math.Cos(v.cam.Angle)
	v.cam.X += (cs*fwd + sn*strafe) * step
	v.cam.Y += (sn*fwd - cs*strafe) * step
	if in.Down(imkit.KeyE) {
		v.cam.Z += step
	}
	if in.Down(imkit.KeyQ) {
		v.cam.Z -= step
	}
}

func clampF(x, lo, hi float64) float64 {
	return math.Max(lo, math.Min(x, hi))
}

// ---- thing preview sprites -------------------------------------------
//
// The editor placed Things as coloured dots in the 2D view only; the 3D
// preview never rendered them at all. This resolves each one to the same
// billboard the real game shows (raster.SpriteRotation + assets.Textures'
// SpriteFrame — see engine/mobj.go's collectWorldObjects, which this
// mirrors without needing a live raster.Renderer or mobj), so placing a
// monster, weapon, or pickup shows what it'll actually look like.

// thingSprites appends d.Level.Things (that resolve to a real sprite) to
// out as camera-facing billboards for AddSprites, and returns it — reused
// across frames by the caller to avoid reallocating every frame.
func (v *view3d) thingSprites(d *Doc, out []worldgeo.Sprite) []worldgeo.Sprite {
	if v.bank == nil {
		return out
	}
	for _, t := range d.Level.Things {
		td, ok := thingByNum[int(t.Type)]
		if !ok || td.Sprite == "" {
			continue
		}
		x, y := float64(t.X), float64(t.Y)
		angle := float64(t.Angle) * math.Pi / 180
		rot := raster.SpriteRotation(v.cam.X, v.cam.Y, x, y, angle)
		sp, flip, ok := v.bank.Tex.SpriteFrame(td.Sprite, 0, rot)
		if !ok || sp == nil {
			continue
		}
		out = append(out, worldgeo.Sprite{
			X: float32(x), Y: float32(y), Z: float32(d.floorAt(x, y)),
			Lift: float32(spriteLift(sp)), SizeMul: 1,
			Tex:   sp,
			Light: float32(d.lightAt(x, y)),
			Flip:  flip,
		})
	}
	return out
}

// spriteLift is raster.Renderer.ThingSprite's vertical-seat calculation
// (that method needs a live Renderer for its texture cache, which the
// editor doesn't have — everything else it does is reproduced above via
// the exported SpriteRotation/SpriteFrame): art whose authored topoffset
// falls short of the full sprite height gets lifted so it doesn't look
// like it's sinking through the floor.
func spriteLift(sp *assets.RGBA) float64 {
	tpu := sp.TexelsPerUnit
	if tpu <= 0 {
		tpu = 1
	}
	lift := float64(sp.Height)/tpu - float64(sp.OffsetY)/tpu
	if lift < 0 {
		lift = 0
	}
	return lift
}
