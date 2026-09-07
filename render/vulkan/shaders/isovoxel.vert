#version 450

// Isometric viewer — voxel model vertex stage. Same mesh/push-constant
// shape as the FPS hardware path's voxel.vert (worldgeo.BuildVoxelMesh:
// exposed cube faces, model space, pivot at the origin; mvp = viewProj *
// model built CPU-side, trans/rot decomposed so the fragment stage can
// reconstruct the world normal) — reused verbatim so the Go side
// (worldgeo.VoxelModelMatrix/Mat4Mul, the voxelMesh cache/upload code) is
// shared between the two pipelines; only the shading in the fragment
// stage differs (no dynamic lights/distance fade here — see isovoxel.frag).

layout(location = 0) in vec3 inPos;    // model space, voxel units
layout(location = 1) in vec3 inNormal; // model space, unit
layout(location = 2) in vec4 inColor;  // R8G8B8A8_UNORM -> 0..1

layout(push_constant) uniform PC {
    mat4 mvp;   // viewProj * model
    vec4 trans; // xyz = world position (feet), w = scale (map units / voxel)
    vec4 rot;   // x = cos(yaw), y = sin(yaw)
} pc;

layout(location = 0) out vec3 vColor;
layout(location = 1) out vec3 vNormal;

void main() {
    gl_Position = pc.mvp * vec4(inPos, 1.0);
    vColor = inColor.rgb;

    float c = pc.rot.x, s = pc.rot.y;
    vNormal = normalize(vec3(inNormal.x * c - inNormal.y * s,
                             inNormal.x * s + inNormal.y * c,
                             inNormal.z));
}
