//go:build windows

package window

import (
	"os/exec"
	"strings"
)

// pickMap shows the WAD's maps in PowerShell's built-in graphical list
// picker (Out-GridView), the Windows equivalent of the zenity list on Unix.
// It ships with Windows PowerShell 5.1 on every desktop Windows 10/11; a
// stripped install (Server Core / Nano) or a PowerShell error returns
// ErrPickMapUnavailable and the caller uses def. Cancel / close -> def.
func pickMap(maps []string, def string) (string, error) {
	ps, err := exec.LookPath("powershell")
	if err != nil {
		if ps, err = exec.LookPath("pwsh"); err != nil {
			return "", ErrPickMapUnavailable
		}
	}

	var b strings.Builder
	b.WriteString("$m=@(")
	for i, m := range maps {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('\'')
		b.WriteString(sanitizeMapName(m)) // map markers are [A-Za-z0-9_] only
		b.WriteByte('\'')
	}
	b.WriteString("); $s=$m | Out-GridView -Title 'Choose a start level' -OutputMode Single; if($s){[Console]::Out.WriteLine($s)}")

	out, err := exec.Command(ps, "-NoProfile", "-NonInteractive", "-Command", b.String()).Output()
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

// sanitizeMapName strips anything that isn't a plain identifier character —
// so a map name can't break out of the single-quoted PowerShell / AppleScript
// string literal it gets embedded in. A real Doom map marker (E1M1, MAP07,
// custom "LEVELA") is already only [A-Za-z0-9_].
func sanitizeMapName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '_' || (r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
			b.WriteRune(r)
		}
	}
	return b.String()
}
