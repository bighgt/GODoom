package wad

import "testing"

func TestDecodeDMXSoundEffects(t *testing.T) {
	w, err := Load(testdataPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	for _, name := range []string{"DSPISTOL", "DSSHOTGN", "DSDOROPN", "DSDORCLS"} {
		raw, ok := w.Find(name)
		if !ok {
			t.Errorf("%s: lump not found", name)
			continue
		}
		snd, err := DecodeDMX(raw)
		if err != nil {
			t.Errorf("DecodeDMX(%s): %v", name, err)
			continue
		}
		if snd.SampleRate != 11025 && snd.SampleRate != 22050 {
			t.Errorf("%s: unexpected sample rate %d", name, snd.SampleRate)
		}
		if len(snd.Samples) == 0 {
			t.Errorf("%s: decoded zero samples", name)
		}
		t.Logf("%s: %d Hz, %d samples (%.2fs)", name, snd.SampleRate, len(snd.Samples), float64(len(snd.Samples))/float64(snd.SampleRate))
	}
}

func TestDecodeMusMusic(t *testing.T) {
	w, err := Load(testdataPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	raw, ok := w.Find("D_E1M1")
	if !ok {
		t.Fatal("D_E1M1 lump not found")
	}
	events, err := DecodeMus(raw)
	if err != nil {
		t.Fatalf("DecodeMus: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("DecodeMus returned no events")
	}

	var noteOns, totalTicks int
	channels := map[byte]bool{}
	for _, e := range events {
		totalTicks += e.DeltaTicks
		channels[e.Channel] = true
		if e.Type == MusNoteOn {
			noteOns++
		}
	}
	if noteOns == 0 {
		t.Error("no note-on events decoded")
	}
	t.Logf("D_E1M1: %d events, %d note-ons, %d distinct channels, %d total ticks (~%.1fs at default tempo)",
		len(events), noteOns, len(channels), totalTicks, float64(totalTicks)*60.0/120.0/70.0)
}
