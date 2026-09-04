package engine

import (
	"encoding/binary"
	"strconv"
	"strings"

	"twopointfive/wad"
)

// Animated flats and wall textures — id's P_InitPicAnims, plus BOOM's
// ANIMATED lump.
//
// Vanilla cycles a short run of consecutive flats or wall textures one
// frame every `speed` tics. This engine resolves textures by name every
// frame, so rather than a render-time translation table it rewrites the
// live name in Level.Sectors[i] / Level.Sidedefs[i] each tic — the same
// "write the slice the renderer reads" approach doors and light thinkers
// use. The visible frame is a pure function of levelTime, so it is
// stateless and stays correct across a level reload.
//
// The animation set is the classic Doom / Doom II list (builtinAnimDefs).
// If the WAD ships a BOOM ANIMATED lump, that replaces the built-in list
// entirely (P_InitPicAnims reads ANIMATED in preference to its own table,
// and so do we), which is how a Boom/MBF PWAD defines its own animations.

type texAnimGroup struct {
	frames []string
	speed  int // uniform tics/frame — used when durs == nil
	// durs, when set, is the per-frame tic count (ANIMDEFS `pic ... tics`).
	// prefix is its running sum with prefix[0]=0 and prefix[len]=cycle, so
	// the frame showing at tic t is the last i with prefix[i] <= t%cycle.
	durs   []int
	prefix []int
}

type texAnimRef struct {
	grp *texAnimGroup
	idx int // this frame's index in grp.frames (its phase offset)
}

// wallAnimSlot is one sidedef texture slot whose authored texture is an
// animated frame — recorded once at level load so animateWalls touches
// only the handful of sidedefs that actually animate, not every one.
type wallAnimSlot struct {
	side int32
	slot uint8 // 0 upper, 1 middle, 2 lower
	base string
}

// animDef is one row of the animation table: the first and last frame name
// and the tics per frame. isTexture picks the wall-texture namespace over
// the flat namespace. explicit, when set, is the exact frame list — used
// for the built-in ranges whose names don't step by a trailing digit
// (FIREWALA / FIREWALB / FIREWALL).
type animDef struct {
	isTexture bool
	first     string
	last      string
	speed     int
	explicit  []string
	// durs is the per-frame tic list for an ANIMDEFS `pic`-list animation
	// (len == len(explicit)); nil for a plain range that ticks at `speed`.
	durs []int
}

// builtinAnimDefs is id's P_InitPicAnims table for Doom and Doom II: every
// liquid flat plus every animated wall texture (fire, gore, waterfalls,
// the brain). Used when the WAD has no ANIMATED lump.
var builtinAnimDefs = []animDef{
	// Flats.
	{false, "NUKAGE1", "NUKAGE3", 8, nil, nil},
	{false, "FWATER1", "FWATER4", 8, nil, nil},
	{false, "SWATER1", "SWATER4", 8, nil, nil},
	{false, "LAVA1", "LAVA4", 8, nil, nil},
	{false, "BLOOD1", "BLOOD3", 8, nil, nil},
	{false, "RROCK05", "RROCK08", 8, nil, nil},
	{false, "SLIME01", "SLIME04", 8, nil, nil},
	{false, "SLIME05", "SLIME08", 8, nil, nil},
	{false, "SLIME09", "SLIME12", 8, nil, nil},
	// Wall textures.
	{true, "BLODGR1", "BLODGR4", 8, nil, nil},
	{true, "SLADRIP1", "SLADRIP3", 8, nil, nil},
	{true, "BLODRIP1", "BLODRIP4", 8, nil, nil},
	{true, "FIREWALA", "FIREWALL", 8, []string{"FIREWALA", "FIREWALB", "FIREWALL"}, nil},
	{true, "GSTFONT1", "GSTFONT3", 8, nil, nil},
	{true, "FIRELAV3", "FIRELAVA", 8, []string{"FIRELAV3", "FIRELAVA"}, nil},
	{true, "FIREMAG1", "FIREMAG3", 8, nil, nil},
	{true, "FIREBLU1", "FIREBLU2", 8, nil, nil},
	{true, "ROCKRED1", "ROCKRED3", 8, nil, nil},
	{true, "BFALL1", "BFALL4", 8, nil, nil},
	{true, "SFALL1", "SFALL4", 8, nil, nil},
	{true, "WFALL1", "WFALL4", 8, nil, nil},
	{true, "DBRAIN1", "DBRAIN4", 8, nil, nil},
}

// parseAnimatedLump decodes a BOOM ANIMATED lump: a run of 23-byte records
// terminated by a 0xFF byte. Each record is istexture(1) + endname(9,
// NUL-padded) + startname(9) + speed(uint32 LE). The animation runs
// startname..endname.
func parseAnimatedLump(b []byte) []animDef {
	var out []animDef
	for off := 0; off < len(b); off += 23 {
		if b[off] == 0xFF {
			break
		}
		if off+23 > len(b) {
			break
		}
		rec := b[off : off+23]
		last := lumpName(rec[1:10])
		first := lumpName(rec[10:19])
		if first == "" || last == "" {
			continue
		}
		speed := int(binary.LittleEndian.Uint32(rec[19:23]))
		switch {
		case speed < 1:
			speed = 1
		case speed > 65535:
			// GZDoom reads a speed this large as a request to warp the
			// texture instead; this engine has no warp, so just animate it
			// at the normal rate rather than leaving it effectively frozen.
			speed = 8
		}
		out = append(out, animDef{isTexture: rec[0]&1 != 0, first: first, last: last, speed: speed})
	}
	return out
}

// lumpName trims a NUL/space-padded fixed-width name field to an upper-cased
// Go string (matching wad.cleanName's normalisation).
func lumpName(b []byte) string {
	n := len(b)
	for i, c := range b {
		if c == 0 {
			n = i
			break
		}
	}
	return strings.ToUpper(strings.TrimSpace(string(b[:n])))
}

// chooseAnimDefs picks the animation table: a non-empty ANIMATED lump
// replaces the built-in Doom / Doom II list entirely (matching
// P_InitPicAnims, which reads ANIMATED in place of its own table).
func chooseAnimDefs(animatedLump []byte) []animDef {
	if len(animatedLump) > 0 {
		if defs := parseAnimatedLump(animatedLump); len(defs) > 0 {
			return defs
		}
	}
	return builtinAnimDefs
}

// buildAnimDefs is the animation table for this WAD: the vanilla / BOOM
// ANIMATED base list, then any ZDoom ANIMDEFS `flat` / `texture` blocks
// layered on top (an ANIMDEFS entry for a name overrides an earlier list
// entry for it, since initTexAnims keys groups by frame name and the last
// write wins). ANIMDEFS `warp` lines are handled separately in
// initWarpAnims.
func (g *Game) buildAnimDefs() []animDef {
	raw, _ := g.WAD.Find("ANIMATED")
	defs := chooseAnimDefs(raw)
	if extra := g.animDefsFromANIMDEFS(); len(extra) > 0 {
		defs = append(append([]animDef(nil), defs...), extra...)
	}
	return defs
}

// splitTrailingDigits returns s split into its non-digit prefix and its
// trailing run of digits ("NUKAGE1" -> "NUKAGE","1"; "SLIME09" ->
// "SLIME","09"). No trailing digits -> ("", "").
func splitTrailingDigits(s string) (prefix, digits string) {
	i := len(s)
	for i > 0 && s[i-1] >= '0' && s[i-1] <= '9' {
		i--
	}
	if i == len(s) {
		return "", ""
	}
	return s[:i], s[i:]
}

// expandFrameRange lists first..last inclusive by stepping the shared
// trailing number, preserving its width. Returns nil if the two names don't
// share a prefix and digit width, or the range is backwards / absurd.
func expandFrameRange(first, last string) []string {
	pf, df := splitTrailingDigits(first)
	pl, dl := splitTrailingDigits(last)
	if pf == "" || pf != pl || len(df) != len(dl) {
		return nil
	}
	lo, err1 := strconv.Atoi(df)
	hi, err2 := strconv.Atoi(dl)
	if err1 != nil || err2 != nil || hi < lo || hi-lo > 31 {
		return nil
	}
	out := make([]string, 0, hi-lo+1)
	for n := lo; n <= hi; n++ {
		out = append(out, pf+leftPad(strconv.Itoa(n), len(df)))
	}
	return out
}

func leftPad(s string, w int) string {
	if len(s) >= w {
		return s
	}
	return strings.Repeat("0", w-len(s)) + s
}

// nameRange returns the slice of order between names a and b inclusive,
// whichever way round they appear. nil if either name is absent or the span
// is implausibly long.
func nameRange(order []string, a, b string) []string {
	ia, ib := -1, -1
	for i, n := range order {
		switch n {
		case a:
			ia = i
		case b:
			ib = i
		}
	}
	if ia < 0 || ib < 0 {
		return nil
	}
	if ia > ib {
		ia, ib = ib, ia
	}
	if ib-ia > 63 {
		return nil
	}
	return append([]string(nil), order[ia:ib+1]...)
}

// flatNameRange returns the flat lump names between a and b inclusive in
// WAD directory order — how BOOM's ANIMATED lump defines a flat animation
// whose frames aren't a simple numbered run. nil if a marker lump (empty
// name) falls inside the span.
func (g *Game) flatNameRange(a, b string) []string {
	ia, ib := g.WAD.IndexOf(a), g.WAD.IndexOf(b)
	if ia < 0 || ib < 0 {
		return nil
	}
	if ia > ib {
		ia, ib = ib, ia
	}
	if ib-ia > 63 {
		return nil
	}
	out := make([]string, 0, ib-ia+1)
	for i := ia; i <= ib; i++ {
		n := g.WAD.Entries[i].Name
		if n == "" {
			return nil
		}
		out = append(out, n)
	}
	return out
}

// buildTextureOrder lists every composite wall-texture name in TEXTURE1
// then TEXTURE2 definition order (deduplicated), plus a set for existence
// checks. Parsed straight from the WAD so this works without the renderer's
// assets.Textures (e.g. in tests).
func (g *Game) buildTextureOrder() (order []string, set map[string]bool) {
	set = map[string]bool{}
	for _, lump := range [2]string{"TEXTURE1", "TEXTURE2"} {
		raw, ok := g.WAD.Find(lump)
		if !ok {
			continue
		}
		defs, err := wad.LoadTextureDefs(raw)
		if err != nil {
			continue
		}
		for _, d := range defs {
			if !set[d.Name] {
				set[d.Name] = true
				order = append(order, d.Name)
			}
		}
	}
	return
}

// expandAnimDef turns one animDef into its ordered frame list, or nil if
// any frame is missing from this WAD (so a def for content the WAD doesn't
// ship is simply skipped).
func (g *Game) expandAnimDef(d animDef, texOrder []string, texSet map[string]bool) []string {
	exists := func(name string) bool {
		if d.isTexture {
			return texSet[name]
		}
		_, ok := g.WAD.Find(name)
		return ok
	}
	allExist := func(fs []string) bool {
		for _, f := range fs {
			if !exists(f) {
				return false
			}
		}
		return len(fs) > 0
	}

	if len(d.explicit) > 0 {
		if allExist(d.explicit) {
			return d.explicit
		}
		return nil
	}
	if fs := expandFrameRange(d.first, d.last); fs != nil && allExist(fs) {
		return fs
	}
	// A non-numbered range: take the names between the endpoints in their
	// natural order — texture-definition order for a wall texture, WAD
	// directory order for a flat.
	var fs []string
	if d.isTexture {
		fs = nameRange(texOrder, d.first, d.last)
	} else {
		fs = g.flatNameRange(d.first, d.last)
	}
	if allExist(fs) {
		return fs
	}
	return nil
}

// initTexAnims builds the flat and wall-texture animation groups for this
// WAD, then snapshots which sectors / sidedefs carry an animated frame so
// the current frame can be recomputed statelessly from levelTime. Called
// from spawnSpecials (so: on NewGame and every level load).
func (g *Game) initTexAnims() {
	g.flatAnimByName = map[string]texAnimRef{}
	g.wallAnimByName = map[string]texAnimRef{}
	g.lastAnimHashSet = false // force one static-geometry rebuild on the HW path

	texOrder, texSet := g.buildTextureOrder()

	for _, d := range g.buildAnimDefs() {
		frames := g.expandAnimDef(d, texOrder, texSet)
		if len(frames) < 2 {
			continue // one frame is not an animation
		}
		grp := &texAnimGroup{frames: frames, speed: d.speed}
		if d.durs != nil && len(d.durs) == len(frames) {
			grp.durs = d.durs
			grp.prefix = make([]int, len(frames)+1)
			for i, dt := range d.durs {
				if dt < 1 {
					dt = 1
				}
				grp.prefix[i+1] = grp.prefix[i] + dt
			}
		}
		dst := g.flatAnimByName
		if d.isTexture {
			dst = g.wallAnimByName
		}
		for i, f := range frames {
			dst[f] = texAnimRef{grp: grp, idx: i}
		}
	}

	// ANIMDEFS `warp` / `warp2`: bake pre-distorted frame cycles and register
	// them as groups keyed by the base name (overriding any list animation
	// for it).
	g.initWarpAnims()

	ns := len(g.Level.Sectors)
	g.animBaseFloor = make([]string, ns)
	g.animBaseCeil = make([]string, ns)
	if len(g.flatAnimByName) > 0 {
		for i := range g.Level.Sectors {
			s := &g.Level.Sectors[i]
			if _, ok := g.flatAnimByName[s.FloorTexture]; ok {
				g.animBaseFloor[i] = s.FloorTexture
			}
			if _, ok := g.flatAnimByName[s.CeilingTexture]; ok {
				g.animBaseCeil[i] = s.CeilingTexture
			}
		}
	}

	g.wallAnimSlots = g.wallAnimSlots[:0]
	if len(g.wallAnimByName) > 0 {
		for i := range g.Level.Sidedefs {
			sd := &g.Level.Sidedefs[i]
			for slot, name := range [3]string{sd.UpperTexture, sd.MiddleTexture, sd.LowerTexture} {
				if _, ok := g.wallAnimByName[name]; ok {
					g.wallAnimSlots = append(g.wallAnimSlots, wallAnimSlot{side: int32(i), slot: uint8(slot), base: name})
				}
			}
		}
	}
}

// frameFor resolves an authored flat or wall-texture name to the frame that
// should show at tic t. For a uniform-speed group this is id's
// `basepic + (leveltime/speed + i) % numpics`, where i is the authored
// frame's own index so two surfaces that started on different frames of one
// group animate a step out of phase. For an ANIMDEFS group with per-frame
// durations it walks the running-sum table instead, phase-shifted by the
// authored frame's own start offset.
func (g *Game) frameFor(base string, t int) string {
	ref, ok := g.flatAnimByName[base]
	if !ok {
		if ref, ok = g.wallAnimByName[base]; !ok {
			return base
		}
	}
	grp := ref.grp
	fr := grp.frames
	if grp.durs == nil {
		return fr[(t/grp.speed+ref.idx)%len(fr)]
	}
	cycle := grp.prefix[len(fr)]
	if cycle <= 0 {
		return fr[0]
	}
	tt := (t + grp.prefix[ref.idx]) % cycle
	i := 0
	for i+1 < len(fr) && grp.prefix[i+1] <= tt {
		i++
	}
	return fr[i]
}

// animateFlats rewrites every animated sector's live floor/ceiling flat
// name to the current frame. Called from runTic, after tickSpecials.
func (g *Game) animateFlats() {
	if len(g.flatAnimByName) == 0 {
		return
	}
	for i := range g.Level.Sectors {
		if b := g.animBaseFloor[i]; b != "" {
			g.Level.Sectors[i].FloorTexture = g.frameFor(b, g.levelTime)
		}
		if b := g.animBaseCeil[i]; b != "" {
			g.Level.Sectors[i].CeilingTexture = g.frameFor(b, g.levelTime)
		}
	}
}

// animateWalls rewrites every animated sidedef slot's live texture name to
// the current frame. Called from runTic, right after animateFlats.
func (g *Game) animateWalls() {
	for _, s := range g.wallAnimSlots {
		sd := &g.Level.Sidedefs[s.side]
		name := g.frameFor(s.base, g.levelTime)
		switch s.slot {
		case 0:
			sd.UpperTexture = name
		case 1:
			sd.MiddleTexture = name
		case 2:
			sd.LowerTexture = name
		}
	}
}
