// Package procstats measures how much CPU and memory THIS process — the
// game engine — is using, for the debug overlay. Never system-wide totals:
// other programs' load doesn't show up here.
//
//   - CPU: the process's own CPU time (user + kernel, every thread) over the
//     last sampling interval, as a percentage of ONE core — so 250% means
//     two and a half cores' worth of work. Cores is the machine's logical
//     core count, for scale.
//   - Memory: RSS, the process's resident memory — what the OS has actually
//     given it, including the GPU driver's and cgo libraries' allocations
//     inside the process (not video memory on the card itself) — and the Go
//     runtime's own figures: GoHeap, the live heap objects, and GoTotal,
//     everything the runtime has mapped.
//
// The OS-specific parts (processCPUTime, processRSS) live in
// procstats_<os>.go; an OS without them reports CPUKnown / RSS as absent
// rather than guessing.
package procstats

import (
	"runtime"
	"runtime/metrics"
	"time"
)

// Interval is how often Sampler.Update actually re-measures; calls in
// between return the previous sample. Twice a second keeps the numbers
// readable and the cost negligible (one syscall + one small file read).
const Interval = 500 * time.Millisecond

// Sample is one measurement.
type Sample struct {
	CPUPercent float64 // % of one core over the last interval
	CPUKnown   bool    // false until two readings exist, or on an OS without process CPU times
	Cores      int
	RSS        uint64 // bytes resident; 0 = unknown on this OS
	GoHeap     uint64 // bytes in live Go heap objects
	GoTotal    uint64 // bytes the Go runtime has mapped in total
}

// Sampler turns successive readings into a Sample. The zero value is
// ready to use; it isn't safe for concurrent use (the engine calls it from
// its render loop only).
type Sampler struct {
	cur      Sample
	lastAt   time.Time
	lastCPU  time.Duration
	haveBase bool
	metrics  []metrics.Sample
}

// Update re-measures if Interval has passed since the last measurement (or
// there hasn't been one) and returns the current Sample.
func (s *Sampler) Update(now time.Time) Sample {
	if s.haveBase && now.Sub(s.lastAt) < Interval {
		return s.cur
	}
	s.cur.Cores = runtime.NumCPU()

	if cpu, ok := processCPUTime(); ok {
		if s.haveBase {
			if wall := now.Sub(s.lastAt); wall > 0 {
				s.cur.CPUPercent = 100 * float64(cpu-s.lastCPU) / float64(wall)
				s.cur.CPUKnown = true
			}
		}
		s.lastCPU = cpu
	}
	if rss, ok := processRSS(); ok {
		s.cur.RSS = rss
	}

	if s.metrics == nil {
		s.metrics = []metrics.Sample{
			{Name: "/memory/classes/heap/objects:bytes"},
			{Name: "/memory/classes/total:bytes"},
		}
	}
	metrics.Read(s.metrics) // no stop-the-world, unlike runtime.ReadMemStats
	if v := s.metrics[0].Value; v.Kind() == metrics.KindUint64 {
		s.cur.GoHeap = v.Uint64()
	}
	if v := s.metrics[1].Value; v.Kind() == metrics.KindUint64 {
		s.cur.GoTotal = v.Uint64()
	}

	s.lastAt, s.haveBase = now, true
	return s.cur
}
