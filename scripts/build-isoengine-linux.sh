#!/usr/bin/env bash
# Build the native Linux isometric-engine binary (cmd/isoengine) — same
# toolchain/runtime requirements as build-linux.sh (cmd/isoengine pulls in
# the same go-gl/glfw + goki/vulkan + oto/FluidSynth cgo dependencies via
# render/vulkan, window, and audio), just a different cmd/ package. See
# that script's own header for the exact dev-package list and runtime
# notes (libvulkan.so.1, the soundfont search, etc.) — not repeated here.
#
# Naming matches build-linux.sh's <os>_<program> scheme: linux_isoengine,
# alongside linux_engine.
set -euo pipefail

cd "$(dirname "$0")/.."

OUT=${1:-bin/linux_isoengine}
mkdir -p "$(dirname "$OUT")"

CGO_ENABLED=1 go build -trimpath -o "$OUT" ./cmd/isoengine

echo "built $OUT"
file "$OUT" 2>/dev/null || true
