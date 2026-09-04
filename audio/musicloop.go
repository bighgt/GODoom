package audio

import (
	"os"
	"sync"
	"time"

	"twopointfive/wad"
)

// musSecondsPerTick assumes the default MIDI tempo (120 BPM) at MUS's
// standard 70 ticks per quarter note — a MUS lump carries no tempo meta
// event, so mus2mid-style playback fixes the tempo the same way.
const musSecondsPerTick = 60.0 / 120.0 / 70.0

// soundFontEnv names an absolute .sf2 path that overrides the search list.
const soundFontEnv = "TPF_SOUNDFONT"

// musicLoop runs a decoded MUS event stream on a background goroutine,
// looping until stopped. It is embedded by every music backend (winmm and
// FluidSynth on Windows, FluidSynth on Linux): the backend supplies send
// (deliver one event) and silence (all-notes-off, run once when the loop
// stops), and optionally reset (run before each (re)start). restart/halt
// enforce the wait-for-the-previous-goroutine discipline that keeps two
// goroutines from ever driving the synth at once.
type musicLoop struct {
	send    func(wad.MusEvent)
	silence func()
	reset   func()

	mu      sync.Mutex
	stopCh  chan struct{}
	doneCh  chan struct{}
	playing bool
}

func (l *musicLoop) restart(events []wad.MusEvent) {
	l.mu.Lock()
	if l.playing {
		stopCh, doneCh := l.stopCh, l.doneCh
		l.playing = false
		l.mu.Unlock()
		close(stopCh)
		<-doneCh
		l.mu.Lock()
	}
	if len(events) == 0 {
		l.mu.Unlock()
		return
	}
	if l.reset != nil {
		l.reset()
	}
	l.stopCh = make(chan struct{})
	l.doneCh = make(chan struct{})
	l.playing = true
	stopCh, doneCh := l.stopCh, l.doneCh
	l.mu.Unlock()

	go l.run(events, stopCh, doneCh)
}

func (l *musicLoop) halt() {
	l.mu.Lock()
	if l.playing {
		stopCh, doneCh := l.stopCh, l.doneCh
		l.playing = false
		l.mu.Unlock()
		close(stopCh)
		<-doneCh
	} else {
		l.mu.Unlock()
	}
}

func (l *musicLoop) run(events []wad.MusEvent, stop, done chan struct{}) {
	defer close(done)
	for {
		next := time.Now()
		for _, e := range events {
			if e.DeltaTicks > 0 {
				next = next.Add(time.Duration(float64(e.DeltaTicks) * musSecondsPerTick * float64(time.Second)))
				if d := time.Until(next); d > 0 {
					select {
					case <-stop:
						l.silence()
						return
					case <-time.After(d):
					}
				}
			}
			l.send(e)
		}
		select {
		case <-stop:
			l.silence()
			return
		default:
		}
	}
}

// findSoundFont returns the first usable path: $TPF_SOUNDFONT if it is set
// and points at a file, otherwise the first existing entry in paths, else
// "". A set-but-missing $TPF_SOUNDFONT returns "" so the caller reports the
// "no soundfont" hint rather than silently searching.
func findSoundFont(paths []string) string {
	if p := os.Getenv(soundFontEnv); p != "" {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
		return ""
	}
	for _, p := range paths {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	return ""
}
