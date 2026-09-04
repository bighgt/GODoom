package assets

import (
	"archive/zip"
	"bytes"
	"image"
	"image/color"
	"image/png"
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
	if b := got["PISGA0"].Bounds(); b.Dx() != 114 || b.Dy() != 124 {
		t.Errorf("PISGA0 decoded %dx%d, want 114x124", b.Dx(), b.Dy())
	}
	if loadWeaponPackDir(filepath.Join(dir, "nope")) != nil {
		t.Error("absent dir should give nil")
	}
}

func TestWeaponOverrideRGBADensityAndHotspot(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 114, 124)) // 2x a 57-wide original

	// With the WAD patch: density is the exact ratio, hotspot scales by it.
	p := &wad.Patch{Width: 57, Height: 62, LeftOffset: -126, TopOffset: -106}
	out := weaponOverrideRGBA(img, p)
	if out.TexelsPerUnit != 2.0 {
		t.Errorf("TexelsPerUnit = %v, want 2.0", out.TexelsPerUnit)
	}
	if out.OffsetX != -252 || out.OffsetY != -212 {
		t.Errorf("hotspot = (%d,%d), want (-252,-212)", out.OffsetX, out.OffsetY)
	}
	if out.Width != 114 || out.Height != 124 {
		t.Errorf("decoded %dx%d, want 114x124", out.Width, out.Height)
	}

	// Without a WAD patch (a weapon this IWAD lacks): fallback density, a
	// centred-bottom hotspot.
	out = weaponOverrideRGBA(img, nil)
	if out.TexelsPerUnit != weaponPackTexelsPerUnit {
		t.Errorf("no-patch TexelsPerUnit = %v, want %v", out.TexelsPerUnit, weaponPackTexelsPerUnit)
	}
	if out.OffsetX != 57 || out.OffsetY != 124 {
		t.Errorf("no-patch hotspot = (%d,%d), want (57,124)", out.OffsetX, out.OffsetY)
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
	tex.weaponSprites = map[string]image.Image{
		"PISGA0": image.NewRGBA(image.Rect(0, 0, 114, 124)), // shareware PISGA0 is 57 wide
		"BFGGA0": image.NewRGBA(image.Rect(0, 0, 340, 168)), // not in shareware at all
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

	// A non-weapon sprite the pack has nothing for is still 1 texel/unit.
	if face, ok := tex.Sprite("STFB1"); ok && face.TexelsPerUnit != 1.0 {
		t.Errorf("STFB1 TexelsPerUnit = %v, want 1", face.TexelsPerUnit)
	}
}

func keys(m map[string]image.Image) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
