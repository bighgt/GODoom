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

layout(location = 0) out vec4 outColor;

// Fragment-stage push range [96,112) only (the vertex stage owns [0,96)).
layout(push_constant) uniform PC {
    layout(offset = 96) vec4 shade; // x = sector light 0..1, y = fullbright flag
} pc;

const int MAX_LIGHTS = 64;

// Shares the world path's set-1 layout (dslT1: LUTs + World UBO), but the
// voxel pipeline layout has just that one set, so it's bound at set 0 here.
layout(set = 0, binding = 0) uniform sampler2D uScaleLUT; // walls/sprites, 48 cols, 16 rows
layout(set = 0, binding = 1) uniform sampler2D uZLUT;     // (unused here, part of the shared set)
layout(set = 0, binding = 2) uniform World {
    vec4 cam;
    vec4 screen;
    vec4 light;    // lightScale, extraLight, scaleRef, lutLevels
    vec4 nLights;
    vec4 fog;      // unused here, but must be declared so the arrays below
    vec4 amb;      // land at the same std140 offset world.frag reads them at
    vec4 lightPos[MAX_LIGHTS];
    vec4 lightColor[MAX_LIGHTS];
} W;

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

const float DYN_GAIN = 2.2; // keep == world.frag

vec3 dynamicDiffuse() {
    vec3 N = vNormal;
    float nl = length(N);
    if (nl < 1e-3) return vec3(0.0);
    N /= nl;

    vec3 sum = vec3(0.0);
    int n = min(int(W.nLights.x), MAX_LIGHTS);
    for (int i = 0; i < n; ++i) {
        vec3 d = W.lightPos[i].xyz - vWorldPos;
        float d2 = dot(d, d);
        float atten = 1.0 - d2 * W.lightPos[i].w;
        if (atten <= 0.0) continue;
        atten *= atten;
        float ndotl = max(dot(N, d * inversesqrt(max(d2, 1e-8))), 0.0);
        sum += W.lightColor[i].rgb * (atten * (0.35 + 0.65 * ndotl)); // see world.frag
    }
    vec3 g = sum * DYN_GAIN;
    float m = max(g.x, max(g.y, g.z));
    return g / (1.0 + 0.30 * max(m - 0.9, 0.0));
}

void main() {
    if (pc.shade.y > 0.5) {
        outColor = vec4(vColor * 1.35, 1.0); // emissive — pushed >1 for bloom
        return;
    }
    float base = spriteFade();
    vec3 lit = vec3(base) + dynamicDiffuse(); // HDR — see world.frag
    outColor = vec4(vColor * lit + vColor * hemiAmbient() * (1.0 - clamp(base, 0.0, 1.0)), 1.0);
}
