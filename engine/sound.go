package engine

import (
	"archive/zip"
	"io"
	"path"
	"strings"

	"twopointfive/assets"
	"twopointfive/wad"
)

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
	orig := name
	if g.heretic {
		if h, ok := hereticSfx[name]; ok {
			name = h // Doom DS* lump -> Heretic's own (no DS prefix)
		}
	}
	if s, ok := g.soundCache[name]; ok {
		return s
	}
	var snd *wad.Sound
	raw, ok := g.WAD.Find(name)
	if !ok {
		// A real-3D WAD carries every arsenal's weapon sounds (cmd/wad3d),
		// under the post-remap name or the original DS* one.
		if raw, ok = g.embeddedSound(name); !ok && orig != name {
			raw, ok = g.embeddedSound(orig)
		}
	}
	if !ok && g.AltWAD != nil {
		// switchWeapons: a weapon fire lump the played WAD lacks — the other
		// game's IWAD has it, under the post-remap name or (for a Doom gun in
		// a Heretic session, where the hereticSfx remap above didn't fire and
		// left "DS...") the original.
		if raw, ok = g.AltWAD.Find(name); !ok && orig != name {
			raw, ok = g.AltWAD.Find(orig)
		}
	}
	if ok {
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

// embeddedSound returns a sound lump from the WAD's TPFWEAPN container
// (wad.TPFWeaponsLump), ok=false when the WAD has no such container or it
// lacks name.
func (g *Game) embeddedSound(name string) ([]byte, bool) {
	if !g.embeddedSoundsProbed {
		g.embeddedSoundsProbed = true
		if zr, ok := g.WAD.ZipLump(wad.TPFWeaponsLump); ok {
			g.embeddedSounds = map[string]*zip.File{}
			for _, f := range zr.File {
				dir, file := path.Split(f.Name)
				if strings.TrimSuffix(dir, "/") == assets.EmbeddedSoundsDir {
					g.embeddedSounds[strings.ToUpper(strings.TrimSuffix(file, path.Ext(file)))] = f
				}
			}
		}
	}
	f, ok := g.embeddedSounds[name]
	if !ok {
		return nil, false
	}
	rc, err := f.Open()
	if err != nil {
		return nil, false
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	return b, err == nil
}
