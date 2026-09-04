package assets

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
)

// skyboxPath is where tools/gensky writes the generated Mars sky texture,
// and where LoadSkybox looks for it — see findAssetDir (hires.go) for how
// the base directory is located. Deliberately read from disk rather than
// baked in with an embed directive — see hires.go's doc comment for why a
// ~6MB texture doesn't belong inside the executable itself.
const skyboxPath = "assets/skybox/mars_sky.png"

// LoadSkybox loads the Mars sky texture (see tools/gensky,
// game_design.txt) from disk into an RGBA bitmap, the same format every
// other texture in this package uses. Returns an error if it can't be
// found/decoded — non-fatal to the caller (raster.New logs it and falls
// back to the WAD's own sky texture; see raster/sky.go).
func LoadSkybox() (*RGBA, error) {
	dir, ok := findAssetDir(filepath.Dir(skyboxPath))
	if !ok {
		return nil, fmt.Errorf("assets: %s not found next to the executable or working directory", skyboxPath)
	}
	path := filepath.Join(dir, filepath.Base(skyboxPath))
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("assets: open skybox: %w", err)
	}
	defer f.Close()

	img, err := png.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("assets: decode skybox: %w", err)
	}
	// TexelsPerUnit doesn't apply to the sky at all — drawSkySpan samples it
	// by screen angle/row, never in map-unit space — so 1 here is just a
	// safe, conventional default, not a meaningful density.
	return imageToRGBA(img, 1), nil
}
