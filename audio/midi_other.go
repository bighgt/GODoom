//go:build !windows

package audio

import "twopointfive/wad"

// MusicPlayer is a no-op stub outside Windows: MUS/MIDI playback in this
// project only talks to Windows' winmm.dll synth so far (see
// midi_windows.go) — extending it to other platforms, e.g. via a bundled
// software synth, is future work; see game_design.txt. Sound effects
// (package audio's Device) are unaffected and work everywhere oto does.
type MusicPlayer struct{}

// NewMusicPlayer always succeeds; Play is simply a no-op.
func NewMusicPlayer() (*MusicPlayer, error) { return &MusicPlayer{}, nil }

func (m *MusicPlayer) Play(events []wad.MusEvent) {}
func (m *MusicPlayer) Stop()                      {}
