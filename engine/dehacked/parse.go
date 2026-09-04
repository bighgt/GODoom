// Package dehacked is a clean-room reader for the DeHackEd / BEX patch
// format — the line-oriented text (or lump) patches that classic Doom mods
// use to retune the actor, frame, weapon, string and par tables. It only
// PARSES: the result is a Patch of plain maps/slices keyed by DeHackEd's
// fixed numbering and mnemonics; engine/dehacked_load.go translates those to
// this engine's own tables. Written from the DeHackEd v3 documentation and
// the Boom d_deh.c section list — no Doom-source-derived data beyond the
// public enumeration orderings in idtables.go.
package dehacked

import (
	"fmt"
	"strconv"
	"strings"
)

// Patch is a parsed DEH/BEX file. Every section is optional; a missing one
// leaves its field nil/empty. Numeric keys are DeHackEd's (Thing/Frame are
// 1-based and 0-based respectively, matching the tool). Property keys keep
// their original spelling; lookups are case-insensitive (see Get).
type Patch struct {
	DoomVersion int
	PatchFormat int

	Things   map[int]Props // "Thing N" blocks
	Frames   map[int]Props // "Frame N" blocks
	Weapons  map[int]Props // "Weapon N" blocks
	Ammo     map[int]Props // "Ammo N" blocks
	Sounds   map[int]Props // "Sound N" blocks
	Pointers map[int]int    // old-style "Pointer N (Frame F)" -> "Codep Frame = S": frame F takes frame S's action
	CodePtrs map[int]string // BEX [CODEPTR] "Frame N = ActionName"
	Cheat    Props          // "Cheat 0" block: cheat key -> new code string
	Misc     Props          // "Misc 0" block
	Strings  map[string]string // BEX [STRINGS] mnemonic -> replacement
	Pars     []Par             // [PARS]
	Texts    []TextSub         // old-style "Text a b" byte substitutions
	Sprites  map[int]string    // BEX [SPRITES] deh sprite # -> new 4-char name
}

// Props is a section's key/value pairs. Keys are stored verbatim; use Get
// for case-insensitive access.
type Props map[string]string

// Get returns the value for key (case-insensitive) and whether it was set.
func (p Props) Get(key string) (string, bool) {
	if v, ok := p[key]; ok {
		return v, true
	}
	for k, v := range p {
		if strings.EqualFold(k, key) {
			return v, true
		}
	}
	return "", false
}

// Int returns the value for key parsed as an int (0-prefixed hex allowed),
// and whether a value was present and parsed.
func (p Props) Int(key string) (int, bool) {
	s, ok := p.Get(key)
	if !ok {
		return 0, false
	}
	return parseInt(s)
}

// Par is one [PARS] entry. Episode 0 means "Doom II style" (map-number only).
type Par struct {
	Episode, Map, Seconds int
}

// TextSub is one old-style "Text <alen> <blen>" substitution: replace the
// exact string Old wherever it appears in a game string / sprite / sound
// name table with New.
type TextSub struct {
	Old, New string
}

// Parse reads a DEH/BEX blob. It is lenient: an unrecognised section or key
// is skipped (recorded in the returned error list) and parsing continues,
// so a patch that targets features this engine lacks still applies the rest.
func Parse(src []byte) (*Patch, []string) {
	p := &Patch{
		Things: map[int]Props{}, Frames: map[int]Props{}, Weapons: map[int]Props{},
		Ammo: map[int]Props{}, Sounds: map[int]Props{}, Pointers: map[int]int{},
		CodePtrs: map[int]string{}, Cheat: Props{}, Misc: Props{},
		Strings: map[string]string{}, Sprites: map[int]string{},
	}
	var errs []string
	lines := splitKeepOffsets(src)

	cur := Props(nil)       // active numbered block's Props
	curKind := ""           // "thing" | "frame" | "weapon" | "ammo" | "sound" | "cheat" | "misc" | "pointer"
	curPtrFrame := 0        // for a "Pointer" block
	bexSection := ""        // "strings" | "pars" | "codeptr" | "sprites" | "sounds" | "helper" | ""
	var strAcc struct {     // [STRINGS] line-continuation accumulator
		key string
		val string
	}

	flushStr := func() {
		if strAcc.key != "" {
			p.Strings[strAcc.key] = unescapeBex(strAcc.val)
			strAcc.key, strAcc.val = "", ""
		}
	}

	for li := 0; li < len(lines); li++ {
		ln := lines[li]
		raw := strings.TrimRight(ln.text, "\r\n")
		trimmed := strings.TrimSpace(raw)

		// Comments / blanks. A blank line ends a numbered block but not a
		// BEX section (those run to the next [Header] or EOF).
		if trimmed == "" {
			cur, curKind = nil, ""
			continue
		}
		if trimmed[0] == '#' {
			continue
		}

		// BEX section header?
		if trimmed[0] == '[' && strings.HasSuffix(trimmed, "]") {
			flushStr()
			cur, curKind = nil, ""
			bexSection = strings.ToLower(strings.Trim(trimmed, "[]"))
			continue
		}

		// Inside a BEX section, lines are section-specific.
		if bexSection != "" {
			switch bexSection {
			case "strings":
				if k, v, ok := splitEq(raw); ok {
					flushStr()
					strAcc.key = strings.ToUpper(strings.TrimSpace(k))
					v = strings.TrimSpace(v)
					cont := strings.HasSuffix(v, "\\")
					strAcc.val = strings.TrimRight(v, "\\")
					if !cont {
						flushStr()
					}
				} else if strAcc.key != "" { // continuation line
					v := strings.TrimSpace(raw)
					cont := strings.HasSuffix(v, "\\")
					strAcc.val += strings.TrimRight(v, "\\")
					if !cont {
						flushStr()
					}
				}
			case "pars":
				if par, ok := parsePar(trimmed); ok {
					p.Pars = append(p.Pars, par)
				} else {
					errs = append(errs, fmt.Sprintf("line %d: bad [PARS] entry %q", ln.line, trimmed))
				}
			case "codeptr":
				// Frame <n> = <ActionName>
				if k, v, ok := splitEq(trimmed); ok {
					f := strings.Fields(k)
					if len(f) == 2 && strings.EqualFold(f[0], "frame") {
						if n, ok := parseInt(f[1]); ok {
							p.CodePtrs[n] = strings.TrimSpace(v)
							break
						}
					}
					errs = append(errs, fmt.Sprintf("line %d: bad [CODEPTR] entry %q", ln.line, trimmed))
				}
			case "sprites":
				if k, v, ok := splitEq(trimmed); ok {
					if n, ok := parseInt(strings.TrimSpace(k)); ok {
						p.Sprites[n] = strings.TrimSpace(v)
					}
				}
			default:
				// [SOUNDS] [HELPER] etc. — parsed but not modelled.
			}
			continue
		}

		// A "Key = Value" line inside the current numbered block.
		if cur != nil {
			if k, v, ok := splitEq(raw); ok {
				k = strings.TrimSpace(k)
				v = strings.TrimSpace(v)
				if curKind == "pointer" && strings.EqualFold(k, "codep frame") {
					if s, ok := parseInt(v); ok {
						p.Pointers[curPtrFrame] = s
					}
					continue
				}
				cur[k] = v
				continue
			}
			errs = append(errs, fmt.Sprintf("line %d: expected `Key = Value` in %s block, got %q", ln.line, curKind, trimmed))
			continue
		}

		// Header / section-start line.
		fields := strings.Fields(trimmed)
		head := strings.ToLower(fields[0])
		switch head {
		case "thing", "frame", "weapon", "ammo", "sound":
			if len(fields) < 2 {
				errs = append(errs, fmt.Sprintf("line %d: %q needs a number", ln.line, trimmed))
				continue
			}
			n, ok := parseInt(fields[1])
			if !ok {
				errs = append(errs, fmt.Sprintf("line %d: %q: %q is not a number", ln.line, head, fields[1]))
				continue
			}
			cur = Props{}
			curKind = head
			switch head {
			case "thing":
				p.Things[n] = cur
			case "frame":
				p.Frames[n] = cur
			case "weapon":
				p.Weapons[n] = cur
			case "ammo":
				p.Ammo[n] = cur
			case "sound":
				p.Sounds[n] = cur
			}
		case "pointer":
			// Pointer <n> (Frame <f>)
			curPtrFrame = 0
			if i := strings.IndexByte(trimmed, '('); i >= 0 {
				inner := strings.Fields(strings.Trim(trimmed[i:], "()"))
				if len(inner) == 2 && strings.EqualFold(inner[0], "frame") {
					curPtrFrame, _ = parseInt(inner[1])
				}
			}
			cur, curKind = Props{}, "pointer"
		case "cheat":
			cur, curKind = p.Cheat, "cheat"
		case "misc":
			cur, curKind = p.Misc, "misc"
		case "text":
			if len(fields) < 3 {
				errs = append(errs, fmt.Sprintf("line %d: `Text` needs two lengths", ln.line))
				continue
			}
			alen, aok := parseInt(fields[1])
			blen, bok := parseInt(fields[2])
			if !aok || !bok || alen < 0 || blen < 0 {
				errs = append(errs, fmt.Sprintf("line %d: bad `Text` lengths", ln.line))
				continue
			}
			// The payload is the next alen+blen raw bytes AFTER this line's
			// newline. Pull them straight from src by offset.
			start := ln.end
			total := alen + blen
			if start+total > len(src) {
				total = len(src) - start
			}
			payload := string(src[start : start+total])
			old, new := "", ""
			if len(payload) >= alen {
				old = payload[:alen]
				new = payload[alen:]
			} else {
				old = payload
			}
			p.Texts = append(p.Texts, TextSub{Old: old, New: new})
			// Skip the lines the payload covered.
			li = advancePastOffset(lines, li, start+total)
		case "patch":
			// "Patch File for DeHackEd v3.0" (informational) or
			// "Patch format = N".
			if len(fields) >= 2 && strings.EqualFold(fields[1], "format") {
				if _, v, ok := splitEq(trimmed); ok {
					p.PatchFormat, _ = parseInt(strings.TrimSpace(v))
				}
			}
		case "doom":
			// "Doom version = N"
			if _, v, ok := splitEq(trimmed); ok {
				if n, ok := parseInt(strings.TrimSpace(v)); ok {
					p.DoomVersion = n
				}
			}
		case "include":
			// `include foo.deh` — resolved by the loader, not here.
		default:
			errs = append(errs, fmt.Sprintf("line %d: unrecognised section %q", ln.line, fields[0]))
		}
	}
	flushStr()
	return p, errs
}

// ---- helpers ------------------------------------------------------------

type srcLine struct {
	text       string
	line       int
	start, end int // byte offsets of the line's start and just-past-newline
}

func splitKeepOffsets(src []byte) []srcLine {
	var out []srcLine
	line, start := 1, 0
	for i := 0; i < len(src); i++ {
		if src[i] == '\n' {
			out = append(out, srcLine{text: string(src[start : i+1]), line: line, start: start, end: i + 1})
			line++
			start = i + 1
		}
	}
	if start < len(src) {
		out = append(out, srcLine{text: string(src[start:]), line: line, start: start, end: len(src)})
	}
	return out
}

// advancePastOffset returns the index of the last line fully consumed by a
// payload that ends at byte offset `end`, so the outer loop's i++ resumes
// on the first line after it.
func advancePastOffset(lines []srcLine, from int, end int) int {
	i := from
	for i+1 < len(lines) && lines[i+1].start < end {
		i++
	}
	return i
}

func splitEq(s string) (key, val string, ok bool) {
	i := strings.IndexByte(s, '=')
	if i < 0 {
		return "", "", false
	}
	return s[:i], s[i+1:], true
}

func parseInt(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		n, err := strconv.ParseInt(s[2:], 16, 64)
		return int(n), err == nil
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}

func parsePar(s string) (Par, bool) {
	f := strings.Fields(s)
	if len(f) < 2 || !strings.EqualFold(f[0], "par") {
		return Par{}, false
	}
	nums := make([]int, 0, 3)
	for _, tok := range f[1:] {
		if n, ok := parseInt(tok); ok {
			nums = append(nums, n)
		}
	}
	switch len(nums) {
	case 2: // par <map> <sec>   (Doom II)
		return Par{Episode: 0, Map: nums[0], Seconds: nums[1]}, true
	case 3: // par <ep> <map> <sec>
		return Par{Episode: nums[0], Map: nums[1], Seconds: nums[2]}, true
	}
	return Par{}, false
}

// unescapeBex turns BEX string escapes (\n \t \r \\ \") into their bytes.
func unescapeBex(s string) string {
	if !strings.ContainsRune(s, '\\') {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
			switch s[i] {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			default:
				b.WriteByte(s[i])
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// ParseBits splits a DeHackEd `Bits = A+B|C, D` flag list into upper-cased
// names. A purely numeric value (a raw bitmask) is returned as the single
// element "<number>" for the caller to interpret.
func ParseBits(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if n, ok := parseInt(s); ok {
		return []string{strconv.Itoa(n)}
	}
	repl := strings.NewReplacer("+", " ", "|", " ", ",", " ")
	var out []string
	for _, f := range strings.Fields(repl.Replace(s)) {
		out = append(out, strings.ToUpper(f))
	}
	return out
}
