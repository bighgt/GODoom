#version 450

// Isometric viewer — voxel model fragment stage. Shades a per-voxel colour
// the same way iso.frag shades textured geometry (sector-light ambient
// floor + a fixed directional "sun" for shape) rather than the FPS
// hardware path's dynamic-light/distance-fade model (voxel.frag) — no
// per-frame World UBO here, matching the rest of this pipeline's
// deliberately simpler shading.

layout(location = 0) in vec3 vColor;
layout(location = 1) in vec3 vNormal;

layout(location = 0) out vec4 outColor;

// Fragment-stage push range [96,112) (the vertex stage owns [0,96) — split
// the same way voxel.frag's PC is, so the two stages' blocks can't be
// pruned to incompatible layouts).
layout(push_constant) uniform PC {
    layout(offset = 96) vec4 shade; // x = sector light 0..1, y = fullbright flag
} pc;

// Keep == isoSunDir/isoSunStrength/isoAmbientFloor in isoworld.go — there's
// no push-constant room left (96 vertex + 16 fragment already fills the
// 112-byte budget voxel.vert's layout reserves) to pass these per-frame
// instead, and they change rarely enough that a shader-recompile on tuning
// them is an acceptable trade.
const vec3 SUN_DIR = vec3(0.35, -0.55, 0.75);
const float SUN_STRENGTH = 0.5;
const float AMBIENT_FLOOR = 0.35;

void main() {
    if (pc.shade.y > 0.5) {
        outColor = vec4(vColor * 1.35, 1.0); // emissive, matches iso.frag's sprite-full handling
        return;
    }
    float base = mix(AMBIENT_FLOOR, 1.0, clamp(pc.shade.x, 0.0, 1.0));
    float lambert = max(dot(normalize(vNormal), normalize(SUN_DIR)), 0.0);
    float lit = clamp(base + lambert * SUN_STRENGTH, 0.0, 1.3);
    outColor = vec4(vColor * lit, 1.0);
}
