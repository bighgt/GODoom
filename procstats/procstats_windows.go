package procstats

import (
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// processCPUTime is this process's total kernel + user CPU time, every
// thread included (GetProcessTimes; FILETIME ticks are 100 ns).
func processCPUTime() (time.Duration, bool) {
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(windows.CurrentProcess(), &creation, &exit, &kernel, &user); err != nil {
		return 0, false
	}
	ticks := func(f windows.Filetime) int64 { return int64(f.HighDateTime)<<32 | int64(f.LowDateTime) }
	return time.Duration((ticks(kernel) + ticks(user)) * 100), true
}

// processMemoryCounters is PROCESS_MEMORY_COUNTERS (psapi.h).
type processMemoryCounters struct {
	cb                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
}

// k32GetProcessMemoryInfo is psapi's GetProcessMemoryInfo, exported from
// kernel32 since Windows 7 — x/sys/windows doesn't bind it.
var k32GetProcessMemoryInfo = windows.NewLazySystemDLL("kernel32.dll").NewProc("K32GetProcessMemoryInfo")

// processRSS is the working set — Windows' resident memory figure.
func processRSS() (uint64, bool) {
	var c processMemoryCounters
	c.cb = uint32(unsafe.Sizeof(c))
	if err := k32GetProcessMemoryInfo.Find(); err != nil {
		return 0, false
	}
	r, _, _ := k32GetProcessMemoryInfo.Call(uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&c)), uintptr(c.cb))
	if r == 0 {
		return 0, false
	}
	return uint64(c.WorkingSetSize), true
}
