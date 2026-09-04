package vfs

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// makeZip writes a pk3-shaped zip with the given path->contents and returns
// its path.
func makeZip(t *testing.T, name string, files map[string]string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestZipLookupAndNamespace(t *testing.T) {
	zp := makeZip(t, "pack.pk3", map[string]string{
		"GLDEFS":                 "pointlight A { color 1 1 1 size 10 }",
		"textures/STARTAN3.png":  "PNGDATA",
		"brightmaps/COMPUTE1.png": "MASK",
		"sprites/TROOA1.png":     "IMP",
		"readme.txt":             "hi",
	})
	fs := New()
	if err := fs.Mount(zp); err != nil {
		t.Fatal(err)
	}
	defer fs.Close()

	if b, ok := fs.Lump("gldefs"); !ok || !bytes.Contains(b, []byte("pointlight A")) {
		t.Fatalf("GLDEFS lookup failed: %q %v", b, ok)
	}
	// Graphics extension stripped for the key.
	if _, ok := fs.Lump("STARTAN3"); !ok {
		t.Error("textures/STARTAN3.png not found as lump STARTAN3")
	}
	// Namespace tagging.
	if _, ok := fs.InNamespace(NSTextures, "STARTAN3"); !ok {
		t.Error("STARTAN3 not tagged NSTextures")
	}
	if _, ok := fs.InNamespace(NSFlats, "STARTAN3"); ok {
		t.Error("STARTAN3 wrongly matched NSFlats")
	}
	if _, ok := fs.InNamespace(NSBrightmaps, "COMPUTE1"); !ok {
		t.Error("brightmaps/COMPUTE1.png not tagged NSBrightmaps")
	}
	// .txt is stripped too.
	if _, ok := fs.Lump("readme"); !ok {
		t.Error("readme.txt not found as lump README")
	}
	if got := fs.List(NSTextures); len(got) != 1 || got[0] != "textures/STARTAN3.png" {
		t.Errorf("List(NSTextures) = %v", got)
	}
}

func TestMountShadowingAndLumps(t *testing.T) {
	base := makeZip(t, "base.pk3", map[string]string{
		"GLDEFS": "// base\npointlight BASE { color 1 0 0 size 20 }",
	})
	over := makeZip(t, "over.pk3", map[string]string{
		"GLDEFS": "// override\npointlight OVER { color 0 1 0 size 30 }",
	})
	fs := New()
	fs.Mount(base)
	fs.Mount(over)
	defer fs.Close()

	// Lump: newest wins.
	if b, _ := fs.Lump("GLDEFS"); !bytes.Contains(b, []byte("override")) {
		t.Errorf("Lump should return the last-mounted GLDEFS, got %q", b)
	}
	// Lumps: every copy, oldest first.
	all := fs.Lumps("GLDEFS")
	if len(all) != 2 || !bytes.Contains(all[0], []byte("base")) || !bytes.Contains(all[1], []byte("override")) {
		t.Fatalf("Lumps order/content wrong: %q", all)
	}
}

func TestMountDir(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "textures"), 0o755)
	os.WriteFile(filepath.Join(dir, "GLDEFS"), []byte("pointlight D { color 1 1 1 size 5 }"), 0o644)
	os.WriteFile(filepath.Join(dir, "textures", "WALL01.png"), []byte("x"), 0o644)
	// A loose archive in the mod folder must be ignored by the dir walk.
	os.WriteFile(filepath.Join(dir, "nested.pk3"), []byte("PK\x03\x04junk"), 0o644)

	fs := New()
	if err := fs.Mount(dir); err != nil {
		t.Fatal(err)
	}
	if _, ok := fs.Lump("GLDEFS"); !ok {
		t.Error("dir mount: GLDEFS not found")
	}
	if _, ok := fs.InNamespace(NSTextures, "WALL01"); !ok {
		t.Error("dir mount: textures/WALL01.png not tagged NSTextures")
	}
	if _, ok := fs.Lump("nested"); ok {
		t.Error("dir mount indexed a nested .pk3 as a lump")
	}
}

func TestEmptyAndMissing(t *testing.T) {
	fs := New()
	if !fs.Empty() {
		t.Error("New() FS should be Empty")
	}
	if _, ok := fs.Lump("ANYTHING"); ok {
		t.Error("Lump on empty FS should miss")
	}
	if err := fs.Mount(filepath.Join(t.TempDir(), "nope.pk3")); err == nil {
		t.Error("Mount of a missing path should error")
	}
}

func TestLooseFileMount(t *testing.T) {
	p := filepath.Join(t.TempDir(), "MYLIGHTS.gldefs")
	os.WriteFile(p, []byte("pointlight L { color 1 1 1 size 1 }"), 0o644)
	fs := New()
	if err := fs.Mount(p); err != nil {
		t.Fatal(err)
	}
	// .gldefs is not a stripped extension, so the key keeps it.
	if _, ok := fs.Lump("MYLIGHTS.gldefs"); !ok {
		t.Errorf("loose file not mounted under its full name")
	}
}
