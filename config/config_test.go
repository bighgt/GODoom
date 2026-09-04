package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// DefaultJSON must carry EVERY Config field (that's its whole point — one
// file with all the knobs) and round-trip back to Default(). If someone
// adds a field to Config and forgets it here, this fails.
func TestDefaultJSONIsComplete(t *testing.T) {
	data, err := DefaultJSON()
	if err != nil {
		t.Fatalf("DefaultJSON: %v", err)
	}

	var got map[string]json.RawMessage
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("DefaultJSON is not valid JSON: %v", err)
	}

	ct := reflect.TypeOf(Config{})
	for i := 0; i < ct.NumField(); i++ {
		tag := ct.Field(i).Tag.Get("json")
		name, _, _ := strings.Cut(tag, ",")
		if name == "" || name == "-" {
			t.Fatalf("Config.%s has no usable json tag", ct.Field(i).Name)
		}
		if _, ok := got[name]; !ok {
			t.Errorf("DefaultJSON is missing key %q (Config.%s)", name, ct.Field(i).Name)
		}
	}
	if len(got) != ct.NumField() {
		t.Errorf("DefaultJSON has %d keys, Config has %d fields", len(got), ct.NumField())
	}

	var back Config
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	if back != Default() {
		t.Errorf("DefaultJSON did not round-trip:\n got %+v\nwant %+v", back, Default())
	}
}

func TestFindFrom(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	deep := filepath.Join(bin, "sub", "sub2")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgAtRoot := filepath.Join(root, "config.json")
	if err := os.WriteFile(cfgAtRoot, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Launched from bin\ (or double-clicked, wd unknown): walk up from the
	// exe dir finds the config in the repo root.
	if got, ok := findFrom(bin, bin); !ok || got != cfgAtRoot {
		t.Errorf("findFrom(bin, bin) = %q,%v; want %q,true", got, ok, cfgAtRoot)
	}
	// Even a few levels deep under the exe.
	if got, ok := findFrom("", deep); !ok || got != cfgAtRoot {
		t.Errorf("findFrom('', deep) = %q,%v; want %q,true", got, ok, cfgAtRoot)
	}
	// The working directory still wins when it has one.
	binCfg := filepath.Join(bin, "config.json")
	if err := os.WriteFile(binCfg, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, ok := findFrom(bin, bin); !ok || got != binCfg {
		t.Errorf("findFrom prefers the working directory: got %q", got)
	}
	// Nothing anywhere -> not found.
	if _, ok := findFrom(t.TempDir(), t.TempDir()); ok {
		t.Error("findFrom found a config where there is none")
	}
}

func TestRepairedKeepsValid(t *testing.T) {
	c := Config{Resolution: "1440p", FOVDegrees: 110, VSync: false}.repaired()
	if c.Resolution != "1440p" || c.FOVDegrees != 110 || c.VSync {
		t.Errorf("valid config was altered: %+v", c)
	}
}

func TestLightingModeDefaultAndRepair(t *testing.T) {
	if Default().LightingMode != LightingEnhanced {
		t.Errorf("LightingMode default = %q, want %q", Default().LightingMode, LightingEnhanced)
	}
	// Absent (zero value) -> default.
	if c := (Config{Resolution: "720p", FOVDegrees: 90}).repaired(); c.LightingMode != LightingEnhanced {
		t.Errorf("absent lightingMode -> %q, want %q", c.LightingMode, LightingEnhanced)
	}
	// Valid, case-insensitive; an explicit vanilla survives.
	if c := (Config{Resolution: "720p", FOVDegrees: 90, LightingMode: "VANILLA"}).repaired(); c.LightingMode != LightingVanilla {
		t.Errorf("lightingMode VANILLA -> %q, want %q", c.LightingMode, LightingVanilla)
	}
	// Unknown -> falls back to the default, doesn't fail.
	if c := (Config{Resolution: "720p", FOVDegrees: 90, LightingMode: "raytraced"}).repaired(); c.LightingMode != LightingEnhanced {
		t.Errorf("unknown lightingMode -> %q, want %q", c.LightingMode, LightingEnhanced)
	}
}

func TestRendererDefaultAndRepair(t *testing.T) {
	if Default().Renderer != RendererSoftware {
		t.Errorf("Renderer default = %q, want %q", Default().Renderer, RendererSoftware)
	}
	base := Config{Resolution: "720p", FOVDegrees: 90}
	if c := base.repaired(); c.Renderer != RendererSoftware {
		t.Errorf("absent renderer -> %q, want %q", c.Renderer, RendererSoftware)
	}
	for _, want := range []string{RendererSoftware, RendererHardware} {
		in := base
		in.Renderer = " " + want + " "
		if c := in.repaired(); c.Renderer != want {
			t.Errorf("renderer %q -> %q, want %q", in.Renderer, c.Renderer, want)
		}
	}
	in := base
	in.Renderer = "raytraced"
	if c := in.repaired(); c.Renderer != RendererSoftware {
		t.Errorf("unknown renderer -> %q, want %q", c.Renderer, RendererSoftware)
	}
}

func TestCrosshairDefaultAndRepair(t *testing.T) {
	if Default().Crosshair != CrosshairDot {
		t.Errorf("Crosshair default = %q, want %q", Default().Crosshair, CrosshairDot)
	}
	base := Config{Resolution: "720p", FOVDegrees: 90}
	// Absent (zero value) -> default.
	if c := base.repaired(); c.Crosshair != CrosshairDot {
		t.Errorf("absent crosshair -> %q, want %q", c.Crosshair, CrosshairDot)
	}
	// Valid, case-insensitive; an explicit choice survives.
	for _, want := range []string{CrosshairOff, CrosshairDot, CrosshairCross} {
		in := base
		in.Crosshair = " " + want + " "
		if c := in.repaired(); c.Crosshair != want {
			t.Errorf("crosshair %q -> %q, want %q", in.Crosshair, c.Crosshair, want)
		}
	}
	// Unknown -> falls back to the default, doesn't fail.
	in := base
	in.Crosshair = "circle"
	if c := in.repaired(); c.Crosshair != CrosshairDot {
		t.Errorf("unknown crosshair -> %q, want %q", c.Crosshair, CrosshairDot)
	}
}

func TestCrosshairSizeDefaultAndRepair(t *testing.T) {
	if Default().CrosshairSize != CrosshairMedium {
		t.Errorf("CrosshairSize default = %q, want %q", Default().CrosshairSize, CrosshairMedium)
	}
	base := Config{Resolution: "720p", FOVDegrees: 90}
	if c := base.repaired(); c.CrosshairSize != CrosshairMedium {
		t.Errorf("absent crosshairSize -> %q, want %q", c.CrosshairSize, CrosshairMedium)
	}
	for _, want := range []string{CrosshairTiny, CrosshairSmall, CrosshairMedium, CrosshairLarge} {
		in := base
		in.CrosshairSize = " " + want + " "
		if c := in.repaired(); c.CrosshairSize != want {
			t.Errorf("crosshairSize %q -> %q, want %q", in.CrosshairSize, c.CrosshairSize, want)
		}
	}
	in := base
	in.CrosshairSize = "enormous"
	if c := in.repaired(); c.CrosshairSize != CrosshairMedium {
		t.Errorf("unknown crosshairSize -> %q, want %q", c.CrosshairSize, CrosshairMedium)
	}
}

func TestHUDStyleDefaultAndRepair(t *testing.T) {
	if Default().HUDStyle != HUDStyleDoom {
		t.Errorf("HUDStyle default = %q, want %q", Default().HUDStyle, HUDStyleDoom)
	}
	base := Config{Resolution: "720p", FOVDegrees: 90}
	if c := base.repaired(); c.HUDStyle != HUDStyleDoom {
		t.Errorf("absent hudStyle -> %q, want %q", c.HUDStyle, HUDStyleDoom)
	}
	for _, want := range []string{HUDStyleDoom, HUDStyleQuake2, HUDStyleNone} {
		in := base
		in.HUDStyle = " " + want + " "
		if c := in.repaired(); c.HUDStyle != want {
			t.Errorf("hudStyle %q -> %q, want %q", in.HUDStyle, c.HUDStyle, want)
		}
	}
	in := base
	in.HUDStyle = "minimal"
	if c := in.repaired(); c.HUDStyle != HUDStyleDoom {
		t.Errorf("unknown hudStyle -> %q, want %q", c.HUDStyle, HUDStyleDoom)
	}
}

func TestLegacyShowStatusBarFalseHidesHUD(t *testing.T) {
	// An old config with showStatusBar:false and no hudStyle key -> "none".
	dir := t.TempDir()
	p := filepath.Join(dir, fileName)
	if err := os.WriteFile(p, []byte(`{"resolution":"720p","showStatusBar":false}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	if c := Load(); c.HUDStyle != HUDStyleNone {
		t.Errorf("legacy showStatusBar:false -> hudStyle %q, want %q", c.HUDStyle, HUDStyleNone)
	}

	// An explicit hudStyle wins over the legacy bool.
	if err := os.WriteFile(p, []byte(`{"resolution":"720p","showStatusBar":false,"hudStyle":"quake2"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if c := Load(); c.HUDStyle != HUDStyleQuake2 {
		t.Errorf("explicit hudStyle -> %q, want %q", c.HUDStyle, HUDStyleQuake2)
	}
}

func TestLightScaleAndExposureDefaultAndRepair(t *testing.T) {
	d := Default()
	if d.LightScale != 0.75 || d.Exposure != 1.0 {
		t.Errorf("defaults: lightScale=%.2f exposure=%.2f, want 0.75 / 1.00", d.LightScale, d.Exposure)
	}
	// Absent (zero) -> default.
	if c := (Config{Resolution: "720p", FOVDegrees: 90}).repaired(); c.LightScale != 0.75 || c.Exposure != 1.0 {
		t.Errorf("absent -> lightScale=%.2f exposure=%.2f", c.LightScale, c.Exposure)
	}
	// Out of range -> default.
	if c := (Config{Resolution: "720p", FOVDegrees: 90, LightScale: 9, Exposure: 99}).repaired(); c.LightScale != 0.75 || c.Exposure != 1.0 {
		t.Errorf("out-of-range not clamped: lightScale=%.2f exposure=%.2f", c.LightScale, c.Exposure)
	}
	// In range -> kept.
	if c := (Config{Resolution: "720p", FOVDegrees: 90, LightScale: 0.4, Exposure: 1.6}).repaired(); c.LightScale != 0.4 || c.Exposure != 1.6 {
		t.Errorf("valid altered: lightScale=%.2f exposure=%.2f", c.LightScale, c.Exposure)
	}
}

func TestWeaponVolumeDefaultAndRepair(t *testing.T) {
	if d := Default(); d.WeaponVolume != 1.0 {
		t.Errorf("default weaponVolume = %.2f, want 1.0", d.WeaponVolume)
	}
	base := Config{Resolution: "720p", FOVDegrees: 90}
	// Absent (zero) -> default (0 is "unset", not "mute", like the other scalars).
	if c := base.repaired(); c.WeaponVolume != 1.0 {
		t.Errorf("absent -> weaponVolume=%.2f, want 1.0", c.WeaponVolume)
	}
	// Above range -> default.
	c2 := base
	c2.WeaponVolume = 5
	if c := c2.repaired(); c.WeaponVolume != 1.0 {
		t.Errorf("out-of-range -> weaponVolume=%.2f, want 1.0", c.WeaponVolume)
	}
	// In range -> kept.
	c3 := base
	c3.WeaponVolume = 0.4
	if c := c3.repaired(); c.WeaponVolume != 0.4 {
		t.Errorf("valid altered: weaponVolume=%.2f, want 0.4", c.WeaponVolume)
	}
}

func TestWorldVolumeDefaultAndRepair(t *testing.T) {
	if d := Default(); d.WorldVolume != 0.5 {
		t.Errorf("default worldVolume = %.2f, want 0.5", d.WorldVolume)
	}
	base := Config{Resolution: "720p", FOVDegrees: 90}
	// Absent (zero) -> default (0 is "unset", not "mute", like the other scalars).
	if c := base.repaired(); c.WorldVolume != 0.5 {
		t.Errorf("absent -> worldVolume=%.2f, want 0.5", c.WorldVolume)
	}
	// Above range -> default.
	c2 := base
	c2.WorldVolume = 5
	if c := c2.repaired(); c.WorldVolume != 0.5 {
		t.Errorf("out-of-range -> worldVolume=%.2f, want 0.5", c.WorldVolume)
	}
	// In range -> kept.
	c3 := base
	c3.WorldVolume = 0.3
	if c := c3.repaired(); c.WorldVolume != 0.3 {
		t.Errorf("valid altered: worldVolume=%.2f, want 0.3", c.WorldVolume)
	}
}

func TestTextureQualityDefaultAndRepair(t *testing.T) {
	if Default().TextureQuality != TextureNearest {
		t.Errorf("TextureQuality default = %q, want %q", Default().TextureQuality, TextureNearest)
	}
	base := Config{Resolution: "720p", FOVDegrees: 90}
	if c := base.repaired(); c.TextureQuality != TextureNearest {
		t.Errorf("absent textureQuality -> %q, want %q", c.TextureQuality, TextureNearest)
	}
	for _, want := range []string{
		TextureNearest, TextureBilinear, TextureTrilinear,
		TextureAniso2x, TextureAniso4x, TextureAniso8x, TextureAniso16x,
	} {
		in := base
		in.TextureQuality = " " + want + " " // case/space-insensitive
		if c := in.repaired(); c.TextureQuality != want {
			t.Errorf("textureQuality %q -> %q, want %q", in.TextureQuality, c.TextureQuality, want)
		}
	}
	in := base
	in.TextureQuality = "aniso32x"
	if c := in.repaired(); c.TextureQuality != TextureNearest {
		t.Errorf("unknown textureQuality -> %q, want %q", c.TextureQuality, TextureNearest)
	}
}

func TestDeprecatedFilterKeysReported(t *testing.T) {
	dep := deprecatedKeys([]byte(`{"spriteFilter":"nearest","voxels":true,"voxelFilter":"smooth"}`))
	if len(dep) != 2 || dep[0] != "spriteFilter" || dep[1] != "voxelFilter" {
		t.Errorf("deprecatedKeys = %v, want [spriteFilter voxelFilter]", dep)
	}
	if d := deprecatedKeys([]byte(`{"textureQuality":"aniso8x"}`)); len(d) != 0 {
		t.Errorf("deprecatedKeys on a clean file = %v, want none", d)
	}
}

func TestDefaultSwitchWeaponOnPickup(t *testing.T) {
	if !Default().SwitchWeaponOnPickup {
		t.Error("SwitchWeaponOnPickup should default to true (id's own behaviour)")
	}
	// An explicit false in the file survives repaired().
	if (Config{Resolution: "720p", FOVDegrees: 90, SwitchWeaponOnPickup: false}).repaired().SwitchWeaponOnPickup {
		t.Error("explicit switchWeaponOnPickup:false was overridden")
	}
}

func TestInputAndGameplayDefaultsAndRepair(t *testing.T) {
	d := Default()
	if d.DamageScale != 100 {
		t.Errorf("default damageScale = %d, want 100", d.DamageScale)
	}
	if r := (Config{Resolution: "720p", FOVDegrees: 90}).repaired(); r.DamageScale != 100 {
		t.Errorf("absent damageScale not defaulted: %d", r.DamageScale)
	}
	if r := (Config{Resolution: "720p", FOVDegrees: 90, DamageScale: -5}).repaired(); r.DamageScale != 100 {
		t.Errorf("out-of-range damageScale not clamped: %d", r.DamageScale)
	}
	if r := (Config{Resolution: "720p", FOVDegrees: 90, DamageScale: 250}).repaired(); r.DamageScale != 250 {
		t.Errorf("valid damageScale altered: %d", r.DamageScale)
	}

	if d.MouseSensitivity != 0.0022 || d.TurnSpeed != 2.5 || d.Skill != 3 || d.HUDScale != 1.0 || !d.Music || !d.ShowDebugInfo || !d.ShowStatusBar {
		t.Errorf("unexpected defaults: %+v", d)
	}
	// showStatusBar defaults on; an explicit false in the file survives.
	if (Config{Resolution: "720p", FOVDegrees: 90, ShowStatusBar: false}).repaired().ShowStatusBar {
		t.Error("explicit showStatusBar:false was overridden")
	}
	if d.InvertMouseY || d.AlwaysRun || d.MaxFPS != 0 || !d.Voxels || d.VoxelScale != 1.0 ||
		d.TextureQuality != TextureNearest || !d.Shadows || d.GroundShadows {
		t.Errorf("unexpected defaults: %+v", d)
	}
	// An explicit shadows:false in the file survives repaired(); groundShadows
	// defaults off and an explicit true survives.
	if (Config{Resolution: "720p", FOVDegrees: 90, Shadows: false}).repaired().Shadows {
		t.Error("explicit shadows:false was overridden")
	}
	if !(Config{Resolution: "720p", FOVDegrees: 90, GroundShadows: true}).repaired().GroundShadows {
		t.Error("explicit groundShadows:true was overridden")
	}

	// voxelScale: absent -> default, out of range -> default, in range kept;
	// an explicit voxels:false survives.
	if r := (Config{Resolution: "720p", FOVDegrees: 90}).repaired(); r.VoxelScale != 1.0 {
		t.Errorf("absent voxelScale not defaulted: %v", r.VoxelScale)
	}
	if r := (Config{Resolution: "720p", FOVDegrees: 90, VoxelScale: 99}).repaired(); r.VoxelScale != 1.0 {
		t.Errorf("out-of-range voxelScale not clamped: %v", r.VoxelScale)
	}
	if r := (Config{Resolution: "720p", FOVDegrees: 90, VoxelScale: 2}).repaired(); r.VoxelScale != 2 {
		t.Errorf("valid voxelScale altered: %v", r.VoxelScale)
	}
	if (Config{Resolution: "720p", FOVDegrees: 90, Voxels: false}).repaired().Voxels {
		t.Error("explicit voxels:false was overridden")
	}

	// iwad / map: empty by default, surrounding whitespace trimmed.
	if d.IWAD != "" || d.Map != "" {
		t.Errorf("default iwad=%q map=%q, want both empty", d.IWAD, d.Map)
	}
	if r := (Config{Resolution: "720p", FOVDegrees: 90, IWAD: "  C:\\wads\\doom2.wad \t"}).repaired(); r.IWAD != "C:\\wads\\doom2.wad" {
		t.Errorf("iwad not trimmed: %q", r.IWAD)
	}
	if r := (Config{Resolution: "720p", FOVDegrees: 90, Map: "  E1M5\t"}).repaired(); r.Map != "E1M5" {
		t.Errorf("map not trimmed: %q", r.Map)
	}

	if d.WeaponScale != 1.0 {
		t.Errorf("default weaponScale = %.2f, want 1.0", d.WeaponScale)
	}

	// Zero (key absent) falls back to the default...
	r := (Config{Resolution: "720p", FOVDegrees: 90}).repaired()
	if r.MouseSensitivity != d.MouseSensitivity || r.TurnSpeed != d.TurnSpeed || r.Skill != d.Skill || r.HUDScale != d.HUDScale || r.WeaponScale != d.WeaponScale {
		t.Errorf("absent input/skill keys not defaulted: %+v", r)
	}

	// ...out-of-range values are clamped to the default...
	r = (Config{Resolution: "720p", FOVDegrees: 90, MouseSensitivity: 5, TurnSpeed: 999, Skill: 9, HUDScale: 9, WeaponScale: 9, MaxFPS: -30}).repaired()
	if r.MouseSensitivity != d.MouseSensitivity || r.TurnSpeed != d.TurnSpeed || r.Skill != d.Skill || r.HUDScale != d.HUDScale || r.WeaponScale != d.WeaponScale || r.MaxFPS != 0 {
		t.Errorf("out-of-range input/skill not clamped: %+v", r)
	}

	// ...and in-range values (incl. an explicit false bool) are kept.
	r = (Config{Resolution: "720p", FOVDegrees: 90, MouseSensitivity: 0.004, TurnSpeed: 4, Skill: 1, HUDScale: 0.6, WeaponScale: 0.8,
		InvertMouseY: true, AlwaysRun: true, Music: false, ShowDebugInfo: false, MaxFPS: 144}).repaired()
	if r.MouseSensitivity != 0.004 || r.TurnSpeed != 4 || r.Skill != 1 || r.HUDScale != 0.6 || r.WeaponScale != 0.8 || !r.InvertMouseY || !r.AlwaysRun ||
		r.Music || r.ShowDebugInfo || r.MaxFPS != 144 {
		t.Errorf("valid input/skill values were altered: %+v", r)
	}
}

func TestRepairedFixesBadValues(t *testing.T) {
	d := Default()
	c := Config{Resolution: "4k", FOVDegrees: 300}.repaired()
	if c.Resolution != d.Resolution {
		t.Errorf("bad resolution -> %q, want default %q", c.Resolution, d.Resolution)
	}
	if c.FOVDegrees != d.FOVDegrees {
		t.Errorf("out-of-range fov -> %v, want default %v", c.FOVDegrees, d.FOVDegrees)
	}
}

func TestRepairedNormalizesCaseAndKeepsNative(t *testing.T) {
	if c := (Config{Resolution: "  NATIVE ", FOVDegrees: 90}).repaired(); c.Resolution != "native" {
		t.Errorf("native not normalized: %q", c.Resolution)
	}
	if c := (Config{Resolution: "1080P", FOVDegrees: 90}).repaired(); c.Resolution != "1080p" {
		t.Errorf("case not normalized: %q", c.Resolution)
	}
}

func TestRenderSize(t *testing.T) {
	const full, win = false, true

	// Full screen: the Resolution preset drives the render target.
	if w, h := (Config{Resolution: "720p"}).RenderSize(full, 3000, 2000); w != 1280 || h != 720 {
		t.Errorf("fullscreen 720p -> %dx%d, want 1280x720", w, h)
	}
	if w, h := (Config{Resolution: "native"}).RenderSize(full, 3440, 1440); w != 3440 || h != 1440 {
		t.Errorf("fullscreen native -> %dx%d, want the monitor size", w, h)
	}
	dw, dh := presets[Default().Resolution][0], presets[Default().Resolution][1]
	if w, h := (Config{Resolution: "native"}).RenderSize(full, 0, 0); w != dw || h != dh {
		t.Errorf("fullscreen native with no framebuffer -> %dx%d, want default %dx%d", w, h, dw, dh)
	}
	if w, h := (Config{Resolution: "1080p", RenderScale: 0.5}).RenderSize(full, 0, 0); w != 960 || h != 540 {
		t.Errorf("fullscreen 1080p x0.5 -> %dx%d, want 960x540", w, h)
	}
	if w, h := (Config{Resolution: "native", RenderScale: 0.5}).RenderSize(full, 2560, 1440); w != 1280 || h != 720 {
		t.Errorf("fullscreen native x0.5 -> %dx%d, want 1280x720", w, h)
	}

	// Full screen: a preset bigger than the monitor is capped to it (no
	// point rendering above the display just to downscale)...
	if w, h := (Config{Resolution: "1440p"}).RenderSize(full, 1920, 1080); w != 1920 || h != 1080 {
		t.Errorf("fullscreen 1440p on a 1080p monitor -> %dx%d, want it capped to 1920x1080", w, h)
	}
	// ...unless RenderScale > 1 explicitly asks to supersample.
	if w, h := (Config{Resolution: "1440p", RenderScale: 2}).RenderSize(full, 1920, 1080); w != 5120 || h != 2880 {
		t.Errorf("fullscreen 1440p x2 -> %dx%d, want 5120x2880 (no cap when supersampling)", w, h)
	}
	// A preset smaller than the monitor is left alone — that's the perf lever.
	if w, h := (Config{Resolution: "720p"}).RenderSize(full, 3840, 2160); w != 1280 || h != 720 {
		t.Errorf("fullscreen 720p on a 4K monitor -> %dx%d, want 1280x720 (upscaled on present)", w, h)
	}

	// Windowed: the framebuffer (= window) size drives it; Resolution is
	// ignored.
	if w, h := (Config{Resolution: "1440p"}).RenderSize(win, 1280, 720); w != 1280 || h != 720 {
		t.Errorf("windowed 1440p preset -> %dx%d, want the 1280x720 window", w, h)
	}
	if w, h := (Config{Resolution: "720p"}).RenderSize(win, 1920, 1080); w != 1920 || h != 1080 {
		t.Errorf("windowed, window bigger than preset -> %dx%d, want 1920x1080", w, h)
	}
	if w, h := (Config{Resolution: "1440p", RenderScale: 0.5}).RenderSize(win, 1280, 720); w != 640 || h != 360 {
		t.Errorf("windowed x0.5 -> %dx%d, want 640x360", w, h)
	}
}

func TestFog(t *testing.T) {
	// Off by default.
	if _, _, _, _, on := Default().Fog(); on {
		t.Error("fog should be off by default")
	}
	// Colour but no density -> off.
	if _, _, _, _, on := (Config{FogColor: "8899aa"}).Fog(); on {
		t.Error("fog with no density should be off")
	}
	// Density but no colour -> off.
	if _, _, _, _, on := (Config{FogDensity: 0.001}).Fog(); on {
		t.Error("fog with no colour should be off")
	}
	// Both set -> on, colour parsed.
	r, g, b, d, on := (Config{FogColor: "#4080c0", FogDensity: 0.001}).Fog()
	if !on || d != 0.001 {
		t.Fatalf("fog not on: on=%v d=%v", on, d)
	}
	if !(r > 0.24 && r < 0.26 && g > 0.49 && g < 0.51 && b > 0.74 && b < 0.76) {
		t.Errorf("fog colour parse: %.3f %.3f %.3f (want ~0.25 0.50 0.75)", r, g, b)
	}
}

func TestRepairedFog(t *testing.T) {
	base := Config{Resolution: "720p", FOVDegrees: 90}

	// Bad hex -> blanked (fog disabled).
	c := base
	c.FogColor, c.FogDensity = "not-hex", 0.001
	if got := c.repaired(); got.FogColor != "" {
		t.Errorf("invalid fogColor kept: %q", got.FogColor)
	}
	// Leading '#' stripped, kept.
	c = base
	c.FogColor = "#aabbcc"
	if got := c.repaired(); got.FogColor != "aabbcc" {
		t.Errorf("fogColor '#' not stripped: %q", got.FogColor)
	}
	// Density clamped.
	c = base
	c.FogColor, c.FogDensity = "aabbcc", 5
	if got := c.repaired(); got.FogDensity != MaxFogDensity {
		t.Errorf("fogDensity not clamped: %v", got.FogDensity)
	}
	c = base
	c.FogDensity = -1
	if got := c.repaired(); got.FogDensity != 0 {
		t.Errorf("negative fogDensity not zeroed: %v", got.FogDensity)
	}
}
