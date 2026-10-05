package wad

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
)

// TPF3D marks a WAD that cmd/wad3d recompiled into TwoPointFive's "real
// 3D" form: its maps carry auto-generated geometry (a SLOPES lump of ramps
// and 3D floors) and its hi-res wall/flat textures travel inside the WAD
// itself (the TPFHIRES lump — see TPFHiresLump) rather than being looked
// up on disk. The marker's own body is a small plain-text manifest, one
// "key value" pair per line, '#' starting a comment:
//
//	format   1
//	tool     wad3d 1
//	source   DOOM2.WAD 3f4bc4c1...   (file name, then its SHA-1)
//	iwad     3f4bc4c1... /path/to/DOOM2.WAD   (the base IWAD the output
//	         must be merged over: SHA-1, then the rest of the line is its
//	         path, spaces allowed — see LoadWithIWAD)
//	passes   stairs
//	textures hires 412
//	weapons  doom heretic 1234   (arsenals embedded, then asset count)
//	maps     MAP01 MAP02 ...
//
// Unknown keys are ignored, so a later tool version can add fields that an
// older engine simply skips; a Format newer than TPF3DFormat is still
// loaded but logged, since the engine may then be missing something the
// WAD relies on.
//
// A real-3D WAD also carries every arsenal's weapon art and sounds in a
// TPFWEAPN zip (TPFWeaponsLump): sprites/<LUMP>.png (final, palette-
// resolved RGBA — a Heretic sprite in a Doom WAD can't borrow the Doom
// palette), sprites/offsets.txt ("LUMP offsetX offsetY texelsPerUnit
// bottomPad" per line, in the PNG's own pixel space — assets.RGBA's
// fields), and sounds/<LUMP>.lmp (the raw DMX
// lump). So config's switchWeapons works, and shareware Doom has the
// Doom II / registered-only guns' art, with no other IWAD on disk.
//
// And every map object is drawn as a voxel model — never a sprite — from
// the voxel pack(s) baked into a TPFVOXEL zip (TPFVoxelLump): the pk3s
// stored whole, in priority order by name ("1-<pack>.pk3", "2-..."), each
// later one filling only sprites the earlier ones lack entirely.
const (
	TPF3DLump      = "TPF3D"
	TPFHiresLump   = "TPFHIRES"
	TPFWeaponsLump = "TPFWEAPN"
	TPFVoxelLump   = "TPFVOXEL"
	// TPFModelLump is an optional zip of a MODELDEF model pack — the
	// definitions plus only the models and skins they use, pack paths kept
	// (assets/models.Load reads it as it would the .pk3). A map object with
	// a model there is drawn as it, outranking its voxel.
	TPFModelLump = "TPFMODEL"
	TPF3DFormat  = 1
)

// ConvertTool is what a player is told to run on a WAD the engine won't
// play (see CheckPlayable).
const ConvertTool = "the wad3d converter (bin/linux_wad3d or bin/windows_wad3d.exe)"

// ErrNotReal3D is CheckPlayable's error for a WAD that isn't a real-3D WAD.
var ErrNotReal3D = errors.New("only real-3D WADs are playable")

// CheckPlayable is the engine's one rule for what it will play: a real-3D
// WAD (a TPF3D marker) that carries its baked voxel models (TPFVOXEL — map
// objects are voxel-only). Anything else — a vanilla IWAD or PWAD, or a WAD
// converted before voxels were baked in — is refused with a message saying
// to convert it. name labels the file in that message.
func CheckPlayable(w *WAD, name string) error {
	if _, ok := w.TPF3D(); !ok {
		return &NotPlayableError{Name: name}
	}
	if w.IndexOf(TPFVoxelLump) < 0 {
		return &NotPlayableError{Name: name, OldConversion: true}
	}
	return nil
}

// NotPlayableError is CheckPlayable's refusal. Error() is the full
// explanation (console, logs); Short() is a one-line form for the
// launcher's status bar. errors.Is(err, ErrNotReal3D) matches it.
type NotPlayableError struct {
	Name          string
	OldConversion bool // a 3D WAD from a wad3d that didn't bake voxels yet
}

func (e *NotPlayableError) Error() string {
	if e.OldConversion {
		return fmt.Sprintf("%v: %s was converted by an older wad3d without voxel models — convert the original WAD again with %s", ErrNotReal3D, e.Name, ConvertTool)
	}
	return fmt.Sprintf("%v: %s is a standard WAD — convert it into a 3D WAD with %s, then play the converted file", ErrNotReal3D, e.Name, ConvertTool)
}

// Short is the status-bar form of the refusal.
func (e *NotPlayableError) Short() string {
	if e.OldConversion {
		return e.Name + ": old 3D WAD (no voxels). Convert the original again with wad3d."
	}
	return e.Name + ": not a 3D WAD. Convert it with the wad3d converter first."
}

func (e *NotPlayableError) Unwrap() error { return ErrNotReal3D }

// PeekTPF3D reports whether the WAD file at path carries a TPF3D marker,
// reading only its header and lump directory — cheap enough for the
// launcher to filter a folder of multi-hundred-MB WADs.
func PeekTPF3D(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	var h [headerSize]byte
	if _, err := io.ReadFull(f, h[:]); err != nil {
		return false, err
	}
	n := int64(int32(binary.LittleEndian.Uint32(h[4:8])))
	off := int64(int32(binary.LittleEndian.Uint32(h[8:12])))
	if n < 0 || n > 1<<20 || off < 0 {
		return false, fmt.Errorf("wad: %s: bad header", path)
	}
	dir := make([]byte, n*dirEntrySize)
	if _, err := f.ReadAt(dir, off); err != nil {
		return false, err
	}
	for i := int64(0); i < n; i++ {
		if cleanName(dir[i*dirEntrySize+8:i*dirEntrySize+16]) == TPF3DLump {
			return true, nil
		}
	}
	return false, nil
}

// TPF3DInfo is a parsed TPF3D manifest.
type TPF3DInfo struct {
	Format     int
	Tool       string
	Source     string // file name of the WAD the tool read
	SourceSHA1 string
	// IWADPath / IWADSHA1 are the base IWAD the output was built against —
	// the source itself for an IWAD source, its merged IWAD for a PWAD.
	// LoadWithIWAD merges exactly this IWAD under the output, so music,
	// monsters and every other lump the WAD doesn't carry come from the
	// right game.
	IWADPath     string
	IWADSHA1     string
	Passes       []string
	Textures     string // which texture set was embedded ("hires"), "" = none
	TextureCount int
	Voxels       []string // voxel packs baked into TPFVOXEL, in priority order
	Models       []string // 3D model packs baked into TPFMODEL
	Weapons      []string // arsenals embedded ("doom", "heretic")
	WeaponCount  int      // sprites + sounds in the TPFWEAPN container
	Maps         []string
}

// EncodeTPF3D renders info as a TPF3D lump body.
func EncodeTPF3D(info TPF3DInfo) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "# TwoPointFive real-3D WAD (cmd/wad3d)\n")
	fmt.Fprintf(&b, "format %d\n", info.Format)
	if info.Tool != "" {
		fmt.Fprintf(&b, "tool %s\n", info.Tool)
	}
	if info.Source != "" {
		fmt.Fprintf(&b, "source %s %s\n", strings.ReplaceAll(info.Source, " ", "_"), info.SourceSHA1)
	}
	if info.IWADPath != "" {
		fmt.Fprintf(&b, "iwad %s %s\n", info.IWADSHA1, info.IWADPath)
	}
	fmt.Fprintf(&b, "passes %s\n", strings.Join(info.Passes, " "))
	if info.Textures != "" {
		fmt.Fprintf(&b, "textures %s %d\n", info.Textures, info.TextureCount)
	}
	if len(info.Weapons) > 0 {
		fmt.Fprintf(&b, "weapons %s %d\n", strings.Join(info.Weapons, " "), info.WeaponCount)
	}
	if len(info.Voxels) > 0 {
		fmt.Fprintf(&b, "voxels %s\n", strings.Join(info.Voxels, " "))
	}
	if len(info.Models) > 0 {
		fmt.Fprintf(&b, "models %s\n", strings.Join(info.Models, " "))
	}
	fmt.Fprintf(&b, "maps %s\n", strings.Join(info.Maps, " "))
	return b.Bytes()
}

// ParseTPF3D reads a TPF3D lump body. It fails only when the mandatory
// format line is missing or malformed; anything else unrecognised is
// skipped.
func ParseTPF3D(b []byte) (TPF3DInfo, error) {
	var info TPF3DInfo
	haveFormat := false
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		line := sc.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		args := f[1:]
		switch strings.ToLower(f[0]) {
		case "format":
			if len(args) < 1 {
				return info, fmt.Errorf("wad: TPF3D: format line has no value")
			}
			n, err := strconv.Atoi(args[0])
			if err != nil || n < 1 {
				return info, fmt.Errorf("wad: TPF3D: bad format %q", args[0])
			}
			info.Format, haveFormat = n, true
		case "tool":
			info.Tool = strings.Join(args, " ")
		case "source":
			if len(args) > 0 {
				info.Source = args[0]
			}
			if len(args) > 1 {
				info.SourceSHA1 = args[1]
			}
		case "iwad":
			// SHA-1, then the whole rest of the line is the path.
			rest := strings.TrimSpace(line[strings.Index(line, f[0])+len(f[0]):])
			sha, path, _ := strings.Cut(rest, " ")
			info.IWADSHA1, info.IWADPath = sha, strings.TrimSpace(path)
		case "passes":
			info.Passes = append([]string(nil), args...)
		case "textures":
			if len(args) > 0 {
				info.Textures = args[0]
			}
			if len(args) > 1 {
				info.TextureCount, _ = strconv.Atoi(args[1])
			}
		case "weapons":
			for _, a := range args {
				if n, err := strconv.Atoi(a); err == nil {
					info.WeaponCount = n
				} else {
					info.Weapons = append(info.Weapons, a)
				}
			}
		case "voxels":
			info.Voxels = append([]string(nil), args...)
		case "models":
			info.Models = append([]string(nil), args...)
		case "maps":
			info.Maps = append([]string(nil), args...)
		}
	}
	if !haveFormat {
		return info, fmt.Errorf("wad: TPF3D: no format line")
	}
	return info, nil
}

// TPF3D reports whether w carries a TPF3D marker and returns its parsed
// manifest. A malformed marker is logged and treated as absent.
func (w *WAD) TPF3D() (TPF3DInfo, bool) {
	raw, ok := w.Find(TPF3DLump)
	if !ok {
		return TPF3DInfo{}, false
	}
	info, err := ParseTPF3D(raw)
	if err != nil {
		log.Printf("%v", err)
		return TPF3DInfo{}, false
	}
	if info.Format > TPF3DFormat {
		log.Printf("wad: TPF3D format %d is newer than this engine supports (%d) — loading anyway", info.Format, TPF3DFormat)
	}
	return info, true
}

// DescribeTPF3D is the one-line startup summary cmd/engine and
// cmd/isoengine log for a cmd/wad3d WAD, or "" for any other WAD.
func (w *WAD) DescribeTPF3D() string {
	info, ok := w.TPF3D()
	if !ok {
		return ""
	}
	desc := fmt.Sprintf("real-3D WAD (TPF3D format %d, %s", info.Format, info.Tool)
	if info.Source != "" {
		desc += ", from " + info.Source
	}
	desc += fmt.Sprintf("): passes [%s], %d map(s)", strings.Join(info.Passes, " "), len(info.Maps))
	if info.Textures != "" {
		desc += fmt.Sprintf(", %d embedded %s texture(s)", info.TextureCount, info.Textures)
	}
	if len(info.Weapons) > 0 {
		desc += fmt.Sprintf(", %s weapons (%d assets)", strings.Join(info.Weapons, "+"), info.WeaponCount)
	}
	if len(info.Voxels) > 0 {
		desc += ", voxels " + strings.Join(info.Voxels, "+")
	}
	if len(info.Models) > 0 {
		desc += ", 3D models " + strings.Join(info.Models, "+")
	}
	return desc
}

// ZipLump opens lump name (the last one, as Find) as a zip archive — the
// TPFHIRES / TPFWEAPN containers. ok is false when the lump is absent; a
// lump that isn't a readable zip is logged and reported absent too.
func (w *WAD) ZipLump(name string) (*zip.Reader, bool) {
	raw, ok := w.Find(name)
	if !ok {
		return nil, false
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		log.Printf("wad: %s lump: %v", name, err)
		return nil, false
	}
	return zr, true
}
