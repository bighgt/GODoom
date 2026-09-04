package engine

import (
	"math"
	"testing"

	"twopointfive/assets"
	"twopointfive/bsp"
	"twopointfive/raster"
)

func voidPct2(pix []byte) int {
	n := 0
	for i := 0; i+3 < len(pix); i += 4 {
		if pix[i] == 0 && pix[i+1] == 0 && pix[i+2] == 0 && pix[i+3] == 255 {
			n++
		}
	}
	return n * 100 / (len(pix) / 4)
}

// TestWalkDoom2Map01ToExit walks the player from the start toward the exit
// switch with real tryMove collision, opening doors on the way, rendering
// each step from the real eye position and both far shoulders. Guards
// against a whole room rendering as black, see-through void — the reported
// Doom II MAP01 regression — from a spot the player can actually stand in.
func TestWalkDoom2Map01ToExit(t *testing.T) {
	g := loadRealLevel(t, "../wad/Doom2.wad", "MAP01")
	tex, err := assets.New(g.WAD)
	if err != nil {
		t.Fatalf("assets.New: %v", err)
	}
	ras := raster.New(tex, 1280, 800)

	var sx, sy, sa float64
	for _, th := range g.Level.Things {
		if th.Type == 1 {
			sx, sy = float64(th.X), float64(th.Y)
			sa = float64(th.Angle) * math.Pi / 180
		}
	}
	var ex, ey float64
	for i := range g.Level.Linedefs {
		if g.Level.Linedefs[i].SpecialType == 11 {
			v1 := g.Level.Vertexes[g.Level.Linedefs[i].StartVertex]
			v2 := g.Level.Vertexes[g.Level.Linedefs[i].EndVertex]
			ex = (float64(v1.X) + float64(v2.X)) / 2
			ey = (float64(v1.Y) + float64(v2.Y)) / 2
		}
	}

	sec := bsp.PointSector(g.BSP, g.Level, float32(sx), float32(sy))
	g.Camera.X, g.Camera.Y, g.Camera.Z = sx, sy, float64(sec.FloorHeight)+EyeHeight
	g.Camera.Angle = sa
	g.playerMobj.X, g.playerMobj.Y = sx, sy

	worst := 0
	for step := 0; step < 400; step++ {
		ang := math.Atan2(ey-g.Camera.Y, ex-g.Camera.X)
		g.Camera.Angle = ang
		g.tryUseLine()
		for k := 0; k < 6; k++ {
			g.runTic()
		}
		g.tryMove(math.Cos(ang)*12, math.Sin(ang)*12)
		if ps := bsp.PointSector(g.BSP, g.Level, float32(g.Camera.X), float32(g.Camera.Y)); ps != nil {
			g.Camera.Z = float64(ps.FloorHeight) + EyeHeight
		}
		g.playerMobj.X, g.playerMobj.Y = g.Camera.X, g.Camera.Y

		for _, da := range []float64{-1.2, -0.6, 0, 0.6, 1.2} {
			ras.Render(g.Level, g.BSP, raster.Camera{
				X: g.Camera.X, Y: g.Camera.Y, Z: g.Camera.Z, Angle: ang + da, FOVDeg: 100,
			})
			v := voidPct2(ras.Pix)
			if v > worst {
				worst = v
			}
			if v > 65 {
				t.Errorf("step %d (%.0f,%.0f) ang=%.2f: %d%% of the frame is undrawn void",
					step, g.Camera.X, g.Camera.Y, ang+da, v)
			}
		}
		if math.Hypot(ex-g.Camera.X, ey-g.Camera.Y) < 80 {
			break
		}
	}
	t.Logf("worst void along the walked path start->exit: %d%%", worst)
}
