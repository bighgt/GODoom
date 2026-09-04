#!/usr/bin/env bash
# Compiles .vert/.frag/.comp in this directory to <name>.<stage>.spv.
# The Go build does NOT run this — it go:embed's the committed .spv — so run
# it by hand after editing a shader, then commit the regenerated .spv.
#
#   render/vulkan/shaders/compile.sh [name ...]
#
# With no args, compiles every shader; with args, only those base names
# (e.g. `compile.sh world.frag light.frag`).
#
# Compiler search order:
#   1. glslc on PATH            (shaderc / Vulkan SDK — matches compile.ps1)
#   2. glslangValidator on PATH
#   3. <repo>/.tooling/bin/glslangValidator   (auto-installed, gitignored)
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo="$(cd "$here/../../.." && pwd)"

compile_glslc() { glslc -O --target-env=vulkan1.0 "$1" -o "$2"; }
compile_glslang() { "$GLSLANG" -V --target-env vulkan1.0 "$1" -o "$2"; }

if command -v glslc >/dev/null 2>&1; then
    CC=compile_glslc; echo "compiler: $(command -v glslc)"
elif command -v glslangValidator >/dev/null 2>&1; then
    GLSLANG="$(command -v glslangValidator)"; CC=compile_glslang
    echo "compiler: $GLSLANG (unoptimized SPIR-V)"
elif [[ -x "$repo/.tooling/bin/glslangValidator" ]]; then
    GLSLANG="$repo/.tooling/bin/glslangValidator"; CC=compile_glslang
    echo "compiler: $GLSLANG (unoptimized SPIR-V)"
else
    echo "no shader compiler found — install glslc (shaderc / Vulkan SDK)" >&2
    exit 1
fi

shopt -s nullglob
if [[ $# -gt 0 ]]; then
    targets=("$@")
else
    targets=()
    for f in "$here"/*.vert "$here"/*.frag "$here"/*.comp; do
        targets+=("$(basename "$f")")
    done
fi

for name in "${targets[@]}"; do
    src="$here/$name"
    [[ -f "$src" ]] || { echo "skip (no such file): $name" >&2; continue; }
    stage="${name##*.}"
    out="$here/${name%.*}.$stage.spv"
    echo "  $name -> $(basename "$out")"
    "$CC" "$src" "$out"
done
echo "done."
