package raster

// Sector light + distance shading, ported from PrBoom's r_main.c
// (R_InitLightTables) and the render-time lookups in r_segs.c / r_plane.c.
//
// Vanilla Doom never multiplies a colour by a brightness. It swaps in one
// of NUMCOLORMAPS (32) progressively darker COLORMAP rows, per wall column
// and per flat pixel, chosen from the sector's light level and the
// surface's on-screen scale / camera-space depth:
//
//	lightnum                    = (sectorlight >> LIGHTSEGSHIFT) + extralight   [0..15]
//	scalelight[lightnum][scale] -> COLORMAP row for a wall column
//	zlight[lightnum][z]         -> COLORMAP row for a flat pixel
//
// Axis-aligned wall faces get lightnum -/+ 1 first ("fake contrast",
// applied in renderSeg). Both tables fade from the sector's base row
// (startmap, far away) toward row 0 (point blank) as 1/distance.
//
// This renderer has no palette to remap, so each COLORMAP row is collapsed
// to a plain brightness multiply, (NUMCOLORMAPS - row) / NUMCOLORMAPS — the
// same near-linear approximation of the real COLORMAP fade that GZDoom's
// software "Doom" light mode uses — and both tables are baked to 0..256
// integer multipliers so a texel shades with `c*m >> 8`, no per-pixel
// arithmetic.
const (
	lightLevels   = 16  // LIGHTLEVELS
	numColormaps  = 32  // NUMCOLORMAPS   (row 0 = identity, 31 = darkest)
	lightSegShift = 4   // LIGHTSEGSHIFT: 8-bit sector light -> lightnum
	maxLightScale = 48  // MAXLIGHTSCALE: wall scalelight columns
	maxLightZ     = 128 // MAXLIGHTZ:     flat zlight columns
	distMap       = 2   // DISTMAP

	// wallScaleRef is vanilla's rw_scale >> LIGHTSCALESHIFT for a wall
	// column one map unit deep, evaluated at the 320-wide / 90-degree
	// reference the light tables were tuned against: rw_scale is roughly
	// (160<<16)/depth in 16.16 fixed point, and >>12 leaves 2560/depth.
	// Using a fixed reference rather than this renderer's real width /
	// focal length is deliberate — vanilla's scalelight[] carries a
	// SCREENWIDTH/viewwidth term that exactly cancels the resolution
	// dependence, so wall brightness must not move with render scale or FOV.
	wallScaleRef = 2560.0

	// fakeContrast is the nudge renderSeg folds into a wall's raw 0..255
	// light level before it reaches this file. 16 raw units is exactly -/+1
	// lightnum after the >>4 ((x-16)>>4 == (x>>4)-1 for x >= 16), i.e.
	// r_segs.c's lightnum-- / lightnum++.
	fakeContrast = 16
)

// ExtraLight is id's extralight: added to every lightnum, bumped for a
// couple of tics by a weapon muzzle flash. Nothing drives it yet; a caller
// may set it between frames.
var ExtraLight int

// lightScale multiplies every raw sector light level before it is turned
// into a table row (config lightScale, section 17). 1.0 is the WAD's
// authored brightness; SetLightScale changes it. It sits here rather than
// on Renderer because lightnumOf / shadeMul / spriteShade are package
// functions the whole vanilla path shares.
var lightScale = 1.0

// SetLightScale sets the global sector-light multiplier for the vanilla
// shading path. Out-of-range values are ignored (config already clamps).
func SetLightScale(s float64) {
	if s > 0 {
		lightScale = s
	}
}

// scaleLight / zLight are R_InitLightTables' two tables, already reduced
// from "COLORMAP row 0..31" to "0..256 brightness multiplier".
var (
	scaleLight [lightLevels][maxLightScale]uint16
	zLight     [lightLevels][maxLightZ]uint16
)

func init() {
	for i := 0; i < lightLevels; i++ {
		// startmap = ((LIGHTLEVELS-1-i)*2) * NUMCOLORMAPS / LIGHTLEVELS
		startmap := ((lightLevels - 1 - i) * 2) * numColormaps / lightLevels
		for j := 0; j < maxLightScale; j++ {
			// scalelight[i][j] = startmap - j/DISTMAP  (SCREENWIDTH == viewwidth)
			scaleLight[i][j] = colormapMul(startmap - j/distMap)
		}
		for j := 0; j < maxLightZ; j++ {
			// zlight[i][j] = startmap -
			//   (FixedDiv(160*FRACUNIT, (j+1)<<LIGHTZSHIFT) >> LIGHTSCALESHIFT) / DISTMAP
			// with FRACUNIT=1<<16, LIGHTZSHIFT=20, LIGHTSCALESHIFT=12 that
			// inner term is (655360 / (j+1)) >> 12.
			scale := (655360 / (j + 1)) >> 12
			zLight[i][j] = colormapMul(startmap - scale/distMap)
		}
	}
}

// colormapMul turns a COLORMAP row (clamped to 0..31) into a 0..256
// brightness multiplier: (NUMCOLORMAPS - row) / NUMCOLORMAPS, so row 0 is
// full bright (256) and row 31 is a faint 8/256 rather than pure black —
// matching vanilla, whose darkest colormap row still shows shape.
func colormapMul(row int) uint16 {
	if row < 0 {
		row = 0
	} else if row > numColormaps-1 {
		row = numColormaps - 1
	}
	return uint16((numColormaps - row) * 256 / numColormaps)
}

// lightnumOf maps a raw 0..255 sector light level (already carrying any
// fake-contrast nudge from renderSeg) to a 0..15 table row, clamped the way
// r_segs.c clamps when it indexes scalelight / zlight.
func lightnumOf(sectorLight int16) int {
	sl := sectorLight
	if lightScale != 1.0 {
		scaled := float64(sectorLight) * lightScale
		switch {
		case scaled <= 0:
			sl = 0
		case scaled >= 255:
			sl = 255
		default:
			sl = int16(scaled)
		}
	}
	n := (int(sl) >> lightSegShift) + ExtraLight
	if n < 0 {
		return 0
	}
	if n > lightLevels-1 {
		return lightLevels - 1
	}
	return n
}

// scaleLightCol is the scalelight column for a camera-space depth: vanilla's
// rw_scale >> LIGHTSCALESHIFT (2560/depth at this renderer's reference),
// clamped to 0..MAXLIGHTSCALE-1. A depth <= 0 pins to the bright end.
func scaleLightCol(depth float64) int {
	if depth <= 0 {
		return maxLightScale - 1
	}
	j := int(wallScaleRef / depth)
	if j < 0 {
		return 0
	}
	if j > maxLightScale-1 {
		return maxLightScale - 1
	}
	return j
}

// shadeMul is the 0..256 brightness for a wall column of sector light level
// light at camera-space depth. A wall column has constant depth, so
// drawWallSpan calls this once for the whole span.
func shadeMul(light int16, depth float64) uint32 {
	return uint32(scaleLight[lightnumOf(light)][scaleLightCol(depth)])
}

// spriteShade is the 0..256 brightness a world sprite of sector light level
// `light` gets at camera-space `depth`. Sprites read the same scalelight
// table the walls do, indexed by the sprite's on-screen scale — which at
// this engine's reference is a wall's 2560/depth — so a monster or item
// shades to match the wall it stands against (id's R_ProjectSprite:
// spritelights = scalelight[lightlevel>>LIGHTSEGSHIFT], no fake contrast).
// A fullbright frame (id's FF_FULLBRIGHT — a lit powerup, a projectile, a
// monster's attack flash) is lit by nothing and returns 256 unchanged.
func spriteShade(light int16, depth float64, fullBright bool) uint32 {
	if fullBright {
		return 256
	}
	return shadeMul(light, depth)
}

// pspriteShade is the brightness for the player's own weapon viewmodel. id
// shades a psprite with spritelights[MAXLIGHTSCALE-1] — the bright end of
// its sector's scalelight, i.e. "point blank" for that light level — so the
// held weapon tracks how lit the room is with no distance falloff of its
// own. A fullbright frame (a muzzle-flash sprite) skips it.
func pspriteShade(light int16, fullBright bool) uint32 {
	if fullBright {
		return 256
	}
	return shadeMul(light, 0)
}

// lightRow returns a flat's zlight row for sector light level light.
// drawFlatSpan holds one row across a whole span and indexes it per screen
// row with distIdx, since a flat span's depth changes every row.
func lightRow(light int16) *[maxLightZ]uint16 {
	return &zLight[lightnumOf(light)]
}

// distIdx maps a camera-space depth to a zlight column: distance >>
// LIGHTZSHIFT, i.e. map units / 16, clamped to 0..MAXLIGHTZ-1.
func distIdx(depth float64) int {
	i := int(depth * (1.0 / 16.0))
	if i < 0 {
		return 0
	}
	if i > maxLightZ-1 {
		return maxLightZ - 1
	}
	return i
}

// LightLUT is raster's baked sector/distance shading, laid out for a GPU
// upload: Scale is the wall/sprite scalelight table (Levels rows of
// ScaleCols columns), Z the flat zlight table (Levels rows of ZCols), both
// row-major ([row*cols + col]), values 0..256 brightness multipliers (row 0
// = full bright). A hardware renderer uploads these as a texture and
// reproduces the exact vanilla fade with the index helpers below — the
// software path here calls the same underlying code, so the two can't drift.
type LightLUT struct {
	Levels           int
	ScaleCols, ZCols int
	Scale            []uint16
	Z                []uint16
}

// BuildLightLUT flattens the two package tables for upload.
func BuildLightLUT() LightLUT {
	l := LightLUT{
		Levels: lightLevels, ScaleCols: maxLightScale, ZCols: maxLightZ,
		Scale: make([]uint16, lightLevels*maxLightScale),
		Z:     make([]uint16, lightLevels*maxLightZ),
	}
	for i := 0; i < lightLevels; i++ {
		copy(l.Scale[i*maxLightScale:], scaleLight[i][:])
		copy(l.Z[i*maxLightZ:], zLight[i][:])
	}
	return l
}

// LightScaleRef is vanilla's fixed wall-scale reference (2560/depth) the
// shader needs to reproduce LightScaleColOf.
const LightScaleRef = wallScaleRef

// LightScaleValue reports lightScale (config lightScale) so a hardware
// renderer can pass it to its shader as a uniform.
func LightScaleValue() float64 { return lightScale }

// ambientLight is config ambientLight (section 17): a minimum brightness
// floor the enhanced/hardware pipelines apply after the sector fade and
// every dynamic light are summed. The vanilla colormap path ignores it —
// see Config.AmbientLight's doc for why.
var ambientLight = 0.0

// SetAmbientLight sets the global ambient-light floor. Config already
// clamps to [0, 1]; negative values are ignored defensively.
func SetAmbientLight(v float64) {
	if v >= 0 {
		ambientLight = v
	}
}

// AmbientLightValue reports ambientLight (config ambientLight) so a
// hardware renderer can pass it to its shader as a uniform.
func AmbientLightValue() float64 { return ambientLight }
