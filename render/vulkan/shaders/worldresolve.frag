#version 450

// Hardware renderer — tonemap-resolve pass. Runs once per frame at full
// render resolution, between the bloom pass and the present blit
// (recordWorldCommandBuffer's step (4.5) / worldresolve.go's recordResolve),
// and does the SAME water-SSR + ambient-combine + ACES-tonemap work
// worldblit.frag used to do itself — but exactly once per pixel, writing
// the LDR result to wr.resolve.img. worldblit.frag then reads that
// resolved image directly for FXAA/CAS's neighbour taps instead of
// re-deriving the full HDR->LDR chain (2 texture fetches + a 3-channel
// ACES curve) 8-10 times per pixel — see worldblit.frag's own header for
// exactly what moved here and what stayed.

layout(location = 0) in vec2 fragUV;
layout(location = 0) out vec4 outColor;

layout(set = 0, binding = 0) uniform sampler2D uScene;       // HDR, render res
layout(set = 0, binding = 1) uniform sampler2D uBloom;       // HDR, half res (linear upsample)
layout(set = 0, binding = 2) uniform sampler2D uDepth;       // world pass D32 depth, render res (nearest)
layout(set = 0, binding = 3) uniform sampler2D uAmbientTerm; // quarter-res shadow/AO/god-ray (worldambient.frag)

layout(push_constant) uniform PC {
    vec4 p0; // exposure, bloomStrength, waterClock, nearPlane
    vec4 p1; // sinA, cosA, focal(px), farPlane
    vec4 p2; // camX, camY, camZ, horizonY(px, pitch-sheared)
    vec4 p3; // width, height, ssrStrength, godrayStrength
} pc;

// ---- helpers — duplicated from worldblit.frag rather than shared: this
// codebase has no GLSL #include (see world.frag's own doc comment on the
// same choice), and these moved here wholesale when the pass split.

vec3 aces(vec3 x) {
    const float a = 2.51, b = 0.03, c = 2.43, d = 0.59, e = 0.14;
    return clamp((x * (a * x + b)) / (x * (c * x + d) + e), 0.0, 1.0);
}

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

float linDepth(float ndcZ) {
    float n = pc.p0.w, f = pc.p1.w;
    return n * f / (f - (f - n) * ndcZ);
}

float depthAtUV(vec2 uv) {
    vec2 res = pc.p3.xy;
    ivec2 t = ivec2(clamp(uv, vec2(0.0), vec2(0.9999)) * res);
    return linDepth(texelFetch(uDepth, t, 0).r);
}

vec3 worldFromPixel(vec2 fpix, float ez) {
    float w = pc.p3.x, focal = pc.p1.z;
    float sinA = pc.p1.x, cosA = pc.p1.y;
    float colF = (fpix.x - w * 0.5) / focal;
    float kx = cosA + colF * sinA;
    float ky = sinA - colF * cosA;
    float wz = pc.p2.z + ez * (pc.p2.w - fpix.y) / focal;
    return vec3(pc.p2.x + ez * kx, pc.p2.y + ez * ky, wz);
}

vec3 worldFromUV(vec2 uv, float ez) { return worldFromPixel(uv * pc.p3.xy, ez); }

// ---- water SSR (identical math to worldblit.frag's former copy) --------

vec3 waterSSR(vec2 uv, vec3 baseHDR, float ez0) {
    float w = pc.p3.x, h = pc.p3.y, focal = pc.p1.z;
    float sinA = pc.p1.x, cosA = pc.p1.y, nearP = pc.p0.w;
    float camX = pc.p2.x, camY = pc.p2.y, camZ = pc.p2.z, horizonY = pc.p2.w;

    vec3 P = worldFromUV(uv, ez0);
    vec3 V = normalize(P - vec3(camX, camY, camZ));
    vec3 wv = waterWave(P.xy, pc.p0.z);
    vec3 N = normalize(vec3(-wv.x * 0.10, -wv.y * 0.10, 1.0));
    vec3 Rd = reflect(V, N);

    vec3 refl = vec3(0.0);
    float hit = 0.0, edge = 1.0;
    if (Rd.z > 0.02) {
        float t = 10.0;
        for (int i = 0; i < 28; ++i) {
            vec3 Q = P + Rd * t;
            float rx = Q.x - camX, ry = Q.y - camY;
            float ez = rx * cosA + ry * sinA;
            if (ez <= nearP) break;
            float ex = rx * sinA - ry * cosA;
            float sx = w * 0.5 + focal * ex / ez;
            float sy = horizonY - focal * (Q.z - camZ) / ez;
            if (sx < 0.0 || sx >= w || sy < 0.0 || sy >= h) break;
            float sd = linDepth(texelFetch(uDepth, ivec2(sx, sy), 0).r);
            float diff = ez - sd;
            if (diff > 1.0 && diff < t * 0.8 + 60.0) {
                refl = texture(uScene, vec2(sx / w, sy / h)).rgb;
                hit = 1.0;
                vec2 ec = min(vec2(sx, sy), vec2(w, h) - vec2(sx, sy)) / vec2(w, h);
                edge = clamp(min(ec.x, ec.y) * 6.0, 0.0, 1.0);
                break;
            }
            t = t * 1.32 + 7.0;
        }
    }
    // Stronger than a physically-minimal fresnel would give — matches
    // world.frag's own sky-reflection fresnel bump (see its comment): real
    // water reads close to a mirror at grazing angles, not a faint sheen.
    float fres = 0.03 + 0.55 * pow(1.0 - clamp(-V.z, 0.0, 1.0), 5.0);
    return mix(baseHDR, refl, hit * edge * fres * pc.p3.z);
}

void main() {
    vec2 uv = fragUV;
    ivec2 texel = ivec2(clamp(uv, vec2(0.0), vec2(0.9999)) * pc.p3.xy);

    vec4 sc = texelFetch(uScene, texel, 0);
    float ez0 = depthAtUV(uv);
    bool isWater = sc.a < 0.25 && ez0 < 50000.0;
    bool isSky   = sc.a >= 0.25 && sc.a < 0.75;

    vec3 hdr = texture(uScene, uv).rgb;

    // 1. water SSR
    if (isWater && pc.p3.z > 0.0) {
        hdr = waterSSR(uv, hdr, ez0);
    }

    // 2. ambient (SSAO + god rays + dynamic-light shadows) — one bilinear
    // sample of the quarter-res buffer worldambient.frag pre-computed this
    // frame: r = shadow, g = AO, b = god-ray glow. AO multiplies first,
    // then the god-ray glow adds (with its fixed warm tint), then shadow
    // multiplies last — so a shadowed pixel dims its own god-ray
    // contribution too.
    vec3 amb = texture(uAmbientTerm, uv).rgb;
    if (!isWater && !isSky && ez0 < 20000.0) {
        hdr *= amb.g; // SSAO
    }
    if (pc.p3.w > 0.0) {
        hdr += amb.b * pc.p3.w * vec3(1.06, 1.0, 0.88); // god rays
    }
    if (!isWater && !isSky && ez0 < 20000.0) {
        hdr *= amb.r; // dynamic-light shadows
    }

    // 3. tonemap
    vec3 mapped = aces((hdr + texture(uBloom, uv).rgb * pc.p0.y) * pc.p0.x);
    outColor = vec4(mapped, 1.0);
}
