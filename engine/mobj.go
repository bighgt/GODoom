package engine

import (
	"log"
	"math"
	"slices"

	"twopointfive/bsp"
	"twopointfive/raster"
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

	// AI bookkeeping.
	Target       *Mobj
	MoveDir      int
	MoveCount    int
	ReactionTime int
	Threshold    int

	removed bool
}

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
		return float64(sec.FloorHeight), float64(sec.CeilingHeight)
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

	switch {
	case mo.Flags&MF_MISSILE != 0:
		g.moveMissile(mo)
	case mo.Flags&MF_SKULLFLY != 0:
		g.moveSkull(mo)
	default:
		g.mobjZClip(mo)
	}
	if mo.removed {
		return
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
	// door shutting on a corpse) pushes it back down under the ceiling.
	if mo.Z+mo.Height > ceil {
		if mo.Z = ceil - mo.Height; mo.Z < floor {
			mo.Z = floor
		}
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
		dmg := (pRandom()%8 + 1) * mo.Info.Damage
		g.pDamageMobj(hit, mo, mo.Target, dmg)
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
	}

	g.updatePowers()
	g.tickSpecials()
	g.animateFlats()
	g.animateWalls()
	g.checkPlayerCrush()
	g.playerInSpecialSector()

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

// drawMobjs renders every live map object farthest first (so nearer things
// overdraw) as either a voxel model or a flat billboard. Called each frame
// after the world has written the depth buffer — see raster.DrawVoxel /
// raster.DrawThing.
func (g *Game) drawMobjs() {
	if len(g.mobjs) == 0 {
		return
	}
	order := append(g.mobjScratch[:0], g.mobjs...)
	cx, cy := g.Camera.X, g.Camera.Y
	d2 := func(m *Mobj) float64 { return (m.X-cx)*(m.X-cx) + (m.Y-cy)*(m.Y-cy) }
	slices.SortFunc(order, func(a, b *Mobj) int {
		// Farthest first (nearer things overdraw).
		switch da, db := d2(a), d2(b); {
		case da > db:
			return -1
		case da < db:
			return 1
		default:
			return 0
		}
	})
	g.mobjScratch = order

	// Forward axis, for a cheap "clearly behind the camera" reject: those
	// things would still cost a projection, an atan2 rotation pick and a BSP
	// sector-light lookup in the draw pass before the rasterizer dropped them.
	fdx, fdy := math.Cos(g.Camera.Angle), math.Sin(g.Camera.Angle)

	frac := g.renderLerp()
	things := g.thingScratch[:0]
	haveModel := 0
	for _, mo := range order {
		if mo.State == S_NULL {
			continue // inert marker (teleport dest) — nothing to draw
		}
		if (mo.X-cx)*fdx+(mo.Y-cy)*fdy < -160 {
			continue // behind the view plane (160u margin covers the largest radius)
		}
		name := spriteNames[mo.Sprite]
		ix, iy, iz, iang := mo.renderState(frac)
		t := raster.SceneThing{
			X: ix, Y: iy, Z: iz, Angle: iang,
			Light: g.spriteSectorLight(ix, iy), FullBright: mo.Fullbright,
			Prefix: name, Frame: mo.Frame,
		}
		if g.Voxels != nil {
			if m, angOff, _, ok := g.Voxels.Model(name, mo.Frame); ok {
				haveModel++
				t.Model, t.ModelAngleOff = m, angOff
			}
		}
		things = append(things, t)
	}
	g.thingScratch = things

	// One parallel pass over the strip workers (the wall render is already
	// strip-parallel; a screenful of big sprites/voxels was the next cost).
	asVoxel, asSprite := g.Raster.DrawThings(g.Camera, things)

	// One line, the first frame anything is on screen, so it's obvious from
	// the log whether voxels are actually being drawn.
	if g.Voxels != nil && !g.loggedVoxelStatus && asVoxel+asSprite > 0 {
		g.loggedVoxelStatus = true
		log.Printf("engine: map objects this frame — %d voxel, %d sprite (%d had a model, %d too small/declined)",
			asVoxel, asSprite, haveModel, haveModel-asVoxel)
	}
}

// voxelKVXFrontBias backs a KVX model's built-in "front" a quarter turn to
// line up with a Doom thing's facing — raster/voxel.go's voxelYawBias.
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
		name := spriteNames[mo.Sprite]
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

// collectGroundShadows lists this frame's contact-shadow casters for the
// hardware path — grounded things (same filter as drawGroundShadows) as
// {x, y, floorZ, radius}, nearest-first and capped at hwGroundShadowCap so a
// slaughtermap can't overflow the World UBO array. world.frag stamps a soft
// blob on the floor under each. Always on for the hardware path (cheap: a
// small UBO array + a short per-floor-pixel loop); the groundShadows config
// gates only the software rasterizer's own blob pass.
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

// drawGroundShadows stamps a soft contact shadow on the floor under every
// grounded thing and under the player, so nothing reads as hovering. Call
// after the world pass (r.depth is filled) and before the sprite/voxel
// pass, so each thing draws over its own shadow. A no-op when config
// groundShadows is off (raster.SetGroundShadows was told the same) — it is
// off by default.
func (g *Game) drawGroundShadows() {
	if !g.groundShadows {
		return
	}
	for _, mo := range g.mobjs {
		if mo.State == S_NULL || mo.Flags&(MF_MISSILE|MF_SPAWNCEILING|MF_NOGRAVITY) != 0 {
			continue
		}
		if mo.Radius < 6 || mo.Z > mo.FloorZ+16 {
			continue // too small to matter, or airborne / hung — no ground contact
		}
		radius := mo.Radius * 1.6
		if radius < 14 {
			radius = 14
		} else if radius > 60 {
			radius = 60
		}
		strength := 0.5
		if mo.Flags&MF_CORPSE != 0 {
			strength = 0.4
		}
		g.Raster.DrawGroundShadow(g.Camera, mo.X, mo.Y, mo.FloorZ, radius, strength)
	}
	// The player's own shadow — visible when looking down, and it grounds
	// the view the same way.
	g.Raster.DrawGroundShadow(g.Camera, g.Camera.X, g.Camera.Y, g.PlayerZ, 24, 0.5)
}

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
