package launcher

import (
	"fmt"
	"testing"

	"twopointfive/window"
)

// openDropdown simulates a click on box to open id's dropdown and returns
// the resulting deferred state (nil if it didn't open).
func openDropdown(ui *UI, id string, row Rect, current string, options []string) *deferredDropdown {
	// Options' actual clickable control sits to the right of the label
	// column (see Options' box := ... offset by labelWidth), not at row's
	// own origin.
	ui.Begin(Input{
		MouseX: float64(row.X + labelWidth + 5), MouseY: float64(row.Y + 5),
		MouseLeftClicked: true, Keys: map[window.MenuKey]bool{},
	})
	ui.Options(id, row, "Level", current, options, func(string) {})
	return ui.deferred
}

// TestDropdownRowsStayFullHeightWithManyOptions is a regression test for the
// reported bug: with a WAD carrying a lot of maps, the level dropdown
// "squished" every row into a fixed-height box instead of showing a capped
// number of full-height rows with the rest reachable by scrolling. Each
// visible row must stay exactly rowHeight regardless of how many options
// there are.
func TestDropdownRowsStayFullHeightWithManyOptions(t *testing.T) {
	opts := make([]string, 40)
	for i := range opts {
		opts[i] = fmt.Sprintf("MAP%02d", i+1)
	}
	ui := NewUI(NewCanvas(windowW, windowH))
	box := Rect{100, 100, 300, rowHeight}
	d := openDropdown(ui, "map", box, opts[0], opts)
	if d == nil {
		t.Fatal("dropdown did not open")
	}
	if d.visible != dropdownMaxRows {
		t.Errorf("visible = %d, want the %d-row cap", d.visible, dropdownMaxRows)
	}
	wantH := dropdownMaxRows * rowHeight
	if d.list.H != wantH {
		t.Errorf("list.H = %d, want %d (%d rows * rowHeight — rows must not shrink to cram in all %d options)",
			d.list.H, wantH, dropdownMaxRows, len(opts))
	}
}

// TestDropdownFewOptionsNoScroll: with fewer options than the cap, every
// option shows at full height and nothing needs to scroll.
func TestDropdownFewOptionsNoScroll(t *testing.T) {
	opts := []string{"E1M1", "E1M2", "E1M3"}
	ui := NewUI(NewCanvas(windowW, windowH))
	box := Rect{100, 100, 300, rowHeight}
	d := openDropdown(ui, "map", box, opts[0], opts)
	if d == nil {
		t.Fatal("dropdown did not open")
	}
	if d.visible != len(opts) {
		t.Errorf("visible = %d, want %d (all of them)", d.visible, len(opts))
	}
	if d.list.H != len(opts)*rowHeight {
		t.Errorf("list.H = %d, want %d", d.list.H, len(opts)*rowHeight)
	}
}

// TestDropdownScrollRevealsLaterOptions: scrolling the open list moves
// which options are visible, and clicking a now-visible-but-not-originally
// row selects the right one (accounting for the scroll offset). Each
// "frame" below calls Begin/Options/End in that order, same as run.go's
// real loop calling Draw then (*UI).End once per frame — Options must be
// called every frame an open dropdown should keep showing, not just the
// frame that opened it.
func TestDropdownScrollRevealsLaterOptions(t *testing.T) {
	opts := make([]string, 20)
	for i := range opts {
		opts[i] = fmt.Sprintf("MAP%02d", i+1)
	}
	ui := NewUI(NewCanvas(windowW, windowH))
	row := Rect{100, 100, 300, rowHeight}
	var selected string
	write := func(s string) { selected = s }

	// Frame 1: click opens it.
	d := openDropdown(ui, "map", row, opts[0], opts)
	if d == nil {
		t.Fatal("dropdown did not open")
	}
	ui.End()

	// Frame 2: no click; scroll down 5 while hovering the list.
	ui.Begin(Input{MouseX: float64(d.list.X + 5), MouseY: float64(d.list.Y + 5), ScrollY: -5})
	ui.Options("map", row, "Level", opts[0], opts, write)
	ui.End()
	if got := ui.dropdownScroll["map"]; got != 5 {
		t.Fatalf("dropdownScroll = %d, want 5 after scrolling down 5", got)
	}

	// Frame 3: no click; just re-confirm the list persisted scrolled to 5,
	// then click its first visible row (which is now option[5], not [0]).
	ui.Begin(Input{})
	ui.Options("map", row, "Level", opts[0], opts, write)
	if ui.deferred == nil || ui.deferred.scroll != 5 {
		t.Fatalf("deferred.scroll = %+v, want 5 (persisted)", ui.deferred)
	}
	listX, listY := ui.deferred.list.X, ui.deferred.list.Y
	ui.End()

	ui.Begin(Input{MouseX: float64(listX + 5), MouseY: float64(listY + 5), MouseLeftClicked: true})
	ui.Options("map", row, "Level", opts[0], opts, write)
	if !ui.End() {
		t.Fatal("End() reported no change on a valid row click")
	}
	if selected != opts[5] {
		t.Errorf("selected %q, want %q (row 0 of a list scrolled to offset 5)", selected, opts[5])
	}
}
