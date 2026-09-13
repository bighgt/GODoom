package engine

import (
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"testing"

	"twopointfive/assets"
	"twopointfive/bsp"
	"twopointfive/config"
	"twopointfive/raster"
	"twopointfive/wad"
)

// TestHereticScreenshot renders a few Heretic frames (geometry + spawned
// things + voxels) to PNGs for eyeballing. Opt-in:
//   HERETIC_SHOT=1 go test -run TestHereticScreenshot ./engine/
func TestHereticScreenshot(t *testing.T) {
	if os.Getenv("HERETIC_SHOT") == "" {
		t.Skip("set HERETIC_SHOT=1 to render screenshots (HERETIC_SHOT_DIR overrides the output dir)")
	}
	outDir := os.Getenv("HERETIC_SHOT_DIR")
	if outDir == "" {
		outDir = t.TempDir()
	}
	const wadPath = "../wad/heretic.wad"

	w, err := wad.Load(wadPath)
	if err != nil {
		t.Fatal(err)
	}
	tex, err := assets.New(w)
	if err != nil {
		t.Fatal(err)
	}
	vs, verr := assets.LoadVoxelPack("../assets/voxel/reikallhereticvoxels.pk3")
	if verr != nil {
		t.Logf("voxels: %v", verr)
	}

	for _, mapName := range []string{"E1M1", "E2M1", "E3M4"} {
		lvl, err := w.LoadLevel(mapName)
		if err != nil {
			t.Fatal(err)
		}
		tree, err := bsp.Build(lvl)
		if err != nil {
			t.Fatal(err)
		}
		g := NewGame(nil, nil, nil, w, lvl, tree, raster.Camera{}, config.Config{Skill: 3})
		g.Voxels = vs
		for i := 0; i < 10; i++ {
			g.runTic() // let monsters settle into a natural idle frame
		}

		const W, H = 1024, 640
		r := raster.New(tex, W, H)
		g.Raster = r // so g.drawHUD() has a renderer

		var px, py, pang float64
		for _, thg := range lvl.Things {
			if thg.Type == 1 {
				px, py, pang = float64(thg.X), float64(thg.Y), float64(thg.Angle)
			}
		}
		z := 41.0
		if sec := bsp.PointSector(tree, lvl, float32(px), float32(py)); sec != nil {
			z = float64(sec.FloorHeight) + 41
		}

		// An extra shot from just behind the nearest Heretic monster,
		// looking at it — so a monster is actually in frame.
		var near *Mobj
		bestD := math.MaxFloat64
		for _, mo := range g.mobjs {
			if mo.hact == nil {
				continue
			}
			if d := math.Hypot(mo.X-px, mo.Y-py); d < bestD {
				bestD, near = d, mo
			}
		}
		if near != nil {
			ang := math.Atan2(near.Y-py, near.X-px)
			cx := near.X - 220*math.Cos(ang)
			cy := near.Y - 220*math.Sin(ang)
			cz := z
			if sec := bsp.PointSector(tree, lvl, float32(cx), float32(cy)); sec != nil {
				cz = float64(sec.FloorHeight) + 41
			}
			cam := raster.Camera{X: cx, Y: cy, Z: cz, Angle: ang, FOVDeg: 90}
			r.Render(lvl, tree, cam)
			drawThings(g, r, cam, vs)
			saveShot(t, fmt.Sprintf("%s/HER_%s_mob.png", outDir, mapName), r.Pix, W, H)
		}

		for i, d := range []float64{0, 90, 180, 270} {
			cam := raster.Camera{X: px, Y: py, Z: z, Angle: (pang + d) * math.Pi / 180, FOVDeg: 90}
			r.Render(lvl, tree, cam)
			drawThings(g, r, cam, vs)
			if i == 0 {
				// one frame with the weapon (mid-fire) + status bar
				def := Weapons[g.Weapon.Current]
				r.DrawWeapon(def.SpritePrefix, 'B', def.FlashPrefix, 0, false, 0, 0, 0, 1, 220)
				g.drawHUD()
				r.CompositeOverlay()
			}
			saveShot(t, fmt.Sprintf("%s/HER_%s_%d.png", outDir, mapName, i*90), r.Pix, W, H)
		}

		// The Quake II-style HUD, with keys + an artifact held so the whole
		// cluster is exercised (DrawHereticQuakeHUD).
		{
			cam := raster.Camera{X: px, Y: py, Z: z, Angle: pang * math.Pi / 180, FOVDeg: 90}
			r.Render(lvl, tree, cam)
			drawThings(g, r, cam, vs)
			g.hudStyle = hudQuake2
			g.Player.Keys = [6]bool{true, true, true, false, false, false}
			g.Player.Armor = 120
			g.Player.Inventory[artiTome] = 3
			g.Player.ArtiCurrent = int(artiTome)
			def := Weapons[g.Weapon.Current]
			r.DrawWeapon(def.SpritePrefix, 'A', def.FlashPrefix, 0, false, 0, 0, 0, 1, 220)
			g.drawHUD()
			r.CompositeOverlay()
			saveShot(t, fmt.Sprintf("%s/HER_%s_quakehud.png", outDir, mapName), r.Pix, W, H)
		}
		t.Logf("%s: %d mobjs rendered -> %s/HER_%s_*.png", mapName, len(g.mobjs), outDir, mapName)
	}
}

func drawThings(g *Game, r *raster.Renderer, cam raster.Camera, vs *assets.VoxelSet) {
	for _, mo := range g.mobjs {
		if mo.State == S_NULL {
			continue
		}
		name := mobjSpriteName(mo)
		sl := g.spriteSectorLight(mo.X, mo.Y)
		if vs != nil {
			if m, angOff, _, ok := vs.Model(name, mo.Frame); ok && m != nil {
				if r.DrawVoxel(cam, mo.X, mo.Y, mo.Z, mo.Angle, sl, mo.Fullbright, angOff, m) {
					continue
				}
			}
		}
		r.DrawThing(cam, mo.X, mo.Y, mo.Z, mo.Angle, sl, mo.Fullbright, name, mo.Frame)
	}
}

func saveShot(t *testing.T, path string, pix []byte, w, h int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	copy(img.Pix, pix)
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 255
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}
