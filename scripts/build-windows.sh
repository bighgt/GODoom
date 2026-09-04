#!/usr/bin/env bash
# Cross-compile the Windows engine.exe from Linux.
#
# Needs the MinGW-w64 cross toolchain:
#   sudo apt-get install gcc-mingw-w64-x86-64 g++-mingw-w64-x86-64
#
# cgo for GOOS=windows is pulled in by go-gl/glfw, goki/vulkan, and
# audio/fluidsynth_windows.go (which LoadLibrary's libfluidsynth at
# runtime — nothing is linked at build time); everything else is pure Go
# there. The build tags do the platform swap on their own.
#
# The resulting .exe needs only vulkan-1.dll on the target machine (it
# ships with every GPU driver): -extldflags -static folds libgcc and
# libwinpthread into the binary so there is no MinGW-runtime-DLL dependency.
# Music: the .exe uses the built-in GS Wavetable synth unless the FluidSynth
# release DLLs (libfluidsynth-3.dll + deps) and a GM .sf2 are placed next to
# it (or in soundfonts/, or via $TPF_SOUNDFONT).
set -euo pipefail

cd "$(dirname "$0")/.."

CC=${CC:-x86_64-w64-mingw32-gcc}
CXX=${CXX:-x86_64-w64-mingw32-g++}

if ! command -v "$CC" >/dev/null 2>&1; then
	echo "error: $CC not found — install gcc-mingw-w64-x86-64" >&2
	exit 1
fi

OUT=${1:-bin/windows_engine.exe}
mkdir -p "$(dirname "$OUT")"

CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC="$CC" CXX="$CXX" \
	go build -trimpath \
	-ldflags '-linkmode external -extldflags -static' \
	-o "$OUT" ./cmd/engine

echo "built $OUT"
file "$OUT" 2>/dev/null || true
