package engine

import (
	"log"
	"math"

	"twopointfive/assets/vfs"
	"twopointfive/engine/slopes"
	"twopointfive/wad"
)

// SLOPES support — an optional sidecar lump carrying two kinds of sector
// geometry vanilla Doom has no room for: a tilted floor/ceiling plane, and
// a Boom/GZDoom-style "3D floor" (room-over-room) band borrowed from
// another sector — see engine/slopes' own doc comment for the grammar.
// Vanilla IWADs have no SLOPES lump, so nothing changes for them:
// wad.Sector's FloorZAt/CeilZAt degenerate to the ordinary flat height
// everywhere, and g.extraFloors stays empty, until a SLOPES entry says
// otherwise.
//
// The lump parser lives in engine/slopes (a small hand-rolled reader). This
// file is only the WAD glue, following the same convention as
// gldefs_load.go/mapinfo_load.go: read every SLOPES lump from the WAD (file
// order, so a later PWAD layers over the IWAD) then from the mounted mods,
// parse once, and apply per map.

// loadSlopes concatenates every SLOPES lump from w, then from the mounted
// mods, and parses the result into per-map plane defs and extra-floor defs.
// Both return maps are nil when nothing provides a SLOPES lump.
func loadSlopes(w *wad.WAD, mods *vfs.FS) (planes map[string][]slopes.Def, floors map[string][]slopes.ExtraFloorDef) {
	var blob []byte
	count := 0
	add := func(b []byte) {
		blob = append(blob, b...)
		blob = append(blob, '\n')
		count++
	}

	if w != nil {
		for i := range w.Entries {
			if w.Entries[i].Name == "SLOPES" {
				add(w.Lump(i))
			}
		}
	}
	if mods != nil {
		for _, b := range mods.Lumps("SLOPES") {
			add(b)
		}
	}
	if count == 0 {
		return nil, nil
	}

	planes, floors, errs := slopes.Parse(blob)
	for _, e := range errs {
		log.Printf("%v", e)
	}
	nPlanes, nFloors := 0, 0
	for _, ds := range planes {
		nPlanes += len(ds)
	}
	for _, ds := range floors {
		nFloors += len(ds)
	}
	log.Printf("slopes: loaded from %d lump(s) — %d sloped plane(s), %d extra floor(s), across %d map(s)",
		count, nPlanes, nFloors, len(planes)+len(floors))
	return planes, floors
}

// applySlopes tilts g.Level's own sectors and rebuilds g.extraFloors per
// g.slopeDefs/g.extraFloorDefs' entries for the current map name, if any —
// called once per level load (NewGame, loadMap, right after g.Level/g.BSP
// are set, before anything reads a sector height). Idempotent and a no-op
// with no SLOPES lump loaded or no entries for this map, so it's always
// safe to call.
func (g *Game) applySlopes() {
	g.extraFloors = nil
	if g.Level == nil {
		return
	}
	mapName := upperMapName(g.Level.Name)

	for _, d := range g.slopeDefs[mapName] {
		if d.SectorIndex < 0 || d.SectorIndex >= len(g.Level.Sectors) {
			log.Printf("slopes: line %d: sector %d out of range (map has %d)", d.Line, d.SectorIndex, len(g.Level.Sectors))
			continue
		}
		sec := &g.Level.Sectors[d.SectorIndex]
		tiltX, tiltY, ok := planeTilt(d.P1, d.P2, d.P3)
		if !ok {
			log.Printf("slopes: line %d: sector %d's three points are collinear/degenerate — skipped", d.Line, d.SectorIndex)
			continue
		}
		// The pivot is shared between a sector's floor and ceiling tilt (see
		// wad.Sector's doc comment) — the last plane definition for this
		// sector to set it wins, which only matters if floor and ceiling are
		// each given a DIFFERENT P1 (unusual but not forbidden).
		sec.SlopePivotX, sec.SlopePivotY = d.P1[0], d.P1[1]
		if d.Ceiling {
			sec.CeilingHeight = int16(math.Round(d.P1[2]))
			sec.CeilTiltX, sec.CeilTiltY = tiltX, tiltY
		} else {
			sec.FloorHeight = int16(math.Round(d.P1[2]))
			sec.FloorTiltX, sec.FloorTiltY = tiltX, tiltY
		}
	}

	for _, d := range g.extraFloorDefs[mapName] {
		if d.TargetSector < 0 || d.TargetSector >= len(g.Level.Sectors) {
			log.Printf("slopes: line %d: target sector %d out of range (map has %d)", d.Line, d.TargetSector, len(g.Level.Sectors))
			continue
		}
		if d.ControlSector < 0 || d.ControlSector >= len(g.Level.Sectors) {
			log.Printf("slopes: line %d: control sector %d out of range (map has %d)", d.Line, d.ControlSector, len(g.Level.Sectors))
			continue
		}
		if g.extraFloors == nil {
			g.extraFloors = map[int][]ExtraFloor{}
		}
		g.extraFloors[d.TargetSector] = append(g.extraFloors[d.TargetSector], ExtraFloor{ControlSector: d.ControlSector})
	}
}

// planeTilt solves for the (TiltX, TiltY) of the plane through p1, p2, p3
// that passes through p1 with that exact height (p1 is the pivot — see
// wad.Sector's doc comment) — i.e. z(x,y) = p1.z + TiltX*(x-p1.x) +
// TiltY*(y-p1.y), solved from the two other points' offsets from p1. ok is
// false when the three points are collinear (or p2/p3 coincide with p1) —
// no unique plane, or a degenerate (zero-tilt either way) one.
func planeTilt(p1, p2, p3 [3]float64) (tiltX, tiltY float64, ok bool) {
	dx2, dy2, dz2 := p2[0]-p1[0], p2[1]-p1[1], p2[2]-p1[2]
	dx3, dy3, dz3 := p3[0]-p1[0], p3[1]-p1[1], p3[2]-p1[2]
	det := dx2*dy3 - dx3*dy2
	if det == 0 {
		return 0, 0, false
	}
	tiltX = (dz2*dy3 - dz3*dy2) / det
	tiltY = (dx2*dz3 - dx3*dz2) / det
	return tiltX, tiltY, true
}
