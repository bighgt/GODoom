package wad

import "testing"

func TestMusicLumpName(t *testing.T) {
	cases := map[string]string{
		"E1M1":  "D_E1M1",
		"E3M8":  "D_E3M8",
		"e1m1":  "D_E1M1",
		"MAP01": "D_RUNNIN",
		"MAP07": "D_SHAWN",
		"MAP32": "D_ULTIMA",
		"map01": "D_RUNNIN",
		"MAP33": "D_MAP33", // out of range -> falls back to the "D_"+name form
	}
	for in, want := range cases {
		if got := MusicLumpName(in); got != want {
			t.Errorf("MusicLumpName(%q) = %q, want %q", in, got, want)
		}
	}
}
