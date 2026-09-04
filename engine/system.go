// Package engine is the core of the game engine: the Game struct that owns
// and drives everything else (window, GPU renderer, CPU rasterizer, loaded
// WAD/level/BSP tree, camera, map objects), and the ordered list of Systems
// that make up one frame of simulation.
//
// Go has no classes, so the structure here is idiomatic Go: interfaces as
// contracts (System, render.Renderer), composition over inheritance, and
// polymorphism through interface slices. The per-frame simulation is a
// []System driven by Game.Run rather than a hand-written call sequence, so
// adding a subsystem is an addition to NewGame, not an edit to the loop.
package engine

// System is a self-contained slice of engine behavior that runs once per
// frame against the Game's current state (input handling, physics,
// combat, ...). Game.Run holds a single ordered []System and calls through
// it polymorphically — adding a new subsystem later means implementing this
// interface and registering it in NewGame, not editing the main loop.
type System interface {
	// Name identifies the system, for logging.
	Name() string
	// Update runs one frame of this system's work.
	Update(g *Game, dt float32)
}
