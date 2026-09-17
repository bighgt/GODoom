package engine

// Heretic's weapon set, mapped onto the eight Doom weapon slots. A Heretic
// session calls resetWeaponTables() then applyHereticWeapons() from NewGame
// (game.go), which overwrites the package-global Weapons[] and weaponSlots
// in place — the same pattern DeHackEd already uses, so the pristine copies
// below are restored at the top of every NewGame to stop a Heretic session
// leaking its weapons into the next Doom one.
//
// Slot -> Heretic weapon -> Doom index it reuses / ammo type:
//   1  Staff / Gauntlets      wpFist / wpChainsaw   (melee, no ammo)
//   2  Elven Wand             wpPistol             ammo 0  wand crystals
//   3  Ethereal Crossbow      wpShotgun            ammo 1  ethereal arrows
//   4  Dragon Claw            wpChaingun           ammo 2  claw orbs
//   5  Hellstaff              wpPlasma             ammo 3  hellstaff runes
//   6  Phoenix Rod            wpMissile            ammo 4  flame orbs
//   7  Firemace               wpBFG                ammo 5  mace spheres
//
// Fire mechanics reuse the Doom dispatch (beginFire): Melee, a
// ProjectileDef, or the default hitscan path. Frame letters / sounds are
// from gzdoom's zscript/actors/heretic/weapon*.zs; the powered (Tome of
// Power) fire modes are not modelled.

var (
	pristineWeapons = Weapons     // captured at init, before any DEH / Heretic edit
	pristineSlots   = weaponSlots // slice headers; applyHereticWeapons assigns new slices
)

// resetWeaponTables restores the Doom weapon set. Called at the top of
// every NewGame so DEH / Heretic reshaping never carries across sessions.
func resetWeaponTables() {
	Weapons = pristineWeapons
	weaponSlots = pristineSlots
	weaponPickupAmmo = map[int][2]int{
		wpShotgun: {1, 2}, wpChaingun: {0, 2}, wpMissile: {3, 2},
		wpPlasma: {2, 2}, wpBFG: {2, 2}, wpSuperShotgun: {1, 2},
	}
}

// heretic FX projectiles. Speed is map units/sec (Projectile.def.Speed);
// direct-hit damage is (P_Random%8+1)*Damage.
var (
	// hFXWand is the Elven Wand's shot. Vanilla Heretic's Gold Wand is
	// hitscan, but "PUFF" isn't a lump here and a spark at the impact point
	// reads as nothing leaving the wand — so the starting weapon fires a
	// real travelling bolt instead: FX01 (the yellow Gold-Wand fireball
	// sprite, also its A_FireGoldWandPL2 shot), aimed straight down the
	// crosshair by spawnProjectile, glowing warm via projectileLight's
	// default. FX01 C..H are its little ripple impact.
	hFXWand = ProjectileDef{
		Sprite: "FX01A0", Speed: 1500,
		ExplodePrefix: "FX01", ExplodeFrames: []byte{'C', 'D', 'E', 'F', 'G', 'H'}, ExplodeTics: 3,
		ExplodeSound: "GLDHIT", Damage: 2,
	}
	hFXWandP = ProjectileDef{ // Tome of Power: the wide 5-bolt fan
		Sprite: "FX01A0", Speed: 1500,
		ExplodePrefix: "FX01", ExplodeFrames: []byte{'C', 'D', 'E', 'F', 'G', 'H'}, ExplodeTics: 3,
		ExplodeSound: "GLDHIT", Damage: 2, Shots: 5, SpreadRad: 6 * 3.14159 / 180,
	}

	hFXCrossbow = ProjectileDef{
		Sprite: "FX03B1", Speed: 1200,
		ExplodePrefix: "FX03", ExplodeFrames: []byte{'H', 'I', 'J'}, ExplodeTics: 8,
		Damage: 6, Shots: 3, SpreadRad: 4 * 3.14159 / 180, // 1 main bolt + 2 angled (A_FireCrossbowPL1)
	}
	hFXDragonClaw = ProjectileDef{
		Sprite: "FX18A0", Speed: 1500,
		ExplodePrefix: "FX18", ExplodeFrames: []byte{'A', 'B', 'C', 'D', 'E', 'F', 'G'}, ExplodeTics: 3,
		ExplodeSound: "BLSHIT", Damage: 3,
	}
	hFXHellstaff = ProjectileDef{
		Sprite: "FX00A0", Speed: 1000,
		ExplodePrefix: "FX00", ExplodeFrames: []byte{'H', 'I', 'J', 'K', 'L', 'M'}, ExplodeTics: 4,
		ExplodeSound: "HRNHIT", Damage: 4,
	}
	hFXPhoenix = ProjectileDef{
		Sprite: "FX04A1", Speed: 700,
		ExplodePrefix: "FX08", ExplodeFrames: []byte{'A', 'B', 'C', 'D', 'E', 'F', 'G', 'H'}, ExplodeTics: 5,
		ExplodeSound: "PHOHIT", ExplodeRadius: 80, Damage: 14,
	}
	hFXMace = ProjectileDef{
		Sprite: "FX02A0", Speed: 640,
		ExplodePrefix: "FX02", ExplodeFrames: []byte{'F', 'G', 'H', 'I', 'J'}, ExplodeTics: 4,
		ExplodeSound: "LOBHIT", Damage: 6,
	}

	// --- Tome of Power ("powered") modes ---
	hFXCrossbowP = ProjectileDef{
		Sprite: "FX03B1", Speed: 1200,
		ExplodePrefix: "FX03", ExplodeFrames: []byte{'H', 'I', 'J'}, ExplodeTics: 8,
		Damage: 8, Shots: 3, SpreadRad: 5 * 3.14159 / 180,
	}
	hFXDragonClawP = ProjectileDef{
		Sprite: "FX18B0", Speed: 1100,
		ExplodePrefix: "FX18", ExplodeFrames: []byte{'A', 'B', 'C', 'D', 'E', 'F', 'G'}, ExplodeTics: 3,
		ExplodeSound: "BLSHIT", ExplodeRadius: 48, Damage: 5,
	}
	hFXHellstaffP = ProjectileDef{
		Sprite: "FX00B0", Speed: 1300,
		ExplodePrefix: "FX00", ExplodeFrames: []byte{'H', 'I', 'J', 'K', 'L', 'M'}, ExplodeTics: 3,
		ExplodeSound: "HRNHIT", ExplodeRadius: 40, Damage: 4, Shots: 2, SpreadRad: 4 * 3.14159 / 180,
	}
	hFXPhoenixP = ProjectileDef{
		Sprite: "FX04B0", Speed: 900,
		ExplodePrefix: "FX08", ExplodeFrames: []byte{'A', 'B', 'C', 'D'}, ExplodeTics: 3,
		ExplodeSound: "PHOHIT", ExplodeRadius: 40, Damage: 5,
	}
	hFXMaceP = ProjectileDef{
		Sprite: "FX02E0", Speed: 500,
		ExplodePrefix: "FX02", ExplodeFrames: []byte{'C', 'G', 'H', 'I', 'J'}, ExplodeTics: 4,
		ExplodeSound: "PHOHIT", ExplodeRadius: 128, Damage: 20,
	}
)

func applyHereticWeapons() {
	Weapons[wpFist] = WeaponDef{
		Name: "Staff", SpritePrefix: "STFF", FireSound: "STFHIT",
		AmmoType: -1, AmmoCost: 0, Melee: true,
		FireFrames:     []WeaponFrame{{Frame: 'B', Tics: 6}, {Frame: 'C', Tics: 8}, {Frame: 'B', Tics: 8}},
		PoweredMeleeMul: 4,
	}
	Weapons[wpChainsaw] = WeaponDef{
		Name: "Gauntlets", SpritePrefix: "GAUN", FireSound: "GNTFUL",
		AmmoType: -1, AmmoCost: 0, Melee: true,
		FireFrames:      []WeaponFrame{{Frame: 'B', Tics: 4}, {Frame: 'C', Tics: 4}, {Frame: 'D', Tics: 4}, {Frame: 'E', Tics: 4}, {Frame: 'F', Tics: 4}, {Frame: 'C', Tics: 4}},
		IdleFrames:      []WeaponFrame{{Frame: 'A', Tics: 4}},
		IdleSound:       "GNTACT",
		PoweredMeleeMul: 2, PoweredLeech: true,
	}
	Weapons[wpPistol] = WeaponDef{
		Name: "Elven Wand", SpritePrefix: "GWND", FireSound: "GLDHIT",
		AmmoType: 0, AmmoCost: 1,
		FireFrames: []WeaponFrame{{Frame: 'B', Tics: 3}, {Frame: 'C', Tics: 5}, {Frame: 'D', Tics: 3}},
		Projectile: &hFXWand, Powered: &hFXWandP,
	}
	Weapons[wpShotgun] = WeaponDef{
		Name: "Ethereal Crossbow", SpritePrefix: "CRBW", FireSound: "BOWSHT",
		AmmoType: 1, AmmoCost: 1,
		FireFrames: []WeaponFrame{{Frame: 'D', Tics: 6}, {Frame: 'E', Tics: 3}, {Frame: 'F', Tics: 3}, {Frame: 'G', Tics: 3}, {Frame: 'H', Tics: 3}, {Frame: 'A', Tics: 4}, {Frame: 'B', Tics: 4}, {Frame: 'C', Tics: 5}},
		Projectile: &hFXCrossbow, Powered: &hFXCrossbowP,
	}
	Weapons[wpChaingun] = WeaponDef{
		Name: "Dragon Claw", SpritePrefix: "BLSR", FireSound: "BLSSHT",
		AmmoType: 2, AmmoCost: 1,
		FireFrames: []WeaponFrame{{Frame: 'B', Tics: 3}, {Frame: 'C', Tics: 3}, {Frame: 'D', Tics: 2}, {Frame: 'C', Tics: 2}, {Frame: 'B', Tics: 2}},
		Projectile: &hFXDragonClaw, Powered: &hFXDragonClawP,
	}
	Weapons[wpPlasma] = WeaponDef{
		Name: "Hellstaff", SpritePrefix: "HROD", FireSound: "HRNHIT",
		AmmoType: 3, AmmoCost: 1,
		FireFrames: []WeaponFrame{{Frame: 'A', Tics: 4}, {Frame: 'B', Tics: 4}},
		Projectile: &hFXHellstaff, Powered: &hFXHellstaffP,
	}
	Weapons[wpMissile] = WeaponDef{
		Name: "Phoenix Rod", SpritePrefix: "PHNX", FireSound: "PHOPOW",
		AmmoType: 4, AmmoCost: 1,
		FireFrames: []WeaponFrame{{Frame: 'B', Tics: 5}, {Frame: 'C', Tics: 7}, {Frame: 'D', Tics: 4}, {Frame: 'B', Tics: 4}},
		Projectile: &hFXPhoenix, Powered: &hFXPhoenixP,
	}
	Weapons[wpBFG] = WeaponDef{
		Name: "Firemace", SpritePrefix: "MACE", FireSound: "LOBSHT",
		AmmoType: 5, AmmoCost: 1,
		FireFrames: []WeaponFrame{{Frame: 'B', Tics: 4}, {Frame: 'C', Tics: 3}, {Frame: 'D', Tics: 3}, {Frame: 'E', Tics: 3}, {Frame: 'F', Tics: 3}, {Frame: 'C', Tics: 4}},
		Projectile: &hFXMace, Powered: &hFXMaceP,
	}
	// wpSuperShotgun is left as its Doom self — Heretic never spawns one.

	weaponSlots = [8][]int{
		1: {wpFist, wpChainsaw}, // Staff / Gauntlets
		2: {wpPistol},           // Elven Wand
		3: {wpShotgun},          // Ethereal Crossbow
		4: {wpChaingun},         // Dragon Claw
		5: {wpPlasma},           // Hellstaff
		6: {wpMissile},          // Phoenix Rod
		7: {wpBFG},              // Firemace
	}

	// a found weapon grants ~2 "clips" of its own ammo type
	weaponPickupAmmo = map[int][2]int{
		wpPistol: {0, 2}, wpShotgun: {1, 2}, wpChaingun: {2, 2},
		wpPlasma: {3, 2}, wpMissile: {4, 2}, wpBFG: {5, 2},
	}
}

// hereticMaxAmmo is Heretic's per-type cap (doubled by the Bag of Holding):
// 0 wand, 1 crossbow, 2 dragon claw, 3 hellstaff, 4 phoenix, 5 mace.
var hereticMaxAmmo = [numAmmoTypes]int{100, 50, 200, 200, 20, 150}

// hereticPlayerStats is a Heretic fresh spawn: 100 health, no armour, staff
// + wand, 50 wand crystals.
func hereticPlayerStats() PlayerStats {
	ps := PlayerStats{Health: 100, CurrentAmmo: 0, ArtiCurrent: -1}
	ps.MaxAmmo = hereticMaxAmmo
	ps.Ammo[0] = 50
	ps.Weapons[wpFist] = true   // staff
	ps.Weapons[wpPistol] = true // elven wand
	return ps
}
