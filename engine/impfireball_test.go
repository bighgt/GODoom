package engine

import "testing"

// impShotAt is an in-flight imp fireball thrown by imp at (x, y, z).
func impShotAt(g *Game, imp *Mobj, x, y, z float64) *Mobj {
	ball := g.P_SpawnMobj(x, y, z, MT_TROOPSHOT)
	ball.Target = imp
	return ball
}

// The rendered fireball's burst splashes nearby things — falling off with
// distance — but spares the thing it hit squarely, the imp that threw it,
// other imps, and anything outside the radius. Off, there's no splash.
func TestImpFireballSplash(t *testing.T) {
	for _, on := range []bool{true, false} {
		g := &Game{impFireballs: on}
		g.Player = DefaultPlayerStats()
		g.playerMobj = &Mobj{Type: MT_PLAYER, Info: &mobjInfo[MT_PLAYER], Flags: MF_SHOOTABLE,
			Health: g.Player.Health, X: 30, Radius: 16, Height: 56}
		imp := g.P_SpawnMobj(-200, 0, 0, MT_TROOP)
		otherImp := g.P_SpawnMobj(0, 30, 0, MT_TROOP)
		struck := g.P_SpawnMobj(0, -30, 0, MT_SERGEANT)
		far := g.P_SpawnMobj(400, 0, 0, MT_SERGEANT)
		hp := map[*Mobj]int{imp: imp.Health, otherImp: otherImp.Health, struck: struck.Health, far: far.Health}

		ball := impShotAt(g, imp, 0, 0, 20)
		ball.directHit = struck
		g.explodeMissile(ball)

		lost := DefaultPlayerStats().Health - g.Player.Health
		if on {
			// Player's edge is 30-16 = 14 units out: 16 * (1 - 14/64) = 12.
			if lost != 12 {
				t.Errorf("player lost %d health to the splash, want 12", lost)
			}
		} else if lost != 0 {
			t.Errorf("splash with impFireballs off: player lost %d", lost)
		}
		for mo, h := range hp {
			if mo.Health != h {
				t.Errorf("on=%v: type %d took splash damage (%d -> %d)", on, mo.Type, h, mo.Health)
			}
		}
	}
}

// In flight the ball is drawn as the procedural core + halo in the
// additive pass, not as a BAL1 sprite or model; exploding, it isn't drawn.
func TestImpFireballDrawnProcedurally(t *testing.T) {
	g := &Game{impFireballs: true}
	g.Camera.FOVDeg = 90
	ball := impShotAt(g, nil, 100, 0, 20)
	g.hwAddSprites = g.hwAddSprites[:0]
	g.appendFireballSprites(ball, &impFireball, ball.X, ball.Y, ball.Z)
	if len(g.hwAddSprites) != 2 {
		t.Fatalf("in flight: %d additive sprites, want core + halo", len(g.hwAddSprites))
	}
	for _, s := range g.hwAddSprites {
		if !s.FullBright || s.Tex == nil || s.Tex.OffsetX != s.Tex.Width/2 || s.Tex.OffsetY != s.Tex.Height/2 {
			t.Errorf("sprite %+v: want a centred, full-bright texture", s)
		}
		if want := float32(ball.Z + ball.Height/2); s.Z != want {
			t.Errorf("drawn at z %v, want the ball's centre %v", s.Z, want)
		}
	}
	g.explodeMissile(ball)
	g.hwAddSprites = g.hwAddSprites[:0]
	g.appendFireballSprites(ball, &impFireball, ball.X, ball.Y, ball.Z)
	if len(g.hwAddSprites) != 0 {
		t.Errorf("exploding ball still drew %d sprites", len(g.hwAddSprites))
	}
	if len(g.particles) == 0 {
		t.Error("impact spawned no burst particles")
	}
}

// The trail sheds embers at a fixed rate whatever the frame rate.
func TestImpFireballTrailRate(t *testing.T) {
	for _, steps := range []int{10, 100} {
		g := &Game{impFireballs: true}
		ball := impShotAt(g, nil, 0, 0, 20)
		ball.MomX = 10
		for i := 0; i < steps; i++ {
			g.updateMobjFire(0.5 / float64(steps))
		}
		if n := len(g.particles); n < 38 || n > 44 {
			t.Errorf("%d frames over 0.5s shed %d embers, want ~%.0f", steps, n, 0.5/impTrailEvery)
		}
	}
}

// The procedural core frames have fire in the middle and nothing at the
// corners, and every visible texel is opaque for the sprite alpha test.
func TestFireballArt(t *testing.T) {
	core := fireballCoreFrames()
	if len(core) != impCoreFrames {
		t.Fatalf("%d core frames, want %d", len(core), impCoreFrames)
	}
	for f, img := range core {
		n := img.Width
		mid := ((n/2)*n + n/2) * 4
		if img.Pix[mid+3] != 255 || img.Pix[mid] < 200 {
			t.Errorf("frame %d: centre texel %v, want bright and opaque", f, img.Pix[mid:mid+4])
		}
		if img.Pix[3] != 0 {
			t.Errorf("frame %d: corner texel visible", f)
		}
	}
}

// The cacodemon's ball: drawn procedurally when cacoFireballs is on (and
// the vanilla way when off), a little larger than the imp's, a red core
// in blue flames.
func TestCacoFireballArt(t *testing.T) {
	g := &Game{cacoFireballs: true}
	ball := g.P_SpawnMobj(0, 0, 20, MT_HEADSHOT)
	if g.renderedFireball(ball) != &cacoFireball {
		t.Fatal("caco shot isn't drawn as the rendered caco fireball")
	}
	if (&Game{impFireballs: true}).renderedFireball(ball) != nil {
		t.Error("caco shot rendered with cacoFireballs off")
	}

	imp, caco := impFireballArt(), cacoFireballArt()
	if !(caco.core[0].TexelsPerUnit < imp.core[0].TexelsPerUnit && caco.halo.TexelsPerUnit < imp.halo.TexelsPerUnit) {
		t.Error("caco fireball isn't drawn larger than the imp's")
	}
	blue := 0
	for f, img := range caco.core {
		n := img.Width
		mid := ((n/2)*n + n/2) * 4
		if r, b := img.Pix[mid], img.Pix[mid+2]; img.Pix[mid+3] != 255 || r < 200 || b > r {
			t.Errorf("frame %d: centre texel %v, want a bright red-hot core", f, img.Pix[mid:mid+4])
		}
		for i := 0; i < len(img.Pix); i += 4 {
			r, b := int(img.Pix[i]), int(img.Pix[i+2])
			if img.Pix[i+3] == 255 && b > 120 && b > 2*r {
				blue++
			}
		}
	}
	if blue == 0 {
		t.Error("caco fireball has no blue flames")
	}
}

// The caco's splash reaches further than the imp's and spares other
// cacodemons (no caco-on-caco infighting from a splash).
func TestCacoFireballSplash(t *testing.T) {
	g := &Game{cacoFireballs: true}
	g.Player = DefaultPlayerStats()
	// Player's edge 82-16 = 66 units out: past the imp's 64, inside the caco's 72.
	g.playerMobj = &Mobj{Type: MT_PLAYER, Info: &mobjInfo[MT_PLAYER], Flags: MF_SHOOTABLE,
		Health: g.Player.Health, X: 82, Radius: 16, Height: 56}
	thrower := g.P_SpawnMobj(-300, 0, 0, MT_HEAD)
	other := g.P_SpawnMobj(0, 40, 0, MT_HEAD)
	hp := other.Health
	ball := g.P_SpawnMobj(0, 0, 20, MT_HEADSHOT)
	ball.Target = thrower
	g.explodeMissile(ball)
	if lost := DefaultPlayerStats().Health - g.Player.Health; lost <= 0 {
		t.Error("caco splash didn't reach the player 66 units out")
	}
	if other.Health != hp {
		t.Errorf("caco splash hurt another cacodemon (%d -> %d)", hp, other.Health)
	}
}
