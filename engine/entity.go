// Package engine is the core of the game engine: the Entity/Component/System
// framework and the Game struct that owns and drives everything else
// (window, renderer, loaded level). Go has no classes, so the "core
// essential of OOP" this engine is built on is realized idiomatically —
// interfaces as contracts, struct embedding for shared behavior, and
// polymorphism through interface slices. See game_design.txt's "OOP
// philosophy" section for the full rationale.
package engine

// Entity is anything that exists in the game world: the player, a monster,
// an item, a decoration. It's an interface — not a struct — so the engine
// loop can hold a single []Entity and treat every concrete kind uniformly
// (polymorphism), while each kind still defines its own behavior.
type Entity interface {
	// ID returns this entity's unique identifier within the Game that owns it.
	ID() uint32
	// Update advances this entity's state by dt seconds.
	Update(dt float32)
}

// BaseEntity is an embeddable struct providing the bookkeeping every Entity
// needs (an ID, a position). Concrete entity types embed BaseEntity for that
// shared behavior "for free" — Go's composition-over-inheritance answer to
// a base class — and then implement Update themselves to specialize it,
// which is how a type "overrides" behavior without a class hierarchy.
type BaseEntity struct {
	EntityID uint32
	X, Y, Z  float32
	Angle    float32 // facing direction, radians
}

// ID satisfies Entity. Types embedding BaseEntity get this for free.
func (b *BaseEntity) ID() uint32 {
	return b.EntityID
}

// Update is BaseEntity's default no-op implementation of Entity, satisfying
// the interface for entities with no per-frame behavior of their own (a
// static decoration, for instance). Types with real behavior define their
// own Update method, which — because Go dispatches methods by the concrete
// type, not the embedded one — replaces this one for that type.
func (b *BaseEntity) Update(dt float32) {}
