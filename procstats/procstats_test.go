package procstats

import (
	"runtime"
	"testing"
	"time"
)

// TestSamplerMeasuresThisProcess: busy work in this process shows up as
// CPU (at least a sizeable fraction of one core while two goroutines
// spin), memory figures are non-zero (RSS where the OS provides it), and
// calls inside Interval return the same sample instead of re-measuring.
func TestSamplerMeasuresThisProcess(t *testing.T) {
	var s Sampler
	t0 := time.Now()
	first := s.Update(t0)
	if first.CPUKnown {
		t.Error("CPU can't be known from a single reading")
	}
	if first.GoHeap == 0 || first.GoTotal < first.GoHeap {
		t.Errorf("Go memory: heap %d, total %d", first.GoHeap, first.GoTotal)
	}
	if runtime.GOOS == "linux" || runtime.GOOS == "windows" {
		if first.RSS == 0 {
			t.Error("RSS unknown on an OS that provides it")
		}
	}
	if again := s.Update(t0.Add(Interval / 2)); again != first {
		t.Error("an Update inside Interval re-measured")
	}

	stop := time.Now().Add(Interval + 200*time.Millisecond) // outlast Interval, or Update returns the cached sample
	done := make(chan bool)
	for i := 0; i < 2; i++ {
		go func() {
			x := 0
			for time.Now().Before(stop) {
				x++
			}
			done <- x > 0
		}()
	}
	<-done
	<-done
	next := s.Update(time.Now())
	if runtime.GOOS == "linux" || runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		if !next.CPUKnown || next.CPUPercent < 50 {
			t.Errorf("CPU while two goroutines spin = %.1f%% (known %v), want well above 50%% of a core", next.CPUPercent, next.CPUKnown)
		}
	}
	t.Logf("cpu %.1f%% of a core (%d cores), rss %d MB, go heap %d MB, go total %d MB",
		next.CPUPercent, next.Cores, next.RSS>>20, next.GoHeap>>20, next.GoTotal>>20)
}
