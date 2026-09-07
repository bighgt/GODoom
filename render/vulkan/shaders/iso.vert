#version 450

// Isometric viewer — vertex stage. Consumes the exact same packed triangle
// list render/worldgeo builds for the FPS hardware renderer (see
// worldgeo.VertexAttributes / Pack) — this pipeline only differs in the
// projection: worldgeo.IsoViewProj (a true rotation + orthographic
// projection) instead of ViewProj's perspective-with-pitch-shear.

layout(location = 0) in vec3 inPos;    // world, map units, Z up
layout(location = 1) in vec2 inUV;     // texture tiles (REPEAT sampler)
layout(location = 2) in float inLight; // sector light 0..1 (fake contrast folded in for walls)
layout(location = 3) in uint inKind;   // 0 wall, 1 masked, 2 flat, 3 sky, 4 sprite, 5 sprite-full
layout(location = 4) in vec3 inNormal; // unit world-space surface normal

layout(push_constant) uniform PC {
    mat4 viewProj;
    vec4 sun; // xyz = normalized sun direction (world space), w = sun strength
    vec4 amb; // x = ambient floor (0..1), yzw unused
} pc;

layout(location = 0) out vec2 vUV;
layout(location = 1) out float vLight;
layout(location = 2) out flat uint vKind;
layout(location = 3) out vec3 vNormal;

void main() {
    gl_Position = pc.viewProj * vec4(inPos, 1.0);
    vUV = inUV;
    vLight = inLight;
    vKind = inKind;
    vNormal = inNormal;
}
