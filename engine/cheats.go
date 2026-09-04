package engine

import (
	"fmt"
	"log"
	"math"
	"strings"

	"twopointfive/wad"
)

// Classic Doom cheat codes, baked in for testing. Type the letters on the
// keyboard during play (there is no console) exactly like the 1993/94
// games — the matcher keeps a short rolling buffer of typed characters
// (from window.Window.TakeTyped) and fires when a code is its suffix:
//
//	iddqd        toggle god mode (damage/crush immune; tops health to 100)
//	idkfa        all weapons, full ammo, all keys, blue armor
//	idfa         all weapons, full ammo, blue armor (no keys)
//	idclip       toggle no-clipping (walk through walls)
//	idspispopd   same as idclip (the Doom 1 spelling)
//	idbehold     log the powerup sub-codes
//	idbeholdv    invulnerability          idbeholds   berserk (strength)
//	idbeholdi    partial invisibility     idbeholdr   radiation suit
//	idbeholda    computer area map        idbeholdl   light amplification
//	idclevXY     warp — XY is episode+map on Doom 1 (E2M5 -> "25"),
//	             the two-digit slot on Doom 2 (MAP07 -> "07")
//	idmusXY      change music, same XY scheme
//	iddt         (needs an automap — this engine has none; logs that)
//	idmypos      log the player's position and angle
//	idchoppers   give the chainsaw
//
// Every fired code writes an "engine: cheat:" line to the log.

// cheatBufMax bounds the rolling typed-character buffer — comfortably
// longer than the longest code ("idspispopd", "idchoppers").
const cheatBufMax = 24

// cheatSystem feeds typed keys into the matcher each frame. Registered in
// NewGame after the gameplay systems; a no-op without a window (tests).
type cheatSystem struct{}

func (cheatSystem) Name() string { return "cheats" }

func (cheatSystem) Update(g *Game, dt float32) { g.pollCheats() }

// pollCheats appends this frame's typed characters to the rolling buffer
// and runs the matcher.
func (g *Game) pollCheats() {
	if g.Window == nil {
		return
	}
	typed := g.Window.TakeTyped()
	if typed == "" {
		return
	}
	g.cheatBuf += typed
	if len(g.cheatBuf) > cheatBufMax {
		g.cheatBuf = g.cheatBuf[len(g.cheatBuf)-cheatBufMax:]
	}
	g.matchCheats()
}

// matchCheats fires whichever cheat code (if any) the buffer now ends with,
// then clears the buffer so it can't re-trigger on the next keystroke.
func (g *Game) matchCheats() {
	buf := g.cheatBuf

	// Parameterized codes: prefix followed by exactly two digits.
	if c := g.cheat("idclev"); c != "" {
		if xy := digitsAfter(buf, c); xy != "" {
			g.cheatWarp(g.mapFromCode(xy))
			g.cheatBuf = ""
			return
		}
	}
	if c := g.cheat("idmus"); c != "" {
		if xy := digitsAfter(buf, c); xy != "" {
			g.cheatMusic(g.mapFromCode(xy))
			g.cheatBuf = ""
			return
		}
	}

	godHealth := g.dm().godModeHealth
	switch {
	case hasCheat(buf, g.cheat("iddqd")):
		g.godMode = !g.godMode
		if g.godMode && g.Player.Health < godHealth {
			g.Player.Health = godHealth
			g.syncPlayerMobjHealth()
		}
		g.cheatMsg("god mode %s", onOff(g.godMode))

	case hasCheat(buf, g.cheat("idkfa")):
		g.giveAllWeaponsAndAmmo()
		for i := range g.Player.Keys {
			g.Player.Keys[i] = true
		}
		g.Player.Armor, g.Player.ArmorType = g.dm().idkfaArmor, g.dm().idkfaArmorClass
		g.cheatMsg("all weapons, full ammo, all keys, armor")

	case hasCheat(buf, g.cheat("idfa")):
		g.giveAllWeaponsAndAmmo()
		g.Player.Armor, g.Player.ArmorType = g.dm().idfaArmor, g.dm().idfaArmorClass
		g.cheatMsg("all weapons, full ammo, armor")

	case hasCheat(buf, g.cheat("idclip")), hasCheat(buf, g.cheat("idspispopd")):
		g.noclip = !g.noclip
		g.cheatMsg("no-clip %s", onOff(g.noclip))

	case hasCheat(buf, g.cheat("idbeholdv")):
		g.cheatPower(pwInvulnerability)
	case hasCheat(buf, g.cheat("idbeholds")):
		g.cheatPower(pwStrength)
	case hasCheat(buf, g.cheat("idbeholdi")):
		g.cheatPower(pwInvisibility)
	case hasCheat(buf, g.cheat("idbeholdr")):
		g.cheatPower(pwIronFeet)
	case hasCheat(buf, g.cheat("idbeholda")):
		g.cheatPower(pwAllMap)
	case hasCheat(buf, g.cheat("idbeholdl")):
		g.cheatPower(pwInfrared)
	case hasCheat(buf, g.cheat("idbehold")):
		g.cheatMsg("idbehold: append v/s/i/r/a/l for invuln / berserk / invis / radsuit / map / light")
		return // leave the buffer so the follow-up letter still completes idbehold<x>

	case hasCheat(buf, g.cheat("iddt")):
		g.cheatMsg("iddt: this engine has no automap")

	case hasCheat(buf, g.cheat("idmypos")):
		g.cheatMsg("pos  x=%.0f y=%.0f z=%.0f  angle=%.0f",
			g.Camera.X, g.Camera.Y, g.Camera.Z, math.Mod(g.Camera.Angle*180/math.Pi+360, 360))

	case hasCheat(buf, g.cheat("idchoppers")):
		g.Player.Weapons[wpChainsaw] = true
		g.SwitchWeapon(wpChainsaw)
		g.cheatMsg("chainsaw — doesn't suck as much now")

	default:
		return // no code completed; keep accumulating
	}
	g.cheatBuf = ""
}

// giveAllWeaponsAndAmmo is the idkfa/idfa arsenal: every weapon owned, each
// ammo type filled to its current maximum (backpack-aware).
func (g *Game) giveAllWeaponsAndAmmo() {
	for i := 0; i < numWeapons; i++ {
		g.Player.Weapons[i] = true
	}
	for i := range g.Player.Ammo {
		g.Player.Ammo[i] = g.Player.MaxAmmo[i]
	}
}

// cheatPower grants one idbehold powerup, matching the pickup in pickup.go
// (durations, the berserk health top-up, the invisibility fuzz flag).
func (g *Game) cheatPower(pw int) {
	switch pw {
	case pwStrength:
		g.Player.Powers[pwStrength] = 1
		g.giveBody(100)
		g.cheatMsg("berserk")
	case pwInvulnerability:
		g.Player.Powers[pwInvulnerability] = invulnTics
		g.cheatMsg("invulnerability")
	case pwInvisibility:
		g.Player.Powers[pwInvisibility] = invisTics
		if g.playerMobj != nil {
			g.playerMobj.Flags |= MF_SHADOW
		}
		g.cheatMsg("partial invisibility")
	case pwIronFeet:
		g.Player.Powers[pwIronFeet] = ironTics
		g.cheatMsg("radiation suit")
	case pwAllMap:
		g.Player.Powers[pwAllMap] = 1
		g.cheatMsg("computer area map")
	case pwInfrared:
		g.Player.Powers[pwInfrared] = infraTics
		g.cheatMsg("light amplification")
	}
}

// cheatWarp queues an idclev warp for stepSimulation to load at a safe
// point (like a level exit). A target not in the WAD is reported and
// ignored.
func (g *Game) cheatWarp(mapName string) {
	if g.WAD == nil || g.WAD.IndexOf(mapName) < 0 {
		g.cheatMsg("idclev: %s is not in this WAD", mapName)
		return
	}
	g.pendingWarp = mapName
	g.cheatMsg("warping to %s", mapName)
}

// cheatMusic asks cmd/engine (via OnMusicChange) to switch the track to the
// one that map conventionally uses.
func (g *Game) cheatMusic(mapName string) {
	if g.OnMusicChange == nil {
		g.cheatMsg("idmus: music is not available")
		return
	}
	g.OnMusicChange(mapName)
	g.cheatMsg("music -> %s (%s)", mapName, wad.MusicLumpName(mapName))
}

// mapFromCode turns an idclev/idmus two-digit code into a map name in the
// format the loaded WAD uses: "E<x>M<y>" for Doom / Ultimate Doom (current
// map name starts with 'E'), "MAP<xy>" otherwise.
func (g *Game) mapFromCode(xy string) string {
	if g.Level != nil && strings.HasPrefix(g.Level.Name, "E") {
		return fmt.Sprintf("E%cM%c", xy[0], xy[1])
	}
	return "MAP" + xy
}

// digitsAfter returns the two trailing characters of buf when they are
// digits and the rest of buf ends with prefix; otherwise "".
func digitsAfter(buf, prefix string) string {
	if len(buf) < len(prefix)+2 {
		return ""
	}
	d := buf[len(buf)-2:]
	if d[0] < '0' || d[0] > '9' || d[1] < '0' || d[1] > '9' {
		return ""
	}
	if !strings.HasSuffix(buf[:len(buf)-2], prefix) {
		return ""
	}
	return d
}

func (g *Game) cheatMsg(format string, args ...any) {
	log.Printf("engine: cheat: "+format, args...)
}

func onOff(b bool) string {
	if b {
		return "ON"
	}
	return "OFF"
}
