package procstats

import (
	"bytes"
	"os"
	"strconv"
	"syscall"
	"time"
)

// processCPUTime is this process's total user + system CPU time, every
// thread included (getrusage RUSAGE_SELF).
func processCPUTime() (time.Duration, bool) {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0, false
	}
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano()), true
}

// processRSS reads the resident set size from /proc/self/statm — its
// second field, in pages.
func processRSS() (uint64, bool) {
	b, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0, false
	}
	f := bytes.Fields(b)
	if len(f) < 2 {
		return 0, false
	}
	pages, err := strconv.ParseUint(string(f[1]), 10, 64)
	if err != nil {
		return 0, false
	}
	return pages * uint64(os.Getpagesize()), true
}
