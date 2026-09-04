package main

import (
	"testing"

	"twopointfive/window"
)

// Never let a test pop a real GUI map dialog (it would hang CI on a box
// with $DISPLAY + zenity). chooseStartMap only reaches mapPickerDialog when
// stdin is not a tty and has no queued input — exactly the `go test` case.
func init() {
	mapPickerDialog = func(maps []string, def string) (string, error) {
		return "", window.ErrPickMapUnavailable
	}
}

func TestPickStartMap(t *testing.T) {
	if got := pickStartMap([]string{"MAP03", "E1M1", "MAP01"}); got != "E1M1" {
		t.Errorf("prefers E1M1, got %q", got)
	}
	if got := pickStartMap([]string{"MAP07", "MAP01", "MAP12"}); got != "MAP01" {
		t.Errorf("prefers MAP01, got %q", got)
	}
	if got := pickStartMap([]string{"LEVELA", "LEVELB"}); got != "LEVELA" {
		t.Errorf("falls back to first, got %q", got)
	}
}

func TestResolveMapChoice(t *testing.T) {
	maps := []string{"E1M1", "E1M2", "E1M3", "E1M9"}
	def := "E1M1"

	cases := []struct {
		in, want, wantMsg string
	}{
		{"", "E1M1", ""},
		{"  \t ", "E1M1", ""},
		{"1", "E1M1", ""},
		{"3", "E1M3", ""},
		{"4", "E1M9", ""},       // index, not name
		{"e1m2", "E1M2", ""},    // case-insensitive name
		{"  E1M9 ", "E1M9", ""}, // trimmed name
		{"0", "", "enter 1..4"},
		{"5", "", "enter 1..4"},
		{"E1M4", "", `"E1M4" is not one of the maps`},
		{"exit", "", `"exit" is not one of the maps`},
	}
	for _, c := range cases {
		got, msg := resolveMapChoice(c.in, maps, def)
		if got != c.want || (c.wantMsg == "") != (msg == "") {
			t.Errorf("resolveMapChoice(%q) = (%q, %q), want (%q, msg?%v)",
				c.in, got, msg, c.want, c.wantMsg != "")
		}
		if c.wantMsg != "" && msg != c.wantMsg {
			t.Errorf("resolveMapChoice(%q) msg = %q, want %q", c.in, msg, c.wantMsg)
		}
	}
}

func TestChooseStartMapConfigOverride(t *testing.T) {
	maps := []string{"MAP01", "MAP02", "MAP07"}
	if got := chooseStartMap(maps, "map07"); got != "MAP07" {
		t.Errorf("config map=map07 -> %q, want MAP07", got)
	}
	// A single-map WAD never prompts.
	if got := chooseStartMap([]string{"E1M1"}, ""); got != "E1M1" {
		t.Errorf("single map -> %q, want E1M1", got)
	}
	// No config map, not a tty, nothing on stdin, no GUI dialog (the init()
	// stub returns ErrPickMapUnavailable) -> conventional start.
	if got := chooseStartMap(maps, "NOPE"); got != "MAP01" {
		t.Errorf("unknown config map -> %q, want MAP01 fallback", got)
	}
}

func TestChooseStartMapUsesGUIDialog(t *testing.T) {
	prev := mapPickerDialog
	defer func() { mapPickerDialog = prev }()
	mapPickerDialog = func(maps []string, def string) (string, error) { return "MAP07", nil }

	// Not a tty, nothing queued on stdin -> the GUI list dialog's choice wins.
	if got := chooseStartMap([]string{"MAP01", "MAP02", "MAP07"}, ""); got != "MAP07" {
		t.Errorf("GUI dialog picked MAP07, chooseStartMap -> %q", got)
	}
	// A cancel (dialog returns def) is honoured too.
	mapPickerDialog = func(maps []string, def string) (string, error) { return def, nil }
	if got := chooseStartMap([]string{"MAP01", "MAP02"}, ""); got != "MAP01" {
		t.Errorf("GUI dialog cancelled -> %q, want the default MAP01", got)
	}
}
