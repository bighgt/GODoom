package window

import (
	"errors"
	"fmt"

	"github.com/sqweek/dialog"
)

// ErrPickCancelled is returned by PickWADFile when the user closes or
// cancels the file dialog instead of choosing a file.
var ErrPickCancelled = errors.New("window: WAD file selection cancelled")

// PickWADFile opens the OS's native "open file" dialog (Win32
// GetOpenFileName on Windows), filtered to .wad files, and returns the path
// the user chose. This is how the engine satisfies "ask for the WAD file to
// load" at startup, before any GLFW/Vulkan window exists.
func PickWADFile() (string, error) {
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
