// Package config loads the user-tunable engine settings — display, input,
// and a few gameplay knobs — from an optional config.json next to the
// executable (or in the working directory), with baked-in defaults so the
// game runs fine unconfigured. It's deliberately tiny: a flat struct, a
// preset table, and forgiving parsing (a missing or malformed file, or an
// out-of-range value, falls back to the default rather than failing
// startup).
package config

import (
	"encoding/json"
	"log"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
)

// Config is the on-disk settings model (config.json). Build it with Load;
// the zero value is not meaningful.
type Config struct {
	// Resolution is the FULL-SCREEN render target size: one of "540p",
	// "600p", "720p", "900p", "1080p", "1440p", or "native" (the monitor's
	// own framebuffer), case-insensitive; anything else falls back to the
	// default. The rendered image is letterboxed to the screen, so a lower
	// value is the single biggest performance lever the software renderer
	// has. IGNORED in windowed mode — there the render target is the window
	// itself (WindowWidth x WindowHeight).
	Resolution string `json:"resolution"`
	// RenderScale multiplies the resolved render size on both axes (so 0.5
	// quarters the pixel count, the dominant cost). Clamped to [0.25, 2.0];
	// 1.0 (the default) is a no-op. Applies in both modes — full screen it
	// scales the Resolution preset, windowed it scales the window size.
	RenderScale float64 `json:"renderScale"`
	// Windowed requests a normal decorated, resizable window instead of the
	// default borderless fullscreen (which covers the primary monitor at
	// its current video mode). Left false — fullscreen — unless set.
	Windowed bool `json:"windowed"`
	// WindowWidth/WindowHeight are the window size in windowed mode, and
	// also the render resolution there (Resolution is not consulted).
	// Ignored for fullscreen. Zero falls back to 1280x720.
	WindowWidth  int `json:"windowWidth"`
	WindowHeight int `json:"windowHeight"`
	// FOVDegrees is the horizontal field of view at the engine's 16:10
	// reference aspect (the classic 320x200). Wider display aspects extend
	// the horizontal view further while holding the vertical FOV constant
	// (Hor+ widescreen). Clamped to [MinFOV, MaxFOV].
	FOVDegrees float64 `json:"fovDegrees"`
	// HUDScale sizes the status bar and the debug text overlay. 1.0
	// (default) draws them at the authored 320x200 proportions scaled to
	// fill the screen height (like the original); lower values shrink toward
	// the bottom-centre, higher values enlarge. Clamped to [MinHUDScale,
	// MaxHUDScale]. The weapon viewmodel is NOT affected — it has its own
	// weaponScale.
	HUDScale float64 `json:"hudScale"`
	// WeaponScale sizes the first-person weapon viewmodel, independent of
	// hudScale. 1.0 (default) fills the screen height (the original look);
	// lower values draw a smaller gun. Clamped to [MinWeaponScale,
	// MaxWeaponScale].
	WeaponScale float64 `json:"weaponScale"`
	// VSync requests the tear-free FIFO present mode. When false the
	// backend prefers mailbox (low-latency, uncapped) if the driver offers
	// it.
	VSync bool `json:"vsync"`
	// SwitchWeaponOnPickup: when true (the default), picking up a weapon the
	// player didn't already own switches to it immediately, the way the
	// original game does. Set false to keep your current weapon up and just
	// add the new one to the arsenal.
	SwitchWeaponOnPickup bool `json:"switchWeaponOnPickup"`
	// InvertMouseY flips vertical mouse-look: with it set, pushing the mouse
	// forward looks down (the "flight-stick" convention). Default false.
	InvertMouseY bool `json:"invertMouseY"`
	// MouseSensitivity is radians of view rotation per pixel of raw mouse
	// motion (both axes). Clamped to [MinMouseSensitivity,
	// MaxMouseSensitivity]; default 0.0022.
	MouseSensitivity float64 `json:"mouseSensitivity"`
	// TurnSpeed is the keyboard turn rate (Left/Right arrows) in radians
	// per second. Clamped to [MinTurnSpeed, MaxTurnSpeed]; default 2.5.
	TurnSpeed float64 `json:"turnSpeed"`
	// AlwaysRun inverts the run modifier: move at run speed by default and
	// hold Shift to walk, instead of the reverse. Default false.
	AlwaysRun bool `json:"alwaysRun"`
	// Skill is the difficulty a map's THINGS are filtered by, 1 ("I'm Too
	// Young To Die") to 5 ("Nightmare"). Only monster/item spawn counts are
	// affected — this engine doesn't model ITYTD's half-damage or
	// Nightmare's fast/respawning monsters. Clamped to [1, 5]; default 3.
	Skill int `json:"skill"`
	// DamageScale is a testing knob for how hard damage hits the PLAYER, as
	// a percentage: 100 (the default) is normal, 50 half, 200 double, 1
	// effectively invincible. It scales every ordinary hit (monster shots,
	// nukage, explosions) — but being crushed by a closing door, a crusher
	// ceiling or a rising floor is always instant death regardless. Clamped
	// to [1, 100000].
	DamageScale int `json:"damageScale"`
	// Music enables loading and playing each level's background track.
	// Default true. (Windows-only regardless — see package audio.)
	Music bool `json:"music"`
	// WeaponVolume scales the loudness of weapon sounds — every gun's fire
	// and reload noise and the chainsaw's idle whir — relative to the rest
	// of the sound effects. 1.0 (the default) leaves them at full volume;
	// set e.g. 0.4 to bring a too-loud chainsaw down. Clamped to
	// [MinWeaponVolume, MaxWeaponVolume]; like the other scalar knobs, a
	// literal 0 is treated as "unset" and falls back to the default, so use
	// a small value (0.02) rather than 0 for near-silent.
	WeaponVolume float64 `json:"weaponVolume"`
	// WorldVolume scales the loudness of the level's own machinery — doors,
	// lifts and platforms, moving floors and ceilings/crushers, stairs,
	// switch clicks and teleporters — relative to the rest of the sound
	// effects. Several of these loop while a sector is in motion, so at full
	// volume a busy map drones; the default 0.5 pulls that background down.
	// Clamped to [MinWorldVolume, MaxWorldVolume]; like the other scalar
	// knobs a literal 0 means "unset" and falls back to the default, so use
	// a small value (0.02) rather than 0 for near-silent.
	WorldVolume float64 `json:"worldVolume"`
	// ShowDebugInfo toggles the top-left FPS / position / angle text
	// overlay. Default true.
	ShowDebugInfo bool `json:"showDebugInfo"`
	// ShowStatusBar is the legacy on/off for the bottom status bar,
	// SUPERSEDED by hudStyle below. Kept for back-compat: if hudStyle is
	// unset and this is false, the HUD is hidden (hudStyle "none").
	ShowStatusBar bool `json:"showStatusBar"`
	// HUDStyle selects the in-game HUD:
	//   "doom"    the full bottom status bar — health / armour / ammo /
	//             arms / face / keys, the original graphics and layout (default)
	//   "quake2"  a minimal Quake II-style HUD: big health bottom-left and
	//             ammo bottom-right with pickup icons, armour above health,
	//             keys top-right — no status-bar chrome
	//   "none"    no HUD at all (the weapon viewmodel, crosshair and debug
	//             overlay are unaffected)
	// F1 cycles doom -> quake2 -> none -> doom at runtime. Case-insensitive.
	HUDStyle string `json:"hudStyle"`
	// Voxels replaces map-object sprites (monsters, items, ammo, keys, ...)
	// with voxel models from assets/voxel/Doom{1,2}_Voxel.pk3 when a model
	// exists for the current frame. Default true; harmless if the pack is
	// absent (falls back to sprites). A distant thing still draws as a
	// sprite regardless (see raster.DrawVoxel's size cutoff).
	Voxels bool `json:"voxels"`
	// Flashlight enables the F-key head-mounted flashlight (a forward beam
	// in the enhanced/hardware render tiers, a flat brightness bump in
	// vanilla). Default true; false disables the toggle entirely.
	Flashlight bool `json:"flashlight"`
	// VoxelScale is map units per voxel for the models above. Cheello's
	// Voxel Doom is authored at 1.0 (the default); raise/lower to fit a pack
	// whose proportions differ. Clamped to [0.25, 4.0].
	VoxelScale float64 `json:"voxelScale"`
	// Quake1Textures, when true, layers a Quake 1 texture pack over the
	// hi-res override set: for every Doom wall/flat name the curated table in
	// tools/importquake covers, the Quake texture in assets/textures/quake1/
	// wins; any other name still falls through to the assets/textures/walls|
	// flats PNG override and then the WAD's own texture. Default false;
	// harmless if assets/textures/quake1/ is absent. Populate that directory
	// with `go run ./tools/importquake` (reads assets/import/
	// Quake1_Textures.pk3) — it never touches the existing hi-res PNGs.
	Quake1Textures bool `json:"quake1Textures"`
	// TextureQuality is the single global texture-filtering setting for every
	// world surface — walls, flats, sprites and voxels:
	//   "nearest"    classic hard pixels, no mips (the default; fastest; the
	//                vanilla look, and byte-for-byte the pre-existing wall/flat
	//                output)
	//   "bilinear"   smooth within a texture
	//   "trilinear"  bilinear + a box-filtered mip chain by on-screen scale
	//                (kills distance shimmer)
	//   "aniso2x" / "aniso4x" / "aniso8x" / "aniso16x"
	//                trilinear plus up to N anisotropic taps, so a surface seen
	//                at a grazing angle — a long wall, a floor stretching to
	//                the horizon — stays sharp instead of blurring. N is the
	//                max sample count.
	// The filtered modes are pure CPU work in the software rasterizer's
	// innermost loops: at 1080p "trilinear" is ~5x the per-frame cost of
	// "nearest" and "aniso8x"/"aniso16x" ~8x, so it is opt-in. Case-
	// insensitive; anything unrecognised -> the "nearest" default. Voxels
	// have no texture, so for them anything but "nearest" just means
	// "antialias the splats". Replaces the old per-surface spriteFilter /
	// voxelFilter knobs.
	TextureQuality string `json:"textureQuality"`
	// MaxFPS caps the frame rate when VSync is off (a plain sleep at the end
	// of each frame). 0 means uncapped. Ignored when VSync is on. Default 0.
	MaxFPS int `json:"maxFPS"`
	// LightingMode selects the lighting pipeline: "enhanced" (the default)
	// has the CPU emit a G-buffer (albedo + normal + sector light + depth)
	// and a multi-pass Vulkan pipeline do fully hardware dynamic lighting —
	// map light emitters (animated-light sectors, torches, lamps), the
	// muzzle flash and in-flight rockets/plasma/BFG, ground shadows under
	// every thing, screen-space shadows for the nearest lights, HDR, bloom
	// and a highlight soft-knee. "vanilla" shades every wall/flat on the CPU
	// through the exact PrBoom scalelight/zlight port (raster/light.go) and
	// the GPU only blits the finished frame — the fallback for a GPU that
	// can't stand up the enhanced pipeline. Anything else -> "enhanced".
	LightingMode string `json:"lightingMode"`
	// Renderer selects the rasterization path: "software" (the default) runs
	// the CPU rasterizer in raster/ and lightingMode picks vanilla vs
	// enhanced from there; "hardware" submits GPU geometry built from the BSP
	// walk (render/worldgeo) and the GPU does texturing / depth / the vanilla
	// scalelight fade + per-surface dynamic point lights. Plain Vulkan 1.0
	// (one draw call per texture, no bindless); falls back to "software" if
	// that path can't start. World sprites are billboards; voxel models and
	// ground shadows are software-only for now. Anything else -> "software".
	Renderer string `json:"renderer"`
	// LightScale multiplies every sector's light level before it is shaded,
	// in both pipelines: 1.0 is the WAD's authored brightness, lower is
	// darker (0.75, the default, is a deliberately moodier baseline than
	// vanilla Doom), higher washes the map out. Clamped to [0.1, 2.0].
	LightScale float64 `json:"lightScale"`
	// Exposure is a final multiply on the enhanced pipeline's HDR colour
	// just before the tonemap — the knob to pull the whole image up or down
	// after LightScale and the emitters have been summed. 1.0 default,
	// clamped to [0.1, 4.0]. Ignored by the vanilla pipeline.
	Exposure float64 `json:"exposure"`
	// Shadows toggles the enhanced pipeline's screen-space light shadows:
	// the nearest dynamic lights are occluded by geometry between them and a
	// surface. Default true. (Vanilla mode has no dynamic lights, so this
	// does nothing there.)
	Shadows bool `json:"shadows"`
	// GroundShadows toggles the cheap contact-shadow blob drawn on the floor
	// under every thing and the player (both pipelines). It grounds sprites/
	// voxels but reads as a plain dark ellipse up close, so it's OFF by
	// default — set true to enable.
	GroundShadows bool `json:"groundShadows"`
	// FogColor is the distance-fog colour as a hex "RRGGBB" string (a
	// leading '#' is allowed). Empty (the default) disables fog. Works in
	// all three render paths.
	FogColor string `json:"fogColor"`
	// FogDensity is the exponential distance-fog strength: fogFactor =
	// 1 - exp(-density * cameraDistance), in map units. 0 (default) = off;
	// ~0.0006 is a light haze, ~0.003 a heavy murk. Clamped to [0, 0.02].
	// Fog is only applied when FogColor is set AND FogDensity > 0.
	FogDensity float64 `json:"fogDensity"`
	// IWAD is an optional explicit path to the base IWAD (DOOM2.WAD,
	// DOOM.WAD, ...). It's only consulted when the WAD you pick at startup is
	// a PWAD (a patch that has no palette of its own): the engine merges the
	// two, load order IWAD-then-PWAD, like Doom's `-file`. Leave it empty to
	// let the engine auto-find an IWAD next to the PWAD or up its parent
	// directories.
	IWAD string `json:"iwad"`
	// Map is the map marker to start on (e.g. "E1M5", "MAP07"),
	// case-insensitive. Empty (the default) means ask at startup when the
	// WAD has more than one map, falling back to the conventional first
	// level (E1M1 / MAP01). A name not present in the loaded WAD is ignored
	// with a warning.
	Map string `json:"map"`
	// Crosshair is the small aiming marker drawn at the centre of the
	// screen: "dot" (the default — a tiny filled white square), "cross" (a
	// thin white plus with a centre gap), or "off". Case-insensitive;
	// anything else -> "dot". It scales with the vertical render resolution
	// so it looks the same on any monitor, and is hidden while the player is
	// dead.
	Crosshair string `json:"crosshair"`
	// CrosshairSize scales the crosshair: "tiny", "small", "medium" (the
	// default), or "large". Case-insensitive; anything else -> "medium".
	CrosshairSize string `json:"crosshairSize"`
}

// FOV bounds.
const (
	MinFOV = 70.0
	MaxFOV = 130.0
)

// RenderScale bounds.
const (
	MinRenderScale = 0.25
	MaxRenderScale = 2.0
)

// HUDScale bounds.
const (
	MinHUDScale = 0.25
	MaxHUDScale = 2.0
)

// WeaponScale bounds.
const (
	MinWeaponScale = 0.25
	MaxWeaponScale = 2.0
)

// Input bounds.
const (
	MinMouseSensitivity = 0.0002
	MaxMouseSensitivity = 0.02
	MinTurnSpeed        = 0.5
	MaxTurnSpeed        = 10.0
)

// Skill bounds (id's skill 1..5).
const (
	MinSkill = 1
	MaxSkill = 5
)

// VoxelScale bounds (map units per voxel).
const (
	MinVoxelScale = 0.25
	MaxVoxelScale = 4.0
)

// Lighting bounds.
const (
	MinLightScale = 0.1
	MaxLightScale = 2.0
	MinExposure   = 0.1
	MaxExposure   = 4.0
)

// WeaponVolume bounds (a linear multiplier on weapon sound loudness).
const (
	MinWeaponVolume = 0.0
	MaxWeaponVolume = 2.0
)

// WorldVolume bounds (a linear multiplier on level-machinery sound loudness).
const (
	MinWorldVolume = 0.0
	MaxWorldVolume = 2.0
)

// presets maps a resolution keyword to a fixed render size. "native" is
// handled separately — it means "whatever the window's framebuffer is".
var presets = map[string][2]int{
	"540p":  {960, 540},
	"600p":  {800, 600},
	"720p":  {1280, 720},
	"900p":  {1600, 900},
	"1080p": {1920, 1080},
	"1440p": {2560, 1440},
}

const fileName = "config.json"

// Default is the configuration used when config.json is absent, and the
// source of per-field fallbacks when it's present but a value is bad.
func Default() Config {
	return Config{
		Resolution:           "720p",
		RenderScale:          1.0,
		Windowed:             false,
		WindowWidth:          1280,
		WindowHeight:         720,
		FOVDegrees:           100,
		HUDScale:             1.0,
		WeaponScale:          1.0,
		VSync:                true,
		SwitchWeaponOnPickup: true,
		InvertMouseY:         false,
		MouseSensitivity:     0.0022,
		TurnSpeed:            2.5,
		AlwaysRun:            false,
		Skill:                3,
		DamageScale:          100,
		Music:                true,
		WeaponVolume:         1.0,
		WorldVolume:          0.5,
		ShowDebugInfo:        true,
		ShowStatusBar:        true,
		HUDStyle:             HUDStyleDoom,
		Voxels:               true,
		Flashlight:           true,
		VoxelScale:           1.0,
		Quake1Textures:       false,
		TextureQuality:       TextureNearest,
		MaxFPS:               0,
		LightingMode:         LightingEnhanced,
		Renderer:             RendererSoftware,
		LightScale:           0.75,
		Exposure:             1.0,
		Shadows:              true,
		GroundShadows:        false,
		FogColor:             "",
		FogDensity:           0,
		Crosshair:            CrosshairDot,
		CrosshairSize:        CrosshairMedium,
	}
}

// DefaultJSON is Default() rendered as an indented config.json, every key
// present — the canonical "here is every option" file. tools/genconfig
// writes it; a test asserts it stays in sync with the Config struct so a
// newly-added field can't silently go missing from it.
func DefaultJSON() ([]byte, error) {
	b, err := json.MarshalIndent(Default(), "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// Lighting mode keywords (Config.LightingMode).
const (
	LightingVanilla  = "vanilla"
	LightingEnhanced = "enhanced"
)

// Renderer keywords (Config.Renderer).
const (
	RendererSoftware = "software"
	RendererHardware = "hardware"
)

// Texture-quality keywords (Config.TextureQuality). The four "aniso" values
// are trilinear plus that many anisotropic taps.
const (
	TextureNearest   = "nearest"
	TextureBilinear  = "bilinear"
	TextureTrilinear = "trilinear"
	TextureAniso2x   = "aniso2x"
	TextureAniso4x   = "aniso4x"
	TextureAniso8x   = "aniso8x"
	TextureAniso16x  = "aniso16x"
)

// textureQualityKeywords is every accepted TextureQuality value, for
// repaired()'s validation.
var textureQualityKeywords = map[string]bool{
	TextureNearest: true, TextureBilinear: true, TextureTrilinear: true,
	TextureAniso2x: true, TextureAniso4x: true, TextureAniso8x: true, TextureAniso16x: true,
}

// Crosshair keywords (Config.Crosshair).
const (
	CrosshairOff   = "off"
	CrosshairDot   = "dot"
	CrosshairCross = "cross"
)

// Crosshair size keywords (Config.CrosshairSize).
const (
	CrosshairTiny   = "tiny"
	CrosshairSmall  = "small"
	CrosshairMedium = "medium"
	CrosshairLarge  = "large"
)

// HUD-style keywords (Config.HUDStyle).
const (
	HUDStyleDoom   = "doom"
	HUDStyleQuake2 = "quake2"
	HUDStyleNone   = "none"
)

// Load reads config.json from the working directory, then from beside the
// executable. A missing file is not an error. A malformed file, or an
// unrecognized/out-of-range field, logs a warning and uses the default for
// that part.
func Load() Config {
	cfg := Default()
	path, ok := find()
	if !ok {
		wd, _ := os.Getwd()
		log.Printf("config: no %s in %s or next to the executable — using built-in defaults", fileName, wd)
		return cfg.repaired()
	}
	if raw, err := os.ReadFile(path); err != nil {
		log.Printf("config: %v (using defaults)", err)
	} else if err := json.Unmarshal(raw, &cfg); err != nil {
		// Decode failed outright — don't trust any of it.
		log.Printf("config: %s: %v (using defaults)", path, err)
		cfg = Default()
	} else {
		abs, _ := filepath.Abs(path)
		log.Printf("config: loaded %s", abs)
		if missing := missingKeys(raw); len(missing) > 0 {
			log.Printf("config: %d option(s) not in the file, using defaults: %s — run `go run ./tools/genconfig -f` for a complete config.json",
				len(missing), strings.Join(missing, " "))
		}
		if dep := deprecatedKeys(raw); len(dep) > 0 {
			log.Printf("config: ignoring deprecated key(s) %s — use \"textureQuality\" (nearest/bilinear/trilinear/aniso2x..16x) instead",
				strings.Join(dep, " "))
		}
		// Back-compat: an old config that set "showStatusBar": false and has
		// no "hudStyle" key means "no HUD" — map it to hudStyle "none".
		if keyPresent(raw, "showStatusBar") && !keyPresent(raw, "hudStyle") && !cfg.ShowStatusBar {
			cfg.HUDStyle = HUDStyleNone
			log.Printf("config: showStatusBar:false with no hudStyle — using hudStyle %q", HUDStyleNone)
		}
	}
	return cfg.repaired()
}

// deprecatedKeys reports which retired config keys appear in the file, so a
// user editing an old config.json is told their setting is being ignored
// rather than silently having no effect.
func deprecatedKeys(raw []byte) []string {
	var present map[string]json.RawMessage
	if json.Unmarshal(raw, &present) != nil {
		return nil
	}
	var dep []string
	for _, k := range []string{"spriteFilter", "voxelFilter"} {
		if _, ok := present[k]; ok {
			dep = append(dep, k)
		}
	}
	return dep
}

// keyPresent reports whether the given top-level key appears in the raw
// config.json bytes.
func keyPresent(raw []byte, key string) bool {
	var present map[string]json.RawMessage
	if json.Unmarshal(raw, &present) != nil {
		return false
	}
	_, ok := present[key]
	return ok
}

// missingKeys reports the json tags of Config fields that don't appear as
// keys in the given config.json bytes — so a partial file's absent options
// are visible at startup rather than silently defaulted.
func missingKeys(raw []byte) []string {
	var present map[string]json.RawMessage
	if json.Unmarshal(raw, &present) != nil {
		return nil
	}
	ct := reflect.TypeOf(Config{})
	var missing []string
	for i := 0; i < ct.NumField(); i++ {
		name, _, _ := strings.Cut(ct.Field(i).Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		if _, ok := present[name]; !ok {
			missing = append(missing, name)
		}
	}
	return missing
}

func (c Config) repaired() Config {
	d := Default()

	res := strings.ToLower(strings.TrimSpace(c.Resolution))
	if _, ok := presets[res]; !ok && res != "native" {
		if res != "" {
			log.Printf("config: unknown resolution %q, using %q", c.Resolution, d.Resolution)
		}
		res = d.Resolution
	}
	c.Resolution = res

	if c.RenderScale == 0 {
		c.RenderScale = d.RenderScale
	}
	if c.RenderScale < MinRenderScale || c.RenderScale > MaxRenderScale {
		log.Printf("config: renderScale %.2f out of [%.2f, %.2f], using %.2f",
			c.RenderScale, MinRenderScale, MaxRenderScale, d.RenderScale)
		c.RenderScale = d.RenderScale
	}

	if c.WindowWidth <= 0 || c.WindowHeight <= 0 {
		c.WindowWidth, c.WindowHeight = d.WindowWidth, d.WindowHeight
	}

	if c.FOVDegrees < MinFOV || c.FOVDegrees > MaxFOV {
		log.Printf("config: fovDegrees %.0f out of [%.0f, %.0f], using %.0f",
			c.FOVDegrees, MinFOV, MaxFOV, d.FOVDegrees)
		c.FOVDegrees = d.FOVDegrees
	}

	if c.HUDScale == 0 {
		c.HUDScale = d.HUDScale
	}
	if c.HUDScale < MinHUDScale || c.HUDScale > MaxHUDScale {
		log.Printf("config: hudScale %.2f out of [%.2f, %.2f], using %.2f",
			c.HUDScale, MinHUDScale, MaxHUDScale, d.HUDScale)
		c.HUDScale = d.HUDScale
	}

	if c.WeaponScale == 0 {
		c.WeaponScale = d.WeaponScale
	}
	if c.WeaponScale < MinWeaponScale || c.WeaponScale > MaxWeaponScale {
		log.Printf("config: weaponScale %.2f out of [%.2f, %.2f], using %.2f",
			c.WeaponScale, MinWeaponScale, MaxWeaponScale, d.WeaponScale)
		c.WeaponScale = d.WeaponScale
	}

	if c.MouseSensitivity == 0 {
		c.MouseSensitivity = d.MouseSensitivity
	}
	if c.MouseSensitivity < MinMouseSensitivity || c.MouseSensitivity > MaxMouseSensitivity {
		log.Printf("config: mouseSensitivity %.4f out of [%.4f, %.4f], using %.4f",
			c.MouseSensitivity, MinMouseSensitivity, MaxMouseSensitivity, d.MouseSensitivity)
		c.MouseSensitivity = d.MouseSensitivity
	}

	if c.TurnSpeed == 0 {
		c.TurnSpeed = d.TurnSpeed
	}
	if c.TurnSpeed < MinTurnSpeed || c.TurnSpeed > MaxTurnSpeed {
		log.Printf("config: turnSpeed %.2f out of [%.2f, %.2f], using %.2f",
			c.TurnSpeed, MinTurnSpeed, MaxTurnSpeed, d.TurnSpeed)
		c.TurnSpeed = d.TurnSpeed
	}

	if c.Skill == 0 {
		c.Skill = d.Skill
	}
	if c.Skill < MinSkill || c.Skill > MaxSkill {
		log.Printf("config: skill %d out of [%d, %d], using %d", c.Skill, MinSkill, MaxSkill, d.Skill)
		c.Skill = d.Skill
	}

	if c.MaxFPS < 0 {
		c.MaxFPS = 0
	}

	c.IWAD = strings.TrimSpace(c.IWAD)
	c.Map = strings.TrimSpace(c.Map)

	if c.DamageScale == 0 {
		c.DamageScale = d.DamageScale
	}
	if c.DamageScale < 1 || c.DamageScale > 100000 {
		log.Printf("config: damageScale %d out of [1, 100000], using %d", c.DamageScale, d.DamageScale)
		c.DamageScale = d.DamageScale
	}

	if c.WeaponVolume == 0 {
		c.WeaponVolume = d.WeaponVolume
	}
	if c.WeaponVolume < MinWeaponVolume || c.WeaponVolume > MaxWeaponVolume {
		log.Printf("config: weaponVolume %.2f out of [%.2f, %.2f], using %.2f",
			c.WeaponVolume, MinWeaponVolume, MaxWeaponVolume, d.WeaponVolume)
		c.WeaponVolume = d.WeaponVolume
	}

	if c.WorldVolume == 0 {
		c.WorldVolume = d.WorldVolume
	}
	if c.WorldVolume < MinWorldVolume || c.WorldVolume > MaxWorldVolume {
		log.Printf("config: worldVolume %.2f out of [%.2f, %.2f], using %.2f",
			c.WorldVolume, MinWorldVolume, MaxWorldVolume, d.WorldVolume)
		c.WorldVolume = d.WorldVolume
	}

	if c.VoxelScale == 0 {
		c.VoxelScale = d.VoxelScale
	}
	if c.VoxelScale < MinVoxelScale || c.VoxelScale > MaxVoxelScale {
		log.Printf("config: voxelScale %.2f out of [%.2f, %.2f], using %.2f",
			c.VoxelScale, MinVoxelScale, MaxVoxelScale, d.VoxelScale)
		c.VoxelScale = d.VoxelScale
	}

	switch tq := strings.ToLower(strings.TrimSpace(c.TextureQuality)); {
	case tq == "":
		c.TextureQuality = d.TextureQuality
	case textureQualityKeywords[tq]:
		c.TextureQuality = tq
	default:
		log.Printf("config: unknown textureQuality %q, using %q", c.TextureQuality, d.TextureQuality)
		c.TextureQuality = d.TextureQuality
	}

	switch mode := strings.ToLower(strings.TrimSpace(c.LightingMode)); mode {
	case LightingVanilla, LightingEnhanced:
		c.LightingMode = mode
	case "":
		c.LightingMode = d.LightingMode
	default:
		log.Printf("config: unknown lightingMode %q, using %q", c.LightingMode, d.LightingMode)
		c.LightingMode = d.LightingMode
	}

	switch mode := strings.ToLower(strings.TrimSpace(c.Renderer)); mode {
	case RendererSoftware, RendererHardware:
		c.Renderer = mode
	case "":
		c.Renderer = d.Renderer
	default:
		log.Printf("config: unknown renderer %q, using %q", c.Renderer, d.Renderer)
		c.Renderer = d.Renderer
	}

	if c.LightScale == 0 {
		c.LightScale = d.LightScale
	}
	if c.LightScale < MinLightScale || c.LightScale > MaxLightScale {
		log.Printf("config: lightScale %.2f out of [%.2f, %.2f], using %.2f",
			c.LightScale, MinLightScale, MaxLightScale, d.LightScale)
		c.LightScale = d.LightScale
	}

	if c.Exposure == 0 {
		c.Exposure = d.Exposure
	}
	if c.Exposure < MinExposure || c.Exposure > MaxExposure {
		log.Printf("config: exposure %.2f out of [%.2f, %.2f], using %.2f",
			c.Exposure, MinExposure, MaxExposure, d.Exposure)
		c.Exposure = d.Exposure
	}

	switch mode := strings.ToLower(strings.TrimSpace(c.Crosshair)); mode {
	case CrosshairOff, CrosshairDot, CrosshairCross:
		c.Crosshair = mode
	case "":
		c.Crosshair = d.Crosshair
	default:
		log.Printf("config: unknown crosshair %q, using %q", c.Crosshair, d.Crosshair)
		c.Crosshair = d.Crosshair
	}

	switch sz := strings.ToLower(strings.TrimSpace(c.CrosshairSize)); sz {
	case CrosshairTiny, CrosshairSmall, CrosshairMedium, CrosshairLarge:
		c.CrosshairSize = sz
	case "":
		c.CrosshairSize = d.CrosshairSize
	default:
		log.Printf("config: unknown crosshairSize %q, using %q", c.CrosshairSize, d.CrosshairSize)
		c.CrosshairSize = d.CrosshairSize
	}

	switch hs := strings.ToLower(strings.TrimSpace(c.HUDStyle)); hs {
	case HUDStyleDoom, HUDStyleQuake2, HUDStyleNone:
		c.HUDStyle = hs
	case "":
		c.HUDStyle = d.HUDStyle
	default:
		log.Printf("config: unknown hudStyle %q, using %q", c.HUDStyle, d.HUDStyle)
		c.HUDStyle = d.HUDStyle
	}

	if fc := strings.TrimSpace(c.FogColor); fc != "" {
		if _, _, _, ok := parseHexRGB(fc); ok {
			c.FogColor = strings.TrimPrefix(fc, "#")
		} else {
			log.Printf("config: fogColor %q is not a hex RRGGBB, disabling fog", c.FogColor)
			c.FogColor = ""
		}
	} else {
		c.FogColor = ""
	}
	if c.FogDensity < 0 || c.FogDensity > MaxFogDensity {
		log.Printf("config: fogDensity %.4f out of [0, %.4f], clamping", c.FogDensity, MaxFogDensity)
		if c.FogDensity < 0 {
			c.FogDensity = 0
		} else {
			c.FogDensity = MaxFogDensity
		}
	}

	return c
}

// MaxFogDensity caps FogDensity (config.json). At 0.02 the view is opaque
// within ~200 map units — well past any useful setting.
const MaxFogDensity = 0.02

// parseHexRGB parses "RRGGBB" / "#RRGGBB" into linear-ish 0..1 components.
func parseHexRGB(s string) (r, g, b float32, ok bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) != 6 {
		return 0, 0, 0, false
	}
	var v [3]int64
	for i := 0; i < 3; i++ {
		n, err := strconv.ParseInt(s[i*2:i*2+2], 16, 0)
		if err != nil {
			return 0, 0, 0, false
		}
		v[i] = n
	}
	return float32(v[0]) / 255, float32(v[1]) / 255, float32(v[2]) / 255, true
}

// Fog returns the parsed fog colour (0..1) and density, and whether fog is
// active (a valid colour AND density > 0). Consumers pass these to the
// renderers.
func (c Config) Fog() (r, g, b float32, density float64, on bool) {
	r, g, b, ok := parseHexRGB(c.FogColor)
	if !ok || c.FogDensity <= 0 {
		return 0, 0, 0, 0, false
	}
	return r, g, b, c.FogDensity, true
}

// RenderSize resolves the internal render-target size in pixels.
//
//   - Windowed: the render target is the window's own framebuffer
//     (WindowWidth x WindowHeight, in real pixels) — the Resolution preset
//     is ignored, so the window size is exactly what gets rendered and
//     there is no rescale on present.
//   - Full screen: the render target is the Resolution preset ("native" =
//     the monitor's framebuffer), presented letterboxed to the screen —
//     but capped to the monitor size unless RenderScale > 1, since a CPU
//     rasterizer rendering above the display only to downscale on present
//     is wasted work (and the cap also gives an ultrawide monitor its
//     proper Hor+ aspect instead of a cropped 16:9 render).
//
// RenderScale then multiplies whichever base was chosen (0.5 = render a
// quarter of the pixels and let the GPU upscale; > 1 = supersample).
// fbW/fbH is the window's current framebuffer size.
func (c Config) RenderSize(windowed bool, fbW, fbH int) (w, h int) {
	hasFB := fbW > 0 && fbH > 0
	switch {
	case windowed && hasFB:
		w, h = fbW, fbH // window size wins; Resolution preset is not consulted
	case !windowed && presetOK(c.Resolution):
		p := presets[c.Resolution]
		w, h = p[0], p[1]
		if hasFB && c.RenderScale <= 1 {
			if fbW < w {
				w = fbW
			}
			if fbH < h {
				h = fbH
			}
		}
	case hasFB:
		w, h = fbW, fbH // full-screen "native", or windowed before a framebuffer exists
	default:
		p := presets[Default().Resolution]
		w, h = p[0], p[1]
	}

	s := c.RenderScale
	if s <= 0 {
		s = 1
	}
	w = int(math.Round(float64(w) * s))
	h = int(math.Round(float64(h) * s))
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	return w, h
}

func presetOK(name string) bool {
	_, ok := presets[name]
	return ok
}

// find locates config.json: the working directory first, then the
// executable's own directory and a few parents of it. The walk up from the
// exe is what makes "bin\engine.exe" pick up a config.json sitting in the
// repo root whether it's launched from the root, from bin\, or by a
// double-click (where the working directory is anyone's guess).
func find() (string, bool) {
	wd, _ := os.Getwd()
	exeDir := ""
	if exe, err := os.Executable(); err == nil {
		exeDir = filepath.Dir(exe)
	}
	return findFrom(wd, exeDir)
}

// findFrom is the testable core: check wd/config.json, then
// exeDir/config.json and up to 3 parent directories of exeDir.
func findFrom(wd, exeDir string) (string, bool) {
	if wd != "" {
		if cand := filepath.Join(wd, fileName); statFile(cand) {
			return cand, true
		}
	}
	dir := exeDir
	for i := 0; dir != "" && i < 4; i++ {
		if cand := filepath.Join(dir, fileName); statFile(cand) {
			return cand, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break // reached the filesystem root
		}
		dir = parent
	}
	return "", false
}

func statFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}
