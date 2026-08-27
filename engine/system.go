package engine

// System is a self-contained slice of engine behavior that runs once per
// frame against the Game's current state (input handling, physics,
// rendering, ...). Like Entity, it's an interface so Game.Run can hold a
// single, ordered []System and call through it polymorphically — adding a
// new subsystem later means implementing this interface, not editing the
// main loop.
type System interface {
	// Name identifies the system, for logging.
	Name() string
	// Update runs one frame of this system's work.
	Update(g *Game, dt float32)
}
