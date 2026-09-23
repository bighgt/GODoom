#version 450

// Hardware renderer — quarter-resolution "ambient" pass: dynamic-light
// shadows, SSAO, and god rays, all pre-computed here into one small
// offscreen buffer that worldblit.frag later samples back at full res
// through the ordinary linear sampler. All three are inherently
// low-frequency effects (a soft shadow penumbra, an ambient-occlusion
// darkening, a light-shaft glow) — the low-res-then-bilinear-upsample read
// is a real blur from hardware texture filtering, not shader math, the
// same trick the shadow term alone used to use before this pass grew to
// cover SSAO/god rays too. Running the two per-pixel-expensive effects
// (SSAO's disc gather, god rays' 24-step march) at 1/16th the pixel count
// is the actual performance win; the soft blur is a side benefit.
//
// Output channels: r = shadow term (1 = unshadowed), g = SSAO (1 = no
// occlusion), b = god-ray glow (a luminance proxy — see godRaysLuma).
// Pairs with the existing fullscreen-triangle blit.vert.

layout(location = 0) in vec2 fragUV;
layout(location = 0) out vec4 outColor;

layout(set = 0, binding = 0) uniform sampler2D uDepth; // world pass D32, FULL render res
layout(set = 0, binding = 2) uniform sampler2D uScene; // world pass HDR colour, FULL render res — god rays only

// A prefix of world.frag's real World UBO, laid out identically through
// lightPos[] so the offsets line up with what the CPU actually wrote there
// (this pass only reads lightPos[], but std140 has no way to "skip" the
// spot fields without declaring them — omitting them, as the pre-move copy
// of this code in worldblit.frag used to, silently reads the flashlight's
// spotPosInvR2/spotDirCosOuter/spotColor/spotCosInner as if they were the
// first four dynamic lights instead of the real ones).
layout(set = 0, binding = 1, std140) uniform W {
    vec4 cam, screen, light, nLights, fog, amb;
    vec4 spotPosInvR2, spotDirCosOuter, spotColor, spotCosInner; // unused here
    vec4 lightPos[64]; // xyz = world pos, w = 1 / radius^2 — keep == worldMaxLights
} wU;

layout(push_constant) uniform PC {
    vec4 q0; // nearPlane, farPlane, sinA, cosA
    vec4 q1; // focal(px), camX, camY, camZ
    vec4 q2; // horizonY(px, pitch-sheared), fullWidth, fullHeight, shadowCasterCount
    vec4 q3; // shadowStrength, shadowMarchDist, shadowDepthBias, aoStrength
    vec4 q4; // aoRadius(world u), aoBias(world u), godrayStrength, (pad)
    vec4 q5; // sunDir.xyz (world), (pad)
} pc;

// linDepth / depthAtUV / worldFromPixel / worldFromUV — identical logic to
// worldblit.frag's (duplicated rather than shared: this build has no
// cross-file GLSL #include, and the project already duplicates small
// helpers like this between world.frag/worldblit.frag the same way). All
// resolution-dependent math below (UV radii, screen projections) uses the
// FULL render resolution (pc.q2.yz) throughout, not this pass's own
// (quarter) output size — UV space is 0..1 regardless of which resolution
// is currently rendering into it, so the exact same formulas the full-res
// version used still give the right on-screen radius/position; only the
// actual texel fetches this pass makes land on this pass's own coarser
// grid (via gl_FragCoord/fragUV, sized by the framebuffer, not by
// anything in this shader), which is what makes SSAO cheaper AND softer
// here, appropriately for a blurry effect.
float linDepth(float ndcZ) {
    float n = pc.q0.x, f = pc.q0.y;
    return n * f / (f - (f - n) * ndcZ);
}

float depthAtUV(vec2 uv) {
    vec2 res = pc.q2.yz; // uDepth's own (full) resolution
    ivec2 t = ivec2(clamp(uv, vec2(0.0), vec2(0.9999)) * res);
    return linDepth(texelFetch(uDepth, t, 0).r);
}

vec3 worldFromPixel(vec2 fpix, float ez) {
    float w = pc.q2.y, focal = pc.q1.x;
    float sinA = pc.q0.z, cosA = pc.q0.w;
    float colF = (fpix.x - w * 0.5) / focal;
    float kx = cosA + colF * sinA;
    float ky = sinA - colF * cosA;
    float wz = pc.q1.w + ez * (pc.q2.x - fpix.y) / focal;
    return vec3(pc.q1.y + ez * kx, pc.q1.z + ez * ky, wz);
}

vec3 worldFromUV(vec2 uv, float ez) { return worldFromPixel(uv * pc.q2.yz, ez); }

float hash12(vec2 p) {
    return fract(sin(dot(p, vec2(12.9898, 78.233))) * 43758.5453);
}

// interleavedGradientNoise (Jorge Jimenez) — like hash12, a 2D-position ->
// 0..1 dither source, but spatially structured instead of pure noise:
// neighbouring pixels get smoothly-varying (not independently random)
// values. ssao() below rotates its sample disc by this instead of hash12
// specifically because the AO term is bilinearly upsampled 4x per axis
// (this pass runs at quarter render resolution — see the file header); a
// hash's neighbour-to-neighbour randomness survives that upsample as a
// speckled grey noise baked into the final image (worst in corners, where
// AO darkens the most and the same relative noise reads strongest) — this
// is exactly what was reported as "transparent grey dots on walls, worst
// in corners." IGN's spatial coherence means adjacent quarter-res texels'
// differently-rotated sample discs average out under the same bilinear
// upsample instead of amplifying into visible dots.
float interleavedGradientNoise(vec2 p) {
    return fract(52.9829189 * fract(dot(p, vec2(0.06711056, 0.00583715))));
}

// ---- dynamic-light shadows ------------------------------------------------

// For each of the first pc.q2.w lights (muzzle flash, in-flight shots,
// monster attacks, then the nearest torches), march uDepth from the shaded
// point P toward the light. On-screen geometry between them occludes that
// light; the pixel is dimmed by that light's falloff-weighted share, so a
// distant or dim light casts only a faint shadow. Screen-space only — an
// occluder the camera can't see casts nothing.
//
// PENUMBRA_NEAR/FAR bound a soft-shadow proxy: an occluder found early in
// the march (close to the shaded point — think a monster's own foot right
// at the contact) blocks with a narrow, near-hard transition, while one
// found late (close to the light — a wall edge far across the room) spreads
// its shadow over a wide band, the way a real (non-infinitesimal) light
// source would.
const float PENUMBRA_NEAR = 3.0;
const float PENUMBRA_FAR  = 48.0;

float dynShadow(vec3 P) {
    int n = int(pc.q2.w + 0.5);
    if (n <= 0) return 1.0;
    float strength = pc.q3.x, marchMax = pc.q3.y, bias = pc.q3.z;

    float w = pc.q2.y, h = pc.q2.z, focal = pc.q1.x;
    float sinA = pc.q0.z, cosA = pc.q0.w, nearP = pc.q0.x;
    float camX = pc.q1.y, camY = pc.q1.z, camZ = pc.q1.w, horizonY = pc.q2.x;

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
            if (diff > 0.0 && diff < 96.0) {
                float travelled = stepLen * float(i);
                float penumbra = mix(PENUMBRA_NEAR, PENUMBRA_FAR, clamp(travelled / max(reach, 1.0), 0.0, 1.0));
                occ = max(occ, smoothstep(bias, bias + penumbra, diff));
                if (occ >= 0.999) break; // fully opaque already — keep marching would only confirm it
            }
        }
        removed += occ * atten * atten;
    }
    return 1.0 - clamp(removed, 0.0, 1.0) * strength;
}

// ---- SSAO ------------------------------------------------------------------

vec3 reconNormal(vec2 uv, vec3 P) {
    vec2 tx = vec2(1.0 / pc.q2.y, 0.0), ty = vec2(0.0, 1.0 / pc.q2.z);
    float dC = depthAtUV(uv);
    float dR = depthAtUV(uv + tx), dL = depthAtUV(uv - tx);
    float dD = depthAtUV(uv + ty), dU = depthAtUV(uv - ty);
    vec3 pR = worldFromUV(uv + tx, dR);
    vec3 pL = worldFromUV(uv - tx, dL);
    vec3 pD = worldFromUV(uv + ty, dD);
    vec3 pU = worldFromUV(uv - ty, dU);
    vec3 ddx = (abs(dR - dC) < abs(dC - dL)) ? (pR - P) : (P - pL);
    vec3 ddy = (abs(dD - dC) < abs(dC - dU)) ? (pD - P) : (P - pU);
    vec3 n = cross(ddx, ddy);
    float nl = length(n);
    if (nl < 1e-6) return vec3(0.0, 0.0, 1.0);
    n /= nl;
    vec3 toCam = vec3(pc.q1.y, pc.q1.z, pc.q1.w) - P;
    if (dot(n, toCam) < 0.0) n = -n;
    return n;
}

float ssao(vec2 uv, vec3 P, vec3 N, float ez0) {
    float w = pc.q2.y, h = pc.q2.z, focal = pc.q1.x;
    float radius = pc.q4.x;
    float rUVx = (radius * focal / max(ez0, 1.0)) / w;
    float rUVy = (radius * focal / max(ez0, 1.0)) / h;
    vec3 Pb = P + N * pc.q4.y; // bias off the surface

    // gl_FragCoord.xy — this PASS's own (quarter-res) pixel coordinates,
    // consecutive integers between neighbouring invocations — not uv*(w,h)
    // (w/h here are the FULL render resolution; since this shader runs once
    // per QUARTER-res pixel, that multiply strides by 4 between neighbours
    // instead of by 1). A hash doesn't care about that: scrambled output
    // looks equally random at any input spacing. IGN does: it's a
    // structured gradient function meant to be sampled at consecutive
    // integer pixels, and evaluating it at a stride-4 subset of its own
    // domain aliases into a coarser, visible repeating pattern — exactly
    // the "looks like a pattern, not noise" report after the hash->IGN
    // swap. Sampling at this pass's native coordinates is the fix.
    float rot = interleavedGradientNoise(gl_FragCoord.xy) * 6.2831853;
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
    float ao = 1.0 - (occ / float(K)) * (pc.q3.w * 3.0);
    return clamp(ao, 1.0 - pc.q3.w, 1.0);
}

// ---- god rays ---------------------------------------------------------

// Reduced to a luminance proxy (godRaysLuma returns a scalar, not the full
// accumulated colour the pre-move version did): worldblit.frag re-applies
// the fixed warm tint at composite time, same as before. God rays pick up
// mostly sky-blue and bloom-white streaks (the accumulation is explicitly
// gated on `sky`/`bright`, see below), which read close to grayscale
// already, so this trades a little hue nuance in exchange for not needing
// to carry a full RGB accumulation through a quarter-res single-channel
// budget shared with shadow/AO.
float godRaysLuma(vec2 uv) {
    float w = pc.q2.y, h = pc.q2.z, focal = pc.q1.x;
    float sinA = pc.q0.z, cosA = pc.q0.w;
    vec3 sd = pc.q5.xyz;

    float ez = sd.x * cosA + sd.y * sinA;   // camera-forward part of the sun dir
    if (ez <= 0.03) return 0.0;             // sun behind the camera
    float ex = sd.x * sinA - sd.y * cosA;
    vec2 sun = vec2(0.5 + focal * ex / ez / w,
                    (pc.q2.x - focal * sd.z / ez) / h);

    vec2 m = clamp(sun, vec2(0.0), vec2(1.0));
    float off = length((sun - m) * vec2(w, h));
    float sunVis = clamp(1.0 - off / (0.6 * h), 0.0, 1.0);
    if (sunVis <= 0.0) return 0.0;

    // N is uniform across the whole frame (sunVis doesn't depend on uv —
    // see the file header on why), so this is a wave-coherent branch, not
    // per-pixel divergence. delta's own /float(N) keeps the march's total
    // reach (0.85 of the sun-to-pixel vector) the same regardless of N —
    // fewer steps only coarsens the sampling density, it doesn't shorten
    // the streak. Halving it when the sun is already fading toward the
    // edge of view (sunVis low, so the final result is heavily damped by
    // the `* sunVis` below anyway) trades a little accumulation smoothness
    // for cost only in the regime where that smoothness is least visible.
    int N = sunVis > 0.4 ? 24 : 12;
    vec2 delta = (sun - uv) * (0.85 / float(N));
    // Jitter the march's starting phase per pixel (same hash12 the SSAO
    // gather below rotates its disc with) instead of every pixel sampling
    // at the exact same 24 fixed offsets from its own uv: with a fixed
    // phase, pixels along any line through the sun sample at correlated
    // distances-from-sun, which reads as concentric/parallel banding once
    // this quarter-res result is bilinearly upsampled — most visible as
    // near-horizontal bands when the sun sits off to one side (a march
    // direction close to horizontal), and most noticeable while turning
    // quickly sweeps the sun through view. Randomizing the phase turns
    // that coherent banding into imperceptible noise without changing the
    // effect's overall brightness (same 24 samples, same total decay).
    vec2 p = uv + delta * hash12(uv * vec2(w, h) + 17.0);
    float decay = 1.0;
    float acc = 0.0;
    for (int i = 0; i < N; ++i) {
        p += delta;
        vec2 cp = clamp(p, vec2(0.0), vec2(1.0));
        vec4 s = texture(uScene, cp);
        // alpha 0.5 == sky (world.frag marks it): ~1 there, ~0 for world/water.
        float sky = 1.0 - smoothstep(0.05, 0.35, abs(s.a - 0.5));
        float bright = smoothstep(0.75, 1.7, dot(s.rgb, vec3(0.2126, 0.7152, 0.0722)));
        acc += dot(s.rgb, vec3(0.2126, 0.7152, 0.0722)) * max(sky, bright) * decay;
        decay *= 0.92;
    }
    // Cap the result: a very bright HDR sky/bloom texel (there is no upper
    // bound on scene-linear brightness here) times up to N=24 accumulated,
    // decayed samples could otherwise dump enough luminance into worldblit's
    // hdr += amb.b * godrayStrength * ... to blow the tonemap input into a
    // flat, banded white for a few frames right as the sun sweeps past
    // (again, most visible while turning quickly) rather than the soft glow
    // this effect is meant to be.
    return min(acc / float(N) * sunVis, 3.0);
}

void main() {
    float ez0 = depthAtUV(fragUV);
    bool isBackground = ez0 >= 20000.0;

    float shadow = 1.0;
    float ao = 1.0;
    if (!isBackground) {
        vec3 P = worldFromUV(fragUV, ez0);
        if (pc.q2.w >= 0.5 && pc.q3.x > 0.0) {
            shadow = dynShadow(P);
        }
        if (pc.q3.w > 0.0) {
            vec3 N = reconNormal(fragUV, P);
            ao = ssao(fragUV, P, N, ez0);
        }
    }

    float godray = 0.0;
    if (pc.q4.z > 0.0) {
        godray = godRaysLuma(fragUV);
    }

    outColor = vec4(shadow, ao, godray, 1.0);
}
