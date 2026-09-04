package engine

import (
	"strings"

	"twopointfive/engine/dehacked"
)

// DeHackEd tail: the [MISC] section, numbered Weapon / Ammo blocks, and the
// [CHEAT] section. Numbered Sound blocks stay unmapped on purpose — this
// engine plays sound lumps by name and has no sfxinfo_t priority/link
// table for a "Sound N" edit to touch.

// dehMisc holds every tunable a DEH `Misc` section can retune, defaulted to
// vanilla Doom II. g.dm() materialises it lazily so a Game built by hand in
// a test still reads sane values.
type dehMisc struct {
	initialHealth, initialBullets      int
	maxHealth, maxSoulsphere, maxArmor int
	soulsphereHealth, megasphereHealth int
	godModeHealth                      int
	idfaArmor, idfaArmorClass          int
	idkfaArmor, idkfaArmorClass        int
	greenArmorClass, blueArmorClass    int
}

func defaultDehMisc() dehMisc {
	return dehMisc{
		initialHealth: 100, initialBullets: 50,
		maxHealth: 100, maxSoulsphere: 200, maxArmor: 200,
		soulsphereHealth: 100, megasphereHealth: 200,
		godModeHealth: 100,
		idfaArmor:     200, idfaArmorClass: 2,
		idkfaArmor: 200, idkfaArmorClass: 2,
		greenArmorClass: 1, blueArmorClass: 2,
	}
}

func (g *Game) dm() *dehMisc {
	if g.misc == nil {
		m := defaultDehMisc()
		g.misc = &m
	}
	return g.misc
}

// dehMaxAmmoDefault is DefaultPlayerStats's fresh-spawn ceiling; dehMaxAmmo
// is the live copy a DEH `Ammo N` "Max ammo" edits (DefaultPlayerStats and
// backpack doubling both read it).
var (
	dehMaxAmmoDefault = [4]int{200, 50, 300, 50}
	dehMaxAmmo        = dehMaxAmmoDefault
)

func (g *Game) applyDehMisc(m dehacked.Props) int {
	d := g.dm()
	n := 0
	set := func(key string, dst *int) {
		if v, ok := m.Int(key); ok {
			*dst = v
			n++
		}
	}
	set("Initial Health", &d.initialHealth)
	set("Initial Bullets", &d.initialBullets)
	set("Max Health", &d.maxHealth)
	set("Max Armor", &d.maxArmor)
	set("Max Soulsphere", &d.maxSoulsphere)
	set("Soulsphere Health", &d.soulsphereHealth)
	set("Megasphere Health", &d.megasphereHealth)
	set("God Mode Health", &d.godModeHealth)
	set("IDFA Armor", &d.idfaArmor)
	set("IDFA Armor Class", &d.idfaArmorClass)
	set("IDKFA Armor", &d.idkfaArmor)
	set("IDKFA Armor Class", &d.idkfaArmorClass)
	set("Green Armor Class", &d.greenArmorClass)
	set("Blue Armor Class", &d.blueArmorClass)
	if v, ok := m.Int("BFG Cells/Shot"); ok {
		Weapons[wpBFG].AmmoCost = v
		n++
	}

	// NewGame filled g.Player from DefaultPlayerStats before DEH loaded;
	// retro-patch it while it's still that fresh loadout.
	if g.Player.Health == 100 && d.initialHealth != 100 {
		g.Player.Health = d.initialHealth
		g.syncPlayerMobjHealth()
	}
	if g.Player.Ammo[0] == 50 && d.initialBullets != 50 {
		g.Player.Ammo[0] = d.initialBullets
	}
	return n
}

func (g *Game) applyDehAmmo(edits map[int]dehacked.Props) int {
	n := 0
	for idx, props := range edits {
		if idx < 0 || idx > 3 {
			continue
		}
		if v, ok := props.Int("Max ammo"); ok {
			dehMaxAmmo[idx] = v
			if g.Player.MaxAmmo[idx] == dehMaxAmmoDefault[idx] {
				g.Player.MaxAmmo[idx] = v
			}
			n++
		}
		if v, ok := props.Int("Per ammo"); ok {
			clipAmmo[idx] = v
			n++
		}
	}
	return n
}

func (g *Game) applyDehWeapons(edits map[int]dehacked.Props) int {
	n := 0
	for idx, props := range edits {
		if idx < 0 || idx >= numWeapons {
			continue
		}
		w := &Weapons[idx]
		if v, ok := props.Int("Ammo type"); ok {
			if v >= 0 && v <= 3 {
				w.AmmoType = v
			} else {
				w.AmmoType = -1 // id's am_noammo / am_misc
			}
			n++
		}
		if v, ok := props.Int("Ammo per shot"); ok { // MBF21
			w.AmmoCost = v
			n++
		}
	}
	return n
}

// dehCheatKeyToCode maps a DEH [CHEAT] field name to the built-in code it
// replaces. A blank replacement value disables the cheat.
var dehCheatKeyToCode = map[string]string{
	"change music": "idmus", "chainsaw": "idchoppers", "god mode": "iddqd",
	"ammo & keys": "idkfa", "ammo": "idfa",
	"no clipping 1": "idspispopd", "no clipping 2": "idclip",
	"invincibility": "idbeholdv", "berserk": "idbeholds",
	"invisibility": "idbeholdi", "radiation suit": "idbeholdr",
	"auto-map": "idbeholda", "lite-amp goggles": "idbeholdl",
	"behold menu": "idbehold", "level warp": "idclev",
	"player position": "idmypos", "map cheat": "iddt",
}

func (g *Game) applyDehCheat(m dehacked.Props) int {
	n := 0
	for k, v := range m {
		code, ok := dehCheatKeyToCode[strings.ToLower(strings.TrimSpace(k))]
		if !ok {
			continue
		}
		if g.cheatOverride == nil {
			g.cheatOverride = map[string]string{}
		}
		g.cheatOverride[code] = cheatSanitize(v)
		n++
	}
	return n
}

// cheatSanitize keeps only [a-z0-9] (lower-cased) — DEH cheat strings can
// carry a trailing terminator or parameter marker.
func cheatSanitize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// cheat returns the active code for a built-in cheat, honouring any DEH
// [CHEAT] override (which may be "" to disable it).
func (g *Game) cheat(def string) string {
	if g.cheatOverride != nil {
		if v, ok := g.cheatOverride[def]; ok {
			return v
		}
	}
	return def
}

func hasCheat(buf, code string) bool {
	return code != "" && strings.HasSuffix(buf, code)
}
