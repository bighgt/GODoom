// Package audio is the engine's sound output. It is structured like
// PrBoom's i_sound.c rather than vanilla Doom's model of two unrelated
// audio devices (a PC/Sound-Blaster PCM mixer plus an MPU-401/OPL MIDI
// synth): there is ONE output stream — S16 stereo at 48 kHz — fed by a
// single software mixer (mixer.go).
//
//   - Device adds short one-shot DMX PCM clips (door open/close, weapon
//     fire, ...) to that mixer as effect "voices": linear interpolation
//     from the 8-bit 11 kHz source, a fixed voice pool with oldest-wins
//     stealing, master gain with headroom, hard clipping — the shape of
//     I_UpdateSound.
//   - MusicPlayer streams a level's MUS track to a MIDI synth. Where the
//     synth is FluidSynth (Linux via purego, Windows via cgo) it is driven
//     in PULL mode: it opens no audio device of its own, and the mixer
//     renders a block from it inside its output callback and sums it with
//     the effects — the way PrBoom renders its MIDI player into the SDL
//     callback. The winmm fallback on Windows (winmm_windows.go) is the OS
//     synth on its own output, and macOS/BSD have no MUS backend yet
//     (midi_other.go).
//
// The public surface is the same on every OS (Device: NewDevice /
// PlaySound / PlaySoundVol; MusicPlayer: NewMusicPlayer / Play / Stop);
// only the synth backend is build-tagged. See game_design.txt section on
// sound.
package audio

import "twopointfive/wad"

// Device is the one-shot sound-effect player: a thin handle to the
// process-wide software mixer (mixer.go). PlaySound adds another voice
// rather than opening a fresh output stream per clip, and the mixer shares
// its single output stream with the music player.
type Device struct {
	m *mixer
}

// NewDevice brings up (or joins) the shared mixer and its output stream.
func NewDevice() (*Device, error) {
	m, err := sharedAudio()
	if err != nil {
		return nil, err
	}
	return &Device{m: m}, nil
}

// PlaySound plays s once at full volume, fire-and-forget; safe from any
// goroutine and while other sounds are playing (the mixer sums them).
func (d *Device) PlaySound(s *wad.Sound) { d.PlaySoundVol(s, 1) }

// PlaySoundVol is PlaySound with a per-sound loudness multiplier (1 = the
// clip's own level, 0 = silent). Values above 1 amplify and may clip
// against the mixer's hard limiter. Used for the weaponVolume / worldVolume
// knobs (see engine/sound.go).
func (d *Device) PlaySoundVol(s *wad.Sound, volume float64) {
	if d == nil || d.m == nil || s == nil || volume <= 0 {
		return
	}
	d.m.playSFX(s.Samples, s.SampleRate, float32(volume))
}
