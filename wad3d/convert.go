// Package wad3d recompiles a Doom WAD into TwoPointFive's "real 3D" form:
// geometry passes analyse each map and add true 3D features the vanilla
// format can't express (today: staircases become continuous ramps), the
// result is written as SLOPES data, the hi-res texture set the maps use is
// packed into the WAD itself (a TPFHIRES container — see assets/
// embedded.go), and a TPF3D manifest lump marks the output so the engine
// knows what it is loading. The output is a TwoPointFive-only PWAD; it is
// still merged with its base IWAD at load time for sprites, sounds and the
// palette.
//
// cmd/wad3d is the converter program; this package is the library
// so other tools (the editor) can run the same passes.
package wad3d

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"twopointfive/engine/slopes"
	"twopointfive/wad"
)

// ToolVersion is written into the TPF3D manifest's tool line.
const ToolVersion = "wad3d 1"

// Pass names accepted in Options.Passes.
const (
	PassStairs = "stairs"
)

// KnownPasses lists every pass, in the order Convert runs them.
var KnownPasses = []string{PassStairs}

// DefaultPasses is what a run without an explicit pass list uses.
var DefaultPasses = []string{PassStairs}

// Options controls a conversion.
type Options struct {
	Passes      []string // subset of KnownPasses; nil = DefaultPasses
	Maps        []string // maps to convert; nil = every map in the source file
	Textures    bool     // embed the hi-res texture set
	AllTextures bool     // embed every texture the WAD defines, not only what the maps use
	MaxTex      int      // downscale embedded PNGs larger than this (0 = keep native size)
	// NoVoxels skips baking voxel models (voxels.go). Only for tests and
	// tools that don't produce a playable WAD: the engine refuses a 3D WAD
	// without them (wad.CheckPlayable), since map objects are voxel-only.
	NoVoxels bool
	// NoModels skips baking a 3D model pack (models.go) even when one is on
	// hand. Models are optional either way — voxels are the baseline.
	NoModels bool
	// Weapons embeds every arsenal in Source.Weapons (weapons.go). The
	// command-line tool always sets it; a caller turning it off gets a WAD
	// whose switchWeapons falls back to the other IWAD on disk as before.
	Weapons bool
	Stairs  StairOptions
}

// Source is the WAD being converted.
type Source struct {
	Name   string   // file name recorded in the manifest
	SHA1   string   // hex SHA-1 of the source file, recorded in the manifest
	Picked *wad.WAD // the file itself
	Merged *wad.WAD // Picked merged over its IWAD (== Picked for an IWAD)
	// IWADPath / IWADSHA1 identify the base IWAD the output must be loaded
	// over (the source itself for an IWAD source), recorded in the TPF3D
	// manifest so the engine merges exactly that one — see
	// wad.TPF3DInfo.IWADPath.
	IWADPath, IWADSHA1 string
	// Weapons are the arsenals to embed when Options.Weapons is set: the
	// Doom II-family IWAD ("doom") and the Heretic IWAD ("heretic").
	Weapons []WeaponIWAD
}

// MapReport summarises what the passes did to one map.
type MapReport struct {
	Name        string
	StairRuns   int
	RampSectors int
	MaxSlope    float64 // steepest generated ramp (rise/run)
}

// Report summarises a whole conversion.
type Report struct {
	Maps            []MapReport
	TextureSet      string // "hires" / "heretic" / "" when not embedded
	Walls, Flats    int    // textures embedded
	MissingTextures int    // wanted names with no hi-res file
	TextureBytes    int

	VoxelPacks      []string // voxel packs baked in, priority order
	VoxelBytes      int
	VoxelSprites    int      // sprite frames with a model
	UnmodelledTypes []string // map-object types with no model (invisible in play)

	ModelPacks  []string // 3D model packs baked in
	ModelFrames int      // sprite frames with a 3D model
	ModelFiles  int
	ModelBytes  int
	ModelErrors []error // problems with single pack entries (dropped, the rest baked)

	WeaponGames    []string // arsenals embedded
	WeaponSprites  int
	WeaponSounds   int
	WeaponBytes    int
	MissingWeapons []string // sprite prefixes / sounds no weapon source had
}

// mapLumpNames are the lumps of a vanilla map block after its marker.
var mapLumpNames = map[string]bool{
	"THINGS": true, "LINEDEFS": true, "SIDEDEFS": true, "VERTEXES": true, "SEGS": true,
	"SSECTORS": true, "NODES": true, "SECTORS": true, "REJECT": true, "BLOCKMAP": true,
}

// Convert runs the passes over src and returns the output WAD's lumps, ready
// for wad.WritePWAD.
func Convert(src Source, opt Options) ([]wad.NamedLump, Report, error) {
	var rep Report
	if _, ok := src.Picked.TPF3D(); ok {
		return nil, rep, fmt.Errorf("wad3d: %s is already a TPF3D WAD — convert the original instead", src.Name)
	}
	passes := opt.Passes
	if passes == nil {
		passes = DefaultPasses
	}
	enabled := map[string]bool{}
	for _, p := range passes {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" {
			continue
		}
		if !contains(KnownPasses, p) {
			return nil, rep, fmt.Errorf("wad3d: unknown pass %q (known: %s)", p, strings.Join(KnownPasses, ", "))
		}
		enabled[p] = true
	}
	var passList []string
	for _, p := range KnownPasses {
		if enabled[p] {
			passList = append(passList, p)
		}
	}

	maps, err := selectMaps(src.Picked, opt.Maps)
	if err != nil {
		return nil, rep, err
	}

	// The source's own SLOPES (a PWAD may carry one): kept verbatim in the
	// output, and its sectors left alone by the passes.
	srcSlopes := sourceSlopes(src.Picked)
	srcPlanes, srcFloors, _ := slopes.Parse(srcSlopes)

	var slopesText bytes.Buffer
	slopesText.Write(srcSlopes)
	var mapLumps []wad.NamedLump
	var levels []*wad.Level
	for _, name := range maps {
		lvl, err := src.Merged.LoadLevel(name)
		if err != nil {
			return nil, rep, fmt.Errorf("wad3d: %s: %w", name, err)
		}
		levels = append(levels, lvl)
		mr := MapReport{Name: name}

		var defs []slopes.Def
		if enabled[PassStairs] {
			topo := buildTopology(lvl)
			exclude := slopedSectors(srcPlanes[name], srcFloors[name])
			runs := findStairRuns(topo, exclude, opt.Stairs)
			for _, r := range runs {
				mr.StairRuns++
				mr.RampSectors += len(r.steps)
				mr.MaxSlope = max(mr.MaxSlope, r.slope)
			}
			defs = append(defs, stairDefs(runs)...)
		}
		writeSlopesBlock(&slopesText, name, defs)

		// No pass changes 2D geometry yet, so the map block — including its
		// shipped nodes, REJECT and BLOCKMAP — is copied through verbatim.
		mapLumps = append(mapLumps, mapBlock(src.Picked, name)...)
		rep.Maps = append(rep.Maps, mr)
	}

	info := wad.TPF3DInfo{
		Format:     wad.TPF3DFormat,
		Tool:       ToolVersion,
		Source:     src.Name,
		SourceSHA1: src.SHA1,
		IWADPath:   src.IWADPath,
		IWADSHA1:   src.IWADSHA1,
		Passes:     passList,
		Maps:       maps,
	}

	var hires []byte
	if opt.Textures {
		walls, flats := usedNames(levels)
		if opt.AllTextures {
			for _, n := range src.Merged.WallTextureNames() {
				walls[n] = true
			}
			for _, n := range src.Merged.FlatNames() {
				flats[n] = true
			}
		}
		addFamilies(src.Merged, walls, flats)
		ts := pickTextureSource(src.Merged)
		z, err := buildTextureZip(src.Merged, ts, walls, flats, opt.MaxTex)
		if err != nil {
			return nil, rep, fmt.Errorf("wad3d: textures: %w", err)
		}
		if z.walls+z.flats > 0 {
			hires = z.data
			info.Textures, info.TextureCount = ts.label, z.walls+z.flats
			rep.TextureSet = ts.label
		}
		rep.Walls, rep.Flats, rep.MissingTextures, rep.TextureBytes = z.walls, z.flats, z.missing, len(z.data)
	}

	var weapons []byte
	if opt.Weapons {
		if len(src.Weapons) == 0 {
			return nil, rep, fmt.Errorf("wad3d: weapons requested but no Doom/Heretic IWAD supplied")
		}
		wz, err := buildWeaponZip(src.Weapons)
		if err != nil {
			return nil, rep, fmt.Errorf("wad3d: weapons: %w", err)
		}
		weapons = wz.data
		info.Weapons, info.WeaponCount = wz.games, wz.sprites+wz.sounds
		rep.WeaponGames, rep.WeaponSprites, rep.WeaponSounds = wz.games, wz.sprites, wz.sounds
		rep.WeaponBytes, rep.MissingWeapons = len(wz.data), wz.missing
	}

	var voxels []byte
	if !opt.NoVoxels {
		vz, err := buildVoxelZip(src.Merged, VoxelPacksFor(src.Merged, maps))
		if err != nil {
			return nil, rep, fmt.Errorf("wad3d: voxels: %w", err)
		}
		voxels = vz.data
		info.Voxels = vz.packs
		rep.VoxelPacks, rep.VoxelBytes, rep.VoxelSprites, rep.UnmodelledTypes = vz.packs, len(vz.data), vz.sprites, vz.unmodelled
	}

	var modelData []byte
	if !opt.NoModels {
		mz, err := buildModelZip(src.Merged, ModelPacks())
		if err != nil {
			return nil, rep, fmt.Errorf("wad3d: 3D models: %w", err)
		}
		modelData = mz.data
		info.Models = mz.packs
		rep.ModelPacks, rep.ModelFrames, rep.ModelFiles, rep.ModelBytes, rep.ModelErrors = mz.packs, mz.frames, mz.files, len(mz.data), mz.errs
	}

	out := []wad.NamedLump{{Name: wad.TPF3DLump, Data: wad.EncodeTPF3D(info)}}
	if src.Picked.Header.Magic == "PWAD" {
		out = append(out, nonMapLumps(src.Picked)...)
	}
	if slopesText.Len() > 0 {
		out = append(out, wad.NamedLump{Name: "SLOPES", Data: slopesText.Bytes()})
	}
	if hires != nil {
		out = append(out, wad.NamedLump{Name: wad.TPFHiresLump, Data: hires})
	}
	if weapons != nil {
		out = append(out, wad.NamedLump{Name: wad.TPFWeaponsLump, Data: weapons})
	}
	if voxels != nil {
		out = append(out, wad.NamedLump{Name: wad.TPFVoxelLump, Data: voxels})
	}
	if modelData != nil {
		out = append(out, wad.NamedLump{Name: wad.TPFModelLump, Data: modelData})
	}
	out = append(out, mapLumps...)
	return out, rep, nil
}

// selectMaps returns the maps to convert, in the source file's order.
func selectMaps(w *wad.WAD, want []string) ([]string, error) {
	all := w.ListMaps()
	if len(all) == 0 {
		return nil, fmt.Errorf("wad3d: no maps in the source WAD")
	}
	if len(want) == 0 {
		return all, nil
	}
	var out []string
	for _, m := range want {
		m = strings.ToUpper(strings.TrimSpace(m))
		if m == "" {
			continue
		}
		if !contains(all, m) {
			return nil, fmt.Errorf("wad3d: map %s is not in the source WAD (has %s)", m, strings.Join(all, " "))
		}
		out = append(out, m)
	}
	sort.SliceStable(out, func(i, j int) bool { return indexOf(all, out[i]) < indexOf(all, out[j]) })
	return out, nil
}

// sourceSlopes concatenates every SLOPES lump the source file carries.
func sourceSlopes(w *wad.WAD) []byte {
	var b []byte
	for i := range w.Entries {
		if w.Entries[i].Name == "SLOPES" {
			b = append(b, w.Lump(i)...)
			b = append(b, '\n')
		}
	}
	return b
}

// writeSlopesBlock appends one map's generated defs in the SLOPES grammar
// (engine/slopes). Nothing is written for a map with no defs.
func writeSlopesBlock(b *bytes.Buffer, mapName string, defs []slopes.Def) {
	if len(defs) == 0 {
		return
	}
	fmt.Fprintf(b, "// generated by %s\n%s {\n", ToolVersion, mapName)
	for _, d := range defs {
		kind := "floor"
		if d.Ceiling {
			kind = "ceiling"
		}
		fmt.Fprintf(b, "    sector %d %s plane %g %g %g  %g %g %g  %g %g %g\n", d.SectorIndex, kind,
			d.P1[0], d.P1[1], d.P1[2], d.P2[0], d.P2[1], d.P2[2], d.P3[0], d.P3[1], d.P3[2])
	}
	b.WriteString("}\n")
}

// mapBlock copies the marker and map lumps for name out of w verbatim.
func mapBlock(w *wad.WAD, name string) []wad.NamedLump {
	idx := w.IndexOf(name)
	out := []wad.NamedLump{{Name: name}}
	for i := idx + 1; i < len(w.Entries) && mapLumpNames[w.Entries[i].Name]; i++ {
		out = append(out, wad.NamedLump{Name: w.Entries[i].Name, Data: w.Lump(i)})
	}
	return out
}

// nonMapLumps copies every lump of a PWAD that isn't part of a map block,
// a SLOPES lump (folded into the generated one) or a previous tool run's
// marker/container — music, TEXTURE1, flats, sprites, DEHACKED, MAPINFO...
// so the output stands in for the source PWAD entirely.
func nonMapLumps(w *wad.WAD) []wad.NamedLump {
	var out []wad.NamedLump
	maps := map[string]bool{}
	for _, m := range w.ListMaps() {
		maps[m] = true
	}
	for i := 0; i < len(w.Entries); i++ {
		n := w.Entries[i].Name
		if maps[n] {
			for i+1 < len(w.Entries) && mapLumpNames[w.Entries[i+1].Name] {
				i++
			}
			continue
		}
		switch n {
		case "SLOPES", wad.TPF3DLump, wad.TPFHiresLump, wad.TPFWeaponsLump, wad.TPFVoxelLump, wad.TPFModelLump:
			continue
		}
		out = append(out, wad.NamedLump{Name: n, Data: w.Lump(i)})
	}
	return out
}

func contains(s []string, v string) bool { return indexOf(s, v) >= 0 }

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}
