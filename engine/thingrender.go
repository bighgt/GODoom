package engine

import (
	"twopointfive/bsp"
	"twopointfive/wad"
)

// ThingRenderInfo resolves one wad.Level THINGS entry to what a renderer
// needs to draw it *without* a live simulation: the sprite prefix/frame its
// spawn state shows, whether that frame is full-bright, and its world Z
// (floor or ceiling seat, exactly like P_SpawnMobj resolves a
// MF_SPAWNCEILING thing via onFloorZ/onCeilingZ — see mobj.go). tree/lvl are
// the same BSP tree and level a caller already has on hand for
// worldgeo.New (bsp.Build(lvl)); this does its own bsp.PointSector lookup,
// same as sectorFloorCeil, since there's no live Game to ask.
//
// ok is false for anything spawnMapThings itself would skip: player/
// deathmatch starts and any doomednum with no MobjInfo entry (an unknown or
// unsupported thing type) — doomedNumToType simply has no entry for those,
// so this mirrors that skip list without duplicating it. Skill/ambush
// flags are NOT applied — every thing in the WAD is considered "present"
// here, since a static viewer wants to show what the map contains rather
// than simulate one particular skill's spawn set.
//
// This is the one piece of engine's Doom-specific data (doomednum -> sprite)
// exported for a caller — like cmd/isoengine — that wants to render map
// things without pulling in or running any of the simulation itself:
// importing this package costs nothing at runtime beyond binary size (its
// init()s only populate static tables), so this is a thin export rather
// than a duplicated copy of the doomednum table.
func ThingRenderInfo(lvl *wad.Level, tree bsp.Node, t wad.Thing) (prefix string, frame int, fullBright bool, z float64, ok bool) {
	typ, ok := doomedNumToType[int(t.Type)]
	if !ok {
		return "", 0, false, 0, false
	}
	info := &mobjInfo[typ]
	if info.SpawnState == S_NULL {
		return "", 0, false, 0, false
	}
	st := states[info.SpawnState]

	floor, ceil := -32768.0, 32768.0
	if sec := bsp.PointSector(tree, lvl, float32(t.X), float32(t.Y)); sec != nil {
		floor, ceil = sec.FloorZAt(float64(t.X), float64(t.Y)), sec.CeilZAt(float64(t.X), float64(t.Y))
	}
	z = floor
	if info.Flags&MF_SPAWNCEILING != 0 {
		z = ceil - info.Height
	}
	return spriteNames[st.Sprite], st.Frame, st.fullbright, z, true
}
