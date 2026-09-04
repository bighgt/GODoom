package engine

import "testing"

func TestDoomedNumToType(t *testing.T) {
	cases := map[int]mobjType{
		3004: MT_POSSESSED,
		9:    MT_SHOTGUY,
		3001: MT_TROOP,
		3002: MT_SERGEANT,
		3005: MT_HEAD,
		3003: MT_BRUISER,
		2035: MT_BARREL,
		5:    MT_MISC4, // blue keycard
		2001: MT_SHOTGUN,
	}
	for dn, want := range cases {
		got, ok := doomedNumToType[dn]
		if !ok || got != want {
			t.Errorf("doomedNumToType[%d] = %v,%v want %v", dn, got, ok, want)
		}
	}
	if _, ok := doomedNumToType[99999]; ok {
		t.Error("unknown doomednum resolved to a type")
	}
}

func TestSetMobjStateAdvancesOnCountdown(t *testing.T) {
	g := &Game{}
	mo := &Mobj{}
	if !g.setMobjState(mo, S_BAR1) {
		t.Fatal("setMobjState(S_BAR1) returned false")
	}
	if mo.State != S_BAR1 || mo.Tics != 6 || mo.Sprite != SPR_BAR1 {
		t.Fatalf("after spawn: state=%d tics=%d sprite=%d", mo.State, mo.Tics, mo.Sprite)
	}
	g.mobjs = []*Mobj{mo}
	for i := 0; i < 6; i++ {
		g.runTic()
	}
	if mo.State != S_BAR2 {
		t.Errorf("after 6 tics barrel state = %d, want S_BAR2 (%d)", mo.State, S_BAR2)
	}
}

func TestSetMobjStateNullRemoves(t *testing.T) {
	g := &Game{}
	mo := &Mobj{}
	if g.setMobjState(mo, S_NULL) {
		t.Error("setMobjState(S_NULL) returned true")
	}
	if !mo.removed {
		t.Error("S_NULL did not mark the mobj removed")
	}
}

func TestFrozenFrameNeverAdvances(t *testing.T) {
	g := &Game{}
	mo := &Mobj{}
	g.setMobjState(mo, S_CLIP) // ammo clip: Tics -1, static
	g.mobjs = []*Mobj{mo}
	for i := 0; i < 100; i++ {
		g.runTic()
	}
	if mo.State != S_CLIP {
		t.Errorf("static pickup advanced to state %d", mo.State)
	}
}
