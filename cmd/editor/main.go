// Command editor is the TwoPointFive level editor: a single window with a
// 2D top-down map view on the left and a live 3D view (the engine's own
// software renderer) on the right. Edit geometry and things in 2D and see
// them in 3D as you go; drag textures and things from the side panels.
//
// Usage:
//
//	editor [-new] [-fullscreen] [WAD path]
//
// With no WAD path it opens the first WAD (with maps) it finds under
// wad/Game WADs/ or assets/. -new starts on a fresh one-room level instead
// of that WAD's first map; the WAD is still loaded, for its textures.
// -fullscreen opens borderless-fullscreen on the primary monitor instead of
// the default windowed size (F11, or View > Toggle Fullscreen, switches
// either way once the editor is running). Textures and things are
// aggregated from every WAD in those directories.
package main

import (
	"flag"
	"log"
	"runtime"

	"twopointfive/editor"
)

func init() {
	// GLFW's window/event functions must be called from the thread that
	// called glfw.Init() — pin this goroutine to the OS thread Go started it
	// on before main runs (see cmd/engine/main.go's init, which this
	// mirrors). Without this the Go scheduler is free to migrate main's
	// goroutine to a different OS thread at any point — a blocking disk
	// read while loading assets, a GC pause — after which every further
	// GLFW call runs on the wrong thread with undefined behaviour: windows
	// that silently close themselves, input that stops arriving, a toggle
	// that tears down the window and never gets a new one going. Easy to
	// miss because small/quick windowed runs rarely hit a thread switch
	// before the loop ends up pinned anyway; fullscreen's heavier startup
	// (and any later resize/toggle) hits it far more often.
	runtime.LockOSThread()
}

func main() {
	log.SetFlags(log.Ltime)
	newLevel := flag.Bool("new", false, "start on a fresh one-room level")
	fullscreen := flag.Bool("fullscreen", false, "open borderless-fullscreen on the primary monitor")
	flag.Parse()
	if err := editor.Run(flag.Arg(0), *newLevel, *fullscreen); err != nil {
		log.Fatalf("editor: %v", err)
	}
}
