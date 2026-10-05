package engine

import (
	"math"
	"sort"

	"twopointfive/assets"
	"twopointfive/assets/models"
	"twopointfive/bsp"
	"twopointfive/render/worldgeo"
	"twopointfive/wad"
)

// Map objects are voxel-only. Every monster, item, decoration and barrel —
// and whatever it becomes (a corpse, a dropped clip) — is drawn as a voxel
// model from the WAD's baked-in pack (Game.Voxels, loaded from the real-3D
// WAD's TPFVOXEL — see assets.LoadVoxelsFromWAD), never as a sprite. An
// object with no model is simply not drawn. Effects keep their sprites:
// missiles, puffs, blood, teleport fog, the player's own projectiles and
// the player — see isMapObject.

// isMapObject reports whether mo is a voxel-only map object rather than an
// effect: a Doom type a map can place (DoomedNum > 0), or any Heretic thing
// except a missile (every Heretic object is one MT_HERETIC type, told apart
// by sprite; its missiles carry hMissile).
func isMapObject(mo *Mobj) bool {
	if mo.Type == MT_HERETIC {
		return mo.hMissile == nil
	}
	return mo.Info != nil && mo.Info.DoomedNum > 0
}

// appendMobjVoxel resolves mo's 3D model (withModels, first-person only) or
// voxel model and, if it has one, appends
// the instance (plus a Heretic torch's flame overlay) to *voxels. A map
// object missing just this frame falls back to its nearest modelled frame
// (VoxelSet.ModelNearest) rather than dropping out. Reports whether mo is
// fully handled — drawn as a voxel, or a map object with no model at all
// (voxel-only: not drawn) — so the caller only billboards effects.
func (g *Game) appendMobjVoxel(mo *Mobj, name string, ix, iy, iz, iang, vscale float64, voxels *[]worldgeo.VoxelInstance, withModels bool) bool {
	if withModels && g.Models != nil && g.appendMobjModel(mo, name, ix, iy, iz, iang) {
		return true // a 3D model outranks the voxel (and the sprite)
	}
	obj := isMapObject(mo)
	if g.Voxels == nil {
		return obj
	}
	model, angOff, mscale, ok := g.Voxels.Model(name, mo.Frame)
	if !ok && obj {
		model, angOff, mscale, ok = g.Voxels.ModelNearest(name, mo.Frame)
	}
	if !ok || model == nil {
		return obj
	}
	if mscale <= 0 {
		mscale = 1
	}
	*voxels = append(*voxels, worldgeo.VoxelInstance{
		Model: model,
		X:     float32(ix), Y: float32(iy), Z: float32(iz),
		Yaw:        float32(iang + angOff*math.Pi/180 + voxelKVXFrontBias),
		Scale:      float32(vscale * mscale),
		Light:      float32(g.spriteSectorLight(ix, iy)) / 255,
		FullBright: mo.Fullbright,
		Alpha:      g.mobjTransAlpha(mo),
		Sector:     worldgeo.SectorTag(bsp.PointSectorIndex(g.BSP, g.Level, float32(ix), float32(iy))),
	})
	if g.heretic {
		if f, ok := g.hereticFlameFor(name, model, ix, iy, iz, vscale*mscale, g.renderClock); ok {
			*voxels = append(*voxels, f)
		}
	}
	return true
}

// UnmodelledObjects lists the map-object types a game on w would place but
// vs has no voxel model for at all — under the voxel-only rule these are
// invisible in play. For cmd/wad3d's conversion report. Doom: every
// placeable type's spawn sprite; Heretic: every Heretic map thing and
// monster.
func UnmodelledObjects(w *wad.WAD, vs *assets.VoxelSet) []string {
	has := func(prefix string) bool {
		_, _, _, ok := vs.ModelNearest(prefix, 0)
		return ok
	}
	seen := map[string]bool{}
	var out []string
	add := func(prefix, name string) {
		if prefix == "" || has(prefix) || seen[prefix] {
			return
		}
		seen[prefix] = true
		if name == "" {
			name = prefix
		} else {
			name += " (" + prefix + ")"
		}
		out = append(out, name)
	}
	if w.IsHeretic() {
		for _, d := range hereticMonsters {
			add(d.sprite, d.name)
		}
		for _, hs := range hereticThings {
			add(hs.sprite, "")
		}
	} else {
		for i := range mobjInfo {
			mi := &mobjInfo[i]
			if mi.DoomedNum > 0 && mi.SpawnState != S_NULL {
				add(spriteNames[states[mi.SpawnState].Sprite], mi.Name)
			}
		}
	}
	sort.Strings(out)
	return out
}

// 3D models (assets/models, a MODELDEF pack baked into the real-3D WAD as
// TPFMODEL) outrank voxels: collectWorldObjects draws a map object as its
// model when the pack has one for its sprite frame, else its voxel, else
// (voxel-only) nothing; an effect — a monster fireball, the player's own
// rockets and plasma — as its model when there is one, else its sprite.
// The isometric view has no model pipeline and keeps voxels / sprites.

// WeaponPickupSprites are the Doom weapon pickups' sprites — the models
// objectStyle voxels still draws (cmd/engine filters the pack to these with
// models.Set.Only), so the guns on the floor keep their 3D models while
// monsters, items and decorations are voxels.
var WeaponPickupSprites = []string{"CSAW", "SHOT", "SGN2", "MGUN", "LAUN", "PLAS", "BFUG"}

// appendMobjModel appends mo's model parts for sprite name / mo.Frame to
// g.hwModels, reporting whether the pack has any. A missile is pitched to
// its flight direction for parts that ask (PitchFromMomentum).
func (g *Game) appendMobjModel(mo *Mobj, name string, ix, iy, iz, iang float64) bool {
	parts := g.Models.Parts(name, mo.Frame)
	if len(parts) == 0 {
		return false
	}
	pitch := math.Atan2(mo.MomZ, math.Hypot(mo.MomX, mo.MomY))
	next, lerp := g.modelBlendTarget(mo)
	g.appendModelParts(parts, next, lerp, ix, iy, iz, iang, pitch, float32(g.spriteSectorLight(ix, iy))/255, mo.Fullbright, mobjAddAlpha(mo), g.mobjTransAlpha(mo))
	return true
}

// modelBlendTarget is GZDoom's model frame interpolation for mo: the parts
// bound to the frame mo moves to next, and how far along the way it is (0
// at the start, approaching 1 just before the switch — the render-frame
// fraction of the current tic included, so the blend is smooth at any frame
// rate). nil when mo won't change frame (an infinite state) or has no Doom
// state (a Heretic actor).
//
// A pose held across consecutive states blends as ONE span — GZDoom's
// MODELDEF InterpolateDoubledFrames, generalised to any run length and on
// for every model. Doom's walk cycles hold each frame for two states (the
// imp's run is A A B B C C D D, 3 tics each), and blending state-to-state
// froze the mesh for one state (A -> A) and then moved it twice as fast in
// the next (A -> B): the model walked in jerks.
func (g *Game) modelBlendTarget(mo *Mobj) ([]models.Part, float32) {
	sd := mo.stateDef
	if sd == nil || sd.Tics <= 0 || mo.spriteOverride != "" || sd.Next == S_NULL {
		return nil, 0
	}
	same := func(o *State) bool { return o.Sprite == sd.Sprite && o.Frame == sd.Frame && o.Tics > 0 }
	// The run's states before this one: the array neighbour that chains
	// straight into it (GZDoom's own curState-1 test).
	before := 0
	for st, n := mo.State, 0; st > 0 && n < maxHeldRun; n++ {
		p := &states[st-1]
		if p.Next != st || !same(p) {
			break
		}
		before += p.Tics
		st--
	}
	// ...and after it, up to the first state showing a different frame.
	after := 0
	end := sd
	for n := 0; n < maxHeldRun && end.Next != S_NULL && end.Next != mo.State; n++ {
		ns := &states[end.Next]
		if !same(ns) {
			break
		}
		after += ns.Tics
		end = ns
	}
	if end.Next == S_NULL {
		return nil, 0
	}
	ns := &states[end.Next]
	next := g.Models.Parts(spriteNames[ns.Sprite], ns.Frame)
	if len(next) == 0 {
		return nil, 0
	}
	cur := max(mo.stateTics, 1)
	elapsed := float64(before+cur-mo.Tics) + g.renderLerp()
	f := elapsed / float64(before+cur+after)
	if f < 0 {
		f = 0
	} else if f > 1 {
		f = 1
	}
	return next, float32(f)
}

// maxHeldRun bounds modelBlendTarget's walk along a run of same-frame
// states (a looping 1-frame state chain would otherwise never end).
const maxHeldRun = 8

// appendModelParts turns one binding's parts into ModelInstances, each
// blending toward the matching part of next (same slot, same model) by
// lerp, and sized by config modelScale (Game.ModelScale — the same
// multiplier voxel models use). add > 0 draws them additively at that
// alpha, alpha > 0 as translucent glass (renderstyle.go).
func (g *Game) appendModelParts(parts, next []models.Part, lerp float32, x, y, z, yaw, pitch float64, light float32, fullBright bool, add, alpha float32) {
	const deg = math.Pi / 180
	k := g.ModelScale
	if k <= 0 {
		k = 1
	}
	for i, p := range parts {
		fm := p.Mesh.Frame(p.Frame)
		if fm == nil || fm.Count == 0 {
			continue
		}
		var to *models.FrameMesh
		if !p.NoInterp {
			// The same slot's part in the next frame: usually at the same
			// index, but a slot a frame hides (frame 999) shifts the rest.
			n := -1
			if i < len(next) && next[i].Slot == p.Slot {
				n = i
			} else {
				for j := range next {
					if next[j].Slot == p.Slot {
						n = j
						break
					}
				}
			}
			if n >= 0 && next[n].Mesh == p.Mesh && next[n].Frame != p.Frame {
				to = p.Mesh.Frame(next[n].Frame)
			}
		}
		t := worldgeo.ModelTransform{
			X: x, Y: y, Z: z,
			Yaw:    yaw + float64(p.AngleOffset)*deg,
			Pitch:  float64(p.PitchOffset) * deg,
			Roll:   float64(p.RollOffset) * deg,
			Scale:  [3]float64{k * float64(p.Scale[0]), k * float64(p.Scale[1]), k * float64(p.Scale[2])},
			Offset: [3]float64{k * float64(p.Offset[0]), k * float64(p.Offset[1]), k * float64(p.Offset[2])},
		}
		if p.PitchFromMomentum {
			t.Pitch += pitch
		}
		if p.Rotating {
			// GZDoom's MODELDEF spin: RotSpeed revolutions per 200 tics.
			t.SpinAxis = [3]float64{float64(p.RotVec[0]), float64(p.RotVec[1]), float64(p.RotVec[2])}
			t.Spin = 2 * math.Pi * float64(p.RotSpeed) * g.renderClock * ticsPerSecond / 200
		}
		inst := worldgeo.ModelInstance{
			Mesh: fm, Skin: p.Skin, Model: worldgeo.ModelMatrix(t), Light: light, FullBright: fullBright,
			Additive: add, Alpha: alpha,
		}
		if to != nil && to.Count == fm.Count {
			inst.Next, inst.Lerp = to, lerp
		}
		g.hwModels = append(g.hwModels, inst)
	}
}

// appendProjectileModel draws the player's in-flight projectile p as its
// model when the pack has one for its current sprite ("PLSSA0" -> PLSS A),
// reporting whether it did.
func (g *Game) appendProjectileModel(p *Projectile, name string) bool {
	if g.Models == nil || len(name) < 5 {
		return false
	}
	parts := g.Models.Parts(name[:4], int(name[4])-'A')
	if len(parts) == 0 {
		return false
	}
	yaw := math.Atan2(p.DY, p.DX)
	pitch := math.Atan2(p.DZ, math.Hypot(p.DX, p.DY))
	g.appendModelParts(parts, nil, 0, p.X, p.Y, p.Z, yaw, pitch, 1, true, projectileAddAlpha(p), 0)
	return true
}
