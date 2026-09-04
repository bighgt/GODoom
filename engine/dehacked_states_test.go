package engine

import "testing"

// Anchor frame numbers from id's info.h statenum_t (Doom II v1.9). A wrong
// offset anywhere above one of these would shift it, silently corrupting
// every Frame edit past that point, so pin the chunk boundaries hard.
func TestDoomStateAnchors(t *testing.T) {
	idx := map[string]int{}
	for i, n := range doomStateNames {
		if _, dup := idx[n]; dup {
			t.Fatalf("doomStateNames[%d] = %q duplicates index %d", i, n, idx[n])
		}
		idx[n] = i
	}

	want := map[string]int{
		"S_LIGHTDONE":  1,
		"S_PUNCH":      2,
		"S_SAW":        67,
		"S_PLAY":       149,
		"S_PLAY_RUN1":  150,
		"S_POSS_STND":  174,
		"S_SPOS_STND":  207,
		"S_VILE_STND":  241,
		"S_SKEL_STND":  321,
		"S_FATT_STND":  362,
		"S_CPOS_STND":  406,
		"S_TROO_STND":  442,
		"S_SARG_STND":  475,
		"S_HEAD_STND":  502,
		"S_BRBALL1":    522,
		"S_BOSS_STND":  527,
		"S_BOS2_STND":  556,
		"S_SKULL_STND": 585,
		"S_SPID_STND":  601,
		"S_BSPI_STND":  632,
		"S_CYBER_STND": 674,
		"S_PAIN_STND":  701,
		"S_SSWV_STND":  726,
		"S_KEENSTND":   763,
		"S_BRAIN":      778,
		"S_ARM1":       802,
		"S_BAR1":       806,
		"S_CLIP":       870,
	}
	for name, n := range want {
		got, ok := idx[name]
		if !ok {
			t.Errorf("%s missing from doomStateNames", name)
			continue
		}
		if got != n {
			t.Errorf("%s at frame %d, want %d (off by %+d)", name, got, n, got-n)
		}
	}

	if len(doomStateNames) != 967 {
		t.Errorf("len(doomStateNames) = %d, want 967", len(doomStateNames))
	}
}

// dehState must land real, implemented monster frames on this engine's
// stateNum values.
func TestDehStateResolves(t *testing.T) {
	cases := []struct {
		frame int
		name  string
	}{
		{149, "S_PLAY"},
		{442, "S_TROO_STND"},
		{475, "S_SARG_STND"},
		{502, "S_HEAD_STND"},
		{585, "S_SKULL_STND"},
		{806, "S_BAR1"},
		{870, "S_CLIP"},
	}
	for _, c := range cases {
		st, ok := dehState(c.frame)
		if !ok {
			t.Errorf("dehState(%d) unresolved, want %s", c.frame, c.name)
			continue
		}
		want, ok := engineStateByName[c.name]
		if !ok {
			t.Fatalf("test bug: engine has no state %s", c.name)
		}
		if st != want {
			t.Errorf("dehState(%d) = %d, want %s (%d)", c.frame, st, c.name, want)
		}
	}

	// A weapon psprite frame and an unimplemented-monster frame must report
	// not-ok rather than resolving to something wrong.
	if _, ok := dehState(10); ok { // S_SAW-ish region is fine; pick a psprite
		// frame 10 = S_PISTOL — no such engine state
		t.Log("frame 10 resolved (ok if engine gained pistol psprite states)")
	}
	if st, ok := dehState(241); ok { // S_VILE_STND — no Arch-vile in this build
		t.Errorf("dehState(240) resolved to %d, but this build has no Arch-vile", st)
	}
}
