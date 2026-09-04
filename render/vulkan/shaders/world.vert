#version 450

// Hardware (GPU-geometry) renderer — vertex stage. Consumes the packed
// triangle list render/worldgeo builds from the BSP walk (see
// worldgeo.VertexAttributes / Pack) and the column-major view-projection
// matrix worldgeo.ViewProj produces (byte-matched to raster's projection,
// so the geometry lands exactly where the software rasterizer would).
//
// The texture is bound per draw (the CPU groups the list by texture into
// worldgeo.Draws), so there's no per-vertex texture index. clip.w carries
// camera-forward distance in map units — the fragment stage indexes the
// sector/distance fade LUT with it, like raster's shadeMul / distIdx.

layout(location = 0) in vec3 inPos;    // world, map units, Z up
layout(location = 1) in vec2 inUV;     // texture tiles (REPEAT sampler)
layout(location = 2) in float inLight; // sector light 0..1 (fake contrast folded in for walls)
layout(location = 3) in uint inKind;   // 0 wall, 1 masked, 2 flat, 3 sky, 4 sprite, 5 sprite-full
layout(location = 4) in vec3 inNormal; // unit world-space surface normal (dynamic-light Lambert)

layout(push_constant) uniform PC {
    mat4 viewProj;
} pc;

layout(location = 0) out vec2 vUV;
layout(location = 1) out float vLight;
layout(location = 2) out flat uint vKind;
layout(location = 3) out float vDepth;
layout(location = 4) out vec3 vWorldPos; // for per-fragment dynamic point lights
layout(location = 5) out vec3 vNormal;

void main() {
    vec4 clip = pc.viewProj * vec4(inPos, 1.0);
    gl_Position = clip;
    vUV = inUV;
    vLight = inLight;
    vKind = inKind;
    vDepth = clip.w;
    vWorldPos = inPos;
    vNormal = inNormal;
}
