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
//   2. ambient     — one bilinear sample of uAmbientTerm, the quarter-res
//                    buffer worldambient.frag pre-computed this frame:
//                    r = dynamic-light shadow, g = SSAO, b = god-ray glow.
//                    Moving all three off full-res pixels and onto a
//                    quarter-res buffer is a real perf win (SSAO's disc
//                    gather and god rays' march are the two most
//                    per-pixel-expensive things this shader used to do);
//                    the low-res-then-bilinear-upsample read is also what
//                    makes the shadow edge soft, not any math in this
//                    shader.
//   3. tonemap     — ACES filmic (Narkowicz fit) of (scene+bloom)*exposure.
//   4. FXAA        — console-variant edge AA on the tonemapped image.
//   5. CAS         — FidelityFX-style contrast-adaptive sharpen.
//   6. outline     — config cartoonShading: darkens pixels where uDepth
//                    jumps sharply against its 4 neighbours (a silhouette,
//                    a corner, geometry against the sky) toward black.
//   7. overlay + ordered-free triangular dither to kill 8-bit banding.

layout(location = 0) in vec2 fragUV;
layout(location = 0) out vec4 outColor;

layout(set = 0, binding = 0) uniform sampler2D uScene;   // HDR, render res
layout(set = 0, binding = 1) uniform sampler2D uBloom;   // HDR, half res (linear upsample)
layout(set = 0, binding = 2) uniform sampler2D uOverlay; // LDR straight-alpha
layout(set = 0, binding = 3) uniform sampler2D uDepth;   // world pass D32 depth, render res (nearest)
// The pre-computed shadow/AO/god-ray term (worldambient.go/.frag), quarter
// render resolution — this is a SEPARATE descriptor set (set 1) rather than
// a 5th binding on set 0, so this pass's own dedicated pool never has to
// touch worldsetup.go's shared framePool sizing. Sampled through the
// ordinary linear/clamp sampler: that bilinear upscale from quarter-res IS
// the soft blur on the shadow edge (and the AO/god-ray softening), not any
// math in this shader.
layout(set = 1, binding = 0) uniform sampler2D uAmbientTerm;

// SSAO, god-ray sun direction, and the dyn-light shadow march all moved to
// worldambient.frag's quarter-res pass (see the file header) — this layout
// only keeps what worldblit.frag itself still reads (godrayStrength, to
// scale+tint the pre-marched god-ray luma sampled from uAmbientTerm); it
// used to carry those passes' own now-unread constants too (aoStrength,
// sunDir.xyz, aoRadius/aoBias, and a whole vestigial p6 vec4) rather than
// repacking when they moved out.
layout(push_constant) uniform PC {
    vec4 p0; // exposure, bloomStrength, waterClock, nearPlane
    vec4 p1; // sinA, cosA, focal(px), farPlane
    vec4 p2; // camX, camY, camZ, horizonY(px, pitch-sheared)
    vec4 p3; // width, height, ssrStrength, godrayStrength
    vec4 p4; // sharpenStrength(0..1), ditherStrength(LSBs), unused, cartoonOutlineStrength(0=off)
    vec4 p5; // screen tint: rgb, amount (0 = none; amount < 0 also = heat wobble)
} pc;

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

// depthAtTexel is depthAtUV for an already-clamped integer texel — used by
// cartoonOutline's 4-neighbour taps, which would otherwise sample past the
// edge of uDepth for a pixel on the screen border (texelFetch outside a
// texture's bounds is undefined, unlike a filtered sampler's clamp-to-edge).
float depthAtTexel(ivec2 t) {
    ivec2 res = ivec2(pc.p3.xy) - 1;
    return linDepth(texelFetch(uDepth, clamp(t, ivec2(0), res), 0).r);
}

// cartoonOutline (config cartoonShading, pc.p4.w = strength, 0 = off): a
// cheap 4-neighbour depth-edge detector — silhouettes, corners, and
// geometry seen against the sky (whose depth reads as the far plane) all
// show up as a sharp jump against centerDepth. The threshold scales with
// distance rather than a fixed map-unit gap: at typical Doom-scale depths a
// fixed threshold either misses distant wall seams (a few units of true
// depth difference is a whole wall, far away) or false-positives on nearby
// texture-scale depth noise, so it's a fraction of the pixel's own depth
// instead. Returns 0..1 — how much of an edge this pixel sits on, not just
// a bool, so a grazing edge still darkens gently.
float cartoonOutline(ivec2 texel, float centerDepth) {
    // A 1-texel radius reads as a hairline once the render-resolution image
    // is upscaled to the window — too thin to read as a deliberate cartoon
    // outline rather than ordinary edge noise. OUTLINE_R thickens it by
    // sampling further out; still just 4 taps, so the cost stays flat.
    const int OUTLINE_R = 2;
    float dl = depthAtTexel(texel + ivec2(-OUTLINE_R, 0));
    float dr = depthAtTexel(texel + ivec2(OUTLINE_R, 0));
    float du = depthAtTexel(texel + ivec2(0, -OUTLINE_R));
    float dd = depthAtTexel(texel + ivec2(0, OUTLINE_R));
    float delta = abs(dl - centerDepth) + abs(dr - centerDepth) + abs(du - centerDepth) + abs(dd - centerDepth);
    float threshold = max(centerDepth * 0.01, 4.0);
    return clamp(delta / threshold - 1.0, 0.0, 1.0);
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

// SSAO and god rays used to live here, run per full-res pixel; both moved
// to worldambient.frag's quarter-res pass (see the file header) — main()
// below just samples their pre-computed result from uAmbientTerm now.

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

    // Primary blend only — this used to have a second-order refinement here
    // (rgbB: two more mappedAt taps plus a min/max reject test) that only
    // ever changed the result on the minority of edge pixels where rgbA's
    // luma fell outside the local min/max. Dropping it roughly halves this
    // function's worst-case cost (an edge pixel used to cost up to 8
    // mappedAt evaluations — 2 texture samples + a full ACES tonemap each
    // — now at most 6: 4 for the initial contrast check, 2 for this blend)
    // for a small loss of edge-refinement quality most players won't
    // notice next to the CAS sharpen pass that runs right after it.
    return mappedAt(uv + dir * (1.0 / 3.0 - 0.5)) * 0.5 +
           mappedAt(uv + dir * (2.0 / 3.0 - 0.5)) * 0.5;
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
    float peak = -1.0 / mix(8.0, 5.0, clamp(pc.p4.x, 0.0, 1.0));
    vec3 wgt = amp * peak;                    // negative -> subtract neighbours
    vec3 res = (e + (n + s + wv + ev) * wgt) / (1.0 + 4.0 * wgt);
    return clamp(res, 0.0, 1.0);
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

    // 2. ambient (SSAO + god rays + dynamic-light shadows) — one bilinear
    // sample of the quarter-res buffer worldambient.frag pre-computed this
    // frame: r = shadow, g = AO, b = god-ray glow. Order preserved from
    // when these ran inline here: AO multiplies first, then the god-ray
    // glow adds (with its fixed warm tint), then shadow multiplies last —
    // so a shadowed pixel dims its own god-ray contribution too, same as
    // before.
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

    // 4. FXAA + 5. CAS — skip on water and sky: their taps (mappedAt) omit
    // the SSR reflection / are a smooth gradient, so AA there would wash the
    // reflection out and sharpening would only amplify banding. Silhouette
    // edges against sky are still cleaned from the geometry side.
    if (!isWater && !isSky) {
        mapped = fxaa(uv, mapped);
        if (pc.p4.x > 0.0) mapped = cas(uv, mapped);
    }

    // 6. cartoon outline (config cartoonShading) — runs on everything,
    // water and sky included: a boat's waterline and a building's roofline
    // against the sky are exactly the silhouettes a cel-shaded look wants
    // outlined, not edges to protect from.
    if (pc.p4.w > 0.0) {
        float edge = cartoonOutline(texel, ez0) * pc.p4.w;
        mapped = mix(mapped, vec3(0.0), edge);
    }

    // 7. sector-effect screen tint (lava / nukage / blood) — after tonemap,
    // before the HUD so the overlay stays true-colour. A negative amount also
    // asks for a slow heat wobble + brightness breathing (lava).
    float tintAmt = abs(pc.p5.w);
    if (tintAmt > 0.0) {
        if (pc.p5.w < 0.0) {
            vec2 warp = vec2(sin(uv.y * 42.0 + pc.p0.z * 3.1),
                             cos(uv.x * 39.0 + pc.p0.z * 2.7)) * 0.0016;
            mapped = mix(mapped, mappedAt(clamp(uv + warp, 0.0, 1.0)), 0.55);
            mapped *= 1.0 + 0.05 * sin(pc.p0.z * 2.0);
        }
        mapped = mix(mapped, pc.p5.rgb, clamp(tintAmt, 0.0, 0.85));
    }

    // 8. overlay (crisp, unsharpened) + triangular-PDF dither
    vec4 ov = texture(uOverlay, uv);
    vec3 col = mix(mapped, ov.rgb, ov.a);

    float r1 = hash12(gl_FragCoord.xy);
    float r2 = hash12(gl_FragCoord.xy + 17.3);
    col += vec3((r1 + r2 - 1.0) * pc.p4.y / 255.0);

    outColor = vec4(clamp(col, 0.0, 1.0), 1.0);
}
