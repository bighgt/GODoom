package engine

import (
	"testing"

	"twopointfive/procstats"
)

// TestDebugOverlayCPUAndMemoryRows: the overlay's CPU/MEM rows read the
// engine process's own figures, and degrade to what's known — no CPU
// until two samples exist, Go-runtime memory alone where the OS gives no
// resident size.
func TestDebugOverlayCPUAndMemoryRows(t *testing.T) {
	st := procstats.Sample{CPUPercent: 137.4, CPUKnown: true, Cores: 16, RSS: 412 << 20, GoHeap: 180 << 20, GoTotal: 260 << 20}
	if got, want := cpuLine(st), "CPU 137% of a core  (16 cores)"; got != want {
		t.Errorf("cpuLine = %q, want %q", got, want)
	}
	if got, want := memLine(st), "MEM 412 MB  (Go heap 180 MB of 260 MB)"; got != want {
		t.Errorf("memLine = %q, want %q", got, want)
	}
	st.CPUKnown, st.RSS = false, 0
	if got, want := cpuLine(st), "CPU --  (16 cores)"; got != want {
		t.Errorf("cpuLine (unknown) = %q, want %q", got, want)
	}
	if got, want := memLine(st), "MEM Go heap 180 MB of 260 MB"; got != want {
		t.Errorf("memLine (no RSS) = %q, want %q", got, want)
	}
}
