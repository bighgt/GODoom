// Package mapinfo is a clean-room reader for the community UMAPINFO lump
// (https://doomwiki.org/wiki/UMAPINFO) — a list of
//
//	MAP <name>
//	{
//	    levelname   = "Entryway"
//	    next        = "MAP02"
//	    music       = "D_RUNNIN"
//	    skytexture  = "SKY1"
//	    partime     = 30
//	}
//
// blocks that override the per-level metadata the vanilla engine otherwise
// hardcodes. Written from the wiki's grammar; no GZDoom / ZDoom source is
// consulted. Full ZDoom-style MAPINFO (nested blocks, gameinfo, clusters,
// episodes, skills) is a separate, larger format and is not handled here.
package mapinfo

import (
	"fmt"
	"strconv"
	"strings"
)

// Entry is one map's overrides. A zero value means "no override" for every
// field; callers fall back to their vanilla behaviour. String fields left
// "" were not present. LevelName etc. are already unquoted.
type Entry struct {
	LevelName  string
	Label      string // "" absent; "-" hidden on the automap/intermission
	LevelPic   string // patch shown instead of LevelName on the intermission
	Author     string
	Next       string // map lump to load on a normal exit
	NextSecret string // map lump to load on a secret exit
	SkyTexture string // TEXTURE1 name for the sky
	Music      string // music lump ("D_...")
	ExitPic    string
	EnterPic   string
	ParTime    int // seconds; 0 = absent
	EndGame    bool
	EndPic     string
	NoIntermission bool
	InterMusic     string
	InterBackdrop  string
	// InterText / InterTextSecret: nil = absent (use the game default);
	// non-nil (possibly empty) = an explicit override, empty meaning the
	// `clear` keyword (show no text).
	InterText       []string
	InterTextSecret []string
	// BossActions: things whose death triggers a line special once every
	// tagged monster of that type is dead. BossActionsCleared reports the
	// `bossaction = clear` keyword (suppress the game's built-in ones).
	BossActions        []BossAction
	BossActionsCleared bool

	// Raw keeps every key verbatim (last value wins), including ones this
	// struct doesn't model, so nothing a WAD sets is silently dropped.
	Raw map[string]string
}

// BossAction is one `bossaction = <thing>, <special>, <tag>` line.
type BossAction struct {
	Thing   string // actor class name or doomednum, as written
	Special int
	Tag     int
}

// Parse reads a UMAPINFO lump into a name->Entry map (keys upper-cased, as
// map lumps are). It is lenient: an unknown key is kept in Raw, a
// malformed value is skipped with the error collected, and parsing
// continues. The returned error (if any) joins every problem found; the
// map is still usable.
func Parse(src []byte) (map[string]*Entry, error) {
	p := &parser{toks: lexMapInfo(src)}
	out := map[string]*Entry{}
	var errs []string

	for !p.at(tEOF) {
		if !p.acceptWordFold("map") {
			errs = append(errs, fmt.Sprintf("line %d: expected `map`, got %q", p.peek().line, p.peek().text))
			p.next() // skip and resync
			continue
		}
		name := strings.ToUpper(p.next().text)
		if name == "" {
			errs = append(errs, fmt.Sprintf("line %d: map has no name", p.peek().line))
		}
		if !p.accept(tLBrace) {
			errs = append(errs, fmt.Sprintf("line %d: expected `{` after map %s", p.peek().line, name))
			continue
		}
		e := &Entry{Raw: map[string]string{}}
		for !p.at(tRBrace) && !p.at(tEOF) {
			if err := p.readProp(e); err != "" {
				errs = append(errs, err)
			}
		}
		p.accept(tRBrace)
		if prev, ok := out[name]; ok {
			mergeEntry(prev, e) // a later block for the same map patches the earlier
		} else {
			out[name] = e
		}
	}

	if len(errs) > 0 {
		return out, fmt.Errorf("umapinfo: %s", strings.Join(errs, "; "))
	}
	return out, nil
}

// readProp consumes one `key = value[, value...]` line, applying it to e.
// Returns a non-empty string describing any problem (parsing still goes on).
func (p *parser) readProp(e *Entry) string {
	key := strings.ToLower(p.next().text)
	if !p.accept(tEq) {
		return fmt.Sprintf("line %d: expected `=` after %q", p.peek().line, key)
	}
	vals := []token{p.next()}
	for p.accept(tComma) {
		vals = append(vals, p.next())
	}
	texts := make([]string, len(vals))
	for i, v := range vals {
		texts[i] = v.text
	}
	e.Raw[key] = strings.Join(texts, ", ")

	str := func() string { return vals[0].text }
	num := func() (int, bool) { n, err := strconv.Atoi(strings.TrimSpace(vals[0].text)); return n, err == nil }
	boolv := func() bool { return strings.EqualFold(str(), "true") || str() == "1" }
	isClear := func() bool { return len(vals) == 1 && strings.EqualFold(vals[0].text, "clear") }

	switch key {
	case "levelname":
		e.LevelName = str()
	case "label":
		if isClear() {
			e.Label = "-"
		} else {
			e.Label = str()
		}
	case "levelpic":
		e.LevelPic = str()
	case "author":
		e.Author = str()
	case "next":
		e.Next = strings.ToUpper(str())
	case "nextsecret":
		e.NextSecret = strings.ToUpper(str())
	case "skytexture":
		e.SkyTexture = str()
	case "music":
		e.Music = str()
	case "exitpic":
		e.ExitPic = str()
	case "enterpic":
		e.EnterPic = str()
	case "endpic":
		e.EndPic = str()
	case "intermusic":
		e.InterMusic = str()
	case "interbackdrop":
		e.InterBackdrop = str()
	case "partime":
		if n, ok := num(); ok {
			e.ParTime = n
		} else {
			return fmt.Sprintf("line %d: partime %q is not a number", vals[0].line, vals[0].text)
		}
	case "endgame":
		e.EndGame = boolv()
	case "nointermission":
		e.NoIntermission = boolv()
	case "intertext":
		e.InterText = clearOrLines(vals)
	case "intertextsecret":
		e.InterTextSecret = clearOrLines(vals)
	case "bossaction":
		if isClear() {
			e.BossActionsCleared = true
			e.BossActions = nil
			break
		}
		if len(vals) != 3 {
			return fmt.Sprintf("line %d: bossaction wants `thing, special, tag`", vals[0].line)
		}
		sp, _ := strconv.Atoi(strings.TrimSpace(vals[1].text))
		tg, _ := strconv.Atoi(strings.TrimSpace(vals[2].text))
		e.BossActions = append(e.BossActions, BossAction{Thing: vals[0].text, Special: sp, Tag: tg})
	default:
		// kept in Raw only.
	}
	return ""
}

func clearOrLines(vals []token) []string {
	if len(vals) == 1 && strings.EqualFold(vals[0].text, "clear") {
		return []string{} // explicit "no text"
	}
	lines := make([]string, len(vals))
	for i, v := range vals {
		lines[i] = v.text
	}
	return lines
}

// mergeEntry patches dst with every field src set (a second `map X {}`
// block for the same map, as ZDoom allows).
func mergeEntry(dst, src *Entry) {
	if src.LevelName != "" {
		dst.LevelName = src.LevelName
	}
	if src.Label != "" {
		dst.Label = src.Label
	}
	if src.LevelPic != "" {
		dst.LevelPic = src.LevelPic
	}
	if src.Author != "" {
		dst.Author = src.Author
	}
	if src.Next != "" {
		dst.Next = src.Next
	}
	if src.NextSecret != "" {
		dst.NextSecret = src.NextSecret
	}
	if src.SkyTexture != "" {
		dst.SkyTexture = src.SkyTexture
	}
	if src.Music != "" {
		dst.Music = src.Music
	}
	if src.ExitPic != "" {
		dst.ExitPic = src.ExitPic
	}
	if src.EnterPic != "" {
		dst.EnterPic = src.EnterPic
	}
	if src.EndPic != "" {
		dst.EndPic = src.EndPic
	}
	if src.InterMusic != "" {
		dst.InterMusic = src.InterMusic
	}
	if src.InterBackdrop != "" {
		dst.InterBackdrop = src.InterBackdrop
	}
	if src.ParTime != 0 {
		dst.ParTime = src.ParTime
	}
	dst.EndGame = dst.EndGame || src.EndGame
	dst.NoIntermission = dst.NoIntermission || src.NoIntermission
	if src.InterText != nil {
		dst.InterText = src.InterText
	}
	if src.InterTextSecret != nil {
		dst.InterTextSecret = src.InterTextSecret
	}
	if src.BossActionsCleared {
		dst.BossActionsCleared = true
		dst.BossActions = nil
	}
	dst.BossActions = append(dst.BossActions, src.BossActions...)
	for k, v := range src.Raw {
		dst.Raw[k] = v
	}
}
