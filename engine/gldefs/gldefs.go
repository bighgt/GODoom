// Package gldefs is a clean-room, pure-Go reader for the GLDEFS text lump
// used by ZDoom-family source ports (GZDoom, UZDoom) to describe dynamic
// lights and, attached to actor sprite frames, which lights an object
// emits. It is written from the ZDoom wiki's published format description —
// no GZDoom/UZDoom source is copied or translated (those are GPLv3; this
// stays a from-scratch reimplementation of the data format only).
//
// Scope of this first cut:
//   - the five light kinds: pointlight, pulselight, flickerlight,
//     flickerlight2, sectorlight, with their documented properties.
//   - object { frame <SPR>[F] { light NAME ... } } attachments, indexed by
//     sprite name + frame letter so the engine can look them up without a
//     GZDoom-class-name table.
//   - brightmap blocks are captured verbatim for a later feature; other
//     top-level blocks (skybox, glow, material, hardwareshader, ...) are
//     skipped over safely.
//   - #include "LUMP" via a caller-supplied resolver.
package gldefs

import (
	"fmt"
	"strconv"
	"strings"
)

// LightType is one of the GLDEFS dynamic-light kinds.
type LightType int

const (
	Point    LightType = iota // pointlight — steady
	Pulse                     // pulselight — smoothly oscillates size over Interval
	Flicker                   // flickerlight — randomly toggles size, per-tic, at Chance
	Flicker2                  // flickerlight2 — sine flicker over Interval
	Sector                    // sectorlight — brightness scales with the sector light level
)

func (t LightType) String() string {
	switch t {
	case Point:
		return "pointlight"
	case Pulse:
		return "pulselight"
	case Flicker:
		return "flickerlight"
	case Flicker2:
		return "flickerlight2"
	case Sector:
		return "sectorlight"
	}
	return "light"
}

// Spot is the optional cone restriction on a light (GLDEFS `spot`).
type Spot struct {
	InnerAngle float32 // degrees; full brightness within this half-angle
	OuterAngle float32 // degrees; falls to zero by this half-angle
	MaxDist    float32 // 0 = unset
}

// Light is one parsed *light definition. Colour components are linear 0..1.
// Size / SecondarySize are radii in map units. Angles in Spot are degrees.
type Light struct {
	Name          string
	Type          LightType
	Color         [3]float32
	Size          float32
	SecondarySize float32 // pulse / flicker second radius
	Interval      float32 // seconds — pulse / flickerlight2 period
	Chance        float32 // 0..1 — flickerlight on-probability
	Offset        [3]float32 // x, y(up), z relative to the actor origin
	Scale         float32    // 0 = unset
	Subtractive   bool
	Additive      bool
	Attenuate     bool // physically-attenuated falloff (GZDoom's modern default)
	DontLightSelf bool
	Spot          *Spot
}

// Brightmap is a captured `brightmap` block — consumed by the brightmap
// feature, not by the light path. Kind is "texture", "flat" or "sprite".
type Brightmap struct {
	Kind             string
	Name             string // target texture/flat/sprite lump name (upper-case)
	Map              string // greyscale mask lump/path
	IWAD             bool
	DisableFullbright bool
}

// frameAttach is one resolved object{frame{...}} entry.
type frameAttach struct {
	frames string   // frame letters this applies to; "" = every frame
	lights []*Light // resolved; a `light NONE` yields an explicit empty slice
}

// Defs is the parsed result. The zero value and a nil *Defs are both safe
// to query (every method is nil-tolerant), so callers can hold a possibly
// -nil *Defs without guarding each use.
type Defs struct {
	lights     map[string]*Light
	bySprite   map[string][]frameAttach // key: 4-char sprite name, upper-case
	brightmaps []Brightmap
}

// NumLights reports how many distinct light definitions were parsed.
func (d *Defs) NumLights() int {
	if d == nil {
		return 0
	}
	return len(d.lights)
}

// NumAttachments reports how many sprites carry at least one frame light.
func (d *Defs) NumAttachments() int {
	if d == nil {
		return 0
	}
	return len(d.bySprite)
}

// Brightmaps returns the captured brightmap blocks (nil if none).
func (d *Defs) Brightmaps() []Brightmap {
	if d == nil {
		return nil
	}
	return d.brightmaps
}

// Light looks up a light definition by name (case-insensitive). ok is false
// for an unknown name or a nil *Defs.
func (d *Defs) Light(name string) (l *Light, ok bool) {
	if d == nil {
		return nil, false
	}
	l, ok = d.lights[strings.ToUpper(name)]
	return l, ok
}

// Frame returns the lights attached to the given sprite (4-char name,
// case-insensitive) at frame index frame (0 == 'A'). Returns nil when
// nothing is attached, the sprite is unknown, or d is nil. The result
// aliases internal storage — treat it as read-only.
func (d *Defs) Frame(sprite string, frame int) []*Light {
	if d == nil {
		return nil
	}
	attaches := d.bySprite[strings.ToUpper(sprite)]
	if len(attaches) == 0 {
		return nil
	}
	var out []*Light
	for _, a := range attaches {
		if a.frames != "" {
			if frame < 0 || frame > 'Z'-'A' {
				continue
			}
			if !strings.ContainsRune(a.frames, rune('A'+frame)) {
				continue
			}
		}
		out = append(out, a.lights...)
	}
	return out
}

// Parse reads GLDEFS source. include resolves an #include "NAME" to bytes
// (return ok=false to skip); pass nil to ignore includes. Parsing is
// best-effort: recoverable problems are collected in errs and parsing
// continues, so a partial lump still yields the lights it could read.
func Parse(src []byte, include func(name string) ([]byte, bool)) (defs *Defs, errs []error) {
	p := &parser{
		toks:    lex(src),
		include: include,
		seen:    map[string]bool{},
		defs: &Defs{
			lights:   map[string]*Light{},
			bySprite: map[string][]frameAttach{},
		},
	}
	p.run()
	p.resolve()
	return p.defs, p.errs
}

type parser struct {
	toks    []token
	pos     int
	defs    *Defs
	errs    []error
	include func(name string) ([]byte, bool)
	seen    map[string]bool // #include cycle guard
	depth   int             // #include recursion depth

	// pending frame->light-name attachments, resolved to *Light after the
	// whole lump is read (a frame may reference a light defined later).
	pending []pendingAttach
}

type pendingAttach struct {
	sprite string
	frames string
	names  []string // upper-case light names; empty slice from `light NONE`
}

func (p *parser) errf(line int, format string, a ...any) {
	p.errs = append(p.errs, fmt.Errorf("line %d: %s", line, fmt.Sprintf(format, a...)))
}

func (p *parser) eof() bool { return p.pos >= len(p.toks) }

func (p *parser) peek() token {
	if p.eof() {
		return token{kind: tWord, text: ""}
	}
	return p.toks[p.pos]
}

func (p *parser) next() token {
	t := p.peek()
	if !p.eof() {
		p.pos++
	}
	return t
}

// run is the top-level loop.
func (p *parser) run() {
	for !p.eof() {
		t := p.next()
		if t.kind != tWord {
			continue // stray brace / string at top level — ignore
		}
		switch kw := strings.ToLower(t.text); kw {
		case "pointlight":
			p.parseLight(Point)
		case "pulselight":
			p.parseLight(Pulse)
		case "flickerlight":
			p.parseLight(Flicker)
		case "flickerlight2":
			p.parseLight(Flicker2)
		case "sectorlight":
			p.parseLight(Sector)
		case "object":
			p.parseObject()
		case "brightmap":
			p.parseBrightmap()
		case "#include", "include":
			p.parseInclude()
		default:
			// skybox / glow / material / hardwareshader / fogdensity / etc.
			p.skipStatement()
		}
	}
}

// skipStatement consumes an unrecognised top-level construct: any leading
// words, then a full brace group if one follows.
func (p *parser) skipStatement() {
	for !p.eof() {
		switch p.peek().kind {
		case tLBrace:
			p.skipBraced()
			return
		case tRBrace:
			return
		default:
			if isTopKeyword(strings.ToLower(p.peek().text)) {
				return
			}
			p.next()
		}
	}
}

// skipBraced consumes a balanced { ... } group; peek() must be '{'.
func (p *parser) skipBraced() {
	if p.peek().kind != tLBrace {
		return
	}
	depth := 0
	for !p.eof() {
		switch p.next().kind {
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

func isTopKeyword(s string) bool {
	switch s {
	case "pointlight", "pulselight", "flickerlight", "flickerlight2", "sectorlight",
		"object", "brightmap", "skybox", "glow", "material", "hardwareshader",
		"#include", "include":
		return true
	}
	return false
}

// expectLBrace advances past an opening brace, reporting if it is missing.
func (p *parser) expectLBrace(ctx string) bool {
	if p.peek().kind == tLBrace {
		p.next()
		return true
	}
	p.errf(p.peek().line, "expected '{' after %s", ctx)
	return false
}

func (p *parser) parseLight(kind LightType) {
	nameTok := p.next()
	if nameTok.kind != tWord || nameTok.text == "" {
		p.errf(nameTok.line, "%s: missing name", kind)
		return
	}
	l := &Light{Name: strings.ToUpper(nameTok.text), Type: kind}
	if !p.expectLBrace(kind.String() + " " + l.Name) {
		return
	}
	for !p.eof() && p.peek().kind != tRBrace {
		key := strings.ToLower(p.next().text)
		switch key {
		case "color":
			l.Color = p.readColor()
		case "size":
			l.Size = p.readFloat()
		case "secondarysize":
			l.SecondarySize = p.readFloat()
		case "offset":
			l.Offset = [3]float32{p.readFloat(), p.readFloat(), p.readFloat()}
		case "interval":
			l.Interval = p.readFloat()
		case "chance":
			l.Chance = p.readFloat()
		case "scale":
			l.Scale = p.readFloat()
		case "subtractive":
			l.Subtractive = p.readBool()
		case "additive":
			l.Additive = p.readBool()
		case "attenuate":
			l.Attenuate = p.readBool()
		case "dontlightself":
			l.DontLightSelf = p.readBool()
		case "dontlightactors", "dontlightmap", "noshadowmap":
			p.readBool() // recognised, not yet acted on
		case "spot":
			sp := &Spot{InnerAngle: p.readFloat(), OuterAngle: p.readFloat()}
			if f, ok := p.tryFloat(); ok {
				sp.MaxDist = f
			}
			l.Spot = sp
		case "":
			// stray token — skip
		default:
			// Unknown property: best-effort skip of its value word(s).
			for !p.eof() && p.peek().kind == tWord && !isLightKey(strings.ToLower(p.peek().text)) {
				p.next()
			}
		}
	}
	if p.peek().kind == tRBrace {
		p.next()
	}
	p.defs.lights[l.Name] = l
}

func isLightKey(s string) bool {
	switch s {
	case "color", "size", "secondarysize", "offset", "interval", "chance", "scale",
		"subtractive", "additive", "attenuate", "dontlightself", "dontlightactors",
		"dontlightmap", "noshadowmap", "spot":
		return true
	}
	return false
}

func (p *parser) parseObject() {
	classTok := p.next()
	if classTok.kind != tWord {
		p.errf(classTok.line, "object: missing class name")
		return
	}
	// optional: replaces <OtherClass>
	if strings.EqualFold(p.peek().text, "replaces") {
		p.next()
		p.next()
	}
	if !p.expectLBrace("object " + classTok.text) {
		return
	}
	for !p.eof() && p.peek().kind != tRBrace {
		kw := strings.ToLower(p.next().text)
		if kw != "frame" {
			// tolerate stray tokens / unknown sub-keywords
			if p.peek().kind == tLBrace {
				p.skipBraced()
			}
			continue
		}
		sprTok := p.next()
		if sprTok.kind != tWord || len(sprTok.text) < 4 {
			p.errf(sprTok.line, "frame: bad sprite token %q", sprTok.text)
			if p.peek().kind == tLBrace {
				p.skipBraced()
			}
			continue
		}
		spr := strings.ToUpper(sprTok.text)
		sprite, frames := spr[:4], spr[4:]
		var names []string
		explicitNone := false
		if p.expectLBrace("frame " + spr) {
			for !p.eof() && p.peek().kind != tRBrace {
				sub := strings.ToLower(p.next().text)
				if sub == "light" {
					ln := strings.ToUpper(p.next().text)
					if ln == "NONE" {
						explicitNone = true
						continue
					}
					names = append(names, ln)
				}
			}
			if p.peek().kind == tRBrace {
				p.next()
			}
		}
		if len(names) > 0 || explicitNone {
			p.pending = append(p.pending, pendingAttach{sprite: sprite, frames: frames, names: names})
		}
	}
	if p.peek().kind == tRBrace {
		p.next()
	}
}

func (p *parser) parseBrightmap() {
	kindTok := p.next()
	nameTok := p.next()
	bm := Brightmap{
		Kind: strings.ToLower(kindTok.text),
		Name: strings.ToUpper(nameTok.text),
	}
	if !p.expectLBrace("brightmap") {
		return
	}
	for !p.eof() && p.peek().kind != tRBrace {
		key := strings.ToLower(p.next().text)
		switch key {
		case "map":
			bm.Map = p.next().text
		case "iwad":
			bm.IWAD = true
		case "disablefullbright":
			bm.DisableFullbright = true
		}
	}
	if p.peek().kind == tRBrace {
		p.next()
	}
	if bm.Kind != "" && bm.Name != "" {
		p.defs.brightmaps = append(p.defs.brightmaps, bm)
	}
}

func (p *parser) parseInclude() {
	nameTok := p.next()
	name := strings.ToUpper(strings.TrimSpace(nameTok.text))
	if name == "" || p.include == nil {
		return
	}
	if p.depth >= 16 {
		p.errf(nameTok.line, "#include %q: nesting too deep", name)
		return
	}
	if p.seen[name] {
		return // already included — silently ignore (matches GZDoom)
	}
	p.seen[name] = true
	data, ok := p.include(name)
	if !ok {
		p.errf(nameTok.line, "#include %q: lump not found", name)
		return
	}
	sub := &parser{
		toks: lex(data), defs: p.defs, include: p.include,
		seen: p.seen, depth: p.depth + 1,
	}
	sub.run()
	p.errs = append(p.errs, sub.errs...)
	p.pending = append(p.pending, sub.pending...)
}

// resolve turns pending frame attachments into *Light pointers now that
// every light definition in the lump has been read.
func (p *parser) resolve() {
	for _, pa := range p.pending {
		fa := frameAttach{frames: pa.frames, lights: []*Light{}}
		for _, n := range pa.names {
			if l, ok := p.defs.lights[n]; ok {
				fa.lights = append(fa.lights, l)
			} else {
				p.errf(0, "frame %s%s: light %q is not defined", pa.sprite, pa.frames, n)
			}
		}
		p.defs.bySprite[pa.sprite] = append(p.defs.bySprite[pa.sprite], fa)
	}
}

// ---- value readers -------------------------------------------------------

func (p *parser) readFloat() float32 {
	f, _ := p.tryFloat()
	return f
}

// tryFloat consumes the next token only if it parses as a number.
func (p *parser) tryFloat() (float32, bool) {
	if p.eof() || p.peek().kind != tWord {
		return 0, false
	}
	s := strings.TrimSuffix(p.peek().text, ",")
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	p.next()
	return float32(v), true
}

func (p *parser) readBool() bool {
	if p.eof() || p.peek().kind != tWord {
		return true // bare flag, e.g. `attenuate` with no value
	}
	switch strings.ToLower(strings.TrimSuffix(p.next().text, ",")) {
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

// readColor accepts `r g b` floats, a "RRGGBB" hex string, or a small set
// of X11-style colour names. Integer triples with a component > 1 are read
// as 0..255 and scaled — a pragmatic nod to the many older lumps authored
// that way; a valid 0..1 triple is never all-integer-and-over-1 so it is
// unaffected.
func (p *parser) readColor() [3]float32 {
	if p.peek().kind == tString || looksHex(p.peek().text) {
		if c, ok := parseHexColor(strings.Trim(p.next().text, `"#`)); ok {
			return c
		}
		return [3]float32{}
	}
	if p.peek().kind == tWord {
		if c, ok := namedColor(strings.ToLower(p.peek().text)); ok {
			p.next()
			return c
		}
	}
	var raw [3]float64
	allInt := true
	for i := 0; i < 3; i++ {
		s := strings.TrimSuffix(p.peek().text, ",")
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			break
		}
		if v != float64(int64(v)) {
			allInt = false
		}
		raw[i] = v
		p.next()
	}
	if allInt && (raw[0] > 1 || raw[1] > 1 || raw[2] > 1) {
		for i := range raw {
			raw[i] /= 255
		}
	}
	var c [3]float32
	for i := range raw {
		c[i] = clamp01(float32(raw[i]))
	}
	return c
}

func clamp01(f float32) float32 {
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}

func looksHex(s string) bool {
	s = strings.TrimPrefix(s, "#")
	if len(s) != 6 {
		return false
	}
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

func parseHexColor(s string) ([3]float32, bool) {
	if len(s) != 6 {
		return [3]float32{}, false
	}
	n, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return [3]float32{}, false
	}
	return [3]float32{
		float32((n>>16)&0xff) / 255,
		float32((n>>8)&0xff) / 255,
		float32(n&0xff) / 255,
	}, true
}

func namedColor(s string) ([3]float32, bool) {
	switch s {
	case "white":
		return [3]float32{1, 1, 1}, true
	case "black":
		return [3]float32{0, 0, 0}, true
	case "red":
		return [3]float32{1, 0, 0}, true
	case "green":
		return [3]float32{0, 1, 0}, true
	case "blue":
		return [3]float32{0, 0, 1}, true
	case "yellow":
		return [3]float32{1, 1, 0}, true
	case "orange":
		return [3]float32{1, 0.5, 0}, true
	case "cyan":
		return [3]float32{0, 1, 1}, true
	case "magenta", "purple":
		return [3]float32{1, 0, 1}, true
	}
	return [3]float32{}, false
}
