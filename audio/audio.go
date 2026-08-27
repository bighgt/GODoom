// Package audio is the engine's sound output. It deliberately mirrors
// vanilla Doom's own split between two unrelated audio systems: Device
// plays short one-shot DMX PCM clips (door open/close, weapon fire, ...)
// the way id's PC/Sound-Blaster mixer did, while MusicPlayer (see
// midi_windows.go) streams a level's MUS track to a MIDI synth the way
// id's MPU-401/OPL driver did — see game_design.txt section on sound.
package audio

import (
	"bytes"
	"fmt"

	"github.com/ebitengine/oto/v3"

	"twopointfive/wad"
)

// deviceSampleRate matches the overwhelming majority of vanilla DMX sound
// effects (11025 Hz), so most sounds need no resampling at all before
// being handed to oto — see PlaySound.
const deviceSampleRate = 11025

// Device is the shared one-shot sound-effect player. oto supports exactly
// one Context per process, so create exactly one Device.
type Device struct {
	ctx *oto.Context
}

// NewDevice opens the audio output device.
func NewDevice() (*Device, error) {
	ctx, ready, err := oto.NewContext(&oto.NewContextOptions{
		SampleRate:   deviceSampleRate,
		ChannelCount: 1,
		Format:       oto.FormatUnsignedInt8,
	})
	if err != nil {
		return nil, fmt.Errorf("audio: open device: %w", err)
	}
	<-ready
	return &Device{ctx: ctx}, nil
}

// PlaySound plays s once, fire-and-forget; safe to call from any goroutine
// and while other sounds are already playing (oto mixes them together).
func (d *Device) PlaySound(s *wad.Sound) {
	if d == nil || s == nil || len(s.Samples) == 0 {
		return
	}
	pcm := s.Samples
	if s.SampleRate != deviceSampleRate {
		pcm = resample(pcm, s.SampleRate, deviceSampleRate)
	}
	p := d.ctx.NewPlayer(bytes.NewReader(pcm))
	p.Play()
	// p is deliberately not retained after this: oto's mixer keeps a
	// playing Player alive via its own internal registration until
	// playback finishes, so letting this local reference go out of scope
	// is the standard fire-and-forget pattern for one-shot oto sounds.
}

// resample does simple nearest-neighbor rate conversion — good enough for
// 8-bit retro sound effects, and only ever exercised by the small minority
// of vanilla sounds recorded at 22050 Hz instead of the usual 11025 Hz.
func resample(pcm []byte, fromRate, toRate int) []byte {
	if fromRate <= 0 || toRate <= 0 || len(pcm) == 0 || fromRate == toRate {
		return pcm
	}
	outLen := len(pcm) * toRate / fromRate
	if outLen <= 0 {
		return pcm
	}
	out := make([]byte, outLen)
	for i := range out {
		srcIdx := i * fromRate / toRate
		if srcIdx >= len(pcm) {
			srcIdx = len(pcm) - 1
		}
		out[i] = pcm[srcIdx]
	}
	return out
}
