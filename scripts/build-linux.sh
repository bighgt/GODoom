#!/usr/bin/env bash
# Build the native Linux engine binary.
#
# Needs a C toolchain plus the X11 / ALSA development headers that
# go-gl/glfw and the oto audio backend compile against:
#   sudo apt-get install build-essential libx11-dev libxrandr-dev \
#       libxcursor-dev libxi-dev libxinerama-dev libgl1-mesa-dev \
#       libasound2-dev
#
# cgo is required (GLFW + oto are cgo); the goki/vulkan and libfluidsynth
# bindings dlopen their libraries at runtime, so nothing Vulkan- or
# FluidSynth-related is linked at build time. The build tags do the
# platform swap on their own — no GOOS override here.
#
# At run time the binary needs libvulkan.so.1 (ships with every GPU
# driver / Mesa) for the hardware renderer. Music uses the built-in synth
# path unless libfluidsynth.so.3 and a GM .sf2 are installed (see
# audio/midi_linux.go for the paths probed) or $TPF_SOUNDFONT is set.
set -euo pipefail

cd "$(dirname "$0")/.."

OUT=${1:-bin/linux_engine}
mkdir -p "$(dirname "$OUT")"

CGO_ENABLED=1 go build -trimpath -o "$OUT" ./cmd/engine

echo "built $OUT"
file "$OUT" 2>/dev/null || true
