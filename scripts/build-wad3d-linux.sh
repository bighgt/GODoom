#!/usr/bin/env bash
# Build the real-3D WAD converter as bin/linux_wad3d.
#
# Same toolchain as scripts/build-linux.sh (cgo: it shares the window
# package's file picker, which on Linux shells out to zenity/kdialog).
# Run it with a WAD path, drag a WAD onto it, or run it bare for a picker.
set -euo pipefail

cd "$(dirname "$0")/.."

OUT=${1:-bin/linux_wad3d}
mkdir -p "$(dirname "$OUT")"

CGO_ENABLED=1 go build -trimpath -o "$OUT" ./cmd/wad3d

echo "built $OUT"
file "$OUT" 2>/dev/null || true
