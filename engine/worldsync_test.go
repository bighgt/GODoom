package engine

import (
	"testing"

	"twopointfive/assets"
	"twopointfive/render/worldgeo"
	"twopointfive/wad"
)

// meshVert is one drawn vertex plus the texture its draw binds — what the
// GPU actually renders, independent of how the builder split or ordered it.
type meshVert struct {
	X, Y, Z, U, V, Light float32
	Kind                 uint16
	Tex                  *assets.RGBA
}

// drawnMesh is the multiset of everything g draws for the level itself:
// the static mesh (StaticDraws + WaterDraws) plus this frame's moving
// sectors (LiveDraws).
func drawnMesh(g *worldgeo.Geometry) map[meshVert]int {
	m := map[meshVert]int{}
	add := func(tris []worldgeo.Vert, draws []worldgeo.Draw) {
		for _, d := range draws {
			for _, v := range tris[d.First : d.First+d.Count] {
				m[meshVert{v.X, v.Y, v.Z, v.U, v.V, v.Light, v.Kind, d.Tex}]++
			}
		}
	}
	add(g.StaticTris, g.StaticDraws)
	add(g.StaticTris, g.WaterDraws)
	add(g.Tris, g.LiveDraws)
	return m
}

func sameMesh(a, b map[meshVert]int) (missing, extra int) {
	for k, n := range a {
		if d := n - b[k]; d > 0 {
			extra += d
		}
	}
	for k, n := range b {
		if d := n - a[k]; d > 0 {
			missing += d
		}
	}
	return
}

// TestWorldSyncMatchesFullRebuild: with a door opening and closing and the
// nukage animating, what the hardware path draws each frame — the static
// mesh, synced by moving-sector tracking and texture repointing instead of
// rebuilds, plus the moving sectors re-emitted per frame — is vertex for
// vertex what a from-scratch build of the level at that moment draws. And
// the door costs a couple of static rebuilds, not one per height change.
func TestWorldSyncMatchesFullRebuild(t *testing.T) {
	g := loadRealLevel(t, "../testdata/DOOM1.WAD", "E1M1")
	t.Chdir("..")
	tex, err := assets.New(g.WAD)
	if err != nil {
		t.Fatal(err)
	}
	g.WorldBuilder = worldgeo.New(g.Level, g.BSP, tex)
	door := -1
	for i, ld := range g.Level.Linedefs {
		if ld.SpecialType == 1 && ld.BackSidedef != wad.NoSidedef { // DR door
			door = i
			break
		}
	}
	if door < 0 {
		t.Fatal("no DR door on E1M1")
	}

	frame := func() *worldgeo.Geometry {
		g.ticAccum = 0 // draw exactly the tic's heights, so a fresh build can match
		restore := g.syncWorldGeometry()
		geom := g.WorldBuilder.Build(0, 0)
		restore()
		return geom
	}
	check := func(when string) {
		t.Helper()
		got := drawnMesh(frame())
		want := drawnMesh(worldgeo.New(g.Level, g.BSP, tex).Build(0, 0))
		if missing, extra := sameMesh(got, want); missing+extra != 0 {
			t.Fatalf("%s: drawn mesh differs from a full rebuild — %d vertices missing, %d extra", when, missing, extra)
		}
	}

	doorSec := int(g.Level.Sidedefs[g.Level.Linedefs[door].BackSidedef].Sector)
	check("start")
	for range int(2 * ticsPerSecond) { // let the level settle: flickering lights, scrollers
		g.runTic()
		frame()
	}
	check("settled")
	rebuilds, last := 0, g.WorldBuilder.Build(0, 0).StaticVersion
	g.activateLine(door, g.playerMobj, trUse)
	liveFrames := 0
	for tic := 1; tic <= 12*ticsPerSecond; tic++ {
		g.runTic()
		geom := frame()
		if geom.StaticVersion != last {
			rebuilds++
			last = geom.StaticVersion
		}
		if len(geom.LiveDraws) > 0 {
			liveFrames++
		}
		if tic%17 == 0 {
			check("tic " + itoa(tic))
		}
	}
	if liveFrames == 0 {
		t.Fatal("the door never showed up as moving geometry")
	}
	if g.isLiveSector(doorSec) {
		t.Error("door still in the live set 12s later")
	}
	// Opening, (staying in the set through its 4s wait), closing, settling:
	// one rebuild to lift it out, one to put it back.
	if rebuilds > 3 {
		t.Errorf("%d static rebuilds for one door cycle, want ~2", rebuilds)
	}
	t.Logf("door cycle: %d static rebuilds over %d frames with live geometry", rebuilds, liveFrames)
	check("end")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}
