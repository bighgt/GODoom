package imkit

// A top menu bar with click-to-open popups, and a right-click context menu.
// Popups are drawn immediately; while one is open its rectangle is recorded
// in ui.menuBlock so hover() ignores widgets beneath it (same trick as the
// dropdown overlay). Selections resolve the same frame.

// MenuItem is one popup row. An empty Label is a separator.
type MenuItem struct {
	Label    string
	Shortcut string // right-aligned hint, e.g. "Ctrl+S"
	Disabled bool
	Checked  bool
}

func (ui *UI) menuRowH() int { return GlyphHeight + 10 }

// BeginMenuBar draws the bar across the top menuH px of area and returns the
// body rect below it. Follow with Menu(...) calls, then the rest of the UI.
func (ui *UI) BeginMenuBar(area Rect, menuH int) Rect {
	ui.C.FillRect(Rect{area.X, area.Y, area.W, menuH}, Theme.Panel)
	ui.C.FillRect(Rect{area.X, area.Y + menuH - 1, area.W, 1}, Theme.Border)
	ui.menuBar = Rect{area.X, area.Y, area.W, menuH}
	ui.menuX = area.X + 2
	return Rect{area.X, area.Y + menuH, area.W, area.H - menuH}
}

// Menu draws one top-level title. If its popup is open and an item is
// clicked, returns that item's index; else -1.
func (ui *UI) Menu(title string, items []MenuItem) int {
	w := TextWidth(title) + 20
	r := Rect{ui.menuX, ui.menuBar.Y, w, ui.menuBar.H}
	ui.menuX += w
	open := ui.openMenu == title
	hot := r.Contains(int(ui.In.MouseX), int(ui.In.MouseY)) && !ui.blockInput
	bg := Theme.Panel
	if open || hot {
		bg = Theme.ControlHover
	}
	ui.C.FillRect(r, bg)
	ui.C.TextCenter(r, title, Theme.Text)

	if hot && ui.In.LeftClicked {
		if open {
			ui.openMenu = ""
		} else {
			ui.openMenu = title
		}
		open = ui.openMenu == title
	} else if ui.openMenu != "" && !open && hot {
		ui.openMenu, open = title, true // slide between menus while the bar is live
	}
	if !open {
		return -1
	}
	return ui.popup("menu:"+title, Rect{r.X, r.Y + r.H, 0, 0}, items, func() { ui.openMenu = "" })
}

// ContextMenu opens a popup at the mouse when trigger is true (a right-click
// in a pane). Returns the chosen index, or -1.
func (ui *UI) ContextMenu(id string, trigger bool, items []MenuItem) int {
	key := "ctx:" + id
	if trigger {
		ui.openMenu = key
		ui.ctxAt = Rect{int(ui.In.MouseX), int(ui.In.MouseY), 0, 0}
	}
	if ui.openMenu != key {
		return -1
	}
	return ui.popup(key, ui.ctxAt, items, func() { ui.openMenu = "" })
}

// popup lays out a popup whose top-left is anchor.{X,Y}, records it for End
// to draw on top of everything, resolves the click, and calls close when it
// should dismiss. Returns the clicked index or -1.
func (ui *UI) popup(id string, anchor Rect, items []MenuItem, close func()) int {
	mw := 150
	for _, it := range items {
		if lw := TextWidth(it.Label) + TextWidth(it.Shortcut) + 44; lw > mw {
			mw = lw
		}
	}
	rh := ui.menuRowH()
	mr := Rect{anchor.X, anchor.Y, mw, len(items)*rh + 4}
	if mr.X+mr.W > ui.C.W {
		mr.X = ui.C.W - mr.W
	}
	if mr.Y+mr.H > ui.C.H {
		mr.Y = ui.C.H - mr.H
	}
	ui.menuBlock = mr                        // hover() ignores widgets under here
	ui.pendingMenu = &pendingMenu{mr, items} // End draws it on top

	chosen := -1
	for i, it := range items {
		if it.Label == "" || it.Disabled {
			continue
		}
		row := Rect{mr.X, mr.Y + 2 + i*rh, mr.W, rh}
		if row.Contains(int(ui.In.MouseX), int(ui.In.MouseY)) && ui.In.LeftClicked {
			chosen = i
		}
	}
	outside := ui.In.LeftClicked && !mr.Contains(int(ui.In.MouseX), int(ui.In.MouseY)) &&
		!ui.menuBar.Contains(int(ui.In.MouseX), int(ui.In.MouseY))
	if chosen >= 0 || outside || ui.In.Edge(KeyEscape) {
		close()
	}
	return chosen
}

type pendingMenu struct {
	rect  Rect
	items []MenuItem
}

func (ui *UI) drawPendingMenu() {
	m := ui.pendingMenu
	if m == nil {
		return
	}
	rh := ui.menuRowH()
	ui.C.FillRect(m.rect, Theme.Panel)
	ui.C.StrokeRect(m.rect, Theme.Border, 1)
	for i, it := range m.items {
		row := Rect{m.rect.X, m.rect.Y + 2 + i*rh, m.rect.W, rh}
		if it.Label == "" {
			ui.C.FillRect(Rect{row.X + 6, row.Y + rh/2, row.W - 12, 1}, Theme.Border)
			continue
		}
		if !it.Disabled && row.Contains(int(ui.In.MouseX), int(ui.In.MouseY)) {
			ui.C.FillRect(row, Theme.ControlHover)
		}
		col := Theme.Text
		if it.Disabled {
			col = Theme.TextDim
		}
		if it.Checked {
			ui.C.Text(row.X+6, row.Y+row.H/2, "•", Theme.Accent)
		}
		ui.C.Text(row.X+18, row.Y+row.H/2, it.Label, col)
		if it.Shortcut != "" {
			ui.C.Text(row.X+row.W-TextWidth(it.Shortcut)-10, row.Y+row.H/2, it.Shortcut, Theme.TextDim)
		}
	}
	ui.pendingMenu = nil
}
