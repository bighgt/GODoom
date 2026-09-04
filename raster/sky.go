package raster

import (
	"math"

	"twopointfive/wad"
)

// skyFlatName is Doom's one hardcoded special flat name: a sector whose
// ceiling texture is exactly this gets the sky rendered instead of a
// normal textured flat — see drawCeilingSpan (renderer.go) and PrBoom's
// r_bsp.c, which checks "sector->ceilingpic == skyflatnum" the same way.
const skyFlatName = "F_SKY1"

// skyReferenceHeight is the texture height vanilla Doom's own sky-vertical
// constants (skyTextureMidRow below) were tuned for — a Doom sky texture is
// always 128 tall. The Mars skybox is much taller (2K), so those constants
// scale up proportionally by (actual height / this) rather than being
// fixed pixel counts — see drawSkySpan.
const skyReferenceHeight = 128.0

// skyReferenceScreenHeight is the screen height vanilla's skytexturemid /
// dc_iscale sky presentation assumed (SCREENHEIGHT = 200). drawSkySpan
// scales its vertical rate by this / r.Height so the sky fills the same
// share of the view at any render resolution instead of tiling — PrBoom's
// sky is likewise resolution-independent (dc_iscale scales with the view).
const skyReferenceScreenHeight = 200.0

// skyTextureMidRow is id's own skytexturemid (R_InitSkyMap:
// "skytexturemid = 100*FRACUNIT") at skyReferenceHeight: the texture row
// that lands at the screen's vertical center — not the texture's own
// midpoint, a fixed constant chosen so a sky texture's usually-plain upper
// portion gets more room than its horizon-detail lower portion.
const skyTextureMidRow = 100.0

// skyPixelsPerTurn is how many sky-texture columns a full 360° yaw scans —
// id's ANGLETOSKYSHIFT (22) makes a full circle (2^32 BAM) map to
// 2^32 >> 22 = 1024 texture pixels, so vanilla's 256-wide sky wraps 4x per
// turn. drawSkySpan keeps that for any narrow (WAD-format) sky; a wide
// panorama (the Mars skybox) instead maps its whole width to one turn, so
// it doesn't visibly repeat.
const skyPixelsPerTurn = 1024.0

// skyTextureForMap returns the sky *texture* name (TEXTURE1 entry, not the
// patch lump) PrBoom would pick for a given map marker — p_setup.c's
// P_SetupLevel: SKY1..4 by episode for Doom / Ultimate Doom, and SKY1
// (MAP01-11) / SKY2 (12-20) / SKY3 (21+) for Doom II & Final Doom. Both
// games' TEXTURE1 lumps define "SKY1".."SKYn" (the Doom II ones wrap the
// RSKYn patches), so this name resolves through WallTexture for either.
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
		default:
			return "SKY1"
		}
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
		default:
			return "SKY1"
		}
	}
	return "SKY1"
}

// drawSkySpan fills screen rows [yTop, yBottom) of column x with the sky
// texture, using Doom's core technique: rather than being positioned in
// the 3D world at all (there is no "sky sector" out past the level's
// walls), it's painted directly from the camera, not from BSP geometry —
// so walking never moves it. Full-bright: sector light / diminishing
// (light.go) never applies to the sky.
//
// Horizontal: turning yaws the sky, at skyPixelsPerTurn texture columns per
// 360° for a WAD sky (id's ANGLETOSKYSHIFT — a 256-wide sky wraps 4x), or
// once around for a wide panorama. Vertical: the texture row is a linear
// function of screen row, anchored so skyTextureMidRow sits at the
// pitch-adjusted horizon (id's skytexturemid), at a rate scaled to the
// render height so the sky doesn't tile as resolution rises.
//
// Both axes use one mapping shared by every column and row — u depends only
// on x and cam.Angle, v only on y and the horizon — not fit locally to
// each column's exposed wall silhouette (an earlier version did, and the
// per-column scale variance bulged the flat backdrop into a "goldfish
// bowl").
func (r *Renderer) drawSkySpan(x, yTop, yBottom int) {
	if yBottom <= yTop {
		return
	}
	tex := r.skyTex
	if tex == nil {
		return
	}
	tx := r.skyTx[x]
	scale := r.skyScale
	texW, texH := tex.Width, tex.Height
	src := tex.Pix

	// Sky panoramas (Mars skybox, or a 128-tall WAD sky) are power-of-two
	// tall, so mask instead of a per-row modulo.
	texHMask := texH - 1
	texHPow2 := texH&texHMask == 0

	// v is linear in screen row — anchored to skyTextureMidRow at the
	// (pitch-shifted) horizon — so step it by a constant instead of
	// recomputing per row. The 200/Height factor keeps the vanilla 200px
	// presentation at any render resolution.
	vStep := scale * (skyReferenceScreenHeight / float64(r.Height))
	v := skyTextureMidRow*scale + (float64(yTop)-r.horizonY())*vStep

	dst := r.Pix
	i := (yTop*r.Width + x) * 4
	stride := r.Width * 4
	for y := yTop; y < yBottom; y++ {
		var ty int
		if texHPow2 {
			ty = int(math.Floor(v)) & texHMask
		} else {
			ty = wrapInt(int(math.Floor(v)), texH)
		}
		v += vStep
		s := (ty*texW + tx) * 4
		dst[i] = src[s]
		dst[i+1] = src[s+1]
		dst[i+2] = src[s+2]
		dst[i+3] = src[s+3]
		i += stride
	}
}

// buildSkyTable resolves this frame's sky bitmap and fills r.skyTx (texture
// column per screen column) so drawSkySpan is a plain texel copy with no
// trig. The sky texture is the external Mars panorama when present (see
// assets.LoadSkybox), otherwise the WAD's own sky for this map, picked the
// way PrBoom does (skyTextureForMap). The per-column view angle only
// depends on x and the focal length, so it's recomputed only on a FOV
// change; the resolved WAD sky is cached per map name.
func (r *Renderer) buildSkyTable(cam *Camera, level *wad.Level) {
	mapName := ""
	if level != nil {
		mapName = level.Name
	}

	tex := r.skybox // the disk panorama overrides the WAD sky when it loaded
	if tex == nil {
		if r.skyWADTex == nil || r.skyWADFor != mapName {
			name := skyTextureForMap(mapName)
			if r.skyNameOverride != "" {
				name = r.skyNameOverride
			}
			t, ok := r.textures.WallTexture(name)
			if !ok || t == nil {
				t, _ = r.textures.WallTexture("SKY1") // last-ditch: the one every IWAD defines
			}
			r.skyWADTex, r.skyWADFor = t, mapName
		}
		tex = r.skyWADTex
	}
	r.skyTex = tex
	if tex == nil {
		return
	}
	r.skyScale = float64(tex.Height) / skyReferenceHeight

	// Columns a full 360° yaw scans: a wide panorama maps its whole width to
	// one turn (so it doesn't repeat); a narrow WAD sky uses id's
	// ANGLETOSKYSHIFT constant (1024) so a 256-wide sky wraps 4x, the vanilla
	// feel.
	pixPerTurn := skyPixelsPerTurn
	if float64(tex.Width) > skyPixelsPerTurn {
		pixPerTurn = float64(tex.Width)
	}

	if r.skyColAngleFocal != r.focal {
		halfW := float64(r.Width) / 2
		for x := 0; x < r.Width; x++ {
			r.skyColAngle[x] = math.Atan2(float64(x)+0.5-halfW, r.focal)
		}
		r.skyColAngleFocal = r.focal
	}

	half := pixPerTurn / 2
	perRad := pixPerTurn / (2 * math.Pi)
	for x := 0; x < r.Width; x++ {
		// Screen-column angle plus the camera yaw, scaled to sky columns.
		u := half + (r.skyColAngle[x]-cam.Angle)*perRad
		r.skyTx[x] = wrapInt(int(math.Floor(u)), tex.Width)
	}
}
