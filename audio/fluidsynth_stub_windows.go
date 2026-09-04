//go:build windows && !cgo

package audio

import "fmt"

// newFluidMusic in a non-cgo Windows build: FluidSynth is reached through
// cgo (LoadLibrary/GetProcAddress — see fluidsynth_windows.go), so a
// CGO_ENABLED=0 build has no FluidSynth path and NewMusicPlayer falls
// straight through to winmm.
func newFluidMusic() (musicBackend, string, error) {
	return nil, "", fmt.Errorf("FluidSynth music needs a cgo build (CGO_ENABLED=1)")
}
