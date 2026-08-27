// Package raster is a software rasterizer that renders a Doom level the way
// id Software's own C engine did: a front-to-back BSP walk (package bsp)
// draws each subsector's walls as perspective-correct textured columns,
// narrowing a per-column "still open" vertical range exactly like Doom's
// ceilingclip/floorclip arrays, and floors/ceilings are filled into
// whatever range each wall newly exposes. See game_design.txt section 7 for
// the specific, deliberate ways this differs from id's fixed-point,
// tangent-table implementation, and how the RGBA frame it produces reaches
// the screen through the Vulkan renderer (package render/vulkan).
package raster

import (
	"math"

	"twopointfive/assets"
	"twopointfive/bsp"
	"twopointfive/wad"
)

// InternalWidth and InternalHeight are the rasterizer's fixed internal
// resolution — matching vanilla Doom's own 320x200 framebuffer, right down
// to the chunky look that comes from the Vulkan blit stage upscaling it
// with nearest-neighbor filtering (see render/vulkan/pipeline.go). Doom
// itself supported other resolutions later, but 320x200 is the one this
// engine deliberately committed to for Phase 2.
const (
	InternalWidth  = 320
	InternalHeight = 200
)

// nearPlane is the minimum camera-space depth (map units) a point must be
// at to be projectable; segs entirely closer than this are culled.
const nearPlane = 1.0

// Renderer owns the CPU-side RGBA framebuffer and the decoded texture cache,
// and renders one Level/BSP/Camera combination into that framebuffer per call.
type Renderer struct {
	Width, Height int
	Pix           []byte // RGBA8, row-major, len == Width*Height*4

	textures *assets.Textures

	ceilClip  []int // per column: rows [0, ceilClip[x]) already drawn from the top
	floorClip []int // per column: rows [floorClip[x], Height) already drawn from the bottom

	focal      float64 // pinhole-camera focal length in pixels, recomputed per frame from FOV
	pitchShear float64 // vertical screen-space shift implementing Camera.Pitch, recomputed per frame
}

// New creates a renderer targeting the fixed InternalWidth x InternalHeight
// framebuffer, resolving textures/flats through textures.
func New(textures *assets.Textures) *Renderer {
	return &Renderer{
		Width: InternalWidth, Height: InternalHeight,
		Pix:       make([]byte, InternalWidth*InternalHeight*4),
		textures:  textures,
		ceilClip:  make([]int, InternalWidth),
		floorClip: make([]int, InternalWidth),
	}
}

// Render draws one frame of level, viewed from cam, into r.Pix.
func (r *Renderer) Render(level *wad.Level, tree bsp.Node, cam Camera) {
	fov := cam.FOVDeg
	if fov <= 0 {
		fov = 90
	}
	r.focal = float64(r.Width) / 2 / math.Tan(fov*math.Pi/180/2)
	// tan(pitch)*focal is the standard software-renderer freelook
	// approximation: shift the whole projection vertically rather than
	// truly rotating the camera in 3D (see Camera.Pitch's doc comment).
	r.pitchShear = math.Tan(cam.Pitch) * r.focal

	for x := 0; x < r.Width; x++ {
		r.ceilClip[x] = 0
		r.floorClip[x] = r.Height
	}
	for i := 0; i < len(r.Pix); i += 4 {
		// Void backdrop for anything no wall ever covers (looking through a
		// missing wall, or out past a level's edge).
		r.Pix[i+0], r.Pix[i+1], r.Pix[i+2], r.Pix[i+3] = 0, 0, 0, 255
	}

	bsp.Traverse(tree, float32(cam.X), float32(cam.Y), func(leaf *bsp.Leaf) {
		r.renderSubsector(level, leaf, cam)
	})
}

func (r *Renderer) renderSubsector(level *wad.Level, leaf *bsp.Leaf, cam Camera) {
	ss := leaf.Subsector
	for i := uint16(0); i < ss.SegCount; i++ {
		segIdx := int(ss.FirstSeg) + int(i)
		if segIdx >= len(level.Segs) {
			continue
		}
		r.renderSeg(level, level.Segs[segIdx], cam)
	}
}

// toCameraSpace rotates a world-space point into camera space: depth is the
// distance along the camera's forward axis, horiz the signed offset along
// its right axis (positive = to the right of center).
func toCameraSpace(cam Camera, wx, wy float64) (depth, horiz float64) {
	relX := wx - cam.X
	relY := wy - cam.Y
	s, c := math.Sin(cam.Angle), math.Cos(cam.Angle)
	depth = relX*c + relY*s
	horiz = relX*s - relY*c
	return
}

func (r *Renderer) renderSeg(level *wad.Level, seg wad.Seg, cam Camera) {
	if int(seg.StartVertex) >= len(level.Vertexes) || int(seg.EndVertex) >= len(level.Vertexes) {
		return
	}
	v1 := level.Vertexes[seg.StartVertex]
	v2 := level.Vertexes[seg.EndVertex]
	x1, y1 := float64(v1.X), float64(v1.Y)
	x2, y2 := float64(v2.X), float64(v2.Y)

	d1, h1 := toCameraSpace(cam, x1, y1)
	d2, h2 := toCameraSpace(cam, x2, y2)

	// t is the distance along the seg (map units, from its start vertex) at
	// each endpoint — carried alongside depth/horiz so it can be clipped
	// and perspective-interpolated the same way, giving correct wall
	// texture-space X per column.
	t1, t2 := 0.0, math.Hypot(x2-x1, y2-y1)

	if d1 < nearPlane && d2 < nearPlane {
		return // entirely behind the near plane
	}
	if d1 < nearPlane {
		f := (nearPlane - d1) / (d2 - d1)
		h1 += (h2 - h1) * f
		t1 += (t2 - t1) * f
		d1 = nearPlane
	} else if d2 < nearPlane {
		f := (nearPlane - d2) / (d1 - d2)
		h2 += (h1 - h2) * f
		t2 += (t1 - t2) * f
		d2 = nearPlane
	}

	halfW := float64(r.Width) / 2
	sx1 := halfW + h1/d1*r.focal
	sx2 := halfW + h2/d2*r.focal
	if sx1 >= sx2 {
		// Doom's map data always winds a seg so solid space is to its
		// right; a seg that would project right-to-left is facing away
		// from the camera (or degenerate) — classic backface cull.
		return
	}

	xStart := int(math.Ceil(sx1 - 0.5))
	xEnd := int(math.Floor(sx2 - 0.5))
	if xStart < 0 {
		xStart = 0
	}
	if xEnd > r.Width-1 {
		xEnd = r.Width - 1
	}
	if xStart > xEnd {
		return
	}

	frontSD, backSD, frontSec, backSec := bsp.SegSides(level, seg)
	if frontSD == nil || frontSec == nil {
		return
	}

	invD1, invD2 := 1/d1, 1/d2
	tOverD1, tOverD2 := t1*invD1, t2*invD2

	for x := xStart; x <= xEnd; x++ {
		if r.ceilClip[x] >= r.floorClip[x] {
			continue // column already fully occluded by something nearer
		}
		alpha := (float64(x) + 0.5 - sx1) / (sx2 - sx1)
		invD := invD1 + (invD2-invD1)*alpha
		depth := 1 / invD
		segT := (tOverD1 + (tOverD2-tOverD1)*alpha) * depth

		texX := int(math.Floor(float64(frontSD.XOffset) + float64(seg.Offset) + segT))
		r.renderColumn(x, depth, texX, frontSD, backSD, frontSec, backSec, cam)
	}
	_ = backSD // reserved: masked (transparent) middle textures on two-sided lines are Phase 3
}

func (r *Renderer) worldZToScreenY(depth, worldZ, camZ float64) int {
	return int(r.horizonY() - (worldZ-camZ)/depth*r.focal + 0.5)
}

// horizonY is the screen row the horizon (eye-level, zero pitch) sits at —
// ordinarily the exact vertical center, shifted by r.pitchShear when the
// camera is looking up or down.
func (r *Renderer) horizonY() float64 {
	return float64(r.Height)/2 + r.pitchShear
}

// renderColumn draws one screen column's contribution from a single seg:
// the wall piece(s) it defines, and the floor/ceiling flat spans the wall
// newly exposes — then narrows r.ceilClip/r.floorClip to reflect what's now
// been drawn, exactly mirroring Doom's own per-column occlusion tracking.
func (r *Renderer) renderColumn(x int, depth float64, texX int, frontSD, backSD *wad.Sidedef, frontSec, backSec *wad.Sector, cam Camera) {
	top := r.ceilClip[x]
	bottom := r.floorClip[x]

	ceilY := clampInt(r.worldZToScreenY(depth, float64(frontSec.CeilingHeight), cam.Z), top, bottom)
	floorY := clampInt(r.worldZToScreenY(depth, float64(frontSec.FloorHeight), cam.Z), top, bottom)

	if backSec == nil {
		// One-sided (solid) wall: the middle texture fills the entire
		// floor-to-ceiling gap and nothing behind this seg can ever be
		// seen through it — close the column completely.
		r.drawFlatSpan(x, top, ceilY, frontSec.CeilingTexture, float64(frontSec.CeilingHeight), cam)
		r.drawWallSpan(x, ceilY, floorY, texX, frontSD.MiddleTexture, frontSD, float64(frontSec.CeilingHeight), depth, cam)
		r.drawFlatSpan(x, floorY, bottom, frontSec.FloorTexture, float64(frontSec.FloorHeight), cam)
		r.ceilClip[x] = bottom
		r.floorClip[x] = bottom
		return
	}

	backCeilY := clampInt(r.worldZToScreenY(depth, float64(backSec.CeilingHeight), cam.Z), top, bottom)
	backFloorY := clampInt(r.worldZToScreenY(depth, float64(backSec.FloorHeight), cam.Z), top, bottom)

	// Ceiling flat down to the (possibly stepped) upper wall piece.
	r.drawFlatSpan(x, top, ceilY, frontSec.CeilingTexture, float64(frontSec.CeilingHeight), cam)
	if backSec.CeilingHeight < frontSec.CeilingHeight {
		r.drawWallSpan(x, ceilY, backCeilY, texX, frontSD.UpperTexture, frontSD, float64(frontSec.CeilingHeight), depth, cam)
	}
	r.ceilClip[x] = maxInt(ceilY, backCeilY)

	// Floor flat up to the (possibly stepped) lower wall piece.
	r.drawFlatSpan(x, floorY, bottom, frontSec.FloorTexture, float64(frontSec.FloorHeight), cam)
	if backSec.FloorHeight > frontSec.FloorHeight {
		r.drawWallSpan(x, backFloorY, floorY, texX, frontSD.LowerTexture, frontSD, float64(backSec.FloorHeight), depth, cam)
	}
	r.floorClip[x] = minInt(floorY, backFloorY)

	// The gap between backCeilY and backFloorY is the open "portal" into
	// the next area — deliberately left undrawn/unclipped so subsectors
	// behind it, visited later in this same front-to-back traversal, are
	// still free to draw into it.
}

// drawWallSpan draws one wall piece's texture into screen rows [yTop,
// yBottom) of column x. topWorldZ is the wall piece's true, *unclamped*
// top edge in world Z (not derived from the possibly-clipped yTop pixel),
// which anchors the texture's row 0 there (Doom's default, "not unpegged"
// behavior; sd.YOffset shifts that anchor further).
//
// Doom's texture formats store 1 texel per map unit, so the texture row at
// screen row y is just topWorldZ minus that row's own world Z. Because
// depth (this column's distance to the wall plane) is constant across a
// column, world Z is a *linear* function of screen row here — unlike
// drawFlatSpan, which has to recompute a fresh depth every row — so this
// reduces to stepping v by a constant `depth/r.focal` texels per row. That
// step is exactly what makes a texture visibly stretch as a wall gets
// closer (small depth → small step, few rows needed to reach the next
// texel) and compress as it recedes (large depth → large step) instead of
// just showing a fixed number of texels regardless of distance.
func (r *Renderer) drawWallSpan(x, yTop, yBottom, texX int, texName string, sd *wad.Sidedef, topWorldZ, depth float64, cam Camera) {
	if yBottom <= yTop {
		return
	}
	tex, ok := r.textures.WallTexture(texName)
	if !ok {
		return
	}
	tx := ((texX % tex.Width) + tex.Width) % tex.Width

	step := depth / r.focal
	base := topWorldZ - cam.Z + float64(sd.YOffset) - r.horizonY()*step

	for y := yTop; y < yBottom; y++ {
		v := base + (float64(y)+0.5)*step
		ty := wrapInt(int(math.Floor(v)), tex.Height)
		r.setPixel(x, y, tex.At(tx, ty))
	}
}

// drawFlatSpan fills screen rows [yTop, yBottom) of column x with a
// floor/ceiling flat, computing each row's own world-space distance and
// sample point the way a raycaster's floor-casting pass does — the
// per-column equivalent of Doom's own per-row visplane spans, chosen
// because it fits this renderer's column-major structure far more simply
// than reintroducing horizontal span buffers would.
func (r *Renderer) drawFlatSpan(x, yTop, yBottom int, texName string, planeZ float64, cam Camera) {
	if yBottom <= yTop {
		return
	}
	flat, ok := r.textures.Flat(texName)
	if !ok {
		return
	}

	zRel := planeZ - cam.Z
	s, c := math.Sin(cam.Angle), math.Cos(cam.Angle)
	horizon := r.horizonY()
	halfW := float64(r.Width) / 2

	for y := yTop; y < yBottom; y++ {
		denom := horizon - (float64(y) + 0.5)
		if denom == 0 {
			continue
		}
		depth := zRel * r.focal / denom
		if depth <= 0 {
			continue
		}
		horiz := (float64(x) + 0.5 - halfW) / r.focal * depth

		// Invert toCameraSpace to recover the world point this pixel's ray
		// hits the floor/ceiling plane at.
		relX := depth*c + horiz*s
		relY := depth*s - horiz*c
		wx := cam.X + relX
		wy := cam.Y + relY

		fx := wrapInt(int(math.Floor(wx)), flat.Width)
		fy := wrapInt(int(math.Floor(wy)), flat.Height)
		r.setPixel(x, y, flat.At(fx, fy))
	}
}

func (r *Renderer) setPixel(x, y int, c [4]byte) {
	if x < 0 || x >= r.Width || y < 0 || y >= r.Height || c[3] == 0 {
		return
	}
	i := (y*r.Width + x) * 4
	r.Pix[i], r.Pix[i+1], r.Pix[i+2], r.Pix[i+3] = c[0], c[1], c[2], c[3]
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func wrapInt(v, m int) int {
	v %= m
	if v < 0 {
		v += m
	}
	return v
}
