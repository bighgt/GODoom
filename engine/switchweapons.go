package engine

// config.SwitchWeapons (game_design.txt §24) swaps the two games' arsenals.
// The weapon TABLE swap and the cross-game viewmodel/sound art are handled
// in NewGame / cmd/engine; this file re-skins the pickup THINGS the map
// places on the floor so a Doom map's shotgun-on-a-pedestal reads as the
// Ethereal Crossbow you'll actually pick up, and vice versa.
//
// Only the sprite changes: the pickup mobj keeps its type, so the touch
// handler (pickup.go / giveHeretic) still fires and — because giveWeapon is
// keyed by SLOT and the table is already swapped — grants the right weapon.

// weaponPickupSkin is a weapon slot's floor-pickup sprite in each game;
// switchWeapons shows the other one. frames > 1 bobs/cycles off the level
// clock (the Firemace pickup animates).
type weaponPickupSkin struct {
	doom, heretic   string
	doomF, hereticF int
}

var weaponPickupSkins = map[int]weaponPickupSkin{
	wpChainsaw:     {"CSAW", "WGNT", 1, 1}, // chainsaw  <-> Gauntlets
	wpShotgun:      {"SHOT", "WBOW", 1, 1}, // shotgun   <-> Ethereal Crossbow
	wpChaingun:     {"MGUN", "WBLS", 1, 1}, // chaingun  <-> Dragon Claw
	wpMissile:      {"LAUN", "WSKL", 1, 1}, // rocket    <-> Hellstaff
	wpPlasma:       {"PLAS", "WPHX", 1, 1}, // plasma    <-> Phoenix Rod
	wpBFG:          {"BFUG", "WMCE", 1, 5}, // BFG       <-> Firemace (WMCE A..E)
	wpSuperShotgun: {"SGN2", "WBOW", 1, 1}, // SSG has no Heretic peer -> Crossbow
}

// doomWeaponPickupSlot: a Doom weapon-pickup mobj type -> its weapon slot.
var doomWeaponPickupSlot = map[mobjType]int{
	MT_SHOTGUN:      wpShotgun,
	MT_CHAINGUN:     wpChaingun,
	MT_MISC27:       wpMissile,
	MT_MISC28:       wpPlasma,
	MT_MISC25:       wpBFG,
	MT_MISC26:       wpChainsaw,
	MT_SUPERSHOTGUN: wpSuperShotgun,
}

// ammoPickupSkin is one ammo type's small/large pickup sprite in each game.
type ammoPickupSkin struct {
	dSmall, dLarge string
	hSmall, hLarge string
}

var ammoPickupSkins = [4]ammoPickupSkin{
	0: {"CLIP", "AMMO", "AMG1", "AMG2"}, // bullets      <-> wand crystals
	1: {"SHEL", "SBOX", "AMC1", "AMC2"}, // shells       <-> ethereal arrows
	2: {"CELL", "CELP", "AMB1", "AMB2"}, // cells        <-> claw orbs
	3: {"ROCK", "BROK", "AMS1", "AMS2"}, // rockets      <-> hellstaff runes
}

// doomAmmoPickup: a Doom ammo mobj type -> (ammo type, is it the big box).
var doomAmmoPickup = map[mobjType]struct {
	t     int
	large bool
}{
	MT_CLIP:   {0, false},
	MT_MISC17: {0, true},
	MT_MISC22: {1, false},
	MT_MISC23: {1, true},
	MT_MISC20: {2, false},
	MT_MISC21: {2, true},
	MT_MISC18: {3, false},
	MT_MISC19: {3, true},
}

// setSwapSprite re-skins a pickup mobj to name, animating if frames > 1.
// The mobj's own state stays put (Doom pickups sit on one -1-tic frame, and
// spawnHereticThing already froze the MT_HERETIC one), so mobjSpriteName's
// spriteOverride and the mobjTick frame cycler own the look from here.
func setSwapSprite(mo *Mobj, name string, frames int) {
	if name == "" {
		return
	}
	mo.spriteOverride = name
	if frames > 1 {
		mo.overrideFrames, mo.overrideTics = frames, 4
	} else {
		mo.overrideFrames, mo.overrideTics = 0, 0
	}
}

// swapDoomPickupSprite re-skins a just-spawned Doom pickup (typ) to its
// Heretic counterpart — a Doom map played with the Heretic arsenal. No-op
// for anything that isn't a weapon / ammo / backpack pickup.
func (g *Game) swapDoomPickupSprite(mo *Mobj, typ mobjType) {
	if wp, ok := doomWeaponPickupSlot[typ]; ok {
		if s, ok := weaponPickupSkins[wp]; ok {
			setSwapSprite(mo, s.heretic, s.hereticF)
		}
		return
	}
	if a, ok := doomAmmoPickup[typ]; ok {
		s := ammoPickupSkins[a.t]
		if a.large {
			setSwapSprite(mo, s.hLarge, 1)
		} else {
			setSwapSprite(mo, s.hSmall, 1)
		}
		return
	}
	if typ == MT_MISC24 { // backpack -> Bag of Holding
		setSwapSprite(mo, "BAGH", 1)
	}
}

// swapHereticPickupSprite re-skins a just-spawned Heretic pickup (whose
// grant is gift, original sprite hsprite) to its Doom counterpart — a
// Heretic map played with the Doom arsenal.
func (g *Game) swapHereticPickupSprite(mo *Mobj, gift hereticGift, hsprite string) {
	large := len(hsprite) >= 4 && hsprite[len(hsprite)-1] == '2' // AMx2 = the big one
	switch gift {
	case hgWpGauntlets:
		setSwapSprite(mo, weaponPickupSkins[wpChainsaw].doom, 1)
	case hgWpCrossbow:
		setSwapSprite(mo, weaponPickupSkins[wpShotgun].doom, 1)
	case hgWpDragonClaw:
		setSwapSprite(mo, weaponPickupSkins[wpChaingun].doom, 1)
	case hgWpHellstaff:
		setSwapSprite(mo, weaponPickupSkins[wpMissile].doom, 1)
	case hgWpPhoenix:
		setSwapSprite(mo, weaponPickupSkins[wpPlasma].doom, 1)
	case hgWpFiremace:
		setSwapSprite(mo, weaponPickupSkins[wpBFG].doom, 1)
	case hgAmmoWand:
		setSwapSprite(mo, pick(ammoPickupSkins[0].dLarge, ammoPickupSkins[0].dSmall, large), 1)
	case hgAmmoBow:
		setSwapSprite(mo, pick(ammoPickupSkins[1].dLarge, ammoPickupSkins[1].dSmall, large), 1)
	case hgAmmoClaw:
		setSwapSprite(mo, pick(ammoPickupSkins[2].dLarge, ammoPickupSkins[2].dSmall, large), 1)
	case hgAmmoRunes:
		setSwapSprite(mo, pick(ammoPickupSkins[3].dLarge, ammoPickupSkins[3].dSmall, large), 1)
	case hgBag:
		setSwapSprite(mo, "BPAK", 1) // Bag of Holding -> backpack
		// hgAmmoFlame / hgAmmoMace (Phoenix / Firemace ammo) have no Doom
		// equivalent — leave the Heretic sprite; a Doom-arsenal player just
		// can't use them.
	}
}

func pick(a, b string, first bool) string {
	if first {
		return a
	}
	return b
}
