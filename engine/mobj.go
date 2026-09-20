package engine

import (
	"math"
	"slices"

	"twopointfive/bsp"
	"twopointfive/render/worldgeo"
	"twopointfive/wad"
)

// Mobj is a map object — id's mobj_t. Every monster, projectile, pickup,
// decoration, and corpse in the world is one. It animates through the
// states table (see states.go) at the fixed 35Hz tic and, from Phase 2,
// moves via P_XYMovement / P_ZMovement.
type Mobj struct {
	X, Y, Z          float64 // map units; Z is the bottom of the object
	MomX, MomY, MomZ float64 // per-tic momentum
	Angle            float64 // radians, id's convention (0 = +X, CCW)

	// prevX..prevAngle are X/Y/Z/Angle as of the start of the current tic,
	// captured at the top of mobjTick (and seeded at spawn). The renderer
	// draws between these and the live fields — see interp.go. Presentation
	// only; nothing in the simulation reads them.
	prevX, prevY, prevZ, prevAngle float64

	Type     mobjType
	Info     *MobjInfo
	Health   int
	Flags    int
	Radius   float64
	Height   float64
	FloorZ   float64 // floor height under the object right now
	CeilingZ float64 // ceiling height above it

	State      stateNum
	stateDef   *State
	Tics       int
	Sprite     spriteNum
	Frame      int
	Fullbright bool

	// spriteOverride, when non-empty, is the 4-letter sprite name the
	// renderer draws instead of spriteNames[Sprite] — used for Heretic map
	// objects (engine/heretic.go), whose sprites aren't in Doom's table.
	// overrideFrames>1 cycles Frame 0..overrideFrames-1 every overrideTics
	// tics (torches, chandeliers, braziers). See mobjSpriteName / mobjTick.
	spriteOverride string
	overrideFrames int
	overrideTics   int
	// hereticGift is what a MF_SPECIAL Heretic pickup grants on touch
	// (pTouchSpecialThing); hgNone for a plain decoration.
	hereticGift hereticGift

	// Heretic monster FSM (engine/heretic_actors.go). hact non-nil marks a
	// Heretic actor: hereticActorTick drives movement + the phase animation
	// (hPhase / hFrameIdx / hFrameTic). hDamage is a Heretic projectile's
	// per-shot base damage (mo.Info is the shared MT_HERETIC template).
	hact      *hereticActorDef
	hPhase    hPhase
	hFrameIdx int
	hFrameTic int
	hDamage   int
	hMissile  *hMissileDef // set on a Heretic monster projectile — its impact anim
	hExpireIn int          // >0: a dying MT_HERETIC effect mobj, removed when it hits 0

	// AI bookkeeping.
	Target       *Mobj
	MoveDir      int
	MoveCount    int
	ReactionTime int
	Threshold    int

	// TeleportFreeze counts down from teleportFreezeTics after this mobj
	// lands on a teleporter (evTeleport), ticking to 0 in mobjTick. While
	// it's positive, evTeleport refuses to teleport it again — some pads
	// sit hard against another teleport line (or their own, if it's boxed
	// in on every side), and without this a mobj could get bounced right
	// back where it came from the instant it lands, with no window to walk
	// clear; for the player that reads as "stuck".
	TeleportFreeze int

	// TeleportGuard/TeleportLandX/Y are TeleportFreeze's companion: a timer
	// alone still lets a mobj get bounced straight back if it hasn't
	// physically moved off the landing spot by the time the timer expires
	// (stand still on a pad wedged against another teleport line and the
	// instant the freeze lifts, the very same trigger fires again). While
	// TeleportGuard is set, evTeleport refuses to fire until the mobj has
	// moved at least teleportClearRadius from TeleportLandX/Y — the
	// destination genuinely doesn't work again until you actually get off
	// it, not just after half a second passes.
	TeleportGuard                bool
	TeleportLandX, TeleportLandY float64

	// explosionBoost, when >0, is a one-shot multiplier aExplode applies to
	// explosionBarrel before spawning this barrel's death burst, then
	// resets to 0 — set by a rocket's killing hit (see advanceProjectile)
	// so that single, merged explosion reads as bigger than an ordinary
	// barrel's, instead of the rocket's own burst and the barrel's playing
	// as two separate explosions.
	explosionBoost float64

	// barrelBubbleTimer/barrelFogTimer count down to this barrel's next
	// ambient bubble/fog puff while it's still intact — see
	// updateBarrelAmbience in engine/barrelambience.go. Unused by any
	// non-barrel mobj, the same way Projectile's own flameTrailTimer/
	// smokeTrailTimer are unused by any non-rocket shot.
	barrelBubbleTimer float64
	barrelFogTimer    float64

	removed bool
}

// teleportFreezeTics is how long a just-landed mobj is immune to being
// teleported again — long enough to walk off a cramped landing pad, short
// enough not to block a map's deliberate back-to-back teleporter chain.
const teleportFreezeTics = 18 // 1/2 second at 35 tics/sec

// teleportClearRadius is how far a mobj must move from where it landed
// (TeleportLandX/Y) before TeleportGuard lets evTeleport fire again — see
// TeleportGuard's doc comment. A little more than playerRadius*2 so a
// landing pad barely wider than the player still counts as "left" once
// they've stepped off it, not just shifted a few units within it.
const teleportClearRadius = 40.0

// Z-spawn sentinels (id's ONFLOORZ / ONCEILINGZ).
const (
	onFloorZ   = math.MaxFloat64
	onCeilingZ = -math.MaxFloat64
)

// ticDuration is one 35Hz sim step in seconds.
const ticDuration = 1.0 / ticsPerSecond

// sectorFloorCeil resolves the floor and ceiling height at (x, y) via the
// BSP, falling back to a wide-open range if point location fails.
func (g *Game) sectorFloorCeil(x, y float64) (floor, ceil float64) {
	if sec := bsp.PointSector(g.BSP, g.Level, float32(x), float32(y)); sec != nil {
		return sec.FloorZAt(x, y), sec.CeilZAt(x, y)
	}
	return -32768, 32768
}

// P_SpawnMobj creates a mobj of type typ at (x, y, z). z may be onFloorZ or
// onCeilingZ. The mobj is appended to g.mobjs and returned.
func (g *Game) P_SpawnMobj(x, y, z float64, typ mobjType) *Mobj {
	info := &mobjInfo[typ]
	mo := &Mobj{
		X: x, Y: y,
		Type: typ, Info: info,
		Health:       info.SpawnHealth,
		Flags:        info.Flags,
		Radius:       info.Radius,
		Height:       info.Height,
		ReactionTime: info.ReactionTime,
		MoveDir:      diNoDir,
	}
	mo.FloorZ, mo.CeilingZ = g.sectorFloorCeil(x, y)
	switch z {
	case onFloorZ:
		mo.Z = mo.FloorZ
	case onCeilingZ:
		mo.Z = mo.CeilingZ - mo.Height
	default:
		mo.Z = z
	}
	// Seed the interpolation snapshot so the first frame draws it where it
	// spawned, not lerping in from the origin.
	mo.prevX, mo.prevY, mo.prevZ, mo.prevAngle = mo.X, mo.Y, mo.Z, mo.Angle

	// Enter the spawn state (runs its action, if any). A type with no spawn
	// state (S_NULL) is an inert position marker — e.g. a teleport landing
	// spot — kept in the list but never ticked or drawn.
	if info.SpawnState != S_NULL {
		g.setMobjState(mo, info.SpawnState)
	} else {
		mo.stateDef = &states[S_NULL]
		mo.Tics = -1
	}

	g.mobjs = append(g.mobjs, mo)
	return mo
}

// setMobjState is id's P_SetMobjState: point the mobj at state st, copy its
// sprite/frame/tics, run its action, and chase through any 0-tic states.
// Returns false (and removes the mobj) if the chain reaches S_NULL.
func (g *Game) setMobjState(mo *Mobj, st stateNum) bool {
	// A Heretic actor's synthesized MobjInfo points its state links at the
	// S_HERETIC_* sentinels; the ported Doom AI calls setMobjState(mo,
	// mo.Info.SeeState) etc. unchanged, and this routes it into the
	// data-driven Heretic FSM instead (engine/heretic_actors.go).
	if mo.hact != nil && st >= S_HERETIC && st <= S_HERETIC_XDEATH {
		g.hSetPhase(mo, hPhaseForSentinel(st))
		return !mo.removed
	}
	for {
		if st == S_NULL {
			mo.stateDef = &states[S_NULL]
			mo.State = S_NULL
			g.removeMobj(mo)
			return false
		}
		sd := &states[st]
		mo.State = st
		mo.stateDef = sd
		mo.Tics = sd.Tics
		mo.Sprite = sd.Sprite
		mo.Frame = sd.Frame
		mo.Fullbright = sd.fullbright
		if sd.Action != nil {
			sd.Action(g, mo)
		}
		if mo.Tics != 0 {
			return true
		}
		st = sd.Next // 0-tic passthrough
	}
}

// removeMobj marks mo for removal; the mobj system compacts the slice at
// the end of the tic.
func (g *Game) removeMobj(mo *Mobj) { mo.removed = true }

// mobjTick advances one mobj by a single 35Hz tic: momentum for missiles
// and charging lost souls (walking monsters move inside A_Chase instead),
// a floor re-seat for everything else, then the state-duration countdown.
func (g *Game) mobjTick(mo *Mobj) {
	if mo.removed {
		return
	}

	// Snapshot the pre-move state for this tic so the renderer can draw
	// between it and wherever this tic leaves the mobj (see interp.go).
	mo.prevX, mo.prevY, mo.prevZ, mo.prevAngle = mo.X, mo.Y, mo.Z, mo.Angle

	if mo.TeleportFreeze > 0 {
		mo.TeleportFreeze--
	}

	// A dying Heretic effect mobj (a missile playing its impact anim).
	if mo.hExpireIn > 0 {
		if mo.hExpireIn--; mo.hExpireIn <= 0 {
			g.removeMobj(mo)
			return
		}
	}

	// Heretic decorations with an animated sprite (torch/brazier/chandelier)
	// just cycle their frame off the level clock — no state machine.
	if mo.overrideFrames > 1 {
		tics := mo.overrideTics
		if tics < 1 {
			tics = 1
		}
		mo.Frame = (g.levelTime / tics) % mo.overrideFrames
	}

	switch {
	case mo.Flags&MF_MISSILE != 0:
		g.moveMissile(mo)
	case mo.Flags&MF_SKULLFLY != 0:
		g.moveSkull(mo)
	case mo.hact != nil:
		g.hereticActorTick(mo) // data-driven Heretic monster FSM (movement + frames)
	default:
		g.mobjZClip(mo)
	}
	if mo.removed {
		return
	}

	if mo.hact != nil {
		return // Heretic actor — hereticActorTick owns the frame timer, not mo.Tics
	}
	if mo.Tics == -1 {
		return // frozen frame (idle decoration, ready pickup, settled corpse)
	}
	mo.Tics--
	if mo.Tics <= 0 {
		g.setMobjState(mo, mo.stateDef.Next)
	}
}

// mobjZClip keeps a ground-bound thing seated on its current sector's
// floor — the equivalent of id's P_ChangeSector / P_ThingHeightClip run
// after every floor/ceiling move, plus the gravity that would otherwise
// pull a thing left hanging when the platform under it drops. This project
// has no fall arc (Z is pinned to the floor, exactly as the player camera
// is — see handleMovement), so the re-seat is unconditional: a lift or
// crusher moving a sector's floor carries the monsters, corpses and items
// standing in it. Flying things (MF_NOGRAVITY: lost souls, cacodemons,
// pain elementals) and ceiling-hung decorations (MF_SPAWNCEILING) are
// exempt.
func (g *Game) mobjZClip(mo *Mobj) {
	if mo.Flags&(MF_NOGRAVITY|MF_SPAWNCEILING) != 0 {
		return
	}
	floor, ceil := g.sectorFloorCeil(mo.X, mo.Y)
	mo.FloorZ, mo.CeilingZ = floor, ceil
	mo.Z = floor
	// A ceiling that has dropped below the thing's head (a crusher, or a
	// door shutting on a corpse) pushes it back down under the ceiling —
	// and grinds it, id's PIT_ChangeSector crush branch.
	if mo.Z+mo.Height > ceil {
		if mo.Z = ceil - mo.Height; mo.Z < floor {
			mo.Z = floor
		}
		g.crushMobj(mo)
	}
}

// crushMobj is what a descending ceiling does to a thing pinned under it,
// for everything EXCEPT the player (whose crush is always-lethal
// checkPlayerCrush). A live shootable thing takes 10 damage every 4 tics
// with a blood spurt; once it (or an already-dead corpse) is being ground,
// it flattens to a smear and stops — the MF_CORPSE "stop grinding" rule.
func (g *Game) crushMobj(mo *Mobj) {
	if mo == g.playerMobj || mo.removed {
		return
	}
	if mo.Flags&MF_CORPSE != 0 || mo.Health <= 0 {
		if mo.Height > 0 { // flatten once
			mo.Height = 0
			mo.Radius = 0
			mo.Flags &^= MF_SOLID
			g.spawnAttackBlood(mo.X, mo.Y, mo.Z)
		}
		return
	}
	if mo.Flags&MF_SHOOTABLE == 0 {
		return // décor / items are just shoved down, not damaged
	}
	if g.levelTime&3 != 0 {
		return
	}
	g.pDamageMobj(mo, nil, nil, 10)
	if mo.Health > 0 && mo.Flags&MF_NOBLOOD == 0 {
		g.spawnAttackBlood(
			mo.X+float64(pRandomSpread())/64,
			mo.Y+float64(pRandomSpread())/64,
			mo.Z+mo.Height/2,
		)
	}
}

// moveMissile steps a flying missile one tic; on hitting a wall,
// floor/ceiling, or a shootable thing it deals damage and detonates
// (enters its death state). Vanilla moves missiles one whole tic per step
// (so a very fast one can skip a thin target) — kept faithful.
func (g *Game) moveMissile(mo *Mobj) {
	nx, ny, nz := mo.X+mo.MomX, mo.Y+mo.MomY, mo.Z+mo.MomZ
	if g.missilePathBlocked(mo, nx, ny, nz) {
		g.explodeMissile(mo)
		return
	}
	if hit := g.missileThingHit(mo, nx, ny, nz, true); hit != nil {
		base := mo.Info.Damage
		if mo.hDamage > 0 { // Heretic monster projectile (engine/heretic_actors.go)
			base = mo.hDamage
		}
		g.pDamageMobj(hit, mo, mo.Target, (pRandom()%8+1)*base)
		g.explodeMissile(mo)
		return
	}
	mo.X, mo.Y, mo.Z = nx, ny, nz
}

// moveSkull steps a charging lost soul; on impact it damages what it hit
// (or just stops on a wall) and drops back to its spawn state.
func (g *Game) moveSkull(mo *Mobj) {
	nx, ny, nz := mo.X+mo.MomX, mo.Y+mo.MomY, mo.Z+mo.MomZ
	stop := func() {
		mo.Flags &^= MF_SKULLFLY
		mo.MomX, mo.MomY, mo.MomZ = 0, 0, 0
		g.setMobjState(mo, mo.Info.SpawnState)
	}
	if g.missilePathBlocked(mo, nx, ny, nz) {
		stop()
		return
	}
	if hit := g.missileThingHit(mo, nx, ny, nz, false); hit != nil {
		g.pDamageMobj(hit, mo, mo, (pRandom()%8+1)*mo.Info.Damage)
		stop()
		return
	}
	mo.X, mo.Y, mo.Z = nx, ny, nz
}

func (g *Game) explodeMissile(mo *Mobj) {
	// Heretic monster projectiles are MT_HERETIC: freeze in place and play a
	// short impact animation (engine/heretic_actors.go), else just vanish.
	if mo.Type == MT_HERETIC {
		if md := mo.hMissile; md != nil {
			spr, frames, tics := md.deathSprite, md.deathFrames, md.deathTics
			if spr == "" { // default: replay the flight sprite a few times as a puff
				spr, frames, tics = md.sprite, 3, 3
			}
			mo.Flags &^= MF_MISSILE
			mo.MomX, mo.MomY, mo.MomZ = 0, 0, 0
			mo.spriteOverride = spr
			mo.overrideFrames, mo.overrideTics = frames, tics
			mo.Frame = 0
			mo.hExpireIn = frames * tics
			hPlay(g, md.deathSound)
			return
		}
		g.removeMobj(mo)
		return
	}
	mo.MomX, mo.MomY, mo.MomZ = 0, 0, 0
	mo.Flags &^= MF_MISSILE
	if mo.Info.DeathSound != "" {
		g.playSound(mo.Info.DeathSound)
	}
	g.setMobjState(mo, mo.Info.DeathState)
}

// missilePathBlocked reports whether point (x,y,z) is blocked by a solid
// line, a too-tall/short two-sided opening, or the floor/ceiling.
func (g *Game) missilePathBlocked(mo *Mobj, x, y, z float64) bool {
	blocked := false
	g.blockGrid.forEachNear(x, y, mo.Radius, func(i int32) bool {
		ld := &g.Level.Linedefs[i]
		v1 := g.Level.Vertexes[ld.StartVertex]
		v2 := g.Level.Vertexes[ld.EndVertex]
		if distancePointToSegment(x, y, float64(v1.X), float64(v1.Y), float64(v2.X), float64(v2.Y)) >= mo.Radius {
			return true
		}
		if ld.BackSidedef == wad.NoSidedef {
			blocked = true
			return false
		}
		open := g.wallOpeningAt(ld)
		if !open.twoSided || z <= open.bottom || z >= open.top {
			blocked = true
			return false
		}
		return true
	})
	if blocked {
		return true
	}
	f, c := g.sectorFloorCeil(x, y)
	return z <= f || z >= c
}

// missileThingHit returns the first shootable thing whose body (x,y,z)
// enters. skipKin drops the launcher (mo.Target) and same-species
// infighting immunity (id's MF_MISSILE rules); a charging skull passes
// false so it hits anything including its own target.
func (g *Game) missileThingHit(mo *Mobj, x, y, z float64, skipKin bool) *Mobj {
	test := func(other *Mobj) *Mobj {
		if other == nil || other == mo || other.removed ||
			other.Flags&MF_SHOOTABLE == 0 || other.Health <= 0 {
			return nil
		}
		if skipKin {
			if other == mo.Target {
				return nil
			}
			if mo.Target != nil && other.Type == mo.Target.Type && other.Type != MT_PLAYER {
				return nil // monsters of the same species don't shoot each other
			}
		}
		block := other.Radius + mo.Radius
		if math.Abs(other.X-x) >= block || math.Abs(other.Y-y) >= block {
			return nil
		}
		if z+mo.Height < other.Z || z > other.Z+other.Height {
			return nil
		}
		return other
	}
	if h := test(g.playerMobj); h != nil {
		return h
	}
	for _, other := range g.mobjs {
		if h := test(other); h != nil {
			return h
		}
	}
	return nil
}

// runTic advances the whole map-object simulation by one 35Hz tic and then
// compacts out anything removed this tic.
func (g *Game) runTic() {
	g.levelTime++
	g.snapshotSectorHeights() // "start of tic" heights for hardware render interpolation

	// Mirror the player's per-frame camera state onto its map object so AI
	// (targeting, sight, chase) sees an up-to-date position this tic.
	if p := g.playerMobj; p != nil {
		p.X, p.Y = g.Camera.X, g.Camera.Y
		p.Z = g.PlayerZ // feet; independent of the eye's landing dip / ceiling clamp
		p.Angle = g.Camera.Angle
		p.Health = g.Player.Health
		// The player mobj isn't in g.mobjs, so mobjTick never runs for it —
		// count its post-teleport immunity down here, or evTeleport refuses
		// every crossing after the first and the pad only works once.
		if p.TeleportFreeze > 0 {
			p.TeleportFreeze--
		}
	}

	g.updatePowers()
	g.tickSpecials()
	g.animateFlats()
	g.animateWalls()
	g.checkPlayerCrush()
	g.playerInSpecialSector()
	g.checkPlayerLiquidCrossing()
	g.checkFireDamage()
	if g.heretic {
		g.tickTimeBombs()
	}

	for _, mo := range g.mobjs {
		g.mobjTick(mo)
	}

	g.checkItemPickups()

	alive := g.mobjs[:0]
	for _, mo := range g.mobjs {
		if !mo.removed {
			alive = append(alive, mo)
		}
	}
	g.mobjs = alive
}

// voxelKVXFrontBias backs a KVX model's built-in "front" a quarter turn to
// line up with a Doom thing's facing.
const voxelKVXFrontBias = -math.Pi / 2

// worldGeomChanged reports whether any sector's floor or ceiling height has
// moved since the last call, refreshing the snapshot. HardwareMode uses it
// to decide when the cached static level geometry needs rebuilding — a rare
// event (a door/lift mid-travel), so the rest of the time the GPU just
// re-draws the unchanged device-local buffer. Returns false on the first
// call / after a level change (WorldBuilder was just built fresh).
func (g *Game) worldGeomChanged() bool {
	secs := g.Level.Sectors
	if len(g.geomHeights) != len(secs)*2 {
		g.geomHeights = make([]int16, len(secs)*2)
		for i := range secs {
			g.geomHeights[i*2], g.geomHeights[i*2+1] = secs[i].FloorHeight, secs[i].CeilingHeight
		}
		return false
	}
	changed := false
	for i := range secs {
		f, c := secs[i].FloorHeight, secs[i].CeilingHeight
		if g.geomHeights[i*2] != f || g.geomHeights[i*2+1] != c {
			g.geomHeights[i*2], g.geomHeights[i*2+1] = f, c
			changed = true
		}
	}
	return changed
}

// collectWorldObjects resolves every live map object and in-flight
// projectile for the hardware-geometry path: a voxel-model instance where
// the loaded pack has one for a thing's sprite frame, a camera-facing
// billboard otherwise (and always for projectiles). The GPU equivalent of
// drawMobjs + the projectile blit loop. Things clearly behind the view
// plane are dropped cheaply; the GPU depth test sorts the rest. Reuses
// g.hwSprites / g.hwVoxels backing arrays.
func (g *Game) collectWorldObjects() ([]worldgeo.Sprite, []worldgeo.VoxelInstance) {
	sprites := g.hwSprites[:0]
	voxels := g.hwVoxels[:0]
	cx, cy := g.Camera.X, g.Camera.Y
	fdx, fdy := math.Cos(g.Camera.Angle), math.Sin(g.Camera.Angle)
	frac := g.renderLerp()
	vscale := g.VoxelScale
	if vscale <= 0 {
		vscale = 1
	}

	for _, mo := range g.mobjs {
		if mo.State == S_NULL {
			continue
		}
		if (mo.X-cx)*fdx+(mo.Y-cy)*fdy < -160 {
			continue // behind the view plane (160u margin covers the largest radius)
		}
		if g.explosionParticles && mo.Type == MT_BARREL && mo.State >= S_BEXP1 && mo.State <= S_BEXP5 {
			// The native particle burst (spawned once, in aExplode) replaces
			// the vanilla BEXP flip-book entirely when the feature is on —
			// same treatment as a rocket's own ExplodeFrames in the
			// projectile loop below.
			continue
		}
		name := mobjSpriteName(mo)
		ix, iy, iz, iang := mo.renderState(frac)
		if g.Voxels != nil {
			if model, angOff, mscale, ok := g.Voxels.Model(name, mo.Frame); ok && model != nil {
				if mscale <= 0 {
					mscale = 1
				}
				voxels = append(voxels, worldgeo.VoxelInstance{
					Model: model,
					X:     float32(ix), Y: float32(iy), Z: float32(iz),
					Yaw:        float32(iang + angOff*math.Pi/180 + voxelKVXFrontBias),
					Scale:      float32(vscale * mscale),
					Light:      float32(g.spriteSectorLight(ix, iy)) / 255,
					FullBright: mo.Fullbright,
				})
				if g.heretic {
					if f, ok := g.hereticFlameFor(name, model, ix, iy, iz, vscale*mscale, g.renderClock); ok {
						voxels = append(voxels, f)
					}
				}
				continue // the voxel model replaces the billboard
			}
		}
		sp, flip, lift, ok := g.Raster.ThingSprite(g.Camera, ix, iy, iang, name, mo.Frame)
		if !ok {
			continue
		}
		sprites = append(sprites, worldgeo.Sprite{
			X: float32(ix), Y: float32(iy), Z: float32(iz),
			Lift: float32(lift), SizeMul: 1,
			Tex:        sp,
			Light:      float32(g.spriteSectorLight(ix, iy)) / 255,
			FullBright: mo.Fullbright,
			Flip:       flip,
		})
	}
	for _, p := range g.projectiles {
		if p.exploding && g.explosionParticles {
			// The native particle burst (spawnExplosionParticles) replaces
			// the vanilla explosion-frame sprite entirely when the feature
			// is on; falls back to it when off, so the toggle still shows
			// *something* at the impact point either way.
			continue
		}
		name, ok := p.CurrentSprite()
		if !ok {
			continue
		}
		sp, ok := g.Raster.Textures().Sprite(name)
		if !ok || sp == nil {
			continue
		}
		// Projectiles / explosions glow — emissive, full bright, no rotations.
		sprites = append(sprites, worldgeo.Sprite{
			X: float32(p.X), Y: float32(p.Y), Z: float32(p.Z),
			Lift: 0, SizeMul: float32(p.SizeMultiplier()),
			Tex:        sp,
			Light:      1,
			FullBright: true,
		})
	}
	g.hwSprites, g.hwVoxels = sprites, voxels
	return sprites, voxels
}

// collectGroundShadows lists this frame's contact-shadow casters as {x, y,
// floorZ, radius}, nearest-first and capped at hwGroundShadowCap so a
// slaughtermap can't overflow the World UBO array. world.frag stamps a soft
// blob on the floor under each — always on (cheap: a small UBO array + a
// short per-floor-pixel loop).
func (g *Game) collectGroundShadows() [][4]float32 {
	cx, cy := g.Camera.X, g.Camera.Y
	out := g.hwShadows[:0]
	for _, mo := range g.mobjs {
		if mo.State == S_NULL || mo.Flags&(MF_MISSILE|MF_SPAWNCEILING|MF_NOGRAVITY) != 0 {
			continue
		}
		if mo.Radius < 6 || mo.Z > mo.FloorZ+16 {
			continue // too small, or airborne / hung
		}
		dx, dy := mo.X-cx, mo.Y-cy
		if dx*dx+dy*dy > 1600*1600 {
			continue // a blob this far off is sub-pixel
		}
		r := mo.Radius * 1.6
		r = math.Max(14, math.Min(60, r))
		out = append(out, [4]float32{float32(mo.X), float32(mo.Y), float32(mo.FloorZ), float32(r)})
	}
	if len(out) > hwGroundShadowCap {
		slices.SortFunc(out, func(a, b [4]float32) int {
			da := (float64(a[0])-cx)*(float64(a[0])-cx) + (float64(a[1])-cy)*(float64(a[1])-cy)
			db := (float64(b[0])-cx)*(float64(b[0])-cx) + (float64(b[1])-cy)*(float64(b[1])-cy)
			switch {
			case da < db:
				return -1
			case da > db:
				return 1
			default:
				return 0
			}
		})
		out = out[:hwGroundShadowCap]
	}
	g.hwShadows = out
	return out
}

// hwGroundShadowCap must equal SHADOW_MAX in world.frag and shadowCasterMax
// in render/vulkan/world.go.
const hwGroundShadowCap = 24

// spriteSectorLight is the light level of the sector a world point stands
// in — what raster shades a billboard by, mirroring id's R_ProjectSprite
// reading thing->subsector->sector->lightlevel. It also drives the held
// weapon's brightness (from the player's own position). Outside the level
// geometry it returns full bright rather than guessing dark.
func (g *Game) spriteSectorLight(x, y float64) int16 {
	if sec := bsp.PointSector(g.BSP, g.Level, float32(x), float32(y)); sec != nil {
		return sec.LightLevel
	}
	return 255
}
