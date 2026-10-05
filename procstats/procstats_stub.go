//go:build !unix && !windows

package procstats

import "time"

// No process CPU/memory source on this OS: the overlay shows the Go
// runtime's own memory figures only.
func processCPUTime() (time.Duration, bool) { return 0, false }
func processRSS() (uint64, bool)            { return 0, false }
