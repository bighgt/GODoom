//go:build darwin

package window

import (
	"fmt"
	"os/exec"
	"strings"
)

// pickMap shows the WAD's maps in a native "choose from list" dialog via
// osascript (AppleScript), which ships with every macOS. Cancel returns
// def; an osascript failure returns ErrPickMapUnavailable.
func pickMap(maps []string, def string) (string, error) {
	if _, err := exec.LookPath("osascript"); err != nil {
		return "", ErrPickMapUnavailable
	}

	quoted := make([]string, len(maps))
	for i, m := range maps {
		quoted[i] = `"` + sanitizeMapName(m) + `"`
	}
	script := fmt.Sprintf(
		`set c to choose from list {%s} with title "Choose a start level" `+
			`with prompt "This WAD has %d maps — pick one:"`+"\n"+
			`if c is false then return ""`+"\n"+
			`return item 1 of c`,
		strings.Join(quoted, ", "), len(maps))

	out, err := exec.Command("osascript", "-e", script).Output()
	if err != nil {
		return "", ErrPickMapUnavailable
	}
	choice := strings.TrimSpace(string(out))
	if choice == "" {
		return def, nil
	}
	for _, m := range maps {
		if strings.EqualFold(m, choice) {
			return m, nil
		}
	}
	return def, nil
}

// sanitizeMapName: see pickmap_windows.go. Duplicated (small, and the two
// files never build together) to keep each self-contained.
func sanitizeMapName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '_' || (r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
			b.WriteRune(r)
		}
	}
	return b.String()
}
