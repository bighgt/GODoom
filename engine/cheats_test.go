package engine

import (
	"testing"

	"twopointfive/wad"
)

// fire pushes a code through the matcher the way pollCheats would, without
// needing a window.
func fire(g *Game, code string) {
	g.cheatBuf += code
	if len(g.cheatBuf) > cheatBufMax {
		g.cheatBuf = g.cheatBuf[len(g.cheatBuf)-cheatBufMax:]
	}
	g.matchCheats()
}

func newCheatGame() *Game {
	return &Game{Player: DefaultPlayerStats(), Weapon: NewWeaponState()}
}

func TestCheatIDDQD(t *testing.T) {
	g := newCheatGame()
	g.Player.Health = 20

	fire(g, "iddqd")
	if !g.godMode {
		t.Fatal("iddqd did not enable god mode")
	}
	if g.Player.Health != 100 {
		t.Fatalf("iddqd health = %d, want 100", g.Player.Health)
	}
	if g.cheatBuf != "" {
		t.Fatalf("buffer not cleared after match: %q", g.cheatBuf)
	}

	fire(g, "iddqd")
	if g.godMode {
		t.Fatal("second iddqd did not toggle god mode off")
	}
}

func TestCheatGodModeBlocksDamage(t *testing.T) {
	g := newCheatGame()
	g.damageScale = 100
	g.playerMobj = &Mobj{Type: MT_PLAYER, Flags: MF_SHOOTABLE, Health: 100}

	fire(g, "iddqd")
	g.pDamageMobj(g.playerMobj, nil, nil, 50)
	if g.Player.Health != 100 {
		t.Fatalf("god mode let %d damage through (health %d)", 50, g.Player.Health)
	}

	// A crush is a direct path, not through pDamageMobj — still blocked.
	g.crushPlayer()
	if g.playerDead {
		t.Fatal("god mode did not stop a crush")
	}
}

func TestCheatIDKFAvsIDFA(t *testing.T) {
	g := newCheatGame()
	fire(g, "idkfa")
	for i := 0; i < numWeapons; i++ {
		if !g.Player.Weapons[i] {
			t.Fatalf("idkfa missing weapon %d", i)
		}
	}
	for i := range g.Player.Ammo {
		if g.Player.Ammo[i] != g.Player.MaxAmmo[i] {
			t.Fatalf("idkfa ammo[%d] = %d, want %d", i, g.Player.Ammo[i], g.Player.MaxAmmo[i])
		}
	}
	for i := range g.Player.Keys {
		if !g.Player.Keys[i] {
			t.Fatalf("idkfa missing key %d", i)
		}
	}
	if g.Player.Armor != 200 || g.Player.ArmorType != 2 {
		t.Fatalf("idkfa armor = %d/%d, want 200/2", g.Player.Armor, g.Player.ArmorType)
	}

	g2 := newCheatGame()
	fire(g2, "idfa")
	if !g2.Player.Weapons[wpBFG] || g2.Player.Armor != 200 {
		t.Fatal("idfa did not give weapons + armor")
	}
	for i := range g2.Player.Keys {
		if g2.Player.Keys[i] {
			t.Fatalf("idfa wrongly gave key %d", i)
		}
	}
}

func TestCheatNoclipToggle(t *testing.T) {
	g := newCheatGame()
	fire(g, "idclip")
	if !g.noclip {
		t.Fatal("idclip did not enable noclip")
	}
	fire(g, "idspispopd")
	if g.noclip {
		t.Fatal("idspispopd did not toggle noclip back off")
	}
}

func TestCheatBehold(t *testing.T) {
	g := newCheatGame()
	g.playerMobj = &Mobj{Type: MT_PLAYER}

	fire(g, "idbeholdv")
	if g.Player.Powers[pwInvulnerability] != invulnTics {
		t.Fatalf("idbeholdv power = %d, want %d", g.Player.Powers[pwInvulnerability], invulnTics)
	}
	fire(g, "idbeholds")
	if g.Player.Powers[pwStrength] != 1 || g.Player.Health != 100 {
		t.Fatalf("idbeholds -> strength %d health %d", g.Player.Powers[pwStrength], g.Player.Health)
	}
	fire(g, "idbeholdi")
	if g.playerMobj.Flags&MF_SHADOW == 0 {
		t.Fatal("idbeholdi did not set the fuzz flag")
	}

	// Bare idbehold prints help and must NOT clear the buffer (so the
	// follow-up letter can still complete idbehold<x>).
	g.cheatBuf = ""
	fire(g, "idbehold")
	if g.cheatBuf == "" {
		t.Fatal("bare idbehold cleared the buffer")
	}
	fire(g, "r")
	if g.Player.Powers[pwIronFeet] != ironTics {
		t.Fatal("idbehold then 'r' did not grant the radiation suit")
	}
}

func TestCheatChoppers(t *testing.T) {
	g := newCheatGame()
	if g.Player.Weapons[wpChainsaw] {
		t.Fatal("precondition: chainsaw already owned")
	}
	fire(g, "idchoppers")
	if !g.Player.Weapons[wpChainsaw] {
		t.Fatal("idchoppers did not grant the chainsaw")
	}
}

func TestCheatSuffixMatchAndDigits(t *testing.T) {
	// A code fires as a suffix regardless of leading junk.
	g := newCheatGame()
	fire(g, "wasdwasdiddqd")
	if !g.godMode {
		t.Fatal("iddqd did not fire as a buffer suffix")
	}

	if got := digitsAfter("xxidclev07", "idclev"); got != "07" {
		t.Fatalf("digitsAfter = %q, want 07", got)
	}
	if got := digitsAfter("idclev7", "idclev"); got != "" {
		t.Fatalf("digitsAfter with one digit = %q, want empty", got)
	}
	if got := digitsAfter("idmus23", "idclev"); got != "" {
		t.Fatalf("digitsAfter wrong prefix = %q, want empty", got)
	}
}

func TestMapFromCode(t *testing.T) {
	doom1 := &Game{Level: &wad.Level{Name: "E1M1"}}
	if got := doom1.mapFromCode("25"); got != "E2M5" {
		t.Fatalf("doom1 mapFromCode(25) = %q, want E2M5", got)
	}
	doom2 := &Game{Level: &wad.Level{Name: "MAP01"}}
	if got := doom2.mapFromCode("07"); got != "MAP07" {
		t.Fatalf("doom2 mapFromCode(07) = %q, want MAP07", got)
	}
}

func TestCheatWarpQueuesExistingMap(t *testing.T) {
	g := loadRealLevel(t, "../testdata/DOOM1.WAD", "E1M1")
	fire(g, "idclev13")
	if g.pendingWarp != "E1M3" {
		t.Fatalf("idclev13 pendingWarp = %q, want E1M3", g.pendingWarp)
	}

	g.pendingWarp = ""
	fire(g, "idclev99") // E9M9 — not in the shareware WAD
	if g.pendingWarp != "" {
		t.Fatalf("idclev to a missing map queued %q", g.pendingWarp)
	}
}

func TestCheatMusicHook(t *testing.T) {
	g := &Game{Level: &wad.Level{Name: "MAP01"}}
	var got string
	g.OnMusicChange = func(m string) { got = m }
	fire(g, "idmus07")
	if got != "MAP07" {
		t.Fatalf("idmus07 -> OnMusicChange(%q), want MAP07", got)
	}
}
