package editor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"twopointfive/nodebuild"
	"twopointfive/wad"
)

// SavePWAD writes the doc's map to path as a single-map PWAD the engine can
// load: the raw geometry lumps plus a freshly built BSP (SEGS / SSECTORS /
// NODES) and a generated BLOCKMAP / empty REJECT. The marker lump keeps the
// map's name so `engine file.wad` runs straight into it.
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
	if err := wad.WritePWAD(path, lumps); err != nil {
		return err
	}
	return nil
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
