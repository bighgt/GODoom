package editor

import (
	"os"
	"testing"

	"twopointfive/bsp"
	"twopointfive/wad"
)

func testBank(t *testing.T) *Bank {
	t.Helper()
	const path = "../testdata/DOOM1.WAD"
	if _, err := os.Stat(path); err != nil {
		t.Skipf("no %s on disk: %v", path, err)
	}
	w, err := wad.Load(path)
	if err != nil {
		t.Fatalf("wad.Load: %v", err)
	}
	b, err := LoadBank(w, path)
	if err != nil {
		t.Fatalf("LoadBank: %v", err)
	}
	return b
}

// TestNewEmptyDocBuildsAndFacesInward is a regression test for the winding
// bug: a New Level's one-sided walls must face INTO the room (Doom's front
// sidedef is the right side of start->end), or the 3D view renders solid
// black — every wall backface-culled from inside. Also checks the room
// actually resolves to its own sector and outside the room doesn't.
func TestNewEmptyDocBuildsAndFacesInward(t *testing.T) {
	bank := testBank(t)
	d := NewEmptyDoc(bank, "")

	if d.treeErr != nil {
		t.Fatalf("node build failed: %v", d.treeErr)
	}
	if d.tree == nil {
		t.Fatal("no BSP tree built")
	}
	if len(d.Level.Things) != 1 || d.Level.Things[0].Type != 1 {
		t.Fatalf("expected exactly one player-1 start, got %+v", d.Level.Things)
	}

	rl := d.RenderLevel()
	if inside := bsp.PointSectorIndex(d.tree, rl, 0, 0); inside != 0 {
		t.Errorf("centre of the room: sector = %d, want 0", inside)
	}

	// The real symptom of backwards winding: raster.renderSeg's world-space
	// backface cull (cross(seg, viewer) >= 0 => skip) culls every wall as
	// seen from inside the room, and the 3D view renders solid black. Check
	// the room centre is on each wall's FRONT (cross < 0) side directly,
	// with the same formula renderSeg uses.
	cx, cy := 0.0, 0.0
	for _, seg := range rl.Segs {
		if int(seg.StartVertex) >= len(rl.Vertexes) || int(seg.EndVertex) >= len(rl.Vertexes) {
			t.Fatalf("seg vertex index out of range: %+v", seg)
		}
		a := rl.Vertexes[seg.StartVertex]
		b := rl.Vertexes[seg.EndVertex]
		x1, y1, x2, y2 := float64(a.X), float64(a.Y), float64(b.X), float64(b.Y)
		cross := (x2-x1)*(cy-y1) - (y2-y1)*(cx-x1)
		if cross >= 0 {
			t.Errorf("wall (%.0f,%.0f)->(%.0f,%.0f) faces AWAY from the room centre (cross=%.0f) — one-sided walls face outward, 3D view would render black",
				x1, y1, x2, y2, cross)
		}
		_, fsec, _ := bsp.SegSides(rl, seg)
		if fsec == nil {
			t.Errorf("seg on linedef %d has no front sector", seg.Linedef)
		}
	}
}

// TestCommitRoomFacesInward drives the draw tool's commitRoom directly with
// a counter-clockwise draft (the winding a user's clicks could equally well
// produce) and checks the new sector's walls still face inward — the same
// regression as TestNewEmptyDocBuildsAndFacesInward, for the other room-
// creation path.
func TestCommitRoomFacesInward(t *testing.T) {
	bank := testBank(t)
	d := NewEmptyDoc(bank, "")
	var v view2d
	v.init(d)

	// A counter-clockwise square 600 units east of the starting room, away
	// from it so the two rooms don't touch.
	v.draft = []wad.Vertex{{X: 700, Y: -100}, {X: 900, Y: -100}, {X: 900, Y: 100}, {X: 700, Y: 100}}
	v.commitRoom(d, bank, &panelState{})

	secIdx := len(d.Level.Sectors) - 1
	if secIdx != 1 {
		t.Fatalf("expected a second sector, got %d sectors", len(d.Level.Sectors))
	}
	d.rebuildTree()
	if d.treeErr != nil {
		t.Fatalf("node build failed after commitRoom: %v", d.treeErr)
	}
	rl := d.RenderLevel()
	cx, cy := 800.0, 0.0 // centre of the new room
	for i, ld := range d.Level.Linedefs {
		if int(d.Level.Sidedefs[ld.FrontSidedef].Sector) != secIdx {
			continue // a wall of the original room
		}
		a := d.Level.Vertexes[ld.StartVertex]
		b := d.Level.Vertexes[ld.EndVertex]
		x1, y1, x2, y2 := float64(a.X), float64(a.Y), float64(b.X), float64(b.Y)
		if cross := (x2-x1)*(cy-y1) - (y2-y1)*(cx-x1); cross >= 0 {
			t.Errorf("drawn room wall %d (%.0f,%.0f)->(%.0f,%.0f) faces outward (cross=%.0f)", i, x1, y1, x2, y2, cross)
		}
	}
	if inside := bsp.PointSectorIndex(d.tree, rl, float32(cx), float32(cy)); inside != secIdx {
		t.Errorf("centre of drawn room: sector = %d, want %d", inside, secIdx)
	}
}

// TestPolyAreaWindingMatchesFrontSidedefConvention pins polyArea's sign
// convention against the same "front = right of start->end" rule: a
// clockwise square (front sidedefs facing in) must have negative area.
func TestPolyAreaWindingMatchesFrontSidedefConvention(t *testing.T) {
	cw := []wad.Vertex{{X: 0, Y: 0}, {X: 0, Y: 10}, {X: 10, Y: 10}, {X: 10, Y: 0}}
	if a := polyArea(cw); a >= 0 {
		t.Errorf("clockwise square area = %.1f, want negative", a)
	}
	ccw := []wad.Vertex{{X: 0, Y: 0}, {X: 10, Y: 0}, {X: 10, Y: 10}, {X: 0, Y: 10}}
	if a := polyArea(ccw); a <= 0 {
		t.Errorf("counter-clockwise square area = %.1f, want positive", a)
	}
}
