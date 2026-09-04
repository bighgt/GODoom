//go:build !windows

package main

// holdConsoleOpen is a no-op off Windows: every other platform the engine
// might run on launches it from a shell that outlives the process, so the
// logs stay on screen without help.
func holdConsoleOpen(hadError bool) {}
