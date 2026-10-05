package wad

import (
	"reflect"
	"testing"
)

func TestTPF3DRoundTrip(t *testing.T) {
	in := TPF3DInfo{
		Format: TPF3DFormat, Tool: "wad3d 1", Source: "my mod.wad", SourceSHA1: "abc123",
		IWADPath: "/home/me/wad/Game WADs/doom2.wad", IWADSHA1: "def456", // a path with spaces survives
		Passes: []string{"stairs"}, Textures: "hires", TextureCount: 42, Maps: []string{"MAP01", "MAP02"},
	}
	got, err := ParseTPF3D(EncodeTPF3D(in))
	if err != nil {
		t.Fatal(err)
	}
	want := in
	want.Source = "my_mod.wad" // spaces can't survive a whitespace-split field
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip:\n got %+v\nwant %+v", got, want)
	}
}

func TestParseTPF3DTolerance(t *testing.T) {
	got, err := ParseTPF3D([]byte("# hi\nformat 2\nfuturekey whatever\nmaps E1M1\n"))
	if err != nil || got.Format != 2 || len(got.Maps) != 1 {
		t.Errorf("unknown keys should be skipped: %+v, %v", got, err)
	}
	if _, err := ParseTPF3D([]byte("maps E1M1\n")); err == nil {
		t.Error("a manifest without a format line should be rejected")
	}
	if _, err := ParseTPF3D([]byte("format zero\n")); err == nil {
		t.Error("a non-numeric format should be rejected")
	}
}
