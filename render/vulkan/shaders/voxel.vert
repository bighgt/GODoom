#version 450

// Hardware renderer — voxel model vertex stage. The mesh is one static
// buffer per KVX model (worldgeo.BuildVoxelMesh: exposed cube faces, model
// space, 1 unit = 1 voxel, pivot at the origin). The per-instance transform
// rides in the push constant: mvp is viewProj * model (built CPU-side), and
// trans / rot / scale are the same model decomposed, so the fragment stage
// can reconstruct the world position and normal for the dynamic lights
// without a second matrix (128-byte push-constant budget).

layout(location = 0) in vec3 inPos;    // model space, voxel units
layout(location = 1) in vec3 inNormal; // model space, unit
layout(location = 2) in vec4 inColor;  // R8G8B8A8_UNORM -> 0..1

// Vertex-stage push range [0,96); the fragment stage owns [96,112) (shade).
// Kept as separate non-overlapping ranges so glslc's -O can't prune the two
// stages' blocks to incompatible layouts.
layout(push_constant) uniform PC {
    mat4 mvp;   // viewProj * model
    vec4 trans; // xyz = world position (feet), w = scale (map units / voxel)
    vec4 rot;   // x = cos(yaw), y = sin(yaw)
} pc;

layout(location = 0) out vec3 vColor;
layout(location = 1) out vec3 vNormal;
layout(location = 2) out vec3 vWorldPos;
layout(location = 3) out float vDepth;

void main() {
    vec4 clip = pc.mvp * vec4(inPos, 1.0);
    gl_Position = clip;
    vDepth = clip.w;
    vColor = inColor.rgb;

    float c = pc.rot.x, s = pc.rot.y, k = pc.trans.w;
    // model = translate(trans.xyz) * rotZ(yaw) * scale(k)
    vec3 rp = vec3(inPos.x * c - inPos.y * s, inPos.x * s + inPos.y * c, inPos.z) * k;
    vWorldPos = pc.trans.xyz + rp;
    vNormal = normalize(vec3(inNormal.x * c - inNormal.y * s,
                             inNormal.x * s + inNormal.y * c,
                             inNormal.z));
}
