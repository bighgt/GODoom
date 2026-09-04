// Package animdefs is a clean-room parser for the ZDoom ANIMDEFS lump —
// data-driven flat / wall-texture animation, plus the `warp` / `warp2`
// screen-warp (swimming liquid) effect. Written from the format
// description on the zdoom wiki and observable vanilla behaviour; no
// engine source was consulted.
//
// Supported directives:
//
//	flat|texture <name> [warp|warp2|nowarp|allowdecals] ...
//	    pic <n|name> tics <t>
//	    pic <n|name> rand <lo> <hi>
//	    range <name> tics <t>
//	    oscillate
//	warp  flat|texture <name> [<speed>]
//	warp2 flat|texture <name> [<speed>]
//
// `switch`, `cameratexture`, `animateddoor` and `#include` are recognised
// only well enough to skip; each adds a line to Warnings.
package animdefs

import "strconv"

// Frame is one step of an explicit ANIMDEFS animation. Tics > 0 is a fixed
// duration; Tics == 0 means pick uniformly in [RandLo, RandHi] each cycle
// (the engine approximates that with the midpoint to stay stateless).
type Frame struct {
	Name           string
	Tics           int
	RandLo, RandHi int
}

// Def is a `flat` or `texture` animation block.
type Def struct {
	Texture   bool // wall-texture namespace (else flat)
	Name      string
	Frames    []Frame // explicit `pic` list; empty when RangeLast is set
	RangeLast string  // `range <name> tics <t>` short form: animate Name..RangeLast
	RangeTics int
	Oscillate bool
}

// Warp is a `warp` / `warp2` line.
type Warp struct {
	Texture bool
	Name    string
	Speed   float64 // 0 = unspecified (caller picks a default)
	Wide    bool    // warp2 — larger displacement
}

// AnimDefs is one parsed lump (or the merge of several — see Merge).
type AnimDefs struct {
	Defs     []Def
	Warps    []Warp
	Warnings []string
}

type parser struct {
	t   []token
	i   int
	out *AnimDefs
}

func (p *parser) eof() bool { return p.i >= len(p.t) }
func (p *parser) peek() token {
	if p.eof() {
		return token{}
	}
	return p.t[p.i]
}
func (p *parser) next() token    { t := p.peek(); p.i++; return t }
func (p *parser) warnf(s string) { p.out.Warnings = append(p.out.Warnings, s) }

// topKeywords are the directives that end an implicit flat/texture block.
var topKeywords = map[string]bool{
	"flat": true, "texture": true, "warp": true, "warp2": true,
	"switch": true, "cameratexture": true, "animateddoor": true,
	"#include": true, "include": true,
}

// Parse turns an ANIMDEFS lump into an AnimDefs. It never returns an error;
// anything unrecognised is skipped and noted in Warnings.
func Parse(src []byte) *AnimDefs {
	p := &parser{t: lex(src), out: &AnimDefs{}}
	for !p.eof() {
		kw := p.next()
		switch kw.lower {
		case "flat":
			p.parseAnim(false)
		case "texture":
			p.parseAnim(true)
		case "warp", "warp2":
			p.parseWarp(kw.lower == "warp2")
		case "switch", "cameratexture", "animateddoor", "include", "#include":
			p.warnf("animdefs: `" + kw.text + "` not supported, skipped (line " + itoa(kw.line) + ")")
			p.skipToTopKeyword()
		default:
			// Stray token (or a directive we don't know) — skip it.
			p.skipToTopKeyword()
		}
	}
	return p.out
}

func (p *parser) parseWarp(wide bool) {
	kind := p.next()
	isTex := kind.lower == "texture"
	if !isTex && kind.lower != "flat" {
		p.warnf("animdefs: warp missing flat/texture (line " + itoa(kind.line) + ")")
		return
	}
	if p.eof() {
		return
	}
	name := p.next().text
	w := Warp{Texture: isTex, Name: name, Wide: wide}
	// Optional trailing speed (a bare number on the same logical line). The
	// lexer has no line grouping, so accept a number here only if the very
	// next token parses as a float — `flat`/`warp`/... never do.
	if !p.eof() {
		if f, err := strconv.ParseFloat(p.peek().text, 64); err == nil {
			w.Speed = f
			p.i++
		}
	}
	p.out.Warps = append(p.out.Warps, w)
}

func (p *parser) parseAnim(isTex bool) {
	if p.eof() {
		return
	}
	d := Def{Texture: isTex, Name: p.next().text}

	// Optional flags right after the name.
	for !p.eof() {
		switch p.peek().lower {
		case "allowdecals", "nowarp", "warp", "warp2":
			// `warp`/`warp2` as a *flag* here (not a top-level line) is the
			// old inline form; treat it as a warp on this texture.
			if p.peek().lower == "warp" || p.peek().lower == "warp2" {
				p.out.Warps = append(p.out.Warps, Warp{Texture: isTex, Name: d.Name, Wide: p.peek().lower == "warp2"})
			}
			p.i++
		default:
			goto body
		}
	}
body:
	for !p.eof() {
		switch p.peek().lower {
		case "pic":
			p.i++
			f, ok := p.parsePic()
			if ok {
				d.Frames = append(d.Frames, f)
			}
		case "range":
			p.i++
			if p.eof() {
				break
			}
			d.RangeLast = p.next().text
			if !p.eof() && p.peek().lower == "tics" {
				p.i++
				d.RangeTics = p.int(8)
			} else if !p.eof() && p.peek().lower == "rand" {
				p.i++
				lo, hi := p.int(8), p.int(8)
				d.RangeTics = (lo + hi) / 2
			}
		case "oscillate":
			p.i++
			d.Oscillate = true
		default:
			if topKeywords[p.peek().lower] {
				p.finishAnim(d)
				return
			}
			// Unknown line inside the block — drop the token and continue.
			p.i++
		}
	}
	p.finishAnim(d)
}

func (p *parser) finishAnim(d Def) {
	if d.Name == "" || (len(d.Frames) == 0 && d.RangeLast == "") {
		return
	}
	p.out.Defs = append(p.out.Defs, d)
}

// parsePic reads the token(s) after `pic`: `<n|name>` then either
// `tics <t>` or `rand <lo> <hi>`.
func (p *parser) parsePic() (Frame, bool) {
	if p.eof() {
		return Frame{}, false
	}
	arg := p.next().text
	f := Frame{Name: arg}
	if p.eof() {
		return f, true
	}
	switch p.peek().lower {
	case "tics":
		p.i++
		f.Tics = p.int(8)
	case "rand":
		p.i++
		f.RandLo = p.int(1)
		f.RandHi = p.int(1)
		if f.RandHi < f.RandLo {
			f.RandLo, f.RandHi = f.RandHi, f.RandLo
		}
	default:
		f.Tics = 8
	}
	return f, true
}

// int consumes the next token as an integer, or returns def without
// consuming if it isn't one.
func (p *parser) int(def int) int {
	if p.eof() {
		return def
	}
	n, err := strconv.Atoi(p.peek().text)
	if err != nil {
		return def
	}
	p.i++
	return n
}

func (p *parser) skipToTopKeyword() {
	for !p.eof() && !topKeywords[p.peek().lower] {
		p.i++
	}
}

// Merge layers several parsed lumps: a later Def / Warp for the same
// (namespace, name) replaces an earlier one, matching how ZDoom's last
// ANIMDEFS wins.
func Merge(all ...*AnimDefs) *AnimDefs {
	out := &AnimDefs{}
	defAt := map[[2]string]int{}
	warpAt := map[[2]string]int{}
	key := func(tex bool, name string) [2]string {
		k := "F"
		if tex {
			k = "T"
		}
		return [2]string{k, upper(name)}
	}
	for _, a := range all {
		if a == nil {
			continue
		}
		for _, d := range a.Defs {
			k := key(d.Texture, d.Name)
			if idx, ok := defAt[k]; ok {
				out.Defs[idx] = d
			} else {
				defAt[k] = len(out.Defs)
				out.Defs = append(out.Defs, d)
			}
		}
		for _, w := range a.Warps {
			k := key(w.Texture, w.Name)
			if idx, ok := warpAt[k]; ok {
				out.Warps[idx] = w
			} else {
				warpAt[k] = len(out.Warps)
				out.Warps = append(out.Warps, w)
			}
		}
		out.Warnings = append(out.Warnings, a.Warnings...)
	}
	return out
}

func itoa(i int) string { return strconv.Itoa(i) }

func upper(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - ('a' - 'A')
		}
	}
	return string(b)
}
