#version 450

// Hardware renderer — voxel model fragment stage. Shades a per-voxel colour
// like a sprite (raster spriteShade: the wall/sprite scalelight table
// indexed by camera-forward depth and the thing's sector light) and adds
// the same dynamic point lights world.frag does. Full-bright frames pass
// the colour straight through.

layout(location = 0) in vec3 vColor;
layout(location = 1) in vec3 vNormal;
layout(location = 2) in vec3 vWorldPos;
layout(location = 3) in float vDepth;
layout(location = 4) flat in vec3 vCenter; // world-space mesh centre (glass shading)
layout(location = 5) flat in uint vSector; // sector + 1 (0 = unknown) — see lightReaches

layout(location = 0) out vec4 outColor;

// Fragment-stage push range [96,112) only (the vertex stage owns [0,96)).
layout(push_constant) uniform PC {
    layout(offset = 96) vec4 shade; // x = sector light 0..1, y = fullbright flag, z = glass alpha (0 = solid)
} pc;

const int MAX_LIGHTS = 64;

// Shares the world path's set-1 layout (dsl1: World UBO), but the voxel
// pipeline layout has just that one set, so it's bound at set 0 here.
layout(set = 0, binding = 2) uniform World {
    vec4 cam;
    vec4 screen;
    vec4 light;    // lightScale, extraLight, scaleRef, ambientLight
    vec4 nLights;
    vec4 fog;      // unused here, but must be declared so the arrays below
    vec4 amb;      // land at the same std140 offset world.frag reads them at
    vec4 lightPos[MAX_LIGHTS];
    vec4 lightColor[MAX_LIGHTS];
} W;

// Per-light sector reach — keep == world.frag's lightReaches (the world
// set is bound at set 0 in the voxel pipeline's layout).
const uint REACH_STRIDE = 256u; // keep == vulkan.lightReachStride
layout(std430, set = 0, binding = 3) readonly buffer LightReach { uint words[]; } LR;
bool lightReaches(int i) {
    if (W.lightColor[i].w < 0.5 || vSector == 0u) return true;
    uint s = vSector - 1u;
    if (s >= REACH_STRIDE * 32u) return true;
    return (LR.words[uint(i) * REACH_STRIDE + (s >> 5)] & (1u << (s & 31u))) != 0u;
}

// Hemisphere ambient — same model as world.frag, so a voxel prop in shadow
// takes on the level's sky palette instead of going black.
vec3 hemiAmbient() {
    if (W.amb.w <= 0.0) return vec3(0.020, 0.028, 0.045);
    float up = clamp(vNormal.z * 0.5 + 0.5, 0.0, 1.0);
    return mix(W.amb.rgb * 0.30, W.amb.rgb, up) * W.amb.w;
}

// Continuous sector/distance fade — see world.frag fade().
float spriteFade() {
    float ll = clamp(pc.shade.x * 255.0 * W.light.x, 0.0, 255.0);
    float lnum = clamp(floor(ll / 16.0) + W.light.y, 0.0, 15.0);
    float startmap = (15.0 - lnum) * 4.0;
    float k = clamp((W.light.z / max(vDepth, 1.0)) * 0.5, 0.0, 23.5);
    float row = clamp(startmap - k, 0.0, 31.0);
    return (32.0 - row) / 32.0;
}

// Ambient floor (config ambientLight, W.light.w) scaled by this voxel's own
// sector brightness, undiminished by distance — see world.frag's
// sectorAmbientFrac(). Without this a voxel-model monster in a dark sector
// would sit noticeably darker than a sprite-drawn one standing right next
// to it, since sprites/walls already get this floor and voxels didn't.
const float AMBIENT_GAMMA = 0.6; // keep == world.frag
float sectorAmbientFrac() {
    float frac = clamp(pc.shade.x * W.light.x, 0.0, 1.0);
    return pow(frac, AMBIENT_GAMMA);
}

const float DYN_GAIN = 2.2; // keep == world.frag

vec3 dynamicDiffuse() {
    vec3 N = vNormal;
    float nl = length(N);
    if (nl < 1e-3) return vec3(0.0);
    N /= nl;

    vec3 sum = vec3(0.0);
    int n = min(int(W.nLights.x), MAX_LIGHTS);
    for (int i = 0; i < n; ++i) {
        if (!lightReaches(i)) continue;
        vec3 d = W.lightPos[i].xyz - vWorldPos;
        float d2 = dot(d, d);
        float atten = 1.0 - d2 * W.lightPos[i].w;
        if (atten <= 0.0) continue;
        atten *= atten;
        float ndotl = max(dot(N, d * inversesqrt(max(d2, 1e-8))), 0.0);
        sum += W.lightColor[i].rgb * (atten * (0.25 + 0.75 * ndotl)); // see world.frag
    }
    vec3 g = sum * DYN_GAIN;
    float m = max(g.x, max(g.y, g.z));
    return g / (1.0 + 0.30 * max(m - 0.9, 0.0));
}

#define CAM_POS vec3(W.cam.w, W.nLights.z, W.nLights.w) // keep == world.frag

// glass turns col into a translucent-glass fragment at base opacity a (the
// powerup spheres — VoxelInstance.Alpha). The normal is the direction from
// the mesh centre, not the voxel face's own axis-aligned one, so a blocky
// voxel sphere still shades as a smooth round ball: a Fresnel rim (more
// opaque and brighter toward the silhouette, the middle see-through) and a
// tight glossy highlight from a fixed key direction.
vec4 glass(vec3 col, float a) {
    vec3 N = normalize(vWorldPos - vCenter);
    vec3 V = normalize(CAM_POS - vWorldPos);
    float rim = pow(1.0 - clamp(dot(N, V), 0.0, 1.0), 2.5);
    vec3 L = normalize(vec3(0.35, 0.25, 1.0));
    float spec = pow(max(dot(reflect(-V, N), L), 0.0), 48.0);
    vec3 c = col * (1.0 + 0.8 * rim) + vec3(spec * 1.6);
    float alpha = clamp(a + (1.0 - a) * rim * 0.85 + spec, 0.0, 1.0);
    return vec4(c, alpha);
}

void main() {
    vec3 col;
    if (pc.shade.y > 0.5) {
        col = vColor * 1.35; // emissive — pushed >1 for bloom
    } else {
        float base = max(spriteFade(), W.light.w * sectorAmbientFrac());
        vec3 lit = vec3(base) + dynamicDiffuse(); // HDR — see world.frag
        col = vColor * lit + vColor * hemiAmbient() * (1.0 - clamp(base, 0.0, 1.0));
    }
    // Alpha only matters to the glass pipeline (which leaves scene alpha
    // unwritten); the solid pipeline writes the usual 1.
    outColor = pc.shade.z > 0.0 ? glass(col, pc.shade.z) : vec4(col, 1.0);
}
