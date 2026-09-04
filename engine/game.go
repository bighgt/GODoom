package engine

import (
	"fmt"
	"log"
	"math"
	"time"

	"twopointfive/assets"
	"twopointfive/assets/vfs"
	"twopointfive/audio"
	"twopointfive/bsp"
	"twopointfive/config"
	"twopointfive/engine/animdefs"
	"twopointfive/engine/gldefs"
	"twopointfive/engine/mapinfo"
	"twopointfive/raster"
	"twopointfive/render"
	"twopointfive/render/worldgeo"
	"twopointfive/wad"
	"twopointfive/window"
)

// EyeHeight is the classic Doom player eye height above the floor (map
// units) — id's own VIEWHEIGHT. Exported so the initial camera (built in
// cmd/engine before a Game exists) and the per-frame pin in handleMovement
// agree on one value.
const EyeHeight = 41.0

// maxFrameDelta caps the per-frame timestep handed to the simulation. A
// long hitch — asset decode, alt-tab, dragging the window, a debugger
// breakpoint — otherwise yields a multi-hundred-millisecond dt that spikes
// momentum via thrustAccel*dt and moves the camera far enough in a single
// tryMove step to tunnel straight through thin geometry. Beyond this the
// world simply advances in slow motion for that frame.
const maxFrameDelta = 0.1

// maxTicsPerFrame bounds how many 35Hz simulation steps one render frame
// will run to catch up (see stepSimulation). dt is already clamped to
// maxFrameDelta, so this is only reached if ticDuration is unexpectedly
// small; it stops a catch-up loop from ever spiralling.
const maxTicsPerFrame = 8

// turnSpeed (keyboard turning, radians/sec) and mouseSensitivity (radians
// of look per pixel of raw mouse motion — see window.Window.MouseDelta)
// are this project's own tuning; turning in vanilla Doom isn't momentum-
// based, so there's nothing to port for it. Translational movement below
// is momentum-based, ported from PrBoom's p_mobj.c/p_user.c.
const (
	turnSpeed        = 2.5
	mouseSensitivity = 0.0022
)

// Movement momentum, ported from PrBoom's p_mobj.c (P_XYMovement) and
// p_user.c (P_MovePlayer/P_Thrust): id's own engine never sets a player's
// velocity directly. Instead, holding a direction key adds a fixed thrust
// to it every tic, and a multiplicative friction is applied every tic
// regardless — the two settle at whatever speed makes them cancel out,
// which is how Doom's movement gets its slight "spin-up" on starting and
// "slide to a stop" on releasing instead of the instant-velocity feel a
// naive implementation has.
const (
	// moveTopSpeed is the steady-state speed (map units/sec) holding a
	// direction reaches once thrust and friction settle — this project's
	// own choice of "how fast is fast," not a ported value.
	moveTopSpeed = 300.0
	// frictionPerTic is id's ORIG_FRICTION (0xE800 in 16.16 fixed point =
	// 59392/65536), applied multiplicatively to momentum every 1/35s tic.
	frictionPerTic = 59392.0 / 65536.0
	// stopSpeed is id's STOPSPEED (0x1000 fixed point = 4096/65536 map
	// units/tic, converted to units/sec): below this, momentum snaps to
	// zero instead of asymptotically approaching it forever.
	stopSpeed = (4096.0 / 65536.0) * ticsPerSecond
	// thrustAccel (units/sec²) is derived, not ported: the acceleration
	// that makes holding a direction key settle at exactly moveTopSpeed
	// once thrust and per-tic friction reach equilibrium (v = v*friction +
	// thrustPerTic  =>  thrustPerTic = v*(1-friction), converted from
	// per-tic to per-second).
	thrustAccel = moveTopSpeed * (1 - frictionPerTic) * ticsPerSecond
	// runMultiplier scales thrust while the run key (Shift) is held, so the
	// steady-state speed doubles — id's own walk vs. run forwardmove is
	// exactly 2x (0x19 vs 0x32 in p_user.c).
	runMultiplier = 2.0
)

// Vertical movement / falling, ported from PrBoom's p_mobj.c P_ZMovement.
// Vanilla applies these once per 35Hz tic; converted here to continuous
// time (units/sec, units/sec²) so the fall arc stays frame-rate
// independent, the same way friction above is.
const (
	// gravityAccel is id's GRAVITY (FRACUNIT = 1 map unit per tic, per tic)
	// as units/sec²: `momz -= GRAVITY` every tic.
	gravityAccel = 1.0 * ticsPerSecond * ticsPerSecond
	// playerFallGravity is what the player actually falls under — softened
	// below the vanilla figure on purpose. Horizontal reach off a ledge is
	// (air speed) * (air time), and air time grows as 1/sqrt(gravity), so
	// dialing gravity down to this fraction stretches every gap-jump by
	// ~1/sqrt(fallGravityScale) without touching the vanilla constants the
	// "oof" / kick behaviour is defined against.
	fallGravityScale  = 0.7
	playerFallGravity = gravityAccel * fallGravityScale
	// ledgeLaunchBoost multiplies horizontal momentum once, at the instant
	// the feet leave a ledge with real running speed (>= ledgeBoostMinSpeed)
	// — a deliberate "you commit to the jump" kick so a run-up clears a
	// wide gap to another raised platform. Stepping off slowly gets no
	// boost (you just drop), matching the feel of not having taken a run.
	ledgeLaunchBoost   = 1.35
	ledgeBoostMinSpeed = 100.0
	// fallStartKick is id's `if (momz == 0) momz = -GRAVITY*2` — the instant
	// you leave a ledge the drop starts at 2 units/tic, not 0, because
	// vanilla's move-then-apply-gravity order within a tic otherwise wastes
	// the first one.
	fallStartKick = 2.0 * gravityAccel / ticsPerSecond
	// hardLandingSpeed is the descent speed past which a landing grunts
	// ("oof") and dips the view — id's `momz < -GRAVITY*8` (8 units/tic).
	hardLandingSpeed = 8.0 * gravityAccel / ticsPerSecond
	// terminalFallSpeed caps the drop. Vanilla has none, but its per-tic
	// integration is implicitly bounded; this is the continuous-time safety
	// net against a single huge frame stepping through a floor.
	terminalFallSpeed = 2500.0
	// viewDipMax / viewDipRecover shape the landing squat: how deep the
	// camera nudges at hardLandingSpeed (scaling further with speed, capped
	// at ~3x) and how fast (units/sec) it springs back.
	viewDipMax     = -4.0
	viewDipRecover = 40.0
	// jumpImpulse is the upward speed (units/sec) a jump (Space) launches the
	// player at from the ground — ZDoom's default `player.jumpz` is 8 units
	// per 35Hz tic; converted here to units/sec. Against playerFallGravity
	// that clears a bit over a maxStepUp-high (24u) ledge at the apex.
	jumpImpulse = 8.0 * ticsPerSecond
)

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Game is the engine's core object: it owns the window, the GPU renderer
// (held as the render.Renderer interface — see game_design.txt), the CPU
// software rasterizer that actually draws the level (package raster), the
// loaded WAD/level/BSP tree, the camera, every live map object (Mobj), and
// the ordered list of per-frame Systems. Run drives the main loop.
type Game struct {
	Window   *window.Window
	Renderer render.Renderer
	Raster   *raster.Renderer

	WAD   *wad.WAD
	Level *wad.Level
	BSP   bsp.Node

	Camera raster.Camera
	// VelX/VelY is the player's current horizontal momentum (map
	// units/sec) — see the movement-momentum constants above and
	// handleMovement below.
	VelX, VelY float64
	// PlayerZ is the player's feet height (map units); the eye is
	// PlayerZ+EyeHeight. VelZ is vertical momentum (units/sec, negative =
	// falling) and onGround is whether the feet were resting on a floor as
	// of the last resolve. Together they give the vanilla P_ZMovement fall
	// arc: walk off a ledge and you travel through the air — further if you
	// were running, since no friction applies while airborne. viewDip is a
	// short downward camera nudge on a hard landing, springing back to 0.
	PlayerZ  float64
	VelZ     float64
	onGround bool
	viewDip  float64
	Player   PlayerStats
	Weapon   WeaponState

	// playerDead is set by killPlayer once the player's health reaches 0 (or
	// a crush). While dead: no thrust/fire/use (turning still works), the
	// weapon is hidden, the eye sinks toward deadEye over ~1s, and the death
	// face shows. Cleared on level load. damageScale (config damageScale,
	// percent; 100 = normal) scales every ordinary hit the player takes; a
	// crush ignores it and is always lethal.
	playerDead  bool
	deadEye     float64
	damageScale int
	// weaponVolume scales weapon sound loudness (config weaponVolume) —
	// applied in playWeaponSound; 1 = unchanged.
	weaponVolume float64
	// worldVolume scales level-machinery sound loudness (config worldVolume)
	// — doors, lifts, floors/ceilings, stairs, switches, teleporters —
	// applied in playWorldSound; 1 = unchanged.
	worldVolume float64

	// AudioDevice plays one-shot sound effects (door open/close, weapon
	// fire). Nil-safe (playSound no-ops) so a Game still runs with sound
	// unavailable. Set by the caller after NewGame (see cmd/engine/main.go)
	// rather than threading another constructor parameter through it. The
	// level's background music is driven entirely from cmd/engine.
	AudioDevice *audio.Device

	// Voxels, when set, replaces a map object's flat sprite with a voxel
	// model wherever the loaded pack has one for that sprite frame (see
	// drawMobjs, raster.DrawVoxel). nil = sprites only. Set by cmd/engine
	// after picking the Doom1/Doom2 pack for the loaded WAD.
	Voxels            *assets.VoxelSet
	VoxelScale        float64 // config voxelScale — map units per voxel (hardware voxel path)
	loggedVoxelStatus bool    // drawMobjs logs a one-line voxel/sprite tally once

	soundCache map[string]*wad.Sound
	sfxHook    func(name string) // tests only — observes every playSound call

	// ShowHUD toggles the debug text overlay (FPS, position, angle) drawn
	// each frame by Run — on by default (config showDebugInfo).
	// hudStyle picks the in-game HUD (config hudStyle): hudDoom = the full
	// status bar, hudQuake2 = the minimal corner HUD, hudNone = nothing. F1
	// cycles it at runtime (hudKeyDown edge-detects the press).
	ShowHUD     bool
	hudStyle    hudStyle
	hudKeyDown  bool
	fps         float32 // exponential moving average, smoothed in Run
	renderClock float64 // free-running seconds (sum of frame dt) for animated surface effects

	// flashlightEnabled is config Flashlight — false disables the F toggle
	// entirely. flashlightOn is the live state; flashlightKeyDown
	// edge-detects F the same way hudKeyDown does F1. See flashlight.go.
	flashlightEnabled bool
	flashlightOn      bool
	flashlightKeyDown bool

	// SwitchWeaponOnPickup: when true, picking up a weapon the player didn't
	// already own immediately switches to it (id's own behaviour). When
	// false the new weapon is added but the current one stays up. Set from
	// config.json's switchWeaponOnPickup (see cmd/engine); NewGame defaults
	// it to true.
	SwitchWeaponOnPickup bool
	// InvertMouseY flips vertical mouse look (push forward to look down).
	// Set from config.json's invertMouseY; defaults false.
	InvertMouseY bool
	// MouseSensitivity (radians of view rotation per pixel of raw mouse
	// motion) and TurnSpeed (keyboard turn rate, rad/s) — from config.json;
	// NewGame seeds them with this file's mouseSensitivity/turnSpeed
	// constants when the config leaves them zero.
	MouseSensitivity float64
	TurnSpeed        float64
	// AlwaysRun: move at run speed by default, hold Shift to walk.
	AlwaysRun bool
	// Crosshair is the centre-screen aiming marker style — "dot", "cross",
	// or "off" (config.json's crosshair); CrosshairSize scales it — "tiny",
	// "small", "medium", "large" (config.json's crosshairSize). Drawn each
	// frame by renderFrame unless the player is dead.
	Crosshair     string
	CrosshairSize string
	// Skill (1..5) filters which map THINGS spawn; consumed by
	// spawnMapThings during NewGame. Default 3.
	Skill int
	// MaxFPS caps the frame rate in Run when > 0 (a sleep at end of frame);
	// meant for when vsync is off. 0 = uncapped.
	MaxFPS int
	// LitMode routes each frame through the GPU deferred-lighting path
	// (render.Renderer.DrawFrameLit) instead of the plain blit: the raster
	// renderer emits a G-buffer (Raster.SetGBuffer) and Run passes it plus
	// the frame's dynamic lights (collectLights) to the backend. Set from
	// config.json's lightingMode == "enhanced"; both the Renderer and the
	// Raster must have been created for it (see cmd/engine/main.go).
	LitMode bool
	// HardwareMode routes each frame through the GPU-geometry path
	// (render.Renderer.DrawWorld): WorldBuilder walks the BSP into a triangle
	// list the GPU shades, and the CPU rasterizer is used only for the 2D
	// overlay (weapon / HUD / crosshair). Set from config.json's
	// renderer == "hardware"; mutually exclusive with LitMode. The world
	// sprites / monsters aren't GPU geometry yet, so they don't show in this
	// mode (a follow-up stage).
	HardwareMode bool
	// WorldBuilder is the per-level BSP-to-triangle-list builder for
	// HardwareMode (nil otherwise). Rebuilt on level change (see loadMap).
	WorldBuilder *worldgeo.Builder
	// geomHeights snapshots every sector's floor+ceiling height; renderFrame
	// (HardwareMode) rebuilds the cached static geometry only when one of
	// them actually moved — a live door in its open-wait, a strobe light,
	// etc. must not trigger a rebuild.
	geomHeights []int16
	dbgHWFrames int // rate-limits the hardware-path dynamic-light diagnostic
	// lightScale / exposure are config lightScale / exposure, forwarded into
	// every render.Frame (buildFrame) for the enhanced pipeline. lightScale
	// is also handed to the vanilla path via raster.SetLightScale in
	// cmd/engine, so this copy is only read on the enhanced path.
	lightScale float32
	exposure   float32
	// Distance fog (config fogColor / fogDensity), 0..1 colour + density.
	// Fed to every render path: buildFrame -> render.Frame (enhanced),
	// worldgeo.Camera (hardware), raster.SetFog (vanilla, via cmd/engine).
	fogR, fogG, fogB float32
	fogDensity       float32
	// shadows (config shadows) gates the enhanced pipeline's screen-space
	// light shadows (buildFrame sets the shader's shadow-strength tuning
	// slot). groundShadows (config groundShadows, default off) gates the
	// per-thing contact-blob pass (drawGroundShadows).
	shadows       bool
	groundShadows bool

	closing       bool          // set once when Escape has asked the window to close, so the log line prints once
	spaceWasDown  bool          // for edge-detecting the "use" key (E)
	jumpWasDown   bool          // for edge-detecting the jump key (Space)
	weaponKeyDown [8]bool       // for edge-detecting weapon slot keys 1-7
	projectiles   []*Projectile // in-flight shots; see projectiles.go

	// Classic Doom cheat codes, baked in for testing (see cheats.go). godMode
	// (iddqd) and noclip (idclip) are the two persistent toggles; cheatBuf is
	// the rolling typed-key history the matcher scans; pendingWarp is a map
	// name queued by idclev and loaded by stepSimulation at a safe point,
	// like exitLevel.
	godMode     bool
	noclip      bool
	cheatBuf    string
	pendingWarp string
	// OnMusicChange, if set, is called with a map name when the idmus cheat
	// asks for a different track — the music lives in cmd/engine (see
	// main.go), same as OnLevelChange.
	OnMusicChange func(mapName string)

	// Active sector effects (doors, floors, lifts, crushers, light
	// animations, scrollers) — see specials.go. sectorActive tracks which
	// sectors have a height mover so a second one can't start.
	thinkers     []sectorThinker
	sectorActive map[int]bool

	// staticLights are the map's fixed light emitters for the enhanced
	// pipeline — one per light-animation sector (its output tracks the live
	// sector LightLevel, so strobes/flicker actually pulse) and one per
	// emissive decoration (torches, burning barrels, tech lamps, candles).
	// Built once per level by buildStaticLights; collectLights samples and
	// camera-range-culls them each frame (maplights.go). lightScratch /
	// lightCands are its reused per-frame buffers (no alloc per frame).
	staticLights []staticLight
	lightScratch []render.Light
	lightCands   []scoredLight
	frame        render.Frame // reused per-frame render.Frame for buildFrame

	// mods is the mounted assets/mods/ virtual filesystem (.pk3 archives +
	// loose files). Always non-nil after NewGame, usually empty. GLDEFS is
	// read from it today (gldefs_load.go); brightmap / texture overrides
	// will use the same mount stack.
	mods *vfs.FS
	// gldefs is the parsed GLDEFS lump (dynamic-light definitions + sprite
	// -frame attachments) from the loaded WAD and/or a mod, or nil when
	// neither provides one. Loaded once in NewGame; buildStaticLights
	// prefers it over the built-in decoLights table (gldefs_load.go).
	gldefs *gldefs.Defs

	// mapInfo is the parsed UMAPINFO lump (per-map name / music / sky / next
	// / partime overrides) from the WAD and/or a mod, or nil. Loaded once in
	// NewGame; consulted by nextMapName, the HUD level name, loadMap's sky
	// setup and cmd/engine's music pick (mapinfo_load.go). parTime is the
	// current level's UMAPINFO par time in seconds (0 = none).
	mapInfo map[string]*mapinfo.Entry
	parTime int

	// DEHACKED / BEX (dehacked_load.go, dehacked_misc.go): dehLevelNames
	// overrides a map's display name ([STRINGS] HUSTR_*), dehPars its par
	// time ([PARS]). Thing / Frame / Weapon / Ammo edits go straight to the
	// mobjInfo / states / Weapons / clipAmmo tables at load. misc holds the
	// [MISC] tunables (lazily defaulted via dm()); cheatOverride remaps or
	// disables built-in cheat codes from a [CHEAT] section.
	dehLevelNames map[string]string
	dehPars       map[string]int
	misc          *dehMisc
	cheatOverride map[string]string

	// Animated flats and wall textures (liquids, fire, gore, waterfalls —
	// id's P_InitPicAnims plus a BOOM ANIMATED lump). flatAnimByName /
	// wallAnimByName map any frame's name to its group + index; animBase*
	// snapshot each sector's authored flat and wallAnimSlots each animated
	// sidedef slot, so the current frame is recomputed statelessly from
	// levelTime. Set by initTexAnims (from spawnSpecials), advanced by
	// animateFlats + animateWalls (from runTic).
	flatAnimByName              map[string]texAnimRef
	wallAnimByName              map[string]texAnimRef
	animBaseFloor, animBaseCeil []string
	wallAnimSlots               []wallAnimSlot
	// animDefsCache is the parsed+merged ANIMDEFS lump(s) (nil once probed
	// and absent — animDefsProbed tells the two apart). lastAnimHash lets the
	// hardware path rebuild static geometry only on the tics an animated
	// texture actually steps a frame (worldGeomChanged only sees heights).
	animDefsCache   *animdefs.AnimDefs
	animDefsProbed  bool
	lastAnimHash    uint64
	lastAnimHashSet bool
	// exitLevel: 0 none, 1 normal exit, 2 secret exit — checked by Run.
	exitLevel    int
	secretCount  int
	totalSecrets int
	// OnLevelChange, if set, is called with the new map name after a level
	// transition so the caller can restart the music (cmd/engine).
	OnLevelChange func(mapName string)

	// mobjs is every live map object — monsters, items, decorations,
	// corpses (and, from Phase 2, missiles and the player). Stepped at the
	// fixed 35Hz tic (see runTic); rendered every frame. ticAccum carries
	// leftover real time between frames; levelTime counts elapsed tics.
	mobjs        []*Mobj
	mobjScratch  []*Mobj                  // reused per-frame draw-order buffer
	thingScratch []raster.SceneThing      // reused per-frame resolved draw list
	hwSprites    []worldgeo.Sprite        // reused per-frame billboard list (HardwareMode)
	hwVoxels     []worldgeo.VoxelInstance // reused per-frame voxel-instance list (HardwareMode)
	hwShadows    [][4]float32             // reused per-frame ground-shadow caster list (HardwareMode)
	playerMobj   *Mobj                    // the player's own map object — AI target; kept in sync with Camera
	ticAccum     float64
	levelTime    int
	killCount    int // monsters killed this level

	// Hardware-path sector-motion interpolation: prevSectorH is every
	// sector's floor/ceiling height (interleaved) snapshotted at the top of
	// runTic — the "start of the last tic" state renderFrame lerps forward
	// from by renderLerp(), so a door/lift glides instead of stepping at
	// 35Hz. secInterpSaved holds the authoritative heights displaced during
	// one frame's geometry build, restored immediately after. sectorInterp
	// Active reports that some sector was mid-travel this frame (forces a
	// static-geometry rebuild). Presentation only — the sim reads the real
	// Level.Sectors heights.
	prevSectorH        []int16
	secInterpSaved     []savedSecH
	sectorInterpActive bool

	// pickupPrev* is the player's position at the previous checkItemPickups
	// call. The check only runs at the 35Hz tic rate while the camera moves
	// every render frame, so at speed the player can cross an item between
	// two samples; checkItemPickups sweeps this segment to close that gap
	// (see pickup.go). pickupPrevSet is false until the first tic seeds it.
	pickupPrevX, pickupPrevY float64
	pickupPrevSet            bool

	// blockGrid spatially indexes Level.Linedefs for anyLineWithin (see
	// collisiongrid.go) — built once here rather than scanning every
	// Linedef on every collision/projectile query, which is what a
	// hundreds-of-lines level made into real, measurable per-frame cost
	// (every tryMove call probes up to three candidate positions, and
	// every in-flight projectile probes one more, every single frame).
	blockGrid *collisionGrid

	// sectorLines[s] / sectorNeighbors[s] are the linedef and adjacent-
	// sector adjacency tables the p_spec.c surrounding-sector queries use
	// instead of rescanning every Linedef per call — see sectorindex.go.
	// Built by spawnSpecials at level load, immutable thereafter.
	sectorLines     [][]int32
	sectorNeighbors [][]int32

	// Sound-alert flood, one entry per level sector (id's
	// sector_t.soundtarget / soundtraversed). pNoiseAlert (noise.go) floods
	// these from the player's sector on every weapon fire; aLook reads
	// soundTarget so a monster wakes to a shot it never saw. Sized/zeroed by
	// buildSectorIndex, nil before that. soundValid stamps sectors reached
	// by the current flood (guards re-visits within one flood only —
	// soundTarget itself persists across alerts, like id's).
	soundTarget     []*Mobj
	soundTraversed  []int
	soundValid      []uint32
	soundValidCount uint32
	soundQueue      []int // reused pNoiseAlert work-queue scratch

	// systems is the ordered per-frame simulation (see systems.go), driven
	// polymorphically by Run so a new subsystem is an addition to NewGame
	// rather than an edit to the loop.
	systems []System
}

// NewGame wires together an already-created window, GPU renderer, CPU
// rasterizer, loaded level, and starting camera into a running Game. cfg
// carries the user's config.json settings; it must be a repaired() value
// (config.Load does that) — NewGame trusts the ranges.
func NewGame(win *window.Window, renderer render.Renderer, ras *raster.Renderer, w *wad.WAD, level *wad.Level, tree bsp.Node, cam raster.Camera, cfg config.Config) *Game {
	sens, turn := cfg.MouseSensitivity, cfg.TurnSpeed
	if sens <= 0 {
		sens = mouseSensitivity
	}
	if turn <= 0 {
		turn = turnSpeed
	}
	skill := cfg.Skill
	if skill < 1 || skill > 5 {
		skill = 3
	}
	g := &Game{
		Window:    win,
		Renderer:  renderer,
		Raster:    ras,
		WAD:       w,
		blockGrid: buildCollisionGrid(level),
		Level:     level,
		BSP:       tree,
		Camera:    cam,
		Player:    DefaultPlayerStats(),
		Weapon:    NewWeaponState(),
		ShowHUD:   cfg.ShowDebugInfo,

		hudStyle: hudStyleFromConfig(cfg.HUDStyle),

		SwitchWeaponOnPickup: cfg.SwitchWeaponOnPickup,
		InvertMouseY:         cfg.InvertMouseY,
		MouseSensitivity:     sens,
		TurnSpeed:            turn,
		AlwaysRun:            cfg.AlwaysRun,
		Crosshair:            cfg.Crosshair,
		CrosshairSize:        cfg.CrosshairSize,
		Skill:                skill,
		MaxFPS:               cfg.MaxFPS,
		LitMode:              cfg.LightingMode == config.LightingEnhanced,
		lightScale:           float32(cfg.LightScale),
		exposure:             float32(cfg.Exposure),
		shadows:              cfg.Shadows,
		groundShadows:        cfg.GroundShadows,
		damageScale:          cfg.DamageScale,
		weaponVolume:         cfg.WeaponVolume,
		worldVolume:          cfg.WorldVolume,
		VoxelScale:           cfg.VoxelScale,
		flashlightEnabled:    cfg.Flashlight,
	}
	if fr, fg, fb, fdens, on := cfg.Fog(); on {
		g.fogR, g.fogG, g.fogB, g.fogDensity = fr, fg, fb, float32(fdens)
		if ras != nil {
			ras.SetFog(fr, fg, fb, fdens) // vanilla-path post-pass
		}
	}
	// The per-frame simulation runs as an ordered list of Systems (see
	// systems.go) rather than a hardcoded call sequence in Run — order is
	// load-bearing: movement settles the camera, doors animate the sectors
	// movement and rendering both read, combat runs against the result.
	g.AddSystem(movementSystem{})
	g.AddSystem(useSystem{})
	g.AddSystem(combatSystem{})
	g.AddSystem(cheatSystem{})
	g.AddSystem(flashlightSystem{})

	g.mods = loadMods()                // assets/mods/*.pk3 + loose files
	g.loadTextureDefs(w, g.mods)       // ZDoom TEXTURES composite defs (WAD and/or mod)
	g.gldefs = loadGLDefs(w, g.mods)   // data-driven dynamic lights (WAD and/or mod)
	g.loadDehacked(w, g.mods, "")      // DEH/BEX table edits (WAD and/or mod lumps)
	g.mapInfo = loadMapInfo(w, g.mods) // UMAPINFO per-map metadata (WAD and/or mod)
	g.applyMapInfo()                   // sky / par-time for the starting level
	if dn := g.levelDisplayName(); dn != "" && dn != g.Level.Name {
		log.Printf("engine: %s — %q", g.Level.Name, dn)
	}
	g.spawnSpecials()
	g.spawnMapThings()
	g.buildStaticLights()
	// The player's own map object: monsters target it, and it's a solid
	// body they bump into. Not in g.mobjs (never ticked or drawn); kept in
	// sync with the Camera at the top of every tic (see runTic).
	g.playerMobj = &Mobj{
		Type: MT_PLAYER, Info: &mobjInfo[MT_PLAYER],
		Health: g.Player.Health, Flags: mobjInfo[MT_PLAYER].Flags,
		Radius: mobjInfo[MT_PLAYER].Radius, Height: mobjInfo[MT_PLAYER].Height,
		X: cam.X, Y: cam.Y, Z: cam.Z - EyeHeight, Angle: cam.Angle,
	}
	g.groundPlayer(cam.Z - EyeHeight)
	return g
}

// AddSystem registers a system. Systems run every frame in the order they
// were added.
func (g *Game) AddSystem(s System) {
	g.systems = append(g.systems, s)
}

// Run drives the main loop until the window is closed. Each frame: poll
// input, run every System, step the fixed-rate simulation, rasterize on the
// CPU, and present via the GPU renderer. Returns cleanly on window close.
func (g *Game) Run() error {
	last := time.Now()
	for !g.Window.ShouldClose() {
		now := time.Now()
		dt := float32(now.Sub(last).Seconds())
		last = now
		if dt > maxFrameDelta {
			dt = maxFrameDelta
		}

		g.pollInput()
		for _, s := range g.systems {
			s.Update(g, dt)
		}
		g.stepSimulation(dt)
		g.updateFPS(dt)
		g.renderClock += float64(dt) // animated-surface clock (water ripple)
		if err := g.renderFrame(); err != nil {
			return err
		}
		g.capFrameRate(now)
	}

	log.Println("engine: window closed, shutting down")
	return nil
}

// pollInput pumps the OS event queue and handles the one window-level key
// the loop itself owns: Escape asks the window to close (logged once; the
// process then pauses so the debug console stays readable — see
// cmd/engine).
func (g *Game) pollInput() {
	g.Window.PollEvents()
	if g.Window.EscapePressed() && !g.closing {
		log.Println("engine: escape pressed — closing game window")
		g.closing = true
		g.Window.RequestClose()
	}
}

// stepSimulation advances the fixed 35Hz world by however many whole tics dt
// has accumulated (capped at maxTicsPerFrame so a long hitch can't spiral
// into a catch-up storm), then services a pending level exit.
func (g *Game) stepSimulation(dt float32) {
	g.ticAccum += float64(dt)
	for steps := 0; g.ticAccum >= ticDuration && steps < maxTicsPerFrame; steps++ {
		g.ticAccum -= ticDuration
		g.runTic()
	}
	if g.pendingWarp != "" {
		g.loadMap(g.pendingWarp)
		g.pendingWarp = ""
		g.ticAccum = 0
	} else if g.exitLevel != 0 {
		g.loadNextMap()
		g.ticAccum = 0
	}
}

// updateFPS folds this frame's rate into an exponential moving average — no
// rolling sample window needed — seeding it on the first frame so the HUD
// doesn't spend a second ramping up from zero.
func (g *Game) updateFPS(dt float32) {
	if dt <= 0 {
		return
	}
	const smoothing = 0.9
	if g.fps == 0 {
		g.fps = 1 / dt
		return
	}
	g.fps = g.fps*smoothing + (1/dt)*(1-smoothing)
}

// renderFrame rasterizes the world on the CPU, overlays the in-flight
// projectiles, map objects, viewmodel and HUD, then hands the result to the
// GPU backend: the enhanced path uploads a G-buffer plus a separate crisp
// overlay, the vanilla path a single pre-composited image.
func (g *Game) renderFrame() error {
	g.Raster.SetAnimTime(g.renderClock) // water ripple (software path; harmless otherwise)
	if g.HardwareMode {
		// GPU-geometry path: no CPU world render — just prep the 2D overlay
		// plane's per-frame lifecycle. The world + billboards go to the GPU
		// below.
		g.Raster.BeginOverlayFrame()
	} else {
		g.Raster.Render(g.Level, g.BSP, g.Camera)
		// Soft contact shadow on the floor under every grounded thing + the
		// player, before anything is drawn on top of it.
		g.drawGroundShadows()
		// Only the true-projectile weapons (Rocket Launcher, Plasma Rifle,
		// BFG9000) ever populate g.projectiles; the hitscan weapons never do,
		// so this loop is simply a no-op for them.
		for _, p := range g.projectiles {
			if name, ok := p.CurrentSprite(); ok {
				g.Raster.DrawWorldSprite(g.Camera, p.X, p.Y, p.Z, p.SizeMultiplier(), name)
			}
		}
		g.drawMobjs()
		// Distance fog over the finished world + sprites (vanilla path; the
		// enhanced pipeline fogs in light.frag, hardware in world.frag).
		if !g.LitMode {
			g.Raster.ApplyFog()
		}
	}
	if !g.playerDead {
		def := Weapons[g.Weapon.Current]
		gunFrame, flashFrame, hasFlash := g.currentSprite()
		// The held weapon dims with the sector the player is standing in,
		// the way vanilla shades a psprite (the muzzle flash stays full
		// bright).
		g.Raster.DrawWeapon(def.SpritePrefix, gunFrame, def.FlashPrefix, flashFrame, hasFlash, g.Weapon.Offset,
			g.spriteSectorLight(g.Camera.X, g.Camera.Y))
		if g.Crosshair != "" && g.Crosshair != "off" {
			g.Raster.DrawCrosshair(g.Crosshair, g.CrosshairSize)
		}
	}
	g.drawHUD()
	if g.ShowHUD {
		g.Raster.DrawHUD([]string{
			fmt.Sprintf("%s  FPS %.0f", g.levelDisplayName(), g.fps),
			fmt.Sprintf("XYZ %.0f %.0f %.0f", g.Camera.X, g.Camera.Y, g.Camera.Z),
			fmt.Sprintf("ANGLE %.0f PITCH %.0f", g.Camera.Angle*180/math.Pi, g.Camera.Pitch*180/math.Pi),
		})
	}
	if g.HardwareMode {
		// GPU-geometry path: walk the BSP into a triangle list, add a
		// camera-facing billboard per live thing / projectile, and let the GPU
		// rasterise + shade it. The 2D overlay draws above (weapon / HUD /
		// crosshair) landed in the crisp overlay plane, which the GPU blends
		// over the world.
		fov := g.Camera.FOVDeg
		if fov <= 0 {
			fov = 90
		}
		geomChanged := g.worldGeomChanged() // reads authoritative (pre-interp) heights
		animChanged := g.animFrameChanged() // an animated flat/texture stepped a frame
		restoreSecH := g.applySectorInterp()
		if geomChanged || g.sectorInterpActive || animChanged {
			// A mover changed a sector height between tics, one is mid-glide
			// this frame, or an animated flat/wall texture advanced — rebuild
			// the static geometry (its texture bindings are baked in, and the
			// height version needs to track interpolation, not 35Hz).
			g.WorldBuilder.Invalidate()
		}
		geom := g.WorldBuilder.Build(float32(g.Camera.X), float32(g.Camera.Y))
		restoreSecH() // authoritative heights back before anything else reads them
		sprites, voxels := g.collectWorldObjects()
		g.WorldBuilder.AddSprites(g.Camera.Angle, sprites)
		g.WorldBuilder.AddVoxels(voxels)
		wc := worldgeo.Camera{
			X: g.Camera.X, Y: g.Camera.Y, Z: g.Camera.Z,
			Angle: g.Camera.Angle, Pitch: g.Camera.Pitch, FOVDeg: fov,
			Time: g.renderClock, // world.frag water ripple
			FogR: g.fogR, FogG: g.fogG, FogB: g.fogB, FogDensity: g.fogDensity,
		}
		// Dynamic point lights (muzzle flash, projectile glow, map lights) —
		// world.frag adds a smooth 1-(d/R)^2 falloff on top of the vanilla
		// sector/distance fade.
		lights, dynamicN := g.collectLights()
		// The present pass traces screen-space shadow rays for the leading
		// lights: every dynamic emitter (muzzle flash, in-flight shots,
		// monster attacks), then up to two of the nearest map emitters
		// (torches), capped at the shader's 8-light loop. Same order as the
		// light array the world UBO packs, so the blit just reads
		// lightPos[0..N).
		shadowN := dynamicN
		if extra := len(lights) - dynamicN; extra > 0 {
			shadowN += min(extra, 2)
		}
		if shadowN > 8 {
			shadowN = 8
		}
		wc.ShadowCasterN = shadowN
		// Full-frame tint when the player is standing in a damaging liquid.
		wc.TintR, wc.TintG, wc.TintB, wc.TintA = g.hwLiquidTint()
		// Soft contact-shadow blobs on the floor under grounded things.
		wc.GroundShadows = g.collectGroundShadows()
		// Head-mounted flashlight (F) — a forward cone, separate from the
		// point lights above.
		if spot, ok := g.flashlightSpot(); ok {
			wc.SpotX, wc.SpotY, wc.SpotZ = spot.X, spot.Y, spot.Z
			wc.SpotDX, wc.SpotDY, wc.SpotDZ = spot.DX, spot.DY, spot.DZ
			wc.SpotR, wc.SpotG, wc.SpotB = spot.R, spot.G, spot.B
			wc.SpotIntensity, wc.SpotRadius = spot.Intensity, spot.Radius
			wc.SpotCosOuter, wc.SpotCosInner = spot.CosOuter, spot.CosInner
		}
		if g.dbgHWFrames < 5400 {
			g.dbgHWFrames++
			if g.dbgHWFrames <= 3 || g.dbgHWFrames%1800 == 0 {
				log.Printf("engine: [hw] frame %d — collectLights -> %d light(s) to DrawWorld", g.dbgHWFrames, len(lights))
			}
		}
		if err := g.Renderer.DrawWorld(geom, wc, g.Raster.Width, g.Raster.Height, g.Raster.Overlay(), lights); err != nil {
			return fmt.Errorf("engine: draw world: %w", err)
		}
		return nil
	}
	if g.LitMode {
		// Enhanced: the overlay is uploaded separately and composited on the
		// GPU after tonemapping, so it stays crisp and unlit.
		if err := g.Renderer.DrawFrameLit(g.buildFrame()); err != nil {
			return fmt.Errorf("engine: draw frame: %w", err)
		}
		return nil
	}
	// Vanilla: scale the 320x200 HUD/weapon overlay onto the full-res frame,
	// then blit the finished image.
	g.Raster.CompositeOverlay()
	if err := g.Renderer.DrawFrame(g.Raster.Pix); err != nil {
		return fmt.Errorf("engine: draw frame: %w", err)
	}
	return nil
}

// capFrameRate sleeps out the rest of the frame budget when config.json's
// maxFPS is set — meant for vsync off; harmless with it on, since the
// present already blocks longer than this would sleep.
func (g *Game) capFrameRate(frameStart time.Time) {
	if g.MaxFPS <= 0 {
		return
	}
	budget := time.Second / time.Duration(g.MaxFPS)
	if elapsed := time.Since(frameStart); elapsed < budget {
		time.Sleep(budget - elapsed)
	}
}

// mouseLook applies one mouse delta (window pixels: +mdx right, +mdy down —
// see window.Window.MouseDelta) to a yaw/pitch pair and returns the new
// values, pitch clamped to +-raster.MaxPitch. sens is radians of rotation
// per pixel (Game.MouseSensitivity).
//
// Mouse-right turns the view right, a *decreasing* Camera.Angle (Angle
// increases counter-clockwise, matching the Left/Right arrow keys). By
// default mouse-up (GLFW y decreases upward) looks up, an *increasing*
// Camera.Pitch; invertY flips that so pushing the mouse forward looks down,
// the "flight-stick" convention some players prefer.
func mouseLook(angle, pitch, mdx, mdy, sens float64, invertY bool) (float64, float64) {
	angle -= mdx * sens
	pitchDelta := mdy * sens
	if invertY {
		pitchDelta = -pitchDelta
	}
	pitch = clamp(pitch-pitchDelta, -raster.MaxPitch, raster.MaxPitch)
	return angle, pitch
}

// applyMouseLook feeds one mouse delta through mouseLook into the camera,
// using this Game's MouseSensitivity and InvertMouseY. Split out of
// handleMovement so the config-to-camera path is directly testable without
// a live window.
func (g *Game) applyMouseLook(mdx, mdy float64) {
	g.Camera.Angle, g.Camera.Pitch = mouseLook(
		g.Camera.Angle, g.Camera.Pitch, mdx, mdy, g.MouseSensitivity, g.InvertMouseY)
}

// handleMovement is Phase 2's placeholder input scheme: WASD relative to
// facing, Left/Right arrows to turn, and the camera's eye height kept
// pinned to floorHeight+eyeHeight for whatever Sector it's currently in
// (via a BSP point-location query) — enough to walk around and confirm the
// level renders correctly, without yet being real physics or collision.
func (g *Game) handleMovement(dt float32) {
	w := g.Window
	if w.TurnLeftPressed() {
		g.Camera.Angle += g.TurnSpeed * float64(dt)
	}
	if w.TurnRightPressed() {
		g.Camera.Angle -= g.TurnSpeed * float64(dt)
	}

	if mdx, mdy := w.MouseDelta(); mdx != 0 || mdy != 0 {
		g.applyMouseLook(mdx, mdy)
	}
	// Keep Angle bounded to (-2π, 2π) rather than letting it accumulate
	// without limit over a long session — sin/cos handle a large argument
	// correctly, but there's no reason to let it grow forever either.
	// (math.Mod keeps the sign of the dividend, hence the open interval on
	// both ends.)
	g.Camera.Angle = math.Mod(g.Camera.Angle, 2*math.Pi)

	// Dead: you can still look around, but no movement — just keep the eye
	// (which resolvePlayerZ is sinking toward the floor) tracking the sector.
	if g.playerDead {
		g.VelX, g.VelY = 0, 0
		g.resolvePlayerZ(float64(dt))
		return
	}

	// F1: cycle the HUD style (doom -> quake2 -> none). Edge-triggered.
	hudKey := w.HUDCyclePressed()
	if hudKey && !g.hudKeyDown {
		g.cycleHUD()
	}
	g.hudKeyDown = hudKey

	// Jump (Space): a one-shot upward impulse, only from the ground.
	// Edge-triggered so holding the key doesn't re-launch the instant the
	// feet touch down again.
	jumpDown := w.JumpPressed()
	if jumpDown && !g.jumpWasDown && g.onGround {
		g.VelZ = jumpImpulse
		g.onGround = false
	}
	g.jumpWasDown = jumpDown

	// "Wedged": the player's current position fails the movement clip — a
	// fall into a pocket too tight for the normal slide, a sector risen into
	// them, an awkward teleport. While wedged, run thrust and friction even
	// with the feet off the ground so the player can push clear (stepMove's
	// own recovery then lets each sub-step crawl toward looser space);
	// otherwise a bad landing is unrecoverable.
	wedged := !g.canMoveTo(g.Camera.X, g.Camera.Y, g.PlayerZ)

	fx, fy := math.Cos(g.Camera.Angle), math.Sin(g.Camera.Angle)
	rx, ry := math.Sin(g.Camera.Angle), -math.Cos(g.Camera.Angle)

	// Thrust: add to momentum rather than set it, exactly like id's own
	// P_Thrust — see the movement-momentum constants above.
	dx, dy := 0.0, 0.0
	if w.ForwardPressed() {
		dx += fx
		dy += fy
	}
	if w.BackPressed() {
		dx -= fx
		dy -= fy
	}
	if w.StrafeRightPressed() {
		dx += rx
		dy += ry
	}
	if w.StrafeLeftPressed() {
		dx -= rx
		dy -= ry
	}
	// Thrust and friction only apply with your feet on the ground — id's
	// P_MovePlayer gates P_Thrust on `onground`, and P_XYMovement skips
	// friction while `mo->z > mo->floorz`. That's what carries you forward
	// through a fall: run off a ledge and you keep the full run speed the
	// whole way down, so you land much further out than a walk would.
	if (dx != 0 || dy != 0) && (g.onGround || wedged) {
		accel := thrustAccel
		// AlwaysRun inverts the Shift modifier: run unless Shift is held.
		if w.RunPressed() != g.AlwaysRun {
			accel *= runMultiplier
		}
		n := math.Hypot(dx, dy)
		g.VelX += dx / n * accel * float64(dt)
		g.VelY += dy / n * accel * float64(dt)
	}

	if g.onGround || wedged {
		// Friction: id's per-tic multiplicative decay (frictionPerTic every
		// 1/35s) converted to continuous time so it's frame-rate independent.
		decay := math.Pow(frictionPerTic, float64(dt)*ticsPerSecond)
		g.VelX *= decay
		g.VelY *= decay
		if math.Hypot(g.VelX, g.VelY) < stopSpeed {
			g.VelX, g.VelY = 0, 0
		}
	}

	g.tryMove(g.VelX*float64(dt), g.VelY*float64(dt))
	g.resolvePlayerZ(float64(dt))
}

// resolvePlayerZ runs the vanilla P_ZMovement floor/gravity step for the
// player against whatever sector its (x, y) is now over, then places the
// eye. See fallStep for the pure arithmetic; this wrapper adds the
// sector lookup, the hard-landing "oof" + view dip, and the eye-under-
// ceiling clamp handleMovement used to do unconditionally.
func (g *Game) resolvePlayerZ(dt float64) {
	sec := bsp.PointSector(g.BSP, g.Level, float32(g.Camera.X), float32(g.Camera.Y))
	if sec == nil {
		g.Camera.Z = g.PlayerZ + EyeHeight // outside geometry — just hold
		return
	}
	floor, ceil := float64(sec.FloorHeight), float64(sec.CeilingHeight)

	impact := g.VelZ // descent speed going into this step, for the dip depth
	wasGround := g.onGround
	nz, nvz, ground, hardLand := fallStep(g.PlayerZ, g.VelZ, g.onGround, floor, ceil, dt)
	g.PlayerZ, g.VelZ, g.onGround = nz, nvz, ground

	// Just ran off a ledge (leaving the ground already heading DOWN — not a
	// deliberate upward jump, which keeps its own arc): commit it with a
	// one-time horizontal kick so a run-up reaches the next platform.
	if wasGround && !ground && nvz <= 0 {
		g.VelX, g.VelY = ledgeBoosted(g.VelX, g.VelY)
	}

	if hardLand {
		g.playSound("DSOOF")
		// Deeper dip the faster the drop, capped around 3x.
		g.viewDip = clamp(-impact/hardLandingSpeed, 1, 3) * viewDipMax
	}
	if g.viewDip < 0 {
		if g.viewDip += viewDipRecover * dt; g.viewDip > 0 {
			g.viewDip = 0
		}
	}

	// Eye = feet + eye height + the recovering landing dip, never poking up
	// through a low ceiling (degenerate geometry, or a crusher).
	eye := g.PlayerZ + EyeHeight + g.viewDip
	if lim := ceil - 4; eye > lim {
		eye = lim
	}
	if g.playerDead {
		// Sink to id's PLAYERDEATHVIEWHEIGHT-ish over about a second.
		const deadViewHeight = 8.0
		g.deadEye += (g.PlayerZ + deadViewHeight - g.deadEye) * clamp(dt*5, 0, 1)
		eye = g.deadEye
		if lim := ceil - 4; eye > lim {
			eye = lim
		}
	}
	g.Camera.Z = eye
}

// ledgeBoosted scales a horizontal velocity by ledgeLaunchBoost, but only
// when it already carries running speed — a slow step-off is left alone.
func ledgeBoosted(vx, vy float64) (float64, float64) {
	if math.Hypot(vx, vy) < ledgeBoostMinSpeed {
		return vx, vy
	}
	return vx * ledgeLaunchBoost, vy * ledgeLaunchBoost
}

// fallStep is one frame of the player's vertical physics: id's
// P_ZMovement, converted to continuous time. Move by momentum first, then
// either land (feet at/under the floor: snap, zero VelZ, report a hard
// landing) or keep falling (apply gravity — the first airborne frame gets
// id's -GRAVITY*2 kick instead, since a grounded VelZ is 0). A head-bump on
// a low ceiling clamps the feet and kills any upward VelZ.
func fallStep(feetZ, velZ float64, onGround bool, floor, ceil, dt float64) (nz, nvz float64, ground, hardLand bool) {
	nz = feetZ + velZ*dt
	nvz = velZ

	if nz <= floor {
		if velZ < 0 && -velZ >= hardLandingSpeed {
			hardLand = true
		}
		nz, nvz, ground = floor, 0, true
	} else {
		if onGround {
			nvz = -fallStartKick
		} else {
			nvz = velZ - playerFallGravity*dt
			if nvz < -terminalFallSpeed {
				nvz = -terminalFallSpeed
			}
		}
	}

	if head := ceil - playerHeight; nz > head {
		if nz = head; nz < floor {
			nz = floor
		}
		if nvz > 0 {
			nvz = 0
		}
	}
	return
}

// groundPlayer seats the player firmly on floorZ with no vertical
// momentum — used at spawn, on a level change, and after a teleport.
func (g *Game) groundPlayer(floorZ float64) {
	g.PlayerZ = floorZ
	g.VelZ = 0
	g.onGround = true
	g.viewDip = 0
}
