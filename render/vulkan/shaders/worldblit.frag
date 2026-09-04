#version 450

// Hardware renderer — present pass. The world pipeline rendered the scene
// into an offscreen RGBA16F image at render resolution (dynamic lights can
// pile past 1.0); worldbloom blurred its bright part into uBloom at half
// res. This fullscreen triangle (blit.vert) runs the post chain and
// upscales it letterboxed to the swapchain, with the un-composited 2D
// overlay (HUD, weapon) blended on top LDR and crisp.
//
// Post chain, in order (each gated by its own strength push-constant, 0 =
// skip), all sampling only what is already bound — no extra passes:
//   1. water SSR   — world.frag stamped water pixels' scene alpha to 0;
//                    reconstruct the world point from (pixel, sampled
//                    depth), reflect the eye ray about the rippled normal,
//                    march uDepth for the first geometry it crosses.
//   2. SSAO        — reconstruct position + normal from uDepth, gather a
//                    rotated disc, darken creases. World pixels only.
//   3. god rays    — radial scatter of sky / bright HDR toward the sun's
//                    projected screen point.
//   4. tonemap     — ACES filmic (Narkowicz fit) of (scene+bloom)*exposure.
//   5. FXAA        — console-variant edge AA on the tonemapped image.
//   6. CAS         — FidelityFX-style contrast-adaptive sharpen.
//   7. overlay + ordered-free triangular dither to kill 8-bit banding.

layout(location = 0) in vec2 fragUV;
layout(location = 0) out vec4 outColor;

layout(binding = 0) uniform sampler2D uScene;   // HDR, render res
layout(binding = 1) uniform sampler2D uBloom;   // HDR, half res (linear upsample)
layout(binding = 2) uniform sampler2D uOverlay; // LDR straight-alpha
layout(binding = 3) uniform sampler2D uDepth;   // world pass D32 depth, render res (nearest)

layout(push_constant) uniform PC {
    vec4 p0; // exposure, bloomStrength, waterClock, nearPlane
    vec4 p1; // sinA, cosA, focal(px), farPlane
    vec4 p2; // camX, camY, camZ, horizonY(px, pitch-sheared)
    vec4 p3; // width, height, ssrStrength, aoStrength
    vec4 p4; // sunDir.xyz (world), godrayStrength
    vec4 p5; // aoRadius(world u), sharpenStrength(0..1), ditherStrength(LSBs), aoBias(world u)
    vec4 p6; // dyn-light shadows: count, strength, marchDist, depthBias
    vec4 p7; // screen tint: rgb, amount (0 = none; amount < 0 also = heat wobble)
} pc;

// The world UBO, bound here too (binding 4) — dynShadow() needs the light
// positions. Layout must match world.frag's World block up to lightPos[].
layout(binding = 4, std140) uniform W {
    vec4 cam, screen, light, nLights, fog, amb;
    vec4 lightPos[64]; // xyz = world pos, w = 1 / radius^2
} wU;

// ---- shared helpers -------------------------------------------------------

float luma(vec3 c) { return dot(c, vec3(0.2126, 0.7152, 0.0722)); }
float luma601(vec3 c) { return dot(c, vec3(0.299, 0.587, 0.114)); }

float hash12(vec2 p) {
    return fract(sin(dot(p, vec2(12.9898, 78.233))) * 43758.5453);
}

// ACES filmic tonemap — Krzysztof Narkowicz's cheap fit. Input scene-linear,
// output display-linear (the swapchain is *_SRGB, so the hardware applies
// the sRGB OETF on write — no gamma here). Replaces the old softKnee curve
// (kept below): a proper toe + shoulder, so highlights roll off and
// desaturate instead of clipping flat.
vec3 aces(vec3 x) {
    const float a = 2.51, b = 0.03, c = 2.43, d = 0.59, e = 0.14;
    return clamp((x * (a * x + b)) / (x * (c * x + d) + e), 0.0, 1.0);
}

// softKnee: the previous tonemap (identity to KNEE, gentle compression
// above). Retained as the fall-back look — swap it for aces() in main() if
// the filmic curve reads too dark against the software path.
vec3 softKnee(vec3 c) {
    const float KNEE = 0.8;
    vec3 over = max(c - vec3(KNEE), 0.0);
    return min(c, vec3(KNEE)) + (1.0 - KNEE) * (over / (over + (1.0 - KNEE)));
}

// waterWave — identical constants to world.frag / raster.waterWave so the
// SSR ripple matches the surface ripple. .xy = slope, .z = crest.
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

// linDepth: NDC z (0..1, what worldgeo.ViewProj writes) -> camera-forward
// distance, using the same near/far the projection did.
float linDepth(float ndcZ) {
    float n = pc.p0.w, f = pc.p1.w;
    return n * f / (f - (f - n) * ndcZ);
}

float depthAtUV(vec2 uv) {
    vec2 res = pc.p3.xy;
    ivec2 t = ivec2(clamp(uv, vec2(0.0), vec2(0.9999)) * res);
    return linDepth(texelFetch(uDepth, t, 0).r);
}

// worldFromPixel: render-res pixel (origin top-left) + camera-forward
// distance ez -> world point. Inverse of raster.drawFlatSpan's column ray
// and its horizonY/focal vertical mapping (== worldgeo.ViewProj).
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

// tonemapped (scene + bloom)*exposure at uv — the base image FXAA/CAS
// operate on. SSAO / god rays / SSR are not re-run per tap (low frequency).
vec3 mappedAt(vec2 uv) {
    vec3 hdr = texture(uScene, uv).rgb + texture(uBloom, uv).rgb * pc.p0.y;
    return aces(hdr * pc.p0.x);
}

// ---- 1. water SSR -------------------------------------------------------

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
    float fres = 0.03 + 0.32 * pow(1.0 - clamp(-V.z, 0.0, 1.0), 5.0);
    return mix(baseHDR, refl, hit * edge * fres * pc.p3.z);
}

// ---- 2. SSAO ---------------------------------------------------------------

vec3 reconNormal(vec2 uv, vec3 P) {
    vec2 res = pc.p3.xy;
    vec2 tx = vec2(1.0 / res.x, 0.0), ty = vec2(0.0, 1.0 / res.y);
    float dC = depthAtUV(uv);
    float dR = depthAtUV(uv + tx), dL = depthAtUV(uv - tx);
    float dD = depthAtUV(uv + ty), dU = depthAtUV(uv - ty);
    vec3 pR = worldFromUV(uv + tx, dR);
    vec3 pL = worldFromUV(uv - tx, dL);
    vec3 pD = worldFromUV(uv + ty, dD);
    vec3 pU = worldFromUV(uv - ty, dU);
    // pick the neighbour on each axis nearer in camera-forward distance, so
    // the normal isn't smeared across a depth discontinuity.
    vec3 ddx = (abs(dR - dC) < abs(dC - dL)) ? (pR - P) : (P - pL);
    vec3 ddy = (abs(dD - dC) < abs(dC - dU)) ? (pD - P) : (P - pU);
    vec3 n = cross(ddx, ddy);
    float nl = length(n);
    if (nl < 1e-6) return vec3(0.0, 0.0, 1.0);
    n /= nl;
    vec3 toCam = vec3(pc.p2.x, pc.p2.y, pc.p2.z) - P;
    if (dot(n, toCam) < 0.0) n = -n;
    return n;
}

float ssao(vec2 uv, vec3 P, vec3 N, float ez0) {
    float w = pc.p3.x, h = pc.p3.y, focal = pc.p1.z;
    float radius = pc.p5.x;
    // world radius -> screen pixels at this depth -> uv
    float rUVx = (radius * focal / max(ez0, 1.0)) / w;
    float rUVy = (radius * focal / max(ez0, 1.0)) / h;
    vec3 Pb = P + N * pc.p5.w; // bias off the surface

    float rot = hash12(uv * vec2(w, h)) * 6.2831853;
    const int K = 12;
    float occ = 0.0;
    for (int i = 0; i < K; ++i) {
        float ang = rot + float(i) * (6.2831853 / float(K));
        float rad = (float(i) + 0.5) / float(K);
        vec2 s = uv + vec2(cos(ang) * rUVx, sin(ang) * rUVy) * rad;
        if (s.x < 0.0 || s.x > 1.0 || s.y < 0.0 || s.y > 1.0) continue;
        vec3 SP = worldFromUV(s, depthAtUV(s));
        vec3 dv = SP - Pb;
        float dl = length(dv);
        if (dl < 0.02 || dl > radius) continue;
        float ndl = max(dot(N, dv / dl), 0.0);
        occ += ndl * (1.0 - dl / radius);
    }
    float ao = 1.0 - (occ / float(K)) * (pc.p3.w * 3.0);
    return clamp(ao, 1.0 - pc.p3.w, 1.0);
}

// ---- 3. god rays --------------------------------------------------------

vec3 godRays(vec2 uv) {
    float w = pc.p3.x, h = pc.p3.y, focal = pc.p1.z;
    float sinA = pc.p1.x, cosA = pc.p1.y;
    vec3 sd = pc.p4.xyz;

    float ez = sd.x * cosA + sd.y * sinA;   // camera-forward part of the sun dir
    if (ez <= 0.03) return vec3(0.0);       // sun behind the camera
    float ex = sd.x * sinA - sd.y * cosA;
    vec2 sun = vec2(0.5 + focal * ex / ez / w,
                    (pc.p2.w - focal * sd.z / ez) / h);

    vec2 m = clamp(sun, vec2(0.0), vec2(1.0));
    float off = length((sun - m) * vec2(w, h));
    float sunVis = clamp(1.0 - off / (0.6 * h), 0.0, 1.0);
    if (sunVis <= 0.0) return vec3(0.0);

    const int N = 24;
    vec2 delta = (sun - uv) * (0.85 / float(N));
    vec2 p = uv;
    float decay = 1.0;
    vec3 acc = vec3(0.0);
    for (int i = 0; i < N; ++i) {
        p += delta;
        vec2 cp = clamp(p, vec2(0.0), vec2(1.0));
        vec4 s = texture(uScene, cp);
        // alpha 0.5 == sky (world.frag marks it): ~1 there, ~0 for world/water.
        float sky = 1.0 - smoothstep(0.05, 0.35, abs(s.a - 0.5));
        float bright = smoothstep(0.75, 1.7, luma(s.rgb));
        acc += s.rgb * max(sky, bright) * decay;
        decay *= 0.92;
    }
    return acc / float(N) * sunVis;
}

// ---- 5. FXAA (console variant, on the tonemapped image) ----------------

vec3 fxaa(vec2 uv, vec3 mid) {
    vec2 tx = 1.0 / pc.p3.xy;
    float lM = luma601(mid);
    float lNW = luma601(mappedAt(uv + vec2(-tx.x, -tx.y)));
    float lNE = luma601(mappedAt(uv + vec2( tx.x, -tx.y)));
    float lSW = luma601(mappedAt(uv + vec2(-tx.x,  tx.y)));
    float lSE = luma601(mappedAt(uv + vec2( tx.x,  tx.y)));
    float lMin = min(lM, min(min(lNW, lNE), min(lSW, lSE)));
    float lMax = max(lM, max(max(lNW, lNE), max(lSW, lSE)));
    if (lMax - lMin < max(0.03, lMax * 0.125)) return mid;

    vec2 dir = vec2(-((lNW + lNE) - (lSW + lSE)),
                     ((lNW + lSW) - (lNE + lSE)));
    float dirReduce = max((lNW + lNE + lSW + lSE) * 0.03125, 0.0078125);
    float rcpMin = 1.0 / (min(abs(dir.x), abs(dir.y)) + dirReduce);
    dir = clamp(dir * rcpMin, -8.0, 8.0) * tx;

    vec3 rgbA = 0.5 * (mappedAt(uv + dir * (1.0 / 3.0 - 0.5)) +
                       mappedAt(uv + dir * (2.0 / 3.0 - 0.5)));
    vec3 rgbB = rgbA * 0.5 + 0.25 * (mappedAt(uv + dir * -0.5) +
                                     mappedAt(uv + dir *  0.5));
    float lB = luma601(rgbB);
    return (lB < lMin || lB > lMax) ? rgbA : rgbB;
}

// ---- 6. CAS (contrast-adaptive sharpen, FidelityFX RCAS shape) --------

vec3 cas(vec2 uv, vec3 e) {
    vec2 tx = 1.0 / pc.p3.xy;
    vec3 n = mappedAt(uv - vec2(0.0, tx.y));
    vec3 s = mappedAt(uv + vec2(0.0, tx.y));
    vec3 wv = mappedAt(uv - vec2(tx.x, 0.0));
    vec3 ev = mappedAt(uv + vec2(tx.x, 0.0));
    vec3 mn = min(e, min(min(n, s), min(wv, ev)));
    vec3 mx = max(e, max(max(n, s), max(wv, ev)));
    vec3 amp = sqrt(clamp(min(mn, 1.0 - mx) / max(mx, 1e-4), 0.0, 1.0));
    float peak = -1.0 / mix(8.0, 5.0, clamp(pc.p5.y, 0.0, 1.0));
    vec3 wgt = amp * peak;                    // negative -> subtract neighbours
    vec3 res = (e + (n + s + wv + ev) * wgt) / (1.0 + 4.0 * wgt);
    return clamp(res, 0.0, 1.0);
}

// ---- 6b. screen-space shadows for the leading dynamic lights ----------

// For each of the first pc.p6.x lights (muzzle flash, in-flight shots,
// monster attacks, then the nearest torches), march uDepth from the shaded
// point P toward the light. On-screen geometry between them occludes that
// light; the pixel is dimmed by that light's falloff-weighted share, so a
// distant or dim light casts only a faint shadow. Screen-space only — an
// occluder the camera can't see casts nothing.
float dynShadow(vec3 P) {
    int n = int(pc.p6.x + 0.5);
    if (n <= 0) return 1.0;
    float strength = pc.p6.y, marchMax = pc.p6.z, bias = pc.p6.w;

    float w = pc.p3.x, h = pc.p3.y, focal = pc.p1.z;
    float sinA = pc.p1.x, cosA = pc.p1.y, nearP = pc.p0.w;
    float camX = pc.p2.x, camY = pc.p2.y, camZ = pc.p2.z, horizonY = pc.p2.w;

    float removed = 0.0; // Σ (falloff weight) over occluded lights
    for (int li = 0; li < 8; ++li) {
        if (li >= n) break;
        vec3 Lp = wU.lightPos[li].xyz;
        float invR2 = wU.lightPos[li].w;
        vec3 toL = Lp - P;
        float dist = length(toL);
        float atten = 1.0 - dist * dist * invR2; // 1 - (d/R)^2
        if (atten <= 0.03 || dist < 6.0) continue;
        vec3 dir = toL / dist;

        const int STEPS = 12;
        float reach = min(dist - 3.0, marchMax);
        float stepLen = reach / float(STEPS);
        float occ = 0.0;
        for (int i = 1; i <= STEPS; ++i) {
            vec3 Q = P + dir * (stepLen * float(i) + 2.0);
            float rx = Q.x - camX, ry = Q.y - camY;
            float ez = rx * cosA + ry * sinA;
            if (ez <= nearP) break;
            float ex = rx * sinA - ry * cosA;
            float sx = w * 0.5 + focal * ex / ez;
            float sy = horizonY - focal * (Q.z - camZ) / ez;
            if (sx < 0.0 || sx >= w || sy < 0.0 || sy >= h) break;
            float diff = ez - linDepth(texelFetch(uDepth, ivec2(sx, sy), 0).r);
            if (diff > bias && diff < 96.0) { occ = 1.0; break; }
        }
        removed += occ * atten * atten;
    }
    return 1.0 - clamp(removed, 0.0, 1.0) * strength;
}

// ---- main ---------------------------------------------------------------

void main() {
    vec2 uv = fragUV;
    vec2 res = pc.p3.xy;
    ivec2 texel = ivec2(clamp(uv, vec2(0.0), vec2(0.9999)) * res);

    vec4 sc = texelFetch(uScene, texel, 0);
    float ez0 = depthAtUV(uv);
    bool isWater = sc.a < 0.25 && ez0 < 50000.0;
    bool isSky   = sc.a >= 0.25 && sc.a < 0.75;

    vec3 hdr = texture(uScene, uv).rgb;

    // 1. water SSR
    if (isWater && pc.p3.z > 0.0) {
        hdr = waterSSR(uv, hdr, ez0);
    }

    // 2. SSAO — world surfaces only (not sky, not water)
    if (pc.p3.w > 0.0 && !isWater && !isSky && ez0 < 20000.0) {
        vec3 P = worldFromUV(uv, ez0);
        vec3 N = reconNormal(uv, P);
        hdr *= ssao(uv, P, N, ez0);
    }

    // 3. god rays
    if (pc.p4.w > 0.0) {
        hdr += godRays(uv) * pc.p4.w * vec3(1.06, 1.0, 0.88);
    }

    // 3b. screen-space shadows from the leading dynamic lights
    if (pc.p6.x > 0.5 && pc.p6.y > 0.0 && !isWater && !isSky && ez0 < 20000.0) {
        hdr *= dynShadow(worldFromUV(uv, ez0));
    }

    // 4. tonemap
    vec3 mapped = aces((hdr + texture(uBloom, uv).rgb * pc.p0.y) * pc.p0.x);

    // 5. FXAA + 6. CAS — skip on water and sky: their taps (mappedAt) omit
    // the SSR reflection / are a smooth gradient, so AA there would wash the
    // reflection out and sharpening would only amplify banding. Silhouette
    // edges against sky are still cleaned from the geometry side.
    if (!isWater && !isSky) {
        mapped = fxaa(uv, mapped);
        if (pc.p5.y > 0.0) mapped = cas(uv, mapped);
    }

    // 6b. sector-effect screen tint (lava / nukage / blood) — after tonemap,
    // before the HUD so the overlay stays true-colour. A negative amount also
    // asks for a slow heat wobble + brightness breathing (lava).
    float tintAmt = abs(pc.p7.w);
    if (tintAmt > 0.0) {
        if (pc.p7.w < 0.0) {
            vec2 warp = vec2(sin(uv.y * 42.0 + pc.p0.z * 3.1),
                             cos(uv.x * 39.0 + pc.p0.z * 2.7)) * 0.0016;
            mapped = mix(mapped, mappedAt(clamp(uv + warp, 0.0, 1.0)), 0.55);
            mapped *= 1.0 + 0.05 * sin(pc.p0.z * 2.0);
        }
        mapped = mix(mapped, pc.p7.rgb, clamp(tintAmt, 0.0, 0.85));
    }

    // 7. overlay (crisp, unsharpened) + triangular-PDF dither
    vec4 ov = texture(uOverlay, uv);
    vec3 col = mix(mapped, ov.rgb, ov.a);

    float r1 = hash12(gl_FragCoord.xy);
    float r2 = hash12(gl_FragCoord.xy + 17.3);
    col += vec3((r1 + r2 - 1.0) * pc.p5.z / 255.0);

    outColor = vec4(clamp(col, 0.0, 1.0), 1.0);
}
