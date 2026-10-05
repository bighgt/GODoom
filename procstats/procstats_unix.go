//go:build unix && !linux

package procstats

import (
	"syscall"
	"time"
)

// processCPUTime is this process's total user + system CPU time
// (getrusage RUSAGE_SELF — available on every other Unix, macOS included).
func processCPUTime() (time.Duration, bool) {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0, false
	}
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano()), true
}

// processRSS isn't available portably here (getrusage's maxrss is the
// PEAK, not the current size) — the overlay then shows the Go runtime's
// figures alone.
func processRSS() (uint64, bool) { return 0, false }
