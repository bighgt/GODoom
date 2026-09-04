package engine

import (
	"testing"

	"twopointfive/bsp"
	"twopointfive/wad"
)

// loadRealLevel builds a Game from an IWAD map with just enough wired up to
// run runTic (no window/renderer needed). Skips if the shareware WAD isn't
// present.
func loadRealLevel(t *testing.T, path, mapName string) *Game {
	t.Helper()
	w, err := wad.Load(path)
	if err != nil {
		t.Skipf("%s not available: %v", path, err)
	}
	lvl, err := w.LoadLevel(mapName)
	if err != nil {
		t.Fatalf("load %s: %v", mapName, err)
	}
	tree, err := bsp.Build(lvl)
	if err != nil {
		t.Fatalf("bsp %s: %v", mapName, err)
	}
	g := &Game{WAD: w, Level: lvl, BSP: tree, Player: DefaultPlayerStats(), Weapon: NewWeaponState()}
	g.blockGrid = buildCollisionGrid(lvl)
	g.spawnSpecials()
	g.spawnMapThings()
	g.playerMobj = &Mobj{
		Type: MT_PLAYER, Info: &mobjInfo[MT_PLAYER], Health: g.Player.Health,
		Flags: mobjInfo[MT_PLAYER].Flags, Radius: mobjInfo[MT_PLAYER].Radius,
		Height: mobjInfo[MT_PLAYER].Height,
	}
	return g
}

// standOn teleports the player onto mo's exact position (and floor) and runs
// one tic, the way checkItemPickups sees it in the real loop.
func (g *Game) standOnAndTic(mo *Mobj) {
	f, _ := g.sectorFloorCeil(mo.X, mo.Y)
	g.Camera.X, g.Camera.Y, g.Camera.Z = mo.X, mo.Y, f+EyeHeight
	g.groundPlayer(f) // establish the vertical state handleMovement would
	g.runTic()
}

func (g *Game) firstOfType(typ mobjType) *Mobj {
	for _, mo := range g.mobjs {
		if mo.Type == typ && !mo.removed {
			return mo
		}
	}
	return nil
}

// Regression guard: collecting the chainsaw (a weapon slot added later) must
// not stop ordinary items — health bonuses especially — from being picked
// up on the same or a following tic.
func TestChainsawPickupDoesNotBlockOtherItems(t *testing.T) {
	g := loadRealLevel(t, "../testdata/DOOM1.WAD", "E1M2") // E1M2 has the chainsaw

	saw := g.firstOfType(MT_MISC26)
	hb := g.firstOfType(MT_MISC2)  // health bonus
	arm := g.firstOfType(MT_MISC3) // armour bonus
	if saw == nil || hb == nil || arm == nil {
		t.Fatalf("missing spawns: saw=%v healthBonus=%v armourBonus=%v", saw != nil, hb != nil, arm != nil)
	}

	g.standOnAndTic(saw)
	if !saw.removed || !g.Player.Weapons[wpChainsaw] {
		t.Fatalf("chainsaw not collected (removed=%v owned=%v)", saw.removed, g.Player.Weapons[wpChainsaw])
	}

	h0 := g.Player.Health
	g.standOnAndTic(hb)
	if !hb.removed || g.Player.Health != h0+1 {
		t.Errorf("health bonus after chainsaw: removed=%v health %d->%d (want +1)", hb.removed, h0, g.Player.Health)
	}

	a0 := g.Player.Armor
	g.standOnAndTic(arm)
	if !arm.removed || g.Player.Armor != a0+1 {
		t.Errorf("armour bonus after chainsaw: removed=%v armour %d->%d (want +1)", arm.removed, a0, g.Player.Armor)
	}
}

// Every collectable thing the shareware IWAD places should still be
// collectable when the player stands on it (with room in the relevant
// counter — a stimpack at full health legitimately stays put).
func TestAllSharewareItemsCollectable(t *testing.T) {
	for _, mp := range []string{"E1M1", "E1M2", "E1M3", "E1M4", "E1M5", "E1M6", "E1M7", "E1M8", "E1M9"} {
		g := loadRealLevel(t, "../testdata/DOOM1.WAD", mp)
		seen := map[mobjType]bool{}
		for _, mo := range append([]*Mobj(nil), g.mobjs...) {
			if mo.Flags&MF_SPECIAL == 0 || seen[mo.Type] || mo.removed {
				continue
			}
			seen[mo.Type] = true
			// Reset the counters this item feeds so "no room" can't mask a
			// real "won't collect" bug.
			g.Player.Health, g.Player.Armor, g.Player.ArmorType = 1, 0, 0
			g.Player.Ammo = [4]int{}
			g.Player.Backpack = false
			g.Player.Keys = [6]bool{}
			g.Player.Powers = [numPowers]int{}
			g.playerMobj.Health = 1
			g.standOnAndTic(mo)
			if !mo.removed {
				t.Errorf("%s: %s (type %d) not collected while standing on it", mp, mo.Info.Name, mo.Type)
			}
		}
	}
}
