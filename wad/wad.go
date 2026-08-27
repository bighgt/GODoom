// Package wad reads id Software's WAD ("Where's All the Data?") archive
// format — the single-file container Doom, Doom II, and their engine
// derivatives use to store every level, texture, sound, and palette. See
// game_design.txt for the full format reference this package implements.
package wad

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"regexp"
	"strings"
)

const (
	headerSize   = 12
	dirEntrySize = 16
)

// Header is the 12-byte block at the start of every WAD file.
type Header struct {
	// Magic is "IWAD" (a complete, standalone game) or "PWAD" (a patch that
	// overrides/extends lumps from an IWAD loaded alongside it).
	Magic    string
	NumLumps int32
	// DirOfs is the byte offset of the lump directory within the file.
	DirOfs int32
}

// DirEntry is one 16-byte record of the lump directory: where a lump's
// bytes live in the file, how big it is, and its (up to 8 character) name.
type DirEntry struct {
	FilePos int32
	Size    int32
	Name    string
}

// WAD is a parsed view over a loaded .wad file's directory. Lump bytes are
// not copied out at load time — Lump() slices directly into the backing
// buffer, so opening even a multi-hundred-megabyte IWAD is a single read.
type WAD struct {
	Header  Header
	Entries []DirEntry

	data  []byte
	index map[string][]int // upper-cased lump name -> directory indices, in file order
}

// Load reads and parses a WAD file from disk.
func Load(path string) (*WAD, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("wad: read %s: %w", path, err)
	}
	w, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("wad: %s: %w", path, err)
	}
	return w, nil
}

// Parse decodes an already-loaded WAD file's bytes.
func Parse(data []byte) (*WAD, error) {
	if len(data) < headerSize {
		return nil, fmt.Errorf("file too small to contain a WAD header (%d bytes)", len(data))
	}

	magic := string(data[0:4])
	if magic != "IWAD" && magic != "PWAD" {
		return nil, fmt.Errorf("bad magic %q (expected IWAD or PWAD)", magic)
	}
	numLumps := int32(binary.LittleEndian.Uint32(data[4:8]))
	dirOfs := int32(binary.LittleEndian.Uint32(data[8:12]))
	if numLumps < 0 || dirOfs < 0 {
		return nil, fmt.Errorf("corrupt header (lump count %d, directory offset %d)", numLumps, dirOfs)
	}

	need := int64(dirOfs) + int64(numLumps)*dirEntrySize
	if need > int64(len(data)) {
		return nil, fmt.Errorf("directory (needs %d bytes) runs past end of file (%d bytes)", need, len(data))
	}

	entries := make([]DirEntry, numLumps)
	index := make(map[string][]int, numLumps)
	for i := int32(0); i < numLumps; i++ {
		off := int(dirOfs) + int(i)*dirEntrySize
		rec := data[off : off+dirEntrySize]

		filePos := int32(binary.LittleEndian.Uint32(rec[0:4]))
		size := int32(binary.LittleEndian.Uint32(rec[4:8]))
		name := cleanName(rec[8:16])

		if filePos < 0 || size < 0 || int64(filePos)+int64(size) > int64(len(data)) {
			// A handful of official IWADs contain zero-size marker lumps
			// with a filePos of 0; only reject lumps that would genuinely
			// read out of bounds.
			if size != 0 {
				return nil, fmt.Errorf("lump %q (index %d) points outside the file", name, i)
			}
		}

		entries[i] = DirEntry{FilePos: filePos, Size: size, Name: name}
		index[name] = append(index[name], int(i))
	}

	return &WAD{
		Header:  Header{Magic: magic, NumLumps: numLumps, DirOfs: dirOfs},
		Entries: entries,
		data:    data,
		index:   index,
	}, nil
}

// cleanName upper-cases an 8-byte, zero-padded lump name field into a plain
// Go string with the padding stripped.
func cleanName(raw []byte) string {
	n := bytes.IndexByte(raw, 0)
	if n < 0 {
		n = len(raw)
	}
	return strings.ToUpper(string(raw[:n]))
}

// Lump returns the raw bytes of the lump at directory index i.
func (w *WAD) Lump(i int) []byte {
	e := w.Entries[i]
	return w.data[e.FilePos : e.FilePos+e.Size]
}

// IndexOf returns the directory index of the last lump named name
// (case-insensitive) — "last" because later entries in a WAD (or a PWAD
// loaded after an IWAD) are meant to override earlier ones with the same
// name, which is how Doom's patch/mod system works. Returns -1 if not found.
func (w *WAD) IndexOf(name string) int {
	idxs := w.index[strings.ToUpper(name)]
	if len(idxs) == 0 {
		return -1
	}
	return idxs[len(idxs)-1]
}

// Find returns the raw bytes of the last lump named name, and whether it exists.
func (w *WAD) Find(name string) ([]byte, bool) {
	i := w.IndexOf(name)
	if i < 0 {
		return nil, false
	}
	return w.Lump(i), true
}

var mapMarkerPattern = regexp.MustCompile(`^(E[1-9]M[1-9]|MAP[0-9]{2})$`)

// ListMaps returns every map marker lump name found in the WAD (e.g. "E1M1"
// for Doom/Ultimate Doom, "MAP01" for Doom II and most PWADs), in directory order.
func (w *WAD) ListMaps() []string {
	var maps []string
	for _, e := range w.Entries {
		if mapMarkerPattern.MatchString(e.Name) {
			maps = append(maps, e.Name)
		}
	}
	return maps
}
