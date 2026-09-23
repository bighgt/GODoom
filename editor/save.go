package editor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"twopointfive/nodebuild"
	"twopointfive/wad"
)

// SavePWAD writes the doc's map to path as a single-map PWAD the engine can
// load: the raw geometry lumps plus a freshly built BSP (SEGS / SSECTORS /
// NODES) and a generated BLOCKMAP / empty REJECT. The marker lump keeps the
// map's name so `engine file.wad` runs straight into it. A SLOPES lump
// (see encodeSlopes) is appended too, if this map has any tilted sectors
// or room-over-room links — so a map round-trips through this editor with
// those intact, instead of the tilt fields silently being dropped on save
// the way plain wad.EncodeSectors always has.
func (d *Doc) SavePWAD(path string) error {
	res, err := nodebuild.Build(d.Level)
	if err != nil {
		return fmt.Errorf("node build: %w", err)
	}
	save := *d.Level
	save.Vertexes = res.Verts
	segs, ssectors, nodes := res.Lumps()
	lumps := wad.MapMarkerLumps(d.MapName, &save, segs, ssectors, nodes,
		nodebuild.Reject(len(d.Level.Sectors)), nodebuild.Blockmap(&save))
	if text := d.encodeSlopes(); text != "" {
		lumps = append(lumps, wad.NamedLump{Name: "SLOPES", Data: []byte(text)})
	}
	if err := wad.WritePWAD(path, lumps); err != nil {
		return err
	}
	return nil
}

// encodeSlopes serializes this map's tilted sectors and extra-floor links
// back into SLOPES lump text (see engine/slopes' own doc comment for the
// grammar) — the inverse of Doc.loadSlopes/applySlopePlane. Returns "" if
// the map has neither (the common case), so SavePWAD skips writing the
// lump entirely rather than an empty, pointless one. Since SavePWAD always
// writes a single-map PWAD (see its own doc comment), there's no other
// map's SLOPES data in the output to preserve — this fully replaces
// whatever the source WAD's own SLOPES lump said, rather than needing to
// splice just this map's block into it.
func (d *Doc) encodeSlopes() string {
	var b strings.Builder
	wrote := false
	open := func() {
		if !wrote {
			fmt.Fprintf(&b, "%s {\n", strings.ToUpper(d.MapName))
			wrote = true
		}
	}
	for i, sec := range d.Level.Sectors {
		if sec.FloorTiltX != 0 || sec.FloorTiltY != 0 {
			open()
			writeSlopePlane(&b, i, false, sec)
		}
		if sec.CeilTiltX != 0 || sec.CeilTiltY != 0 {
			open()
			writeSlopePlane(&b, i, true, sec)
		}
	}
	for _, ef := range d.extraFloors {
		open()
		fmt.Fprintf(&b, "    sector %d extrafloor %d\n", ef.TargetSector, ef.ControlSector)
	}
	if !wrote {
		return ""
	}
	b.WriteString("}\n")
	return b.String()
}

// writeSlopePlane writes one "sector N floor|ceiling plane ..." line,
// reconstructing three plane points from sec's tilt/pivot fields: P1 is
// the pivot at its own authored height (wad.Sector.FloorHeight/
// CeilingHeight already mean "height at the pivot" — see its own doc
// comment), P2/P3 step one grid unit (64 map units) out along +X/+Y so the
// reconstructed points land on ordinary map coordinates. This is an exact
// inverse of Doc.planeTilt: re-parsing these three points recovers the
// same tiltX/tiltY.
func writeSlopePlane(b *strings.Builder, secIdx int, ceiling bool, sec wad.Sector) {
	const step = 64.0
	px, py := sec.SlopePivotX, sec.SlopePivotY
	kind := "floor"
	height, tiltX, tiltY := float64(sec.FloorHeight), sec.FloorTiltX, sec.FloorTiltY
	if ceiling {
		kind, height, tiltX, tiltY = "ceiling", float64(sec.CeilingHeight), sec.CeilTiltX, sec.CeilTiltY
	}
	fmt.Fprintf(b, "    sector %d %s plane %g %g %g  %g %g %g  %g %g %g\n",
		secIdx, kind,
		px, py, height,
		px+step, py, height+tiltX*step,
		px, py+step, height+tiltY*step)
}

// DefaultSavePath is where "Save As" writes without a file dialog yet: next
// to the source WAD, named "<wad>_<map>.wad".
func (d *Doc) DefaultSavePath() string {
	dir := filepath.Dir(d.WADPath)
	base := trimExt(filepath.Base(d.WADPath))
	return filepath.Join(dir, base+"_"+d.MapName+".wad")
}

// Play saves the map to a temp PWAD and launches the engine binary on it.
func (d *Doc) Play() (string, error) {
	tmp := filepath.Join(os.TempDir(), "tpf_editor_playtest.wad")
	if err := d.SavePWAD(tmp); err != nil {
		return "", err
	}
	bin := enginePath()
	if bin == "" {
		return tmp, fmt.Errorf("engine binary not found (build ./cmd/engine)")
	}
	cmd := exec.Command(bin, tmp)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return tmp, err
	}
	go cmd.Wait()
	return tmp, nil
}

func enginePath() string {
	names := []string{"bin/linux_engine", "bin/engine", "./engine"}
	if runtime.GOOS == "windows" {
		names = []string{"bin/windows_engine.exe", "bin/engine.exe", "engine.exe"}
	}
	for _, n := range names {
		if _, err := os.Stat(n); err == nil {
			abs, _ := filepath.Abs(n)
			return abs
		}
	}
	if p, err := exec.LookPath("engine"); err == nil {
		return p
	}
	return ""
}

func trimExt(s string) string {
	if e := filepath.Ext(s); e != "" {
		return s[:len(s)-len(e)]
	}
	return s
}
