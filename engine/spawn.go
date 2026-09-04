package engine

import (
	"log"
	"math"
)

// THINGS option-flag bits (id's MTF_*).
const (
	mtfEasy      = 1 << 0
	mtfNormal    = 1 << 1
	mtfHard      = 1 << 2
	mtfAmbush    = 1 << 3 // deaf — only wakes on sight
	mtfNotSingle = 1 << 4 // absent in single-player
)

// skillThingBit is the MTF_* flag a THING must carry to spawn at Game.Skill
// (config.json's "skill", 1..5): skill 1-2 wants mtfEasy, 3 mtfNormal, 4-5
// mtfHard — id's own skill->flag mapping. The other skill effects (ITYTD
// double ammo / half damage, Nightmare fast+respawning monsters) aren't
// modelled, so 1 vs 2 and 4 vs 5 differ only if a map author set those
// bits differently, which the stock IWADs don't.
func skillThingBit(skill int) int {
	switch {
	case skill < 1 || skill > 5:
		return mtfNormal // unset / out of range -> Hurt Me Plenty
	case skill <= 2:
		return mtfEasy
	case skill >= 4:
		return mtfHard
	default:
		return mtfNormal
	}
}

// spawnMapThings walks the level's THINGS and spawns a Mobj for each that
// applies at the current skill in single-player, skipping player and
// deathmatch starts (the camera is positioned from the player-1 start in
// cmd/engine). Called once from NewGame.
func (g *Game) spawnMapThings() {
	spawned := 0
	skillBit := uint16(skillThingBit(g.Skill))
	for i := range g.Level.Things {
		t := &g.Level.Things[i]

		switch t.Type {
		case 1, 2, 3, 4, 11: // player 1-4 starts, deathmatch start
			continue
		}
		// Teleport landing spots (14) ignore skill/single-player flags.
		if t.Type != 14 {
			if t.Flags&mtfNotSingle != 0 {
				continue
			}
			if t.Flags&skillBit == 0 {
				continue
			}
		}

		typ, ok := doomedNumToType[int(t.Type)]
		if !ok {
			continue // an unsupported thing type — silently skip for now
		}

		z := onFloorZ
		if mobjInfo[typ].Flags&MF_SPAWNCEILING != 0 {
			z = onCeilingZ
		}
		mo := g.P_SpawnMobj(float64(t.X), float64(t.Y), z, typ)
		mo.Angle = float64(t.Angle) * math.Pi / 180
		if t.Flags&mtfAmbush != 0 {
			mo.Flags |= MF_AMBUSH
		}
		spawned++
	}
	log.Printf("engine: spawned %d map things (%d mobjs live)", spawned, len(g.mobjs))
}
