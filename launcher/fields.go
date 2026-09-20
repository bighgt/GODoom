package launcher

import "twopointfive/config"

// kind picks which widget a field row draws.
type kind int

const (
	kindCheckbox kind = iota
	kindSlider
	kindIntSlider
	kindIntField
	kindOptions
	kindTextField
)

// field is one row of the settings panel: an id (also the widget kit's
// per-widget state key), a label, which Section it groups under, which
// widget draws it, and typed accessors straight into a *config.Config — no
// generic get/set-by-reflection, just a closure per row.
type field struct {
	id      string
	label   string
	hint    string // one-line explanation, shown as a hover tooltip
	section string
	kind    kind

	boolAt   func(*config.Config) *bool
	floatAt  func(*config.Config) *float64
	intAt    func(*config.Config) *int
	stringAt func(*config.Config) *string

	min, max float64
	format   string
	options  []choice
}

// choice is one dropdown entry: value is the literal config.json keyword
// written to Config (and compared against its current value) — label is
// what's actually shown in the UI. Config's own accepted keywords (config.go's
// TextureNearest, HUDStyleDoom, ...) read fine in a doc comment or a log
// line, but "aniso8x" or "quake2" on their own aren't a settings label
// anyone not already reading the source would recognize.
type choice struct{ value, label string }

// choices builds a []choice with the label equal to the value — for a
// dropdown whose values are already human-readable (the map-name dropdown
// in screen.go's top bar; a config keyword list gets its own hand-written
// labels instead, see resolutionOptions etc. below).
func choices(values []string) []choice {
	out := make([]choice, len(values))
	for i, v := range values {
		out[i] = choice{value: v, label: v}
	}
	return out
}

// Section names — one per settings tab, in tab order.
const (
	SectionDisplay   = "Display"
	SectionRendering = "Rendering & Lighting"
	SectionAudio     = "Audio"
	SectionInput     = "Input & Controls"
	SectionGameplay  = "Gameplay"
	SectionHUD       = "HUD & Crosshair"
	SectionAdvanced  = "Advanced"
)

var sectionOrder = []string{
	SectionDisplay, SectionRendering, SectionAudio, SectionInput,
	SectionGameplay, SectionHUD, SectionAdvanced,
}

// tabTitles is the short label each section shows on the tab strip — the
// full Section* strings ("Rendering & Lighting", "HUD & Crosshair") are too
// wide to fit seven tabs across the 900px panel. fields_test.go asserts
// every section in sectionOrder has one.
var tabTitles = map[string]string{
	SectionDisplay:   "Display",
	SectionRendering: "Graphics",
	SectionAudio:     "Audio",
	SectionInput:     "Controls",
	SectionGameplay:  "Gameplay",
	SectionHUD:       "HUD",
	SectionAdvanced:  "Advanced",
}

// resolutionOptions/textureQualityOptions/... pair exactly config.go's own
// accepted keyword sets (so this table can never drift into offering a
// value config.Load's repaired() would immediately reject) with a label
// meant for the settings screen rather than a config.json value — several
// of the raw keywords (aniso8x, quake2, hudStyle's "none") read as internal
// shorthand rather than a setting name.
var (
	resolutionOptions = []choice{
		{"540p", "540p (960x540)"},
		{"600p", "600p (800x600)"},
		{"720p", "720p (1280x720)"},
		{"900p", "900p (1600x900)"},
		{"1080p", "1080p (1920x1080)"},
		{"1440p", "1440p (2560x1440)"},
		{"native", "Native (Display Resolution)"},
	}
	hudStyleOptions = []choice{
		{config.HUDStyleDoom, "Doom (Full Status Bar)"},
		{config.HUDStyleQuake2, "Quake II (Minimal HUD)"},
		{config.HUDStyleNone, "None"},
	}
	crosshairOptions = []choice{
		{config.CrosshairOff, "Off"},
		{config.CrosshairDot, "Dot"},
		{config.CrosshairCross, "Cross"},
	}
	crosshairSizeOptions = []choice{
		{config.CrosshairTiny, "Tiny"},
		{config.CrosshairSmall, "Small"},
		{config.CrosshairMedium, "Medium"},
		{config.CrosshairLarge, "Large"},
	}
)

// fields is every config.Config field this launcher exposes, in the order
// they're declared on Config — every field EXCEPT:
//   - Map: it's the dedicated map dropdown in screen.go's top bar, not a
//     settings-panel row.
//   - ShowStatusBar: documented on Config as a deprecated back-compat shim
//     superseded by HUDStyle; SaveMerge simply never touches that key.
//   - WADPath: set programmatically when a WAD is picked (screen.go), not
//     user-edited as its own row.
//   - CartoonShading: a whole-scene visual-style switch, promoted to its
//     own row in screen.go's top bar (next to the WAD/level pickers, where
//     it's seen before scrolling past it) rather than the settings panel.
//
// fields_test.go asserts this list plus those explicit exclusions accounts
// for every field on config.Config, the same way config_test.go's
// TestDefaultJSONIsComplete guards DefaultJSON — so a field added to Config
// later can't silently go missing from the launcher too.
var fields = []field{
	// --- Display ---
	{id: "resolution", label: "Resolution", hint: "Internal render resolution. Higher is sharper but costs performance.", section: SectionDisplay, kind: kindOptions,
		stringAt: func(c *config.Config) *string { return &c.Resolution }, options: resolutionOptions},
	{id: "renderScale", label: "Render Scale", hint: "Multiplier on the render resolution. Below 1.0 renders smaller then upscales for speed.", section: SectionDisplay, kind: kindSlider,
		floatAt: func(c *config.Config) *float64 { return &c.RenderScale }, min: config.MinRenderScale, max: config.MaxRenderScale, format: "%.2f"},
	{id: "windowed", label: "Windowed", hint: "Run in a resizable window instead of borderless fullscreen.", section: SectionDisplay, kind: kindCheckbox,
		boolAt: func(c *config.Config) *bool { return &c.Windowed }},
	{id: "windowWidth", label: "Window Width", hint: "Window width in pixels; also its render width (windowed mode only).", section: SectionDisplay, kind: kindIntField,
		intAt: func(c *config.Config) *int { return &c.WindowWidth }},
	{id: "windowHeight", label: "Window Height", hint: "Window height in pixels; also its render height (windowed mode only).", section: SectionDisplay, kind: kindIntField,
		intAt: func(c *config.Config) *int { return &c.WindowHeight }},
	{id: "fovDegrees", label: "Field of View", hint: "Horizontal field of view in degrees. Wider shows more but distorts the edges.", section: SectionDisplay, kind: kindSlider,
		floatAt: func(c *config.Config) *float64 { return &c.FOVDegrees }, min: config.MinFOV, max: config.MaxFOV, format: "%.0f°"},
	{id: "vsync", label: "V-Sync", hint: "Sync frame presentation to the monitor refresh to remove tearing.", section: SectionDisplay, kind: kindCheckbox,
		boolAt: func(c *config.Config) *bool { return &c.VSync }},
	{id: "maxFPS", label: "Max FPS (0=uncapped)", hint: "Frame-rate cap. 0 leaves it uncapped.", section: SectionDisplay, kind: kindIntField,
		intAt: func(c *config.Config) *int { return &c.MaxFPS }},
	{id: "showDebugInfo", label: "Debug Overlay", hint: "Show the on-screen debug overlay: FPS, player position and frame timings.", section: SectionDisplay, kind: kindCheckbox,
		boolAt: func(c *config.Config) *bool { return &c.ShowDebugInfo }},

	// --- Rendering & Lighting ---
	{id: "lightScale", label: "Light Scale", hint: "Overall brightness of dynamic lights.", section: SectionRendering, kind: kindSlider,
		floatAt: func(c *config.Config) *float64 { return &c.LightScale }, min: config.MinLightScale, max: config.MaxLightScale, format: "%.2f"},
	{id: "ambientLight", label: "Ambient Light", hint: "Minimum light level everywhere, so nothing falls to pure black.", section: SectionRendering, kind: kindSlider,
		floatAt: func(c *config.Config) *float64 { return &c.AmbientLight }, min: config.MinAmbientLight, max: config.MaxAmbientLight, format: "%.2f"},
	{id: "exposure", label: "Exposure", hint: "Tone-mapping exposure for the hardware renderer. Higher is brighter.", section: SectionRendering, kind: kindSlider,
		floatAt: func(c *config.Config) *float64 { return &c.Exposure }, min: config.MinExposure, max: config.MaxExposure, format: "%.2f"},
	{id: "voxels", label: "Voxel Models", hint: "Render supported items and decorations as 3D voxel models instead of flat sprites.", section: SectionRendering, kind: kindCheckbox,
		boolAt: func(c *config.Config) *bool { return &c.Voxels }},
	{id: "voxelScale", label: "Voxel Scale", hint: "Size multiplier for voxel models.", section: SectionRendering, kind: kindSlider,
		floatAt: func(c *config.Config) *float64 { return &c.VoxelScale }, min: config.MinVoxelScale, max: config.MaxVoxelScale, format: "%.2f"},
	{id: "quake1Textures", label: "Quake 1 Texture Pack", hint: "Swap in the converted Quake 1 textures wherever a matching name exists.", section: SectionRendering, kind: kindCheckbox,
		boolAt: func(c *config.Config) *bool { return &c.Quake1Textures }},
	{id: "explosionParticles", label: "Explosion Particles", hint: "Flying spark billboards on rocket/plasma/BFG/fireball impacts. Purely cosmetic.", section: SectionRendering, kind: kindCheckbox,
		boolAt: func(c *config.Config) *bool { return &c.ExplosionParticles }},
	{id: "liquidAmbience", label: "Liquid Ambience", hint: "Steam off lava, mist over water, bubbles in acid, and a splash when crossing in/out. Purely cosmetic.", section: SectionRendering, kind: kindCheckbox,
		boolAt: func(c *config.Config) *bool { return &c.LiquidAmbience }},
	{id: "waterRefraction", label: "Water Refraction", hint: "Real depth-based refraction/murk for water. Only visibly differs from the default fake shading on maps with real geometry beneath a transparent water surface.", section: SectionRendering, kind: kindCheckbox,
		boolAt: func(c *config.Config) *bool { return &c.WaterRefraction }},
	{id: "fogColor", label: "Fog Colour (hex RRGGBB)", hint: "Distance-fog colour as hex RRGGBB, e.g. 202830.", section: SectionRendering, kind: kindTextField,
		stringAt: func(c *config.Config) *string { return &c.FogColor }},
	{id: "fogDensity", label: "Fog Density", hint: "Thickness of distance fog. 0 disables it.", section: SectionRendering, kind: kindSlider,
		floatAt: func(c *config.Config) *float64 { return &c.FogDensity }, min: 0, max: config.MaxFogDensity, format: "%.4f"},

	// --- Audio ---
	// No "Music" checkbox: the slider IS the on/off — drag it to 0 and
	// run.go turns cfg.Music off (NewModel shows the slider at 0 when it
	// was). One control, not a checkbox plus a redundant volume.
	{id: "musicVolume", label: "Music Volume", hint: "Level background music. Drag to 0 to turn music off.", section: SectionAudio, kind: kindSlider,
		floatAt: func(c *config.Config) *float64 { return &c.MusicVolume }, min: config.MinMusicVolume, max: config.MaxMusicVolume, format: "%.2f"},
	{id: "weaponVolume", label: "Weapon Volume", hint: "Volume of the player's own weapons.", section: SectionAudio, kind: kindSlider,
		floatAt: func(c *config.Config) *float64 { return &c.WeaponVolume }, min: config.MinWeaponVolume, max: config.MaxWeaponVolume, format: "%.2f"},
	{id: "worldVolume", label: "World Volume", hint: "Volume of everything else: monsters, doors, pickups and ambience.", section: SectionAudio, kind: kindSlider,
		floatAt: func(c *config.Config) *float64 { return &c.WorldVolume }, min: config.MinWorldVolume, max: config.MaxWorldVolume, format: "%.2f"},

	// --- Input & Controls ---
	{id: "invertMouseY", label: "Invert Mouse Y", hint: "Push the mouse forward to look down instead of up.", section: SectionInput, kind: kindCheckbox,
		boolAt: func(c *config.Config) *bool { return &c.InvertMouseY }},
	{id: "mouseSensitivity", label: "Mouse Sensitivity", hint: "How far the view turns per unit of mouse movement.", section: SectionInput, kind: kindSlider,
		floatAt: func(c *config.Config) *float64 { return &c.MouseSensitivity }, min: config.MinMouseSensitivity, max: config.MaxMouseSensitivity, format: "%.4f"},
	{id: "turnSpeed", label: "Keyboard Turn Speed", hint: "How fast the arrow / turn keys rotate the view.", section: SectionInput, kind: kindSlider,
		floatAt: func(c *config.Config) *float64 { return &c.TurnSpeed }, min: config.MinTurnSpeed, max: config.MaxTurnSpeed, format: "%.2f"},
	{id: "alwaysRun", label: "Always Run", hint: "Move at run speed without holding the run key.", section: SectionInput, kind: kindCheckbox,
		boolAt: func(c *config.Config) *bool { return &c.AlwaysRun }},

	// --- Gameplay ---
	{id: "skill", label: "Skill", hint: "Difficulty 1 (easiest) to 5 (Nightmare): monster count, damage and respawns.", section: SectionGameplay, kind: kindIntSlider,
		intAt: func(c *config.Config) *int { return &c.Skill }, min: config.MinSkill, max: config.MaxSkill, format: "%.0f"},
	{id: "damageScale", label: "Damage Scale %", hint: "Percentage multiplier on damage the player takes. 100 is normal.", section: SectionGameplay, kind: kindIntField,
		intAt: func(c *config.Config) *int { return &c.DamageScale }},
	{id: "switchWeaponOnPickup", label: "Auto-Switch Weapon", hint: "Automatically switch to a newly picked-up weapon.", section: SectionGameplay, kind: kindCheckbox,
		boolAt: func(c *config.Config) *bool { return &c.SwitchWeaponOnPickup }},
	{id: "switchWeapons", label: "Swap Doom/Heretic Weapons", hint: "Give Doom's arsenal in Heretic and Heretic's in Doom, including the on-map pickups.", section: SectionGameplay, kind: kindCheckbox,
		boolAt: func(c *config.Config) *bool { return &c.SwitchWeapons }},
	{id: "flashlight", label: "Flashlight Enabled", hint: "Enable the toggleable player flashlight.", section: SectionGameplay, kind: kindCheckbox,
		boolAt: func(c *config.Config) *bool { return &c.Flashlight }},

	// --- HUD & Crosshair ---
	{id: "hudStyle", label: "HUD Style", hint: "Doom's full status bar, a minimal Quake II readout, or no HUD at all.", section: SectionHUD, kind: kindOptions,
		stringAt: func(c *config.Config) *string { return &c.HUDStyle }, options: hudStyleOptions},
	{id: "hudScale", label: "HUD Scale", hint: "Size of the HUD and status bar.", section: SectionHUD, kind: kindSlider,
		floatAt: func(c *config.Config) *float64 { return &c.HUDScale }, min: config.MinHUDScale, max: config.MaxHUDScale, format: "%.2f"},
	{id: "weaponScale", label: "Weapon Scale", hint: "Size of the first-person weapon sprite.", section: SectionHUD, kind: kindSlider,
		floatAt: func(c *config.Config) *float64 { return &c.WeaponScale }, min: config.MinWeaponScale, max: config.MaxWeaponScale, format: "%.2f"},
	{id: "crosshair", label: "Crosshair", hint: "Crosshair shape, or off.", section: SectionHUD, kind: kindOptions,
		stringAt: func(c *config.Config) *string { return &c.Crosshair }, options: crosshairOptions},
	{id: "crosshairSize", label: "Crosshair Size", hint: "Crosshair size.", section: SectionHUD, kind: kindOptions,
		stringAt: func(c *config.Config) *string { return &c.CrosshairSize }, options: crosshairSizeOptions},

	// --- Advanced ---
	{id: "iwad", label: "IWAD Override Path", hint: "Explicit path to the base IWAD to merge with a PWAD. Blank = auto-detect.", section: SectionAdvanced, kind: kindTextField,
		stringAt: func(c *config.Config) *string { return &c.IWAD }},
}

// fieldsInSection returns fields belonging to section, in table order.
func fieldsInSection(section string) []field {
	var out []field
	for _, f := range fields {
		if f.section == section {
			out = append(out, f)
		}
	}
	return out
}
