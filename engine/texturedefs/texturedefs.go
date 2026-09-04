// Package texturedefs is a clean-room parser for the ZDoom TEXTURES lump —
// text-defined composite textures / sprites / graphics with explicit
// scale, offset and per-patch flip / rotate / blend. Written from the
// format description on the zdoom wiki; no engine source was consulted.
//
// Grammar handled:
//
//	[optional] <Texture|WallTexture|Sprite|Graphic|Flat> <name> [, <w>, <h>]
//	{
//	    XScale <f>   YScale <f>
//	    Offset <x>, <y>
//	    WorldPanning | NoDecals | NullTexture | NoTrim
//	    <Patch|Sprite|Graphic> <name>, <x>, <y>
//	    {
//	        FlipX | FlipY | UseOffsets
//	        Rotate <90|180|270>
//	        Alpha <f>
//	        Style <Copy|Translucent|Add|Subtract|ReverseSubtract|Modulate|CopyAlpha|Overlay>
//	        Blend <r>,<g>,<b>[,<a>] | Blend <hexcolour>[, <a>]
//	        Translation <...>            (parsed, not applied)
//	    }
//	}
//
// A bare `<name>, <x>, <y>` line inside a texture block with no leading
// keyword is treated as a Patch (some lumps omit it).
package texturedefs

import (
	"strconv"
	"strings"
)

// Kind is which resolver namespace a Def targets.
type Kind int

const (
	KindTexture     Kind = iota // wall texture (composite)
	KindWallTexture             // same namespace as KindTexture here
	KindSprite
	KindGraphic // HUD / menu picture — sprite namespace here
	KindFlat
)


// Patch is one stamped source image within a Def.
type Patch struct {
	Name       string // source lump (a patch, or another texture/sprite/graphic)
	X, Y       int
	FlipX      bool
	FlipY      bool
	Rotate     int     // 0, 90, 180, 270
	Alpha      float64 // 1.0 = opaque
	Style      string  // lower-case; "" == "copy"
	UseOffsets bool    // honour the source patch's own LeftOffset/TopOffset
	BlendRGBA  [4]float64
	HasBlend   bool
}

// Def is one texture / sprite / graphic / flat definition.
type Def struct {
	Kind          Kind
	Name          string
	Width, Height int
	XScale        float64 // 0 == unspecified (caller treats as 1)
	YScale        float64
	OffsetX       int
	OffsetY       int
	WorldPanning  bool
	NoDecals      bool
	NullTexture   bool
	Optional      bool
	Patches       []Patch
}

type parser struct {
	t     []token
	i     int
	warns []string
}

func (p *parser) eof() bool     { return p.i >= len(p.t) || p.t[p.i].kind == tEOF }
func (p *parser) cur() token    { return p.t[p.i] }
func (p *parser) adv() token    { t := p.t[p.i]; p.i++; return t }
func (p *parser) warn(s string) { p.warns = append(p.warns, "textures: "+s) }

func (p *parser) num(def float64) float64 {
	if p.eof() {
		return def
	}
	if f, err := strconv.ParseFloat(strings.TrimSpace(p.cur().text), 64); err == nil {
		p.i++
		return f
	}
	return def
}

func (p *parser) skipCommas() {
	for !p.eof() && p.cur().kind == tComma {
		p.i++
	}
}

// Parse turns a TEXTURES lump into its Defs plus a list of non-fatal
// warnings (unknown keywords etc.). It never returns an error.
func Parse(src []byte) ([]Def, []string) {
	p := &parser{t: lex(src)}
	var out []Def

	kindOf := func(w string) (Kind, bool) {
		switch w {
		case "texture":
			return KindTexture, true
		case "walltexture":
			return KindWallTexture, true
		case "sprite":
			return KindSprite, true
		case "graphic":
			return KindGraphic, true
		case "flat":
			return KindFlat, true
		}
		return 0, false
	}

	for !p.eof() {
		w := p.adv()
		if w.kind != tWord {
			continue
		}
		optional := false
		lw := w.lower
		if lw == "optional" {
			optional = true
			if p.eof() || p.cur().kind != tWord {
				continue
			}
			lw = p.adv().lower
		}
		k, ok := kindOf(lw)
		if !ok {
			// Unknown top-level directive (e.g. `#include` already stripped
			// by the lexer, or a keyword we don't model) — skip to the next
			// definition, swallowing any brace block.
			if lw != "" {
				p.warn("ignoring top-level `" + lw + "`")
			}
			p.skipBlock()
			continue
		}
		d := p.parseDef(k)
		d.Optional = optional
		if d.Name != "" {
			out = append(out, d)
		}
	}
	return out, p.warns
}

func (p *parser) parseDef(k Kind) Def {
	d := Def{Kind: k}
	if p.eof() {
		return d
	}
	d.Name = strings.ToUpper(p.adv().text)
	p.skipCommas()
	if !p.eof() && p.cur().kind == tWord {
		d.Width = int(p.num(0))
	}
	p.skipCommas()
	if !p.eof() && p.cur().kind == tWord {
		d.Height = int(p.num(0))
	}

	if p.eof() || p.cur().kind != tLBrace {
		return d // header-only (legal: `Texture FOO, 64, 64` with no body)
	}
	p.adv() // {

	for !p.eof() && p.cur().kind != tRBrace {
		tk := p.adv()
		if tk.kind != tWord {
			continue
		}
		switch tk.lower {
		case "xscale":
			d.XScale = p.num(1)
		case "yscale":
			d.YScale = p.num(1)
		case "offset":
			d.OffsetX = int(p.num(0))
			p.skipCommas()
			d.OffsetY = int(p.num(0))
		case "worldpanning":
			d.WorldPanning = true
		case "nodecals":
			d.NoDecals = true
		case "nulltexture":
			d.NullTexture = true
		case "notrim", "noreload", "offset2":
			// accepted, no effect here
		case "patch", "sprite", "graphic":
			if pt, ok := p.parsePatch(); ok {
				d.Patches = append(d.Patches, pt)
			}
		default:
			p.warn("unknown texture property `" + tk.lower + "` in " + d.Name)
		}
	}
	if !p.eof() && p.cur().kind == tRBrace {
		p.adv()
	}
	return d
}

func (p *parser) parsePatch() (Patch, bool) {
	if p.eof() {
		return Patch{}, false
	}
	pt := Patch{Alpha: 1}
	pt.Name = strings.ToUpper(p.adv().text)
	p.skipCommas()
	pt.X = int(p.num(0))
	p.skipCommas()
	pt.Y = int(p.num(0))

	if p.eof() || p.cur().kind != tLBrace {
		return pt, pt.Name != ""
	}
	p.adv() // {
	for !p.eof() && p.cur().kind != tRBrace {
		tk := p.adv()
		if tk.kind != tWord {
			continue
		}
		switch tk.lower {
		case "flipx":
			pt.FlipX = true
		case "flipy":
			pt.FlipY = true
		case "useoffsets":
			pt.UseOffsets = true
		case "rotate":
			r := int(p.num(0))
			r %= 360
			if r < 0 {
				r += 360
			}
			if r == 0 || r == 90 || r == 180 || r == 270 {
				pt.Rotate = r
			} else {
				p.warn("non-orthogonal Rotate " + strconv.Itoa(r) + " ignored")
			}
		case "alpha":
			pt.Alpha = clamp01(p.num(1))
		case "style":
			if !p.eof() {
				pt.Style = p.adv().lower
			}
		case "blend":
			pt.HasBlend, pt.BlendRGBA = p.parseBlend()
		case "translation":
			// consume the rest of the line-ish (words/commas) — not applied
			for !p.eof() && (p.cur().kind == tWord || p.cur().kind == tComma || p.cur().kind == tString) {
				p.i++
			}
			p.warn("Translation on patch " + pt.Name + " not applied")
		default:
			p.warn("unknown patch property `" + tk.lower + "`")
		}
	}
	if !p.eof() && p.cur().kind == tRBrace {
		p.adv()
	}
	return pt, pt.Name != ""
}

// parseBlend reads `Blend <r>,<g>,<b>[,<a>]` or `Blend <hexcolour>[, <a>]`.
func (p *parser) parseBlend() (bool, [4]float64) {
	if p.eof() {
		return false, [4]float64{}
	}
	first := p.cur().text
	// Hex colour form: "AABBCC" or "0xAABBCC".
	hx := strings.TrimPrefix(strings.ToLower(first), "0x")
	if len(hx) == 6 {
		if v, err := strconv.ParseUint(hx, 16, 32); err == nil {
			p.i++
			out := [4]float64{
				float64((v>>16)&0xff) / 255,
				float64((v>>8)&0xff) / 255,
				float64(v&0xff) / 255,
				1,
			}
			p.skipCommas()
			if !p.eof() && p.cur().kind == tWord {
				out[3] = clamp01(p.num(1))
			}
			return true, out
		}
	}
	// Numeric r,g,b[,a] — components may be 0..255 or 0..1.
	var c [4]float64
	c[3] = 1
	got := 0
	for got < 4 {
		p.skipCommas()
		if p.eof() || p.cur().kind != tWord {
			break
		}
		if _, err := strconv.ParseFloat(p.cur().text, 64); err != nil {
			break
		}
		c[got] = p.num(0)
		got++
	}
	if got < 3 {
		return false, [4]float64{}
	}
	for i := 0; i < 3; i++ {
		if c[i] > 1 {
			c[i] /= 255
		}
	}
	if c[3] > 1 {
		c[3] /= 255
	}
	return true, c
}

// skipBlock skips an optional `{ ... }` (balanced) following an unknown
// directive; if the next token isn't a brace it does nothing.
func (p *parser) skipBlock() {
	// swallow header tokens up to a brace or the next plausible directive
	for !p.eof() {
		k := p.cur().kind
		if k == tLBrace {
			break
		}
		if k == tRBrace {
			return
		}
		p.i++
		if p.eof() {
			return
		}
		if p.cur().kind == tWord {
			// crude: stop before what might be the next definition keyword
			switch p.cur().lower {
			case "texture", "walltexture", "sprite", "graphic", "flat", "optional":
				return
			}
		}
	}
	if p.eof() || p.cur().kind != tLBrace {
		return
	}
	depth := 0
	for !p.eof() {
		switch p.adv().kind {
		case tLBrace:
			depth++
		case tRBrace:
			depth--
			if depth == 0 {
				return
			}
		}
	}
}

func clamp01(f float64) float64 {
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}
