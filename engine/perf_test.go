package engine

import (
	"archive/zip"
	"math"
	"testing"

	"twopointfive/assets"
	"twopointfive/assets/models"
	"twopointfive/raster"
	"twopointfive/render/worldgeo"
)

// Benchmarks for the CPU side of play: one simulation tic, and one frame's
// scene preparation (buildWorldFrame — everything but the GPU submit). Run
// with e.g.
//
//	go test ./engine -run '^$' -bench . -benchmem -cpuprofile cpu.out
//
// Doom II MAP29 with every monster awake and hunting the player (in god
// mode): the AI, missiles, sight checks, model blending, particles and
// lights are all busy, the way they are mid-fight.

const perfWAD, perfMap = "wad/Game WADs/doom2.wad", "MAP29"

func perfGame(b *testing.B) *Game {
	b.Helper()
	b.Chdir("..")
	g := loadRealLevel(b, perfWAD, perfMap)
	tex, err := assets.New(g.WAD)
	if err != nil {
		b.Fatal(err)
	}
	g.Raster = raster.New(tex, 1280, 720)
	g.WorldBuilder = worldgeo.New(g.Level, g.BSP, g.Raster.Textures())
	if vs, err := assets.LoadVoxelPackDir("Doom2_Voxel.pk3"); err == nil {
		g.Voxels = vs
	}
	if zr, err := zip.OpenReader("assets/import/3D_Models.pk3"); err == nil {
		g.Models, _ = models.Load(&zr.Reader)
		zr.Close()
	}
	g.ModelScale = 1
	g.impFireballs, g.lostSoulFire, g.smoothMonsterMovement, g.monsterRoaming = true, true, true, true
	g.explosionParticles, g.liquidAmbience = true, true
	g.godMode = true
	g.Camera.FOVDeg = 100
	for _, th := range g.Level.Things {
		if th.Type == 1 { // player 1 start
			g.Camera.X, g.Camera.Y = float64(th.X), float64(th.Y)
			g.Camera.Angle = float64(th.Angle) * math.Pi / 180
			break
		}
	}
	g.PlayerZ, _ = g.sectorFloorCeil(g.Camera.X, g.Camera.Y)
	g.Camera.Z = g.PlayerZ + EyeHeight
	g.playerMobj.X, g.playerMobj.Y, g.playerMobj.Z = g.Camera.X, g.Camera.Y, g.PlayerZ
	for _, mo := range g.mobjs { // wake the level
		if canRoam(mo) {
			mo.Target = g.playerMobj
			g.setMobjState(mo, mo.Info.SeeState)
		}
	}
	for i := 0; i < 5*35; i++ { // let the fight develop: missiles out, monsters moving
		g.runTic()
	}
	return g
}

func BenchmarkTic(b *testing.B) {
	g := perfGame(b)
	b.ReportMetric(float64(len(g.mobjs)), "mobjs")
	b.ResetTimer()
	for range b.N {
		g.runTic()
	}
}

func BenchmarkWorldFrame(b *testing.B) {
	g := perfGame(b)
	b.ReportMetric(float64(len(g.mobjs)), "mobjs")
	g.buildWorldFrame() // first build of the static geometry
	b.ResetTimer()
	for i := range b.N {
		g.Camera.Angle += 0.01 // look around, as a player does
		g.ticAccum = ticDuration * float64(i%7) / 7
		g.updateMobjFire(1.0 / 144)
		g.updateParticles(1.0 / 144)
		g.buildWorldFrame()
	}
}

// BenchmarkStaticRebuild is one full static-geometry rebuild — what a frame
// pays (before the GPU re-upload) whenever a moving door or lift's drawn
// height changes.
func BenchmarkStaticRebuild(b *testing.B) {
	g := perfGame(b)
	b.ReportMetric(float64(len(g.WorldBuilder.Build(0, 0).StaticTris)), "verts")
	b.ResetTimer()
	for range b.N {
		g.WorldBuilder.Invalidate()
	}
}
