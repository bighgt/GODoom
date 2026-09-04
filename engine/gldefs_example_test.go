package engine

import (
	"os"
	"path/filepath"
	"testing"

	"twopointfive/engine/gldefs"
)

// The shipped assets/mods/GLDEFS.example must stay parseable and keep its
// decoration attachments wired to real sprite names — it is the one-command
// way to see data-driven lights, so a typo in it is a silent dud.
func TestShippedExampleGLDefsParses(t *testing.T) {
	path := filepath.Join("..", "assets", "mods", "GLDEFS.example")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("example not present: %v", err)
	}
	defs, errs := gldefs.Parse(data, nil)
	if len(errs) != 0 {
		t.Fatalf("GLDEFS.example has parse errors: %v", errs)
	}
	if defs.NumLights() < 10 {
		t.Errorf("expected the full decoration light set, got %d lights", defs.NumLights())
	}
	// Spot-check a few attachments against the sprite names the engine uses.
	for _, tc := range []struct {
		sprite string
		want   string
	}{
		{"TRED", "TPF_REDTORCH"},
		{"FCAN", "TPF_BURNINGBARREL"},
		{"TLMP", "TPF_TECHLAMP_TALL"},
		{"CEYE", "TPF_EVILEYE"},
	} {
		got := defs.Frame(tc.sprite, 0)
		if len(got) != 1 || got[0].Name != tc.want {
			t.Errorf("sprite %s: got %v, want [%s]", tc.sprite, lightNames(got), tc.want)
		}
	}
}

func lightNames(ls []*gldefs.Light) []string {
	out := make([]string, len(ls))
	for i, l := range ls {
		out[i] = l.Name
	}
	return out
}
