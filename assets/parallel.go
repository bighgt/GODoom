package assets

import (
	"runtime"
	"sync"
)

// ParallelFor runs fn(0) .. fn(n-1) across the machine's cores and waits
// for them all — for independent decode jobs (pack PNGs, model skins) whose
// results land in a slot per index, so callers can merge them afterwards
// in their original order.
func ParallelFor(n int, fn func(i int)) {
	workers := min(runtime.GOMAXPROCS(0), n)
	if workers <= 1 {
		for i := range n {
			fn(i)
		}
		return
	}
	var wg sync.WaitGroup
	next := make(chan int, n)
	for i := range n {
		next <- i
	}
	close(next)
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for i := range next {
				fn(i)
			}
		}()
	}
	wg.Wait()
}
