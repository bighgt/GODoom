//go:build windows

package audio

import (
	"fmt"
	"syscall"
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

// winmmMusic streams a decoded MUS track to the system MIDI output via
// winmm.dll — the role id's own i_music.c MIDI driver played, on Windows'
// built-in synth. It is the fallback when no FluidSynth DLL / soundfont is
// present (see midi_windows.go); it needs nothing installed.
//
// The timing loop, and the "wait for the previous goroutine before
// switching tracks or closing the handle" discipline, live in the embedded
// musicLoop (musicloop.go); this type only supplies the winmm-specific
// event send and cleanup.
type winmmMusic struct {
	musicLoop
	handle uintptr
}

func newWinmmMusic() (*winmmMusic, error) {
	var handle uintptr
	r, _, _ := procMidiOutOpen.Call(uintptr(unsafe.Pointer(&handle)), uintptr(midiMapper), 0, 0, 0)
	if r != 0 {
		return nil, fmt.Errorf("audio: midiOutOpen failed (MMRESULT %d)", r)
	}
	m := &winmmMusic{handle: handle}
	m.musicLoop.send = m.send
	m.musicLoop.silence = m.allNotesOff
	return m, nil
}

func (m *winmmMusic) Play(events []wad.MusEvent) { m.restart(events) }

// Stop halts playback and closes the MIDI device. halt blocks until the
// loop goroutine has exited (after sending its all-notes-off), so the
// handle is never closed from under it.
func (m *winmmMusic) Stop() {
	m.halt()
	procMidiOutClose.Call(m.handle)
}

func (m *winmmMusic) send(e wad.MusEvent) {
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

func (m *winmmMusic) shortMsg(status, data1, data2 byte) {
	msg := uint32(status) | uint32(data1)<<8 | uint32(data2)<<16
	procMidiOutShortMsg.Call(m.handle, uintptr(msg))
}

func (m *winmmMusic) allNotesOff() {
	for ch := byte(0); ch < 16; ch++ {
		m.shortMsg(0xB0|ch, 0x7B, 0) // MIDI CC 123: all notes off
	}
}
