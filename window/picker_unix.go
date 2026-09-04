//go:build !windows && !darwin

package window

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// pickWAD opens a native "open file" dialog by shelling out to whichever
// desktop file-chooser helper is installed — zenity (GTK/GNOME), its forks
// qarma and matedialog, or kdialog (KDE). This keeps the engine off a
// compile-time GTK dependency (sqweek/dialog's Linux backend is cgo +
// libgtk-3-dev): the helper is an ordinary runtime program, present on
// essentially every desktop install. When none is found the error names
// the two ways round it — install zenity, or pass the WAD path as an
// argument or in $TPF_WAD (see PickWADFile).
func pickWAD() (string, error) {
	const title = "Select a Doom-engine WAD file"

	zenityArgs := []string{
		"--file-selection", "--title=" + title,
		"--file-filter=Doom WAD files | *.wad *.WAD",
		"--file-filter=All files | *",
	}
	for _, name := range []string{"zenity", "qarma", "matedialog"} {
		if bin, err := exec.LookPath(name); err == nil {
			return runFileChooser(bin, zenityArgs...)
		}
	}
	if bin, err := exec.LookPath("kdialog"); err == nil {
		return runFileChooser(bin,
			"--getopenfilename", ".", "*.wad *.WAD | Doom WAD files",
			"--title", title)
	}

	return "", fmt.Errorf("window: no graphical file chooser found — install zenity " +
		"(or kdialog), or name the WAD on the command line or in $TPF_WAD")
}

// pickMap shells out to a desktop list dialog — zenity/qarma/matedialog
// (--list) or kdialog (--menu) — showing every map in the WAD so the player
// can choose a start level without a console. Same helper discovery as
// pickWAD. Needs a running desktop session ($DISPLAY / $WAYLAND_DISPLAY);
// with neither, or no helper installed, it returns ErrPickMapUnavailable and
// the caller uses def.
func pickMap(maps []string, def string) (string, error) {
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return "", ErrPickMapUnavailable
	}
	title := "Choose a start level"
	text := fmt.Sprintf("This WAD has %d maps — pick one (Cancel starts %s):", len(maps), def)

	for _, name := range []string{"zenity", "qarma", "matedialog"} {
		bin, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		args := []string{"--list", "--title=" + title, "--text=" + text,
			"--column=Map", "--hide-header", "--height=460"}
		args = append(args, maps...)
		return runMapChooser(bin, def, args...)
	}
	if bin, err := exec.LookPath("kdialog"); err == nil {
		args := []string{"--title", title, "--menu", text}
		for _, m := range maps { // kdialog --menu wants tag/item pairs
			args = append(args, m, m)
		}
		return runMapChooser(bin, def, args...)
	}
	return "", ErrPickMapUnavailable
}

// runMapChooser runs a list-dialog helper and validates its answer against
// maps. Exit 1 (cancel/close) -> def, nil. Anything the helper prints that
// isn't one of the maps -> def as well (defensive).
func runMapChooser(bin, def string, args ...string) (string, error) {
	out, err := exec.Command(bin, args...).Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 {
			return def, nil // cancelled
		}
		return "", fmt.Errorf("window: map dialog (%s): %w", bin, err)
	}
	return strings.TrimRight(string(out), "\r\n"), nil
}

// runFileChooser runs one of the dialog helpers and interprets its result.
// They share a convention: the chosen path on stdout (one line), exit 0 on
// choose, exit 1 on cancel/close, any other exit a real error.
func runFileChooser(bin string, args ...string) (string, error) {
	out, err := exec.Command(bin, args...).Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 {
			return "", ErrPickCancelled
		}
		return "", fmt.Errorf("window: file dialog (%s): %w", bin, err)
	}
	// zenity multi-select joins paths with '|'; we never pass that flag,
	// but take the first field defensively. Trim the trailing newline.
	path := strings.TrimRight(string(out), "\r\n")
	if i := strings.IndexByte(path, '|'); i >= 0 {
		path = path[:i]
	}
	if path == "" {
		return "", ErrPickCancelled
	}
	return path, nil
}
