package assets

import (
	"archive/zip"
	"bytes"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"testing"

	"twopointfive/wad"
)

// makeWeaponPk3 writes a minimal .pk3 (zip): PNG-encoded frames for entries
// ending .png/.PNG, a stub byte for anything else, laid out the way a
// GZDoom HD weapon pack is.
func makeWeaponPk3(t *testing.T, path string, frames map[string][2]int) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, wh := range frames {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		lower := name
		if n := len(lower); n >= 4 && (lower[n-4:] == ".png" || lower[n-4:] == ".PNG") {
			img := image.NewRGBA(image.Rect(0, 0, wh[0], wh[1]))
			img.Set(0, 0, color.RGBA{9, 9, 9, 255})
			if err := png.Encode(w, img); err != nil {
				t.Fatal(err)
			}
		} else {
			w.Write([]byte("stub"))
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeZipEntry adds one plain-text entry (e.g. a TEXTURES lump) to an
// already-written pk3 by rebuilding it — simplest way to add a non-PNG
// entry alongside makeWeaponPk3's frames without duplicating its zip logic.
func addTextEntry(t *testing.T, pk3Path, name, contents string) {
	t.Helper()
	orig, err := os.ReadFile(pk3Path)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(orig), int64(len(orig)))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range zr.File {
		w, err := zw.Create(f.Name)
		if err != nil {
			t.Fatal(err)
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	w, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(contents)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pk3Path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadWeaponPackDir(t *testing.T) {
	dir := t.TempDir()
	makeWeaponPk3(t, filepath.Join(dir, "doom_weapons.pk3"), map[string][2]int{
		"hires/sprites/pistol/PISGA0.png":        {114, 124},
		"hires/sprites/shotgun/SHTGA0.PNG":       {158, 120},
		"hires/sprites/super shotgun/SHT2C0.png": {242, 260},
		"hires/sprites/notes.txt":                {0, 0}, // ignored: not a PNG
	})
	if err := os.WriteFile(filepath.Join(dir, "readme.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err) // a stray non-pack file must be ignored
	}

	got := loadWeaponPackDir(dir)
	if len(got) != 3 {
		t.Fatalf("got %d frames, want 3: %v", len(got), keys(got))
	}
	for _, n := range []string{"PISGA0", "SHTGA0", "SHT2C0"} {
		if _, ok := got[n]; !ok {
			t.Errorf("missing frame %q", n)
		}
	}
	if b := got["PISGA0"].img.Bounds(); b.Dx() != 114 || b.Dy() != 124 {
		t.Errorf("PISGA0 decoded %dx%d, want 114x124", b.Dx(), b.Dy())
	}
	if got["PISGA0"].scale != 0 {
		t.Errorf("PISGA0 scale = %v, want 0 (no TEXTURES lump in this pack)", got["PISGA0"].scale)
	}
	if loadWeaponPackDir(filepath.Join(dir, "nope")) != nil {
		t.Error("absent dir should give nil")
	}
}

// TestLoadWeaponPackDirReadsTexturesScale: a pack's own TEXTURES lump
// (SLADE3's own output format for an HD sprite-replacement pack) declares
// each frame's actual authored density — regression for weapons rendering
// ~2-4x too large when every frame fell back to the flat
// weaponPackTexelsPerUnit guess instead.
func TestLoadWeaponPackDirReadsTexturesScale(t *testing.T) {
	dir := t.TempDir()
	pk3 := filepath.Join(dir, "hd.pk3")
	makeWeaponPk3(t, pk3, map[string][2]int{
		"patches/pistol/PI0GA0.png": {700, 800},
		"patches/pistol/PI0GB0.png": {700, 800}, // no XScale for this one below — must fall back
	})
	addTextEntry(t, pk3, "TEXTURES", `
Sprite "PI0GA0", 700, 800
{
	XScale 7.300
	YScale 7.000
	Offset -300, -400
	Patch "PI0GA0", 0, 0
}
`)

	got := loadWeaponPackDir(dir)
	pga := got["PI0GA0"]
	if pga.scale != 7.15 { // (7.3+7.0)/2
		t.Errorf("PI0GA0 scale = %v, want 7.15 (average of X/Y)", pga.scale)
	}
	if !pga.hasOffset || pga.offX != -300 || pga.offY != -400 {
		t.Errorf("PI0GA0 offset = (%d,%d) hasOffset=%v, want (-300,-400) true", pga.offX, pga.offY, pga.hasOffset)
	}
	if pgb := got["PI0GB0"]; pgb.scale != 0 || pgb.hasOffset {
		t.Errorf("PI0GB0 = %+v, want scale 0 and no offset (no TEXTURES entry for this frame)", pgb)
	}
}

func TestWeaponOverrideRGBADensityAndHotspot(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 114, 124)) // 2x a 57-wide original

	// With the WAD patch and no authored scale/offset: density is the
	// exact original-width ratio, hotspot scales by it.
	p := &wad.Patch{Width: 57, Height: 62, LeftOffset: -126, TopOffset: -106}
	out := weaponOverrideRGBA(img, p, weaponFrame{})
	if out.TexelsPerUnit != 2.0 {
		t.Errorf("TexelsPerUnit = %v, want 2.0", out.TexelsPerUnit)
	}
	if out.OffsetX != -252 || out.OffsetY != -212 {
		t.Errorf("hotspot = (%d,%d), want (-252,-212)", out.OffsetX, out.OffsetY)
	}
	if out.Width != 114 || out.Height != 124 {
		t.Errorf("decoded %dx%d, want 114x124", out.Width, out.Height)
	}

	// Without a WAD patch (a weapon this IWAD lacks) or an authored
	// scale/offset: the flat fallback density, a centred-bottom hotspot.
	out = weaponOverrideRGBA(img, nil, weaponFrame{})
	if out.TexelsPerUnit != weaponPackTexelsPerUnit {
		t.Errorf("no-patch TexelsPerUnit = %v, want %v", out.TexelsPerUnit, weaponPackTexelsPerUnit)
	}
	if out.OffsetX != 57 || out.OffsetY != 124 {
		t.Errorf("no-patch hotspot = (%d,%d), want (57,124)", out.OffsetX, out.OffsetY)
	}

	// An authored scale (from the pack's own TEXTURES lump) wins over BOTH
	// the WAD-patch ratio and the flat fallback for DENSITY — this is the
	// "3x too large" fix: a hand-painted HD pack's frames don't all share
	// one clean multiple. With no authored OFFSET, the hotspot still comes
	// from the patch, just scaled by the new (authored) density.
	out = weaponOverrideRGBA(img, p, weaponFrame{scale: 9.0})
	if out.TexelsPerUnit != 9.0 {
		t.Errorf("authored-scale TexelsPerUnit = %v, want 9.0 (should win over the patch-ratio 2.0)", out.TexelsPerUnit)
	}
	if out.OffsetX != int(-126*9.0) || out.OffsetY != int(-106*9.0) {
		t.Errorf("authored-scale hotspot = (%d,%d), want (%d,%d)", out.OffsetX, out.OffsetY, int(-126*9.0), int(-106*9.0))
	}

	// An authored OFFSET (also from TEXTURES) wins over the WAD patch's own
	// offset entirely, and is used AS-IS (already in this PNG's own pixel
	// space, no further scaling) — this is the "flash doesn't line up with
	// the muzzle" fix: only the pack's own shared-origin hotspots actually
	// agree with each other across a weapon's gun/flash frames; a WAD
	// patch offset (when one even exists) and the centred-bottom guess
	// don't share that origin at all.
	out = weaponOverrideRGBA(img, p, weaponFrame{scale: 9.0, offX: -911, offY: -752, hasOffset: true})
	if out.OffsetX != -911 || out.OffsetY != -752 {
		t.Errorf("authored-offset hotspot = (%d,%d), want (-911,-752) unscaled", out.OffsetX, out.OffsetY)
	}
}

// TestContentBottomPad: the "rocket launcher gap" fix — measure how many
// fully-transparent rows sit below a frame's actual drawn content.
func TestContentBottomPad(t *testing.T) {
	// Tight to its own canvas (e.g. this project's "Smooth HD Weapons"
	// shotgun, confirmed by direct alpha-channel inspection to have zero
	// bottom padding): opaque pixels reach the very last row.
	tight := image.NewRGBA(image.Rect(0, 0, 10, 20))
	for x := 0; x < 10; x++ {
		tight.Set(x, 19, color.RGBA{1, 1, 1, 255})
	}
	if got := contentBottomPad(tight); got != 0 {
		t.Errorf("tight frame: got %d, want 0", got)
	}

	// Padded (this project's actual RLANA0 shape): opaque content stops
	// well short of the canvas's own bottom edge.
	padded := image.NewRGBA(image.Rect(0, 0, 10, 20))
	for x := 0; x < 10; x++ {
		padded.Set(x, 14, color.RGBA{1, 1, 1, 255}) // last opaque row: y=14
	}
	if got := contentBottomPad(padded); got != 5 { // rows 15..19 are empty
		t.Errorf("padded frame: got %d, want 5", got)
	}

	// Fully transparent: degenerate, but must not go negative or panic.
	blank := image.NewRGBA(image.Rect(0, 0, 10, 20))
	if got := contentBottomPad(blank); got != 20 {
		t.Errorf("blank frame: got %d, want 20 (full height)", got)
	}
}

// TestWeaponOverrideRGBAPropagatesBottomPad: weaponOverrideRGBA must carry
// contentBottomPad's measurement onto the returned RGBA untouched (no
// scaling — DrawWeapon does that itself, the same way it scales
// OffsetX/OffsetY), regardless of density/hotspot source.
func TestWeaponOverrideRGBAPropagatesBottomPad(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 10, 20))
	for x := 0; x < 10; x++ {
		img.Set(x, 14, color.RGBA{1, 1, 1, 255})
	}
	out := weaponOverrideRGBA(img, nil, weaponFrame{})
	if out.BottomPad != 5 {
		t.Errorf("BottomPad = %d, want 5", out.BottomPad)
	}
}

// End to end: a loaded pack overrides Sprite() for a frame the WAD defines
// (using the WAD hotspot, scaled) AND for one it doesn't (SSG/BFG in
// shareware), while non-weapon sprites are untouched.
func TestWeaponPackOverridesSprite(t *testing.T) {
	w, err := wad.Load("../testdata/DOOM1.WAD")
	if err != nil {
		t.Skipf("shareware WAD unavailable: %v", err)
	}
	tex, err := New(w)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Inject a pack so the test doesn't depend on assets/weapons/ resolving
	// from the test's working directory.
	tex.weaponSprites = map[string]weaponFrame{
		"PISGA0": {img: image.NewRGBA(image.Rect(0, 0, 114, 124))},        // shareware PISGA0 is 57 wide
		"BFGGA0": {img: image.NewRGBA(image.Rect(0, 0, 340, 168))},        // not in shareware at all
		"CHGGA0": {img: image.NewRGBA(image.Rect(0, 0, 700, 800)), scale: 7.15}, // authored scale, no WAD original needed
	}

	sp, ok := tex.Sprite("PISGA0")
	if !ok {
		t.Fatal("Sprite(PISGA0) not resolved")
	}
	if sp.Width != 114 || sp.TexelsPerUnit != 2.0 {
		t.Errorf("PISGA0 override: %d wide tpu=%v, want 114 / 2", sp.Width, sp.TexelsPerUnit)
	}
	if lw := float64(sp.Width) / sp.TexelsPerUnit; lw != 57 {
		t.Errorf("PISGA0 logical width %.0f, want the vanilla 57", lw)
	}
	if sp.OffsetX >= 0 {
		t.Errorf("PISGA0 hotspot X = %d, want the WAD's negative offset scaled", sp.OffsetX)
	}

	bfg, ok := tex.Sprite("BFGGA0")
	if !ok {
		t.Fatal("Sprite(BFGGA0) not resolved though a pack frame exists (no-WAD-patch path)")
	}
	if bfg.Width != 340 || bfg.TexelsPerUnit != weaponPackTexelsPerUnit {
		t.Errorf("BFGGA0 override: %d wide tpu=%v, want 340 / %v", bfg.Width, bfg.TexelsPerUnit, weaponPackTexelsPerUnit)
	}

	chg, ok := tex.Sprite("CHGGA0")
	if !ok {
		t.Fatal("Sprite(CHGGA0) not resolved")
	}
	if chg.TexelsPerUnit != 7.15 {
		t.Errorf("CHGGA0 TexelsPerUnit = %v, want the authored 7.15, not the flat fallback", chg.TexelsPerUnit)
	}

	// A non-weapon sprite the pack has nothing for is still 1 texel/unit.
	if face, ok := tex.Sprite("STFB1"); ok && face.TexelsPerUnit != 1.0 {
		t.Errorf("STFB1 TexelsPerUnit = %v, want 1", face.TexelsPerUnit)
	}
}

func keys(m map[string]weaponFrame) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
