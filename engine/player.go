package engine

import "twopointfive/raster"

// PlayerStats is the authoritative gameplay-side model of the player's
// combat state — health, armour, ammo, owned weapons, keys, timed powers.
// The engine mutates it (ammo in FireWeapon, ownership in SwitchWeapon,
// everything in pickup.go / damage.go); the renderer only ever sees the
// flat raster.HUDStats snapshot toHUD produces each frame.
type PlayerStats struct {
	Health, Armor int
	// ArmorType: 0 none, 1 green (absorbs 1/3), 2 blue (absorbs 1/2).
	ArmorType int
	// Ammo/MaxAmmo are indexed by ammo type: 0 bullets, 1 shells, 2 cells, 3 rockets.
	Ammo, MaxAmmo [4]int
	// CurrentAmmo selects which of the 4 ammo types the big number next to
	// the face shows (mirrors the current weapon's ammo type); -1 shows
	// nothing there (e.g. fists).
	CurrentAmmo int
	// Weapons[n] reports whether weapon n (a wp* constant — id's
	// weapontype_t) is owned. Fist and pistol are owned from the start.
	Weapons [numWeapons]bool
	// Keys[n]: 0 blue card, 1 yellow card, 2 red card, 3 blue skull, 4
	// yellow skull, 5 red skull.
	Keys [6]bool
	// Powers[pw*] — remaining 35Hz tics for each timed power (pwStrength is
	// a 0/1 flag). Counted down by updatePowers.
	Powers [numPowers]int
	// Backpack: set once the player has taken a backpack, so a second one
	// only tops ammo up instead of doubling MaxAmmo again (id's
	// player->backpack).
	Backpack bool
}

// DefaultPlayerStats is a real Doom fresh-spawn loadout: 100 health, no
// armour, fist + pistol, 50 bullets. Everything else is found via pickups
// (pickup.go).
func DefaultPlayerStats() PlayerStats {
	ps := PlayerStats{Health: 100, CurrentAmmo: 0}
	ps.MaxAmmo = dehMaxAmmo // vanilla {200,50,300,50} unless a DEH Ammo edit changed it
	ps.Ammo[0] = 50         // bullets
	ps.Weapons[wpFist] = true
	ps.Weapons[wpPistol] = true
	return ps
}

// godMode reports whether the invulnerability power is active (for the
// status-bar face).
func (ps PlayerStats) godMode() bool { return ps.Powers[pwInvulnerability] > 0 }

// toHUD flattens the gameplay stats into the renderer's draw-only snapshot.
func (ps PlayerStats) toHUD() raster.HUDStats {
	// The arms widget shows numbers 2..7 = pistol, shotgun, chaingun,
	// rocket, plasma, BFG (wp constants 1..6). Fist and chainsaw aren't on
	// it; the super shotgun shares slot 3 with the regular shotgun (vanilla
	// lights it for either).
	var arms [8]bool
	for n := 2; n <= 7; n++ {
		arms[n] = ps.Weapons[n-1]
	}
	if ps.Weapons[wpSuperShotgun] {
		arms[3] = true
	}
	return raster.HUDStats{
		Health:      ps.Health,
		Armor:       ps.Armor,
		Ammo:        ps.Ammo,
		MaxAmmo:     ps.MaxAmmo,
		CurrentAmmo: ps.CurrentAmmo,
		Weapons:     arms,
		Keys:        ps.Keys,
		GodMode:     ps.godMode(),
	}
}
