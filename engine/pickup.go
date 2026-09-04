package engine

import (
	"log"
	"math"
	"os"
)

// pickupDebug logs, when TPF_PICKUP_DEBUG is set, every MF_SPECIAL thing the
// player's body reaches each tic and why it was or wasn't collected — a
// diagnostic for "I can't pick this up" reports.
var pickupDebug = os.Getenv("TPF_PICKUP_DEBUG") != ""

// Item pickups, ported from id's p_inter.c. Each tic checkItemPickups scans
// for an MF_SPECIAL thing the player's body overlaps and runs
// pTouchSpecialThing on it (armour, health, ammo, keys, weapons, powers);
// updatePowers counts the timed powers down.

// Power slots (id's powertype_t) and their durations in 35Hz tics.
const (
	pwInvulnerability = iota
	pwStrength        // berserk — a flag, no timer
	pwInvisibility
	pwIronFeet // radiation suit
	pwAllMap
	pwInfrared
	numPowers
)

const (
	invulnTics = 30 * 35
	invisTics  = 60 * 35
	ironTics   = 60 * 35
	infraTics  = 120 * 35
)

// clipAmmo is how much one "clip" of each ammo type is worth
// (bullets, shells, cells, rockets) — id's clipammo[].
var clipAmmo = [4]int{10, 4, 20, 1}

// weaponPickupAmmo: {ammo type, clips} keyed by weapon index (wp*). id's
// P_GiveWeapon always gives two clips of the weapon's ammo for a found
// weapon (one for a monster-dropped one); each clip is clipAmmo[type]
// rounds. Fist and chainsaw take no ammo and aren't listed.
var weaponPickupAmmo = map[int][2]int{
	wpShotgun:      {1, 2}, // 8 shells
	wpChaingun:     {0, 2}, // 20 bullets
	wpMissile:      {3, 2}, // 2 rockets
	wpPlasma:       {2, 2}, // 40 cells
	wpBFG:          {2, 2}, // 40 cells
	wpSuperShotgun: {1, 2}, // 8 shells
}

// pickupSweepMax caps the length of the movement segment checkItemPickups
// sweeps for pickups. A legitimate per-tic move tops out near 20 map units
// (moveTopSpeed * runMultiplier / 35), and one render frame's move can't
// exceed moveTopSpeed*runMultiplier*maxFrameDelta ~= 60; anything longer is
// a teleport or a level load, where sweeping a straight line through the
// map would wrongly vacuum up every item that happens to lie on it.
const pickupSweepMax = 96.0

// checkItemPickups picks up any MF_SPECIAL thing the player touched since
// the last call. It runs at the 35Hz tic rate, but the camera moves every
// render frame, so testing only the current point misses items the player
// ran across between two samples. id never had this gap — it calls
// P_TouchSpecialThing from PIT_CheckThing partway through every move. This
// reproduces that by also testing the segment from the previous sampled
// position to the current one, so a pickup is taken if the player's body
// overlapped the item at the end point (the exact vanilla box test) or
// anywhere along the path.
func (g *Game) checkItemPickups() {
	p := g.playerMobj
	if p == nil || g.Player.Health <= 0 {
		return
	}

	fromX, fromY := g.pickupPrevX, g.pickupPrevY
	if !g.pickupPrevSet {
		fromX, fromY = p.X, p.Y
		g.pickupPrevSet = true
	}
	g.pickupPrevX, g.pickupPrevY = p.X, p.Y
	if math.Hypot(p.X-fromX, p.Y-fromY) > pickupSweepMax {
		fromX, fromY = p.X, p.Y // teleport / respawn — point test only
	}

	for _, mo := range g.mobjs {
		if mo.removed || mo.Flags&MF_SPECIAL == 0 {
			continue
		}
		block := mo.Radius + p.Radius
		onIt := math.Abs(mo.X-p.X) < block && math.Abs(mo.Y-p.Y) < block
		segDist := distancePointToSegment(mo.X, mo.Y, fromX, fromY, p.X, p.Y)
		if !onIt && segDist >= block {
			if pickupDebug && segDist < block*3 {
				log.Printf("pickup: near %s (%s) — closest approach %.0f units, pickup box needs < %.0f",
					itemLabel(mo), spriteNames[mo.Sprite], segDist, block)
			}
			continue
		}
		// id's reach test: the item is out of reach only if its base is
		// above the player's head or more than 8 units below the player's
		// feet (delta > toucher->height || delta < -8).
		if delta := mo.Z - p.Z; delta > p.Height || delta < -8 {
			if pickupDebug {
				log.Printf("pickup: %s (%s) in xy-reach but z-blocked — item feet %.0f, player feet %.0f, delta %.0f (limit +%.0f/-8)",
					itemLabel(mo), spriteNames[mo.Sprite], mo.Z, p.Z, delta, p.Height)
			}
			continue
		}
		g.pTouchSpecialThing(mo, p)
		if pickupDebug {
			if mo.removed {
				log.Printf("pickup: collected %s (%s)", itemLabel(mo), spriteNames[mo.Sprite])
			} else {
				log.Printf("pickup: touched %s (%s, type %d) — not taken (counter already full, or unrecognised type)",
					itemLabel(mo), spriteNames[mo.Sprite], mo.Type)
			}
		}
	}
}

func itemLabel(mo *Mobj) string {
	if mo.Info != nil && mo.Info.Name != "" {
		return mo.Info.Name
	}
	return "item"
}

// updatePowers counts timed powers down and undoes their effects on expiry.
func (g *Game) updatePowers() {
	for i := range g.Player.Powers {
		if i == pwStrength {
			continue // berserk is permanent once picked up
		}
		if g.Player.Powers[i] > 0 {
			g.Player.Powers[i]--
			if g.Player.Powers[i] == 0 && i == pwInvisibility && g.playerMobj != nil {
				g.playerMobj.Flags &^= MF_SHADOW
			}
		}
	}
}

func (g *Game) pTouchSpecialThing(special, toucher *Mobj) {
	got := false
	sound := "DSITEMUP"

	switch special.Type {
	// Armour.
	case MT_MISC0:
		got = g.giveArmor(1)
	case MT_MISC1:
		got = g.giveArmor(2)
	case MT_MISC3: // armour bonus (helmet) — id's SPR_BON2: +1, cap Max Armor
		g.Player.Armor++
		if g.Player.Armor > g.dm().maxArmor {
			g.Player.Armor = g.dm().maxArmor
		}
		if g.Player.ArmorType == 0 {
			g.Player.ArmorType = g.dm().greenArmorClass
		}
		got = true

	// Health.
	case MT_MISC2: // health bonus — id's SPR_BON1: +1, cap Max Soulsphere
		g.Player.Health++
		if g.Player.Health > g.dm().maxSoulsphere {
			g.Player.Health = g.dm().maxSoulsphere
		}
		g.syncPlayerMobjHealth()
		got = true
	case MT_MISC10: // stimpack
		got = g.giveBody(10)
	case MT_MISC11: // medikit
		got = g.giveBody(25)
	case MT_MISC12: // soulsphere
		g.Player.Health += g.dm().soulsphereHealth
		if g.Player.Health > g.dm().maxSoulsphere {
			g.Player.Health = g.dm().maxSoulsphere
		}
		g.syncPlayerMobjHealth()
		got, sound = true, "DSGETPOW"
	case MT_MEGA: // megasphere
		g.Player.Health = g.dm().megasphereHealth
		g.Player.Armor, g.Player.ArmorType = g.dm().maxArmor, g.dm().blueArmorClass
		g.syncPlayerMobjHealth()
		got, sound = true, "DSGETPOW"

	// Powers. id plays sfx_getpow for every one of these.
	case MT_INV:
		g.Player.Powers[pwInvulnerability] = invulnTics
		got, sound = true, "DSGETPOW"
	case MT_MISC13: // berserk
		g.Player.Powers[pwStrength] = 1
		g.giveBody(100)
		got, sound = true, "DSGETPOW"
	case MT_INS: // partial invisibility
		g.Player.Powers[pwInvisibility] = invisTics
		toucher.Flags |= MF_SHADOW
		got, sound = true, "DSGETPOW"
	case MT_MISC14: // radiation suit
		g.Player.Powers[pwIronFeet] = ironTics
		got, sound = true, "DSGETPOW"
	case MT_MISC15: // computer area map — id's P_GivePower refuses a second one
		if g.Player.Powers[pwAllMap] == 0 {
			g.Player.Powers[pwAllMap] = 1
			got, sound = true, "DSGETPOW"
		}
	case MT_MISC16: // light-amp visor
		g.Player.Powers[pwInfrared] = infraTics
		got, sound = true, "DSGETPOW"

	// Keys. id always removes the key thing in single-player, whether or
	// not the key was already held (giveCard just no-ops on a duplicate).
	case MT_MISC4:
		g.giveCard(0) // blue card
		got = true
	case MT_MISC5:
		g.giveCard(2) // red card
		got = true
	case MT_MISC6:
		g.giveCard(1) // yellow card
		got = true
	case MT_MISC7:
		g.giveCard(4) // yellow skull
		got = true
	case MT_MISC8:
		g.giveCard(5) // red skull
		got = true
	case MT_MISC9:
		g.giveCard(3) // blue skull
		got = true

	// Ammo. giveAmmo takes an absolute count; clipAmmo[t] is one clip.
	case MT_CLIP:
		n := clipAmmo[0]
		if special.Flags&MF_DROPPED != 0 {
			n /= 2
		}
		got = g.giveAmmo(0, n)
	case MT_MISC17:
		got = g.giveAmmo(0, clipAmmo[0]*5)
	case MT_MISC22:
		got = g.giveAmmo(1, clipAmmo[1])
	case MT_MISC23:
		got = g.giveAmmo(1, clipAmmo[1]*5)
	case MT_MISC18:
		got = g.giveAmmo(3, clipAmmo[3])
	case MT_MISC19:
		got = g.giveAmmo(3, clipAmmo[3]*5)
	case MT_MISC20:
		got = g.giveAmmo(2, clipAmmo[2])
	case MT_MISC21:
		got = g.giveAmmo(2, clipAmmo[2]*5)
	case MT_MISC24: // backpack — double capacity once, then a clip of everything
		if !g.Player.Backpack {
			for i := 0; i < 4; i++ {
				g.Player.MaxAmmo[i] *= 2
			}
			g.Player.Backpack = true
		}
		for i := 0; i < 4; i++ {
			g.giveAmmo(i, clipAmmo[i])
		}
		got = true

	// Weapons.
	case MT_SHOTGUN:
		got = g.giveWeapon(wpShotgun, special.Flags&MF_DROPPED != 0)
		sound = "DSWPNUP"
	case MT_CHAINGUN:
		got = g.giveWeapon(wpChaingun, special.Flags&MF_DROPPED != 0)
		sound = "DSWPNUP"
	case MT_MISC27:
		got = g.giveWeapon(wpMissile, false)
		sound = "DSWPNUP"
	case MT_MISC28:
		got = g.giveWeapon(wpPlasma, false)
		sound = "DSWPNUP"
	case MT_MISC25:
		got = g.giveWeapon(wpBFG, false)
		sound = "DSWPNUP"
	case MT_MISC26: // chainsaw
		got = g.giveWeapon(wpChainsaw, false)
		sound = "DSWPNUP"
	case MT_SUPERSHOTGUN:
		got = g.giveWeapon(wpSuperShotgun, special.Flags&MF_DROPPED != 0)
		sound = "DSWPNUP"

	default:
		return // not a recognised pickup
	}

	if !got {
		return
	}
	g.playSound(sound)
	g.removeMobj(special)
}

// syncPlayerMobjHealth mirrors PlayerStats.Health onto the player's mobj,
// the way id does after every heal ("mirror mobj health here for Dave").
// runTic already re-syncs each tic, but doing it at the pickup keeps the
// two consistent within the same tic.
func (g *Game) syncPlayerMobjHealth() {
	if g.playerMobj != nil {
		g.playerMobj.Health = g.Player.Health
	}
}

func (g *Game) giveBody(amount int) bool {
	max := g.dm().maxHealth
	if g.Player.Health >= max {
		return false
	}
	g.Player.Health += amount
	if g.Player.Health > max {
		g.Player.Health = max
	}
	g.syncPlayerMobjHealth()
	return true
}

func (g *Game) giveArmor(class int) bool {
	amt := 100 // green
	at := g.dm().greenArmorClass
	if class == 2 {
		amt, at = 200, g.dm().blueArmorClass // blue
	}
	if g.Player.Armor >= amt {
		return false
	}
	g.Player.Armor, g.Player.ArmorType = amt, at
	return true
}

func (g *Game) giveAmmo(ammo, amount int) bool {
	if ammo < 0 || ammo > 3 || amount <= 0 {
		return false
	}
	if g.Player.Ammo[ammo] >= g.Player.MaxAmmo[ammo] {
		return false
	}
	g.Player.Ammo[ammo] += amount
	if g.Player.Ammo[ammo] > g.Player.MaxAmmo[ammo] {
		g.Player.Ammo[ammo] = g.Player.MaxAmmo[ammo]
	}
	return true
}

func (g *Game) giveCard(key int) bool {
	if g.Player.Keys[key] {
		return false
	}
	g.Player.Keys[key] = true
	return true
}

func (g *Game) giveWeapon(weapon int, dropped bool) bool {
	hadWeapon := g.Player.Weapons[weapon]

	gaveAmmo := false
	if a, ok := weaponPickupAmmo[weapon]; ok {
		clips := a[1]
		if dropped {
			clips = a[1] / 2
			if clips < 1 {
				clips = 1
			}
		}
		gaveAmmo = g.giveAmmo(a[0], clipAmmo[a[0]]*clips)
	}

	if hadWeapon {
		return gaveAmmo // already owned — only the ammo counts
	}
	g.Player.Weapons[weapon] = true
	if g.SwitchWeaponOnPickup {
		g.SwitchWeapon(weapon) // auto-equip the newly acquired weapon
	}
	return true
}
