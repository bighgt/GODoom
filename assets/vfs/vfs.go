// Package vfs is a small virtual filesystem for mod archives: a stack of
// mounted sources (ZIP/PK3 files or plain directories) presented as one
// flat, case-insensitive lump namespace, the way ZDoom-family ports treat
// a loaded PK3. Later mounts shadow earlier ones.
//
// Pure Go — archive/zip + os only. It replaces the ad-hoc per-feature zip
// readers (HD weapons, voxel packs) with one layer and is the loading path
// for GLDEFS / brightmaps / textures supplied by a mod.
//
// Only the lookups the current features need are implemented (Lump, Lumps,
// InNamespace, List); the namespace tagging is done up front so brightmap
// and texture support can build on it without re-walking archives.
package vfs

import (
	"archive/zip"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Namespace is the top-level directory a file sits under inside an archive,
// mapped to the role ZDoom assigns it. A file at the archive root is
// NSGlobal (GLDEFS, TEXTURES, MAPINFO, ...).
type Namespace int

const (
	NSGlobal Namespace = iota
	NSTextures
	NSFlats
	NSSprites
	NSPatches
	NSGraphics
	NSHires
	NSBrightmaps
	NSSounds
	NSMusic
	NSColormaps
	NSVoxels
	NSModels
)

func (n Namespace) String() string {
	switch n {
	case NSGlobal:
		return "global"
	case NSTextures:
		return "textures"
	case NSFlats:
		return "flats"
	case NSSprites:
		return "sprites"
	case NSPatches:
		return "patches"
	case NSGraphics:
		return "graphics"
	case NSHires:
		return "hires"
	case NSBrightmaps:
		return "brightmaps"
	case NSSounds:
		return "sounds"
	case NSMusic:
		return "music"
	case NSColormaps:
		return "colormaps"
	case NSVoxels:
		return "voxels"
	case NSModels:
		return "models"
	}
	return "other"
}

func nsFromDir(seg string) Namespace {
	switch seg {
	case "textures":
		return NSTextures
	case "flats":
		return NSFlats
	case "sprites":
		return NSSprites
	case "patches":
		return NSPatches
	case "graphics":
		return NSGraphics
	case "hires":
		return NSHires
	case "brightmaps":
		return NSBrightmaps
	case "sounds":
		return NSSounds
	case "music":
		return NSMusic
	case "colormaps":
		return NSColormaps
	case "voxels":
		return NSVoxels
	case "models":
		return NSModels
	}
	return NSGlobal
}

// strippable extensions: removed from a file's stem to form its lump key,
// so `graphics/TITLEPIC.png` is found as lump "TITLEPIC" and a root
// `GLDEFS.txt` as "GLDEFS". Non-graphics/-audio extensions are kept.
var strippable = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".tga": true, ".pcx": true,
	".bmp": true, ".gif": true, ".lmp": true, ".dds": true,
	".wav": true, ".ogg": true, ".flac": true, ".mp3": true, ".mid": true,
	".mus": true, ".imf": true, ".txt": true,
	".kvx": true, ".md3": true, ".md2": true, ".obj": true, ".iqm": true,
}

// archiveExts are not indexed as lumps when a directory mount walks over
// them (a mod folder that also contains loose .pk3s).
var archiveExts = map[string]bool{
	".pk3": true, ".zip": true, ".pk7": true, ".ipk3": true, ".pkz": true, ".7z": true,
}

type entry struct {
	ns       Namespace
	fullPath string // archive-relative, slash-separated
	read     func() ([]byte, error)
}

type mount struct {
	name  string
	byKey map[string][]entry // upper-case lump key -> entries in archive order
}

// FS is a mount stack. The zero value is not usable — call New.
type FS struct {
	mounts  []mount
	closers []io.Closer
}

// New returns an empty mount stack.
func New() *FS { return &FS{} }

// Empty reports whether nothing has been mounted.
func (f *FS) Empty() bool { return len(f.mounts) == 0 }

// Close releases any open archive readers. A process that mounts for its
// whole lifetime need not call it; tests should.
func (f *FS) Close() error {
	var first error
	for _, c := range f.closers {
		if err := c.Close(); err != nil && first == nil {
			first = err
		}
	}
	f.closers = nil
	return first
}

// Mount adds a source: a directory, or a .pk3/.zip/.pk7/.ipk3 archive
// (dispatched on the path). A path that is neither is mounted as a single
// loose file at the root namespace.
func (f *FS) Mount(p string) error {
	st, err := os.Stat(p)
	if err != nil {
		return err
	}
	if st.IsDir() {
		return f.MountDir(p)
	}
	if archiveExts[strings.ToLower(filepath.Ext(p))] {
		return f.MountZip(p)
	}
	return f.mountLooseFile(p)
}

// MountZip adds one ZIP/PK3 archive. The reader stays open (files are read
// lazily) until Close.
func (f *FS) MountZip(p string) error {
	zr, err := zip.OpenReader(p)
	if err != nil {
		return err
	}
	m := mount{name: filepath.Base(p), byKey: map[string][]entry{}}
	for _, zf := range zr.File {
		if zf.FileInfo().IsDir() {
			continue
		}
		zf := zf
		name := path.Clean(strings.ReplaceAll(zf.Name, `\`, "/"))
		key, ns := keyAndNS(name)
		if key == "" {
			continue
		}
		m.byKey[key] = append(m.byKey[key], entry{
			ns: ns, fullPath: name,
			read: func() ([]byte, error) {
				rc, err := zf.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(rc)
			},
		})
	}
	f.closers = append(f.closers, zr)
	f.mounts = append(f.mounts, m)
	return nil
}

// MountDir adds a directory tree, walked recursively. Nested archive files
// are skipped (not indexed as lumps).
func (f *FS) MountDir(dir string) error {
	m := mount{name: filepath.Base(dir), byKey: map[string][]entry{}}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if archiveExts[strings.ToLower(filepath.Ext(p))] {
			return nil
		}
		rel, relErr := filepath.Rel(dir, p)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		key, ns := keyAndNS(rel)
		if key == "" {
			return nil
		}
		abs := p
		m.byKey[key] = append(m.byKey[key], entry{
			ns: ns, fullPath: rel,
			read: func() ([]byte, error) { return os.ReadFile(abs) },
		})
		return nil
	})
	if err != nil {
		return err
	}
	f.mounts = append(f.mounts, m)
	return nil
}

func (f *FS) mountLooseFile(p string) error {
	key, ns := keyAndNS(filepath.Base(p))
	if key == "" {
		return nil
	}
	p = filepath.Clean(p)
	f.mounts = append(f.mounts, mount{
		name: filepath.Base(p),
		byKey: map[string][]entry{key: {{
			ns: ns, fullPath: filepath.Base(p),
			read: func() ([]byte, error) { return os.ReadFile(p) },
		}}},
	})
	return nil
}

func keyAndNS(slashPath string) (key string, ns Namespace) {
	slashPath = strings.TrimPrefix(slashPath, "./")
	slashPath = strings.TrimLeft(slashPath, "/")
	if slashPath == "" {
		return "", NSGlobal
	}
	segs := strings.Split(slashPath, "/")
	base := segs[len(segs)-1]
	ns = NSGlobal
	if len(segs) > 1 {
		ns = nsFromDir(strings.ToLower(segs[0]))
	}
	stem := base
	if ext := strings.ToLower(path.Ext(base)); strippable[ext] {
		stem = base[:len(base)-len(ext)]
	}
	if stem == "" {
		return "", ns
	}
	return strings.ToUpper(stem), ns
}

// Lump returns the newest-mounted copy of name (searched across every
// namespace), and whether it was found. name is matched case-insensitively
// against lump keys (basename minus a graphics/audio extension).
func (f *FS) Lump(name string) ([]byte, bool) {
	key := strings.ToUpper(name)
	for i := len(f.mounts) - 1; i >= 0; i-- {
		es := f.mounts[i].byKey[key]
		for j := len(es) - 1; j >= 0; j-- {
			if b, err := es[j].read(); err == nil {
				return b, true
			}
		}
	}
	return nil, false
}

// InNamespace is Lump restricted to one namespace.
func (f *FS) InNamespace(ns Namespace, name string) ([]byte, bool) {
	key := strings.ToUpper(name)
	for i := len(f.mounts) - 1; i >= 0; i-- {
		es := f.mounts[i].byKey[key]
		for j := len(es) - 1; j >= 0; j-- {
			if es[j].ns != ns {
				continue
			}
			if b, err := es[j].read(); err == nil {
				return b, true
			}
		}
	}
	return nil, false
}

// Lumps returns every mounted copy of name, oldest mount first and in
// archive order within a mount — for lumps whose contents concatenate
// (GLDEFS, ANIMDEFS, SNDINFO, ...).
func (f *FS) Lumps(name string) [][]byte {
	key := strings.ToUpper(name)
	var out [][]byte
	for i := range f.mounts {
		for _, e := range f.mounts[i].byKey[key] {
			if b, err := e.read(); err == nil {
				out = append(out, b)
			}
		}
	}
	return out
}

// List returns the archive-relative paths of every file in a namespace,
// deduplicated and sorted — for callers that need to enumerate (e.g. every
// override texture a pack ships).
func (f *FS) List(ns Namespace) []string {
	seen := map[string]bool{}
	for i := range f.mounts {
		for _, es := range f.mounts[i].byKey {
			for _, e := range es {
				if e.ns == ns {
					seen[e.fullPath] = true
				}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
