package assets

import (
	"sync"
	"testing"

	"twopointfive/wad"
)

// TestConcurrentSpriteFlatWallTextureConverge exercises WallTexture/Flat/
// Sprite's RWMutex fast-path (see the mu field's doc comment): many
// goroutines resolving the SAME name at once — the raster renderer's own
// access pattern, since Render splits the frame into column strips that
// each walk the whole BSP and resolve their segs' textures independently —
// must never race (run under `go test -race`) and must all converge on the
// exact same decoded *RGBA, proving the double-checked lock's "another
// goroutine already filled this" re-check actually prevents two goroutines
// from both losing the race, each decoding and caching its own separate
// copy under the same name.
func TestConcurrentSpriteFlatWallTextureConverge(t *testing.T) {
	w, err := wad.Load("../testdata/DOOM1.WAD")
	if err != nil {
		t.Skipf("shareware WAD unavailable: %v", err)
	}
	tx, err := New(w)
	if err != nil {
		t.Fatalf("assets.New: %v", err)
	}

	const goroutines = 32
	run := func(name string, resolve func(string) (*RGBA, bool)) {
		t.Helper()
		var wg sync.WaitGroup
		results := make([]*RGBA, goroutines)
		oks := make([]bool, goroutines)
		for i := 0; i < goroutines; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				results[i], oks[i] = resolve(name)
			}(i)
		}
		wg.Wait()
		if !oks[0] {
			t.Skipf("%q not resolvable in this WAD", name)
		}
		want := results[0]
		for i, got := range results {
			if !oks[i] {
				t.Errorf("goroutine %d: ok=false, want true", i)
				continue
			}
			if got != want {
				t.Errorf("goroutine %d: got a different *RGBA than goroutine 0 for %q — the cache produced two separate decodes for one name", i, name)
			}
		}
	}

	run("STARTAN3", tx.WallTexture)
	run("FLOOR4_8", tx.Flat)
	run("PISGA0", tx.Sprite)
}
