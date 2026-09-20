#version 450

// Hardware renderer — water's own fragment shader (see
// worldgeo.Geometry.WaterDraws and recordWorldCommandBuffer's rpWater step).
// world.vert is reused unmodified (it's kind-agnostic); this file duplicates
// the pieces of world.frag/worldblit.frag it needs rather than sharing code
// across files — this codebase has no GLSL #include, and every other
// fragment shader here already duplicates the same way.
//
// uPreWaterColor/uPreWaterDepth are a snapshot of the scene taken right
// before this pass, with water itself excluded (see the barrier/copy
// sequence in world.go). On an ordinary map nothing real is captured under
// water — water WAS the floor, now excluded from that capture — so
// uPreWaterDepth reads at/near the far-plane clear there, and this shader
// falls through to exactly the fake fresnel/absorb shading world.frag's own
// KIND_LIQUID_WATER branch uses. Only where real geometry was captured (a
// room-over-room extra-floor's platform beneath a transparent water
// surface) does it blend in true refraction (sampling the capture at a
// wave-distorted screen position) and true Beer-Lambert murk (from the real
// depth difference) instead — see hasFloor below.

layout(location = 0) in vec2 vUV;
layout(location = 1) in float vLight;
layout(location = 2) in flat uint vKind;
layout(location = 3) in float vDepth; // this water fragment's own camera-forward distance
layout(location = 4) in vec3 vWorldPos;
layout(location = 5) in vec3 vNormal;
layout(location = 6) in float vAlpha;

layout(location = 0) out vec4 outColor;

layout(set = 0, binding = 0) uniform sampler2D uTex;
layout(set = 0, binding = 1) uniform sampler2D uBright;

const int MAX_LIGHTS = 64; // keep == vulkan.worldMaxLights and render.MaxLights
const int SHADOW_MAX = 24; // keep == vulkan.shadowCasterMax and engine.hwGroundShadowCap

layout(set = 1, binding = 2) uniform World {
    vec4 cam;
    vec4 screen;
    vec4 light;
    vec4 nLights;
    vec4 fog;
    vec4 amb;
    vec4 spotPosInvR2;
    vec4 spotDirCosOuter;
    vec4 spotColor;
    vec4 spotCosInner;
    vec4 lightPos[MAX_LIGHTS];
    vec4 lightColor[MAX_LIGHTS];
    vec4 shadowCaster[SHADOW_MAX];
} W;

#define CAM_POS vec3(W.cam.w, W.nLights.z, W.nLights.w)

// dynamicDiffuse's wrap-lambert check compares against this — never true
// here (every vertex WaterDraws submits is KindLiquidWater), kept only so
// the duplicated function reads identically to world.frag's own copy.
const uint KIND_SPRITE = 4u;

// The pre-water capture (see this file's header comment) — a dedicated set,
// not folded into set 0/1, since it's specific to this one pipeline.
layout(set = 2, binding = 0) uniform sampler2D uPreWaterColor;
layout(set = 2, binding = 1) uniform sampler2D uPreWaterDepth;

// ---- duplicated from world.frag (that file is the authoritative copy) ----

float fade(bool isFlat) {
    float ll = clamp(vLight * 255.0 * W.light.x, 0.0, 255.0);
    float lnum = clamp(floor(ll / 16.0) + W.light.y, 0.0, 15.0);
    float startmap = (15.0 - lnum) * 4.0;
    float k = clamp((W.light.z / max(vDepth, 1.0)) * 0.5, 0.0, 23.5);
    float row = clamp(startmap - k, 0.0, 31.0);
    float f = (32.0 - row) / 32.0;
    float bands = W.spotCosInner.y;
    if (bands > 0.5) {
        f = floor(f * bands + 0.5) / bands;
    }
    return f;
}

vec3 skyColorAt(vec2 fc) {
    float w = W.screen.x, h = W.screen.y, focal = W.cam.z;
    float pitchShear = W.screen.w;
    float colAngle = atan((fc.x - w * 0.5) / focal);
    float yaw = atan(W.cam.x, W.cam.y);
    ivec2 sz = textureSize(uTex, 0);
    float u = 0.5 + (colAngle - yaw) * W.screen.z / (2.0 * 3.14159265) / float(sz.x);
    float skyScale = float(sz.y) / 128.0;
    float horizonY = h * 0.5 + pitchShear;
    float row = skyScale * (100.0 + (fc.y - horizonY) * (200.0 / h));
    float v = row / float(sz.y);
    return texture(uTex, vec2(u, v)).rgb;
}

const float FOG_BASE       = -48.0;
const float FOG_FALLOFF    = 220.0;
const float FOG_HEIGHT_AMT = 0.7;

vec3 applyFog(vec3 c) {
    float dens = W.fog.w;
    if (dens <= 0.0) return c;
    float distFog = 1.0 - exp(-dens * max(vDepth, 0.0));
    float midZ = 0.5 * (CAM_POS.z + vWorldPos.z);
    float above = max(midZ - (CAM_POS.z + FOG_BASE), 0.0) / FOG_FALLOFF;
    float heightMul = mix(1.0, exp(-above), FOG_HEIGHT_AMT);
    float f = clamp(distFog * heightMul, 0.0, 1.0);
    return mix(c, W.fog.rgb, f);
}

// waterWave — identical constants to world.frag/worldblit.frag/
// raster.waterWave so this surface's own ripple matches the reflection
// ripple and the SSR march's ripple.
vec3 waterWave(vec2 wp, float t) {
    vec2 p = wp * 0.030;
    float a = sin(p.x * 1.00 + p.y * 0.60 + t * 1.6);
    float b = sin(p.x * -0.70 + p.y * 1.30 + t * 2.1);
    float c = sin(p.x * 1.70 + p.y * -0.40 + t * 1.1);
    vec2 q = wp * 0.008;
    float e = sin(q.x * 1.00 + q.y * 0.70 + t * 0.5);
    float hgt = (a + 0.6 * b + 0.4 * c) / 2.0 + 0.35 * e;
    vec2 grad = vec2(a - c + 0.4 * e, b - 0.5 * c + 0.3 * e);
    float crest = clamp(hgt * 0.4 + 0.5, 0.0, 1.0);
    return vec3(grad, crest);
}

const float DYN_GAIN       = 2.2;
const float SPEC_POWER     = 40.0;
const float SPEC_STRENGTH  = 0.6;

vec3 dynamicDiffuse(vec3 N, float specMask) {
    float nl = length(N);
    if (nl < 1e-3) return vec3(0.0);
    N /= nl;
    vec3 V = normalize(CAM_POS - vWorldPos);

    vec3 sum = vec3(0.0);
    int n = min(int(W.nLights.x), MAX_LIGHTS);
    for (int i = 0; i < n; ++i) {
        vec3 d = W.lightPos[i].xyz - vWorldPos;
        float d2 = dot(d, d);
        float atten = 1.0 - d2 * W.lightPos[i].w;
        if (atten <= 0.0) continue;
        atten *= atten;
        vec3 Ld = d * inversesqrt(max(d2, 1e-8));
        float ndlRaw = dot(N, Ld);
        float ndotl = max(ndlRaw, 0.0);
        float lambert = (vKind == KIND_SPRITE) ? (0.5 + 0.5 * ndlRaw) : ndotl;
        float spec = 0.0;
        if (ndotl > 0.0 && specMask > 0.0) {
            float nh = max(dot(N, normalize(Ld + V)), 0.0);
            spec = pow(nh, SPEC_POWER) * SPEC_STRENGTH * specMask;
        }
        sum += W.lightColor[i].rgb * (atten * (0.25 + 0.75 * lambert + spec));
    }

    if (W.spotColor.a > 0.0) {
        vec3 d = W.spotPosInvR2.xyz - vWorldPos;
        float d2 = dot(d, d);
        float atten = 1.0 - d2 * W.spotPosInvR2.w;
        if (atten > 0.0) {
            atten *= atten;
            vec3 Ld = d * inversesqrt(max(d2, 1e-8));
            float ndlRaw = dot(N, Ld);
            float ndotl = max(ndlRaw, 0.0);
            float lambert = (vKind == KIND_SPRITE) ? (0.5 + 0.5 * ndlRaw) : ndotl;
            float cosAngle = dot(W.spotDirCosOuter.xyz, normalize(vWorldPos - W.spotPosInvR2.xyz));
            float cone = smoothstep(W.spotDirCosOuter.w, W.spotCosInner.x, cosAngle);
            if (cone > 0.0) {
                float spec = 0.0;
                if (ndotl > 0.0 && specMask > 0.0) {
                    float nh = max(dot(N, normalize(Ld + V)), 0.0);
                    spec = pow(nh, SPEC_POWER) * SPEC_STRENGTH * specMask;
                }
                sum += W.spotColor.rgb * (atten * cone * (0.25 + 0.75 * lambert + spec));
            }
        }
    }

    vec3 g = sum * DYN_GAIN;
    float m = max(g.x, max(g.y, g.z));
    return g / (1.0 + 0.30 * max(m - 0.9, 0.0));
}

// ---- duplicated from worldblit.frag (that file is the authoritative copy) ----

// NEAR_P/FAR_P mirror worldgeo.NearPlane/FarPlane — not available through
// the World UBO (only worldblit.frag's push constants carry them today), so
// hardcoded here the same way that file already hardcodes its own copies
// via push constants rather than a shared source of truth.
const float NEAR_P = 1.0;
const float FAR_P  = 65536.0;

float linDepth(float ndcZ) {
    return NEAR_P * FAR_P / (FAR_P - (FAR_P - NEAR_P) * ndcZ);
}

// ---- new: real depth/refraction where captured, the fake cue otherwise ----

void main() {
    float t = W.nLights.y;
    vec3 wv = waterWave(vWorldPos.xy, t);

    float h = W.screen.y;
    float horizonY = h * 0.5 + W.screen.w;
    float graz = clamp(1.0 - (gl_FragCoord.y - horizonY) / max(h - horizonY, 1.0), 0.0, 1.0);

    // hasFloor: was anything real captured under this exact pixel? A
    // vanilla map's water sector has nothing beneath it (water WAS the
    // floor), so uPreWaterDepth there just holds the capture pass's own
    // far-plane clear — mirrors worldblit.frag's own void/far-plane
    // sentinel (its waterSSR march's "ez0 < 50000.0" check). The ezBg >
    // vDepth guard additionally rejects a captured depth nearer than this
    // water surface itself, which correct depth-test ordering shouldn't
    // produce, but costs nothing to guard against.
    float ezBg = linDepth(texelFetch(uPreWaterDepth, ivec2(gl_FragCoord.xy), 0).r);
    bool hasFloor = ezBg < 50000.0 && ezBg > vDepth;
    float trueDepth = hasFloor ? (ezBg - vDepth) : 0.0;

    // Depth cue: real (from the true captured depth) where there's a real
    // floor beneath, else the same fake view-angle/distance cue world.frag's
    // own KIND_LIQUID_WATER branch uses — this is what keeps a vanilla map's
    // water looking exactly as it did before this shader existed.
    float distFrac = clamp(vDepth / 620.0, 0.0, 1.0);
    float depth = hasFloor ? clamp(trueDepth / 260.0, 0.0, 1.0) : (0.14 + 0.55 * graz + 0.34 * distFrac);

    // Same pixel-space wobble scale skyColorAt's own reflection sample uses
    // below, reused here for the refraction sample so both distortions read
    // as the same surface rippling.
    vec2 pixOff = wv.xy * (3.0 + 6.0 * graz);

    vec3 bodyTex;
    if (hasFloor) {
        // Refraction: sample the real capture at a wave-distorted screen
        // position — but reject the offset if it crosses a depth
        // discontinuity (a nearby wall/silhouette edge), the same guard
        // worldblit.frag's own SSR march uses, so refraction never smears a
        // background wall into the water near its edge.
        vec2 uvOff = clamp(gl_FragCoord.xy + pixOff, vec2(0.0), W.screen.xy - vec2(1.0));
        float ezOff = linDepth(texelFetch(uPreWaterDepth, ivec2(uvOff), 0).r);
        vec2 samplePx = (abs(ezOff - ezBg) > max(ezBg * 0.05, 8.0)) ? gl_FragCoord.xy : uvOff;
        bodyTex = texture(uPreWaterColor, samplePx / W.screen.xy).rgb;
    } else {
        // The FWATER texel is "the bottom seen THROUGH the water" (the
        // fake, no-real-floor case) — sampled with the same wave-slope
        // refraction world.frag's own copy of this shading uses.
        bodyTex = texture(uTex, vUV + wv.xy * (0.010 + 0.02 * depth)).rgb;
    }
    vec3 body = bodyTex * fade(true);

    float caustic = 0.86 + 0.5 * (wv.z - 0.5);
    vec3 absorb = vec3(exp(-2.3 * depth), exp(-1.15 * depth), exp(-0.5 * depth));
    body = body * absorb * caustic + vec3(0.02, 0.06, 0.09) * depth;

    // Reflection: same rippled-sky-ish hint and fresnel curve as world.frag's
    // own copy — see that file's doc comment on why this samples uTex
    // (there's no separate sky binding available from a per-texture draw).
    float fres = 0.03 + 0.5 * pow(graz, 4.0);
    vec3 refl = skyColorAt(gl_FragCoord.xy + pixOff);
    refl = mix(refl, vec3(dot(refl, vec3(0.30, 0.59, 0.11))), 0.12);

    vec3 outc = mix(body, refl, fres);
    outc += dynamicDiffuse(vec3(0.0, 0.0, 1.0), 0.0) * (0.08 + 0.4 * pow(wv.z, 4.0) * (0.3 + graz));

    vec3 Rd = normalize(reflect(normalize(vWorldPos - CAM_POS), vec3(0.0, 0.0, 1.0))
                        + vec3(wv.xy * 0.16, 0.0));
    vec3 lref = vec3(0.0);
    int nlr = min(int(W.nLights.x), MAX_LIGHTS);
    for (int i = 0; i < nlr; ++i) {
        vec3 toL = W.lightPos[i].xyz - vWorldPos;
        float dl2 = dot(toL, toL);
        float al = max(dot(Rd, toL * inversesqrt(max(dl2, 1e-6))), 0.0);
        lref += W.lightColor[i].rgb * pow(al, 48.0) / (1.0 + dl2 * 0.0006);
    }
    outc += lref * (0.4 + 0.5 * graz);

    float yaw = atan(W.cam.x, W.cam.y);
    float sunX = W.screen.x * 0.5 + sin(t * 0.06 - yaw * 1.5) * W.screen.x * 0.32;
    vec2 dd = vec2((gl_FragCoord.x - sunX) / (W.screen.x * 0.05),
                   (gl_FragCoord.y - horizonY - 6.0) / 7.0);
    float sun = exp(-dot(dd, dd)) * pow(wv.z, 5.0) * (0.25 + 0.75 * graz);
    outc += vec3(1.0, 0.97, 0.9) * sun * 0.9;

    // Alpha 0 tags this pixel as water for the present pass, exactly like
    // world.frag's own copy — worldblit.frag's SSR still runs on top of
    // this shader's output unchanged, same layering as before this shader
    // existed.
    outColor = vec4(applyFog(outc), 0.0);
}
