package engine

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"twopointfive/bsp"
	"twopointfive/config"
	"twopointfive/raster"
	"twopointfive/wad"
)

func TestMouseLookInvertY(t *testing.T) {
	const sens = 0.0022
	// Mouse pushed forward (mdy < 0): default looks up (pitch increases).
	_, up := mouseLook(0, 0, 0, -100, sens, false)
	if up <= 0 {
		t.Errorf("non-inverted: mouse forward gave pitch %.4f, expected > 0 (look up)", up)
	}
	// Inverted: same motion looks down.
	_, down := mouseLook(0, 0, 0, -100, sens, true)
	if down >= 0 {
		t.Errorf("inverted: mouse forward gave pitch %.4f, expected < 0 (look down)", down)
	}
	if math.Abs(up+down) > 1e-9 {
		t.Errorf("invert should flip the sign, not the magnitude: up=%.5f down=%.5f", up, down)
	}

	// Yaw is unaffected by invertY, and sensitivity scales it.
	yawA, _ := mouseLook(0, 0, 50, 0, sens, false)
	yawB, _ := mouseLook(0, 0, 50, 0, sens, true)
	if yawA != yawB || yawA != -50*sens {
		t.Errorf("yaw: A=%.5f B=%.5f want %.5f", yawA, yawB, -50*sens)
	}

	// Pitch stays clamped to the shear limit.
	_, p := mouseLook(0, 0, 0, -100000, sens, false)
	if p != raster.MaxPitch {
		t.Errorf("pitch not clamped: %.4f, want %.4f", p, raster.MaxPitch)
	}
}

// TestApplyMouseLookRespectsGameFlag exercises the whole
// Game.InvertMouseY -> Camera.Pitch path (not just the pure mouseLook
// helper), so a wiring regression between the field and the camera is
// caught.
func TestApplyMouseLookRespectsGameFlag(t *testing.T) {
	mk := func(invert bool) *Game {
		return &Game{MouseSensitivity: 0.0022, InvertMouseY: invert}
	}

	g := mk(false)
	g.applyMouseLook(0, -100) // mouse pushed forward
	if g.Camera.Pitch <= 0 {
		t.Errorf("invertMouseY=false: forward mouse -> pitch %.4f, want > 0 (look up)", g.Camera.Pitch)
	}

	g = mk(true)
	g.applyMouseLook(0, -100)
	if g.Camera.Pitch >= 0 {
		t.Errorf("invertMouseY=true: forward mouse -> pitch %.4f, want < 0 (look down)", g.Camera.Pitch)
	}
}

// TestNewGamePropagatesConfig makes sure every config.Config knob NewGame
// is responsible for actually lands on the Game (a plain assignment, but
// exactly the sort of thing that silently rots).
func TestNewGamePropagatesConfig(t *testing.T) {
	const path = "../testdata/DOOM1.WAD"
	w, err := wad.Load(path)
	if err != nil {
		t.Skipf("%s not available: %v", path, err)
	}
	lvl, err := w.LoadLevel("E1M1")
	if err != nil {
		t.Fatalf("LoadLevel: %v", err)
	}
	tree, err := bsp.Build(lvl)
	if err != nil {
		t.Fatalf("bsp.Build: %v", err)
	}

	cfg := config.Config{
		InvertMouseY: true, MouseSensitivity: 0.004, TurnSpeed: 4.0,
		AlwaysRun: true, Skill: 1, SwitchWeaponOnPickup: false,
		ShowDebugInfo: false, HUDStyle: config.HUDStyleNone, WeaponVolume: 0.3, WorldVolume: 0.2, MaxFPS: 90,
	}
	g := NewGame(nil, nil, nil, w, lvl, tree, raster.Camera{}, cfg)

	switch {
	case g.InvertMouseY != true:
		t.Error("InvertMouseY not propagated")
	case g.MouseSensitivity != 0.004:
		t.Errorf("MouseSensitivity = %v, want 0.004", g.MouseSensitivity)
	case g.TurnSpeed != 4.0:
		t.Errorf("TurnSpeed = %v, want 4.0", g.TurnSpeed)
	case g.AlwaysRun != true:
		t.Error("AlwaysRun not propagated")
	case g.Skill != 1:
		t.Errorf("Skill = %d, want 1", g.Skill)
	case g.SwitchWeaponOnPickup != false:
		t.Error("SwitchWeaponOnPickup not propagated")
	case g.ShowHUD != false:
		t.Error("ShowDebugInfo -> ShowHUD not propagated")
	case g.hudStyle != hudNone:
		t.Error("HUDStyle -> hudStyle not propagated")
	case g.weaponVolume != 0.3:
		t.Errorf("weaponVolume = %v, want 0.3", g.weaponVolume)
	case g.worldVolume != 0.2:
		t.Errorf("worldVolume = %v, want 0.2", g.worldVolume)
	case g.MaxFPS != 90:
		t.Errorf("MaxFPS = %d, want 90", g.MaxFPS)
	}
}

// TestLoadReadsInvertMouseYFromFile writes a real config.json into a temp
// working directory and confirms Load() reads invertMouseY back (round
// trip through find -> ReadFile -> Unmarshal -> repaired).
func TestLoadReadsInvertMouseYFromFile(t *testing.T) {
	dir := t.TempDir()
	body := []byte(`{"resolution":"720p","fovDegrees":95,"invertMouseY":true,"mouseSensitivity":0.005}`)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	c := config.Load()
	if !c.InvertMouseY {
		t.Errorf("Load() did not pick up invertMouseY:true from %s/config.json (got %+v)", dir, c)
	}
	if c.MouseSensitivity != 0.005 || c.FOVDegrees != 95 {
		t.Errorf("Load() lost other keys: %+v", c)
	}
}

func TestSkillThingBit(t *testing.T) {
	cases := []struct {
		skill int
		want  int
	}{
		{1, mtfEasy}, {2, mtfEasy},
		{3, mtfNormal},
		{4, mtfHard}, {5, mtfHard},
		{0, mtfNormal}, {99, mtfNormal}, // unset / out of range -> Hurt Me Plenty
	}
	for _, c := range cases {
		if got := skillThingBit(c.skill); got != c.want {
			t.Errorf("skillThingBit(%d) = %d, want %d", c.skill, got, c.want)
		}
	}
}
