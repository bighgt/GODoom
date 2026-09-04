//go:build windows || darwin

package window

import (
	"errors"
	"fmt"

	"github.com/sqweek/dialog"
)

// pickWAD opens the OS's native "open file" dialog (Win32 GetOpenFileName
// on Windows, NSOpenPanel via Cocoa on macOS), filtered to .wad files, and
// returns the path the user chose. This is how the engine satisfies "ask
// for the WAD file to load" at startup, before any GLFW/Vulkan window
// exists. See picker_unix.go for the zenity/kdialog equivalent on other
// Unixes, which avoids a compile-time GTK dependency.
func pickWAD() (string, error) {
	path, err := dialog.File().
		Title("Select a Doom-engine WAD file").
		Filter("Doom WAD files", "wad").
		Load()
	if err != nil {
		if errors.Is(err, dialog.ErrCancelled) {
			return "", ErrPickCancelled
		}
		return "", fmt.Errorf("window: WAD file dialog: %w", err)
	}
	return path, nil
}
