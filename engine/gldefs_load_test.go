package engine

import (
	"archive/zip"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"twopointfive/assets/vfs"
	"twopointfive/engine/gldefs"
	"twopointfive/wad"
)

// buildWAD assembles a minimal in-memory PWAD from name->bytes lumps in the
// given order — enough to exercise loadGLDefs without a fixture file.
func buildWAD(t *testing.T, order []string, lumps map[string][]byte) *wad.WAD {
	t.Helper()
	const headerSize, dirEntrySize = 12, 16
	body := make([]byte, headerSize)
	type ent struct {
		pos, size int
		name      string
	}
	var dir []ent
	for _, name := range order {
		data := lumps[name]
		dir = append(dir, ent{pos: len(body), size: len(data), name: name})
		body = append(body, data...)
	}
	dirOfs := len(body)
	for _, e := range dir {
		rec := make([]byte, dirEntrySize)
		binary.LittleEndian.PutUint32(rec[0:4], uint32(e.pos))
		binary.LittleEndian.PutUint32(rec[4:8], uint32(e.size))
		copy(rec[8:16], e.name)
		body = append(body, rec...)
	}
	copy(body[0:4], "PWAD")
	binary.LittleEndian.PutUint32(body[4:8], uint32(len(dir)))
	binary.LittleEndian.PutUint32(body[8:12], uint32(dirOfs))

	w, err := wad.Parse(body)
	if err != nil {
		t.Fatalf("buildWAD: %v", err)
	}
	return w
}

func TestLoadGLDefsFromWAD(t *testing.T) {
	gl := []byte(`
pointlight TESTLAMP { color 0.2 0.4 1.0 size 128 }
object Foo { frame TREDA { light TESTLAMP } }
`)
	w := buildWAD(t, []string{"GLDEFS", "OTHER"}, map[string][]byte{
		"GLDEFS": gl,
		"OTHER":  []byte("junk"),
	})

	defs := loadGLDefs(w, nil)
	if defs == nil {
		t.Fatal("loadGLDefs returned nil for a WAD that has a GLDEFS lump")
	}
	if defs.NumLights() != 1 {
		t.Fatalf("want 1 light, got %d", defs.NumLights())
	}
	if got := defs.Frame("TRED", 0); len(got) != 1 || got[0].Name != "TESTLAMP" {
		t.Fatalf("attachment not indexed: %v", got)
	}
}

// A GLDEFS-only .pk3 mounted from assets/mods/ must light a stock IWAD that
// ships no GLDEFS lump of its own, and a mod GLDEFS layers after the WAD's.
func TestLoadGLDefsFromMod(t *testing.T) {
	zp := filepath.Join(t.TempDir(), "lights.pk3")
	zf, err := os.Create(zp)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(zf)
	w0, _ := zw.Create("GLDEFS")
	w0.Write([]byte(`
pointlight MODLAMP { color 1.0 0.0 1.0 size 200 }
object Barrel { frame BAR1A { light MODLAMP } }
`))
	zw.Close()
	zf.Close()

	mods := vfs.New()
	if err := mods.Mount(zp); err != nil {
		t.Fatal(err)
	}
	defer mods.Close()

	// No WAD GLDEFS at all — the mod alone provides it.
	defs := loadGLDefs(nil, mods)
	if defs == nil || defs.NumLights() != 1 {
		t.Fatalf("mod GLDEFS not loaded: %+v", defs)
	}
	if got := defs.Frame("BAR1", 0); len(got) != 1 || got[0].Name != "MODLAMP" {
		t.Fatalf("mod attachment not indexed: %v", got)
	}
}

func TestLoadGLDefsNoLump(t *testing.T) {
	w := buildWAD(t, []string{"THINGS"}, map[string][]byte{"THINGS": {1, 2, 3}})
	if defs := loadGLDefs(w, nil); defs != nil {
		t.Fatalf("expected nil for a WAD with no GLDEFS lump, got %+v", defs)
	}
	if loadGLDefs(nil, nil) != nil {
		t.Fatal("loadGLDefs(nil, nil) must be nil")
	}
}

// A GLDEFS attachment must override the built-in decoLights entry for the
// same decoration: the red torch (decoLights: warm red) becomes green here.
func TestGLDefsOverridesDecoTable(t *testing.T) {
	g := &Game{}
	defs, errs := gldefs.Parse([]byte(`
pointlight GREENGLOW { color 0.0 1.0 0.0 size 150 }
object RedTorch { frame TREDA { light GREENGLOW } }
`), nil)
	if len(errs) != 0 {
		t.Fatalf("parse: %v", errs)
	}
	g.gldefs = defs
	g.mobjs = []*Mobj{{
		Type: MT_REDTORCH, Sprite: SPR_TRED, Frame: 0,
		X: 100, Y: 200, Z: 0, Height: 68,
	}}

	g.buildStaticLights()

	if len(g.staticLights) != 1 {
		t.Fatalf("want 1 static light, got %d", len(g.staticLights))
	}
	sl := g.staticLights[0]
	if sl.r != 0 || sl.g != 1 || sl.b != 0 {
		t.Errorf("colour not from GLDEFS: r=%.2f g=%.2f b=%.2f", sl.r, sl.g, sl.b)
	}
	if sl.baseRadius != 150 {
		t.Errorf("radius not from GLDEFS: %.0f", sl.baseRadius)
	}
	if sl.sector != -1 {
		t.Errorf("GLDEFS decoration light should be a fixed emitter, sector=%d", sl.sector)
	}
	// Mid-height fallback with no vertical offset.
	if sl.z != g.mobjs[0].Z+g.mobjs[0].Height*0.5 {
		t.Errorf("z anchor: got %.1f", sl.z)
	}
}

// With no GLDEFS loaded, the built-in decoLights table still drives things.
func TestNoGLDefsFallsBackToDecoTable(t *testing.T) {
	g := &Game{}
	g.mobjs = []*Mobj{{
		Type: MT_REDTORCH, Sprite: SPR_TRED, Frame: 0,
		X: 0, Y: 0, Z: 0, Height: 68,
	}}
	g.buildStaticLights()
	if len(g.staticLights) != 1 {
		t.Fatalf("want 1 static light from decoLights, got %d", len(g.staticLights))
	}
	want := decoLights[MT_REDTORCH]
	if g.staticLights[0].baseRadius != want.radius {
		t.Errorf("fallback radius %.0f, want %.0f", g.staticLights[0].baseRadius, want.radius)
	}
}

// A GLDEFS light attached to a flying projectile's sprite frame overrides
// the built-in ExplodePrefix colour for the flight phase; the explosion
// flare still uses the hand-tuned ramp.
func TestGLDefsOverridesProjectileFlightLight(t *testing.T) {
	g := &Game{}
	defs, errs := gldefs.Parse([]byte(`
pointlight IMPFIRE { color 1.0 0.1 0.9 size 260 }
object DoomImpBall { frame BAL1A { light IMPFIRE } }
`), nil)
	if len(errs) != 0 {
		t.Fatalf("parse: %v", errs)
	}
	g.gldefs = defs

	def := &ProjectileDef{Sprite: "BAL1A0", ExplodePrefix: "MISL", ExplodeFrames: []byte{'A', 'B'}, ExplodeTics: 4}

	flying := g.projectileLight(&Projectile{def: def, X: 1, Y: 2, Z: 3})
	if !(flying.R > 0.9 && flying.B > 0.8 && flying.Radius == 260) {
		t.Errorf("flight light not from GLDEFS: %+v", flying)
	}
	// Exploding -> back to the hand-tuned ramp (white-hot orange core), not
	// the GLDEFS magenta.
	boom := g.projectileLight(&Projectile{def: def, exploding: true, explodeElapsed: 0})
	if boom.B > boom.R {
		t.Errorf("explosion light should stay warm (hand-tuned ramp), got %+v", boom)
	}
}
