package imkit

import "image"

// Drag-and-drop. A DragSource starts carrying a payload once the mouse is
// pressed on it and moves a few pixels; a DropTarget claims the payload on
// release over its rect. The payload is any; callers type-assert it.

type dragState struct {
	payload any
	label   string
	thumb   *image.RGBA
	fromX   float64
	fromY   float64
	armed   bool // pressed but not yet moved past the threshold
}

const dragThreshold = 4.0

// DragSource makes hot a drag origin for payload. Returns true on the frame
// the drag actually begins (past the move threshold) — usually ignored.
// Also returns true from a plain click if it never became a drag, so a
// caller can use the same rect for select-on-click; check ui.Dragging().
func (ui *UI) DragSource(hot Rect, payload any, label string, thumb *image.RGBA) {
	if ui.blockInput {
		return
	}
	if ui.In.LeftClicked && hot.Contains(int(ui.In.MouseX), int(ui.In.MouseY)) && ui.drag == nil {
		ui.drag = &dragState{payload: payload, label: label, thumb: thumb,
			fromX: ui.In.MouseX, fromY: ui.In.MouseY, armed: true}
	}
	if ui.drag != nil && ui.drag.armed {
		dx := ui.In.MouseX - ui.drag.fromX
		dy := ui.In.MouseY - ui.drag.fromY
		if dx*dx+dy*dy >= dragThreshold*dragThreshold {
			ui.drag.armed = false
		}
	}
}

// Dragging reports the in-flight payload, if any (nil until the move
// threshold is passed). Use it for drop-target highlighting.
func (ui *UI) Dragging() (any, bool) {
	if ui.drag == nil || ui.drag.armed {
		return nil, false
	}
	return ui.drag.payload, true
}

// DropTarget claims an in-flight payload dropped (left-released) over r.
// Returns (payload, true) exactly once, on the release frame.
func (ui *UI) DropTarget(r Rect) (any, bool) {
	if ui.drag == nil || ui.drag.armed || !ui.In.LeftUp {
		return nil, false
	}
	if !r.Contains(int(ui.In.MouseX), int(ui.In.MouseY)) {
		return nil, false
	}
	p := ui.drag.payload
	ui.dropHit, ui.dropOK = p, true
	return p, true
}

func (ui *UI) drawDragGhost() {
	d := ui.drag
	if d == nil || d.armed {
		return
	}
	x, y := int(ui.In.MouseX)+12, int(ui.In.MouseY)+8
	if d.thumb != nil {
		b := d.thumb.Bounds()
		w, h := b.Dx(), b.Dy()
		if w > 48 {
			h = h * 48 / w
			w = 48
		}
		ui.C.Blit(Rect{x, y, w, h}, d.thumb)
		ui.C.StrokeRect(Rect{x, y, w, h}, Theme.Accent, 1)
		y += h + 2
	}
	if d.label != "" {
		w := TextWidth(d.label) + 8
		ui.C.FillRect(Rect{x, y, w, GlyphHeight + 4}, Theme.Panel)
		ui.C.StrokeRect(Rect{x, y, w, GlyphHeight + 4}, Theme.Accent, 1)
		ui.C.Text(x+4, y+(GlyphHeight+4)/2, d.label, Theme.Text)
	}
}
