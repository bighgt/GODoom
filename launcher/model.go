package launcher

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"twopointfive/config"
	"twopointfive/wad"
)

// Model is the launcher's whole live state for one session — everything
// screen.go's Draw reads and every widget callback mutates. Owned by run.go
// (one instance, one frame loop); no goroutines touch it.
type Model struct {
	Cfg config.Config

	QuickWADs []string // ListWADs() result, computed once at startup

	WADPath  string // "" until a WAD is chosen
	WAD      *wad.WAD
	IWADPath string // the base IWAD that got merged in, "" if WADPath was self-contained
	Is3D     bool   // WADPath is a tools/wad3d "real 3D" WAD (carries a TPF3D marker)
	Maps     []string
	MapName  string

	ActiveTab int     // index into sectionOrder — which settings tab is shown
	ScrollY   float64 // scroll offset within the active tab (reset on tab change)

	Status    string
	StatusErr bool

	BrowseRequested bool // run.go pops the native file dialog when this is true

	Done bool // Play, Quit, or the window closing — exit the frame loop
	Ok   bool // true only for Play with a valid WAD+map
}

// NewModel seeds a Model from the config the launcher was opened with
// (config.Load's result) — WADPath (if it still resolves to a real WAD) is
// pre-picked so re-launching drops you back where you left off.
func NewModel(initial config.Config) *Model {
	m := &Model{Cfg: initial, QuickWADs: ListWADs(), MapName: strings.TrimSpace(initial.Map)}
	// The Audio tab has no Music checkbox — the Music Volume slider is the
	// on/off. Show it at 0 when music was disabled so the slider reflects
	// reality; run.go turns Cfg.Music back on/off from the slider on Play.
	if !m.Cfg.Music {
		m.Cfg.MusicVolume = 0
	}
	if initial.WADPath != "" {
		m.PickWAD(initial.WADPath)
	}
	return m
}

// PickWAD loads path (merging its IWAD if it's a patch — wad.LoadWithIWAD)
// and, on success, populates Maps and picks a starting MapName: the
// current MapName if the new WAD still has it, else the conventional first
// level, else whatever map is listed first. A failure leaves any previous
// selection alone and just reports Status — so a bad Browse… pick doesn't
// wipe out a WAD that was already loaded fine.
func (m *Model) PickWAD(path string) {
	res, err := wad.LoadWithIWAD(path, m.Cfg.IWAD)
	if err != nil {
		m.Status = fmt.Sprintf("Couldn't load %s: %v", path, err)
		m.StatusErr = true
		return
	}
	// Only real-3D WADs (cmd/wad3d output) are playable — anything else is
	// refused here with the "convert it first" message rather than at launch.
	if err := wad.CheckPlayable(res.WAD, filepath.Base(path)); err != nil {
		m.Status = err.Error()
		var np *wad.NotPlayableError
		if errors.As(err, &np) {
			m.Status = np.Short() // the full text is too long for the status bar
		}
		m.StatusErr = true
		return
	}
	maps := res.WAD.ListMaps()
	if len(maps) == 0 {
		m.Status = fmt.Sprintf("%s has no maps (no THINGS/LINEDEFS lumps under a map marker)", path)
		m.StatusErr = true
		return
	}
	m.WADPath, m.WAD, m.IWADPath, m.Maps = path, res.WAD, res.IWADPath, maps
	_, m.Is3D = res.WAD.TPF3D()
	m.MapName = pickDefaultMap(maps, m.MapName)
	m.Status, m.StatusErr = "", false
}

// pickDefaultMap keeps want if it's still one of maps, otherwise falls back
// to wad.PreferredStartMap's convention (E1M1/MAP01, else whatever map is
// listed first — the same convention cmd/engine's own pickStartMap uses).
func pickDefaultMap(maps []string, want string) string {
	for _, m := range maps {
		if strings.EqualFold(m, want) {
			return m
		}
	}
	return wad.PreferredStartMap(maps)
}
