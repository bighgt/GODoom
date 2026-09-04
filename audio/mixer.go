package audio

import (
	"encoding/binary"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/ebitengine/oto/v3"
)

// This file is the shared sound path for every platform, structured like
// PrBoom's i_sound.c rather than vanilla Doom's two-independent-devices
// model.
//
// That model — a fresh oto Player per one-shot clip, plus a FluidSynth
// instance that opens its OWN audio device — means two (or more)
// independent streams into the OS mixer, each with its own clock and its
// own buffer. On a priority-starved audio thread (PipeWire was the worst
// offender) that is exactly what makes the music stutter and the effects
// arrive loud and ragged; even where it holds together it wastes latency
// and routes every 8-bit 11 kHz clip through the OS resampler.
//
// Instead: ONE output stream, S16 stereo at 48 kHz, fed by a single
// software mixer. The mixer sums a fixed set of effect "voices" (linear
// interpolation from the 8-bit 11 kHz DMX data, oldest-wins stealing) and
// the music synth's rendered block, applies master gains with headroom,
// and hard-clips — the same shape as I_UpdateSound.
//
// Gains and buffering are env-tunable without a rebuild:
//
//	TPF_SFX_GAIN    master effect gain   (default 0.5)
//	TPF_MUSIC_GAIN  master music gain    (default 0.5)
//	TPF_AUDIO_MS    output buffer, ms    (default 55) — raise if it crackles

const (
	mixRate     = 48000
	mixChannels = 2
	mixVoices   = 16 // PrBoom's MAX_CHANNELS is 8; we can afford double
)

// mixVoice is one playing effect: a cursor into 8-bit unsigned mono PCM.
type mixVoice struct {
	pcm    []byte
	pos    float64 // fractional read cursor, in source samples
	step   float64 // source samples per output frame (srcRate / mixRate)
	gain   float32
	seq    uint64 // insertion order, for oldest-wins stealing
	active bool
}

type mixer struct {
	ctx    *oto.Context
	player *oto.Player

	mu        sync.Mutex
	voices    [mixVoices]mixVoice
	seq       uint64
	sfxGain   float32
	musicGain float32

	// musicRender, when non-nil, fills an interleaved-stereo int16 block
	// from the music synth. It is set and cleared by the MusicPlayer under
	// mu, and Read only ever calls it while holding mu — so once
	// setMusicRender(nil) returns, the audio thread is guaranteed not to be
	// inside the synth, and the synth can be deleted.
	musicRender func([]int16)
	musicBuf    []int16
}

var (
	sharedOnce sync.Once
	sharedInst *mixer
	sharedErr  error
)

// sharedAudio lazily stands up the process-wide mixer and its output
// stream. Both NewDevice and NewMusicPlayer call it; whichever runs first
// creates it and the other reuses the result (oto allows only one Context
// per process).
func sharedAudio() (*mixer, error) {
	sharedOnce.Do(func() { sharedInst, sharedErr = startSharedAudio() })
	return sharedInst, sharedErr
}

func startSharedAudio() (*mixer, error) {
	ms := envInt("TPF_AUDIO_MS", 55)
	m := &mixer{
		sfxGain:   envGain("TPF_SFX_GAIN", 0.5),
		musicGain: envGain("TPF_MUSIC_GAIN", 0.5),
	}

	ctx, ready, err := oto.NewContext(&oto.NewContextOptions{
		SampleRate:   mixRate,
		ChannelCount: mixChannels,
		Format:       oto.FormatSignedInt16LE,
		BufferSize:   time.Duration(ms) * time.Millisecond,
	})
	if err != nil {
		return nil, fmt.Errorf("audio: open device: %w", err)
	}
	<-ready
	m.ctx = ctx

	p := ctx.NewPlayer(m)
	// Pull oto's player-side buffer down near the device buffer: its 0.5 s
	// default would put half a second of latency on every gunshot.
	p.SetBufferSize(mixRate * mixChannels * 2 * ms / 1000)
	p.Play()
	m.player = p
	return m, nil
}

// playSFX starts a one-shot effect, stealing the oldest voice if all are
// busy. pcm is the clip's raw 8-bit unsigned mono samples, srcRate its
// sample rate, gain the caller's per-sound multiplier (weaponVolume etc.).
func (m *mixer) playSFX(pcm []byte, srcRate int, gain float32) {
	if len(pcm) < 2 || gain <= 0 {
		return
	}
	if srcRate <= 0 {
		srcRate = 11025
	}
	m.mu.Lock()
	i := m.pickVoiceLocked()
	m.voices[i] = mixVoice{
		pcm:    pcm,
		step:   float64(srcRate) / float64(mixRate),
		gain:   gain,
		seq:    m.seq,
		active: true,
	}
	m.seq++
	m.mu.Unlock()
}

func (m *mixer) pickVoiceLocked() int {
	oldest, oldestSeq := 0, ^uint64(0)
	for i := range m.voices {
		if !m.voices[i].active {
			return i
		}
		if m.voices[i].seq < oldestSeq {
			oldest, oldestSeq = i, m.voices[i].seq
		}
	}
	return oldest
}

// setMusicRender installs (fn != nil) or removes (fn == nil) the music
// source. See musicRender's doc comment for the teardown-safety guarantee.
func (m *mixer) setMusicRender(fn func([]int16)) {
	m.mu.Lock()
	m.musicRender = fn
	m.mu.Unlock()
}

// Read is the oto source callback: fill buf with interleaved little-endian
// int16 stereo, always completely, and never report EOF.
func (m *mixer) Read(buf []byte) (int, error) {
	frames := len(buf) / (mixChannels * 2)
	if frames == 0 {
		return 0, nil
	}

	m.mu.Lock()

	var music []int16
	if m.musicRender != nil {
		if cap(m.musicBuf) < frames*mixChannels {
			m.musicBuf = make([]int16, frames*mixChannels)
		}
		music = m.musicBuf[:frames*mixChannels]
		for i := range music {
			music[i] = 0
		}
		m.musicRender(music)
	}
	sfxGain, musicGain := m.sfxGain, m.musicGain

	for f := 0; f < frames; f++ {
		var mono float32
		for vi := range m.voices {
			v := &m.voices[vi]
			if !v.active {
				continue
			}
			i := int(v.pos)
			if i >= len(v.pcm)-1 {
				v.active = false
				continue
			}
			frac := float32(v.pos - float64(i))
			s0 := (float32(v.pcm[i]) - 128) * (1.0 / 128.0)
			s1 := (float32(v.pcm[i+1]) - 128) * (1.0 / 128.0)
			mono += (s0 + (s1-s0)*frac) * v.gain
			v.pos += v.step
		}
		l := mono * sfxGain
		r := l
		if music != nil {
			l += float32(music[f*mixChannels]) * (1.0 / 32768.0) * musicGain
			r += float32(music[f*mixChannels+1]) * (1.0 / 32768.0) * musicGain
		}
		putI16LE(buf[(f*mixChannels)*2:], clipI16(l))
		putI16LE(buf[(f*mixChannels+1)*2:], clipI16(r))
	}

	m.mu.Unlock()
	return frames * mixChannels * 2, nil
}

func clipI16(x float32) int16 {
	switch {
	case x >= 1:
		return 32767
	case x <= -1:
		return -32768
	default:
		return int16(x * 32767)
	}
}

func putI16LE(b []byte, v int16) { binary.LittleEndian.PutUint16(b, uint16(v)) }

func envInt(key string, def int) int {
	if s := os.Getenv(key); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return n
		}
	}
	return def
}

func envGain(key string, def float32) float32 {
	if s := os.Getenv(key); s != "" {
		if f, err := strconv.ParseFloat(s, 32); err == nil && f >= 0 {
			return float32(f)
		}
	}
	return def
}
