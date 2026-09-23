package editor

import (
	"testing"

	"twopointfive/engine/slopes"
	"twopointfive/wad"
)

// newTestDoc builds a Doc from NewEmptyDoc without touching disk — firstOf
// (doc.go) falls back to its own preference list when Bank's Walls/Flats
// are empty, so a zero-value Bank is a safe stand-in here.
func newTestDoc() *Doc {
	return NewEmptyDoc(&Bank{}, "")
}

func TestSetSectorProperties(t *testing.T) {
	d := newTestDoc()
	d.geomDirty = false

	if !d.SetSectorHeights(0, 10, 200) {
		t.Fatal("SetSectorHeights returned false")
	}
	if d.Level.Sectors[0].FloorHeight != 10 || d.Level.Sectors[0].CeilingHeight != 200 {
		t.Errorf("heights = %d/%d, want 10/200", d.Level.Sectors[0].FloorHeight, d.Level.Sectors[0].CeilingHeight)
	}
	if !d.geomDirty {
		t.Error("SetSectorHeights should mark geometry dirty (it changes the built planes)")
	}

	d.geomDirty = false
	if !d.SetSectorLight(0, 100) {
		t.Fatal("SetSectorLight returned false")
	}
	if d.Level.Sectors[0].LightLevel != 100 {
		t.Errorf("light = %d, want 100", d.Level.Sectors[0].LightLevel)
	}
	if !d.geomDirty {
		t.Error("SetSectorLight should mark geometry dirty (it's baked into vertex shading)")
	}

	d.geomDirty = false
	if !d.SetSectorSpecial(0, 5) {
		t.Fatal("SetSectorSpecial returned false")
	}
	if d.Level.Sectors[0].SpecialType != 5 {
		t.Errorf("special = %d, want 5", d.Level.Sectors[0].SpecialType)
	}
	if d.geomDirty {
		t.Error("SetSectorSpecial is gameplay-only data — should not mark geometry dirty")
	}

	if !d.SetSectorTag(0, 7) {
		t.Fatal("SetSectorTag returned false")
	}
	if d.Level.Sectors[0].Tag != 7 {
		t.Errorf("tag = %d, want 7", d.Level.Sectors[0].Tag)
	}

	if d.SetSectorHeights(99, 0, 0) || d.SetSectorLight(99, 0) || d.SetSectorSpecial(99, 0) || d.SetSectorTag(99, 0) {
		t.Error("out-of-range sector index should return false")
	}
}

func TestSetLineProperties(t *testing.T) {
	d := newTestDoc()
	d.geomDirty = false

	if !d.SetLineFlags(0, 0x0042) {
		t.Fatal("SetLineFlags returned false")
	}
	if d.Level.Linedefs[0].Flags != 0x0042 {
		t.Errorf("flags = %#x, want 0x42", d.Level.Linedefs[0].Flags)
	}
	if d.geomDirty {
		t.Error("SetLineFlags is gameplay-only data — should not mark geometry dirty")
	}

	if !d.SetLineSpecial(0, 11) {
		t.Fatal("SetLineSpecial returned false")
	}
	if d.Level.Linedefs[0].SpecialType != 11 {
		t.Errorf("special = %d, want 11", d.Level.Linedefs[0].SpecialType)
	}

	if !d.SetLineTag(0, 3) {
		t.Fatal("SetLineTag returned false")
	}
	if d.Level.Linedefs[0].SectorTag != 3 {
		t.Errorf("tag = %d, want 3", d.Level.Linedefs[0].SectorTag)
	}

	if d.SetLineFlags(99, 0) || d.SetLineSpecial(99, 0) || d.SetLineTag(99, 0) {
		t.Error("out-of-range linedef index should return false")
	}
}

func TestMoveVertex(t *testing.T) {
	d := newTestDoc()
	d.geomDirty = false

	if !d.MoveVertex(0, 999, 888) {
		t.Fatal("MoveVertex returned false")
	}
	if d.Level.Vertexes[0].X != 999 || d.Level.Vertexes[0].Y != 888 {
		t.Errorf("vertex 0 = (%d,%d), want (999,888)", d.Level.Vertexes[0].X, d.Level.Vertexes[0].Y)
	}
	if !d.geomDirty {
		t.Error("MoveVertex should mark geometry dirty")
	}
	if d.MoveVertex(99, 0, 0) {
		t.Error("out-of-range vertex index should return false")
	}
}

// TestDeleteVertexReindexes exercises the actual point of DeleteVertex: a
// stray, unreferenced vertex inserted BEFORE the room's own four vertices
// (so every linedef's Start/EndVertex needs to shift down by one) must be
// removable, and removing it must restore the linedefs' original vertex
// indices exactly.
func TestDeleteVertexReindexes(t *testing.T) {
	d := newTestDoc()
	origVerts := append([]wad.Vertex(nil), d.Level.Vertexes...)
	origLines := append([]wad.Linedef(nil), d.Level.Linedefs...)

	// Referenced vertex: refuse.
	if d.DeleteVertex(0) {
		t.Fatal("DeleteVertex should refuse a vertex still referenced by a linedef")
	}

	// Prepend a stray vertex and shift every linedef's own vertex refs up
	// by one to match, exactly as if it had always been vertex 0.
	d.Level.Vertexes = append([]wad.Vertex{{X: 5000, Y: 5000}}, d.Level.Vertexes...)
	for i := range d.Level.Linedefs {
		d.Level.Linedefs[i].StartVertex++
		d.Level.Linedefs[i].EndVertex++
	}
	d.geomDirty = false

	if !d.DeleteVertex(0) {
		t.Fatal("DeleteVertex should succeed on an unreferenced vertex")
	}
	if !d.geomDirty {
		t.Error("DeleteVertex should mark geometry dirty")
	}
	if len(d.Level.Vertexes) != len(origVerts) {
		t.Fatalf("got %d vertices, want %d", len(d.Level.Vertexes), len(origVerts))
	}
	for i, v := range d.Level.Vertexes {
		if v != origVerts[i] {
			t.Errorf("vertex %d = %+v, want %+v", i, v, origVerts[i])
		}
	}
	for i, ld := range d.Level.Linedefs {
		if ld.StartVertex != origLines[i].StartVertex || ld.EndVertex != origLines[i].EndVertex {
			t.Errorf("linedef %d verts = %d->%d, want %d->%d",
				i, ld.StartVertex, ld.EndVertex, origLines[i].StartVertex, origLines[i].EndVertex)
		}
	}
}

// TestDeleteLinedefRemovesOwnSidedefAndReindexes checks that deleting a
// linedef also removes its own (one-sided) sidedef and shifts every other
// linedef's Front/BackSidedef reference down to track the slice shift.
func TestDeleteLinedefRemovesOwnSidedefAndReindexes(t *testing.T) {
	d := newTestDoc()
	if len(d.Level.Linedefs) != 4 || len(d.Level.Sidedefs) != 4 {
		t.Fatalf("expected NewEmptyDoc's 4-wall room, got %d linedefs / %d sidedefs",
			len(d.Level.Linedefs), len(d.Level.Sidedefs))
	}
	d.geomDirty = false

	if !d.DeleteLinedef(0) {
		t.Fatal("DeleteLinedef returned false")
	}
	if !d.geomDirty {
		t.Error("DeleteLinedef should mark geometry dirty")
	}
	if len(d.Level.Linedefs) != 3 {
		t.Fatalf("got %d linedefs, want 3", len(d.Level.Linedefs))
	}
	if len(d.Level.Sidedefs) != 3 {
		t.Fatalf("got %d sidedefs, want 3 (the deleted line's own sidedef should go with it)", len(d.Level.Sidedefs))
	}
	// The original linedefs 1,2,3 (now at 0,1,2) originally owned sidedefs
	// 1,2,3 — after sidedef 0's removal those should read 0,1,2.
	for i, want := range []uint16{0, 1, 2} {
		if d.Level.Linedefs[i].FrontSidedef != want {
			t.Errorf("linedef %d FrontSidedef = %d, want %d", i, d.Level.Linedefs[i].FrontSidedef, want)
		}
	}
	if d.DeleteLinedef(99) {
		t.Error("out-of-range linedef index should return false")
	}
}

// TestDeleteSectorRefusesThenReindexes checks the refuse-while-referenced
// guard, then a real deletion's Sidedef.Sector re-indexing once nothing
// references the sector anymore.
func TestDeleteSectorRefusesThenReindexes(t *testing.T) {
	d := newTestDoc()
	if d.DeleteSector(0) {
		t.Fatal("DeleteSector should refuse while sidedefs still reference it")
	}

	// A second sector with one sidedef of its own, unconnected to any
	// linedef — just to exercise the re-index math directly.
	d.Level.Sectors = append(d.Level.Sectors, wad.Sector{FloorHeight: 50})
	d.Level.Sidedefs = append(d.Level.Sidedefs, wad.Sidedef{Sector: 1})

	// Remove all four of the room's own linedefs (and, with them, their
	// sidedefs) so nothing references sector 0 anymore. Always delete
	// index 0 — each removal shifts the rest down.
	for len(d.Level.Linedefs) > 0 {
		if !d.DeleteLinedef(0) {
			t.Fatal("DeleteLinedef failed while clearing the room")
		}
	}
	if len(d.Level.Sidedefs) != 1 {
		t.Fatalf("expected only the synthetic sector-1 sidedef left, got %d", len(d.Level.Sidedefs))
	}

	d.geomDirty = false
	if !d.DeleteSector(0) {
		t.Fatal("DeleteSector should now succeed — nothing references it")
	}
	if !d.geomDirty {
		t.Error("DeleteSector should mark geometry dirty")
	}
	if len(d.Level.Sectors) != 1 {
		t.Fatalf("got %d sectors, want 1", len(d.Level.Sectors))
	}
	if d.Level.Sidedefs[0].Sector != 0 {
		t.Errorf("surviving sidedef's Sector = %d, want 0 (re-indexed down from 1)", d.Level.Sidedefs[0].Sector)
	}
}

func TestDeleteThing(t *testing.T) {
	d := newTestDoc()
	if len(d.Level.Things) != 1 {
		t.Fatalf("expected NewEmptyDoc's one player start, got %d things", len(d.Level.Things))
	}
	if !d.DeleteThing(0) {
		t.Fatal("DeleteThing returned false")
	}
	if len(d.Level.Things) != 0 {
		t.Errorf("got %d things, want 0", len(d.Level.Things))
	}
	if d.DeleteThing(0) {
		t.Error("deleting from an empty Things slice should return false")
	}
}

func TestMoveThing(t *testing.T) {
	d := newTestDoc()
	if !d.MoveThing(0, 123, -45) {
		t.Fatal("MoveThing returned false")
	}
	if d.Level.Things[0].X != 123 || d.Level.Things[0].Y != -45 {
		t.Errorf("thing 0 = (%d,%d), want (123,-45)", d.Level.Things[0].X, d.Level.Things[0].Y)
	}
	if d.MoveThing(99, 0, 0) {
		t.Error("out-of-range thing index should return false")
	}
}

// TestDeleteSelectionActsOnWhicheverKindIsSelected checks that
// DeleteSelection doesn't need to be told which kind is selected — it
// works whether a 2D-view or 3D-view pick left a Thing, a Vertex, or
// nothing at all selected.
func TestDeleteSelectionActsOnWhicheverKindIsSelected(t *testing.T) {
	d := newTestDoc()
	d.Sel.selectOne(modeThings, 0)
	d.DeleteSelection()
	if len(d.Level.Things) != 0 {
		t.Errorf("got %d things, want 0 after DeleteSelection with a thing selected", len(d.Level.Things))
	}

	d = newTestDoc()
	d.Sel.clearAll() // nothing selected — must be a safe no-op
	d.DeleteSelection()
	if len(d.Level.Vertexes) != 4 || len(d.Level.Linedefs) != 4 || len(d.Level.Sectors) != 1 {
		t.Error("DeleteSelection with nothing selected should not change the level")
	}
}

func TestSetSectorTilt(t *testing.T) {
	d := newTestDoc()
	d.geomDirty = false

	if !d.SetSectorTilt(0, 0.25, -0.1, 0.05, 0, 16, 32) {
		t.Fatal("SetSectorTilt returned false")
	}
	sec := d.Level.Sectors[0]
	if sec.FloorTiltX != 0.25 || sec.FloorTiltY != -0.1 || sec.CeilTiltX != 0.05 || sec.CeilTiltY != 0 ||
		sec.SlopePivotX != 16 || sec.SlopePivotY != 32 {
		t.Errorf("sector 0 tilt fields = %+v, want floorTilt(0.25,-0.1) ceilTilt(0.05,0) pivot(16,32)", sec)
	}
	if !d.geomDirty {
		t.Error("SetSectorTilt should mark geometry dirty (it changes the built planes)")
	}
	if d.SetSectorTilt(99, 0, 0, 0, 0, 0, 0) {
		t.Error("out-of-range sector index should return false")
	}
}

// addSecondSector appends a bare second sector (no geometry, just an entry
// in Level.Sectors) for tests that only need a second valid sector index to
// link against — mirrors TestDeleteSectorRefusesThenReindexes' own setup.
func addSecondSector(d *Doc, floor int16) int {
	d.Level.Sectors = append(d.Level.Sectors, wad.Sector{FloorHeight: floor})
	return len(d.Level.Sectors) - 1
}

func TestExtraFloorAddRemove(t *testing.T) {
	d := newTestDoc()
	control := addSecondSector(d, 50)
	d.geomDirty = false

	if d.AddExtraFloor(0, 0) {
		t.Error("AddExtraFloor should refuse a sector linking to itself")
	}
	if d.AddExtraFloor(0, 99) || d.AddExtraFloor(99, control) {
		t.Error("AddExtraFloor should refuse an out-of-range sector index")
	}
	if !d.AddExtraFloor(0, control) {
		t.Fatal("AddExtraFloor returned false")
	}
	if !d.geomDirty {
		t.Error("AddExtraFloor should mark geometry dirty")
	}
	got := d.ExtraFloorsFor(0)
	if len(got) != 1 || got[0].ControlSector != control {
		t.Fatalf("ExtraFloorsFor(0) = %+v, want one link to sector %d", got, control)
	}

	d.geomDirty = false
	if !d.RemoveExtraFloor(0, 0) {
		t.Fatal("RemoveExtraFloor returned false")
	}
	if !d.geomDirty {
		t.Error("RemoveExtraFloor should mark geometry dirty")
	}
	if len(d.ExtraFloorsFor(0)) != 0 {
		t.Error("expected no extra floors left on sector 0")
	}
	if d.RemoveExtraFloor(0, 0) {
		t.Error("removing from an empty extra-floor list should return false")
	}
}

// TestDeleteSectorCleansUpExtraFloors checks that deleting a sector drops
// any extra-floor link touching it (as target or control) and reindexes
// the survivors — the same reindexing DeleteSector already does for
// Sidedef.Sector.
func TestDeleteSectorCleansUpExtraFloors(t *testing.T) {
	d := newTestDoc()
	control := addSecondSector(d, 50)   // sector 1
	bystander := addSecondSector(d, 80) // sector 2, unrelated
	d.Level.Sidedefs = append(d.Level.Sidedefs, wad.Sidedef{Sector: uint16(bystander)})
	if !d.AddExtraFloor(0, control) {
		t.Fatal("setup: AddExtraFloor failed")
	}

	// Clear sector 0's own sidedefs so it can be deleted.
	for len(d.Level.Linedefs) > 0 {
		if !d.DeleteLinedef(0) {
			t.Fatal("setup: DeleteLinedef failed")
		}
	}
	if !d.DeleteSector(0) {
		t.Fatal("DeleteSector(0) failed")
	}
	// control (was 1) is now 0; bystander (was 2) is now 1. The
	// extra-floor link referenced sector 0 as its target, so it should be
	// gone entirely — not just reindexed, since its target no longer
	// exists.
	if len(d.ExtraFloorsFor(0)) != 0 {
		t.Errorf("expected the extra-floor link to be dropped with its target sector, got %+v", d.ExtraFloorsFor(0))
	}
	if d.Level.Sidedefs[0].Sector != 1 {
		t.Errorf("bystander sidedef's Sector = %d, want 1 (reindexed down from 2)", d.Level.Sidedefs[0].Sector)
	}
}

// TestSlopesRoundTrip checks that a tilted sector and a room-over-room
// link survive encodeSlopes -> slopes.Parse -> applySlopePlane exactly —
// the save/load symmetry the whole SLOPES feature depends on.
func TestSlopesRoundTrip(t *testing.T) {
	d := newTestDoc()
	control := addSecondSector(d, 50)
	if !d.SetSectorTilt(0, 0.25, -0.5, 0, 0, 16, 32) {
		t.Fatal("setup: SetSectorTilt failed")
	}
	if !d.AddExtraFloor(0, control) {
		t.Fatal("setup: AddExtraFloor failed")
	}

	text := d.encodeSlopes()
	if text == "" {
		t.Fatal("encodeSlopes returned empty text for a map with a tilt and an extra floor")
	}

	planes, floors, errs := slopes.Parse([]byte(text))
	for _, e := range errs {
		t.Errorf("slopes.Parse error: %v", e)
	}
	mapName := "MAP01" // NewEmptyDoc's fixed map name
	defs := planes[mapName]
	if len(defs) != 1 {
		t.Fatalf("got %d plane defs, want 1: %+v", len(defs), defs)
	}
	tiltX, tiltY, ok := planeTilt(defs[0].P1, defs[0].P2, defs[0].P3)
	if !ok {
		t.Fatal("re-parsed plane is degenerate")
	}
	if tiltX != 0.25 || tiltY != -0.5 || defs[0].P1[0] != 16 || defs[0].P1[1] != 32 || defs[0].P1[2] != 0 {
		t.Errorf("round-tripped plane = %+v (tilt %.3f,%.3f), want tilt (0.25,-0.5) pivot (16,32) height 0",
			defs[0], tiltX, tiltY)
	}

	fdefs := floors[mapName]
	if len(fdefs) != 1 || fdefs[0].TargetSector != 0 || fdefs[0].ControlSector != control {
		t.Errorf("got extra-floor defs %+v, want one {Target:0 Control:%d}", fdefs, control)
	}
}
