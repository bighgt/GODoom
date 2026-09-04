//go:build !windows

package window

import (
	"os"

	"golang.org/x/sys/unix"
)

// stdinInteractive reports whether fd 0 is a real terminal: it answers the
// "get window size" ioctl (TIOCGWINSZ, present on every unix incl. macOS)
// only when it's a tty — a pipe, a regular file or /dev/null all fail it,
// which is exactly how a GUI-launched process (stdin = /dev/null) is told
// apart from `engine` run in a shell.
func stdinInteractive() bool {
	_, err := unix.IoctlGetWinsize(int(os.Stdin.Fd()), unix.TIOCGWINSZ)
	return err == nil
}
