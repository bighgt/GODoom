// Command gensky procedurally generates the game's Mars daytime skybox
// texture (assets/skybox/mars_sky.png) — a 4K panorama used in place of the
// WAD's own low-resolution SKY1, loaded separately from any WAD (see
// game_design.txt section 12.9). Not part of the main build; run with
// `go run ./tools/gensky` from the repo root whenever the skybox needs
// regenerating or retuning.
//
// Coloring is based on NASA's own descriptions of what Mars actually looks
// like (Curiosity/Perseverance imagery, not artistic guesswork): a rusty,
// dusty orange-brown daytime sky (iron-oxide dust suspended in the thin
// CO2 atmosphere scatters red/orange light across the whole sky), with a
// distinctive pale-blue "halo" of forward-scattered light close to the sun
// — the same fine dust that reddens the rest of the sky lets blue light
// through more efficiently right around the sun's position. See:
// https://science.nasa.gov/solar-system/planets/mars/what-does-a-sunrise-sunset-look-like-on-mars/
package main

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"math/rand"
	"os"
)

const (
	width  = 4096
	height = 2048
)

// noiseGen is simple seeded value noise (bilinear-interpolated random grid
// points, smoothstepped) — enough for organic-looking dust/haze texture
// without pulling in an external noise library. u wraps around 1.0 so it
// tiles across a full horizontal turn.
type noiseGen struct {
	grid           [][]float64
	cellsX, cellsY int
}

func newNoise(cellsX, cellsY int, seed int64) *noiseGen {
	r := rand.New(rand.NewSource(seed))
	grid := make([][]float64, cellsY+1)
	for y := range grid {
		grid[y] = make([]float64, cellsX+1)
		for x := range grid[y] {
			grid[y][x] = r.Float64()
		}
	}
	return &noiseGen{grid: grid, cellsX: cellsX, cellsY: cellsY}
}

func smoothstep(t float64) float64 { return t * t * (3 - 2*t) }
func lerp(a, b, t float64) float64 { return a + (b-a)*t }
func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func (n *noiseGen) at(u, v float64) float64 {
	u -= math.Floor(u)
	if v < 0 {
		v = 0
	}
	fx := u * float64(n.cellsX)
	fy := v * float64(n.cellsY)
	x0 := int(math.Floor(fx)) % n.cellsX
	y0 := int(math.Floor(fy))
	if y0 >= n.cellsY {
		y0 = n.cellsY - 1
	}
	x1 := (x0 + 1) % n.cellsX
	y1 := y0 + 1
	tx := smoothstep(fx - math.Floor(fx))
	ty := smoothstep(fy - math.Floor(fy))
	top := lerp(n.grid[y0][x0], n.grid[y0][x1], tx)
	bot := lerp(n.grid[y1][x0], n.grid[y1][x1], tx)
	return lerp(top, bot, ty)
}

func fbm(octaves []*noiseGen, u, v float64) float64 {
	sum, amp, total := 0.0, 1.0, 0.0
	for _, g := range octaves {
		sum += g.at(u, v) * amp
		total += amp
		amp *= 0.5
	}
	return sum / total
}

type rgb [3]float64

func lerpRGB(a, b rgb, t float64) rgb {
	return rgb{lerp(a[0], b[0], t), lerp(a[1], b[1], t), lerp(a[2], b[2], t)}
}

func main() {
	img := image.NewRGBA(image.Rect(0, 0, width, height))

	dust := []*noiseGen{
		newNoise(8, 4, 1),
		newNoise(16, 8, 2),
		newNoise(32, 16, 3),
		newNoise(64, 32, 4),
		newNoise(128, 64, 5),
	}
	haze := newNoise(6, 3, 99)
	// Streaks stretched way out horizontally — real Martian dust haze and
	// distant dust-storm fronts read as long, low horizontal bands rather
	// than isotropic blotches; sampling noise at a squashed u (its cellsX
	// is tiny relative to cellsY) gives exactly that elongation.
	streaks := newNoise(5, 40, 7)

	// Palette straight from the research above: rusty brown at the zenith,
	// warming to a butterscotch-orange midsky, paling to a dusty salmon at
	// the horizon (more atmosphere/dust between the eye and the horizon
	// scatters more light, the same reason Earth's sky pales near its
	// horizon too).
	zenith := rgb{112, 60, 42}
	mid := rgb{198, 132, 88}
	horizonC := rgb{230, 178, 142}
	hazeTint := rgb{225, 150, 110}
	streakTint := rgb{160, 95, 68}
	blueHalo := rgb{120, 145, 185}
	sunColor := rgb{255, 248, 228}

	const (
		sunU, sunV = 0.5, 0.30 // texture-space fraction
		sunRadius  = 0.018     // fraction of height
		haloRadius = 0.12      // fraction of height
	)

	for y := 0; y < height; y++ {
		v := float64(y) / float64(height)
		for x := 0; x < width; x++ {
			u := float64(x) / float64(width)

			// Base vertical gradient: zenith -> mid -> horizon over the
			// upper ~65% of the image; held flat below that (mirroring
			// vanilla Doom's own sky convention of keeping the "busy"
			// detail up top).
			band := clamp01(v / 0.65)
			var base rgb
			switch {
			case v > 0.65:
				base = horizonC
			case band < 0.5:
				base = lerpRGB(zenith, mid, smoothstep(band/0.5))
			default:
				base = lerpRGB(mid, horizonC, smoothstep((band-0.5)/0.5))
			}

			// Fine dust/haze texture, strongest low in the sky where
			// suspended dust catches the most light. Pushed harder than a
			// subtle photographic grain — this is a game skybox, meant to
			// read clearly even downsampled to a 320-wide internal frame.
			n := fbm(dust, u, v*2.0) - 0.5
			hazeAmt := (0.35 + 0.9*clamp01(v/0.7)) * n
			base = lerpRGB(base, hazeTint, clamp01(math.Pow(math.Abs(hazeAmt), 0.7)))

			// Long horizontal dust-storm streaks/bands.
			st := streaks.at(u*0.6, v) - 0.5
			base = lerpRGB(base, streakTint, clamp01(math.Abs(st)*1.4)*clamp01(0.3+v))

			// Broad soft haze banding.
			hz := haze.at(u, v)
			base = lerpRGB(base, hazeTint, 0.18*hz*clamp01(v/0.8))

			// Sun + its pale-blue dust halo — real Mars photography's
			// signature "wrong way round" color cue (blue near the sun,
			// red/orange everywhere else) — see the package doc comment.
			px, py := u*width, v*height
			sx, sy := sunU*width, sunV*height
			d := math.Hypot(px-sx, py-sy) / height
			if d < haloRadius {
				t := 1 - clamp01(d/haloRadius)
				base = lerpRGB(base, blueHalo, math.Pow(t, 1.3)*0.85)
			}
			if d < sunRadius {
				t := 1 - clamp01(d/sunRadius)
				base = lerpRGB(base, sunColor, math.Pow(t, 0.5))
			}

			// Small dither to avoid 8bpc gradient banding.
			dither := (rand.Float64() - 0.5) * 3
			r8 := uint8(clamp01((base[0]+dither)/255) * 255)
			g8 := uint8(clamp01((base[1]+dither)/255) * 255)
			b8 := uint8(clamp01((base[2]+dither)/255) * 255)
			img.Set(x, y, color.RGBA{r8, g8, b8, 255})
		}
	}

	if err := os.MkdirAll("assets/skybox", 0o755); err != nil {
		panic(err)
	}
	f, err := os.Create("assets/skybox/mars_sky.png")
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		panic(err)
	}
}
