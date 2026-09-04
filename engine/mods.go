package engine

import (
	"log"
	"os"
	"path/filepath"
	"strings"

	"twopointfive/assets"
	"twopointfive/assets/vfs"
)

// Mod loading. Anything the player drops in assets/mods/ — a GZDoom-style
// .pk3/.zip, or a loose folder / file — is mounted into one virtual
// filesystem (assets/vfs). Right now only GLDEFS is read from it
// (gldefs_load.go); brightmap and texture overrides will consume the same
// mount stack. The directory is optional and gitignored (see .gitignore) —
// a build with no assets/mods/ just mounts nothing.
const modsDir = "assets/mods"

// modArchiveExts are the container files mounted as archives; every other
// loose file is picked up by the directory walk.
var modArchiveExts = map[string]bool{
	".pk3": true, ".zip": true, ".pk7": true, ".ipk3": true, ".pkz": true,
}

// loadMods locates assets/mods/ and mounts it: the directory tree itself
// (loose files) plus every archive inside it, in filename order so a later
// pack shadows an earlier one. Always returns a usable (possibly empty)
// *vfs.FS.
func loadMods() *vfs.FS {
	fs := vfs.New()
	dir, ok := assets.FindDir(modsDir)
	if !ok {
		return fs
	}
	if err := fs.MountDir(dir); err != nil {
		log.Printf("mods: mount %s: %v", dir, err)
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		log.Printf("mods: read %s: %v", dir, err)
		return fs
	}
	archives := 0
	for _, e := range ents {
		if e.IsDir() || !modArchiveExts[strings.ToLower(filepath.Ext(e.Name()))] {
			continue
		}
		if err := fs.MountZip(filepath.Join(dir, e.Name())); err != nil {
			log.Printf("mods: %s: %v", e.Name(), err)
			continue
		}
		archives++
	}
	if !fs.Empty() {
		log.Printf("mods: mounted %s (%d archive(s) + loose files)", dir, archives)
	}
	return fs
}
