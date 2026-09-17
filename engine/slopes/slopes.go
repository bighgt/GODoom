// Package slopes parses a SLOPES sidecar lump — an optional, hand-authored
// text format (following the same convention as GLDEFS/ANIMDEFS/UMAPINFO:
// a WAD or mounted mod may carry one, a vanilla IWAD has none, and nothing
// changes when it's absent) that describes two kinds of sector geometry
// vanilla Doom has no room for: a tilted floor/ceiling plane, and a
// Boom/GZDoom-style "3D floor" (room-over-room) band borrowed from another
// sector elsewhere in the map.
//
// Grammar, one block per map, one line per plane or extra floor:
//
//	MAP01 {
//	    sector 12 floor     plane 0 0 64  128 0 64  0 128 96
//	    sector 12 ceiling   plane 0 0 192 128 0 192 0 128 192
//	    sector 20 extrafloor 45
//	}
//
// "sector <index> <floor|ceiling> plane x1 y1 z1  x2 y2 z2  x3 y3 z3" gives
// three points (world map units) the plane must pass through — the natural
// thing to read off a level editor's 3D view. The first point also becomes
// the plane's pivot: engine/wad.Sector keeps FloorHeight/CeilingHeight
// meaning "the height at the pivot" (not at the world origin), so Def's
// P1 is handed to the engine as (pivot, height-at-pivot) directly, and
// P2/P3 only contribute the plane's tilt.
//
// "sector <target> extrafloor <control>" makes the CONTROL sector's own
// ceiling a walkable top surface floating inside the TARGET sector's
// footprint (a platform), and its floor a ceiling for anything passing
// underneath — see engine's ExtraFloor/planeAt for the runtime side.
// Multiple extrafloor lines for the same target stack any number of
// platforms.
package slopes

import (
	"fmt"
	"strconv"
	"strings"
)

// Def is one authored plane: sector SectorIndex's floor (Ceiling == false)
// or ceiling. P1, P2, P3 are the three points (x, y, z) the plane must pass
// through, in the order written.
type Def struct {
	SectorIndex int
	Ceiling     bool
	P1, P2, P3  [3]float64
	Line        int // source line, for diagnostics
}

// ExtraFloorDef is one "sector <target> extrafloor <control>" line.
type ExtraFloorDef struct {
	TargetSector, ControlSector int
	Line                        int // source line, for diagnostics
}

// Parse reads src and returns every map's plane defs and extra-floor defs,
// each keyed by upper-cased map name, plus any errors found (a malformed
// line is skipped and reported, not fatal to the rest of the file —
// matching mapinfo/gldefs's "never fails outright" convention).
func Parse(src []byte) (planes map[string][]Def, floors map[string][]ExtraFloorDef, errs []error) {
	toks := lex(src)
	p := &parser{toks: toks}
	planes = map[string][]Def{}
	floors = map[string][]ExtraFloorDef{}

	for !p.at(tEOF) {
		mapTok := p.next()
		if mapTok.kind != tWord {
			continue // stray token before a map name — skip and keep going
		}
		mapName := strings.ToUpper(mapTok.text)
		if !p.accept(tLBrace) {
			errs = append(errs, fmt.Errorf("slopes: line %d: expected { after map name %s", mapTok.line, mapTok.text))
			continue
		}
		for !p.at(tRBrace) && !p.at(tEOF) {
			plane, floor, err := parseSectorLine(p)
			switch {
			case err != nil:
				errs = append(errs, err)
			case floor != nil:
				floors[mapName] = append(floors[mapName], *floor)
			default:
				planes[mapName] = append(planes[mapName], *plane)
			}
		}
		p.accept(tRBrace)
	}
	return planes, floors, errs
}

func parseSectorLine(p *parser) (plane *Def, floor *ExtraFloorDef, err error) {
	line := p.peek().line
	if !p.acceptWordFold("sector") {
		p.next() // consume the unexpected token so a bad line can't loop forever
		return nil, nil, fmt.Errorf("slopes: line %d: expected 'sector'", line)
	}
	idxTok := p.next()
	idx, aerr := strconv.Atoi(idxTok.text)
	if aerr != nil {
		return nil, nil, fmt.Errorf("slopes: line %d: expected a sector index, got %q", line, idxTok.text)
	}

	if p.acceptWordFold("extrafloor") {
		ctrlTok := p.next()
		ctrl, cerr := strconv.Atoi(ctrlTok.text)
		if cerr != nil {
			return nil, nil, fmt.Errorf("slopes: line %d: expected a control sector index, got %q", line, ctrlTok.text)
		}
		return nil, &ExtraFloorDef{TargetSector: idx, ControlSector: ctrl, Line: line}, nil
	}

	ceiling := false
	switch {
	case p.acceptWordFold("floor"):
	case p.acceptWordFold("ceiling"):
		ceiling = true
	default:
		bad := p.next() // consume the offending token so a bad line can't loop forever
		return nil, nil, fmt.Errorf("slopes: line %d: expected 'floor', 'ceiling' or 'extrafloor', got %q", line, bad.text)
	}
	if !p.acceptWordFold("plane") {
		bad := p.next()
		return nil, nil, fmt.Errorf("slopes: line %d: expected 'plane', got %q", line, bad.text)
	}
	var pts [3][3]float64
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			tok := p.next()
			v, ferr := strconv.ParseFloat(tok.text, 64)
			if ferr != nil {
				return nil, nil, fmt.Errorf("slopes: line %d: expected a number, got %q", line, tok.text)
			}
			pts[i][j] = v
		}
	}
	return &Def{SectorIndex: idx, Ceiling: ceiling, P1: pts[0], P2: pts[1], P3: pts[2], Line: line}, nil, nil
}
