package engine

import (
	"testing"

	"twopointfive/engine/dehacked"
)

func TestDehThingType(t *testing.T) {
	if got, ok := dehThingType(12); !ok || got != MT_TROOP { // Thing 12 = imp
		t.Errorf("Thing 12 -> %v ok=%v, want MT_TROOP", got, ok)
	}
	if got, ok := dehThingType(1); !ok || got != MT_PLAYER {
		t.Errorf("Thing 1 -> %v ok=%v, want MT_PLAYER", got, ok)
	}
	if got, ok := dehThingType(74); !ok || got != MT_CHAINGUN { // Thing 74 = chaingun pickup
		t.Errorf("Thing 74 -> %v ok=%v, want MT_CHAINGUN", got, ok)
	}
	if _, ok := dehThingType(4); ok { // Thing 4 = Arch-vile, not in this build
		t.Error("Thing 4 (Arch-vile) resolved — this build has no vile")
	}
	if _, ok := dehThingType(0); ok {
		t.Error("Thing 0 resolved")
	}
}

func TestApplyThingStatEdits(t *testing.T) {
	// Snapshot and restore — mobjInfo is package state.
	saved := mobjInfo[MT_TROOP]
	defer func() { mobjInfo[MT_TROOP] = saved }()

	g := &Game{}
	patch, errs := dehacked.Parse([]byte(`Thing 12 (Imp)
Hit points = 150
Speed = 14
Missile damage = 5
Pain chance = 100
Bits = SOLID+SHOOTABLE+COUNTKILL
Height = 4194304
`)) // 4194304 = 64<<16
	if len(errs) != 0 {
		t.Fatalf("parse: %v", errs)
	}
	g.applyDehacked(patch)

	mi := mobjInfo[MT_TROOP]
	if mi.SpawnHealth != 150 {
		t.Errorf("imp health = %d, want 150", mi.SpawnHealth)
	}
	if mi.Speed != 14 {
		t.Errorf("imp speed = %v, want 14", mi.Speed)
	}
	if mi.Damage != 5 || mi.PainChance != 100 {
		t.Errorf("imp damage/painchance = %d/%d", mi.Damage, mi.PainChance)
	}
	if mi.Height != 64 {
		t.Errorf("imp height = %v, want 64 (fixed-point 4194304 >> 16)", mi.Height)
	}
	if mi.Flags != MF_SOLID|MF_SHOOTABLE|MF_COUNTKILL {
		t.Errorf("imp flags = %#x, want SOLID|SHOOTABLE|COUNTKILL", mi.Flags)
	}
}

func TestApplyDehLevelNamesAndPars(t *testing.T) {
	g := loadRealLevel(t, "../wad/Doom2.wad", "MAP01")
	patch, _ := dehacked.Parse([]byte(`[STRINGS]
HUSTR_1 = The Front Door
HUSTR_E1M1 = Hangar Redux

[PARS]
par 1 55
par 3 3 120
`))
	g.applyDehacked(patch)

	if g.dehLevelNames["MAP01"] != "The Front Door" {
		t.Errorf("MAP01 deh name = %q", g.dehLevelNames["MAP01"])
	}
	if g.dehLevelNames["E1M1"] != "Hangar Redux" {
		t.Errorf("E1M1 deh name = %q", g.dehLevelNames["E1M1"])
	}
	if g.levelDisplayName() != "The Front Door" { // current level is MAP01
		t.Errorf("levelDisplayName = %q, want the DEH name", g.levelDisplayName())
	}
	if g.dehPars["MAP01"] != 55 || g.dehPars["E3M3"] != 120 {
		t.Errorf("dehPars = %v", g.dehPars)
	}
	g.ApplyMapInfo()
	if g.parTime != 55 {
		t.Errorf("parTime = %d, want 55 (DEH [PARS] MAP01)", g.parTime)
	}
}
