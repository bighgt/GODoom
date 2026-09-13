package imkit

import "image"

// ThumbItem is one cell of a ThumbGrid.
type ThumbItem struct {
	Name    string
	Img     *image.RGBA // may be nil (draws a placeholder)
	Payload any         // drag payload; defaults to Name when nil
}

// ThumbGrid draws items as a scrolling grid of labelled thumbnails inside
// area. cell is the target cell width in px (height is cell + a label row).
// Clicking a cell sets *selected and returns its name; each cell is also a
// DragSource. *scroll is the persistent vertical offset.
func (ui *UI) ThumbGrid(id string, area Rect, items []ThumbItem, cell int, selected *string, scroll *int) (clicked string) {
	if cell < 32 {
		cell = 32
	}
	labelH := GlyphHeight + 4
	cellH := cell + labelH
	// Reserve Scroll's scrollbar width up front, whether or not a scrollbar
	// ends up needed: computing cols from the full area.W here and then
	// narrowing after Scroll (once contentH is known) makes contentH and
	// cols disagree — more rows than contentH accounted for once the
	// scrollbar takes width, clipping the last row unreachable.
	const scrollbarW = 8
	cols := max((area.W-8-scrollbarW)/(cell+6), 1)
	rows := (len(items) + cols - 1) / cols
	contentH := rows*(cellH+6) + 8

	originY, _ := ui.Scroll(area, contentH, scroll)

	prev := ui.C.Clip(area)
	for i := range items {
		it := &items[i]
		cx := area.X + 6 + (i%cols)*(cell+6)
		cy := originY + 6 + (i/cols)*(cellH+6)
		if cy > area.Y+area.H || cy+cellH < area.Y {
			continue // offscreen row
		}
		cr := Rect{cx, cy, cell, cell}
		box := Rect{cx, cy, cell, cellH}
		hot := ui.hover(box)
		bg := Theme.PanelAlt
		switch {
		case it.Name == *selected:
			bg = Theme.AccentDim
		case hot:
			bg = Theme.ControlHover
		}
		ui.C.FillRect(box, bg)
		if it.Img != nil {
			b := it.Img.Bounds()
			iw, ih := b.Dx(), b.Dy()
			dw, dh := cell-4, cell-4
			if iw > 0 && ih > 0 {
				if iw*dh > ih*dw {
					dh = dw * ih / iw
				} else {
					dw = dh * iw / ih
				}
			}
			ui.C.Blit(Rect{cr.X + (cell-dw)/2, cr.Y + (cell-dw)/2, dw, dh}, it.Img)
		} else {
			ui.C.FillRect(cr.Inset(4), Theme.Control)
		}
		lr := Rect{cx, cy + cell, cell, labelH}
		nm := it.Name
		if TextWidth(nm) > cell-4 {
			for len(nm) > 1 && TextWidth(nm+"…") > cell-4 {
				nm = nm[:len(nm)-1]
			}
			nm += "…"
		}
		ui.C.TextCenter(lr, nm, Theme.Text)

		payload := it.Payload
		if payload == nil {
			payload = it.Name
		}
		ui.DragSource(box, payload, it.Name, it.Img)
		if hot && ui.In.LeftClicked {
			*selected = it.Name
			clicked = it.Name
		}
	}
	ui.C.SetClip(prev)
	return clicked
}

// FilterBox draws a one-line search field filling r and edits *q. Returns
// true on change.
func (ui *UI) FilterBox(id string, r Rect, q *string) bool {
	changed := ui.TextField(id, r, "", q)
	if *q == "" && ui.focusText != id {
		ui.C.Text(r.X+8, r.Y+r.H/2, "filter…", Theme.TextDim)
	}
	return changed
}
