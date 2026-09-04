package engine

import (
	"testing"

	"twopointfive/raster"
	"twopointfive/render"
)

func TestMuzzleFlashLightOnlyWhileFiring(t *testing.T) {
	g := &Game{Weapon: WeaponState{Current: wpPistol, phase: weaponReady, pending: -1}}
	if _, ok := g.muzzleFlashLight(); ok {
		t.Error("muzzle flash light present with the weapon at rest")
	}
	g.Weapon.phase = weaponFiring
	g.Weapon.fireElapsedTics = 0 // pistol FlashFrames start at tic 0
	l, ok := g.muzzleFlashLight()
	if !ok {
		t.Fatal("no muzzle flash light while firing")
	}
	if l.Radius <= 0 || l.Intensity <= 0 {
		t.Errorf("degenerate flash light: %+v", l)
	}
	if !(l.R >= l.G && l.G >= l.B) {
		t.Errorf("flash light isn't warm-toned: r=%.2f g=%.2f b=%.2f", l.R, l.G, l.B)
	}
}

func rocketDef() *ProjectileDef {
	return &ProjectileDef{ExplodePrefix: "BEXP", ExplodeFrames: []byte{'A', 'B', 'C', 'D', 'E'}, ExplodeTics: 4}
}

func TestProjectileLightColourByType(t *testing.T) {
	rocket := (&Game{}).projectileLight(&Projectile{def: rocketDef()})
	if !(rocket.R > rocket.B) {
		t.Errorf("rocket light should be warm: %+v", rocket)
	}
	plasma := (&Game{}).projectileLight(&Projectile{def: &ProjectileDef{ExplodePrefix: "PLSE"}})
	if !(plasma.B > plasma.R) {
		t.Errorf("plasma light should be cool: %+v", plasma)
	}
	bfg := (&Game{}).projectileLight(&Projectile{def: &ProjectileDef{ExplodePrefix: "BFE1"}})
	if !(bfg.G > bfg.R && bfg.G > bfg.B) {
		t.Errorf("BFG light should be green: %+v", bfg)
	}
}

func TestMissileLightByType(t *testing.T) {
	imp, ok := missileLight(&Mobj{Type: MT_TROOPSHOT, Flags: MF_MISSILE, Height: 8}, 100, 200, 40)
	if !ok || !(imp.R > imp.B) {
		t.Errorf("imp fireball light should be warm: ok=%v %+v", ok, imp)
	}
	caco, _ := missileLight(&Mobj{Type: MT_HEADSHOT, Flags: MF_MISSILE, Height: 8}, 0, 0, 0)
	if !(caco.B > caco.R) {
		t.Errorf("caco fireball light should be cool: %+v", caco)
	}
	baron, _ := missileLight(&Mobj{Type: MT_BRUISERSHOT, Flags: MF_MISSILE, Height: 8}, 0, 0, 0)
	if !(baron.G > baron.R && baron.G > baron.B) {
		t.Errorf("baron fireball light should be green: %+v", baron)
	}
	// Detonating (MF_MISSILE cleared) -> brighter, wider, white-hot.
	fly, _ := missileLight(&Mobj{Type: MT_TROOPSHOT, Flags: MF_MISSILE, Height: 8}, 0, 0, 0)
	boom, _ := missileLight(&Mobj{Type: MT_TROOPSHOT, Flags: 0, Height: 8}, 0, 0, 0)
	if !(boom.Intensity > fly.Intensity && boom.Radius > fly.Radius) {
		t.Errorf("impact flash should exceed the travel glow: fly=%+v boom=%+v", fly, boom)
	}
	// A non-missile type -> nothing.
	if _, ok := missileLight(&Mobj{Type: MT_TROOP, Flags: MF_MISSILE}, 0, 0, 0); ok {
		t.Error("missileLight fired for a non-projectile type")
	}
}

func TestCollectLightsIncludesMonsterFireball(t *testing.T) {
	g := loadRealLevel(t, "../wad/DOOM1.WAD", "E1M1")
	g.spawnMapThings()

	cx, cy := g.Camera.X, g.Camera.Y
	base, _ := g.collectLights()
	baseN := len(base)

	// A live imp fireball right next to the camera.
	mo := g.P_SpawnMobj(cx+40, cy, g.Camera.Z, MT_TROOPSHOT)
	if mo == nil || mo.Flags&MF_MISSILE == 0 {
		t.Fatalf("MT_TROOPSHOT spawn: %+v", mo)
	}

	got, dyn := g.collectLights()
	if len(got) <= baseN {
		t.Fatalf("fireball added no light: %d -> %d", baseN, len(got))
	}
	if dyn < 1 {
		t.Errorf("fireball light not counted as dynamic (dynamicN=%d)", dyn)
	}
	// The nearest light should be the fireball's — warm and close.
	near := got[0]
	if !(near.R > near.B) {
		t.Errorf("nearest light isn't the warm fireball: %+v", near)
	}
}

func TestMonsterFlashLight(t *testing.T) {
	// Hitscan shooter in a full-bright (firing) frame -> a warm flash.
	l, ok := monsterFlashLight(&Mobj{Type: MT_CHAINGUY, Fullbright: true, Height: 56}, 10, 20, 0)
	if !ok || l.Radius <= 0 || !(l.R >= l.G && l.G >= l.B) {
		t.Errorf("chaingunner flash: ok=%v %+v", ok, l)
	}
	// Same monster, not currently firing (not bright) -> no light.
	if _, ok := monsterFlashLight(&Mobj{Type: MT_CHAINGUY, Fullbright: false}, 0, 0, 0); ok {
		t.Error("flash light while the chaingunner isn't firing")
	}
	// A bright frame on a non-hitscan monster (imp, baron) -> no flash light
	// (their attack lighting is the fireball itself).
	if _, ok := monsterFlashLight(&Mobj{Type: MT_TROOP, Fullbright: true}, 0, 0, 0); ok {
		t.Error("flash light for an imp (projectile attacker)")
	}
}

func TestProjectileLightExplosionRampsDown(t *testing.T) {
	flying := (&Game{}).projectileLight(&Projectile{def: rocketDef()})
	early := (&Game{}).projectileLight(&Projectile{def: rocketDef(), exploding: true, explodeElapsed: 0})
	late := (&Game{}).projectileLight(&Projectile{def: rocketDef(), exploding: true, explodeElapsed: 0.5})

	if early.Intensity <= flying.Intensity {
		t.Errorf("explosion should flare brighter than flight: early %.2f vs flying %.2f", early.Intensity, flying.Intensity)
	}
	if late.Intensity >= early.Intensity {
		t.Errorf("explosion light should fade over time: late %.2f vs early %.2f", late.Intensity, early.Intensity)
	}
	if late.Radius <= early.Radius {
		t.Errorf("explosion light should spread as it fades: late r%.0f vs early r%.0f", late.Radius, early.Radius)
	}
}

func TestCollectLightsCapAndOrder(t *testing.T) {
	g := &Game{
		Camera: raster.Camera{X: 0, Y: 0, Z: 0},
		Weapon: WeaponState{Current: wpPistol, phase: weaponReady, pending: -1},
	}
	// More shots than the shader can take, deliberately spawned far-first.
	for i := render.MaxLights + 20; i >= 1; i-- {
		g.projectiles = append(g.projectiles, &Projectile{X: float64(i) * 10, def: rocketDef()})
	}
	got, dynamicN := g.collectLights()
	if len(got) != activeLightBudget {
		t.Fatalf("collectLights returned %d, want the cap %d", len(got), activeLightBudget)
	}
	if dynamicN != shadowCasterCap {
		t.Errorf("dynamicN = %d, want it clamped to shadowCasterCap %d", dynamicN, shadowCasterCap)
	}
	// Nearest-first: each successive light is no closer than the previous.
	for i := 1; i < len(got); i++ {
		if got[i].X < got[i-1].X {
			t.Fatalf("lights not sorted nearest-first at %d: %.0f then %.0f", i, got[i-1].X, got[i].X)
		}
	}
	if got[0].X > 20 {
		t.Errorf("closest kept light is at x=%.0f, expected one of the near shots", got[0].X)
	}
}
