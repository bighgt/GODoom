package main

import (
	"bufio"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// holdConsoleOpen keeps the debug console readable after the game window has
// gone away. When the engine is launched by double-clicking the .exe (or any
// other way where it is the only process attached to a freshly-spawned
// console), Windows tears that console down the instant main returns, so
// every log line scrolls past unread. Detect that case — exactly one process
// on the console — and wait for the user to press Enter first. When the
// engine was started from an existing shell (PowerShell, cmd, a terminal in
// the editor) the console outlives the process on its own, so don't pause.
//
// Set TPF_NO_PAUSE=1 to skip the wait unconditionally.
func holdConsoleOpen(hadError bool) {
	if os.Getenv("TPF_NO_PAUSE") != "" {
		return
	}
	if !ownsConsole() {
		return
	}
	if hadError {
		fmt.Fprint(os.Stderr, "\nengine exited with an error. Press Enter to close this window...")
	} else {
		fmt.Fprint(os.Stderr, "\nengine closed. Press Enter to close this window...")
	}
	bufio.NewReader(os.Stdin).ReadString('\n')
}

// ownsConsole reports whether this process is the only one attached to its
// console, which is the signal that the console was created for us and will
// die with us (the double-click / "Run" case) rather than being an existing
// shell we were launched from.
func ownsConsole() bool {
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	getConsoleProcessList := kernel32.NewProc("GetConsoleProcessList")

	var pids [4]uint32
	n, _, _ := getConsoleProcessList.Call(
		uintptr(unsafe.Pointer(&pids[0])),
		uintptr(len(pids)),
	)
	// n == 0 means no console at all (e.g. output fully redirected); treat
	// that as "nothing to hold open". n == 1 means we're alone on it.
	return n == 1
}
