package engine

import (
	"log"

	"twopointfive/assets"
	"twopointfive/assets/vfs"
	"twopointfive/engine/texturedefs"
	"twopointfive/wad"
)

// ZDoom TEXTURES glue. engine/texturedefs is the clean-room text parser;
// this gathers the lump(s) from the WAD + mounted mods, converts the
// parsed definitions to assets.ZDDef, and registers them with the shared
// texture resolver so a TEXTURES entry shadows the WAD's own decode. Called
// once from NewGame, before spawnSpecials / initTexAnims (a warp or
// animation may target a TEXTURES-defined name).

var textureDefsLumpNames = map[string]bool{"TEXTURES": true}

// loadTextureDefs parses every TEXTURES lump (WAD entries in file order,
// then mods) and hands them to the renderer's texture resolver.
func (g *Game) loadTextureDefs(w *wad.WAD, mods *vfs.FS) {
	if g.Raster == nil {
		return
	}
	tex := g.Raster.Textures()
	if tex == nil {
		return
	}

	var defs []texturedefs.Def
	var warns []string
	add := func(b []byte) {
		d, ws := texturedefs.Parse(b)
		defs = append(defs, d...)
		warns = append(warns, ws...)
	}
	if w != nil {
		for i := range w.Entries {
			if textureDefsLumpNames[w.Entries[i].Name] {
				add(w.Lump(i))
			}
		}
	}
	if mods != nil {
		for name := range textureDefsLumpNames {
			for _, b := range mods.Lumps(name) {
				add(b)
			}
		}
	}
	if len(defs) == 0 {
		return
	}
	for _, wmsg := range warns {
		log.Printf("%s", wmsg)
	}

	var walls, flats, sprites []assets.ZDDef
	for _, d := range defs {
		zd := assets.ZDDef{
			Name: d.Name, Width: d.Width, Height: d.Height,
			XScale: d.XScale, YScale: d.YScale,
			OffsetX: d.OffsetX, OffsetY: d.OffsetY,
			WorldPanning: d.WorldPanning, NullTexture: d.NullTexture,
		}
		for _, p := range d.Patches {
			zd.Patches = append(zd.Patches, assets.ZDPatch{
				Name: p.Name, X: p.X, Y: p.Y,
				FlipX: p.FlipX, FlipY: p.FlipY, Rotate: p.Rotate,
				Alpha: p.Alpha, Style: p.Style, UseOffsets: p.UseOffsets,
				Blend: p.BlendRGBA, HasBlend: p.HasBlend,
			})
		}
		switch d.Kind {
		case texturedefs.KindFlat:
			flats = append(flats, zd)
		case texturedefs.KindSprite, texturedefs.KindGraphic:
			sprites = append(sprites, zd)
		default:
			walls = append(walls, zd)
		}
	}
	tex.AddTextureDefs(walls, flats, sprites)
	log.Printf("textures: registered %d wall / %d flat / %d sprite TEXTURES definition(s)",
		len(walls), len(flats), len(sprites))
}
