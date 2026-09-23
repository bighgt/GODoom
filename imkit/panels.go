package imkit

// Layout / container widgets: a draggable splitter, a tab strip, and a
// vertical scroll region.

// SplitState is the persistent position of one Splitter, owned by the app.
type SplitState struct {
	// Pos is the split coordinate (x for a vertical divider, y for
	// horizontal), in canvas pixels. 0 => the app picks a default the first
	// frame.
	Pos      int
	dragging bool
}

// VSplit lays a draggable vertical divider inside area and returns the left
// and right sub-rects. handle is the divider's grab width. min keeps either
// side from collapsing past it.
func (ui *UI) VSplit(id string, area Rect, s *SplitState, handle, minSide int) (left, right Rect) {
	if s.Pos <= 0 {
		s.Pos = area.X + area.W/2
	}
	lo, hi := area.X+minSide, area.X+area.W-minSide-handle
	if s.Pos < lo {
		s.Pos = lo
	}
	if s.Pos > hi {
		s.Pos = hi
	}
	bar := Rect{s.Pos, area.Y, handle, area.H}
	if ui.hover(bar) && ui.In.LeftClicked {
		s.dragging = true
	}
	if s.dragging {
		if ui.In.LeftDown {
			s.Pos = int(ui.In.MouseX) - handle/2
		} else {
			s.dragging = false
		}
	}
	if s.Pos < lo {
		s.Pos = lo
	}
	if s.Pos > hi {
		s.Pos = hi
	}
	col := Theme.Border
	if ui.hover(bar) || s.dragging {
		col = Theme.Accent
	}
	ui.C.FillRect(Rect{s.Pos, area.Y, handle, area.H}, col)
	left = Rect{area.X, area.Y, s.Pos - area.X, area.H}
	right = Rect{s.Pos + handle, area.Y, area.X + area.W - s.Pos - handle, area.H}
	return
}

// Tabs draws a horizontal tab strip filling the top tabH pixels of area and
// returns the body rect below it. *active is the selected index, clamped.
func (ui *UI) Tabs(area Rect, tabH int, labels []string, active *int) Rect {
	if len(labels) == 0 {
		return area
	}
	if *active < 0 {
		*active = 0
	}
	if *active >= len(labels) {
		*active = len(labels) - 1
	}
	strip := Rect{area.X, area.Y, area.W, tabH}
	ui.C.FillRect(strip, Theme.Panel)
	ui.C.FillRect(Rect{strip.X, strip.Y + strip.H - 1, strip.W, 1}, Theme.Border)
	tw := area.W / len(labels)
	for i, lab := range labels {
		tr := Rect{area.X + i*tw, area.Y + 3, tw - 2, tabH - 4}
		if i == len(labels)-1 {
			tr.W = area.X + area.W - tr.X
		}
		bg := Theme.Panel
		if i == *active {
			bg = Theme.Control
		} else if ui.hover(tr) {
			bg = Theme.ControlHover
		}
		ui.C.FillRect(tr, bg)
		ui.C.TextCenter(tr, lab, Theme.Text)
		if ui.hover(tr) && ui.In.LeftClicked {
			*active = i
		}
	}
	return Rect{area.X, area.Y + tabH, area.W, area.H - tabH}
}

// Scroll is a vertical scroll region. Pass the viewport rect and the full
// content height; it returns the content-space Y offset to draw at (add it
// to each child's Y) and the inner width (viewport minus the scrollbar).
// Wrap child drawing in Clip(viewport)/SetClip. *off is the persistent
// scroll offset.
func (ui *UI) Scroll(viewport Rect, contentH int, off *int) (originY, innerW int) {
	const barW = 8
	maxOff := contentH - viewport.H
	if maxOff < 0 {
		maxOff = 0
	}
	if ui.MouseIn(viewport) && ui.In.ScrollY != 0 && !ui.blockInput {
		*off -= int(ui.In.ScrollY) * 32
	}
	if *off < 0 {
		*off = 0
	}
	if *off > maxOff {
		*off = maxOff
	}
	innerW = viewport.W
	if maxOff > 0 {
		innerW -= barW
		track := Rect{viewport.X + viewport.W - barW, viewport.Y, barW, viewport.H}
		ui.C.FillRect(track, Theme.Panel)
		th := max(viewport.H*viewport.H/contentH, 24)
		ty := viewport.Y + (*off)*(viewport.H-th)/maxOff
		ui.C.FillRect(Rect{track.X + 1, ty, barW - 2, th}, Theme.ScrollThumb)
	}
	return viewport.Y - *off, innerW
}
