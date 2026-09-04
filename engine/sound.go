package engine

import "twopointfive/wad"

// loadSound decodes and caches a DS* sound effect lump by name, returning
// nil (cached) if it doesn't exist or fails to decode, so repeated
// triggers of the same sound (footsteps on a door, rapid weapon fire)
// don't re-parse the lump every time.
func (g *Game) loadSound(name string) *wad.Sound {
	if g.WAD == nil {
		return nil
	}
	if g.soundCache == nil {
		g.soundCache = make(map[string]*wad.Sound)
	}
	if s, ok := g.soundCache[name]; ok {
		return s
	}
	var snd *wad.Sound
	if raw, ok := g.WAD.Find(name); ok {
		if s, err := wad.DecodeDMX(raw); err == nil {
			snd = s
		}
	}
	g.soundCache[name] = snd
	return snd
}

// playSound plays a DS* sound effect lump by name, fire-and-forget. A
// no-op if audio isn't available or the lump can't be found/decoded.
func (g *Game) playSound(name string) {
	if g.sfxHook != nil {
		g.sfxHook(name) // test observation point; never set in a real run
	}
	if g.AudioDevice == nil {
		return
	}
	if s := g.loadSound(name); s != nil {
		g.AudioDevice.PlaySound(s)
	}
}

// playWeaponSound is playSound scaled by g.weaponVolume (config
// weaponVolume) — used for every gun's fire/reload noise and the chainsaw's
// idle whir, so those can be turned down relative to the rest of the SFX.
func (g *Game) playWeaponSound(name string) {
	if g.sfxHook != nil {
		g.sfxHook(name) // test observation point; never set in a real run
	}
	if g.AudioDevice == nil {
		return
	}
	vol := g.weaponVolume
	if vol == 0 {
		vol = 1 // a Game built without config: leave weapon SFX untouched
	}
	if s := g.loadSound(name); s != nil {
		g.AudioDevice.PlaySoundVol(s, vol)
	}
}

// playWorldSound is playSound scaled by g.worldVolume (config worldVolume) —
// used for the level's own machinery (doors, lifts/platforms, moving floors
// and ceilings/crushers, stairs, switch clicks, teleporters). Several of
// those loop while a sector moves, so this lets the background drone be
// turned down without touching weapon, monster or pickup SFX.
func (g *Game) playWorldSound(name string) {
	if g.sfxHook != nil {
		g.sfxHook(name) // test observation point; never set in a real run
	}
	if g.AudioDevice == nil {
		return
	}
	vol := g.worldVolume
	if vol == 0 {
		vol = 1 // a Game built without config: leave world SFX untouched
	}
	if s := g.loadSound(name); s != nil {
		g.AudioDevice.PlaySoundVol(s, vol)
	}
}

// soundDuration returns a DS* lump's playback length in seconds, or 0 if it
// can't be found/decoded. Used to loop the chainsaw's idle whir seamlessly
// (see updateWeapon / idleSoundInterval).
func (g *Game) soundDuration(name string) float64 {
	s := g.loadSound(name)
	if s == nil || s.SampleRate <= 0 {
		return 0
	}
	return float64(len(s.Samples)) / float64(s.SampleRate)
}
