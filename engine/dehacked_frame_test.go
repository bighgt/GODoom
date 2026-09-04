package engine

import (
	"reflect"
	"testing"

	"twopointfive/engine/dehacked"
)

// Frame 442 == S_TROO_STND, 444 == S_TROO_RUN1 (both real in this build).
func TestApplyFrameAndCodeptrEdits(t *testing.T) {
	save := map[stateNum]State{}
	for _, s := range []stateNum{S_TROO_STND, S_TROO_RUN1} {
		save[s] = states[s]
	}
	defer func() {
		for s, v := range save {
			states[s] = v
		}
	}()

	patch, errs := dehacked.Parse([]byte(`Frame 442
Sprite number = 39
Sprite subnumber = 32770
Duration = 7
Next frame = 444

[CODEPTR]
Frame 442 = A_Chase
`)) // sprite 39 = SARG; subnumber 32770 = frame 2 + fullbright (0x8000|2)
	if len(errs) != 0 {
		t.Fatalf("parse: %v", errs)
	}
	(&Game{}).applyDehacked(patch)

	got := states[S_TROO_STND]
	if got.Sprite != engineSpriteByName["SARG"] {
		t.Errorf("sprite = %v, want SARG (%v)", got.Sprite, engineSpriteByName["SARG"])
	}
	if got.Frame != 2 || !got.fullbright {
		t.Errorf("frame/fullbright = %d/%v, want 2/true", got.Frame, got.fullbright)
	}
	if got.Tics != 7 {
		t.Errorf("tics = %d, want 7", got.Tics)
	}
	if got.Next != S_TROO_RUN1 {
		t.Errorf("next = %v, want S_TROO_RUN1 (%v)", got.Next, S_TROO_RUN1)
	}
	if reflect.ValueOf(got.Action).Pointer() != reflect.ValueOf(aChase).Pointer() {
		t.Errorf("action not set to aChase")
	}
}

func TestApplyOldStylePointer(t *testing.T) {
	save := states[S_TROO_STND]
	defer func() { states[S_TROO_STND] = save }()
	states[S_TROO_STND].Action = nil

	// Pointer on Frame 442 (S_TROO_STND) copying Frame 444's (S_TROO_RUN1)
	// action, which is aChase in the base table.
	patch, _ := dehacked.Parse([]byte("Pointer 0 (Frame 442)\nCodep Frame = 444\n"))
	(&Game{}).applyDehacked(patch)

	if reflect.ValueOf(states[S_TROO_STND].Action).Pointer() != reflect.ValueOf(aChase).Pointer() {
		t.Errorf("old-style Pointer did not copy aChase onto S_TROO_STND")
	}
}

func TestApplySpriteRename(t *testing.T) {
	spSAB := spriteNum(engineSpriteByName["SARG"])
	saveName := spriteNames[spSAB]
	defer func() {
		spriteNames[spSAB] = saveName
		engineSpriteByName["SARG"] = spSAB
		delete(engineSpriteByName, "PAIN")
	}()

	// old-style Text swap SARG -> PAIN (both 4 chars).
	patch, _ := dehacked.Parse([]byte("Text 4 4\nSARGPAIN"))
	(&Game{}).applyDehacked(patch)

	if spriteNames[spSAB] != "PAIN" {
		t.Errorf("sprite rename: SARG slot now %q, want PAIN", spriteNames[spSAB])
	}
	if _, ok := engineSpriteByName["SARG"]; ok {
		t.Errorf("stale SARG key still in reverse map")
	}
}
