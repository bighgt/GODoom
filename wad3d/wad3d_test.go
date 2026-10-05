package wad3d

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"twopointfive/assets"
	"twopointfive/bsp"
	"twopointfive/engine/slopes"
	"twopointfive/wad"
)

// stairLevel builds a straight corridor along +X, 128 wide: a lower landing
// (sector 0, x 0..64, floor 0), n steps 32 deep rising 16 each, then an
// upper landing 64 deep. Ceilings are all 256.
func stairLevel(n int) *wad.Level {
	lvl := &wad.Level{Name: "MAP01"}
	xs := []int16{0, 64}
	for i := 1; i <= n; i++ {
		xs = append(xs, 64+int16(32*i))
	}
	xs = append(xs, xs[len(xs)-1]+64)
	nsec := len(xs) - 1
	for k := 0; k < nsec; k++ {
		lvl.Sectors = append(lvl.Sectors, wad.Sector{
			FloorHeight: int16(16 * k), CeilingHeight: 256,
			FloorTexture: "FLOOR4_8", CeilingTexture: "CEIL3_5", LightLevel: 160,
		})
	}
	for _, x := range xs {
		lvl.Vertexes = append(lvl.Vertexes, wad.Vertex{X: x, Y: 0}, wad.Vertex{X: x, Y: 128})
	}
	bot := func(i int) uint16 { return uint16(2 * i) }
	top := func(i int) uint16 { return uint16(2*i + 1) }
	side := func(sec int) uint16 {
		lvl.Sidedefs = append(lvl.Sidedefs, wad.Sidedef{MiddleTexture: "STARTAN3", UpperTexture: "-", LowerTexture: "STEP1", Sector: uint16(sec)})
		return uint16(len(lvl.Sidedefs) - 1)
	}
	line := func(a, b uint16, front, back int) {
		ld := wad.Linedef{StartVertex: a, EndVertex: b, FrontSidedef: side(front), BackSidedef: wad.NoSidedef}
		if back >= 0 {
			ld.BackSidedef = side(back)
			ld.Flags = 4 // two-sided
		}
		lvl.Linedefs = append(lvl.Linedefs, ld)
	}
	for k := 0; k < nsec; k++ {
		line(bot(k+1), bot(k), k, -1) // y=0 wall, facing +Y
		line(top(k), top(k+1), k, -1) // y=128 wall, facing -Y
		if k+1 < nsec {
			line(bot(k+1), top(k+1), k+1, k) // riser: front = the higher sector
		}
	}
	line(bot(0), top(0), 0, -1)            // west end
	line(top(nsec), bot(nsec), nsec-1, -1) // east end
	return lvl
}

// planeZ evaluates the plane through a def's three points at (x, y).
func planeZ(d slopes.Def, x, y float64) float64 {
	p1, p2, p3 := d.P1, d.P2, d.P3
	ux, uy, uz := p2[0]-p1[0], p2[1]-p1[1], p2[2]-p1[2]
	vx, vy, vz := p3[0]-p1[0], p3[1]-p1[1], p3[2]-p1[2]
	nx, ny, nz := uy*vz-uz*vy, uz*vx-ux*vz, ux*vy-uy*vx
	return p1[2] - (nx*(x-p1[0])+ny*(y-p1[1]))/nz
}

func TestStairRunBecomesOneRamp(t *testing.T) {
	lvl := stairLevel(5)
	runs := findStairRuns(buildTopology(lvl), nil, StairOptions{})
	if len(runs) != 1 {
		t.Fatalf("got %d runs, want 1", len(runs))
	}
	r := runs[0]
	if r.lower != 0 || r.upper != 6 || len(r.steps) != 5 {
		t.Fatalf("run lower=%d upper=%d steps=%v, want 0 / 6 / [1..5]", r.lower, r.upper, r.steps)
	}
	defs := stairDefs(runs)
	for _, d := range defs[1:] {
		if d.P1 != defs[0].P1 || d.P2 != defs[0].P2 || d.P3 != defs[0].P3 {
			t.Fatalf("step %d got a different plane — adjacent steps would seam", d.SectorIndex)
		}
	}
	// The ramp meets the landings exactly: z 0 at the bottom riser (x=64),
	// z 96 at the top riser (x=224), at both corridor walls.
	for _, y := range []float64{0, 128} {
		if z := planeZ(defs[0], 64, y); z != 0 {
			t.Errorf("bottom riser y=%v: z=%v, want 0", y, z)
		}
		if z := planeZ(defs[0], 224, y); z != 96 {
			t.Errorf("top riser y=%v: z=%v, want 96", y, z)
		}
	}
	if defs[0].P1[2] != float64(int16(defs[0].P1[2])) {
		t.Errorf("pivot height %v is not whole — the engine rounds it", defs[0].P1[2])
	}
}

func TestStairPassSkipsMovers(t *testing.T) {
	cases := map[string]func(*wad.Level){
		"tagged step":   func(l *wad.Level) { l.Sectors[3].Tag = 7 },
		"special step":  func(l *wad.Level) { l.Sectors[3].SpecialType = 9 },
		"manual door":   func(l *wad.Level) { markManual(l, 3) },
		"too steep":     func(l *wad.Level) { steepen(l) },
		"low headroom":  func(l *wad.Level) { l.Sectors[4].CeilingHeight = 80 },
		"already slope": nil,
	}
	for name, mutate := range cases {
		lvl := stairLevel(5)
		var exclude map[int]bool
		if mutate != nil {
			mutate(lvl)
		} else {
			exclude = map[int]bool{3: true}
		}
		if runs := findStairRuns(buildTopology(lvl), exclude, StairOptions{}); len(runs) != 0 {
			t.Errorf("%s: got %d run(s), want none (steps %v)", name, len(runs), runs[0].steps)
		}
	}
}

// markManual gives the riser line in front of sector s a manual-door
// special, so s is that door's back sector.
func markManual(l *wad.Level, s int) {
	for i, ld := range l.Linedefs {
		if ld.BackSidedef != wad.NoSidedef && int(l.Sidedefs[ld.BackSidedef].Sector) == s {
			l.Linedefs[i].SpecialType = 1
			return
		}
	}
}

// steepen makes every rise 24 over a 16-unit tread (150%).
func steepen(l *wad.Level) {
	for i := range l.Sectors {
		l.Sectors[i].FloorHeight = int16(24 * i)
	}
	for i := range l.Vertexes {
		if x := l.Vertexes[i].X; x > 64 {
			l.Vertexes[i].X = 64 + (x-64)/2
		}
	}
}

// TestConvertIWADs runs the full conversion over every IWAD on hand and
// reloads the output exactly as the engine would: the marker parses, every
// map still loads and builds its BSP, the generated SLOPES parses cleanly
// with one plane per ramp sector, and no original lump is shadowed.
func TestConvertIWADs(t *testing.T) {
	paths := []string{"../testdata/DOOM1.WAD", "../wad/Game WADs/doom.wad", "../wad/Game WADs/doom2.wad", "../wad/Game WADs/heretic.wad"}
	ran := 0
	for _, path := range paths {
		iw, err := wad.Load(path)
		if err != nil {
			continue
		}
		ran++
		t.Run(filepath.Base(path), func(t *testing.T) {
			lumps, rep, err := Convert(Source{Name: filepath.Base(path), SHA1: "x", Picked: iw, Merged: iw}, Options{NoVoxels: true})
			if err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(t.TempDir(), "out.wad")
			if err := wad.WritePWAD(out, lumps); err != nil {
				t.Fatal(err)
			}
			pw, err := wad.Load(out)
			if err != nil {
				t.Fatal(err)
			}
			merged := wad.Merge(iw, pw)

			info, ok := merged.TPF3D()
			if !ok || info.Format != wad.TPF3DFormat || len(info.Maps) != len(iw.ListMaps()) {
				t.Fatalf("TPF3D manifest = %+v, ok=%v", info, ok)
			}
			ramps := 0
			for _, m := range rep.Maps {
				ramps += m.RampSectors
				lvl, err := merged.LoadLevel(m.Name)
				if err != nil {
					t.Fatalf("%s: %v", m.Name, err)
				}
				if _, err := bsp.Build(lvl); err != nil {
					t.Fatalf("%s: bsp: %v", m.Name, err)
				}
			}
			raw, _ := merged.Find("SLOPES")
			planes, _, errs := slopes.Parse(raw)
			if len(errs) > 0 {
				t.Fatalf("SLOPES errors: %v", errs)
			}
			got := 0
			for _, ds := range planes {
				got += len(ds)
			}
			if got != ramps || ramps == 0 {
				t.Errorf("SLOPES has %d planes, report says %d ramp sectors", got, ramps)
			}
			t.Logf("%d maps, %d ramp sectors", len(rep.Maps), ramps)
		})
	}
	if ran == 0 {
		t.Skip("no IWADs available")
	}
}

// TestEmbeddedTexturesLoad packs E1M1's textures (downscaled, to keep the
// test quick) and checks the engine's texture loader serves them from the
// WAD — at the density the PNG's own width implies — while the IWAD's
// original flat lumps stay reachable by name.
func TestEmbeddedTexturesLoad(t *testing.T) {
	iw, err := wad.Load("../testdata/DOOM1.WAD")
	if err != nil {
		t.Skip("DOOM1.WAD not available")
	}
	t.Chdir("..") // the hi-res set is found relative to the repo root
	if w, _ := assets.HiresOverrideFiles(); len(w) == 0 {
		t.Skip("no hi-res texture set under assets/textures/")
	}
	lumps, rep, err := Convert(Source{Name: "DOOM1.WAD", Picked: iw, Merged: iw},
		Options{Maps: []string{"E1M1"}, Textures: true, MaxTex: 64, NoVoxels: true})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Walls == 0 || rep.Flats == 0 {
		t.Fatalf("nothing embedded: %+v", rep)
	}
	// NUKAGE1 is used on E1M1; its animation partners must come along.
	var hires []byte
	for _, l := range lumps {
		if l.Name == wad.TPFHiresLump {
			hires = l.Data
		}
	}
	for _, want := range []string{"flats/NUKAGE1.png", "flats/NUKAGE2.png", "flats/NUKAGE3.png"} {
		if !bytes.Contains(hires, []byte(want)) {
			t.Errorf("container is missing %s (animation family)", want)
		}
	}

	out := filepath.Join(t.TempDir(), "out.wad")
	if err := wad.WritePWAD(out, lumps); err != nil {
		t.Fatal(err)
	}
	pw, _ := wad.Load(out)
	merged := wad.Merge(iw, pw)

	orig, _ := iw.Find("NUKAGE1")
	if got, _ := merged.Find("NUKAGE1"); !bytes.Equal(got, orig) {
		t.Error("the IWAD's NUKAGE1 flat lump is shadowed after the merge")
	}

	// Move the disk set out of reach so only the WAD can supply textures.
	t.Chdir(t.TempDir())
	if w, _ := assets.HiresOverrideFiles(); len(w) != 0 {
		exe, _ := os.Executable()
		t.Skipf("disk hi-res set still reachable from %s — can't isolate the embedded path", exe)
	}
	tex, err := assets.New(merged)
	if err != nil {
		t.Fatal(err)
	}
	if ew, ef := tex.EmbeddedCount(); ew != rep.Walls || ef != rep.Flats {
		t.Errorf("EmbeddedCount = %d/%d, want %d/%d", ew, ef, rep.Walls, rep.Flats)
	}
	flat, _ := tex.Flat("NUKAGE1")
	if flat.Width != 64 || flat.TexelsPerUnit != 1 {
		t.Errorf("NUKAGE1 = %dx%d at %v texels/unit, want the embedded 64px image at 1", flat.Width, flat.Height, flat.TexelsPerUnit)
	}
	wall, _ := tex.WallTexture("STARTAN3")
	if wall.Width != 64 || wall.TexelsPerUnit != 0.5 {
		t.Errorf("STARTAN3 = %dx%d at %v texels/unit, want 64px wide at 0.5 (128-unit texture)", wall.Width, wall.Height, wall.TexelsPerUnit)
	}
}

// weaponIWADs loads the two arsenal sources, skipping the test if either
// isn't on hand.
func weaponIWADs(t *testing.T) []WeaponIWAD {
	t.Helper()
	d2, err := wad.Load("../wad/Game WADs/doom2.wad")
	if err != nil {
		t.Skip("doom2.wad not available")
	}
	her, err := wad.Load("../wad/Game WADs/heretic.wad")
	if err != nil {
		t.Skip("heretic.wad not available")
	}
	return []WeaponIWAD{{Game: "doom", WAD: d2, Path: "doom2.wad"}, {Game: "heretic", WAD: her, Path: "heretic.wad"}}
}

// convertWithWeapons converts one map of src (an IWAD) with every arsenal
// embedded, writes it, and returns it merged over src as the engine would
// load it.
func convertWithWeapons(t *testing.T, src *wad.WAD, name, mapName string, ws []WeaponIWAD) (*wad.WAD, Report) {
	t.Helper()
	lumps, rep, err := Convert(Source{Name: name, Picked: src, Merged: src, Weapons: ws},
		Options{Maps: []string{mapName}, Weapons: true, NoVoxels: true})
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "out.wad")
	if err := wad.WritePWAD(out, lumps); err != nil {
		t.Fatal(err)
	}
	pw, err := wad.Load(out)
	if err != nil {
		t.Fatal(err)
	}
	return wad.Merge(src, pw), rep
}

// TestWeaponsEmbedded: a shareware Doom WAD converted with every arsenal
// resolves Doom II's and Heretic's weapon art with no other IWAD loaded —
// the Super Shotgun and plasma pickups shareware never shipped, Heretic's
// crossbow pickup and wand viewmodel — and a Heretic sprite keeps
// Heretic's own colours rather than being pushed through Doom's palette.
// And the reverse: a Heretic WAD gets Doom's.
func TestWeaponsEmbedded(t *testing.T) {
	ws := weaponIWADs(t)
	d1, err := wad.Load("../testdata/DOOM1.WAD")
	if err != nil {
		t.Skip("DOOM1.WAD not available")
	}
	t.Chdir("..") // the HD weapon pack (Doom viewmodels) is found relative to the repo root
	merged, rep := convertWithWeapons(t, d1, "DOOM1.WAD", "E1M1", ws)
	if len(rep.MissingWeapons) > 0 {
		t.Errorf("weapon assets no source had: %v", rep.MissingWeapons)
	}
	if info, _ := merged.TPF3D(); len(info.Weapons) != 2 || info.WeaponCount != rep.WeaponSprites+rep.WeaponSounds {
		t.Errorf("manifest weapons = %v / %d, want doom+heretic / %d", info.Weapons, info.WeaponCount, rep.WeaponSprites+rep.WeaponSounds)
	}
	hasHD := false
	if tex, err := assets.New(ws[0].WAD); err == nil {
		hasHD = len(tex.WeaponPackFrames()) > 0
	}
	hereticRef, err := assets.New(ws[1].WAD)
	if err != nil {
		t.Fatal(err)
	}
	crossbowRef, _, _ := hereticRef.SpriteFrame("WBOW", 0, 0)

	t.Chdir(t.TempDir()) // out of reach of every disk pack: only the WAD can supply art
	tex, err := assets.New(merged)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"SGN2", "PLAS", "BFUG", "WBOW", "WSKL", "AMG1", "FX01"} {
		if _, _, ok := tex.SpriteFrame(p, 0, 0); !ok {
			t.Errorf("sprite %s doesn't resolve in the converted shareware WAD", p)
		}
	}
	if _, ok := tex.Sprite("GWNDA0"); !ok {
		t.Error("Heretic's Elven Wand viewmodel GWNDA0 doesn't resolve")
	}
	if hasHD {
		if _, ok := tex.Sprite("FIS0A0"); !ok {
			t.Error("the HD pack's fist viewmodel FIS0A0 wasn't carried over")
		}
	}
	crossbow, _, _ := tex.SpriteFrame("WBOW", 0, 0)
	if crossbow == nil || crossbowRef == nil || !bytes.Equal(crossbow.Pix, crossbowRef.Pix) ||
		crossbow.OffsetX != crossbowRef.OffsetX || crossbow.OffsetY != crossbowRef.OffsetY {
		t.Error("the embedded crossbow pickup differs from Heretic's own decode (palette/offsets lost)")
	}

	// The reverse: Heretic gets Doom's guns.
	merged, _ = convertWithWeapons(t, ws[1].WAD, "heretic.wad", "E1M1", ws)
	tex, err = assets.New(merged)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"SHOT", "SGN2", "LAUN", "CLIP", "PUFF"} {
		if _, _, ok := tex.SpriteFrame(p, 0, 0); !ok {
			t.Errorf("sprite %s doesn't resolve in the converted Heretic WAD", p)
		}
	}
}
