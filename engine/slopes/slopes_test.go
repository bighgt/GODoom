package slopes

import (
	"testing"
	"time"
)

func TestParseSlopes(t *testing.T) {
	src := []byte(`
// a comment before the first map
MAP01 {
    sector 12 floor   plane 0 0 64  128 0 64  0 128 96
    sector 12 ceiling plane 0 0 192 128 0 192 0 128 192
}
MAP02 {
    sector 3 floor plane 0 0 0  64 0 0  0 64 32
}
`)
	planes, floors, errs := Parse(src)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(planes) != 2 {
		t.Fatalf("parsed %d maps, want 2: %+v", len(planes), planes)
	}
	if len(floors) != 0 {
		t.Fatalf("expected no extrafloor defs, got %+v", floors)
	}

	m1 := planes["MAP01"]
	if len(m1) != 2 {
		t.Fatalf("MAP01: %d defs, want 2", len(m1))
	}
	if m1[0].SectorIndex != 12 || m1[0].Ceiling {
		t.Errorf("MAP01[0] = %+v, want sector 12 floor", m1[0])
	}
	if m1[0].P1 != [3]float64{0, 0, 64} || m1[0].P3 != [3]float64{0, 128, 96} {
		t.Errorf("MAP01[0] points = %+v", m1[0])
	}
	if m1[1].SectorIndex != 12 || !m1[1].Ceiling {
		t.Errorf("MAP01[1] = %+v, want sector 12 ceiling", m1[1])
	}

	m2 := planes["MAP02"]
	if len(m2) != 1 || m2[0].SectorIndex != 3 {
		t.Fatalf("MAP02 = %+v, want one sector-3 def", m2)
	}
}

func TestParseExtraFloors(t *testing.T) {
	src := []byte(`
MAP01 {
    sector 20 extrafloor 45
    sector 20 extrafloor 46
    sector 12 floor plane 0 0 64 128 0 64 0 128 96
}
`)
	planes, floors, errs := Parse(src)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	m1 := floors["MAP01"]
	if len(m1) != 2 {
		t.Fatalf("MAP01 extrafloors = %+v, want 2", m1)
	}
	if m1[0].TargetSector != 20 || m1[0].ControlSector != 45 {
		t.Errorf("MAP01[0] = %+v, want target 20 control 45", m1[0])
	}
	if m1[1].TargetSector != 20 || m1[1].ControlSector != 46 {
		t.Errorf("MAP01[1] = %+v, want target 20 control 46", m1[1])
	}
	if len(planes["MAP01"]) != 1 {
		t.Fatalf("MAP01 planes = %+v, want the one floor-plane def to still parse", planes["MAP01"])
	}
}

func TestParseSlopesMapNameIsUppercased(t *testing.T) {
	planes, _, errs := Parse([]byte("map01 { sector 0 floor plane 0 0 0 1 0 0 0 1 0 }"))
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if _, ok := planes["MAP01"]; !ok {
		t.Fatalf("expected key MAP01, got %+v", planes)
	}
}

// TestParseSlopesMalformedLineIsSkippedNotFatal: a bad line reports an
// error but doesn't stop the rest of the file from parsing — the same
// "never fails outright" convention mapinfo/gldefs follow.
func TestParseSlopesMalformedLineIsSkippedNotFatal(t *testing.T) {
	src := []byte(`
MAP01 {
    sector nonsense floor plane 0 0 0 1 0 0 0 1 0
    sector 5 floor plane 0 0 0 1 0 0 0 1 0
}
`)
	planes, _, errs := Parse(src)
	if len(errs) == 0 {
		t.Fatal("expected an error for the malformed line")
	}
	if len(planes["MAP01"]) != 1 || planes["MAP01"][0].SectorIndex != 5 {
		t.Fatalf("MAP01 = %+v, want the one good def (sector 5) to still parse", planes["MAP01"])
	}
}

// TestParseSlopesNeverHangs guards the parser's error-recovery paths
// (parseSectorLine always consumes at least one token on failure) against
// a regression that would spin forever instead of erroring out.
func TestParseSlopesNeverHangs(t *testing.T) {
	cases := []string{
		"MAP01 { sector 1 sideways plane 0 0 0 1 0 0 0 1 0 }", // bad floor/ceiling keyword
		"MAP01 { sector 1 floor oops 0 0 0 1 0 0 0 1 0 }",      // bad 'plane' keyword
		"MAP01 { sector 1 extrafloor nonsense }",               // bad control sector index
		"MAP01 { garbage garbage garbage }",
		"MAP01 {",     // unterminated block
		"not a block", // no braces at all
	}
	for _, src := range cases {
		done := make(chan struct{})
		go func() {
			Parse([]byte(src))
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatalf("Parse(%q) did not return — likely an infinite loop", src)
		}
	}
}
