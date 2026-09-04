//go:build windows

package audio

import (
	"log"

	"twopointfive/wad"
)

// musicBackend is the common surface of the two Windows music engines.
type musicBackend interface {
	Play(events []wad.MusEvent)
	Stop()
}

// MusicPlayer is the Windows music front end. It prefers FluidSynth with a
// bundled soundfont — far better instruments than the OS synth — and falls
// back to winmm's MIDI_MAPPER (the Microsoft GS Wavetable synth), which
// needs nothing installed. NewMusicPlayer only fails if both are
// unavailable. See fluidsynth_windows.go and winmm_windows.go.
type MusicPlayer struct {
	backend musicBackend
}

// NewMusicPlayer selects the backend and logs which one is in use, so a
// tester can tell at a glance whether the soundfont path took.
func NewMusicPlayer() (*MusicPlayer, error) {
	if b, sf, err := newFluidMusic(); err == nil {
		log.Printf("audio: music via FluidSynth (soundfont %s)", sf)
		return &MusicPlayer{backend: b}, nil
	} else {
		log.Printf("audio: FluidSynth music unavailable (%v) — using the Windows GS Wavetable synth", err)
	}
	wm, err := newWinmmMusic()
	if err != nil {
		return nil, err
	}
	return &MusicPlayer{backend: wm}, nil
}

func (m *MusicPlayer) Play(events []wad.MusEvent) { m.backend.Play(events) }
func (m *MusicPlayer) Stop()                      { m.backend.Stop() }
