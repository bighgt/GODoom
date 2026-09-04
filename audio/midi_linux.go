//go:build linux

package audio

import (
	"fmt"
	"os"

	"github.com/ebitengine/purego"

	"twopointfive/wad"
)

var soundFontPaths = []string{
	"/usr/share/sounds/sf2/default-GM.sf2", // fluid-soundfont-gm (Debian/Ubuntu/Mint)
	"/usr/share/sounds/sf2/FluidR3_GM.sf2",
	"/usr/share/soundfonts/default.sf2", // Arch / Fedora
	"/usr/share/soundfonts/FluidR3_GM.sf2",
	"/usr/share/sounds/sf2/TimGM6mb.sf2", // timgm6mb-soundfont (small fallback)
}

// fluidAPI is the slice of libfluidsynth this player calls, bound at
// runtime with purego (no cgo, no -dev headers). Signatures mirror
// <fluidsynth.h>; every "int" there is a C int, i.e. Go int32. Note there
// is no new_fluid_audio_driver here: FluidSynth is run in pull mode and its
// output is mixed by mixer.go, not sent to a device of its own.
type fluidAPI struct {
	newSettings    func() uintptr
	newSynth       func(uintptr) uintptr
	setNum         func(uintptr, string, float64) int32
	setInt         func(uintptr, string, int32) int32
	sfLoad         func(uintptr, string, int32) int32
	noteOn         func(uintptr, int32, int32, int32) int32
	noteOff        func(uintptr, int32, int32) int32
	cc             func(uintptr, int32, int32, int32) int32
	programChange  func(uintptr, int32, int32) int32
	pitchBend      func(uintptr, int32, int32) int32
	systemReset    func(uintptr) int32 // optional — nil if the lib lacks it
	writeS16       func(uintptr, int32, []int16, int32, int32, []int16, int32, int32) int32
	deleteSynth    func(uintptr) int32
	deleteSettings func(uintptr)
}

// MusicPlayer streams a decoded MUS track to a FluidSynth instance on
// Linux. FluidSynth is loaded at runtime via purego and driven in *pull*
// mode: it never opens its own audio device (that second PipeWire client,
// on a thread that could not get real-time priority, was the stutter). The
// embedded musicLoop feeds MIDI events on the MUS schedule; the shared
// mixer (mixer.go) pulls rendered stereo PCM from the synth inside its own
// output callback — the "render the synth in the audio callback"
// arrangement PrBoom uses. When libfluidsynth or a soundfont is missing,
// NewMusicPlayer returns an error and the engine simply runs without music.
type MusicPlayer struct {
	musicLoop

	api   fluidAPI
	mixer *mixer

	settings uintptr
	synth    uintptr
}

// NewMusicPlayer joins the shared mixer, loads libfluidsynth, and stands up
// a synth with a GM soundfont. A purego binding fault is recovered and
// returned as an ordinary error rather than crashing the engine.
func NewMusicPlayer() (m *MusicPlayer, err error) {
	defer func() {
		if r := recover(); r != nil {
			if m != nil {
				m.teardown()
			}
			m, err = nil, fmt.Errorf("audio: fluidsynth: %v", r)
		}
	}()

	mx, aerr := sharedAudio()
	if aerr != nil {
		return nil, aerr
	}

	handle, derr := dlopenFirst("libfluidsynth.so.3", "libfluidsynth.so.2", "libfluidsynth.so")
	if derr != nil {
		return nil, fmt.Errorf("audio: libfluidsynth not available (install libfluidsynth3 for music): %w", derr)
	}
	sf := findSoundFontLinux()
	if sf == "" {
		return nil, fmt.Errorf("audio: no General MIDI soundfont found — install fluid-soundfont-gm (or timgm6mb-soundfont), or set %s=/path/to.sf2", soundFontEnv)
	}

	for _, sym := range []string{
		"new_fluid_settings", "new_fluid_synth", "fluid_settings_setnum",
		"fluid_settings_setint", "fluid_synth_sfload", "fluid_synth_noteon",
		"fluid_synth_noteoff", "fluid_synth_cc", "fluid_synth_program_change",
		"fluid_synth_pitch_bend", "fluid_synth_write_s16",
		"delete_fluid_synth", "delete_fluid_settings",
	} {
		if _, e := purego.Dlsym(handle, sym); e != nil {
			return nil, fmt.Errorf("audio: libfluidsynth is missing %s: %w", sym, e)
		}
	}

	m = &MusicPlayer{mixer: mx}
	a := &m.api
	purego.RegisterLibFunc(&a.newSettings, handle, "new_fluid_settings")
	purego.RegisterLibFunc(&a.newSynth, handle, "new_fluid_synth")
	purego.RegisterLibFunc(&a.setNum, handle, "fluid_settings_setnum")
	purego.RegisterLibFunc(&a.setInt, handle, "fluid_settings_setint")
	purego.RegisterLibFunc(&a.sfLoad, handle, "fluid_synth_sfload")
	purego.RegisterLibFunc(&a.noteOn, handle, "fluid_synth_noteon")
	purego.RegisterLibFunc(&a.noteOff, handle, "fluid_synth_noteoff")
	purego.RegisterLibFunc(&a.cc, handle, "fluid_synth_cc")
	purego.RegisterLibFunc(&a.programChange, handle, "fluid_synth_program_change")
	purego.RegisterLibFunc(&a.pitchBend, handle, "fluid_synth_pitch_bend")
	purego.RegisterLibFunc(&a.writeS16, handle, "fluid_synth_write_s16")
	purego.RegisterLibFunc(&a.deleteSynth, handle, "delete_fluid_synth")
	purego.RegisterLibFunc(&a.deleteSettings, handle, "delete_fluid_settings")
	if _, e := purego.Dlsym(handle, "fluid_synth_system_reset"); e == nil {
		purego.RegisterLibFunc(&a.systemReset, handle, "fluid_synth_system_reset")
	}

	m.settings = a.newSettings()
	if m.settings == 0 {
		return nil, fmt.Errorf("audio: new_fluid_settings failed")
	}
	a.setNum(m.settings, "synth.sample-rate", float64(mixRate))
	a.setNum(m.settings, "synth.gain", 0.8) // FluidSynth's 0.2 default is very quiet
	a.setInt(m.settings, "synth.cpu-cores", 1)

	m.synth = a.newSynth(m.settings)
	if m.synth == 0 {
		m.teardown()
		return nil, fmt.Errorf("audio: new_fluid_synth failed")
	}
	if a.sfLoad(m.synth, sf, 1) < 0 {
		m.teardown()
		return nil, fmt.Errorf("audio: could not load soundfont %s", sf)
	}

	m.musicLoop.send = m.send
	m.musicLoop.silence = m.allNotesOff
	m.musicLoop.reset = m.resetSynth
	return m, nil
}

// render is handed to the mixer: it fills an interleaved stereo int16 block
// straight from the synth. Called from the audio thread while the mixer
// holds its lock, so it is serialised against Stop's setMusicRender(nil).
func (m *MusicPlayer) render(buf []int16) {
	if len(buf) < 2 {
		return
	}
	m.api.writeS16(m.synth, int32(len(buf)/2), buf, 0, 2, buf, 1, 2)
}

// Play installs the mixer's music-render hook and (re)starts the event
// loop. Switching tracks and Stop wait for the previous loop goroutine to
// finish first (see musicLoop), so no goroutine is left driving the synth
// after it is torn down.
func (m *MusicPlayer) Play(events []wad.MusEvent) {
	m.mixer.setMusicRender(m.render) // the audio thread starts pulling the synth
	m.restart(events)
}

// Stop halts playback and releases FluidSynth. halt blocks until the loop
// goroutine has exited (after its all-notes-off); setMusicRender(nil) then
// fences the audio thread out of the synth before teardown deletes it.
func (m *MusicPlayer) Stop() {
	m.halt()
	m.mixer.setMusicRender(nil)
	m.teardown()
}

func (m *MusicPlayer) send(e wad.MusEvent) {
	ch := int32(e.Channel)
	switch e.Type {
	case wad.MusNoteOff:
		m.api.noteOff(m.synth, ch, int32(e.Data1))
	case wad.MusNoteOn:
		m.api.noteOn(m.synth, ch, int32(e.Data1), int32(e.Data2))
	case wad.MusPitchBend:
		m.api.pitchBend(m.synth, ch, int32(e.Bend14)) // FluidSynth wants the raw 14-bit value
	case wad.MusControlChange:
		m.api.cc(m.synth, ch, int32(e.Data1), int32(e.Data2))
	case wad.MusProgramChange:
		m.api.programChange(m.synth, ch, int32(e.Data1))
	}
}

func (m *MusicPlayer) allNotesOff() {
	for ch := int32(0); ch < 16; ch++ {
		m.api.cc(m.synth, ch, 0x7B, 0) // MIDI CC 123: all notes off
	}
}

// resetSynth silences hanging notes and resets controllers before a new
// track — otherwise a switch can leave a note droning or a pitch bend
// applied.
func (m *MusicPlayer) resetSynth() {
	if m.api.systemReset != nil {
		m.api.systemReset(m.synth)
		return
	}
	for ch := int32(0); ch < 16; ch++ {
		m.api.cc(m.synth, ch, 0x7B, 0) // all notes off
		m.api.cc(m.synth, ch, 0x79, 0) // reset all controllers
	}
}

// teardown deletes the FluidSynth objects. Safe to call more than once and
// on a partially-constructed player.
func (m *MusicPlayer) teardown() {
	if m.synth != 0 {
		m.api.deleteSynth(m.synth)
		m.synth = 0
	}
	if m.settings != 0 {
		m.api.deleteSettings(m.settings)
		m.settings = 0
	}
}

func dlopenFirst(names ...string) (uintptr, error) {
	var last error
	for _, n := range names {
		h, err := purego.Dlopen(n, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err == nil && h != 0 {
			return h, nil
		}
		if err != nil {
			last = err
		}
	}
	if last == nil {
		last = fmt.Errorf("libfluidsynth.so not found")
	}
	return 0, last
}

// findSoundFontLinux resolves a GM .sf2: $TPF_SOUNDFONT if it is set and
// points at a file, otherwise the first existing entry in the common
// distro soundfont-package paths, else "".
func findSoundFontLinux() string {
	if p := os.Getenv(soundFontEnv); p != "" {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
		return "" // set but unusable — surface the "not found" hint
	}
	for _, p := range soundFontPaths {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	return ""
}
