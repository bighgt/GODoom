package assets

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func writePNG(t *testing.T, path string, w, h int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{1, 2, 3, 255})
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()
}

// loadHiresOverride derives TexelsPerUnit from pngWidth / originalWidth so
// a pack at any scale (not just tools/gentex's fixed 4x) samples right.
func TestLoadHiresOverrideScaleInference(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "STARTAN3.png")
	writePNG(t, p, 256, 256) // a 256px image...

	cases := []struct {
		origWidth float64
		wantTPU   float64
	}{
		{64, 4.0},               // ...standing in for a 64-unit-wide texture -> 4x
		{128, 2.0},              // ...or a 128-unit one -> 2x
		{256, 1.0},              // ...same size -> 1x (no upscale)
		{0, HiresTexelsPerUnit}, // unknown original -> assume gentex's 4x
		{1e6, 0.5},              // absurd -> clamped low
		{1, 16.0},               // absurd -> clamped high
	}
	for _, c := range cases {
		im := loadHiresOverride(p, c.origWidth)
		if im == nil {
			t.Fatalf("origWidth=%.0f: nil image", c.origWidth)
		}
		if im.TexelsPerUnit != c.wantTPU {
			t.Errorf("origWidth=%.0f: TexelsPerUnit=%v, want %v", c.origWidth, im.TexelsPerUnit, c.wantTPU)
		}
		if im.Width != 256 || im.Height != 256 {
			t.Errorf("origWidth=%.0f: decoded %dx%d, want 256x256", c.origWidth, im.Width, im.Height)
		}
	}

	if im := loadHiresOverride(filepath.Join(dir, "nope.png"), 64); im != nil {
		t.Error("missing file should return nil")
	}
}

// indexHiresDir walks subdirectories so a third-party pack's own layout
// can be dropped in wholesale, keyed by (upper-cased) file stem.
func TestIndexHiresDirRecursive(t *testing.T) {
	root := t.TempDir()
	writePNG(t, filepath.Join(root, "assets/textures/walls/STARTAN3.png"), 8, 8)
	writePNG(t, filepath.Join(root, "assets/textures/walls/nested/BROWN96.png"), 8, 8)
	writePNG(t, filepath.Join(root, "assets/textures/walls/deep/er/still/BIGDOOR2.png"), 8, 8)
	// non-PNG is ignored
	if err := os.WriteFile(filepath.Join(root, "assets/textures/walls/readme.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)

	idx := indexHiresWalls()
	for _, name := range []string{"STARTAN3", "BROWN96", "BIGDOOR2"} {
		if _, ok := idx[name]; !ok {
			t.Errorf("index missing %q; got %v", name, idx)
		}
	}
	if len(idx) != 3 {
		t.Errorf("index has %d entries, want 3: %v", len(idx), idx)
	}
}

func TestIndexHiresDirAbsentIsNil(t *testing.T) {
	t.Chdir(t.TempDir()) // no assets/ here
	if idx := indexHiresWalls(); idx != nil {
		t.Errorf("absent override dir -> %v, want nil", idx)
	}
}
