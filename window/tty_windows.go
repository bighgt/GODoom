//go:build windows

package window

import (
	"os"

	"golang.org/x/sys/windows"
)

// stdinInteractive reports whether fd 0 is a console: GetConsoleMode
// succeeds on a real console handle and fails on a pipe / file / NUL.
func stdinInteractive() bool {
	var mode uint32
	err := windows.GetConsoleMode(windows.Handle(os.Stdin.Fd()), &mode)
	return err == nil
}
