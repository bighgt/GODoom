package editor

import (
	"fmt"
	"image"
	"path/filepath"
	"strings"

	"twopointfive/imkit"
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

	thingFilter string
	thingSel    int
	thingScroll int
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
	fileBtn := imkit.Rect{X: fr.X + fr.W + 4, Y: area.Y, W: btnW, H: 20}
	dirBtn := imkit.Rect{X: fileBtn.X + btnW + 4, Y: area.Y, W: btnW, H: 20}
	if ui.IconButton(fileBtn, "File", false) {
		p.pending = func() (string, bool) { return importTextureFile(bank, flat) }
	}
	if ui.IconButton(dirBtn, "Folder", false) {
		p.pending = func() (string, bool) { return importTextureFolder(bank, flat) }
	}

	items := make([]imkit.ThumbItem, 0, len(names))
	f := strings.ToUpper(strings.TrimSpace(*filter))
	for _, n := range names {
		if f != "" && !strings.Contains(n, f) {
			continue
		}
		items = append(items, imkit.ThumbItem{Name: n, Img: thumb(n), Payload: texPayload{name: n, flat: id == "flats"}})
	}
	grid := imkit.Rect{X: area.X, Y: area.Y + 26, W: area.W, H: area.H - 26}
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

func (p *panelState) drawInfo(ui *imkit.UI, area imkit.Rect, d *Doc) {
	lines := []string{
		"Map: " + d.MapName,
		itoa(len(d.Level.Vertexes)) + " verts   " + itoa(len(d.Level.Linedefs)) + " lines",
		itoa(len(d.Level.Sidedefs)) + " sides   " + itoa(len(d.Level.Sectors)) + " sectors",
		itoa(len(d.Level.Things)) + " things",
		"",
		"Selected: " + itoa(len(d.Sel.Verts)) + "v " + itoa(len(d.Sel.Lines)) + "l " +
			itoa(len(d.Sel.Sectors)) + "s " + itoa(len(d.Sel.Things)) + "t",
	}
	for i, ln := range lines {
		ui.C.Text(area.X, area.Y+10+i*18, ln, imkit.Theme.Text)
	}
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
}
type thingPayload struct{ num int }
