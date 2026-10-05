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

// TestMapObjectsDrawAsModels: with a model pack loaded (the community pack
// in assets/import, when present) the first-person collector draws every
// map object whose sprite frame has a model AS that model — never also as
// its voxel or a sprite — keeps voxels for the rest, and draws the
// player's plasma shot as its model instead of a sprite.
func TestMapObjectsDrawAsModels(t *testing.T) {
	t.Chdir("..")
	zr, err := zip.OpenReader("assets/import/3D_Models.pk3")
	if err != nil {
		t.Skip("assets/import/3D_Models.pk3 not available")
	}
	defer zr.Close()
	ms, errs := models.Load(&zr.Reader)
	if len(errs) > 0 {
		t.Fatalf("model pack: %v", errs)
	}
	g := loadRealLevel(t, "wad/Game WADs/doom2.wad", "MAP01")
	tex, err := assets.New(g.WAD)
	if err != nil {
		t.Fatal(err)
	}
	g.Raster = raster.New(tex, 640, 400)
	if vs, err := assets.LoadVoxelPackDir("Doom2_Voxel.pk3"); err == nil {
		g.Voxels = vs
	}
	g.Models = ms

	sawModel := false
	for _, mo := range g.mobjs {
		if !isMapObject(mo) || len(ms.Parts(mobjSpriteName(mo), mo.Frame)) == 0 {
			continue
		}
		// Look at this object from just behind it.
		g.Camera.X, g.Camera.Y, g.Camera.Angle = mo.X-96, mo.Y, 0
		sprs, vox := g.collectWorldObjects()
		want := len(ms.Parts(mobjSpriteName(mo), mo.Frame))
		got := 0
		for _, m := range g.hwModels {
			if m.Model[12] > float32(mo.X)-64 && m.Model[12] < float32(mo.X)+64 &&
				m.Model[13] > float32(mo.Y)-64 && m.Model[13] < float32(mo.Y)+64 {
				got++
			}
		}
		if got < want {
			t.Errorf("%s: %d model part(s) drawn, want %d", mo.Info.Name, got, want)
		}
		for _, s := range sprs {
			if s.X == float32(mo.X) && s.Y == float32(mo.Y) {
				t.Errorf("%s has a model but was also drawn as a sprite", mo.Info.Name)
			}
		}
		for _, v := range vox {
			if v.X == float32(mo.X) && v.Y == float32(mo.Y) {
				t.Errorf("%s has a model but was also drawn as a voxel", mo.Info.Name)
			}
		}
		sawModel = true
	}
	if !sawModel {
		t.Fatal("no map object on MAP01 has a model — test setup")
	}

	// The player's plasma shot, in flight in front of the camera.
	g.projectiles = nil
	g.Camera.X, g.Camera.Y, g.Camera.Angle = g.playerMobj.X, g.playerMobj.Y, 0
	resetWeaponTables()
	g.projectiles = append(g.projectiles, &Projectile{
		X: g.Camera.X + 64, Y: g.Camera.Y, Z: g.Camera.Z, DX: 1, def: Weapons[wpPlasma].Projectile, Alive: true,
	})
	sprs, _ := g.collectWorldObjects()
	px, py := float32(g.Camera.X+64), float32(g.Camera.Y)
	for _, s := range sprs {
		if s.X == px && s.Y == py {
			t.Error("the plasma shot has a model but was drawn as a sprite")
		}
	}
	found := false
	for _, m := range g.hwModels {
		if m.Model[12] == px && m.Model[13] == py { // the part's translation: the shot's position
			found = true
		}
	}
	if !found {
		t.Error("the plasma shot wasn't drawn as its model")
	}
}

// TestModelFrameBlendingAndScale: a walking monster's model blends from its
// current frame toward the next state's frame (GZDoom's interpolation),
// further along the later in the state it is; and config modelScale sizes
// 3D models exactly as it does voxels.
func TestModelFrameBlendingAndScale(t *testing.T) {
	t.Chdir("..")
	zr, err := zip.OpenReader("assets/import/3D_Models.pk3")
	if err != nil {
		t.Skip("assets/import/3D_Models.pk3 not available")
	}
	defer zr.Close()
	ms, _ := models.Load(&zr.Reader)
	g := loadRealLevel(t, "wad/Game WADs/doom2.wad", "MAP01")
	g.Models, g.ModelScale = ms, 1
	var mo *Mobj
	for _, m := range g.mobjs {
		if m.Type == MT_POSSESSED || m.Type == MT_SHOTGUY || m.Type == MT_TROOP {
			mo = m
			break
		}
	}
	if mo == nil {
		t.Fatal("no zombieman / sergeant / imp on MAP01")
	}
	g.setMobjState(mo, mo.Info.SeeState) // walking: every frame has a model
	tics := mo.stateDef.Tics

	blend := func() (*worldgeo.ModelInstance, bool) {
		g.hwModels = g.hwModels[:0]
		if !g.appendMobjModel(mo, mobjSpriteName(mo), mo.X, mo.Y, mo.Z, mo.Angle) || len(g.hwModels) == 0 {
			return nil, false
		}
		return &g.hwModels[0], true
	}
	mo.Tics = tics // state just began
	early, ok := blend()
	if !ok || early.Next == nil {
		t.Fatalf("%s walking: no blend target (ok=%v)", mo.Info.Name, ok)
	}
	earlyLerp := early.Lerp
	mo.Tics = 1 // nearly done
	late, _ := blend()
	if late.Next == nil || late.Lerp <= earlyLerp || late.Lerp > 1 {
		t.Errorf("blend amount early %.2f -> late %.2f, want increasing within 0..1", earlyLerp, late.Lerp)
	}

	// modelScale 2 doubles the model's size (the matrix's X column).
	g.ModelScale = 1
	one, _ := blend()
	colLen := func(m [16]float32) float64 { return math.Sqrt(float64(m[0]*m[0] + m[1]*m[1] + m[2]*m[2])) }
	base := colLen(one.Model)
	g.ModelScale = 2
	two, _ := blend()
	if r := colLen(two.Model) / base; math.Abs(r-2) > 1e-4 {
		t.Errorf("modelScale 2 scaled the model by %.3f, want 2", r)
	}
}

// TestModelBlendSpansHeldFrames: the imp's run holds each pose for two
// states (A A B B ...). The model must blend A -> B across BOTH A states
// as one span (GZDoom's InterpolateDoubledFrames), not freeze through the
// first and catch up in the second.
func TestModelBlendSpansHeldFrames(t *testing.T) {
	t.Chdir("..")
	zr, err := zip.OpenReader("assets/import/3D_Models.pk3")
	if err != nil {
		t.Skip("assets/import/3D_Models.pk3 not available")
	}
	defer zr.Close()
	ms, _ := models.Load(&zr.Reader)
	g := &Game{Models: ms}
	mo := &Mobj{}
	enter := func(st stateNum, ticsLeft int) {
		sd := &states[st]
		mo.State, mo.stateDef, mo.Sprite, mo.Frame = st, sd, sd.Sprite, sd.Frame
		mo.stateTics, mo.Tics = sd.Tics, ticsLeft
	}
	lerpAt := func(st stateNum, ticsLeft int) float32 {
		enter(st, ticsLeft)
		next, f := g.modelBlendTarget(mo)
		if next == nil {
			t.Fatalf("state %d: no blend target", st)
		}
		if want := ms.Parts("TROO", 1); next[0].Frame != want[0].Frame {
			t.Fatalf("state %d blends toward frame %d, want TROO B's %d", st, next[0].Frame, want[0].Frame)
		}
		return f
	}
	run := states[S_TROO_RUN1].Tics
	var got []float32
	for _, st := range []stateNum{S_TROO_RUN1, S_TROO_RUN2} {
		for left := run; left >= 1; left-- {
			got = append(got, lerpAt(st, left))
		}
	}
	for i, f := range got {
		want := float32(i) / float32(2*run)
		if math.Abs(float64(f-want)) > 1e-6 {
			t.Fatalf("tic %d of the A-A span: lerp %.3f, want %.3f (all %v)", i, f, want, got)
		}
	}
}

// TestFireballModelIsAdditive: the imp fireball's model is drawn with
// ZDoom's RenderStyle "Add"; the imp itself stays solid.
func TestFireballModelIsAdditive(t *testing.T) {
	t.Chdir("..")
	zr, err := zip.OpenReader("assets/import/3D_Models.pk3")
	if err != nil {
		t.Skip("assets/import/3D_Models.pk3 not available")
	}
	defer zr.Close()
	ms, _ := models.Load(&zr.Reader)
	g := &Game{Models: ms, ModelScale: 1}
	for _, c := range []struct {
		typ  mobjType
		st   stateNum
		want float32
	}{{MT_TROOPSHOT, S_TBALL1, 1}, {MT_TROOP, S_TROO_RUN1, 0}} {
		sd := &states[c.st]
		mo := &Mobj{Type: c.typ, State: c.st, stateDef: sd, Sprite: sd.Sprite, Frame: sd.Frame, Tics: sd.Tics, stateTics: sd.Tics}
		g.hwModels = g.hwModels[:0]
		if !g.appendMobjModel(mo, mobjSpriteName(mo), 0, 0, 0, 0) || len(g.hwModels) == 0 {
			t.Fatalf("type %d: no model", c.typ)
		}
		for _, inst := range g.hwModels {
			if inst.Additive != c.want {
				t.Errorf("type %d: Additive %v, want %v", c.typ, inst.Additive, c.want)
			}
		}
	}
}
