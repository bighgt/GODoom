package engine

import (
	"math"
	"testing"
)

// aimGameAtStart puts the player at the level's player-1 start facing its
// authored angle, with the mobj mirror in sync.
func aimGameAtStart(g *Game) (sx, sy, sang float64) {
	for _, th := range g.Level.Things {
		if th.Type == 1 {
			sx, sy, sang = float64(th.X), float64(th.Y), float64(th.Angle)*math.Pi/180
		}
	}
	f, _ := g.sectorFloorCeil(sx, sy)
	g.Camera.X, g.Camera.Y, g.Camera.Z, g.Camera.Angle = sx, sy, f+EyeHeight, sang
	g.playerMobj.X, g.playerMobj.Y, g.playerMobj.Z = sx, sy, f
	return sx, sy, sang
}

func runProjectiles(g *Game) {
	for i := 0; i < 40 && len(g.projectiles) > 0; i++ {
		g.updateProjectiles(1.0 / 35)
	}
}

// A traveling projectile must lose the target monster health on a direct
// hit — previously advanceProjectile only tested walls/floors, so a rocket
// flew straight through a monster and the plasma bolt did nothing at all.
func TestProjectileDirectHitDamagesMonster(t *testing.T) {
	for _, tc := range []struct {
		name string
		wp   int
	}{
		{"rocket", wpMissile},
		{"plasma", wpPlasma},
		{"bfg", wpBFG},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := loadRealLevel(t, "../testdata/DOOM1.WAD", "E1M1")
			_, _, sang := aimGameAtStart(g)

			// Imp planted 40u dead ahead — the shot spawns 20u out and hits it
			// on the first substep, before it can reach any wall.
			ix := g.Camera.X + math.Cos(sang)*40
			iy := g.Camera.Y + math.Sin(sang)*40
			imp := g.P_SpawnMobj(ix, iy, onFloorZ, MT_TROOP)
			startHP := imp.Health

			g.spawnProjectile(Weapons[tc.wp].Projectile)
			runProjectiles(g)

			if imp.Health >= startHP {
				t.Fatalf("%s direct hit: imp HP %d -> %d (no damage)", tc.name, startHP, imp.Health)
			}
		})
	}
}

// Enough rockets must actually kill a monster.
func TestRocketVolleyKillsMonster(t *testing.T) {
	g := loadRealLevel(t, "../testdata/DOOM1.WAD", "E1M1")
	_, _, sang := aimGameAtStart(g)
	ix := g.Camera.X + math.Cos(sang)*40
	iy := g.Camera.Y + math.Sin(sang)*40
	imp := g.P_SpawnMobj(ix, iy, onFloorZ, MT_TROOP) // 60 HP

	for shot := 0; shot < 8 && imp.Health > 0; shot++ {
		g.spawnProjectile(Weapons[wpMissile].Projectile)
		runProjectiles(g)
	}
	if imp.Health > 0 {
		t.Fatalf("imp survived 8 rockets with %d HP", imp.Health)
	}
}

// The plasma bolt carries no blast radius, so a monster it flies past
// untouched must take no damage (no phantom splash).
func TestPlasmaMissDoesNotDamage(t *testing.T) {
	g := loadRealLevel(t, "../testdata/DOOM1.WAD", "E1M1")
	_, _, sang := aimGameAtStart(g)

	// Imp 64u off to the player's left — well outside the bolt's path.
	perp := sang + math.Pi/2
	ix := g.Camera.X + math.Cos(perp)*64
	iy := g.Camera.Y + math.Sin(perp)*64
	imp := g.P_SpawnMobj(ix, iy, onFloorZ, MT_TROOP)
	startHP := imp.Health

	g.spawnProjectile(Weapons[wpPlasma].Projectile)
	runProjectiles(g)

	if imp.Health != startHP {
		t.Fatalf("plasma bolt that missed still changed imp HP %d -> %d", startHP, imp.Health)
	}
}

// A cosmetic projectile (Damage 0) still detonates but deals no direct hit.
func TestZeroDamageProjectileNoDirectHit(t *testing.T) {
	g := loadRealLevel(t, "../testdata/DOOM1.WAD", "E1M1")
	_, _, sang := aimGameAtStart(g)
	ix := g.Camera.X + math.Cos(sang)*40
	iy := g.Camera.Y + math.Sin(sang)*40
	imp := g.P_SpawnMobj(ix, iy, onFloorZ, MT_TROOP)
	startHP := imp.Health

	def := *Weapons[wpPlasma].Projectile
	def.Damage = 0
	def.ExplodeRadius = 0
	g.spawnProjectile(&def)
	runProjectiles(g)

	if imp.Health != startHP {
		t.Fatalf("zero-damage projectile changed imp HP %d -> %d", startHP, imp.Health)
	}
}
