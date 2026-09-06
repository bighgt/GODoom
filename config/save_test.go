package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// A small fixture mirroring the shipped config.json's real shape: a leading
// "//" doc-comment line above most real keys, mixed types, and (crucially)
// one key — "flashlight" — deliberately left out entirely, to exercise the
// "append a missing key" path.
const fixtureConfigJSON = `{
  "//": "TwoPointFive config. JSON has no comments, so every option is documented by the",
  "//_": "\"//name\" line directly above it (those keys are ignored by the loader).",

  "//resolution": "540p | 600p | ... (default 720p)",
  "resolution": "1440p",
  "//windowed": "true | false (default false)",
  "windowed": false,
  "//vsync": "true | false (default true)",
  "vsync": false,
  "//fovDegrees": "70 .. 130 (default 100)",
  "fovDegrees": 95,
  "//mouseSensitivity": "radians per pixel (default 0.0022)",
  "mouseSensitivity": 0.0022,
  "//fogColor": "hex RRGGBB, empty disables (default \"\")",
  "fogColor": "",
  "//crosshair": "off | dot | cross (default dot)",
  "crosshair": "dot"
}
`

func writeFixture(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(fixtureConfigJSON), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func TestSaveMergePreservesCommentsAndUpdatesValues(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir)
	before, _ := os.ReadFile(path)

	cfg := Default()
	cfg.Resolution = "1080p"
	cfg.Windowed = true
	cfg.VSync = true
	cfg.FOVDegrees = 110
	cfg.MouseSensitivity = 0.005
	cfg.FogColor = "112233"
	cfg.Crosshair = "cross"

	if err := SaveMerge(cfg, path); err != nil {
		t.Fatalf("SaveMerge: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}

	// Every "//"-comment line must survive byte-for-byte.
	for _, line := range strings.Split(string(before), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, `"//`) {
			if !strings.Contains(string(after), line) {
				t.Errorf("comment line lost or altered: %q", line)
			}
		}
	}

	// The file must still be valid JSON that round-trips through Load-style
	// unmarshalling into the values we set.
	var got Config
	if err := json.Unmarshal(after, &got); err != nil {
		t.Fatalf("result is not valid JSON: %v\n%s", err, after)
	}
	if got.Resolution != "1080p" || !got.Windowed || !got.VSync || got.FOVDegrees != 110 ||
		got.MouseSensitivity != 0.005 || got.FogColor != "112233" || got.Crosshair != "cross" {
		t.Errorf("values didn't take: %+v", got)
	}

	// A value NOT touched by this SaveMerge call (all our test set the same
	// ones) should still read from repaired() defaults for anything absent —
	// but more importantly, indentation/format should look untouched. Spot
	// check one updated line's shape.
	if !strings.Contains(string(after), `"resolution": "1080p",`) {
		t.Errorf("resolution line not in the expected shape:\n%s", after)
	}
}

func TestSaveMergeAppendsMissingKey(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir) // fixture has no "flashlight" key at all

	cfg := Default()
	cfg.Flashlight = false // non-default, so it's unambiguous this came from cfg
	if err := SaveMerge(cfg, path); err != nil {
		t.Fatalf("SaveMerge: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	var got Config
	if err := json.Unmarshal(after, &got); err != nil {
		t.Fatalf("result is not valid JSON: %v\n%s", err, after)
	}
	if got.Flashlight {
		t.Errorf("appended flashlight = true, want false (from cfg)")
	}
	if !strings.Contains(string(after), `"flashlight": false`) {
		t.Errorf("flashlight key not appended:\n%s", after)
	}
}

func TestSaveMergeNoExistingFileWritesFullConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	cfg := Default()
	cfg.Skill = 5
	if err := SaveMerge(cfg, path); err != nil {
		t.Fatalf("SaveMerge: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	var got Config
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if got.Skill != 5 {
		t.Errorf("skill = %d, want 5", got.Skill)
	}
	// Fresh-write path should carry every field, like DefaultJSON.
	ct := reflect.TypeOf(Config{})
	var m map[string]json.RawMessage
	json.Unmarshal(data, &m)
	if len(m) != ct.NumField() {
		t.Errorf("fresh file has %d keys, Config has %d fields", len(m), ct.NumField())
	}
}
