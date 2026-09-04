package engine

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"twopointfive/assets/vfs"
	"twopointfive/engine/dehacked"
	"twopointfive/wad"
)

// DEHACKED / BEX support. A DEH/BEX patch retunes the game's internal
// tables — actor stats, animation frames, weapons, strings, par times. This
// engine reads it from a DEHACKED lump in the WAD or a mounted mod, and
// from any *.deh / *.bex file sitting next to the loaded WAD (Doom's
// `-deh` convention).
//
// engine/dehacked is a clean-room parser of the text format; this file is
// the loader + the applier. Applied: Thing stat fields (health, speed,
// size, mass, damage, pain chance, reaction time, flags, thing ID); Frame
// edits (sprite/frame/fullbright/duration/next); [CODEPTR] and old-style
// Pointer action-function edits; BEX [SPRITES] and 4-char Text sprite
// renames; [PARS] par times; [STRINGS] level names. Numbered Weapon / Ammo
// / Sound blocks and the Cheat / Misc sections are parsed and logged but
// not yet wired (Weapon frames live in a separate psprite table here, and
// Misc/Cheat need scattered hard-coded constants hoisted first).

// dehActionByName maps a DeHackEd codepointer name (with or without the
// "A_" prefix, case-insensitive) to the engine action function it names.
// Only the codepointers this build actually implements are here; an edit
// naming any other (A_VileChase, A_BrainSpit, the revenant/mancubus/weapon
// pointers, ...) is dropped with a log line rather than silently ignored.
var dehActionByName = map[string]mobjAction{
	"look": aLook, "chase": aChase, "facetarget": aFaceTarget,
	"posattack": aPosAttack, "sposattack": aSPosAttack,
	"cposattack": aCPosAttack, "cposrefire": aCPosRefire,
	"troopattack": aTroopAttack, "sargattack": aSargAttack,
	"headattack": aHeadAttack, "bruisattack": aBruisAttack,
	"skullattack": aSkullAttack, "scream": aScream, "xscream": aXScream,
	"pain": aPain, "fall": aFall, "explode": aExplode, "bossdeath": aBossDeath,
}

func dehAction(name string) (mobjAction, bool) {
	k := strings.ToLower(strings.TrimSpace(name))
	k = strings.TrimPrefix(k, "a_")
	fn, ok := dehActionByName[k]
	return fn, ok
}

// loadDehacked gathers every patch source, parses each, and applies it in
// order. WAD/mod lumps first, then external files (later sources layer over
// earlier). wadPath is the on-disk path of the loaded WAD ("" if none).
func (g *Game) loadDehacked(w *wad.WAD, mods *vfs.FS, wadPath string) {
	var blobs [][]byte
	names := []string{"DEHACKED"}

	if w != nil {
		for i := range w.Entries {
			if w.Entries[i].Name == "DEHACKED" {
				blobs = append(blobs, w.Lump(i))
			}
		}
	}
	if mods != nil {
		for _, n := range names {
			blobs = append(blobs, mods.Lumps(n)...)
		}
	}
	for _, p := range externalDehFiles(wadPath) {
		if b, err := os.ReadFile(p); err == nil {
			log.Printf("dehacked: reading %s", filepath.Base(p))
			blobs = append(blobs, b)
		}
	}
	if len(blobs) == 0 {
		return
	}

	for _, b := range blobs {
		patch, errs := dehacked.Parse(b)
		for _, e := range errs {
			log.Printf("dehacked: %s", e)
		}
		g.applyDehacked(patch)
	}
}

// externalDehFiles lists *.deh / *.bex files in the same directory as the
// WAD whose base name matches the WAD, plus a generic "dehacked.deh". Keeps
// the surprise factor low — it won't sweep in every .deh in a shared folder.
func externalDehFiles(wadPath string) []string {
	if wadPath == "" {
		return nil
	}
	dir := filepath.Dir(wadPath)
	base := strings.TrimSuffix(filepath.Base(wadPath), filepath.Ext(wadPath))
	var out []string
	for _, cand := range []string{base + ".deh", base + ".bex", "dehacked.deh"} {
		p := filepath.Join(dir, cand)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			out = append(out, p)
		}
	}
	return out
}

// applyDehacked mutates the engine tables / Game fields from one parsed
// patch. Safe to call several times (later patches win).
func (g *Game) applyDehacked(p *dehacked.Patch) {
	things := 0
	for n, props := range p.Things {
		t, ok := dehThingType(n)
		if !ok {
			log.Printf("dehacked: Thing %d edits an actor this build doesn't have — skipped", n)
			continue
		}
		applyThingProps(&mobjInfo[t], props)
		things++
	}

	frames := applyFrameEdits(p.Frames)
	ptrs := applyCodePtrs(p.CodePtrs) + applyPointers(p.Pointers)
	sprites := applySpriteRenames(p.Sprites, p.Texts)
	misc := g.applyDehWeapons(p.Weapons) + g.applyDehAmmo(p.Ammo) + g.applyDehCheat(p.Cheat)
	if len(p.Misc) > 0 {
		misc += g.applyDehMisc(p.Misc)
	}
	if len(p.Sounds) > 0 {
		log.Printf("dehacked: %d numbered Sound block(s) ignored — this build plays sound lumps by "+
			"name and has no sfxinfo_t table for a Sound edit to touch", len(p.Sounds))
	}

	// [PARS]
	if len(p.Pars) > 0 && g.dehPars == nil {
		g.dehPars = map[string]int{}
	}
	for _, par := range p.Pars {
		key := fmt.Sprintf("MAP%02d", par.Map)
		if par.Episode > 0 {
			key = fmt.Sprintf("E%dM%d", par.Episode, par.Map)
		}
		g.dehPars[key] = par.Seconds
	}

	// [STRINGS] — level names (the common, safe subset).
	if g.dehLevelNames == nil {
		g.dehLevelNames = map[string]string{}
	}
	strs := 0
	for mnem, val := range p.Strings {
		if key := levelNameMnemonic(mnem); key != "" {
			g.dehLevelNames[key] = val
			strs++
		}
	}
	log.Printf("dehacked: applied %d Thing stat / %d Frame / %d action-pointer / %d sprite-name / "+
		"%d weapon+ammo+cheat+misc / %d par / %d level-name edit(s)",
		things, frames, ptrs, sprites, misc, len(p.Pars), strs)
}

// applyFrameEdits rewrites states[] entries from "Frame N" blocks: sprite
// number, frame value (low bits = frame index, 0x8000 = fullbright),
// duration in tics, and next-frame link. Returns the count that landed.
func applyFrameEdits(frames map[int]dehacked.Props) int {
	n := 0
	for fn, props := range frames {
		st, ok := dehState(fn)
		if !ok {
			log.Printf("dehacked: Frame %d names a state this build doesn't have — skipped", fn)
			continue
		}
		s := &states[st]
		touched := false
		if v, ok := props.Int("Sprite number"); ok {
			if sp, ok := dehSprite(v); ok {
				s.Sprite = sp
				touched = true
			} else {
				log.Printf("dehacked: Frame %d: sprite number %d not in this build — sprite left as-is", fn, v)
			}
		}
		if v, ok := props.Int("Sprite subnumber"); ok {
			s.Frame = v & 0x7fff
			s.fullbright = v&0x8000 != 0
			touched = true
		}
		if v, ok := props.Int("Duration"); ok {
			s.Tics = v
			touched = true
		}
		if v, ok := props.Int("Next frame"); ok {
			if ns, ok := dehState(v); ok {
				s.Next = ns
				touched = true
			} else {
				log.Printf("dehacked: Frame %d: next frame %d not in this build — link left as-is", fn, v)
			}
		}
		if touched {
			n++
		}
	}
	return n
}

// applyCodePtrs wires BEX [CODEPTR] "Frame N = A_Name" edits onto
// states[N].Action.
func applyCodePtrs(cp map[int]string) int {
	n := 0
	for fn, name := range cp {
		st, ok := dehState(fn)
		if !ok {
			log.Printf("dehacked: [CODEPTR] Frame %d not in this build — skipped", fn)
			continue
		}
		if strings.EqualFold(strings.TrimSpace(name), "NULL") {
			states[st].Action = nil
			n++
			continue
		}
		fn2, ok := dehAction(name)
		if !ok {
			log.Printf("dehacked: [CODEPTR] Frame %d = %s: action not implemented here — skipped", fn, name)
			continue
		}
		states[st].Action = fn2
		n++
	}
	return n
}

// applyPointers handles old-style "Pointer N (Frame N) / Codep Frame = M":
// frame N takes the action frame M currently has.
func applyPointers(ptr map[int]int) int {
	n := 0
	for fn, src := range ptr {
		dst, ok := dehState(fn)
		if !ok {
			log.Printf("dehacked: Pointer for Frame %d not in this build — skipped", fn)
			continue
		}
		ss, ok := dehState(src)
		if !ok {
			log.Printf("dehacked: Pointer Frame %d <- Frame %d: source not in this build — skipped", fn, src)
			continue
		}
		states[dst].Action = states[ss].Action
		n++
	}
	return n
}

// applySpriteRenames applies BEX [SPRITES] entries and any 4-char old-style
// Text substitution whose old text is a live sprite prefix, by rewriting
// spriteNames[] in place (mobj.go reads it per-frame, so this takes effect
// live) and refreshing the reverse lookup.
func applySpriteRenames(bex map[int]string, texts []dehacked.TextSub) int {
	n := 0
	rename := func(old, neu string) bool {
		sp, ok := engineSpriteByName[old]
		if !ok || len(neu) != 4 {
			return false
		}
		spriteNames[sp] = neu
		delete(engineSpriteByName, old)
		engineSpriteByName[neu] = sp
		return true
	}
	for idx, neu := range bex {
		if idx < 0 || idx >= len(doomSpriteNames) {
			continue
		}
		if rename(doomSpriteNames[idx], strings.ToUpper(strings.TrimSpace(neu))) {
			n++
		}
	}
	for _, t := range texts {
		if len(t.Old) == 4 && len(t.New) == 4 && rename(strings.ToUpper(t.Old), strings.ToUpper(t.New)) {
			n++
		}
	}
	return n
}

// applyThingProps applies the DeHackEd stat fields that don't reference a
// frame number.
func applyThingProps(mi *MobjInfo, props dehacked.Props) {
	if v, ok := props.Int("Hit points"); ok {
		mi.SpawnHealth = v
	}
	if v, ok := props.Int("Speed"); ok {
		mi.Speed = float64(v)
	}
	if v, ok := props.Int("Width"); ok {
		mi.Radius = float64(v) / 65536 // DeHackEd stores sizes in 16.16 fixed point
	}
	if v, ok := props.Int("Height"); ok {
		mi.Height = float64(v) / 65536
	}
	if v, ok := props.Int("Mass"); ok {
		mi.Mass = v
	}
	if v, ok := props.Int("Missile damage"); ok {
		mi.Damage = v
	}
	if v, ok := props.Int("Reaction time"); ok {
		mi.ReactionTime = v
	}
	if v, ok := props.Int("Pain chance"); ok {
		mi.PainChance = v
	}
	if v, ok := props.Int("ID #"); ok {
		mi.DoomedNum = v
	}
	if s, ok := props.Get("Bits"); ok {
		mi.Flags = dehFlagBits(dehacked.ParseBits(s))
	}
}

// levelNameMnemonic maps a BEX [STRINGS] level-name key to this engine's
// map key (E1M1 / MAP07), or "" when it isn't one.
func levelNameMnemonic(mnem string) string {
	m := strings.ToUpper(mnem)
	// Doom / Ultimate Doom: HUSTR_E<e>M<m>
	if strings.HasPrefix(m, "HUSTR_E") {
		rest := m[len("HUSTR_"):] // E1M1
		if len(rest) == 4 && rest[0] == 'E' && rest[2] == 'M' &&
			rest[1] >= '1' && rest[1] <= '9' && rest[3] >= '1' && rest[3] <= '9' {
			return rest
		}
		return ""
	}
	// Doom II / Final Doom: HUSTR_<n>, PHUSTR_<n>, THUSTR_<n>
	for _, pfx := range []string{"HUSTR_", "PHUSTR_", "THUSTR_"} {
		if strings.HasPrefix(m, pfx) {
			if n, ok := atoiSafe(m[len(pfx):]); ok && n >= 1 && n <= 32 {
				return fmt.Sprintf("MAP%02d", n)
			}
		}
	}
	return ""
}

func atoiSafe(s string) (int, bool) {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	return n, s != ""
}
