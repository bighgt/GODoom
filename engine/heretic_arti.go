package engine

import (
	"math"

	"twopointfive/raster"
)

// Heretic's artifact inventory. Picked-up artifacts stack in PlayerStats
// .Inventory (counts, capped at 16 each, id's INVENTORYSLOTS behaviour);
// [ and ] cycle the selection, Enter uses it. Effects are best-effort
// Doom-equivalents — the powered ("Tome of Power") weapon modes and the
// chicken-morph player state are not fully modelled.

type artifact int

const (
	artiInvuln      artifact = iota // Ring of Invincibility
	artiInvis                       // Shadowsphere
	artiHealth                      // Quartz Flask (+25)
	artiSuperHealth                 // Mystic Urn (full)
	artiTome                        // Tome of Power
	artiTorch                       // Torch
	artiTimeBomb                    // Time Bomb of the Ancients
	artiEgg                         // Morph Ovum
	artiWings                       // Wings of Wrath
	artiTeleport                    // Chaos Device
	artiNone artifact = -1
)

// numArtifacts is untyped so it compares cleanly with plain ints.
const numArtifacts = int(artiTeleport) + 1

// artiIcon is the HUD inventory-box icon lump for each artifact.
var artiIcon = [numArtifacts]string{
	artiInvuln:      "ARTIINVU",
	artiInvis:       "ARTIINVS",
	artiHealth:      "ARTIPTN2",
	artiSuperHealth: "ARTISPHL",
	artiTome:        "ARTIPWBK",
	artiTorch:       "ARTITRCH",
	artiTimeBomb:    "ARTIFBMB",
	artiEgg:         "ARTIEGGC",
	artiWings:       "ARTISOAR",
	artiTeleport:    "ARTIATLP",
}

const (
	artiMax        = 16
	tomeTics       = 40 * 35
	torchTics      = 20 * 35 // 60*35 in Heretic; shorter here
	flightTics     = 40 * 35
	timeBombDelay  = 35      // ~1s fuse
	timeBombDamage = 128     // pRadiusAttack range
)

// doubled returns v with every element doubled (Bag of Holding on the
// Heretic ammo caps).
func doubled(v [numAmmoTypes]int) [numAmmoTypes]int {
	for i := range v {
		v[i] *= 2
	}
	return v
}

// giveArtifact adds one of artifact a (capped), selecting it if nothing was
// selected. Returns false only when already at the cap.
func (g *Game) giveArtifact(a artifact) bool {
	if a < 0 || int(a) >= numArtifacts {
		return false
	}
	if g.Player.Inventory[a] >= artiMax {
		return false
	}
	g.Player.Inventory[a]++
	if g.Player.ArtiCurrent < 0 || g.Player.Inventory[g.Player.ArtiCurrent] == 0 {
		g.Player.ArtiCurrent = int(a)
	}
	return true
}

// currentArtifactIcon is what the status bar shows in the inventory box.
func (g *Game) currentArtifactIcon() (icon string, count int) {
	a := g.Player.ArtiCurrent
	if a < 0 || a >= numArtifacts || g.Player.Inventory[a] == 0 {
		return "", 0
	}
	return artiIcon[a], g.Player.Inventory[a]
}

// cycleArtifact moves the selection to the next/prev held artifact.
func (g *Game) cycleArtifact(dir int) {
	held := 0
	for _, n := range g.Player.Inventory {
		if n > 0 {
			held++
		}
	}
	if held == 0 {
		g.Player.ArtiCurrent = -1
		return
	}
	cur := g.Player.ArtiCurrent
	if cur < 0 {
		cur = 0
	}
	for i := 0; i < numArtifacts; i++ {
		cur = (cur + dir + numArtifacts) % numArtifacts
		if g.Player.Inventory[cur] > 0 {
			g.Player.ArtiCurrent = cur
			return
		}
	}
}

// useArtifact consumes the selected artifact and applies its effect.
func (g *Game) useArtifact() {
	a := g.Player.ArtiCurrent
	if a < 0 || a >= numArtifacts || g.Player.Inventory[a] == 0 || g.playerDead {
		return
	}
	used := true
	switch artifact(a) {
	case artiInvuln:
		g.Player.Powers[pwInvulnerability] = invulnTics
	case artiInvis:
		g.Player.Powers[pwInvisibility] = invisTics
		if g.playerMobj != nil {
			g.playerMobj.Flags |= MF_SHADOW
		}
	case artiHealth:
		used = g.giveBody(25)
	case artiSuperHealth:
		g.Player.Health = g.dm().maxHealth
		g.syncPlayerMobjHealth()
	case artiTome:
		g.Player.Powers[pwTome] = tomeTics
	case artiTorch:
		g.Player.Powers[pwTorch] = torchTics
	case artiTimeBomb:
		g.dropTimeBomb()
	case artiEgg:
		g.fireEgg()
	case artiWings:
		g.Player.Powers[pwFlight] = flightTics
	case artiTeleport:
		g.chaosDeviceTeleport()
	}
	if !used {
		return
	}
	g.playSound("ARTIUSE")
	g.Player.Inventory[a]--
	if g.Player.Inventory[a] == 0 {
		g.cycleArtifact(1)
		if c := g.Player.ArtiCurrent; c < 0 || g.Player.Inventory[c] == 0 {
			g.Player.ArtiCurrent = -1
		}
	}
}

// dropTimeBomb plants a bomb at the player's feet that blasts after a short
// fuse (id's MT_TIMEBOMB / A_Explode).
func (g *Game) dropTimeBomb() {
	if g.playerMobj == nil {
		return
	}
	fdx, fdy, _ := aimDirection(g.Camera)
	x := g.Camera.X + fdx*24
	y := g.Camera.Y + fdy*24
	mo := g.P_SpawnMobj(x, y, onFloorZ, MT_HERETIC)
	mo.spriteOverride = "FBMB"
	mo.overrideFrames, mo.overrideTics = 5, 3
	mo.Fullbright = true
	mo.Radius, mo.Height = 8, 16
	mo.Flags = MF_NOGRAVITY
	mo.Tics = -1
	g.timeBombs = append(g.timeBombs, timeBomb{mo: mo, fuse: timeBombDelay})
}

type timeBomb struct {
	mo   *Mobj
	fuse int
}

// tickTimeBombs is called from runTic.
func (g *Game) tickTimeBombs() {
	if len(g.timeBombs) == 0 {
		return
	}
	alive := g.timeBombs[:0]
	for _, tb := range g.timeBombs {
		tb.fuse--
		if tb.fuse > 0 {
			alive = append(alive, tb)
			continue
		}
		g.pRadiusAttack(tb.mo, g.playerMobj, timeBombDamage)
		g.P_SpawnMobj(tb.mo.X, tb.mo.Y, tb.mo.Z, MT_TFOG)
		g.playSound("PHOHIT")
		g.removeMobj(tb.mo)
	}
	g.timeBombs = alive
}

// fireEgg turns the monster the player is aiming at into a harmless
// chicken (Heretic's Morph Ovum, hugely simplified: no timer, no player
// morph).
func (g *Game) fireEgg() {
	if g.playerMobj == nil {
		return
	}
	target := g.aimTargetInFront(1024)
	if target == nil {
		return
	}
	g.chickenize(target)
}

// chickenize swaps a monster for a weak, skittish chicken.
func (g *Game) chickenize(mo *Mobj) {
	if mo.hact == nil {
		return
	}
	mo.hact = hChickenDef
	mo.Info = hChickenDef.info
	mo.spriteOverride = "CHKN"
	mo.Health = 10
	mo.Radius, mo.Height = 9, 22
	mo.Flags = monsterFlags
	mo.hPhase = hpSee
	mo.hFrameIdx = 0
	g.hEnterFrame(mo)
	g.P_SpawnMobj(mo.X, mo.Y, mo.Z+mo.Height/2, MT_TFOG)
}

// hChickenDef is the morphed-monster actor: pecks for tiny damage.
var hChickenDef = func() *hereticActorDef {
	d := &hereticActorDef{
		name: "chicken", sprite: "CHKN", health: 10, radius: 9, height: 22,
		speed: 4, mass: 40, painChance: 200,
		meleeLo: 1, meleeHi: 3,
		seeSound: "chicpk1", attackSound: "chicatk", painSound: "chicpai", deathSound: "chicdth",
		sSpawn: "CHKN AB 10 look",
		sSee:   "CHKN ABAB 3 chase",
		sMelee: "CHKN A 8 face; CHKN B 8 melee",
		sPain:  "CHKN D 3; CHKN D 3 pain",
		sDeath: "CHKN C 6 scream; CHKN DEFGH 6 noblock; CHKN I -1",
	}
	d.aSpawn = parseHFrames(d.sSpawn)
	d.aSee = parseHFrames(d.sSee)
	d.aMelee = parseHFrames(d.sMelee)
	d.aPain = parseHFrames(d.sPain)
	d.aDeath = parseHFrames(d.sDeath)
	d.info = buildHereticInfo(d)
	return d
}()

// chaosDeviceTeleport whisks the player back to the level's player-1 start.
func (g *Game) chaosDeviceTeleport() {
	for _, t := range g.Level.Things {
		if t.Type != 1 {
			continue
		}
		g.P_SpawnMobj(g.Camera.X, g.Camera.Y, g.Camera.Z-EyeHeight, MT_TFOG)
		g.Camera.X, g.Camera.Y = float64(t.X), float64(t.Y)
		g.Camera.Angle = float64(t.Angle) * math.Pi / 180
		g.VelX, g.VelY = 0, 0
		floor, _ := g.sectorFloorCeil(g.Camera.X, g.Camera.Y)
		g.groundPlayer(floor)
		g.P_SpawnMobj(g.Camera.X, g.Camera.Y, g.Camera.Z-EyeHeight, MT_TFOG)
		g.playSound("TELEPT")
		return
	}
}

// aimTargetInFront returns the nearest shootable mobj roughly in the
// player's line of fire within maxDist, or nil.
func (g *Game) aimTargetInFront(maxDist float64) *Mobj {
	fx, fy, _ := aimDirection(g.Camera)
	var best *Mobj
	bestD := maxDist
	for _, mo := range g.mobjs {
		if mo.removed || mo.Flags&MF_SHOOTABLE == 0 || mo.Health <= 0 {
			continue
		}
		dx, dy := mo.X-g.Camera.X, mo.Y-g.Camera.Y
		d := math.Hypot(dx, dy)
		if d < 1 || d > bestD {
			continue
		}
		if (dx*fx+dy*fy)/d < 0.86 { // ~30 deg cone
			continue
		}
		best, bestD = mo, d
	}
	return best
}

// artifactSystem: the F-key-style edge-detected inventory controls, plus
// the per-tic Torch light and Wings descent. Registered in NewGame for a
// Heretic game.
type artifactSystem struct{}

func (artifactSystem) Name() string { return "heretic-artifacts" }

func (artifactSystem) Update(g *Game, dt float32) {
	if !g.heretic || g.Window == nil {
		return
	}
	use := g.Window.UseArtifactPressed()
	if use && !g.artiUseDown {
		g.useArtifact()
	}
	g.artiUseDown = use

	next := g.Window.InvNextPressed()
	if next && !g.artiNextDown {
		g.cycleArtifact(1)
	}
	g.artiNextDown = next

	prev := g.Window.InvPrevPressed()
	if prev && !g.artiPrevDown {
		g.cycleArtifact(-1)
	}
	g.artiPrevDown = prev

	// Torch / Tome of Power: a flat extralight bump while they last (this
	// runs after flashlightSystem in the system order, so it wins).
	if g.Player.Powers[pwTorch] > 0 && raster.ExtraLight < 4 {
		raster.ExtraLight = 4
	} else if g.Player.Powers[pwTome] > 0 && raster.ExtraLight < 2 {
		raster.ExtraLight = 2
	}
}
