//go:build windows && cgo

package audio

/*
#include <windows.h>
#include <stdlib.h>

// FluidSynth's C API is resolved from the DLL at runtime, so nothing about
// libfluidsynth is needed at build time. Only the entry points this player
// uses are declared. Every "int" below is a C int; note fsw_setnum takes a
// double — the reason this path is cgo rather than purego, whose Windows
// amd64 fallback cannot pass floating-point arguments.
//
// There is no new_fluid_audio_driver here: like the Linux player
// (midi_linux.go) FluidSynth is run in PULL mode — the shared software
// mixer (mixer.go) calls fluid_synth_write_s16 from its own output
// callback and sums the result with the sound effects, so music and
// effects share one stream, one clock and one buffer.
typedef void *(*p_new0)(void);
typedef void  (*p_del1)(void *);
typedef int   (*p_del1i)(void *);
typedef int   (*p_setnum)(void *, const char *, double);
typedef int   (*p_setint)(void *, const char *, int);
typedef int   (*p_setstr)(void *, const char *, const char *);
typedef void *(*p_new1)(void *);
typedef int   (*p_sfload)(void *, const char *, int);
typedef int   (*p_writes16)(void *, int, void *, int, int, void *, int, int);
typedef int   (*p_i3)(void *, int, int, int);
typedef int   (*p_i2)(void *, int, int);
typedef int   (*p_i1)(void *);

static struct {
	HMODULE  lib;
	p_new0   new_settings;
	p_del1   del_settings;
	p_setnum setnum;
	p_setint setint;
	p_setstr setstr;
	p_new1   new_synth;
	p_del1i  del_synth;
	p_sfload sfload;
	p_writes16 write_s16;
	p_i3     noteon;
	p_i2     noteoff;
	p_i3     cc;
	p_i2     prog;
	p_i2     bend;
	p_i1     sysreset;
} F;

// fsw_load loads the DLL and resolves the entry points. Returns 1 on
// success, 0 if the library or a required symbol is missing.
static int fsw_load(const char *dll) {
	HMODULE h;
	if (F.lib) return 1;
	h = LoadLibraryA(dll);
	if (!h) return 0;
	F.new_settings = (p_new0)  (void *)GetProcAddress(h, "new_fluid_settings");
	F.del_settings = (p_del1)  (void *)GetProcAddress(h, "delete_fluid_settings");
	F.setnum       = (p_setnum)(void *)GetProcAddress(h, "fluid_settings_setnum");
	F.setint       = (p_setint)(void *)GetProcAddress(h, "fluid_settings_setint");
	F.setstr       = (p_setstr)(void *)GetProcAddress(h, "fluid_settings_setstr");
	F.new_synth    = (p_new1)  (void *)GetProcAddress(h, "new_fluid_synth");
	F.del_synth    = (p_del1i) (void *)GetProcAddress(h, "delete_fluid_synth");
	F.sfload       = (p_sfload)(void *)GetProcAddress(h, "fluid_synth_sfload");
	F.write_s16    = (p_writes16)(void *)GetProcAddress(h, "fluid_synth_write_s16");
	F.noteon       = (p_i3)    (void *)GetProcAddress(h, "fluid_synth_noteon");
	F.noteoff      = (p_i2)    (void *)GetProcAddress(h, "fluid_synth_noteoff");
	F.cc           = (p_i3)    (void *)GetProcAddress(h, "fluid_synth_cc");
	F.prog         = (p_i2)    (void *)GetProcAddress(h, "fluid_synth_program_change");
	F.bend         = (p_i2)    (void *)GetProcAddress(h, "fluid_synth_pitch_bend");
	F.sysreset     = (p_i1)    (void *)GetProcAddress(h, "fluid_synth_system_reset");
	if (!F.new_settings || !F.del_settings || !F.setnum || !F.setstr ||
	    !F.new_synth || !F.del_synth || !F.sfload || !F.write_s16 ||
	    !F.noteon || !F.noteoff || !F.cc || !F.prog || !F.bend) {
		FreeLibrary(h);
		return 0;
	}
	F.lib = h;
	return 1;
}

static void *fsw_new_settings(void)                            { return F.new_settings(); }
static void  fsw_del_settings(void *s)                         { F.del_settings(s); }
static int   fsw_setnum(void *s, const char *k, double v)      { return F.setnum(s, k, v); }
static int   fsw_setint(void *s, const char *k, int v)         { return F.setint ? F.setint(s, k, v) : 0; }
static void *fsw_new_synth(void *s)                            { return F.new_synth(s); }
static void  fsw_del_synth(void *y)                            { F.del_synth(y); }
static int   fsw_sfload(void *y, const char *p, int r)         { return F.sfload(y, p, r); }
static int   fsw_write_s16(void *y, int n, void *buf)          { return F.write_s16(y, n, buf, 0, 2, buf, 1, 2); }
static void  fsw_noteon(void *y, int c, int k, int v)          { F.noteon(y, c, k, v); }
static void  fsw_noteoff(void *y, int c, int k)               { F.noteoff(y, c, k); }
static void  fsw_cc(void *y, int c, int n, int v)              { F.cc(y, c, n, v); }
static void  fsw_prog(void *y, int c, int p)                   { F.prog(y, c, p); }
static void  fsw_bend(void *y, int c, int v)                   { F.bend(y, c, v); }
static int   fsw_has_sysreset(void)                            { return F.sysreset != 0; }
static void  fsw_sysreset(void *y)                             { if (F.sysreset) F.sysreset(y); }
*/
import "C"

import (
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"twopointfive/wad"
)

// fluidDLLs are tried in order; the first that loads (with all required
// entry points) wins. The official FluidSynth Windows release ships
// libfluidsynth-3.dll plus its dependency DLLs — drop the whole set next to
// the executable.
var fluidDLLs = []string{
	"libfluidsynth-3.dll",
	"libfluidsynth-2.dll",
	"libfluidsynth.dll",
	"fluidsynth.dll",
}

// fluidMusic plays MUS tracks through FluidSynth on Windows. The DLL is
// loaded at runtime; FluidSynth opens no audio device of its own — the
// shared mixer (mixer.go) pulls a rendered stereo block from it inside its
// output callback (fluid_synth_write_s16), the arrangement PrBoom uses.
// The embedded musicLoop feeds the MIDI events on the MUS schedule.
type fluidMusic struct {
	musicLoop
	mixer    *mixer
	settings unsafe.Pointer
	synth    unsafe.Pointer
}

// newFluidMusic returns the backend and the loaded soundfont's base name,
// or an error (music then falls back to winmm — see midi_windows.go).
func newFluidMusic() (musicBackend, string, error) {
	loaded := false
	for _, name := range fluidDLLs {
		c := C.CString(name)
		ok := C.fsw_load(c) != 0
		C.free(unsafe.Pointer(c))
		if ok {
			loaded = true
			break
		}
	}
	if !loaded {
		return nil, "", fmt.Errorf("libfluidsynth DLL not found (drop the FluidSynth release DLLs next to the exe)")
	}

	mx, err := sharedAudio()
	if err != nil {
		return nil, "", err
	}

	sf := findSoundFont(windowsSoundFonts())
	if sf == "" {
		return nil, "", fmt.Errorf("no soundfont found — put a .sf2 (e.g. FluidR3_GM.sf2) next to the exe or in a soundfonts/ folder, or set %s", soundFontEnv)
	}

	m := &fluidMusic{mixer: mx}

	m.settings = C.fsw_new_settings()
	if m.settings == nil {
		return nil, "", fmt.Errorf("new_fluid_settings failed")
	}
	m.setNum("synth.sample-rate", float64(mixRate)) // must match the mixer's rate
	m.setNum("synth.gain", 0.8)                     // FluidSynth's 0.2 default is very quiet
	m.setInt("synth.cpu-cores", 1)

	m.synth = C.fsw_new_synth(m.settings)
	if m.synth == nil {
		m.teardown()
		return nil, "", fmt.Errorf("new_fluid_synth failed")
	}

	csf := C.CString(sf)
	rc := C.fsw_sfload(m.synth, csf, C.int(1))
	C.free(unsafe.Pointer(csf))
	if rc < 0 {
		m.teardown()
		return nil, "", fmt.Errorf("could not load soundfont %s", sf)
	}

	m.musicLoop.send = m.send
	m.musicLoop.silence = m.allNotesOff
	m.musicLoop.reset = m.resetSynth
	return m, filepath.Base(sf), nil
}

func (m *fluidMusic) setNum(key string, v float64) {
	c := C.CString(key)
	C.fsw_setnum(m.settings, c, C.double(v))
	C.free(unsafe.Pointer(c))
}

func (m *fluidMusic) setInt(key string, v int) {
	c := C.CString(key)
	C.fsw_setint(m.settings, c, C.int(v))
	C.free(unsafe.Pointer(c))
}

// render is handed to the mixer: it fills an interleaved stereo int16 block
// straight from the synth. Called from the audio thread while the mixer
// holds its lock, so it is serialised against Stop's setMusicRender(nil).
func (m *fluidMusic) render(buf []int16) {
	if len(buf) < 2 {
		return
	}
	C.fsw_write_s16(m.synth, C.int(len(buf)/2), unsafe.Pointer(&buf[0]))
}

// Play installs the mixer's music-render hook and (re)starts the event
// loop. restart/halt (musicLoop) wait for the previous goroutine first.
func (m *fluidMusic) Play(events []wad.MusEvent) {
	m.mixer.setMusicRender(m.render)
	m.restart(events)
}

// Stop halts playback and releases FluidSynth. halt blocks until the loop
// goroutine has exited; setMusicRender(nil) then fences the audio thread
// out of the synth before teardown deletes it.
func (m *fluidMusic) Stop() {
	m.halt()
	m.mixer.setMusicRender(nil)
	m.teardown()
}

// teardown deletes the FluidSynth objects. Safe to call more than once and
// partway through.
func (m *fluidMusic) teardown() {
	if m.synth != nil {
		C.fsw_del_synth(m.synth)
		m.synth = nil
	}
	if m.settings != nil {
		C.fsw_del_settings(m.settings)
		m.settings = nil
	}
}

func (m *fluidMusic) send(e wad.MusEvent) {
	ch := C.int(e.Channel)
	switch e.Type {
	case wad.MusNoteOff:
		C.fsw_noteoff(m.synth, ch, C.int(e.Data1))
	case wad.MusNoteOn:
		C.fsw_noteon(m.synth, ch, C.int(e.Data1), C.int(e.Data2))
	case wad.MusPitchBend:
		C.fsw_bend(m.synth, ch, C.int(e.Bend14)) // FluidSynth wants the raw 14-bit value
	case wad.MusControlChange:
		C.fsw_cc(m.synth, ch, C.int(e.Data1), C.int(e.Data2))
	case wad.MusProgramChange:
		C.fsw_prog(m.synth, ch, C.int(e.Data1))
	}
}

func (m *fluidMusic) allNotesOff() {
	for ch := 0; ch < 16; ch++ {
		C.fsw_cc(m.synth, C.int(ch), C.int(0x7B), C.int(0)) // CC 123: all notes off
	}
}

func (m *fluidMusic) resetSynth() {
	if C.fsw_has_sysreset() != 0 {
		C.fsw_sysreset(m.synth)
		return
	}
	for ch := 0; ch < 16; ch++ {
		C.fsw_cc(m.synth, C.int(ch), C.int(0x7B), C.int(0)) // all notes off
		C.fsw_cc(m.synth, C.int(ch), C.int(0x79), C.int(0)) // reset all controllers
	}
}

// windowsSoundFonts lists .sf2 candidates: common file names in the
// executable's directory and a few parents, a soundfonts/ (and
// assets/soundfonts/) subdir of each, and the working directory — the same
// shape as how config.json and the asset packs are located.
func windowsSoundFonts() []string {
	names := []string{
		"soundfont.sf2", "default.sf2",
		"FluidR3_GM.sf2", "GeneralUser GS.sf2", "gm.sf2",
	}
	var dirs []string
	seen := map[string]bool{}
	add := func(d string) {
		for _, sub := range []string{"", "soundfonts", filepath.Join("assets", "soundfonts")} {
			p := filepath.Join(d, sub)
			if !seen[p] {
				seen[p] = true
				dirs = append(dirs, p)
			}
		}
	}
	if exe, err := os.Executable(); err == nil {
		d := filepath.Dir(exe)
		for i := 0; i < 4; i++ {
			add(d)
			parent := filepath.Dir(d)
			if parent == d {
				break
			}
			d = parent
		}
	}
	if wd, err := os.Getwd(); err == nil {
		add(wd)
	}
	var out []string
	for _, d := range dirs {
		for _, n := range names {
			out = append(out, filepath.Join(d, n))
		}
	}
	return out
}
