package mapinfo

import "testing"

func TestParseBasic(t *testing.T) {
	src := []byte(`
// leading comment
MAP MAP01
{
    levelname = "Entryway"
    label     = "MAP01"
    author    = "Sandy Petersen"
    next      = "map02"
    nextsecret = "MAP31"
    skytexture = "RSKY1"
    music     = "D_RUNNIN"
    partime   = 30
    endgame   = false
}

map E1M8 {
    levelname = "Phobos Anomaly"
    endgame = true
    bossaction = BaronOfHell, 23, 666
    bossaction = BaronOfHell, 38, 666
}
`)
	m, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	e := m["MAP01"]
	if e == nil {
		t.Fatal("MAP01 not parsed")
	}
	if e.LevelName != "Entryway" || e.Author != "Sandy Petersen" {
		t.Errorf("MAP01 name/author = %q / %q", e.LevelName, e.Author)
	}
	if e.Next != "MAP02" || e.NextSecret != "MAP31" { // upper-cased
		t.Errorf("MAP01 next/nextsecret = %q / %q", e.Next, e.NextSecret)
	}
	if e.SkyTexture != "RSKY1" || e.Music != "D_RUNNIN" {
		t.Errorf("MAP01 sky/music = %q / %q", e.SkyTexture, e.Music)
	}
	if e.ParTime != 30 {
		t.Errorf("MAP01 partime = %d, want 30", e.ParTime)
	}
	if e.EndGame {
		t.Error("MAP01 endgame should be false")
	}

	b := m["E1M8"]
	if b == nil || !b.EndGame {
		t.Fatalf("E1M8 endgame not set")
	}
	if len(b.BossActions) != 2 || b.BossActions[0].Special != 23 || b.BossActions[1].Special != 38 {
		t.Errorf("E1M8 bossactions = %+v", b.BossActions)
	}
	if b.BossActions[0].Tag != 666 {
		t.Errorf("E1M8 bossaction tag = %d, want 666", b.BossActions[0].Tag)
	}
}

func TestParseClearAndIntertext(t *testing.T) {
	src := []byte(`MAP MAP07 {
        levelname = "Dead Simple"
        intertext = "line one", "line two"
        bossaction = clear
    }
    MAP MAP08 {
        intertext = clear
    }`)
	m, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if it := m["MAP07"].InterText; len(it) != 2 || it[1] != "line two" {
		t.Errorf("MAP07 intertext = %v", it)
	}
	if !m["MAP07"].BossActionsCleared {
		t.Error("MAP07 bossaction=clear not recorded")
	}
	if it := m["MAP08"].InterText; it == nil || len(it) != 0 {
		t.Errorf("MAP08 intertext=clear -> %v (want non-nil empty)", it)
	}
}

func TestParseUnknownKeysKept(t *testing.T) {
	m, err := Parse([]byte(`MAP MAP01 { levelname = "X"  gravity = 400  someflag = true }`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	e := m["MAP01"]
	if e.Raw["gravity"] != "400" || e.Raw["someflag"] != "true" {
		t.Errorf("unknown keys not kept in Raw: %v", e.Raw)
	}
}

func TestParseToleratesGarbage(t *testing.T) {
	// A bad partime is reported but the rest of the block still parses.
	m, err := Parse([]byte(`MAP MAP01 { levelname = "OK"  partime = notanumber  next = "MAP02" }`))
	if err == nil {
		t.Error("expected an error for the bad partime")
	}
	if m["MAP01"] == nil || m["MAP01"].LevelName != "OK" || m["MAP01"].Next != "MAP02" {
		t.Errorf("recovery failed: %+v", m["MAP01"])
	}
}

func TestParseSecondBlockPatches(t *testing.T) {
	m, _ := Parse([]byte(`
        MAP MAP01 { levelname = "First"  music = "D_A" }
        MAP MAP01 { music = "D_B"  partime = 42 }
    `))
	e := m["MAP01"]
	if e.LevelName != "First" || e.Music != "D_B" || e.ParTime != 42 {
		t.Errorf("patch merge = %+v", e)
	}
}
