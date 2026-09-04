package engine

import (
	"log"
	"math"
	"strconv"
	"strings"

	"twopointfive/assets"
	"twopointfive/engine/animdefs"
)

// ANIMDEFS glue. engine/animdefs is the clean-room text parser; this file
// gathers the lump(s) from the WAD + mounted mods, turns `flat` / `texture`
// blocks into this package's animDef rows (feeding buildAnimDefs), and
// bakes `warp` / `warp2` into swap-frame animation groups (see animwarp.go).

// animDefsLumpNames are the lump names ANIMDEFS content lives in.
var animDefsLumpNames = map[string]bool{"ANIMDEFS": true}

// parsedAnimDefs parses and merges every ANIMDEFS lump once, caching the
// result on the Game (nil when none exists).
func (g *Game) parsedAnimDefs() *animdefs.AnimDefs {
	if g.animDefsProbed {
		return g.animDefsCache
	}
	g.animDefsProbed = true

	var parts []*animdefs.AnimDefs
	if g.WAD != nil {
		for i := range g.WAD.Entries {
			if animDefsLumpNames[g.WAD.Entries[i].Name] {
				parts = append(parts, animdefs.Parse(g.WAD.Lump(i)))
			}
		}
	}
	if g.mods != nil {
		for name := range animDefsLumpNames {
			for _, b := range g.mods.Lumps(name) {
				parts = append(parts, animdefs.Parse(b))
			}
		}
	}
	if len(parts) == 0 {
		return nil
	}
	merged := animdefs.Merge(parts...)
	for _, w := range merged.Warnings {
		log.Printf("%s", w)
	}
	g.animDefsCache = merged
	return merged
}

// animDefsFromANIMDEFS converts the parsed `flat` / `texture` blocks into
// animDef rows. A `range` short form becomes a plain uniform-speed range; a
// `pic` list becomes an explicit frame list with per-frame durations (and
// is ping-ponged here when `oscillate` was given).
func (g *Game) animDefsFromANIMDEFS() []animDef {
	ad := g.parsedAnimDefs()
	if ad == nil || len(ad.Defs) == 0 {
		return nil
	}
	var out []animDef
	for _, d := range ad.Defs {
		if d.RangeLast != "" {
			sp := d.RangeTics
			if sp < 1 {
				sp = 8
			}
			out = append(out, animDef{
				isTexture: d.Texture,
				first:     strings.ToUpper(d.Name),
				last:      strings.ToUpper(d.RangeLast),
				speed:     sp,
			})
			continue
		}

		basePfx, baseDigits := splitTrailingDigits(strings.ToUpper(d.Name))
		baseN, _ := strconv.Atoi(baseDigits)
		names := make([]string, 0, len(d.Frames))
		durs := make([]int, 0, len(d.Frames))
		for _, f := range d.Frames {
			nm := strings.ToUpper(f.Name)
			if isAllDigits(f.Name) && basePfx != "" {
				// `pic N` — N is a 1-based offset from the base texture's
				// own trailing number (ZDoom's numeric pic form).
				off, _ := strconv.Atoi(f.Name)
				nm = basePfx + leftPad(strconv.Itoa(baseN+off-1), len(baseDigits))
			}
			t := f.Tics
			if t <= 0 {
				t = (f.RandLo + f.RandHi) / 2
			}
			if t < 1 {
				t = 1
			}
			names = append(names, nm)
			durs = append(durs, t)
		}
		if len(names) < 2 {
			continue
		}
		if d.Oscillate && len(names) >= 3 {
			for i := len(names) - 2; i >= 1; i-- {
				names = append(names, names[i])
				durs = append(durs, durs[i])
			}
		}
		out = append(out, animDef{
			isTexture: d.Texture,
			first:     names[0],
			last:      names[len(names)-1],
			speed:     durs[0],
			explicit:  names,
			durs:      durs,
		})
	}
	return out
}

// initWarpAnims bakes each ANIMDEFS `warp` / `warp2` texture into a
// warpPhases-long cycle of pre-distorted frames, injects them into the
// shared texture resolver, and registers a group keyed by the base name so
// initTexAnims's sector/sidedef scan picks the surface up like any other
// animation. Needs the renderer's texture resolver, so it's a no-op in the
// headless tests that build a Game by hand.
func (g *Game) initWarpAnims() {
	ad := g.parsedAnimDefs()
	if ad == nil || len(ad.Warps) == 0 || g.Raster == nil {
		return
	}
	tex := g.Raster.Textures()
	if tex == nil {
		return
	}

	baked := 0
	for _, w := range ad.Warps {
		if baked >= warpDefLimit {
			log.Printf("animdefs: warp limit (%d) reached — ignoring the rest", warpDefLimit)
			break
		}
		base := strings.ToUpper(w.Name)
		var src *assets.RGBA
		var ok bool
		if w.Texture {
			src, ok = tex.WallTexture(base)
		} else {
			src, ok = tex.Flat(base)
		}
		if !ok || src == nil || src.Width <= 0 || src.Height <= 0 {
			continue
		}

		tics := 2 // gentle default swim (~1.8s per full cycle at warpPhases=32)
		if w.Speed > 0 {
			if t := int(math.Round(2.0 / w.Speed)); t >= 1 {
				tics = t
			} else {
				tics = 1
			}
		}

		frames := make([]string, warpPhases)
		for p := 0; p < warpPhases; p++ {
			name := "~WRP" + strconv.Itoa(baked) + "_" + leftPad(strconv.Itoa(p), 2)
			tex.Inject(name, warpImage(src, p, w.Wide), !w.Texture)
			frames[p] = name
		}
		grp := &texAnimGroup{frames: frames, speed: tics}
		dst := g.flatAnimByName
		if w.Texture {
			dst = g.wallAnimByName
		}
		for i, f := range frames {
			dst[f] = texAnimRef{grp: grp, idx: i}
		}
		dst[base] = texAnimRef{grp: grp, idx: 0} // the authored name -> phase 0
		baked++
	}
	if baked > 0 {
		log.Printf("animdefs: baked %d warp texture(s), %d phases each", baked, warpPhases)
	}
}

// animLiveHash is an FNV-1a hash of every animated surface's current live
// texture name. It changes exactly on the tics an animation steps a frame,
// which is how the hardware path knows to rebuild its otherwise-static
// geometry (worldGeomChanged only watches sector heights). Call after
// animateFlats / animateWalls have run for the tic.
func (g *Game) animLiveHash() uint64 {
	const (
		off   = uint64(1469598103934665603)
		prime = uint64(1099511628211)
	)
	h := off
	mix := func(s string) {
		for i := 0; i < len(s); i++ {
			h ^= uint64(s[i])
			h *= prime
		}
		h ^= '|'
		h *= prime
	}
	for i := range g.animBaseFloor {
		if g.animBaseFloor[i] != "" {
			mix(g.Level.Sectors[i].FloorTexture)
		}
		if g.animBaseCeil[i] != "" {
			mix(g.Level.Sectors[i].CeilingTexture)
		}
	}
	for _, s := range g.wallAnimSlots {
		sd := &g.Level.Sidedefs[s.side]
		switch s.slot {
		case 0:
			mix(sd.UpperTexture)
		case 1:
			mix(sd.MiddleTexture)
		case 2:
			mix(sd.LowerTexture)
		}
	}
	return h
}

// animFrameChanged reports whether any animated texture stepped since the
// last call (always true on the very first call with animations present, to
// force one initial rebuild).
func (g *Game) animFrameChanged() bool {
	if len(g.flatAnimByName) == 0 && len(g.wallAnimByName) == 0 {
		return false
	}
	h := g.animLiveHash()
	if !g.lastAnimHashSet || h != g.lastAnimHash {
		g.lastAnimHash = h
		g.lastAnimHashSet = true
		return true
	}
	return false
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
