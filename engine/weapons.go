package engine

// WeaponDef describes one of the six switchable weapons: its viewmodel
// sprite prefix (e.g. "PISG" -> PISGA0 ready / PISGB0 fire-recoil frame,
// if the WAD has one), muzzle-flash sprite prefix, fire sound lump name,
// and which PlayerStats.Ammo slot/cost firing it consumes.
type WeaponDef struct {
	Name         string
	SpritePrefix string
	FlashPrefix  string
	FireSound    string
	AmmoType     int // index into raster.PlayerStats.Ammo
	AmmoCost     int
}

// Weapons are the six switchable weapons, keys 1-6 in that order. Vanilla
// Doom's fist and chainsaw (its own key 1) aren't counted among these —
// the user-specified "six weapons, keys 1-6" layout this project targets
// deliberately differs from vanilla's seven-slot one; see game_design.txt.
// The chaingun firing DSPISTOL and the BFG's 40-cell cost aren't
// approximations — that's the original's actual data (a well-known bit of
// Doom trivia in the chaingun's case).
var Weapons = [6]WeaponDef{
	{Name: "Pistol", SpritePrefix: "PISG", FlashPrefix: "PISF", FireSound: "DSPISTOL", AmmoType: 0, AmmoCost: 1},
	{Name: "Shotgun", SpritePrefix: "SHTG", FlashPrefix: "SHTF", FireSound: "DSSHOTGN", AmmoType: 1, AmmoCost: 1},
	{Name: "Chaingun", SpritePrefix: "CHGG", FlashPrefix: "CHGF", FireSound: "DSPISTOL", AmmoType: 0, AmmoCost: 1},
	{Name: "Rocket Launcher", SpritePrefix: "MISG", FlashPrefix: "MISF", FireSound: "DSRLAUNC", AmmoType: 3, AmmoCost: 1},
	{Name: "Plasma Rifle", SpritePrefix: "PLSG", FlashPrefix: "PLSF", FireSound: "DSPLASMA", AmmoType: 2, AmmoCost: 1},
	{Name: "BFG9000", SpritePrefix: "BFGG", FlashPrefix: "BFGF", FireSound: "DSBFG", AmmoType: 2, AmmoCost: 40},
}

type weaponPhase int

const (
	weaponLowering weaponPhase = iota
	weaponRaising
	weaponReady
	weaponFiring
)

// weaponRaiseSpeed and weaponFireDuration are this project's own tuning
// (Doom's exact per-weapon frame timing comes from info.c's state tables,
// which this project doesn't replicate — see game_design.txt); chosen to
// read as a quick, responsive raise/lower/fire without needing that data.
const (
	weaponRaiseSpeed   = 3.0 // 0..1 raise-offset units/sec
	weaponFireDuration = 0.3 // seconds the fire pose/flash holds
)

// WeaponState is the equipped weapon's animation state.
type WeaponState struct {
	Current  int
	pending  int // weapon index queued to switch to once lowering completes; -1 if none
	phase    weaponPhase
	Offset   float64 // 0 = fully raised/ready, 1 = fully lowered/hidden — read by raster.DrawWeapon
	fireTime float64
}

// NewWeaponState starts the first weapon (Pistol) already raising in.
func NewWeaponState() WeaponState {
	return WeaponState{Current: 0, pending: -1, phase: weaponRaising, Offset: 1}
}

// SwitchWeapon requests weapon index (0-5) become current, via the same
// lower-then-raise animation Doom used — a no-op if it's already
// current/pending, or firing (Doom doesn't let you switch mid-shot).
// Selecting a weapon also marks it "owned" in the status bar's arms
// widget (Player.Weapons[index+2] lights up yellow instead of gray) —
// there's no real pickup system yet to have granted it earlier (see
// game_design.txt), so switching to a weapon is this project's stand-in
// for having it.
func (g *Game) SwitchWeapon(index int) {
	if index < 0 || index >= len(Weapons) {
		return
	}
	g.Player.Weapons[index+2] = true
	if index == g.Weapon.Current || index == g.Weapon.pending {
		return
	}
	g.Weapon.pending = index
	if g.Weapon.phase == weaponReady {
		g.Weapon.phase = weaponLowering
	}
}

// FireWeapon fires the current weapon if it's ready and there's enough
// ammo: plays its sound, starts the fire animation, deducts ammo, and
// launches a visible projectile from the camera along its facing/pitch
// direction (see projectiles.go) — there's no damage dealt yet (no
// monsters exist to damage — see game_design.txt), just the shot itself
// and what it hits.
func (g *Game) FireWeapon() {
	if g.Weapon.phase != weaponReady {
		return
	}
	def := Weapons[g.Weapon.Current]
	if def.AmmoType >= 0 {
		if g.Player.Ammo[def.AmmoType] < def.AmmoCost {
			return
		}
		g.Player.Ammo[def.AmmoType] -= def.AmmoCost
	}
	g.Weapon.phase = weaponFiring
	g.Weapon.fireTime = weaponFireDuration
	g.playSound(def.FireSound)
	g.spawnProjectile()
}

// updateWeapon advances the raise/lower/fire animation by dt seconds and
// keeps Player.CurrentAmmo (what the status bar's big ammo number shows)
// pointed at whichever ammo type the current weapon actually uses.
func (g *Game) updateWeapon(dt float64) {
	w := &g.Weapon
	switch w.phase {
	case weaponLowering:
		w.Offset += weaponRaiseSpeed * dt
		if w.Offset >= 1 {
			w.Offset = 1
			if w.pending >= 0 {
				w.Current, w.pending = w.pending, -1
			}
			w.phase = weaponRaising
		}
	case weaponRaising:
		w.Offset -= weaponRaiseSpeed * dt
		if w.Offset <= 0 {
			w.Offset = 0
			w.phase = weaponReady
		}
	case weaponFiring:
		w.fireTime -= dt
		if w.fireTime <= 0 {
			w.phase = weaponReady
		}
	case weaponReady:
		if w.pending >= 0 {
			w.phase = weaponLowering
		}
	}
	g.Player.CurrentAmmo = Weapons[w.Current].AmmoType
}
