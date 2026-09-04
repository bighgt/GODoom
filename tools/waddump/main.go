// Command waddump inspects a WAD's contents — the lump directory, and for
// each map (or a named one) a histogram of THING types, LINEDEF special
// types, and SECTOR special types. Used to see what a given IWAD/PWAD
// actually exercises before deciding what the engine needs to support.
//
//	go run ./tools/waddump <file.wad> [MAPNAME]
package main

import (
	"fmt"
	"log"
	"os"
	"sort"

	"twopointfive/wad"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: waddump <file.wad> [MAPNAME]")
	}
	w, err := wad.Load(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s: %s, %d lumps\n", os.Args[1], w.Header.Magic, w.Header.NumLumps)

	// Music lumps present.
	var music []string
	for _, e := range w.Entries {
		if len(e.Name) >= 2 && e.Name[0] == 'D' && e.Name[1] == '_' {
			music = append(music, e.Name)
		}
	}
	fmt.Printf("music lumps (%d): %v\n", len(music), music)

	maps := w.ListMaps()
	if len(os.Args) >= 3 {
		maps = []string{os.Args[2]}
	}
	for _, m := range maps {
		lvl, err := w.LoadLevel(m)
		if err != nil {
			fmt.Printf("\n== %s: %v\n", m, err)
			continue
		}
		fmt.Printf("\n== %s: %d things, %d linedefs, %d sectors\n",
			m, len(lvl.Things), len(lvl.Linedefs), len(lvl.Sectors))

		thingHist := map[uint16]int{}
		for _, t := range lvl.Things {
			thingHist[t.Type]++
		}
		printHist("THING types", thingHist, func(k uint16) string { return thingName(k) })

		lineHist := map[uint16]int{}
		for _, ld := range lvl.Linedefs {
			if ld.SpecialType != 0 {
				lineHist[ld.SpecialType]++
			}
		}
		printHist("LINEDEF specials", lineHist, func(k uint16) string { return "" })

		secHist := map[uint16]int{}
		for _, s := range lvl.Sectors {
			if s.SpecialType != 0 {
				secHist[s.SpecialType]++
			}
		}
		printHist("SECTOR specials", secHist, func(k uint16) string { return "" })
	}
}

func printHist(title string, h map[uint16]int, name func(uint16) string) {
	keys := make([]int, 0, len(h))
	for k := range h {
		keys = append(keys, int(k))
	}
	sort.Ints(keys)
	fmt.Printf("  %s (%d distinct):\n", title, len(keys))
	for _, k := range keys {
		n := name(uint16(k))
		if n != "" {
			n = "  " + n
		}
		fmt.Printf("    %4d x%-4d%s\n", k, h[uint16(k)], n)
	}
}

// thingName covers the common doomednums for readability; unknown ones
// print blank.
func thingName(t uint16) string {
	switch t {
	case 1:
		return "Player 1 start"
	case 2:
		return "Player 2 start"
	case 3:
		return "Player 3 start"
	case 4:
		return "Player 4 start"
	case 11:
		return "Deathmatch start"
	case 14:
		return "Teleport destination"
	// Monsters
	case 3004:
		return "Zombieman"
	case 9:
		return "Shotgun guy"
	case 3001:
		return "Imp"
	case 3002:
		return "Demon (pinky)"
	case 58:
		return "Spectre"
	case 3006:
		return "Lost soul"
	case 3005:
		return "Cacodemon"
	case 3003:
		return "Baron of Hell"
	case 69:
		return "Hell knight"
	case 68:
		return "Arachnotron"
	case 71:
		return "Pain elemental"
	case 66:
		return "Revenant"
	case 67:
		return "Mancubus"
	case 64:
		return "Arch-vile"
	case 88:
		return "Romero head (boss target)"
	case 89:
		return "Monster spawner"
	case 7:
		return "Spider mastermind"
	case 16:
		return "Cyberdemon"
	case 84:
		return "Wolfenstein SS"
	case 65:
		return "Chaingunner"
	case 72:
		return "Commander Keen"
	// Weapons
	case 2001:
		return "Shotgun"
	case 82:
		return "Super shotgun"
	case 2002:
		return "Chaingun"
	case 2003:
		return "Rocket launcher"
	case 2004:
		return "Plasma rifle"
	case 2005:
		return "Chainsaw"
	case 2006:
		return "BFG9000"
	// Ammo
	case 2007:
		return "Clip"
	case 2008:
		return "Shells"
	case 2010:
		return "Rocket"
	case 2047:
		return "Cell"
	case 2048:
		return "Box of ammo"
	case 2049:
		return "Box of shells"
	case 2046:
		return "Box of rockets"
	case 17:
		return "Cell pack"
	// Health/armor/powerups
	case 2011:
		return "Stimpack"
	case 2012:
		return "Medikit"
	case 2013:
		return "Soulsphere"
	case 2014:
		return "Health bonus"
	case 2015:
		return "Armor bonus"
	case 2018:
		return "Armor"
	case 2019:
		return "Megaarmor"
	case 2022:
		return "Invulnerability"
	case 2023:
		return "Berserk"
	case 2024:
		return "Partial invisibility"
	case 2025:
		return "Radiation suit"
	case 2026:
		return "Computer map"
	case 2045:
		return "Light amp visor"
	case 83:
		return "Megasphere"
	// Keys
	case 5:
		return "Blue keycard"
	case 40:
		return "Blue skull key"
	case 6:
		return "Yellow keycard"
	case 39:
		return "Yellow skull key"
	case 13:
		return "Red keycard"
	case 38:
		return "Red skull key"
	}
	return ""
}
