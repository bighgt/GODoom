package assets

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func writeGrayPNG(t *testing.T, path string, w, h int, fill func(x, y int) uint8) {
	t.Helper()
	g := image.NewGray(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			g.Pix[y*w+x] = fill(x, y)
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, g); err != nil {
		t.Fatal(err)
	}
}

func TestLoadBrightmapMaskResizes(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "m.png")
	// 4x4 source: left half white, right half black.
	writeGrayPNG(t, p, 4, 4, func(x, y int) uint8 {
		if x < 2 {
			return 255
		}
		return 0
	})

	m := loadBrightmapMask(p, 16, 8) // resample up to the "base" size
	if m == nil {
		t.Fatal("mask decoded to nil")
	}
	if m.Width != 16 || m.Height != 8 {
		t.Fatalf("mask not resized: %dx%d", m.Width, m.Height)
	}
	// A left-side texel is bright, a right-side one dark; luma is replicated
	// into every channel.
	left := m.Pix[(4*16+2)*4]
	right := m.Pix[(4*16+13)*4]
	if left < 200 || right > 40 {
		t.Errorf("resample lost the half/half split: left=%d right=%d", left, right)
	}
	if m.Pix[(4*16+2)*4] != m.Pix[(4*16+2)*4+1] {
		t.Error("luma not replicated across channels")
	}
}

func TestLoadBrightmapMaskAllBlackIsNil(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "black.png")
	writeGrayPNG(t, p, 8, 8, func(x, y int) uint8 { return 0 })
	if m := loadBrightmapMask(p, 8, 8); m != nil {
		t.Error("an all-black mask should decode to nil (no contribution)")
	}
}

func TestBrightmapNoneWhenUnindexed(t *testing.T) {
	tex := &Textures{brightmaps: map[string]string{}}
	base := &RGBA{Width: 4, Height: 4, Pix: make([]byte, 4*4*4)}
	if m, ok := tex.Brightmap("COMPUTE1", base); ok || m != nil {
		t.Error("Brightmap should miss when nothing is indexed")
	}
	// nil base -> miss, no panic.
	if _, ok := tex.Brightmap("COMPUTE1", nil); ok {
		t.Error("Brightmap(nil base) should miss")
	}
	// The negative result is cached.
	if _, seen := tex.brightCache["COMPUTE1"]; !seen {
		t.Error("negative Brightmap lookup was not cached")
	}
}
