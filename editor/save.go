package editor

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"twopointfive/assets"
	"twopointfive/nodebuild"
	"twopointfive/wad"
	"twopointfive/wad3d"
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
// grammar) — the inverse of Doc.loadSlopes/slopes.Def.Apply. Returns "" if
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
// inverse of slopes.PlaneTilt: re-parsing these three points recovers the
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

// DefaultSavePath is where Save writes (no file dialog yet): the launcher's
// wad/ folder, so the game lists it straight away (else next to the source
// WAD), named "<wad>_<map>_3d.wad" — "newlevel_<map>_3d.wad" for a New
// Level. A source already named "*_3d" doesn't get the suffix twice.
func (d *Doc) DefaultSavePath() string {
	base := "newlevel"
	dir := "."
	if d.WADPath != "" {
		base = strings.TrimSuffix(trimExt(filepath.Base(d.WADPath)), "_3d")
		dir = filepath.Dir(d.WADPath)
	}
	if wd, ok := assets.FindDir("wad"); ok {
		dir = wd
	}
	return filepath.Join(dir, base+"_"+d.MapName+"_3d.wad")
}

// convertJob is everything a real-3D conversion of the doc needs, copied
// out of it — so the (slow) conversion can run on another goroutine while
// the doc keeps being edited. prepareConversion builds one.
type convertJob struct {
	raw     string // the doc's map, already written as a plain PWAD
	wadPath string // source WAD ("" for a New Level)
	mapName string
}

// prepareConversion writes the doc's map as a plain PWAD at raw (fast —
// the only part that reads the doc) and returns the job that converts it.
func (d *Doc) prepareConversion(raw string) (convertJob, error) {
	if err := d.SavePWAD(raw); err != nil {
		return convertJob{}, err
	}
	return convertJob{raw: raw, wadPath: d.WADPath, mapName: d.MapName}, nil
}

// run converts the job's plain PWAD into a real-3D WAD at out with opt —
// the same wad3d.Convert cmd/wad3d runs — recording the base IWAD so the
// engine merges exactly that one.
func (j convertJob) run(out string, opt wad3d.Options) (wad3d.Report, error) {
	var rep wad3d.Report
	pw, err := wad.Load(j.raw)
	if err != nil {
		return rep, err
	}
	base, basePath, err := baseIWAD(j.wadPath, j.mapName)
	if err != nil {
		return rep, err
	}
	data, err := os.ReadFile(basePath)
	if err != nil {
		return rep, err
	}
	sum := sha1.Sum(data)
	abs, _ := filepath.Abs(basePath)
	name := "new level"
	if j.wadPath != "" {
		name = filepath.Base(j.wadPath)
	}
	src := wad3d.Source{
		Name: name, Picked: pw, Merged: wad.Merge(base, pw),
		IWADPath: abs, IWADSHA1: hex.EncodeToString(sum[:]),
	}
	if opt.Weapons {
		near := j.wadPath
		if near == "" {
			near = basePath
		}
		if src.Weapons, err = wad3d.FindWeaponIWADs(near, base, "", ""); err != nil {
			return rep, err
		}
	}
	lumps, rep, err := wad3d.Convert(src, opt)
	if err != nil {
		return rep, err
	}
	if opt.Weapons && len(rep.MissingWeapons) > 0 {
		return rep, fmt.Errorf("weapon art/sounds not found (%s) — those guns would be invisible or silent; nothing was written",
			strings.Join(rep.MissingWeapons, " "))
	}
	if abs, _ := filepath.Abs(out); j.wadPath != "" {
		if src, _ := filepath.Abs(j.wadPath); abs == src {
			return rep, fmt.Errorf("refusing to overwrite the source WAD %s", out)
		}
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return rep, err
	}
	return rep, wad.WritePWAD(out, lumps)
}

// SaveOptions is what Save converts with: the full real-3D WAD cmd/wad3d
// writes — geometry passes, the hi-res textures the map uses, all three
// arsenals, and the baked voxels. The editor saves nothing else: there is
// no plain-PWAD save (the engine only plays 3D WADs — wad.CheckPlayable).
var SaveOptions = wad3d.Options{Textures: true, Weapons: true, MaxTex: 1024}

// BeginSave starts saving the doc as a real-3D WAD at path. It writes the
// map out synchronously (fast, the only part touching the doc) and returns
// the slow conversion as a func for the caller to run off the UI thread;
// that func returns a one-line summary for the status bar.
func (d *Doc) BeginSave(path string) (func() (string, error), error) {
	job, err := d.prepareConversion(filepath.Join(os.TempDir(), "tpf_editor_save_src.wad"))
	if err != nil {
		return nil, err
	}
	return func() (string, error) {
		rep, err := job.run(path, SaveOptions)
		if err != nil {
			return "", err
		}
		ramps := 0
		for _, m := range rep.Maps {
			ramps += m.StairRuns
		}
		st, _ := os.Stat(path)
		size := int64(0)
		if st != nil {
			size = st.Size()
		}
		msg := fmt.Sprintf("saved 3D WAD %s (%.0f MB, %d ramp(s))", path, float64(size)/(1<<20), ramps)
		if n := len(rep.UnmodelledTypes); n > 0 {
			msg += fmt.Sprintf(" — %d object type(s) have no voxel model and are invisible", n)
		}
		return msg, nil
	}, nil
}

// Play saves the map to a temporary real-3D WAD and launches the engine
// binary on it. Same conversion as Save (the engine plays only 3D WADs),
// minus texture and weapon embedding to keep F5 quick: the engine falls
// back to the on-disk hi-res set and the other IWAD for those.
func (d *Doc) Play() (string, error) {
	job, err := d.prepareConversion(filepath.Join(os.TempDir(), "tpf_editor_playtest_src.wad"))
	if err != nil {
		return "", err
	}
	tmp := filepath.Join(os.TempDir(), "tpf_editor_playtest.wad")
	if _, err := job.run(tmp, wad3d.Options{}); err != nil {
		return "", fmt.Errorf("playtest conversion: %w", err)
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

// baseIWAD is the base IWAD an edited map plays over: the source
// WAD's own (itself, if it is an IWAD; the IWAD it merges over, or the one
// a real-3D WAD records, otherwise), else — a New Level with no source —
// the Doom II or Doom IWAD matching the map's name format.
func baseIWAD(wadPath, mapName string) (*wad.WAD, string, error) {
	if wadPath != "" {
		if res, err := wad.LoadWithIWAD(wadPath, ""); err == nil {
			if res.IWADPath == "" {
				return res.WAD, wadPath, nil
			}
			if iw, err := wad.Load(res.IWADPath); err == nil {
				return iw, res.IWADPath, nil
			}
		}
	}
	game := "doom"
	if strings.HasPrefix(strings.ToUpper(mapName), "MAP") {
		game = "doom2"
	}
	near := wadPath
	if near == "" {
		near = "."
	}
	if iw, p, ok := wad.FindGameIWAD(near, game); ok {
		return iw, p, nil
	}
	return nil, "", fmt.Errorf("no %s IWAD found to play this map over", game)
}
