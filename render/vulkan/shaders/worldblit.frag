#version 450

// Hardware renderer — present pass. worldresolve.frag already ran water SSR
// + the ambient (SSAO/god-ray/dyn-shadow) combine + the ACES tonemap once
// per pixel into uResolved (see its own doc comment for why that split
// exists: this pass's FXAA/CAS neighbour taps used to re-derive that whole
// chain independently, up to 8-10 times per pixel). This fullscreen
// triangle (blit.vert) now just reads that resolved LDR image, runs
// FXAA/CAS/the cartoon outline on it, upscales it letterboxed to the
// swapchain, and blends the un-composited 2D overlay (HUD, weapon) on top
// LDR and crisp.
//
// Post chain, in order (each gated by its own strength push-constant, 0 =
// skip):
//   1. FXAA        — console-variant edge AA on the resolved image.
//   2. CAS         — FidelityFX-style contrast-adaptive sharpen.
//   3. outline     — config cartoonShading: darkens pixels where uDepth
//                    jumps sharply against its 4 neighbours (a silhouette,
//                    a corner, geometry against the sky) toward black.
//   4. tint        — sector-effect screen tint (lava/nukage/blood).
//   5. overlay + ordered-free triangular dither to kill 8-bit banding.
//
// isWater/isSky still need a cheap per-pixel classification here (to skip
// FXAA/CAS on them, same reasoning as before) — sc.a is a plain texelFetch,
// no SSR/tonemap involved, so that stays a direct uScene read rather than
// something worldresolve.frag needs to hand off.

layout(location = 0) in vec2 fragUV;
layout(location = 0) out vec4 outColor;

layout(set = 0, binding = 0) uniform sampler2D uScene;   // HDR, render res — only .a (isWater/isSky) read here now
layout(set = 0, binding = 2) uniform sampler2D uOverlay; // LDR straight-alpha
layout(set = 0, binding = 3) uniform sampler2D uDepth;   // world pass D32 depth, render res (nearest)
// The pre-resolved (SSR + ambient + tonemap already applied) LDR image —
// worldresolve.frag, see its own doc comment. Binding 1 of set 0 (uBloom)
// is still allocated/written by worldsetup.go (shared framePool sizing is
// deliberately left untouched — see its own "GOTCHA that bit twice" note)
// but no longer declared/sampled here; an unused descriptor binding is
// legal Vulkan. This is set 1, same slot the quarter-res ambient term used
// to occupy before ITS combine moved into worldresolve.frag too — so this
// pass's own dedicated pool never has to touch worldsetup.go's shared
// framePool sizing either.
layout(set = 1, binding = 0) uniform sampler2D uResolved;

layout(push_constant) uniform PC {
    vec4 p0; // exposure, bloomStrength, waterClock, nearPlane
    vec4 p1; // sinA, cosA, focal(px), farPlane
    vec4 p2; // camX, camY, camZ, horizonY(px, pitch-sheared)
    vec4 p3; // width, height, ssrStrength, godrayStrength — zw unused here now (worldresolve.frag's own)
    vec4 p4; // sharpenStrength(0..1), ditherStrength(LSBs), unused, cartoonOutlineStrength(0=off)
    vec4 p5; // screen tint: rgb, amount (0 = none; amount < 0 also = heat wobble)
} pc;

// ---- shared helpers -------------------------------------------------------

float luma601(vec3 c) { return dot(c, vec3(0.299, 0.587, 0.114)); }

float hash12(vec2 p) {
    return fract(sin(dot(p, vec2(12.9898, 78.233))) * 43758.5453);
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

// ---- 1. FXAA (console variant, on the resolved image) ------------------

vec3 fxaa(vec2 uv, vec3 mid) {
    vec2 tx = 1.0 / pc.p3.xy;
    float lM = luma601(mid);
    float lNW = luma601(texture(uResolved, uv + vec2(-tx.x, -tx.y)).rgb);
    float lNE = luma601(texture(uResolved, uv + vec2( tx.x, -tx.y)).rgb);
    float lSW = luma601(texture(uResolved, uv + vec2(-tx.x,  tx.y)).rgb);
    float lSE = luma601(texture(uResolved, uv + vec2( tx.x,  tx.y)).rgb);
    float lMin = min(lM, min(min(lNW, lNE), min(lSW, lSE)));
    float lMax = max(lM, max(max(lNW, lNE), max(lSW, lSE)));
    if (lMax - lMin < max(0.03, lMax * 0.125)) return mid;

    vec2 dir = vec2(-((lNW + lNE) - (lSW + lSE)),
                     ((lNW + lSW) - (lNE + lSE)));
    float dirReduce = max((lNW + lNE + lSW + lSE) * 0.03125, 0.0078125);
    float rcpMin = 1.0 / (min(abs(dir.x), abs(dir.y)) + dirReduce);
    dir = clamp(dir * rcpMin, -8.0, 8.0) * tx;

    // Primary blend only — a second-order refinement (a rgbB pass plus a
    // min/max reject test) used to live here too; dropped since it only
    // ever changed the result on the minority of edge pixels where rgbA's
    // luma fell outside the local min/max. Each tap here is now a single
    // uResolved texture() read (worldresolve.frag already paid the SSR/
    // ambient/tonemap cost once), not the 2-fetch-plus-ACES "mappedAt" this
    // used to call.
    return texture(uResolved, uv + dir * (1.0 / 3.0 - 0.5)).rgb * 0.5 +
           texture(uResolved, uv + dir * (2.0 / 3.0 - 0.5)).rgb * 0.5;
}

// ---- 2. CAS (contrast-adaptive sharpen, FidelityFX RCAS shape) --------

vec3 cas(vec2 uv, vec3 e) {
    vec2 tx = 1.0 / pc.p3.xy;
    vec3 n = texture(uResolved, uv - vec2(0.0, tx.y)).rgb;
    vec3 s = texture(uResolved, uv + vec2(0.0, tx.y)).rgb;
    vec3 wv = texture(uResolved, uv - vec2(tx.x, 0.0)).rgb;
    vec3 ev = texture(uResolved, uv + vec2(tx.x, 0.0)).rgb;
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

    vec3 mapped = texture(uResolved, uv).rgb;

    // 1. FXAA + 2. CAS — skip on water and sky: their neighbour taps would
    // wash out the SSR reflection / sharpen a smooth gradient. Silhouette
    // edges against sky are still cleaned from the geometry side.
    if (!isWater && !isSky) {
        mapped = fxaa(uv, mapped);
        if (pc.p4.x > 0.0) mapped = cas(uv, mapped);
    }

    // 3. cartoon outline (config cartoonShading) — runs on everything,
    // water and sky included: a boat's waterline and a building's roofline
    // against the sky are exactly the silhouettes a cel-shaded look wants
    // outlined, not edges to protect from.
    if (pc.p4.w > 0.0) {
        float edge = cartoonOutline(texel, ez0) * pc.p4.w;
        mapped = mix(mapped, vec3(0.0), edge);
    }

    // 4. sector-effect screen tint (lava / nukage / blood) — after tonemap,
    // before the HUD so the overlay stays true-colour. A negative amount also
    // asks for a slow heat wobble + brightness breathing (lava).
    float tintAmt = abs(pc.p5.w);
    if (tintAmt > 0.0) {
        if (pc.p5.w < 0.0) {
            vec2 warp = vec2(sin(uv.y * 42.0 + pc.p0.z * 3.1),
                             cos(uv.x * 39.0 + pc.p0.z * 2.7)) * 0.0016;
            mapped = mix(mapped, texture(uResolved, clamp(uv + warp, 0.0, 1.0)).rgb, 0.55);
            mapped *= 1.0 + 0.05 * sin(pc.p0.z * 2.0);
        }
        mapped = mix(mapped, pc.p5.rgb, clamp(tintAmt, 0.0, 0.85));
    }

    // 5. overlay (crisp, unsharpened) + triangular-PDF dither
    vec4 ov = texture(uOverlay, uv);
    vec3 col = mix(mapped, ov.rgb, ov.a);

    float r1 = hash12(gl_FragCoord.xy);
    float r2 = hash12(gl_FragCoord.xy + 17.3);
    col += vec3((r1 + r2 - 1.0) * pc.p4.y / 255.0);

    outColor = vec4(clamp(col, 0.0, 1.0), 1.0);
}
