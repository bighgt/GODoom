package assets

import (
	"strings"

	"twopointfive/wad"
)

// Sprite-frame indexing, ported from id's R_InitSpriteDefs. A sprite lump
// is named PREFIX + frame + rotation, e.g. "TROOA1" (imp frame A, rotation
// 1) or "SHTGA0" (rotation 0 = the same image for every viewing angle). An
// 8-char name like "TROOA2A8" installs one image for frame A rotation 2 and
// the same image, mirrored, for frame A rotation 8 — Doom only stores the
// left-facing halves and flips them.

// spriteFrame holds the up-to-8 rotation images for one animation frame.
type spriteFrame struct {
	rotate bool      // true: 8 distinct rotations in lump[0..7]; false: one image in lump[0]
	lump   [8]string // lump name per rotation
	flip   [8]bool   // draw that rotation mirrored
}

type spriteDef struct {
	frames []spriteFrame
}

// buildSpriteIndex scans the WAD's S_START..S_END (and SS_START..SS_END)
// namespace once and returns prefix -> spriteDef.
func buildSpriteIndex(w *wad.WAD) map[string]*spriteDef {
	defs := map[string]*spriteDef{}

	install := func(prefix string, frame, rot int, lump string, flip bool) {
		if frame < 0 || frame > 28 || rot < 0 || rot > 8 {
			return
		}
		d := defs[prefix]
		if d == nil {
			d = &spriteDef{}
			defs[prefix] = d
		}
		for len(d.frames) <= frame {
			d.frames = append(d.frames, spriteFrame{})
		}
		f := &d.frames[frame]
		if rot == 0 {
			f.rotate = false
			for i := range f.lump {
				f.lump[i] = lump
				f.flip[i] = flip
			}
			return
		}
		f.rotate = true
		f.lump[rot-1] = lump
		f.flip[rot-1] = flip
	}

	inSprites := false
	for _, e := range w.Entries {
		switch e.Name {
		case "S_START", "SS_START":
			inSprites = true
			continue
		case "S_END", "SS_END":
			inSprites = false
			continue
		}
		if !inSprites || e.Size == 0 {
			continue
		}
		n := e.Name
		if len(n) != 6 && len(n) != 8 {
			continue
		}
		prefix := n[0:4]
		install(prefix, frameIndex(n[4]), int(n[5]-'0'), n, false)
		if len(n) == 8 {
			install(prefix, frameIndex(n[6]), int(n[7]-'0'), n, true)
		}
	}
	return defs
}

// frameIndex maps a sprite frame letter to 0-based: A..Z -> 0..25, then
// '[' '\' ']' -> 26..28 (Doom's frames past Z).
func frameIndex(c byte) int {
	switch {
	case c >= 'A' && c <= 'Z':
		return int(c - 'A')
	case c == '[':
		return 26
	case c == '\\':
		return 27
	case c == ']':
		return 28
	}
	return -1
}

// SpriteFrame resolves a thing's current sprite prefix + animation frame +
// viewing rotation (0..7, 0 = facing the viewer) to a decoded image and
// whether to draw it mirrored. ok is false if the WAD has no such sprite.
//
// Not guarded by t.mu (and it lazily writes t.spriteIndex, then calls the
// mutex-taking Sprite): the sprite/HUD pass that calls this runs single-
// threaded, after the parallel BSP walk in raster.Renderer.Render has
// joined. Don't call it from those worker goroutines.
func (t *Textures) SpriteFrame(prefix string, frame, rot int) (im *RGBA, flip, ok bool) {
	if t.spriteIndex == nil {
		t.spriteIndex = buildSpriteIndex(t.w)
	}
	d := t.spriteIndex[strings.ToUpper(prefix)]
	if d == nil || frame < 0 || frame >= len(d.frames) {
		return nil, false, false
	}
	f := &d.frames[frame]
	idx := 0
	if f.rotate {
		idx = rot & 7
	}
	lump := f.lump[idx]
	if lump == "" {
		// A missing rotation — fall back to rotation 1 (front) if present.
		lump = f.lump[0]
		if lump == "" {
			return nil, false, false
		}
	}
	sp, ok := t.Sprite(lump)
	if !ok {
		return nil, false, false
	}
	return sp, f.flip[idx], true
}
