package launcher

import (
	"strings"
	"testing"

	"twopointfive/wad"
)

// TestWrapStatus: the bottom bar's status text wraps at word boundaries to
// the width left of the QUIT button, caps at statusMaxLines (the last line
// ending "..."), hard-breaks a word too long for a line, and swaps the
// em dash the launcher font can't draw for a hyphen.
func TestWrapStatus(t *testing.T) {
	if got := wrapStatus("short message", 40); len(got) != 1 || got[0] != "short message" {
		t.Errorf("short: %q", got)
	}
	long := strings.Repeat("word ", 60)
	got := wrapStatus(long, 20)
	if len(got) != statusMaxLines || !strings.HasSuffix(got[len(got)-1], "...") {
		t.Errorf("long: %d lines %q, want %d ending in ...", len(got), got, statusMaxLines)
	}
	for _, l := range got {
		if len([]rune(l)) > 20 {
			t.Errorf("line %q is wider than 20", l)
		}
	}
	if got := wrapStatus("aaaaaaaaaaaaaaaaaaaaaaaaa", 10); got[0] != "aaaaaaaaaa" {
		t.Errorf("long word not hard-broken: %q", got)
	}
	if got := wrapStatus("a — b", 40); got[0] != "a - b" {
		t.Errorf("em dash not replaced: %q", got)
	}
	// The launcher's "not a 3D WAD" message fits one line of the bar.
	width := (windowW - 260 - 16 - 16 - 16) / glyphAdvance
	msg := (&wad.NotPlayableError{Name: "some_long_pwad_name.wad"}).Short()
	if lines := wrapStatus(msg, width); len(lines) != 1 {
		t.Errorf("not-3D message wraps to %d lines: %q", len(lines), lines)
	}
}
