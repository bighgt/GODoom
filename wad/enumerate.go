package wad

import "strings"

// Enumeration helpers the level editor's texture / flat browsers need. The
// engine itself never lists these — it resolves a texture by name on demand
// (assets.Textures) — so they live in their own file.

// WallTextureNames returns every composite wall-texture name defined in this
// WAD's TEXTURE1 and TEXTURE2 lumps, in lump order (TEXTURE1 first). A WAD
// with neither lump returns nil. Decode errors are skipped, not fatal.
func (w *WAD) WallTextureNames() []string {
	var out []string
	for _, lump := range []string{"TEXTURE1", "TEXTURE2"} {
		b, ok := w.Find(lump)
		if !ok {
			continue
		}
		defs, err := LoadTextureDefs(b)
		if err != nil {
			continue
		}
		for _, d := range defs {
			out = append(out, d.Name)
		}
	}
	return out
}

// FlatNames returns every flat lump name — the entries between an F_START /
// FF_START marker and the matching F_END / FF_END, minus the sub-section
// markers (F1_START, F2_START, ...) themselves and any zero-size lump. Order
// follows the directory. Names that appear more than once (a PWAD flat
// overriding an IWAD one) are returned once, first-seen.
func (w *WAD) FlatNames() []string {
	var out []string
	seen := map[string]bool{}
	depth := 0
	for _, e := range w.Entries {
		n := e.Name
		switch {
		case isStartMarker(n, "F"):
			depth++
		case isEndMarker(n, "F"):
			if depth > 0 {
				depth--
			}
		case depth > 0 && e.Size > 0 && !isSubMarker(n, "F"):
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	return out
}

// SpritePrefixes returns the distinct 4-character sprite-name prefixes found
// between S_START / SS_START and S_END / SS_END markers.
func (w *WAD) SpritePrefixes() []string {
	var out []string
	seen := map[string]bool{}
	depth := 0
	for _, e := range w.Entries {
		n := e.Name
		switch {
		case isStartMarker(n, "S"):
			depth++
		case isEndMarker(n, "S"):
			if depth > 0 {
				depth--
			}
		case depth > 0 && e.Size > 0 && len(n) >= 4:
			p := n[:4]
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	return out
}

// isStartMarker matches "<L>_START" and "<L><L>_START" (F_START, FF_START,
// S_START, SS_START). isEndMarker is the same for _END. isSubMarker matches
// the numbered sub-section markers like F1_START / F2_END.
func isStartMarker(n, l string) bool {
	return n == l+"_START" || n == l+l+"_START"
}
func isEndMarker(n, l string) bool {
	return n == l+"_END" || n == l+l+"_END"
}
func isSubMarker(n, l string) bool {
	return strings.HasPrefix(n, l) && (strings.HasSuffix(n, "_START") || strings.HasSuffix(n, "_END"))
}
