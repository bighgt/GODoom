package editor

import (
	"math"

	"twopointfive/imkit"
	"twopointfive/raster"
)

// view3d is the right pane: the engine's software renderer showing the
// doc's built geometry, with a free-fly camera.
type view3d struct {
	bank  *Bank
	r     *raster.Renderer
	w, h  int
	cam   raster.Camera
	have  bool // camera has been placed
	glide *glide
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
}

// reset forces the camera to re-place itself on the next draw (map switch).
func (v *view3d) reset() { v.have = false; v.glide = nil }

func (v *view3d) ensure(w, h int) {
	if w < 16 || h < 16 || (v.r != nil && v.w == w && v.h == h) {
		return
	}
	v.r = raster.New(v.bank.Tex, w, h)
	v.r.SetTextureQuality("bilinear")
	v.w, v.h = w, h
}

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

func (v *view3d) draw(ui *imkit.UI, area imkit.Rect, d *Doc, dt float64) {
	c := ui.C
	c.FillRect(area, imkit.Theme.Background)
	v.ensure(area.W, area.H)
	if v.r == nil {
		return
	}
	if !v.have {
		v.placeCamera(d)
	}
	v.stepGlide(dt)

	focused := ui.MouseIn(area)
	v.control(ui, dt, focused)

	if d.tree != nil {
		v.r.Render(d.RenderLevel(), d.tree, v.cam)
		c.Blit(area, asImage(v.r.Pix, v.w, v.h))
	} else {
		c.TextCenter(imkit.Rect{X: area.X, Y: area.Y + area.H/2 - 8, W: area.W, H: 16},
			"3D preview unavailable — draw at least one closed room", imkit.Theme.TextDim)
	}

	cx, cy := area.X+area.W/2, area.Y+area.H/2
	c.Line(cx-5, cy, cx+5, cy, imkit.Theme.Accent)
	c.Line(cx, cy-5, cx, cy+5, imkit.Theme.Accent)
	c.Text(area.X+6, area.Y+area.H-10,
		"RMB-drag look   WASD move   Q/E down/up   Shift fast", imkit.Theme.TextDim)
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
		v.cam.Pitch = clampF(v.cam.Pitch-in.DragDY*sens, -raster.MaxPitch, raster.MaxPitch)
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
