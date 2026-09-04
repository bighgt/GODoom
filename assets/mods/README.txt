assets/mods/ — drop-in mod folder
=================================

Everything here is mounted at startup into one virtual filesystem
(assets/vfs) and layered over the loaded WAD, newest last. Put in:

  *.pk3 / *.zip / *.pk7      GZDoom-style archives (mounted whole)
  loose files / subfolders  e.g. a plain GLDEFS text file, or
                            textures/FOO.png, brightmaps/BAR.png, ...

What is read from here today
---------------------------
  GLDEFS   data-driven dynamic lights — see GLDEFS.example in this folder.
           Rename it to "GLDEFS" (no extension) to try it, and run with
           config.json  lightingMode: "enhanced".

Coming: brightmaps/, textures/ overrides consume the same mount stack.

This folder is gitignored (mods are third-party content). GLDEFS.example
and this README are the only tracked files.
