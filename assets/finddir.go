package assets

// FindDir resolves an optional asset directory (e.g. "assets/mods") the
// same way the skybox / hi-res / voxel / HD-weapon loaders resolve theirs:
// relative to the working directory first, then beside the running
// executable and up to four of its parents (so bin/engine finds the repo's
// assets/ folder however it was launched). ok is false when it doesn't
// exist — the caller then just proceeds without that optional data.
//
// This is the exported form of findAssetDir, for sibling packages
// (assets/vfs consumers) that need the same resolution.
func FindDir(rel string) (dir string, ok bool) { return findAssetDir(rel) }
