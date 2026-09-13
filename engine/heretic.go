package engine

import (
	"log"
	"math"

	"twopointfive/assets"
	"twopointfive/render/worldgeo"
	"twopointfive/wad"
)

// Heretic support layered on the Doom engine. Heretic shipped on id's 1993
// engine, so its map format, BSP, most linedef/sector specials and the
// renderer all work unchanged; what differs is the THING set (different
// doomednums, sprites, keys), a couple of linedef specials, the sky per
// episode, the animated liquid flats, and the music lump prefix.
//
// This file makes Heretic *levels* correctly traversable — right
// decorations, working keyed doors, the voxel pack lighting up — but does
// NOT port Heretic's monsters (no AI), weapons or the artifact/inventory
// system. A Heretic weapon THING still spawns whatever Doom weapon shares
// its doomednum (so you have something to fight with); a Heretic monster
// THING is skipped. See game_design.txt.

// isHereticWAD reports whether w is a Heretic IWAD (or a PWAD merged onto
// one) — see wad.WAD.IsHeretic.
func isHereticWAD(w *wad.WAD) bool { return w.IsHeretic() }

// hereticSfx maps the Doom "DS*" sound lump names the engine hard-codes to
// Heretic's own (which carry no "DS" prefix). loadSound applies it when
// g.heretic, so doors, switches, lifts, teleporters, item pickups and the
// (still Doom) weapons all make Heretic noises instead of falling silent.
var hereticSfx = map[string]string{
	"DSDOROPN": "DOROPN", "DSDORCLS": "DORCLS", "DSBDOPN": "DOROPN", "DSBDCLS": "DORCLS",
	"DSPSTART": "PSTART", "DSPSTOP": "PSTOP", "DSSTNMOV": "STNMOV",
	"DSSWTCHN": "SWITCH", "DSSWTCHX": "SWITCH",
	"DSITEMUP": "ITEMUP", "DSWPNUP": "WPNUP", "DSGETPOW": "ARTIUP",
	"DSTELEPT": "TELEPT", "DSOOF": "PLROOF",
	// player
	"DSPLPAIN": "PLRPAI", "DSPLDETH": "PLRDTH", "DSPDIEHI": "GIBDTH", "DSSLOP": "GIBDTH",
	// the still-Doom weapons (see game_design.txt §24) -> the nearest Heretic weapon sfx
	"DSPUNCH": "STFHIT", "DSPISTOL": "GLDHIT", "DSSHOTGN": "BOWSHT", "DSDSHTGN": "BOWSHT",
	"DSPLASMA": "BLSSHT", "DSRLAUNC": "PHOPOW", "DSBFG": "HRNPOW",
	"DSBAREXP": "PHOHIT", "DSRXPLOD": "PHOHIT", "DSFIRXPL": "BLSHIT",
	"DSSAWFUL": "GNTFUL", "DSSAWIDL": "GNTACT", "DSCLAW": "STFHIT",
}

// mobjSpriteName is the 4-letter sprite the renderer draws for mo:
// Mobj.spriteOverride when set (Heretic map objects), else the Doom
// spriteNames[Sprite] entry its state selected.
func mobjSpriteName(mo *Mobj) string {
	if mo.spriteOverride != "" {
		return mo.spriteOverride
	}
	return spriteNames[mo.Sprite]
}

// hereticGift is what a Heretic pickup grants on touch. Heretic's real
// weapons/artifacts aren't ported, so the grants are Doom-equivalents that
// keep a level playable rather than faithful.
type hereticGift int

const (
	hgNone hereticGift = iota
	hgKeyBlue
	hgKeyYellow
	hgKeyGreen
	hgVial      // Crystal Vial — instant +10 (not an inventory item)
	hgAmmoWand  // wand crystals   (ammo 0)
	hgAmmoBow   // ethereal arrows (ammo 1)
	hgAmmoClaw  // claw orbs       (ammo 2)
	hgAmmoRunes // hellstaff runes (ammo 3)
	hgAmmoFlame // flame orbs      (ammo 4)
	hgAmmoMace  // mace spheres    (ammo 5)
	hgBag       // Bag of Holding -> backpack
	hgSilverArm // Silver Shield -> green armour
	hgEnchArm   // Enchanted Shield -> blue armour
	hgAllMap    // Map Scroll -> allmap

	// inventory artifacts (heretic_arti.go)
	hgArtiFlask
	hgArtiUrn
	hgArtiInvuln
	hgArtiInvis
	hgArtiTome
	hgArtiTorch
	hgArtiTimeBomb
	hgArtiEgg
	hgArtiWings
	hgArtiTeleport

	// weapon pickups (grant the mapped Doom slot — see heretic_weapons.go)
	hgWpGauntlets
	hgWpCrossbow
	hgWpDragonClaw
	hgWpHellstaff
	hgWpPhoenix
	hgWpFiremace
)

// giveHeretic applies a hereticGift, returning whether anything was taken
// and the pickup sound. Mirrors pTouchSpecialThing's got/sound contract.
func (g *Game) giveHeretic(gift hereticGift) (got bool, sound string) {
	sound = "DSITEMUP"
	switch gift {
	case hgKeyBlue:
		got = g.giveCard(0)
	case hgKeyYellow:
		got = g.giveCard(1)
	case hgKeyGreen:
		got = g.giveCard(2)
	case hgVial:
		got = g.giveBody(10)
	case hgAmmoWand:
		got = g.giveAmmo(0, clipAmmo[0])
	case hgAmmoBow:
		got = g.giveAmmo(1, clipAmmo[1])
	case hgAmmoClaw:
		got = g.giveAmmo(2, clipAmmo[2])
	case hgAmmoRunes:
		got = g.giveAmmo(3, clipAmmo[3])
	case hgAmmoFlame:
		got = g.giveAmmo(4, clipAmmo[4])
	case hgAmmoMace:
		got = g.giveAmmo(5, clipAmmo[5])
	case hgBag:
		if !g.Player.Backpack {
			g.Player.Backpack = true
			g.Player.MaxAmmo = doubled(g.Player.MaxAmmo)
		}
		for i := range g.Player.Ammo {
			g.giveAmmo(i, clipAmmo[i])
		}
		got = true
	case hgSilverArm:
		got = g.giveArmor(1)
	case hgEnchArm:
		got = g.giveArmor(2)
	case hgAllMap:
		if g.Player.Powers[pwAllMap] == 0 {
			g.Player.Powers[pwAllMap] = 1
			got = true
		}
	case hgArtiFlask:
		got, sound = g.giveArtifact(artiHealth), "ARTIUP"
	case hgArtiUrn:
		got, sound = g.giveArtifact(artiSuperHealth), "ARTIUP"
	case hgArtiInvuln:
		got, sound = g.giveArtifact(artiInvuln), "ARTIUP"
	case hgArtiInvis:
		got, sound = g.giveArtifact(artiInvis), "ARTIUP"
	case hgArtiTome:
		got, sound = g.giveArtifact(artiTome), "ARTIUP"
	case hgArtiTorch:
		got, sound = g.giveArtifact(artiTorch), "ARTIUP"
	case hgArtiTimeBomb:
		got, sound = g.giveArtifact(artiTimeBomb), "ARTIUP"
	case hgArtiEgg:
		got, sound = g.giveArtifact(artiEgg), "ARTIUP"
	case hgArtiWings:
		got, sound = g.giveArtifact(artiWings), "ARTIUP"
	case hgArtiTeleport:
		got, sound = g.giveArtifact(artiTeleport), "ARTIUP"
	case hgWpGauntlets:
		got, sound = g.giveWeapon(wpChainsaw, false), "WPNUP"
	case hgWpCrossbow:
		got, sound = g.giveWeapon(wpShotgun, false), "WPNUP"
	case hgWpDragonClaw:
		got, sound = g.giveWeapon(wpChaingun, false), "WPNUP"
	case hgWpHellstaff:
		got, sound = g.giveWeapon(wpPlasma, false), "WPNUP"
	case hgWpPhoenix:
		got, sound = g.giveWeapon(wpMissile, false), "WPNUP"
	case hgWpFiremace:
		got, sound = g.giveWeapon(wpBFG, false), "WPNUP"
	}
	return got, sound
}

// hereticSpawn describes one Heretic THING: the sprite to draw, its
// collision box, an optional sprite-cycle animation (frames/tics), whether
// it hangs from the ceiling or blocks movement, and — for a pickup — what
// it grants.
type hereticSpawn struct {
	sprite         string
	radius, height float64
	frames, tics   int // frames>1 = cycle Frame 0..frames-1 every tics tics
	ceiling        bool
	solid          bool
	gift           hereticGift
}

// hereticThings maps a Heretic doomednum to how to spawn it. doomednums are
// GZDoom's Heretic DoomEdNums; sprites and sizes are Heretic's info.c.
// Missing doomednums (Heretic monsters 5/6/7/9/15/45/46/64/65/66/68/69/70/
// 90/92, boss/sound-sequence/glitter-generator markers) fall through to
// spawnMapThings' Doom table or are skipped.
var hereticThings = map[int]hereticSpawn{
	// --- keys ---
	79: {sprite: "CKYY", radius: 20, height: 16, gift: hgKeyBlue},
	80: {sprite: "AKYY", radius: 20, height: 16, gift: hgKeyYellow},
	73: {sprite: "BKYY", radius: 20, height: 16, gift: hgKeyGreen},
	// key gizmos (the tall pedestal the key floats above — decoration only)
	94: {sprite: "KGZ1", radius: 16, height: 50, solid: true},
	95: {sprite: "KGZ1", radius: 16, height: 50, solid: true},
	96: {sprite: "KGZ1", radius: 16, height: 50, solid: true},

	// --- health / armour / powerups ---
	81: {sprite: "PTN1", radius: 20, height: 16, gift: hgVial},
	85: {sprite: "SHLD", radius: 20, height: 16, gift: hgSilverArm},
	31: {sprite: "SHD2", radius: 20, height: 16, gift: hgEnchArm},
	8:  {sprite: "BAGH", radius: 20, height: 16, gift: hgBag},
	35: {sprite: "SPMP", radius: 20, height: 16, gift: hgAllMap},

	// --- inventory artifacts (heretic_arti.go) ---
	82: {sprite: "PTN2", radius: 20, height: 16, gift: hgArtiFlask},
	32: {sprite: "SPHL", radius: 20, height: 16, gift: hgArtiUrn},
	84: {sprite: "INVU", radius: 20, height: 16, gift: hgArtiInvuln},
	75: {sprite: "INVS", radius: 20, height: 16, gift: hgArtiInvis},
	86: {sprite: "PWBK", radius: 20, height: 16, gift: hgArtiTome},
	33: {sprite: "TRCH", radius: 20, height: 16, gift: hgArtiTorch},
	34: {sprite: "FBMB", radius: 20, height: 16, gift: hgArtiTimeBomb},
	30: {sprite: "EGGC", radius: 20, height: 16, gift: hgArtiEgg},
	83: {sprite: "SOAR", radius: 20, height: 16, gift: hgArtiWings},
	36: {sprite: "ATLP", radius: 20, height: 16, gift: hgArtiTeleport},

	// --- ammo ---
	10: {sprite: "AMG1", radius: 20, height: 16, gift: hgAmmoWand},
	12: {sprite: "AMG2", radius: 20, height: 16, gift: hgAmmoWand},
	18: {sprite: "AMC1", radius: 20, height: 16, gift: hgAmmoBow},
	19: {sprite: "AMC2", radius: 20, height: 16, gift: hgAmmoBow},
	54: {sprite: "AMB1", radius: 20, height: 16, gift: hgAmmoClaw},
	55: {sprite: "AMB2", radius: 20, height: 16, gift: hgAmmoClaw},
	20: {sprite: "AMS1", radius: 20, height: 16, gift: hgAmmoRunes},
	21: {sprite: "AMS2", radius: 20, height: 16, gift: hgAmmoRunes},
	22: {sprite: "AMP1", radius: 20, height: 16, gift: hgAmmoFlame},
	23: {sprite: "AMP2", radius: 20, height: 16, gift: hgAmmoFlame},
	13: {sprite: "AMM1", radius: 20, height: 16, gift: hgAmmoMace},
	16: {sprite: "AMM2", radius: 20, height: 16, gift: hgAmmoMace},

	// --- decorations (static unless frames>1) ---
	17: {sprite: "SKH1", radius: 20, height: 70, ceiling: true, solid: true}, // skull-on-a-stick, hung
	24: {sprite: "SKH2", radius: 20, height: 60, ceiling: true, solid: true},
	25: {sprite: "SKH3", radius: 20, height: 45, ceiling: true, solid: true},
	26: {sprite: "SKH4", radius: 20, height: 35, ceiling: true, solid: true},
	27: {sprite: "SRTC", radius: 12, height: 54, frames: 3, tics: 4, solid: true}, // serpent torch
	28: {sprite: "CHDL", radius: 20, height: 60, frames: 3, tics: 4, ceiling: true, solid: true},
	29: {sprite: "SMPL", radius: 16, height: 34, solid: true}, // small pillar
	37: {sprite: "STGS", radius: 8, height: 32, solid: true},
	38: {sprite: "STGL", radius: 12, height: 64, solid: true},
	39: {sprite: "STCS", radius: 8, height: 36, ceiling: true, solid: true},
	40: {sprite: "STCL", radius: 12, height: 68, ceiling: true, solid: true},
	43: {sprite: "PPOD", radius: 16, height: 40, solid: true}, // pod (visible only — doesn't burst)
	44: {sprite: "BARL", radius: 12, height: 32, solid: true}, // Heretic barrel
	47: {sprite: "BRPL", radius: 14, height: 128, solid: true},
	48: {sprite: "MOS1", radius: 20, height: 23, ceiling: true},
	49: {sprite: "MOS2", radius: 20, height: 27, ceiling: true},
	50: {sprite: "WTRH", radius: 6, height: 16, frames: 3, tics: 4},   // wall torch
	51: {sprite: "HCOR", radius: 8, height: 104, ceiling: true, solid: true},
	76: {sprite: "KFR1", radius: 16, height: 44, frames: 8, tics: 3, solid: true}, // fire brazier
	87: {sprite: "VLCO", radius: 12, height: 20, frames: 5, tics: 6, solid: true}, // volcano
	2035: {sprite: "PPOD", radius: 16, height: 40, solid: true},                   // pod

	// --- weapon pickups ---
	2005: {sprite: "WGNT", radius: 20, height: 16, gift: hgWpGauntlets},
	2001: {sprite: "WBOW", radius: 20, height: 16, gift: hgWpCrossbow},
	53:   {sprite: "WBLS", radius: 20, height: 16, gift: hgWpDragonClaw},
	2004: {sprite: "WSKL", radius: 20, height: 16, gift: hgWpHellstaff},
	2003: {sprite: "WPHX", radius: 20, height: 16, gift: hgWpPhoenix},
	2002: {sprite: "WMCE", radius: 20, height: 16, gift: hgWpFiremace},
}

// spawnHereticThing spawns one Heretic THING per hereticThings[t.Type].
// Returns false if there's no entry (caller falls back to the Doom table).
func (g *Game) spawnHereticThing(t *wad.Thing) bool {
	hs, ok := hereticThings[int(t.Type)]
	if !ok {
		return false
	}
	z := onFloorZ
	if hs.ceiling {
		z = onCeilingZ
	}
	mo := g.P_SpawnMobj(float64(t.X), float64(t.Y), z, MT_HERETIC)
	mo.Angle = float64(t.Angle) * math.Pi / 180
	mo.spriteOverride = hs.sprite
	mo.overrideFrames, mo.overrideTics = hs.frames, hs.tics
	mo.Radius, mo.Height = hs.radius, hs.height
	mo.hereticGift = hs.gift
	if g.swapWeapons && hs.gift != hgNone {
		g.swapHereticPickupSprite(mo, hs.gift, hs.sprite) // switchWeapons: show the Doom pickup
	}
	mo.Flags = 0
	if hs.ceiling {
		mo.Flags |= MF_SPAWNCEILING | MF_NOGRAVITY
	}
	if hs.solid {
		mo.Flags |= MF_SOLID
	}
	if hs.gift != hgNone {
		mo.Flags |= MF_SPECIAL
	}
	// P_SpawnMobj already seated it with the template's 16u height; re-seat
	// now that the real height is known (matters for a ceiling-hung thing).
	if hs.ceiling {
		mo.Z = mo.CeilingZ - mo.Height
	}
	mo.prevZ = mo.Z
	return true
}

// hereticDecoLights maps a Heretic decoration's sprite prefix to the light
// it emits, keyed the way buildStaticLights needs it: every Heretic
// decoration is one MT_HERETIC mobj distinguished only by spriteOverride, so
// the Doom decoLights table (keyed by mobjType) can't reach them. Warm
// fire-flicker for every torch/brazier; colours/sizes echo the Doom
// decoLights entries so a Heretic level's torches sit in the same lighting
// register as a Doom level's. See gzdoom's heretic gldefs for the originals.
type hereticDecoLightDef struct {
	decoLightDef
	// flameZ is the emitter height above the thing's feet (map units) — a
	// Heretic torch THING sits on the floor but its flame is up a wall /
	// on a stand, so mo.Height*0.72 (the Doom decoration rule) would light
	// the floor, not where the fire is.
	flameZ float64
}

var hereticDecoLights = map[string]hereticDecoLightDef{
	"WTRH": {decoLightDef{1.00, 0.48, 0.20, 150, 0.95, true}, 58}, // wall torch
	"SRTC": {decoLightDef{1.00, 0.50, 0.22, 190, 1.20, true}, 46}, // serpent (two-faced) torch
	"KFR1": {decoLightDef{1.00, 0.46, 0.16, 230, 1.45, true}, 40}, // fire brazier
	"CHDL": {decoLightDef{1.00, 0.55, 0.28, 200, 1.10, true}, 26}, // chandelier (ceiling-hung: measured DOWN from the ceiling)
	"VLCO": {decoLightDef{1.00, 0.38, 0.14, 170, 1.00, true}, 18}, // volcano
}

// hereticDecoLight returns the emitter for a Heretic decoration mobj, if it
// is one that glows, plus the emitter Z (world height for the flame).
// buildStaticLights calls this before the Doom decoLights lookup for
// MT_HERETIC mobjs.
func hereticDecoLight(mo *Mobj) (d decoLightDef, z float64, ok bool) {
	if mo.Type != MT_HERETIC || mo.spriteOverride == "" {
		return decoLightDef{}, 0, false
	}
	hd, ok := hereticDecoLights[mo.spriteOverride]
	if !ok {
		return decoLightDef{}, 0, false
	}
	if mo.Flags&MF_SPAWNCEILING != 0 {
		return hd.decoLightDef, mo.CeilingZ - hd.flameZ, true // chandelier: below the ceiling
	}
	return hd.decoLightDef, mo.Z + hd.flameZ, true
}

// hereticTorchFlames names the Heretic props whose reikall voxel model has
// no usable flame — a voxelized WTRH / SRTC is just the bare bracket, KFR1 a
// dim ember. When one of these voxelizes, the world collectors also emit
// VoxelSet.HereticTorchFlame() (reikall's standalone TFULA flame) on top,
// emissive and gently turning, so the torch reads as lit. scale multiplies
// the config voxel scale; lift nudges the flame off "centred on the host
// model's visual top" (map units, applied at the final scale).
var hereticTorchFlames = map[string]struct{ scale, lift float64 }{
	"WTRH": {0.70, 0},   // wall torch
	"SRTC": {0.85, -2},  // serpent (two-faced) torch
	"KFR1": {1.70, -6},  // fire brazier
	"CHDL": {1.00, -30}, // chandelier — flames ring the underside
	"VLCO": {1.50, -6},  // volcano
}

// tfulaBodyMidZ is reikall's TFULA flame body centre, in voxel units above
// the model's own origin (ZSiz 20, ZPivot 42 -> visible span ~22.5..41.5).
const tfulaBodyMidZ = 32.0

// hereticFlameFor returns the overlay flame draw for a voxelized Heretic
// torch: host is the torch's own model, drawn at hostScale with its feet at
// baseZ; t is the free-running render clock (seconds) for the spin/flicker.
// ok is false when sprite takes no add-on flame or the pack ships none.
func (g *Game) hereticFlameFor(sprite string, host *assets.VoxelModel, x, y, baseZ, hostScale, t float64) (worldgeo.VoxelInstance, bool) {
	fl, want := hereticTorchFlames[sprite]
	if !want || host == nil || g.Voxels == nil {
		return worldgeo.VoxelInstance{}, false
	}
	fm := g.Voxels.HereticTorchFlame()
	if fm == nil {
		return worldgeo.VoxelInstance{}, false
	}
	vs := g.VoxelScale
	if vs <= 0 {
		vs = 1
	}
	pulse := 1 + 0.06*math.Sin(t*11.0)
	flScale := vs * fl.scale * pulse
	// The host model floats to its real wall/stand height through its own
	// KVX pivot, so its visible top is baseZ + (ZPivot-0.5)*hostScale, not
	// just ZSiz. Seat the flame body's midpoint there, then apply lift/bob.
	hostTopZ := baseZ + (host.ZPivot-0.5)*hostScale
	z := hostTopZ - tfulaBodyMidZ*flScale + fl.lift + 1.5*math.Sin(t*7.3)
	return worldgeo.VoxelInstance{
		Model:      fm,
		X:          float32(x),
		Y:          float32(y),
		Z:          float32(z),
		Yaw:        float32(t * 1.7),
		Scale:      float32(flScale),
		Light:      1,
		FullBright: true, // always visibly alight; the warm point light at
		// hereticDecoLights flameZ does the colour work on the wall around it
	}, true
}

// hereticSkyTexture returns the sky TEXTURE1 name Heretic's p_setup.c picks
// for an episode: E1->SKY1, E2->SKY2, E3->SKY3, and E4/E5/E6 reuse
// SKY1/SKY3/SKY3 (Heretic ships only SKY1..SKY3). "" means "not an ExMy
// name, leave the default".
func hereticSkyTexture(mapName string) string {
	if len(mapName) < 4 || (mapName[0] != 'E' && mapName[0] != 'e') {
		return ""
	}
	switch mapName[1] {
	case '1', '4':
		return "SKY1"
	case '2':
		return "SKY2"
	case '3', '5', '6':
		return "SKY3"
	}
	return "SKY1"
}

func init() {
	// A tiny sanity check that MT_HERETIC's template is wired so
	// P_SpawnMobj(…, MT_HERETIC) enters a drawable (non-S_NULL) state.
	if mobjInfo[MT_HERETIC].SpawnState == S_NULL {
		log.Println("engine: BUG — MT_HERETIC has no spawn state; Heretic things won't draw")
	}
}
