//go:build windows

package audio

import (
	"fmt"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"twopointfive/wad"
)

var (
	winmm               = syscall.NewLazyDLL("winmm.dll")
	procMidiOutOpen     = winmm.NewProc("midiOutOpen")
	procMidiOutClose    = winmm.NewProc("midiOutClose")
	procMidiOutShortMsg = winmm.NewProc("midiOutShortMsg")
)

const midiMapper = 0xFFFFFFFF // MIDI_MAPPER: Windows' default synth (Microsoft GS Wavetable Synth)

// secondsPerTick assumes the default MIDI tempo (120 BPM) at MUS's standard
// resolution of 70 ticks per quarter note — the same assumption
// mus2mid-generated files rely on, since a MUS lump carries no tempo meta
// event of its own to read.
const secondsPerTick = 60.0 / 120.0 / 70.0

// MusicPlayer streams a decoded MUS track to the system's MIDI output via
// winmm.dll — the same role id's own i_music.c MIDI driver played, just
// targeting Windows' built-in software synth instead of period OPL/MPU-401
// hardware. Windows-only; see midi_other.go for the no-op fallback
// elsewhere, and game_design.txt for the rationale.
type MusicPlayer struct {
	handle uintptr

	mu      sync.Mutex
	stopCh  chan struct{}
	doneCh  chan struct{} // closed by loop() right before it returns
	playing bool
}

// NewMusicPlayer opens a handle to the system MIDI output device.
func NewMusicPlayer() (*MusicPlayer, error) {
	var handle uintptr
	r, _, _ := procMidiOutOpen.Call(uintptr(unsafe.Pointer(&handle)), uintptr(midiMapper), 0, 0, 0)
	if r != 0 {
		return nil, fmt.Errorf("audio: midiOutOpen failed (MMRESULT %d)", r)
	}
	return &MusicPlayer{handle: handle}, nil
}

// Play stops whatever is currently playing (if anything) and starts
// looping events in the background — Doom's own level music loops
// indefinitely until the level changes, and this does the same.
//
// Switching tracks (or Stop, below) waits for the previous loop goroutine
// to actually finish before proceeding: it's the only way to be sure two
// goroutines never call into winmm concurrently, and — critically for
// Stop — that the MIDI handle isn't closed out from under a goroutine
// that's still using it to send its final "all notes off" cleanup.
func (m *MusicPlayer) Play(events []wad.MusEvent) {
	m.mu.Lock()
	if m.playing {
		stopCh, doneCh := m.stopCh, m.doneCh
		m.playing = false
		m.mu.Unlock()
		close(stopCh)
		<-doneCh
		m.mu.Lock()
	}
	if len(events) == 0 {
		m.mu.Unlock()
		return
	}
	m.stopCh = make(chan struct{})
	m.doneCh = make(chan struct{})
	m.playing = true
	stopCh, doneCh := m.stopCh, m.doneCh
	m.mu.Unlock()

	go m.loop(events, stopCh, doneCh)
}

func (m *MusicPlayer) loop(events []wad.MusEvent, stop, done chan struct{}) {
	defer close(done)
	for {
		next := time.Now()
		for _, e := range events {
			if e.DeltaTicks > 0 {
				next = next.Add(time.Duration(float64(e.DeltaTicks) * secondsPerTick * float64(time.Second)))
				if d := time.Until(next); d > 0 {
					select {
					case <-stop:
						m.allNotesOff()
						return
					case <-time.After(d):
					}
				}
			}
			m.send(e)
		}
		select {
		case <-stop:
			m.allNotesOff()
			return
		default:
		}
	}
}

func (m *MusicPlayer) send(e wad.MusEvent) {
	switch e.Type {
	case wad.MusNoteOff:
		m.shortMsg(0x80|e.Channel, e.Data1, 0)
	case wad.MusNoteOn:
		m.shortMsg(0x90|e.Channel, e.Data1, e.Data2)
	case wad.MusPitchBend:
		m.shortMsg(0xE0|e.Channel, byte(e.Bend14&0x7F), byte((e.Bend14>>7)&0x7F))
	case wad.MusControlChange:
		m.shortMsg(0xB0|e.Channel, e.Data1, e.Data2)
	case wad.MusProgramChange:
		m.shortMsg(0xC0|e.Channel, e.Data1, 0)
	}
}

func (m *MusicPlayer) shortMsg(status, data1, data2 byte) {
	msg := uint32(status) | uint32(data1)<<8 | uint32(data2)<<16
	procMidiOutShortMsg.Call(m.handle, uintptr(msg))
}

func (m *MusicPlayer) allNotesOff() {
	for ch := byte(0); ch < 16; ch++ {
		m.shortMsg(0xB0|ch, 0x7B, 0) // MIDI CC 123: all notes off
	}
}

// Stop halts playback and releases the MIDI output device. Blocks until
// the player goroutine (if any) has fully exited, so the device is never
// closed while still in use — see Play's doc comment.
func (m *MusicPlayer) Stop() {
	m.mu.Lock()
	if m.playing {
		stopCh, doneCh := m.stopCh, m.doneCh
		m.playing = false
		m.mu.Unlock()
		close(stopCh)
		<-doneCh
	} else {
		m.mu.Unlock()
	}
	procMidiOutClose.Call(m.handle)
}
