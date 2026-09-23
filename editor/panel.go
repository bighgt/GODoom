package editor

import (
	"fmt"
	"image"
	"path/filepath"
	"strconv"
	"strings"

	"twopointfive/imkit"
	"twopointfive/wad"
	"twopointfive/window"
)

// panelState is the right-docked tabbed panel: texture / flat browsers, the
// thing palette, and an inspector for the current selection.
type panelState struct {
	tab int

	// pending is a deferred action (a native file/folder dialog, which must
	// run after the frame presents — see App.pending's own doc comment)
	// that reports a status-bar message when it actually did something,
	// e.g. imported a texture. false from the bool means "say nothing"
	// (the dialog was cancelled) rather than blanking the status bar.
	pending func() (msg string, ok bool)

	wallFilter string
	wallSel    string
	wallScroll int

	flatFilter string
	flatSel    string
	flatScroll int
	// flatCeil selects which of a sector's two flats a Flats-tab drag
	// targets — see the Floor/Ceiling toggle drawTexBrowser draws above the
	// flats grid. false (Floor) by default, matching SetSectorFlat's own
	// default.
	flatCeil bool

	thingFilter string
	thingSel    int
	thingScroll int

	// floatDrafts backs floatField's own per-field edit buffers — imkit
	// only ships IntField (int), not a float version, so floatField
	// reimplements its exact "draft buffer, only re-formatted from the
	// canonical value while not focused" shape locally using imkit.Focused.
	floatDrafts map[string]string

	// addExtraFloorTarget/Control hold the sector inspector's pending "add
	// a room-over-room link" row — see drawSectorInspector.
	addExtraFloorControl int
}

// floatField is IntField's float64 counterpart — imkit doesn't ship one.
// Mirrors IntField's own draft-buffer logic exactly (see its doc comment),
// using panelState's own floatDrafts map and imkit.UI.Focused in place of
// imkit's private equivalents.
func (p *panelState) floatField(ui *imkit.UI, id string, r imkit.Rect, label string, v *float64) bool {
	if p.floatDrafts == nil {
		p.floatDrafts = map[string]string{}
	}
	if !ui.Focused(id) {
		p.floatDrafts[id] = strconv.FormatFloat(*v, 'g', -1, 64)
	}
	s := p.floatDrafts[id]
	ui.TextField(id, r, label, &s)
	p.floatDrafts[id] = s
	if n, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil && n != *v {
		*v = n
		return true
	}
	return false
}

func (p *panelState) init() {
	p.tab = 0
}

var panelTabs = []string{"Walls", "Flats", "Things", "Info"}

func (p *panelState) draw(ui *imkit.UI, area imkit.Rect, d *Doc, bank *Bank) {
	c := ui.C
	c.FillRect(area, imkit.Theme.Panel)
	body := ui.Tabs(area, 24, panelTabs, &p.tab)
	pad := imkit.Rect{X: body.X + 6, Y: body.Y + 6, W: body.W - 12, H: body.H - 12}

	switch p.tab {
	case 0:
		p.drawTexBrowser(ui, pad, bank, "walls", &p.wallFilter, &p.wallSel, &p.wallScroll, bank.Walls, bank.WallThumb)
	case 1:
		p.drawTexBrowser(ui, pad, bank, "flats", &p.flatFilter, &p.flatSel, &p.flatScroll, bank.Flats, bank.FlatThumb)
	case 2:
		p.drawThingPalette(ui, pad, bank)
	case 3:
		p.drawInfo(ui, pad, d)
	}
}

func (p *panelState) drawTexBrowser(ui *imkit.UI, area imkit.Rect, bank *Bank, id string,
	filter, sel *string, scroll *int, names []string, thumb func(string) *image.RGBA) {

	const btnW = 44
	fr := imkit.Rect{X: area.X, Y: area.Y, W: area.W - 2*(btnW+4), H: 20}
	ui.FilterBox(id+"-filter", fr, filter)

	flat := id == "flats"
	kind := "wall texture"
	if flat {
		kind = "flat"
	}
	fileBtn := imkit.Rect{X: fr.X + fr.W + 4, Y: area.Y, W: btnW, H: 20}
	dirBtn := imkit.Rect{X: fileBtn.X + btnW + 4, Y: area.Y, W: btnW, H: 20}
	if ui.IconButton(fileBtn, "File", false) {
		p.pending = func() (string, bool) { return importTextureFile(bank, flat) }
	}
	ui.Tooltip(fileBtn, "Import a single "+kind+" PNG file")
	if ui.IconButton(dirBtn, "Folder", false) {
		p.pending = func() (string, bool) { return importTextureFolder(bank, flat) }
	}
	ui.Tooltip(dirBtn, "Import every PNG in a folder (recursively) as "+kind+"s")

	// Floor/Ceiling toggle: which of a sector's two flats a drag from this
	// browser targets (see texPayload.ceil, read by view2d's handleDrop) —
	// SetSectorFlat has always supported a ceiling target, nothing in the
	// UI could reach it until now.
	gridY := area.Y + 26
	if flat {
		toggleY := area.Y + 26
		half := (area.W - 4) / 2
		floorBtn := imkit.Rect{X: area.X, Y: toggleY, W: half, H: 22}
		ceilBtn := imkit.Rect{X: area.X + half + 4, Y: toggleY, W: half, H: 22}
		if ui.IconButton(floorBtn, "Floor", !p.flatCeil) {
			p.flatCeil = false
		}
		ui.Tooltip(floorBtn, "Dragging a flat onto a sector will set its FLOOR texture")
		if ui.IconButton(ceilBtn, "Ceiling", p.flatCeil) {
			p.flatCeil = true
		}
		ui.Tooltip(ceilBtn, "Dragging a flat onto a sector will set its CEILING texture")
		gridY = toggleY + 22 + 4
	}

	items := make([]imkit.ThumbItem, 0, len(names))
	f := strings.ToUpper(strings.TrimSpace(*filter))
	for _, n := range names {
		if f != "" && !strings.Contains(n, f) {
			continue
		}
		items = append(items, imkit.ThumbItem{Name: n, Img: thumb(n), Payload: texPayload{name: n, flat: flat, ceil: flat && p.flatCeil}})
	}
	grid := imkit.Rect{X: area.X, Y: gridY, W: area.W, H: area.Y + area.H - gridY}
	ui.ThumbGrid(id, grid, items, 64, sel, scroll)
}

func (p *panelState) drawThingPalette(ui *imkit.UI, area imkit.Rect, bank *Bank) {
	fr := imkit.Rect{X: area.X, Y: area.Y, W: area.W, H: 20}
	ui.FilterBox("thing-filter", fr, &p.thingFilter)

	list := imkit.Rect{X: area.X, Y: area.Y + 26, W: area.W, H: area.H - 26}
	f := strings.ToLower(strings.TrimSpace(p.thingFilter))
	rowH := 20
	// collect filtered
	rows := make([]ThingDef, 0, len(thingDefs))
	for _, td := range thingDefs {
		if f != "" && !strings.Contains(strings.ToLower(td.Name), f) {
			continue
		}
		rows = append(rows, td)
	}
	originY, _ := ui.Scroll(list, len(rows)*rowH+8, &p.thingScroll)
	prev := ui.C.Clip(list)
	for i, td := range rows {
		r := imkit.Rect{X: list.X, Y: originY + 4 + i*rowH, W: list.W, H: rowH}
		if r.Y+rowH < list.Y || r.Y > list.Y+list.H {
			continue
		}
		hot := ui.MouseIn(r)
		if td.Num == p.thingSel {
			ui.C.FillRect(r, imkit.Theme.AccentDim)
		} else if hot {
			ui.C.FillRect(r, imkit.Theme.ControlHover)
		}
		sw := imkit.Rect{X: r.X + 2, Y: r.Y + 3, W: 14, H: 14}
		ui.C.FillRect(sw, td.Cat.color())
		ui.C.Text(r.X+22, r.Y+r.H/2, td.Name, imkit.Theme.Text)
		ui.DragSource(r, thingPayload{num: td.Num}, td.Name, nil)
		if hot && ui.In.LeftClicked {
			p.thingSel = td.Num
		}
	}
	ui.C.SetClip(prev)
}

// drawInfo is the Info tab: a property inspector when exactly one sector or
// linedef is selected (the only two element kinds with editable properties
// beyond texture assignment — see Doc's SetSector*/SetLine* methods), or
// the map/selection summary otherwise.
func (p *panelState) drawInfo(ui *imkit.UI, area imkit.Rect, d *Doc) {
	onlySector := len(d.Sel.Sectors) == 1 && len(d.Sel.Lines) == 0 && len(d.Sel.Verts) == 0 && len(d.Sel.Things) == 0
	onlyLine := len(d.Sel.Lines) == 1 && len(d.Sel.Sectors) == 0 && len(d.Sel.Verts) == 0 && len(d.Sel.Things) == 0
	switch {
	case onlySector:
		for idx := range d.Sel.Sectors {
			p.drawSectorInspector(ui, area, d, idx)
		}
		return
	case onlyLine:
		for idx := range d.Sel.Lines {
			p.drawLineInspector(ui, area, d, idx)
		}
		return
	}

	lines := []string{
		"Map: " + d.MapName,
		itoa(len(d.Level.Vertexes)) + " verts   " + itoa(len(d.Level.Linedefs)) + " lines",
		itoa(len(d.Level.Sidedefs)) + " sides   " + itoa(len(d.Level.Sectors)) + " sectors",
		itoa(len(d.Level.Things)) + " things",
		"",
		"Selected: " + itoa(len(d.Sel.Verts)) + "v " + itoa(len(d.Sel.Lines)) + "l " +
			itoa(len(d.Sel.Sectors)) + "s " + itoa(len(d.Sel.Things)) + "t",
		"",
		"Select a single sector or linedef to edit its properties here.",
	}
	for i, ln := range lines {
		ui.C.Text(area.X, area.Y+10+i*18, ln, imkit.Theme.Text)
	}
}

// inspectorRows lays out successive fixed-height rows down from area.Y —
// shared by drawSectorInspector/drawLineInspector.
type inspectorRows struct {
	area imkit.Rect
	y    int
}

func newInspectorRows(area imkit.Rect) *inspectorRows { return &inspectorRows{area: area, y: area.Y} }

func (r *inspectorRows) next() imkit.Rect {
	row := imkit.Rect{X: r.area.X, Y: r.y, W: r.area.W, H: 22}
	r.y += 26
	return row
}

// drawSectorInspector shows and edits sector idx's height/light/special/tag
// — the properties Doom mapping needs beyond flat assignment (already
// handled by drag-and-drop from the Flats tab), none of which had any UI
// before this.
func (p *panelState) drawSectorInspector(ui *imkit.UI, area imkit.Rect, d *Doc, idx int) {
	if idx < 0 || idx >= len(d.Level.Sectors) {
		return
	}
	sec := &d.Level.Sectors[idx]
	ui.C.Text(area.X, area.Y+10, fmt.Sprintf("Sector %d", idx), imkit.Theme.Text)
	rows := newInspectorRows(imkit.Rect{X: area.X, Y: area.Y + 24, W: area.W, H: area.H - 24})

	floor := int(sec.FloorHeight)
	if ui.IntField("sec-floor", rows.next(), "Floor Z", &floor) {
		d.SetSectorHeights(idx, int16(floor), sec.CeilingHeight)
	}
	ceiling := int(sec.CeilingHeight)
	if ui.IntField("sec-ceil", rows.next(), "Ceiling Z", &ceiling) {
		d.SetSectorHeights(idx, sec.FloorHeight, int16(ceiling))
	}
	light := int(sec.LightLevel)
	if ui.IntField("sec-light", rows.next(), "Light", &light) {
		d.SetSectorLight(idx, int16(light))
	}
	special := int(sec.SpecialType)
	if ui.IntField("sec-special", rows.next(), "Special", &special) {
		d.SetSectorSpecial(idx, uint16(special))
	}
	tag := int(sec.Tag)
	if ui.IntField("sec-tag", rows.next(), "Tag", &tag) {
		d.SetSectorTag(idx, uint16(tag))
	}
	drawHint(ui, rows.next(), "Special/Tag: raw Doom sector-behavior and linedef-trigger numbers.")

	drawSectionHeader(ui, rows.next(), "Slope")
	drawHint(ui, rows.next(), "Tilt = rise per map unit from the pivot. 0/0 on both = flat.")
	pivotX, pivotY := sec.SlopePivotX, sec.SlopePivotY
	if p.floatField(ui, "sec-pivotx", rows.next(), "Pivot X", &pivotX) {
		d.SetSectorTilt(idx, sec.FloorTiltX, sec.FloorTiltY, sec.CeilTiltX, sec.CeilTiltY, pivotX, sec.SlopePivotY)
	}
	if p.floatField(ui, "sec-pivoty", rows.next(), "Pivot Y", &pivotY) {
		d.SetSectorTilt(idx, sec.FloorTiltX, sec.FloorTiltY, sec.CeilTiltX, sec.CeilTiltY, sec.SlopePivotX, pivotY)
	}
	floorTiltX, floorTiltY := sec.FloorTiltX, sec.FloorTiltY
	if p.floatField(ui, "sec-ftiltx", rows.next(), "Floor Tilt X", &floorTiltX) {
		d.SetSectorTilt(idx, floorTiltX, sec.FloorTiltY, sec.CeilTiltX, sec.CeilTiltY, sec.SlopePivotX, sec.SlopePivotY)
	}
	if p.floatField(ui, "sec-ftilty", rows.next(), "Floor Tilt Y", &floorTiltY) {
		d.SetSectorTilt(idx, sec.FloorTiltX, floorTiltY, sec.CeilTiltX, sec.CeilTiltY, sec.SlopePivotX, sec.SlopePivotY)
	}
	ceilTiltX, ceilTiltY := sec.CeilTiltX, sec.CeilTiltY
	if p.floatField(ui, "sec-ctiltx", rows.next(), "Ceil Tilt X", &ceilTiltX) {
		d.SetSectorTilt(idx, sec.FloorTiltX, sec.FloorTiltY, ceilTiltX, sec.CeilTiltY, sec.SlopePivotX, sec.SlopePivotY)
	}
	if p.floatField(ui, "sec-ctilty", rows.next(), "Ceil Tilt Y", &ceilTiltY) {
		d.SetSectorTilt(idx, sec.FloorTiltX, sec.FloorTiltY, sec.CeilTiltX, ceilTiltY, sec.SlopePivotX, sec.SlopePivotY)
	}

	drawSectionHeader(ui, rows.next(), "Extra Floors (room-over-room)")
	drawHint(ui, rows.next(), "Link a control sector below — only ITS OWN heights matter,")
	drawHint(ui, rows.next(), "not where it sits on the map — put it anywhere convenient.")
	for i, ef := range d.ExtraFloorsFor(idx) {
		r := rows.next()
		ui.C.Text(r.X, r.Y+r.H/2, fmt.Sprintf("Control sector %d", ef.ControlSector), imkit.Theme.Text)
		removeBtn := imkit.Rect{X: r.X + r.W - 64, Y: r.Y, W: 64, H: r.H}
		if ui.IconButton(removeBtn, "Remove", false) {
			d.RemoveExtraFloor(idx, i)
		}
		ui.Tooltip(removeBtn, "Remove this room-over-room link")
	}
	addRow := rows.next()
	ui.IntField("sec-addef", imkit.Rect{X: addRow.X, Y: addRow.Y, W: addRow.W - 64, H: addRow.H}, "Control #", &p.addExtraFloorControl)
	addBtn := imkit.Rect{X: addRow.X + addRow.W - 60, Y: addRow.Y, W: 60, H: addRow.H}
	if ui.IconButton(addBtn, "Add", false) {
		d.AddExtraFloor(idx, p.addExtraFloorControl)
	}
	ui.Tooltip(addBtn, "Link the sector number entered here as a floating platform inside this one")
}

// drawSectionHeader draws a bright sub-heading inside row r, for grouping
// the inspector's own fields (Slope, Extra Floors, ...) — brighter than
// drawHint's own dim explanatory text so the two are visually distinct.
func drawSectionHeader(ui *imkit.UI, r imkit.Rect, text string) {
	ui.C.Text(r.X, r.Y+r.H/2, text, imkit.Theme.Text)
}

// drawHint draws a short explanatory line inside row r — brief in-app help
// next to fields whose meaning isn't obvious from the label alone.
func drawHint(ui *imkit.UI, r imkit.Rect, text string) {
	ui.C.Text(r.X, r.Y+r.H/2, text, imkit.Theme.TextDim)
}

// drawLineInspector shows and edits linedef idx's flags/special/tag.
// upperUnpegged/lowerUnpegged/blockSound are checkboxes for the three flag
// bits this codebase already names (wad.Linedef*); every other classic
// Doom bit (blocking, two-sided, secret, ...) is reachable through the raw
// "Flags" field instead of inventing more named constants than the engine
// itself currently uses.
func (p *panelState) drawLineInspector(ui *imkit.UI, area imkit.Rect, d *Doc, idx int) {
	if idx < 0 || idx >= len(d.Level.Linedefs) {
		return
	}
	ld := &d.Level.Linedefs[idx]
	ui.C.Text(area.X, area.Y+10, fmt.Sprintf("Linedef %d", idx), imkit.Theme.Text)
	rows := newInspectorRows(imkit.Rect{X: area.X, Y: area.Y + 24, W: area.W, H: area.H - 24})

	flags := int(ld.Flags)
	if ui.IntField("line-flags", rows.next(), "Flags", &flags) {
		d.SetLineFlags(idx, uint16(flags))
	}
	drawHint(ui, rows.next(), "Flags is the raw bitfield; blocking/two-sided/secret/etc.")
	drawHint(ui, rows.next(), "are set there too — checkboxes below cover the named ones.")
	upperUnpegged := ld.Flags&wad.LinedefUpperUnpegged != 0
	if ui.Checkbox(rows.next(), "Upper Unpegged", &upperUnpegged) {
		d.SetLineFlags(idx, setBit(ld.Flags, wad.LinedefUpperUnpegged, upperUnpegged))
	}
	lowerUnpegged := ld.Flags&wad.LinedefLowerUnpegged != 0
	if ui.Checkbox(rows.next(), "Lower Unpegged", &lowerUnpegged) {
		d.SetLineFlags(idx, setBit(ld.Flags, wad.LinedefLowerUnpegged, lowerUnpegged))
	}
	blockSound := ld.Flags&wad.LinedefBlockSound != 0
	if ui.Checkbox(rows.next(), "Block Sound", &blockSound) {
		d.SetLineFlags(idx, setBit(ld.Flags, wad.LinedefBlockSound, blockSound))
	}
	special := int(ld.SpecialType)
	if ui.IntField("line-special", rows.next(), "Special", &special) {
		d.SetLineSpecial(idx, uint16(special))
	}
	tag := int(ld.SectorTag)
	if ui.IntField("line-tag", rows.next(), "Tag", &tag) {
		d.SetLineTag(idx, uint16(tag))
	}
	drawHint(ui, rows.next(), "Special/Tag: raw Doom linedef-trigger and target-sector numbers.")
}

// setBit returns flags with bit set or cleared according to on.
func setBit(flags, bit uint16, on bool) uint16 {
	if on {
		return flags | bit
	}
	return flags &^ bit
}

// ---- texture import (File/Folder buttons on the Walls/Flats browsers) ----

// importTextureFile pops a native "open file" dialog (blocking — run from
// panelState.pending, after the frame presents) and imports the chosen PNG
// as a wall texture, or a flat if flat is true.
func importTextureFile(bank *Bank, flat bool) (string, bool) {
	path, err := window.PickImageFile()
	if err != nil {
		if err == window.ErrPickCancelled {
			return "", false
		}
		return "import: " + err.Error(), true
	}
	var name string
	if flat {
		name, err = bank.ImportFlat(path)
	} else {
		name, err = bank.ImportWallTexture(path)
	}
	if err != nil {
		return "import " + name + ": " + err.Error(), true
	}
	kind := "wall texture"
	if flat {
		kind = "flat"
	}
	return fmt.Sprintf("imported %s %s from %s", kind, name, filepath.Base(path)), true
}

// importTextureFolder pops a native "choose folder" dialog and imports
// every *.png found recursively under it, as wall textures or (flat)
// flats.
func importTextureFolder(bank *Bank, flat bool) (string, bool) {
	dir, err := window.PickFolder()
	if err != nil {
		if err == window.ErrPickCancelled {
			return "", false
		}
		return "import: " + err.Error(), true
	}
	n, ferr := bank.ImportFolder(dir, flat)
	kind := "wall texture"
	if flat {
		kind = "flat"
	}
	msg := fmt.Sprintf("imported %d %s(s) from %s", n, kind, dir)
	if ferr != nil {
		msg += "  (" + ferr.Error() + ")"
	}
	return msg, true
}

// payload types carried by drags out of the panel.
type texPayload struct {
	name string
	flat bool
	// ceil is only meaningful when flat is true — which of a sector's two
	// flats this drag targets (see the Floor/Ceiling toggle in
	// drawTexBrowser and view2d's handleDrop).
	ceil bool
}
type thingPayload struct{ num int }
