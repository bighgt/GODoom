package window

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// ErrPickCancelled is returned by PickWADFile when the user closes or
// cancels the file dialog instead of choosing a file.
var ErrPickCancelled = errors.New("window: WAD file selection cancelled")

// ErrPickMapUnavailable is returned by PickMap when there is no graphical
// list dialog on this platform / desktop — the caller should fall back to
// the conventional start map.
var ErrPickMapUnavailable = errors.New("window: no graphical map picker available")

// StdinInteractive reports whether standard input is a real interactive
// terminal (so cmd/engine can show its console map menu) rather than a
// pipe, a file, /dev/null or closed (so it should pop the GUI list). Unlike
// a plain character-device check this rejects /dev/null, which is what a
// desktop launcher / IDE usually hands the process as stdin.
func StdinInteractive() bool { return stdinInteractive() }

// PickMap pops a native list dialog of the WAD's maps and returns the one
// the user chose. It is the graphical counterpart to cmd/engine's terminal
// menu, for when the engine was launched with no console (double-clicked,
// from a desktop launcher or an IDE). `def` is the map that would load
// without a choice — shown in the prompt and returned on cancel. Returns
// ErrPickMapUnavailable when the platform has no list dialog (the caller
// then uses def).
func PickMap(maps []string, def string) (string, error) {
	if len(maps) == 0 {
		return def, nil
	}
	return pickMap(maps, def)
}

// PickWADFile resolves the WAD to load. An explicitly-supplied path — the
// TPF_WAD environment variable, or a *.wad argument on the command line —
// wins and skips any dialog; that's what keeps the engine usable headless,
// from a script, or on a box with no desktop file chooser. Otherwise it
// opens the platform's native "open file" dialog (pickWAD, which is
// OS-specific: a Win32/Cocoa call on Windows and macOS, a zenity/kdialog
// helper on other Unixes).
func PickWADFile() (string, error) {
	if p := wadFromCommandLine(); p != "" {
		return p, nil
	}
	return pickWAD()
}

// wadFromCommandLine looks for an explicitly-supplied WAD path: TPF_WAD
// first, then the first command-line argument naming an existing file that
// ends in .wad (case-insensitive). Returns "" when neither is present. A
// TPF_WAD that points nowhere is still returned — letting the WAD loader
// report the bad path beats silently falling back to a dialog.
func wadFromCommandLine() string {
	if p := strings.TrimSpace(os.Getenv("TPF_WAD")); p != "" {
		return p
	}
	for _, arg := range os.Args[1:] {
		if !strings.EqualFold(filepath.Ext(arg), ".wad") {
			continue
		}
		if fi, err := os.Stat(arg); err == nil && !fi.IsDir() {
			return arg
		}
	}
	return ""
}
