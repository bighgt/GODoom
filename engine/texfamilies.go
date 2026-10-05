package engine

import "twopointfive/wad"

// TextureAnimGroups returns every wall-texture and flat animation w defines
// — the vanilla/BOOM ANIMATED list (or id's built-in table without one)
// plus any ANIMDEFS ranges the WAD itself carries — each as its ordered
// frame list, exactly as initTexAnims would expand them for a game on w.
// For offline tools (cmd/wad3d) that must keep a texture's whole family
// together: a map that references NUKAGE1 shows NUKAGE2/3 too at runtime.
// Mounted mods are not consulted — only what travels inside w.
func TextureAnimGroups(w *wad.WAD) (walls, flats [][]string) {
	g := &Game{WAD: w}
	texOrder, texSet := g.buildTextureOrder()
	for _, d := range g.buildAnimDefs() {
		frames := g.expandAnimDef(d, texOrder, texSet)
		if len(frames) < 2 {
			continue
		}
		if d.isTexture {
			walls = append(walls, frames)
		} else {
			flats = append(flats, frames)
		}
	}
	return walls, flats
}

// SwitchPartner returns the other face of a switch wall texture (SW1xxx <->
// SW2xxx), or ok=false if name isn't a switch — the same pairing
// changeSwitchTexture flips at runtime.
func SwitchPartner(name string) (string, bool) { return switchToggle(name) }
