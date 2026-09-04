//go:build !windows && !linux

package audio

import "twopointfive/wad"

// MusicPlayer is a no-op stub on the platforms with no MUS/MIDI backend yet
// (macOS, the BSDs). Windows drives winmm.dll (midi_windows.go) and Linux
// drives libfluidsynth (midi_linux.go); a macOS port would add an
// AudioToolbox MusicPlayer or its own FluidSynth path with the same four
// symbols. Sound effects (package audio's Device) are unaffected and work
// everywhere oto does.
type MusicPlayer struct{}

// NewMusicPlayer always succeeds; Play is simply a no-op.
func NewMusicPlayer() (*MusicPlayer, error) { return &MusicPlayer{}, nil }

func (m *MusicPlayer) Play(events []wad.MusEvent) {}
func (m *MusicPlayer) Stop()                      {}
