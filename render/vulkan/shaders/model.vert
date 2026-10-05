#version 450

// Hardware renderer — textured 3D model vertex stage (MD2/MD3/DMD parts
// from a MODELDEF pack; see assets/models and worldgeo.ModelInstance). Each
// part's frame is one static unindexed vertex buffer in model units; the
// per-instance transform rides in the push constant: mvp = viewProj * model,
// and the model matrix's top three rows, so the fragment stage gets the
// world position and normal for the dynamic lights (128-byte budget).

layout(location = 0) in vec3 inPos;    // model units
layout(location = 1) in vec3 inNormal; // model space, unit
layout(location = 2) in vec2 inUV;     // skin coordinates, 0..1, v down
// The frame being blended toward (binding 1 — the same buffer as binding 0
// when the part isn't blending; shade.z is then 0). GZDoom's model frame
// interpolation: position and normal lerp by shade.z.
layout(location = 3) in vec3 inPos2;
layout(location = 4) in vec3 inNormal2;

// Push ranges: [0,112) vertex-only; [112,128) shared with the fragment
// stage (shade: x light, y fullbright, z frame-blend amount).
layout(push_constant) uniform PC {
    mat4 mvp;
    vec4 row0; // model -> world, rows 0..2 (translation in .w)
    vec4 row1;
    vec4 row2;
    vec4 shade;
} pc;

layout(location = 0) out vec2 vUV;
layout(location = 1) out vec3 vNormal;
layout(location = 2) out vec3 vWorldPos;
layout(location = 3) out float vDepth;

void main() {
    vec4 p = vec4(mix(inPos, inPos2, pc.shade.z), 1.0);
    vec3 n = mix(inNormal, inNormal2, pc.shade.z);
    vec4 clip = pc.mvp * p;
    gl_Position = clip;
    vDepth = clip.w;
    vUV = inUV;
    vWorldPos = vec3(dot(pc.row0, p), dot(pc.row1, p), dot(pc.row2, p));
    // Non-uniform scale bends normals slightly; renormalising is plenty for
    // the Lambert term the lights use.
    vNormal = normalize(vec3(dot(pc.row0.xyz, n), dot(pc.row1.xyz, n), dot(pc.row2.xyz, n)));
}
