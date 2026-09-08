package audio

import (
	"log"

	"twopointfive/wad"
)

// Setup brings up the shared sound-effect device and music player once at
// process start — see cmd/engine and cmd/isoengine's run(): both hand these
// to every play session rather than recreating them (NewDevice/
// NewMusicPlayer both join one shared, lazily-created mixer/output stream,
// see mixer.go's sharedAudio, and NewMusicPlayer's FluidSynth setup on
// Linux is real, non-instant work) — and, for the MusicPlayer, tear it down
// (Stop) only once, when the process actually exits: Stop deletes the
// underlying FluidSynth synth outright, not a pause, so it must not run on
// every trip back through the pre-game launcher.
//
// A nil return for either just means that subsystem is unavailable
// (already logged here, prefixed with logPrefix so it's clear which
// binary's log line it is); callers treat a nil Device/MusicPlayer as "no
// sound effects" / "no music" respectively.
func Setup(logPrefix string) (*Device, *MusicPlayer) {
	dev, err := NewDevice()
	if err != nil {
		log.Printf("%s: sound effects unavailable: %v", logPrefix, err)
		dev = nil
	}
	music, err := NewMusicPlayer()
	if err != nil {
		log.Printf("%s: music unavailable: %v", logPrefix, err)
		music = nil
	}
	return dev, music
}

// PlayLevelMusic finds mapName's music lump and starts it looping on music:
// a UMAPINFO override (override, "" if the map has none) is tried first,
// then the conventional name for the map's format (wad.MusicLumpName —
// "D_E1M1" for ExMy, "D_RUNNIN"-style for Doom II MAPxx), then "D_"+mapName
// as a fallback (some PWADs name Doom II music that way); if none of those
// resolve to a lump, or music is nil (unavailable — see Setup), it silences
// whatever was playing instead. Play(nil), not Stop, is the safe way to
// silence a shared MusicPlayer mid-process — see MusicPlayer.Stop's doc
// comment on why Stop is a one-way teardown.
func PlayLevelMusic(w *wad.WAD, music *MusicPlayer, override, mapName, logPrefix string) {
	if music == nil {
		return
	}
	candidates := []string{override, wad.MusicLumpName(mapName), "D_" + mapName}
	for _, lumpName := range candidates {
		if lumpName == "" {
			continue
		}
		raw, ok := w.Find(lumpName)
		if !ok {
			continue
		}
		events, err := wad.DecodeMus(raw)
		if err != nil {
			log.Printf("%s: decode music %q: %v", logPrefix, lumpName, err)
			music.Play(nil)
			return
		}
		log.Printf("%s: playing music %s (%d events)", logPrefix, lumpName, len(events))
		music.Play(events)
		return
	}
	log.Printf("%s: no music lump found for %s (tried %v)", logPrefix, mapName, candidates)
	music.Play(nil)
}
