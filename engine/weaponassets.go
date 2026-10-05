package engine

import (
	"sort"

	"twopointfive/raster"
)

// WeaponAssets lists the art and sounds one game's arsenal draws on: the
// 4-letter sprite prefixes (first-person viewmodel and muzzle flash,
// projectile and its explosion, hitscan puff/blood, the weapon and ammo
// pickups lying on the floor) and the sound lumps (fire, idle, projectile
// impact, weapon/ammo pickup). cmd/wad3d embeds exactly these into every
// real-3D WAD so either arsenal works in any game without the other IWAD.
type WeaponAssets struct {
	Sprites []string
	Sounds  []string
}

// ArsenalAssets returns the Doom (Doom + Doom II — the Super Shotgun is
// part of it) and Heretic weapon asset lists, read straight from the
// weapon tables the game itself plays with, plus the few names the attack
// and pickup code uses directly.
//
// It briefly installs the Heretic weapon table to read it and then puts
// the pristine Doom table back (resetWeaponTables), so it must not run
// while a Game is live — it's for offline tools.
func ArsenalAssets() (doom, heretic WeaponAssets) {
	resetWeaponTables()
	doom = collectArsenal(
		[]string{"PUFF", "BLUD", "BFE2"}, // pLineAttack's hitscan puff and blood; aBFGSpray's per-target flash
		[]string{"DSWPNUP", "DSITEMUP"})
	for _, s := range weaponPickupSkins {
		doom.Sprites = append(doom.Sprites, s.doom)
	}
	for _, s := range ammoPickupSkins {
		doom.Sprites = append(doom.Sprites, s.dSmall, s.dLarge)
	}

	applyHereticWeapons()
	heretic = collectArsenal(
		[]string{"PUF1", "PUF2", "PUF3", "PUF4", "BLOD"}, // hereticPuffFrames + spawnAttackBlood
		[]string{"WPNUP", "ITEMUP"})
	resetWeaponTables()
	for _, s := range weaponPickupSkins {
		heretic.Sprites = append(heretic.Sprites, s.heretic)
	}
	for _, hs := range hereticThings {
		if (hs.gift >= hgAmmoWand && hs.gift <= hgAmmoMace) || (hs.gift >= hgWpGauntlets && hs.gift <= hgWpFiremace) {
			heretic.Sprites = append(heretic.Sprites, hs.sprite)
		}
	}

	return doom.dedupe(), heretic.dedupe()
}

// collectArsenal reads every sprite prefix and sound the CURRENT Weapons
// table references, plus the given extras.
func collectArsenal(sprites, sounds []string) WeaponAssets {
	a := WeaponAssets{Sprites: sprites, Sounds: sounds}
	addProj := func(p *ProjectileDef) {
		if p == nil {
			return
		}
		if len(p.Sprite) >= 4 {
			a.Sprites = append(a.Sprites, p.Sprite[:4])
		}
		a.Sprites = append(a.Sprites, p.ExplodePrefix)
		a.Sounds = append(a.Sounds, p.ExplodeSound)
	}
	addFrames := func(fs []WeaponFrame) {
		for _, f := range fs {
			a.Sprites = append(a.Sprites, f.Prefix)
		}
	}
	for i := range Weapons {
		w := &Weapons[i]
		a.Sprites = append(a.Sprites, w.SpritePrefix, w.FlashPrefix)
		addFrames(w.FireFrames)
		for _, fs := range w.FireCycle {
			addFrames(fs)
		}
		addFrames(w.ReleaseFrames)
		addFrames(w.FlashFrames)
		addFrames(w.IdleFrames)
		a.Sounds = append(a.Sounds, w.FireSound, w.IdleSound)
		for _, ts := range w.FireSoundSeq {
			a.Sounds = append(a.Sounds, ts.Sound)
		}
		addProj(w.Projectile)
		addProj(w.Powered)
	}
	return a
}

// dedupe drops empty and repeated names and sorts both lists.
func (a WeaponAssets) dedupe() WeaponAssets {
	clean := func(in []string) []string {
		seen := map[string]bool{}
		var out []string
		for _, s := range in {
			if s != "" && s != raster.NoSpritePrefix && !seen[s] { // TNT1 = no sprite at all
				seen[s] = true
				out = append(out, s)
			}
		}
		sort.Strings(out)
		return out
	}
	return WeaponAssets{Sprites: clean(a.Sprites), Sounds: clean(a.Sounds)}
}
