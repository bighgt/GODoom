package engine

import (
	"math"
	"os"
	"testing"

	"twopointfive/assets"
	"twopointfive/bsp"
	"twopointfive/raster"
)

// TestVoxelsReplaceSprites is the end-to-end check for the reported "sprites
// are not being replaced by voxels" bug: with a real level, the real Doom 1
// voxel pack, and an imp spawned in front of the camera, the rendered frame
// with Game.Voxels set must differ from the frame drawn with sprites — and
// raster.DrawVoxel called directly for that imp must actually draw.
func TestVoxelsReplaceSprites(t *testing.T) {
	const packPath = "../assets/voxel/Doom1_Voxel.pk3"
	if _, err := os.Stat(packPath); err != nil {
		t.Skipf("no %s", packPath)
	}
	vs, err := assets.LoadVoxelPack(packPath)
	if err != nil {
		t.Fatalf("LoadVoxelPack: %v", err)
	}

	g := loadRealLevel(t, "../testdata/DOOM1.WAD", "E1M1")
	tex, err := assets.New(g.WAD)
	if err != nil {
		t.Fatalf("assets.New: %v", err)
	}
	const w, h = 640, 400
	ras := raster.New(tex, w, h)
	g.Raster = ras

	// Stand at the player-1 start, look east (+X).
	var sx, sy float64
	for _, th := range g.Level.Things {
		if th.Type == 1 {
			sx, sy = float64(th.X), float64(th.Y)
		}
	}
	sec := bsp.PointSector(g.BSP, g.Level, float32(sx), float32(sy))
	if sec == nil {
		t.Fatal("player start not in a sector")
	}
	floor := float64(sec.FloorHeight)
	g.Camera = raster.Camera{X: sx, Y: sy, Z: floor + EyeHeight, Angle: 0, FOVDeg: 90}

	// Put an imp just east of the start, on the floor, facing the player —
	// close enough that it fills a good part of the view with clearance.
	imp := g.P_SpawnMobj(sx+72, sy, onFloorZ, MT_TROOP)
	imp.Angle = math.Pi // faces -X, toward the camera
	if got := spriteNames[imp.Sprite]; got != "TROO" {
		t.Fatalf("imp sprite = %q, want TROO", got)
	}

	// The pack must actually have a model for the imp's spawn frame.
	m, _, _, ok := vs.Model(spriteNames[imp.Sprite], imp.Frame)
	if !ok {
		t.Fatalf("voxel pack has no model for TROO frame %d — key lookup mismatch", imp.Frame)
	}

	// Frame A: voxels on.
	g.Voxels = vs
	ras.Render(g.Level, g.BSP, g.Camera)
	g.drawMobjs()
	voxFrame := append([]byte(nil), ras.Pix...)

	// Frame B: voxels off (sprite path).
	g.Voxels = nil
	ras.Render(g.Level, g.BSP, g.Camera)
	g.drawMobjs()
	sprFrame := append([]byte(nil), ras.Pix...)

	diff := 0
	for i := range voxFrame {
		if voxFrame[i] != sprFrame[i] {
			diff++
		}
	}
	if diff == 0 {
		t.Fatal("voxel frame is byte-identical to the sprite frame — DrawVoxel changed nothing")
	}
	t.Logf("voxel vs sprite frame differ in %d/%d bytes", diff, len(voxFrame))

	// And DrawVoxel itself must report it drew, and paint something.
	ras.Render(g.Level, g.BSP, g.Camera)
	before := append([]byte(nil), ras.Pix...)
	drew := ras.DrawVoxel(g.Camera, imp.X, imp.Y, imp.Z, imp.Angle, sec.LightLevel, false, 0, m)
	if !drew {
		t.Fatalf("DrawVoxel declined the imp (model %dx%dx%d at ~160u)", m.XSiz, m.YSiz, m.ZSiz)
	}
	painted := 0
	for i := 0; i+3 < len(ras.Pix); i += 4 {
		if ras.Pix[i] != before[i] || ras.Pix[i+1] != before[i+1] || ras.Pix[i+2] != before[i+2] {
			painted++
		}
	}
	if painted < 300 {
		t.Fatalf("DrawVoxel returned true but painted only %d pixels — model is barely drawing", painted)
	}
	t.Logf("DrawVoxel painted %d pixels for the imp", painted)

	// Enhanced (G-buffer) lighting mode — config.json's "lightingMode":
	// "enhanced" — must also route the imp through DrawVoxel and stamp the
	// G-buffer planes, not silently fall back to (or drop) the sprite.
	lit := raster.New(tex, w, h)
	lit.SetGBuffer(true)
	g.Raster = lit
	g.Voxels = vs
	lit.Render(g.Level, g.BSP, g.Camera)
	g.drawMobjs()
	litVox := append([]byte(nil), lit.Pix...)
	litNrm := append([]byte(nil), lit.Normal...)

	lit2 := raster.New(tex, w, h)
	lit2.SetGBuffer(true)
	g.Raster = lit2
	g.Voxels = nil
	lit2.Render(g.Level, g.BSP, g.Camera)
	g.drawMobjs()

	albedoDiff, normalDiff := 0, 0
	for i := range litVox {
		if litVox[i] != lit2.Pix[i] {
			albedoDiff++
		}
		if litNrm[i] != lit2.Normal[i] {
			normalDiff++
		}
	}
	if albedoDiff == 0 || normalDiff == 0 {
		t.Fatalf("enhanced mode: voxel G-buffer identical to sprite (albedoDiff=%d normalDiff=%d)", albedoDiff, normalDiff)
	}
	t.Logf("enhanced mode: G-buffer differs — albedo %d bytes, normal %d bytes", albedoDiff, normalDiff)
}

// TestVoxelMatchesSpriteSize guards the "voxels are ~20% too large" fix: a
// voxel model is projected at the thing's centre depth, not with per-column
// perspective, so — viewed head-on or from behind, the common cases — it
// occupies the same screen footprint as the flat sprite it replaces.
func TestVoxelMatchesSpriteSize(t *testing.T) {
	const packPath = "../assets/voxel/Doom2_Voxel.pk3"
	if _, err := os.Stat(packPath); err != nil {
		t.Skipf("no %s", packPath)
	}
	vs, err := assets.LoadVoxelPack(packPath)
	if err != nil {
		t.Fatalf("LoadVoxelPack: %v", err)
	}
	g := loadRealLevel(t, "../wad/Doom2.wad", "MAP01")
	tex, err := assets.New(g.WAD)
	if err != nil {
		t.Fatalf("assets.New: %v", err)
	}
	const w, h = 900, 700
	ras := raster.New(tex, w, h)
	ras.SetTextureQuality("nearest") // hard voxel splats (this test compares exact pixels)

	var sx, sy float64
	for _, th := range g.Level.Things {
		if th.Type == 1 {
			sx, sy = float64(th.X), float64(th.Y)
		}
	}
	sec := bsp.PointSector(g.BSP, g.Level, float32(sx), float32(sy))
	floor := float64(sec.FloorHeight)

	bbox := func(base []byte) (bw, bh int) {
		x0, y0, x1, y1 := w, h, -1, -1
		for p := 0; p < w*h; p++ {
			i := p * 4
			if base[i] == ras.Pix[i] && base[i+1] == ras.Pix[i+1] && base[i+2] == ras.Pix[i+2] {
				continue
			}
			x, y := p%w, p/w
			if x < x0 {
				x0 = x
			}
			if y < y0 {
				y0 = y
			}
			if x > x1 {
				x1 = x
			}
			if y > y1 {
				y1 = y
			}
		}
		if x1 < 0 {
			return 0, 0
		}
		return x1 - x0 + 1, y1 - y0 + 1
	}

	for _, tc := range []struct {
		prefix string
		typ    mobjType
	}{
		{"TROO", MT_TROOP}, {"POSS", MT_POSSESSED}, {"SPOS", MT_SHOTGUY},
		{"SARG", MT_SERGEANT}, {"HEAD", MT_HEAD},
	} {
		m, ao, _, ok := vs.Model(tc.prefix, 0)
		if !ok {
			continue
		}
		if _, _, sok := tex.SpriteFrame(tc.prefix, 0, 0); !sok {
			continue
		}
		g.mobjs = g.mobjs[:0]
		g.Camera = raster.Camera{X: sx, Y: sy, Z: floor + EyeHeight, Angle: 0, FOVDeg: 90}
		mo := g.P_SpawnMobj(sx+130, sy, onFloorZ, tc.typ)

		ras.Render(g.Level, g.BSP, g.Camera)
		base := append([]byte(nil), ras.Pix...)

		ras.Render(g.Level, g.BSP, g.Camera)
		ras.DrawThing(g.Camera, mo.X, mo.Y, mo.Z, math.Pi, sec.LightLevel, false, tc.prefix, 0)
		sW, sH := bbox(base)
		if sW == 0 {
			t.Fatalf("%s: sprite drew nothing", tc.prefix)
		}

		for _, face := range []struct {
			name string
			ang  float64
		}{{"front", math.Pi}, {"back", 0}} {
			ras.Render(g.Level, g.BSP, g.Camera)
			ras.DrawVoxel(g.Camera, mo.X, mo.Y, mo.Z, face.ang, sec.LightLevel, false, ao, m)
			vW, vH := bbox(base)
			rw, rh := float64(vW)/float64(sW), float64(vH)/float64(sH)
			if rh < 0.9 || rh > 1.1 {
				t.Errorf("%s %s: voxel height %d vs sprite %d (ratio %.2f) — outside 10%%", tc.prefix, face.name, vH, sH, rh)
			}
			if rw < 0.85 || rw > 1.15 {
				t.Errorf("%s %s: voxel width %d vs sprite %d (ratio %.2f) — outside 15%%", tc.prefix, face.name, vW, sW, rw)
			}
		}
	}
}
