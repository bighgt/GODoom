package worldgeo

import "testing"

func TestVisGridCellOfAndBoundsRoundTrip(t *testing.T) {
	g := newVisGrid(0, 0, 2000, 1000)
	if g.cols < 2 || g.rows < 1 {
		t.Fatalf("grid too small: cols=%d rows=%d", g.cols, g.rows)
	}

	// A point comfortably inside cell (0,0) must land in a cell whose own
	// bounds actually contain that point.
	x, y := float32(100), float32(100)
	c := g.cellOf(x, y)
	minX, minY, maxX, maxY := g.bounds(c)
	if x < minX || x > maxX || y < minY || y > maxY {
		t.Fatalf("cellOf(%v,%v) = %d, bounds (%v,%v)-(%v,%v) don't contain the point", x, y, c, minX, minY, maxX, maxY)
	}

	// Out-of-range points clamp to an edge cell rather than panicking or
	// returning a nonsense index.
	if c := g.cellOf(-999999, -999999); c < 0 || int(c) >= g.cols*g.rows {
		t.Errorf("cellOf far out of bounds = %d, want a valid clamped cell", c)
	}
	if c := g.cellOf(999999, 999999); c < 0 || int(c) >= g.cols*g.rows {
		t.Errorf("cellOf far out of bounds = %d, want a valid clamped cell", c)
	}
}

func TestCellVisibleCameraInsideCell(t *testing.T) {
	cam := Camera{X: 50, Y: 50, Angle: 0, FOVDeg: 90}
	if !cellVisible(cam, 0, 0, 100, 100) {
		t.Error("camera standing inside the cell should always be visible")
	}
}

func TestCellVisibleForwardVsBehind(t *testing.T) {
	// Camera at the origin looking down +X (Angle 0).
	cam := Camera{X: 0, Y: 0, Angle: 0, FOVDeg: 90}

	// A cell straight ahead must read visible.
	if !cellVisible(cam, 500, -50, 600, 50) {
		t.Error("a cell directly ahead of the camera should be visible")
	}

	// A cell directly behind the camera, well outside FOV+margin, must not.
	if cellVisible(cam, -600, -50, -500, 50) {
		t.Error("a cell directly behind the camera should not be visible")
	}
}

// TestUpdateVisibilityUsesDrawBoundsNotNominalCell reproduces the bug a
// wide outdoor subsector triggers (see cellOf's doc): its geometry is
// bucketed under one grid cell by its centroid, but a subsector wider than
// visGridCellSize (common in a map's big outdoor sectors — E1M2's courtyard
// has several) leaves that cell's own square nowhere near the subsector's
// actual footprint. UpdateVisibility must cull against the draw's own
// MinX/MinY/MaxX/MaxY, not the grid cell's nominal bounds, or geometry the
// camera is standing right in front of gets wrongly culled as "not visible"
// (reported as whole sections of a level rendering as flat black).
func TestUpdateVisibilityUsesDrawBoundsNotNominalCell(t *testing.T) {
	b := &Builder{grid: newVisGrid(-4096, -4096, 4096, 4096)}
	// This draw's real geometry sits right in front of the camera below, but
	// centroid-bucketing assigned it to a nominal cell far off to one side —
	// simulating a subsector whose footprint outgrows visGridCellSize.
	awayCell := b.grid.cellOf(-3000, -3000)
	b.g.StaticDraws = []Draw{{Count: 3, Cell: awayCell, MinX: 0, MinY: -200, MaxX: 800, MaxY: 200}}

	cam := Camera{X: 0, Y: 0, Angle: 0, FOVDeg: 90} // facing +X, straight at the draw's real bbox

	// Sanity: the draw's nominal cell (what the old, buggy code tested) is
	// indeed out of view — otherwise this test isn't isolating the fix.
	nomMinX, nomMinY, nomMaxX, nomMaxY := b.grid.bounds(awayCell)
	if cellVisible(cam, nomMinX, nomMinY, nomMaxX, nomMaxY) {
		t.Fatal("test setup invalid: the nominal cell is visible too, doesn't isolate the fix")
	}

	visD, _, _, _ := b.UpdateVisibility(cam)
	if visD != 1 {
		t.Fatalf("draw with real geometry in front of the camera was culled via its nominal cell: UpdateVisibility -> %d visible, want 1", visD)
	}
}

func TestUpdateVisibilityStatsAndArrayAgree(t *testing.T) {
	lvl, tree, tex := loadLevel(t)
	b := New(lvl, tree, tex)
	b.Build(0, 0) // populate StaticDraws via Invalidate (already run in New)

	cam := Camera{X: 1056, Y: -3616, Angle: 0, FOVDeg: 90} // E1M1 player start-ish
	visD, totD, visV, totV := b.UpdateVisibility(cam)

	if totD != len(b.g.StaticDraws) {
		t.Errorf("totalDraws = %d, want %d (len(StaticDraws))", totD, len(b.g.StaticDraws))
	}
	if visD < 0 || visD > totD {
		t.Errorf("visibleDraws = %d out of [0, %d]", visD, totD)
	}
	if visV < 0 || visV > totV {
		t.Errorf("visibleVerts = %d out of [0, %d]", visV, totV)
	}
	if visD == 0 {
		t.Error("nothing visible from the player start — camera/grid math is likely wrong")
	}

	// The returned counts must exactly match what StaticDrawVisible records
	// — render/vulkan/world.go's draw loop reads that array, not these
	// return values, so they'd better agree.
	if len(b.g.StaticDrawVisible) != totD {
		t.Fatalf("StaticDrawVisible has %d entries, want %d", len(b.g.StaticDrawVisible), totD)
	}
	gotVisD, gotVisV := 0, 0
	for i, v := range b.g.StaticDrawVisible {
		if v {
			gotVisD++
			gotVisV += b.g.StaticDraws[i].Count
		}
	}
	if gotVisD != visD || gotVisV != visV {
		t.Errorf("StaticDrawVisible implies %d draws/%d verts visible, UpdateVisibility returned %d/%d", gotVisD, gotVisV, visD, visV)
	}

	t.Logf("visibility: draws %d/%d visible, verts %d/%d visible", visD, totD, visV, totV)
}
