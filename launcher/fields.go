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

// Section names, in the order they're drawn.
const (
	SectionDisplay    = "Display"
	SectionRendering  = "Rendering & Lighting"
	SectionAudio      = "Audio"
	SectionInput      = "Input & Controls"
	SectionGameplay   = "Gameplay"
	SectionHUD        = "HUD & Crosshair"
	SectionAdvanced   = "Advanced"
)

var sectionOrder = []string{
	SectionDisplay, SectionRendering, SectionAudio, SectionInput,
	SectionGameplay, SectionHUD, SectionAdvanced,
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
	textureQualityOptions = []choice{
		{config.TextureNearest, "Nearest (Fastest)"},
		{config.TextureBilinear, "Bilinear"},
		{config.TextureTrilinear, "Trilinear"},
		{config.TextureAniso2x, "Anisotropic 2x"},
		{config.TextureAniso4x, "Anisotropic 4x"},
		{config.TextureAniso8x, "Anisotropic 8x"},
		{config.TextureAniso16x, "Anisotropic 16x (Best Quality)"},
	}
	lightingModeOptions = []choice{
		{config.LightingEnhanced, "Enhanced (Dynamic Lighting)"},
		{config.LightingVanilla, "Vanilla (Classic Shading)"},
	}
	rendererOptions = []choice{
		{config.RendererSoftware, "Software (CPU Renderer)"},
		{config.RendererHardware, "Hardware (GPU Renderer)"},
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
//
// fields_test.go asserts this list plus those three explicit exclusions
// accounts for every field on config.Config, the same way config_test.go's
// TestDefaultJSONIsComplete guards DefaultJSON — so a field added to Config
// later can't silently go missing from the launcher too.
var fields = []field{
	// --- Display ---
	{id: "resolution", label: "Resolution", section: SectionDisplay, kind: kindOptions,
		stringAt: func(c *config.Config) *string { return &c.Resolution }, options: resolutionOptions},
	{id: "renderScale", label: "Render Scale", section: SectionDisplay, kind: kindSlider,
		floatAt: func(c *config.Config) *float64 { return &c.RenderScale }, min: config.MinRenderScale, max: config.MaxRenderScale, format: "%.2f"},
	{id: "windowed", label: "Windowed", section: SectionDisplay, kind: kindCheckbox,
		boolAt: func(c *config.Config) *bool { return &c.Windowed }},
	{id: "windowWidth", label: "Window Width", section: SectionDisplay, kind: kindIntField,
		intAt: func(c *config.Config) *int { return &c.WindowWidth }},
	{id: "windowHeight", label: "Window Height", section: SectionDisplay, kind: kindIntField,
		intAt: func(c *config.Config) *int { return &c.WindowHeight }},
	{id: "fovDegrees", label: "Field of View", section: SectionDisplay, kind: kindSlider,
		floatAt: func(c *config.Config) *float64 { return &c.FOVDegrees }, min: config.MinFOV, max: config.MaxFOV, format: "%.0f°"},
	{id: "vsync", label: "V-Sync", section: SectionDisplay, kind: kindCheckbox,
		boolAt: func(c *config.Config) *bool { return &c.VSync }},
	{id: "maxFPS", label: "Max FPS (0=uncapped)", section: SectionDisplay, kind: kindIntField,
		intAt: func(c *config.Config) *int { return &c.MaxFPS }},
	{id: "showDebugInfo", label: "Debug Overlay", section: SectionDisplay, kind: kindCheckbox,
		boolAt: func(c *config.Config) *bool { return &c.ShowDebugInfo }},

	// --- Rendering & Lighting ---
	{id: "renderer", label: "Renderer", section: SectionRendering, kind: kindOptions,
		stringAt: func(c *config.Config) *string { return &c.Renderer }, options: rendererOptions},
	{id: "lightingMode", label: "Lighting Mode", section: SectionRendering, kind: kindOptions,
		stringAt: func(c *config.Config) *string { return &c.LightingMode }, options: lightingModeOptions},
	{id: "lightScale", label: "Light Scale", section: SectionRendering, kind: kindSlider,
		floatAt: func(c *config.Config) *float64 { return &c.LightScale }, min: config.MinLightScale, max: config.MaxLightScale, format: "%.2f"},
	{id: "ambientLight", label: "Ambient Light", section: SectionRendering, kind: kindSlider,
		floatAt: func(c *config.Config) *float64 { return &c.AmbientLight }, min: config.MinAmbientLight, max: config.MaxAmbientLight, format: "%.2f"},
	{id: "exposure", label: "Exposure", section: SectionRendering, kind: kindSlider,
		floatAt: func(c *config.Config) *float64 { return &c.Exposure }, min: config.MinExposure, max: config.MaxExposure, format: "%.2f"},
	{id: "shadows", label: "Dynamic Shadows", section: SectionRendering, kind: kindCheckbox,
		boolAt: func(c *config.Config) *bool { return &c.Shadows }},
	{id: "groundShadows", label: "Ground Shadows", section: SectionRendering, kind: kindCheckbox,
		boolAt: func(c *config.Config) *bool { return &c.GroundShadows }},
	{id: "textureQuality", label: "Texture Quality", section: SectionRendering, kind: kindOptions,
		stringAt: func(c *config.Config) *string { return &c.TextureQuality }, options: textureQualityOptions},
	{id: "voxels", label: "Voxel Models", section: SectionRendering, kind: kindCheckbox,
		boolAt: func(c *config.Config) *bool { return &c.Voxels }},
	{id: "voxelScale", label: "Voxel Scale", section: SectionRendering, kind: kindSlider,
		floatAt: func(c *config.Config) *float64 { return &c.VoxelScale }, min: config.MinVoxelScale, max: config.MaxVoxelScale, format: "%.2f"},
	{id: "quake1Textures", label: "Quake 1 Texture Pack", section: SectionRendering, kind: kindCheckbox,
		boolAt: func(c *config.Config) *bool { return &c.Quake1Textures }},
	{id: "fogColor", label: "Fog Colour (hex RRGGBB)", section: SectionRendering, kind: kindTextField,
		stringAt: func(c *config.Config) *string { return &c.FogColor }},
	{id: "fogDensity", label: "Fog Density", section: SectionRendering, kind: kindSlider,
		floatAt: func(c *config.Config) *float64 { return &c.FogDensity }, min: 0, max: config.MaxFogDensity, format: "%.4f"},

	// --- Audio ---
	{id: "music", label: "Music", section: SectionAudio, kind: kindCheckbox,
		boolAt: func(c *config.Config) *bool { return &c.Music }},
	{id: "weaponVolume", label: "Weapon Volume", section: SectionAudio, kind: kindSlider,
		floatAt: func(c *config.Config) *float64 { return &c.WeaponVolume }, min: config.MinWeaponVolume, max: config.MaxWeaponVolume, format: "%.2f"},
	{id: "worldVolume", label: "World Volume", section: SectionAudio, kind: kindSlider,
		floatAt: func(c *config.Config) *float64 { return &c.WorldVolume }, min: config.MinWorldVolume, max: config.MaxWorldVolume, format: "%.2f"},

	// --- Input & Controls ---
	{id: "invertMouseY", label: "Invert Mouse Y", section: SectionInput, kind: kindCheckbox,
		boolAt: func(c *config.Config) *bool { return &c.InvertMouseY }},
	{id: "mouseSensitivity", label: "Mouse Sensitivity", section: SectionInput, kind: kindSlider,
		floatAt: func(c *config.Config) *float64 { return &c.MouseSensitivity }, min: config.MinMouseSensitivity, max: config.MaxMouseSensitivity, format: "%.4f"},
	{id: "turnSpeed", label: "Keyboard Turn Speed", section: SectionInput, kind: kindSlider,
		floatAt: func(c *config.Config) *float64 { return &c.TurnSpeed }, min: config.MinTurnSpeed, max: config.MaxTurnSpeed, format: "%.2f"},
	{id: "alwaysRun", label: "Always Run", section: SectionInput, kind: kindCheckbox,
		boolAt: func(c *config.Config) *bool { return &c.AlwaysRun }},

	// --- Gameplay ---
	{id: "skill", label: "Skill", section: SectionGameplay, kind: kindIntSlider,
		intAt: func(c *config.Config) *int { return &c.Skill }, min: config.MinSkill, max: config.MaxSkill, format: "%.0f"},
	{id: "damageScale", label: "Damage Scale %", section: SectionGameplay, kind: kindIntField,
		intAt: func(c *config.Config) *int { return &c.DamageScale }},
	{id: "switchWeaponOnPickup", label: "Auto-Switch Weapon", section: SectionGameplay, kind: kindCheckbox,
		boolAt: func(c *config.Config) *bool { return &c.SwitchWeaponOnPickup }},
	{id: "flashlight", label: "Flashlight Enabled", section: SectionGameplay, kind: kindCheckbox,
		boolAt: func(c *config.Config) *bool { return &c.Flashlight }},

	// --- HUD & Crosshair ---
	{id: "hudStyle", label: "HUD Style", section: SectionHUD, kind: kindOptions,
		stringAt: func(c *config.Config) *string { return &c.HUDStyle }, options: hudStyleOptions},
	{id: "hudScale", label: "HUD Scale", section: SectionHUD, kind: kindSlider,
		floatAt: func(c *config.Config) *float64 { return &c.HUDScale }, min: config.MinHUDScale, max: config.MaxHUDScale, format: "%.2f"},
	{id: "weaponScale", label: "Weapon Scale", section: SectionHUD, kind: kindSlider,
		floatAt: func(c *config.Config) *float64 { return &c.WeaponScale }, min: config.MinWeaponScale, max: config.MaxWeaponScale, format: "%.2f"},
	{id: "crosshair", label: "Crosshair", section: SectionHUD, kind: kindOptions,
		stringAt: func(c *config.Config) *string { return &c.Crosshair }, options: crosshairOptions},
	{id: "crosshairSize", label: "Crosshair Size", section: SectionHUD, kind: kindOptions,
		stringAt: func(c *config.Config) *string { return &c.CrosshairSize }, options: crosshairSizeOptions},

	// --- Advanced ---
	{id: "iwad", label: "IWAD Override Path", section: SectionAdvanced, kind: kindTextField,
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
