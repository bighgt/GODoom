package engine

import "twopointfive/wad"

// loadSound decodes and caches a DS* sound effect lump by name, returning
// nil (cached) if it doesn't exist or fails to decode, so repeated
// triggers of the same sound (footsteps on a door, rapid weapon fire)
// don't re-parse the lump every time.
func (g *Game) loadSound(name string) *wad.Sound {
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
	if g.AudioDevice == nil {
		return
	}
	if s := g.loadSound(name); s != nil {
		g.AudioDevice.PlaySound(s)
	}
}
