#version 450

// Hardware renderer — textured 3D model fragment stage. The same shading
// as voxel.frag (sector/distance fade, ambient floor, hemisphere ambient,
// the dynamic point lights) applied to the part's skin texel instead of a
// per-voxel colour; transparent skin texels are cut out (alpha test), the
// way sprites are. Full-bright frames pass the texel straight through.

layout(location = 0) in vec2 vUV;
layout(location = 1) in vec3 vNormal;
layout(location = 2) in vec3 vWorldPos;
layout(location = 3) in float vDepth;

layout(location = 0) out vec4 outColor;

// Push range [112,128), shared with the vertex stage (which also owns
// [0,112)). x = sector light 0..1, y = fullbright flag, z = the vertex
// stage's frame-blend amount (unused here), w = additive alpha: > 0 when
// drawn by the additive pipeline (ZDoom's RenderStyle "Add" — the colour
// is summed onto the scene, so black is see-through), 0 for a solid part.
layout(push_constant) uniform PC {
    layout(offset = 112) vec4 shade;
} pc;

// Set 0: the part's skin (the world pipeline's per-texture set; binding 1,
// the brightmap slot, is unused here). Set 1: the world path's set, whose
// binding 2 is the World UBO.
layout(set = 0, binding = 0) uniform sampler2D uSkin;

const int MAX_LIGHTS = 64;

layout(set = 1, binding = 2) uniform World {
    vec4 cam;
    vec4 screen;
    vec4 light;    // lightScale, extraLight, scaleRef, ambientLight
    vec4 nLights;
    vec4 fog;      // unused here, but must be declared so the arrays below
    vec4 amb;      // land at the same std140 offset world.frag reads them at
    vec4 lightPos[MAX_LIGHTS];
    vec4 lightColor[MAX_LIGHTS];
} W;

// Hemisphere ambient — same model as world.frag, so a model in shadow
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
// sectorAmbientFrac(). Without this a model monster in a dark sector
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

// glass turns col into a translucent-glass fragment at base opacity a
// (ModelInstance.Alpha, the powerup spheres — pushed as -shade.w): a
// Fresnel rim (more opaque and brighter toward the silhouette, the middle
// see-through) and a tight glossy highlight — keep == voxel.frag's glass().
// Drawn two-sided, so the rim uses |N.V| and a ball's far wall reads as
// glass too, not as an opaque rim.
vec4 glass(vec3 col, float a) {
    vec3 N = normalize(vNormal);
    vec3 V = normalize(CAM_POS - vWorldPos);
    float ndv = dot(N, V);
    if (ndv < 0.0) {
        N = -N;
        ndv = -ndv;
    }
    float rim = pow(1.0 - clamp(ndv, 0.0, 1.0), 2.5);
    vec3 L = normalize(vec3(0.35, 0.25, 1.0));
    float spec = pow(max(dot(reflect(-V, N), L), 0.0), 48.0);
    vec3 c = col * (1.0 + 0.8 * rim) + vec3(spec * 1.6);
    float alpha = clamp(a + (1.0 - a) * rim * 0.85 + spec, 0.0, 1.0);
    return vec4(c, alpha);
}

void main() {
    vec4 texel = texture(uSkin, vUV);
    float add = pc.shade.w;
    // Cut-out skin texels (flames, gore, grilles); an additive part only
    // drops the fully transparent ones and weights the rest by alpha.
    if (texel.a < (add > 0.0 ? 0.01 : 0.5)) discard;
    vec3 vColor = texel.rgb;
    vec3 col;
    if (pc.shade.y > 0.5) {
        col = vColor * 1.35; // emissive — pushed >1 for bloom
    } else {
        float base = max(spriteFade(), W.light.w * sectorAmbientFrac());
        vec3 lit = vec3(base) + dynamicDiffuse(); // HDR — see world.frag
        col = vColor * lit + vColor * hemiAmbient() * (1.0 - clamp(base, 0.0, 1.0));
    }
    if (add > 0.0) {
        outColor = vec4(col * (add * texel.a), 0.0); // alpha isn't written (see the pipeline)
        return;
    }
    if (add < 0.0) {
        outColor = glass(col, -add * texel.a);
        return;
    }
    outColor = vec4(col, 1.0);
}
