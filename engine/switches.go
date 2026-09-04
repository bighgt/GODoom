package engine

import "strings"

// Switch textures, ported from id's p_switch.c. Every vanilla switch
// texture is a SW1xxx / SW2xxx pair with a mechanical name relationship, so
// instead of carrying switches.c's alphSwitchList we just flip the digit.
// A repeatable switch (SR/WR/GR/DR) flips back after ~1s via a switchBack
// thinker.

const switchBackTics = 35

// switchToggle returns the opposite face of a switch texture, or ok=false
// if name isn't a switch.
func switchToggle(name string) (string, bool) {
	if strings.HasPrefix(name, "SW1") {
		return "SW2" + name[3:], true
	}
	if strings.HasPrefix(name, "SW2") {
		return "SW1" + name[3:], true
	}
	return "", false
}

type switchBack struct {
	tex   *string
	orig  string
	count int
}

func (s *switchBack) sectorIndex() int { return -1 }

func (s *switchBack) tick(g *Game) bool {
	if s.count--; s.count <= 0 {
		*s.tex = s.orig
		g.playWorldSound("DSSWTCHN")
		return true
	}
	return false
}

// changeSwitchTexture flips the SW1/SW2 texture on lineIdx's front sidedef
// (whichever of its three slots is a switch) and plays the click. If
// useAgain is set it also schedules the flip-back.
func (g *Game) changeSwitchTexture(lineIdx int, useAgain bool) {
	ld := &g.Level.Linedefs[lineIdx]
	if ld.FrontSidedef == wadNoSidedef {
		return
	}
	sd := &g.Level.Sidedefs[ld.FrontSidedef]
	for _, tex := range []*string{&sd.UpperTexture, &sd.MiddleTexture, &sd.LowerTexture} {
		flipped, ok := switchToggle(*tex)
		if !ok {
			continue
		}
		orig := *tex
		*tex = flipped
		g.playWorldSound("DSSWTCHN")
		if useAgain {
			g.thinkers = append(g.thinkers, &switchBack{tex: tex, orig: orig, count: switchBackTics})
		}
		return
	}
	// No switch texture on the line — still click (e.g. a bare exit trigger).
	g.playWorldSound("DSSWTCHN")
}

const wadNoSidedef = 0xFFFF
