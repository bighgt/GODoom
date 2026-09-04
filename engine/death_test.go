package engine

import (
	"testing"

	"twopointfive/bsp"
)

func newDamageGame() *Game {
	g := &Game{Player: DefaultPlayerStats(), damageScale: 100}
	g.playerMobj = &Mobj{
		Type: MT_PLAYER, Info: &mobjInfo[MT_PLAYER],
		Flags: MF_SHOOTABLE, Health: 100, Height: 56,
	}
	return g
}

func TestPlayerDeathAtZeroHealth(t *testing.T) {
	g := newDamageGame()
	g.pDamageMobj(g.playerMobj, nil, nil, 100)

	if !g.playerDead {
		t.Fatal("player not marked dead after a lethal hit")
	}
	if g.Player.Health != 0 {
		t.Errorf("dead player health = %d, want 0", g.Player.Health)
	}
	if g.playerMobj.Health != 0 || g.playerMobj.Flags&MF_SHOOTABLE != 0 {
		t.Errorf("player mobj not finalised: health=%d shootable=%v", g.playerMobj.Health, g.playerMobj.Flags&MF_SHOOTABLE != 0)
	}

	// Further damage is a no-op once dead.
	g.Player.Health = 5 // pretend something set it
	g.pDamageMobj(g.playerMobj, nil, nil, 10)
	if g.Player.Health != 5 {
		t.Errorf("damage after death changed health to %d", g.Player.Health)
	}
}

func TestDamageScale(t *testing.T) {
	cases := []struct {
		scale, deal, wantLoss int
	}{
		{100, 40, 40}, // normal
		{50, 40, 20},  // half
		{200, 10, 20}, // double
		{0, 30, 30},   // unset -> normal
		{1, 100, 1},   // near-invincible: 1% of 100
	}
	for _, c := range cases {
		g := newDamageGame()
		g.damageScale = c.scale
		g.Player.Health = 100
		g.playerMobj.Health = 100
		g.pDamageMobj(g.playerMobj, nil, nil, c.deal)
		if got := 100 - g.Player.Health; got != c.wantLoss {
			t.Errorf("scale=%d deal=%d: lost %d health, want %d", c.scale, c.deal, got, c.wantLoss)
		}
	}
}

func TestCrushIsAlwaysLethal(t *testing.T) {
	g := newDamageGame()
	g.damageScale = 1 // would make ordinary damage a rounding error
	g.crushPlayer()
	if !g.playerDead || g.Player.Health != 0 {
		t.Errorf("crush not lethal: dead=%v health=%d", g.playerDead, g.Player.Health)
	}
}

func TestCheckPlayerCrush(t *testing.T) {
	g := loadRealLevel(t, "../testdata/DOOM1.WAD", "E1M1")
	g.damageScale = 100

	var sx, sy float64
	for _, th := range g.Level.Things {
		if th.Type == 1 {
			sx, sy = float64(th.X), float64(th.Y)
		}
	}
	sec := bsp.PointSector(g.BSP, g.Level, float32(sx), float32(sy))
	if sec == nil {
		t.Fatal("player start not in a sector")
	}
	g.Camera.X, g.Camera.Y = sx, sy
	g.playerMobj.X, g.playerMobj.Y = sx, sy

	// Plenty of headroom -> not crushed.
	sec.FloorHeight, sec.CeilingHeight = 0, 200
	g.checkPlayerCrush()
	if g.playerDead {
		t.Fatal("player 'crushed' with 200 units of headroom")
	}

	// Ceiling closed to under player height -> crushed dead.
	sec.CeilingHeight = int16(playerHeight) - 20
	g.checkPlayerCrush()
	if !g.playerDead {
		t.Fatalf("player not crushed with a %d-unit gap (height %d)", sec.CeilingHeight-sec.FloorHeight, int(playerHeight))
	}

	// Idempotent — a second call doesn't re-trigger anything.
	g.playerDead = true
	g.checkPlayerCrush() // must not panic / double-fire
}
