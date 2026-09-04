package engine

import (
	"log"

	"twopointfive/assets/vfs"
	"twopointfive/engine/gldefs"
	"twopointfive/wad"
)

// GLDEFS support — data-driven dynamic lights. GZDoom/UZDoom-family WADs
// ship a GLDEFS lump that defines named lights and attaches them to actor
// sprite frames; this replaces hand-tuned Go tables (maplights.go's
// decoLights) whenever a loaded WAD provides one. Vanilla IWADs have no
// GLDEFS lump, so nothing changes for them — the built-in tables remain the
// fallback.
//
// The lump parser lives in engine/gldefs (a clean-room reader of the text
// format, no GZDoom code). This file is only the WAD glue.

// gldefsLumpNames are the lump names that carry GLDEFS content, most
// specific last so a later one wins on a name clash. GZDoom also recognises
// game-specific variants; DOOMDEFS is the one relevant to a Doom IWAD.
var gldefsLumpNames = map[string]bool{
	"GLDEFS":   true,
	"DOOMDEFS": true,
}

// loadGLDefs gathers every GLDEFS-family lump — first from w (in file order,
// so later PWADs layer over the IWAD), then from the mounted mods (mods may
// be nil), so a GLDEFS-only .pk3 in assets/mods/ lights a stock IWAD.
// Returns nil when nothing provides one.
func loadGLDefs(w *wad.WAD, mods *vfs.FS) *gldefs.Defs {
	var blob []byte
	count := 0
	appendBlob := func(b []byte) {
		blob = append(blob, b...)
		blob = append(blob, '\n')
		count++
	}

	if w != nil {
		for i := range w.Entries {
			if gldefsLumpNames[w.Entries[i].Name] {
				appendBlob(w.Lump(i))
			}
		}
	}
	if mods != nil {
		for name := range gldefsLumpNames {
			for _, b := range mods.Lumps(name) {
				appendBlob(b)
			}
		}
	}
	if count == 0 {
		return nil
	}

	include := func(name string) ([]byte, bool) {
		if w != nil {
			if b, ok := w.Find(name); ok {
				return b, true
			}
		}
		if mods != nil {
			return mods.Lump(name)
		}
		return nil, false
	}
	defs, errs := gldefs.Parse(blob, include)
	for _, e := range errs {
		log.Printf("gldefs: %v", e)
	}
	log.Printf("gldefs: loaded from %d lump(s) — %d light defs, %d sprite attachments",
		count, defs.NumLights(), defs.NumAttachments())
	return defs
}

// gldefStaticLights builds the static map lights a decoration mobj emits per
// its GLDEFS sprite-frame attachment. Returns nil when the loaded WAD has no
// GLDEFS lump, or it attaches nothing to this mobj's current frame — the
// caller then falls back to the built-in decoLights table. firstSeed is the
// wobble seed for the first light produced; successive ones increment.
// setCullR2 is left to buildStaticLights' final pass, like every other entry.
func (g *Game) gldefStaticLights(mo *Mobj, firstSeed int) []staticLight {
	if g.gldefs == nil {
		return nil
	}
	name := spriteNames[mo.Sprite]
	if name == "" {
		return nil
	}
	defs := g.gldefs.Frame(name, mo.Frame)
	if len(defs) == 0 {
		return nil
	}
	out := make([]staticLight, 0, len(defs))
	for _, d := range defs {
		r, gg, b := d.Color[0], d.Color[1], d.Color[2]
		if r == 0 && gg == 0 && b == 0 {
			continue // a black light contributes nothing
		}
		radius := d.Size
		if d.SecondarySize > radius {
			radius = 0.5 * (d.Size + d.SecondarySize) // pulse/flicker: middle of the swing
		}
		if radius <= 1 {
			continue
		}
		inten := float32(1.0)
		if d.Scale > 0 {
			inten = d.Scale
		}
		// GLDEFS offset is x, y(up), z about the actor origin (feet). With no
		// vertical offset, hang the light at mid-height like the built-in
		// decoration table does.
		z := mo.Z + float64(d.Offset[1])
		if d.Offset[1] == 0 {
			z = mo.Z + mo.Height*0.5
		}
		flicker := d.Type == gldefs.Flicker || d.Type == gldefs.Flicker2 || d.Type == gldefs.Pulse
		out = append(out, staticLight{
			x: mo.X + float64(d.Offset[0]), y: mo.Y + float64(d.Offset[2]), z: z,
			r: r, g: gg, b: b,
			baseInten: inten, baseRadius: radius,
			sector: -1, flicker: flicker, seed: firstSeed + len(out),
		})
	}
	return out
}
