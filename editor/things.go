package editor

import "image/color"

// thingCat groups THINGS types for the palette and the 2D view's colour
// coding.
type thingCat int

const (
	catOther thingCat = iota
	catStart
	catMonster
	catWeapon
	catAmmo
	catHealth
	catKey
	catPowerup
	catDecor
	catLight
)

func (c thingCat) color() color.RGBA {
	switch c {
	case catStart:
		return color.RGBA{0x4a, 0xd9, 0x8a, 0xff}
	case catMonster:
		return color.RGBA{0xe0, 0x66, 0x5a, 0xff}
	case catWeapon:
		return color.RGBA{0xd9, 0xa5, 0x4a, 0xff}
	case catAmmo:
		return color.RGBA{0xc8, 0xc0, 0x50, 0xff}
	case catHealth:
		return color.RGBA{0x5a, 0xb0, 0xd9, 0xff}
	case catKey:
		return color.RGBA{0xd0, 0x50, 0xd0, 0xff}
	case catPowerup:
		return color.RGBA{0x8a, 0x8a, 0xe0, 0xff}
	case catLight:
		return color.RGBA{0xf0, 0xe0, 0x90, 0xff}
	case catDecor:
		return color.RGBA{0x88, 0x88, 0x88, 0xff}
	}
	return color.RGBA{0xb0, 0xb0, 0xb0, 0xff}
}

// ThingDef is one entry of the editor's THINGS catalogue.
type ThingDef struct {
	Num    int
	Name   string
	Cat    thingCat
	Sprite string // 4-letter prefix; "" if none — the palette shows a swatch
}

// thingDefs is the built-in Doom / Doom II catalogue, doomednum-ordered by
// category. Not exhaustive (no Boom/MBF specials) but covers everything a
// vanilla map places. Heretic types and an engine.MobjInfo cross-check come
// later.
var thingDefs = []ThingDef{
	// player starts / co-op / deathmatch / teleport landing
	{1, "Player 1 start", catStart, "PLAY"},
	{2, "Player 2 start", catStart, "PLAY"},
	{3, "Player 3 start", catStart, "PLAY"},
	{4, "Player 4 start", catStart, "PLAY"},
	{11, "Deathmatch start", catStart, "PLAY"},
	{14, "Teleport destination", catStart, ""},

	// monsters
	{3004, "Zombieman", catMonster, "POSS"},
	{9, "Shotgun guy", catMonster, "SPOS"},
	{65, "Heavy weapon dude", catMonster, "CPOS"},
	{3001, "Imp", catMonster, "TROO"},
	{3002, "Pinky demon", catMonster, "SARG"},
	{58, "Spectre", catMonster, "SARG"},
	{3006, "Lost soul", catMonster, "SKUL"},
	{3005, "Cacodemon", catMonster, "HEAD"},
	{69, "Hell knight", catMonster, "BOS2"},
	{3003, "Baron of Hell", catMonster, "BOSS"},
	{68, "Arachnotron", catMonster, "BSPI"},
	{71, "Pain elemental", catMonster, "PAIN"},
	{66, "Revenant", catMonster, "SKEL"},
	{67, "Mancubus", catMonster, "FATT"},
	{64, "Arch-vile", catMonster, "VILE"},
	{7, "Spider mastermind", catMonster, "SPID"},
	{16, "Cyberdemon", catMonster, "CYBR"},
	{88, "Icon of Sin boss target", catMonster, "BBRN"},

	// weapons
	{2005, "Chainsaw", catWeapon, "CSAW"},
	{2001, "Shotgun", catWeapon, "SHOT"},
	{82, "Super shotgun", catWeapon, "SGN2"},
	{2002, "Chaingun", catWeapon, "MGUN"},
	{2003, "Rocket launcher", catWeapon, "LAUN"},
	{2004, "Plasma rifle", catWeapon, "PLAS"},
	{2006, "BFG9000", catWeapon, "BFUG"},

	// ammo
	{2007, "Ammo clip", catAmmo, "CLIP"},
	{2048, "Box of bullets", catAmmo, "AMMO"},
	{2008, "4 shells", catAmmo, "SHEL"},
	{2049, "Box of shells", catAmmo, "SBOX"},
	{2010, "Rocket", catAmmo, "ROCK"},
	{2046, "Box of rockets", catAmmo, "BROK"},
	{2047, "Cell charge", catAmmo, "CELL"},
	{17, "Cell pack", catAmmo, "CELP"},
	{8, "Backpack", catAmmo, "BPAK"},

	// health & armor
	{2011, "Stimpack", catHealth, "STIM"},
	{2012, "Medikit", catHealth, "MEDI"},
	{2014, "Health bonus", catHealth, "BON1"},
	{2015, "Armor bonus", catHealth, "BON2"},
	{2018, "Green armor", catHealth, "ARM1"},
	{2019, "Blue armor", catHealth, "ARM2"},
	{83, "Megasphere", catHealth, "MEGA"},
	{2013, "Soulsphere", catHealth, "SOUL"},

	// keys
	{5, "Blue keycard", catKey, "BKEY"},
	{40, "Blue skull key", catKey, "BSKU"},
	{6, "Yellow keycard", catKey, "YKEY"},
	{39, "Yellow skull key", catKey, "YSKU"},
	{13, "Red keycard", catKey, "RKEY"},
	{38, "Red skull key", catKey, "RSKU"},

	// powerups
	{2022, "Invulnerability", catPowerup, "PINV"},
	{2023, "Berserk", catPowerup, "PSTR"},
	{2024, "Partial invisibility", catPowerup, "PINS"},
	{2025, "Radiation suit", catPowerup, "SUIT"},
	{2026, "Computer map", catPowerup, "PMAP"},
	{2045, "Light amp visor", catPowerup, "PVIS"},

	// lamps & lights
	{2028, "Floor lamp", catLight, "COLU"},
	{34, "Candle", catLight, "CAND"},
	{35, "Candelabra", catLight, "CBRA"},
	{85, "Tall tech lamp", catLight, "TLMP"},
	{86, "Short tech lamp", catLight, "TLP2"},
	{44, "Tall blue torch", catLight, "TBLU"},
	{45, "Tall green torch", catLight, "TGRN"},
	{46, "Tall red torch", catLight, "TRED"},
	{55, "Short blue torch", catLight, "SMBT"},
	{56, "Short green torch", catLight, "SMGT"},
	{57, "Short red torch", catLight, "SMRT"},
	{70, "Burning barrel", catLight, "FCAN"},

	// decorations (a useful subset)
	{2035, "Explosive barrel", catDecor, "BAR1"},
	{48, "Tall techno pillar", catDecor, "ELEC"},
	{30, "Tall green pillar", catDecor, "COL1"},
	{32, "Tall red pillar", catDecor, "COL3"},
	{31, "Short green pillar", catDecor, "COL2"},
	{33, "Short red pillar", catDecor, "COL4"},
	{37, "Skull rock", catDecor, "SKRK"},
	{47, "Stalagmite", catDecor, "SMIT"},
	{43, "Burnt tree", catDecor, "TRE1"},
	{54, "Big brown tree", catDecor, "TRE2"},
	{28, "Impaled twitching body", catDecor, "POL2"},
	{29, "Skull on pole", catDecor, "POL3"},
	{25, "Impaled human", catDecor, "POL1"},
	{27, "Skull on stake", catDecor, "POL4"},
	{10, "Bloody mess", catDecor, "PLAY"},
	{24, "Pool of blood & flesh", catDecor, "POL5"},
	{50, "Hanging victim, arms out", catDecor, "GOR2"},
	{52, "Hanging pair of legs", catDecor, "GOR4"},
	{53, "Hanging leg", catDecor, "GOR5"},
	{72, "Commander Keen", catDecor, "KEEN"},
}

var thingByNum = func() map[int]ThingDef {
	m := make(map[int]ThingDef, len(thingDefs))
	for _, d := range thingDefs {
		m[d.Num] = d
	}
	return m
}()

func thingCategory(num int) thingCat {
	if d, ok := thingByNum[num]; ok {
		return d.Cat
	}
	return catOther
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
