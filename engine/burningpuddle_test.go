package engine

import (
	"testing"

	"twopointfive/assets"
	"twopointfive/wad"
)

// doorWallGame: a vertical line on the Y axis from (0,-200) to (0,200)
// between sector 0 (x < 0, the barrel's side) and sector 1 (x > 0). With
// doorOpen false, sector 1's ceiling is down on its floor — a closed door,
// no vertical opening; true opens it to 128. No BSP: sectorFloorCeil falls
// back to an unbounded floor/ceiling, which these cases don't depend on.
func doorWallGame(doorOpen bool) *Game {
	ceil := int16(0)
	if doorOpen {
		ceil = 128
	}
	lvl := &wad.Level{
		Vertexes: []wad.Vertex{{X: 0, Y: -200}, {X: 0, Y: 200}},
		Sectors: []wad.Sector{
			{FloorHeight: 0, CeilingHeight: 128},
			{FloorHeight: 0, CeilingHeight: ceil},
		},
		Sidedefs: []wad.Sidedef{{Sector: 0}, {Sector: 1}},
		Linedefs: []wad.Linedef{{StartVertex: 0, EndVertex: 1, FrontSidedef: 0, BackSidedef: 1}},
	}
	g := &Game{Level: lvl, explosionParticles: true}
	g.blockGrid = buildCollisionGrid(lvl)
	return g
}

// A barrel exploding right next to a closed door must leave every
// splatter — and every flame and toxic-smoke puff they emit, through their
// whole drift — on its own side.
func TestBarrelFireStaysBehindClosedDoor(t *testing.T) {
	g := doorWallGame(false)
	frames := []*assets.RGBA{{Width: 1, Height: 1, Pix: make([]byte, 4)}}
	for range 20 {
		g.spawnBurningPuddle(-20, 0, 0)
	}
	for _, p := range g.burningPuddles {
		if p.x >= 0 {
			t.Fatalf("splatter landed through the closed door at x=%.1f", p.x)
		}
		p.ignitionDelay = 0
		g.spawnPuddleFlame(p, frames, 1)
		g.spawnPuddleSmoke(p, frames, 1)
	}
	if len(g.particles) == 0 {
		t.Fatal("no flame/smoke particles spawned")
	}
	for _, pt := range g.particles {
		if pt.X >= 0 {
			t.Fatalf("particle spawned through the closed door at x=%.1f", pt.X)
		}
		pt.DX, pt.life = 40, 10 // drift hard toward the door
	}
	for range 200 {
		g.updateParticles(1.0 / 35)
	}
	for _, pt := range g.particles {
		if pt.X >= 0 {
			t.Fatalf("particle drifted through the closed door to x=%.1f", pt.X)
		}
	}
}

// Once the door is open, the same drift carries smoke through — the wall
// check reads the live opening, it doesn't wall off the doorway for good.
func TestBarrelSmokeDriftsThroughOpenDoor(t *testing.T) {
	g := doorWallGame(true)
	g.particles = append(g.particles, &Particle{X: -10, Z: 30, DX: 40, life: 10, wallClip: true})
	for range 35 {
		g.updateParticles(1.0 / 35)
	}
	if x := g.particles[0].X; x <= 0 {
		t.Errorf("smoke stopped at an open door (x=%.1f)", x)
	}
}

// A barrel fire's light must not come on before its flames: every frame,
// a fire that casts light has at least one live flame lick, and the light
// starts dim and builds up rather than switching on at full strength.
func TestBarrelFireLightWaitsForFlames(t *testing.T) {
	g := doorWallGame(true)
	g.spawnBurningPuddle(-100, 0, 0)
	const dt = 1.0 / 60
	var first, peak float32
	for range 4 * 60 {
		g.updateBurningPuddles(dt)
		g.updateParticles(dt)
		lights := g.appendFireLights(nil, -100, 0)
		if len(lights) == 0 {
			continue
		}
		flame := false
		for _, p := range g.burningPuddles {
			if pickAliveFlame(p.flameParticles) != nil {
				flame = true
			}
		}
		if !flame {
			t.Fatal("fire light is on with no flame lick alive yet")
		}
		if first == 0 {
			first = lights[0].Intensity
		}
		peak = max(peak, lights[0].Intensity)
	}
	if peak == 0 {
		t.Fatal("fire never cast any light")
	}
	if first > peak*0.25 {
		t.Errorf("fire light came on at %.2f of its peak %.2f — it should fade in with the flames", first/peak, peak)
	}

	g.explosionParticles = false // flames not drawn → no light either
	if l := g.appendFireLights(nil, -100, 0); len(l) != 0 {
		t.Error("fire casts light with explosionParticles off (no visible flames)")
	}
}
