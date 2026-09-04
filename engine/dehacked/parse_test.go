package dehacked

import "testing"

func TestParseThingAndFrame(t *testing.T) {
	src := []byte(`Patch File for DeHackEd v3.0
Doom version = 21
Patch format = 6

Thing 12 (Imp)
Hit points = 120
Speed = 12
Bits = SOLID+SHOOTABLE+COUNTKILL
Missile damage = 4

Frame 442
Sprite number = 5
Duration = 3
Next frame = 443

# a trailing comment
`)
	p, errs := Parse(src)
	if len(errs) != 0 {
		t.Fatalf("errs: %v", errs)
	}
	if p.DoomVersion != 21 || p.PatchFormat != 6 {
		t.Errorf("header: version=%d format=%d", p.DoomVersion, p.PatchFormat)
	}
	imp := p.Things[12]
	if hp, _ := imp.Int("Hit Points"); hp != 120 { // case-insensitive
		t.Errorf("imp hit points = %d, want 120", hp)
	}
	if sp, _ := imp.Int("speed"); sp != 12 {
		t.Errorf("imp speed = %d", sp)
	}
	if b, _ := imp.Get("Bits"); b != "SOLID+SHOOTABLE+COUNTKILL" {
		t.Errorf("imp bits = %q", b)
	}
	if got := ParseBits("SOLID+SHOOTABLE|COUNTKILL, MISSILE"); len(got) != 4 || got[3] != "MISSILE" {
		t.Errorf("ParseBits = %v", got)
	}
	fr := p.Frames[442]
	if d, _ := fr.Int("Duration"); d != 3 {
		t.Errorf("frame 442 duration = %d", d)
	}
	if nx, _ := fr.Int("Next frame"); nx != 443 {
		t.Errorf("frame 442 next = %d", nx)
	}
}

func TestParseBexSections(t *testing.T) {
	src := []byte(`[STRINGS]
HUSTR_E1M1 = A New Beginning
GOTARMOR = Picked up some armor.\nStill green.

[PARS]
par 1 1 45
par 7 90

[CODEPTR]
Frame 47 = Punch
Frame 48 = ReFire

[SPRITES]
57 = ZZZZ
`)
	p, errs := Parse(src)
	if len(errs) != 0 {
		t.Fatalf("errs: %v", errs)
	}
	if p.Strings["HUSTR_E1M1"] != "A New Beginning" {
		t.Errorf("HUSTR_E1M1 = %q", p.Strings["HUSTR_E1M1"])
	}
	if p.Strings["GOTARMOR"] != "Picked up some armor.\nStill green." {
		t.Errorf("GOTARMOR = %q", p.Strings["GOTARMOR"])
	}
	if len(p.Pars) != 2 || p.Pars[0] != (Par{1, 1, 45}) || p.Pars[1] != (Par{0, 7, 90}) {
		t.Errorf("pars = %+v", p.Pars)
	}
	if p.CodePtrs[47] != "Punch" || p.CodePtrs[48] != "ReFire" {
		t.Errorf("codeptrs = %v", p.CodePtrs)
	}
	if p.Sprites[57] != "ZZZZ" {
		t.Errorf("sprite 57 = %q", p.Sprites[57])
	}
}

func TestParsePointerAndTextAndMisc(t *testing.T) {
	src := []byte("Pointer 15 (Frame 15)\nCodep Frame = 483\n\n" +
		"Misc 0\nInitial Health = 150\nMax Armor = 250\n\n" +
		"Cheat 0\nGod mode = tehgod\n\n" +
		"Text 4 4\nTROOXXXX")
	p, errs := Parse(src)
	if len(errs) != 0 {
		t.Fatalf("errs: %v", errs)
	}
	if p.Pointers[15] != 483 {
		t.Errorf("pointer 15 -> %d, want 483", p.Pointers[15])
	}
	if h, _ := p.Misc.Int("Initial Health"); h != 150 {
		t.Errorf("misc initial health = %d", h)
	}
	if c, _ := p.Cheat.Get("God Mode"); c != "tehgod" {
		t.Errorf("cheat god = %q", c)
	}
	if len(p.Texts) != 1 || p.Texts[0].Old != "TROO" || p.Texts[0].New != "XXXX" {
		t.Errorf("text sub = %+v", p.Texts)
	}
}

func TestParseToleratesJunk(t *testing.T) {
	p, errs := Parse([]byte("Thing 5\nHit points = 90\n\nWibble 3\nfoo = bar\n\nFrame 1\nDuration = 2\n"))
	if len(errs) == 0 {
		t.Error("expected an error for the `Wibble` section")
	}
	if hp, _ := p.Things[5].Int("hit points"); hp != 90 {
		t.Errorf("recovery: thing 5 hp = %d", hp)
	}
	if d, _ := p.Frames[1].Int("duration"); d != 2 {
		t.Errorf("recovery: frame 1 after junk not parsed (d=%d)", d)
	}
}
