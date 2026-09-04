package assets

import (
	"strconv"
	"strings"
)

// voxelEntry is one VOXELDEF mapping: a sprite frame -> a KVX file basename
// (lowercase, no directory, no ".kvx") plus its orientation tweak.
type voxelEntry struct {
	file        string
	angleOffset float64 // degrees, added to the thing's facing (VOXELDEF AngleOffset)
	scale       float64 // multiplies the model's voxel size (VOXELDEF Scale); 1 if unset
}

// parseVoxeldef parses a VOXELDEF lump in the syntax Cheello's Voxel Doom
// uses — one entry per line:
//
//	<sprite><frame> = "<basename>" [{ AngleOffset = N  Scale = f }]
//
// e.g.  trooa = "trooa" {}          (imp, frame A -> voxels/trooa.kvx)
//
//	clipa = "clipa" { AngleOffset = 270 }
//
// C-style /* */ blocks and // line comments are stripped first; a property
// block that is behind a // (e.g. `misla = "misla" //{ UseActorPitch }`) is
// therefore just a plain mapping with default orientation. The returned map
// is keyed by the lowercase "<sprite><frame>" string.
func parseVoxeldef(text string) map[string]voxelEntry {
	text = stripBlockComments(text)
	out := make(map[string]voxelEntry)

	for _, raw := range strings.Split(text, "\n") {
		line := raw
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(line[:eq]))
		rest := strings.TrimSpace(line[eq+1:])

		q1 := strings.IndexByte(rest, '"')
		if q1 < 0 {
			continue
		}
		q2 := strings.IndexByte(rest[q1+1:], '"')
		if q2 < 0 {
			continue
		}
		file := strings.ToLower(strings.TrimSpace(rest[q1+1 : q1+1+q2]))
		if !validVoxelKey(key) || file == "" {
			continue
		}

		e := voxelEntry{file: file, scale: 1}
		if b1 := strings.IndexByte(rest, '{'); b1 >= 0 {
			body := rest[b1+1:]
			if b2 := strings.IndexByte(body, '}'); b2 >= 0 {
				body = body[:b2]
			}
			if v, ok := propValue(body, "angleoffset"); ok {
				e.angleOffset = v
			}
			if v, ok := propValue(body, "scale"); ok && v > 0 {
				e.scale = v
			}
		}
		out[key] = e
	}
	return out
}

// validVoxelKey accepts a "<sprite><frame>" key: a 4-character sprite name
// followed by a single frame letter (a..z), the only shape this pack uses.
func validVoxelKey(k string) bool {
	if len(k) != 5 {
		return false
	}
	if k[4] < 'a' || k[4] > 'z' {
		return false
	}
	for i := 0; i < 4; i++ {
		c := k[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '[' || c == ']' || c == '\\') {
			return false
		}
	}
	return true
}

// propValue finds `name = <number>` (case-insensitive) inside a property
// block body and returns the number.
func propValue(body, name string) (float64, bool) {
	low := strings.ToLower(body)
	i := strings.Index(low, name)
	if i < 0 {
		return 0, false
	}
	j := strings.IndexByte(body[i:], '=')
	if j < 0 {
		return 0, false
	}
	tail := strings.TrimSpace(body[i+j+1:])
	end := 0
	for end < len(tail) && (tail[end] == '.' || tail[end] == '-' || tail[end] == '+' ||
		(tail[end] >= '0' && tail[end] <= '9')) {
		end++
	}
	if end == 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(tail[:end], 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// stripBlockComments removes every /* ... */ span (voxel packs wrap their
// disabled projectile entries in them).
func stripBlockComments(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if i+1 < len(s) && s[i] == '/' && s[i+1] == '*' {
			j := strings.Index(s[i+2:], "*/")
			if j < 0 {
				break // unterminated — drop the rest
			}
			i += 2 + j + 2
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
