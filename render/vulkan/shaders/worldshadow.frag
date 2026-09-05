#version 450

// Hardware renderer — dynamic-light shadow-term pass. Runs at QUARTER the
// render resolution into its own small offscreen buffer; worldblit.frag
// later samples that buffer back at full res through the ordinary linear
// sampler. The low-res-then-bilinear-upsample IS the soft blur — no hand-
// written blur math, no separate Gaussian pass — while the march itself
// (unchanged from its previous home in worldblit.frag) now costs 1/16th the
// per-pixel work. Pairs with the existing fullscreen-triangle blit.vert.

layout(location = 0) in vec2 fragUV;
layout(location = 0) out vec4 outColor;

layout(set = 0, binding = 0) uniform sampler2D uDepth; // world pass D32, FULL render res

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
    vec4 q3; // shadowStrength, shadowMarchDist, shadowDepthBias, (pad)
} pc;

// linDepth / depthAtUV / worldFromPixel / worldFromUV — identical logic to
// worldblit.frag's (duplicated rather than shared: this build has no
// cross-file GLSL #include, and the project already duplicates small
// helpers like this between world.frag/worldblit.frag the same way).
float linDepth(float ndcZ) {
    float n = pc.q0.x, f = pc.q0.y;
    return n * f / (f - (f - n) * ndcZ);
}

float depthAtUV(vec2 uv) {
    vec2 res = pc.q2.yz; // uDepth's own (full) resolution, not this pass's output size
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
                // Soft transition across `bias` instead of a hard cliff at
                // it, widened by how far along the ray (toward the light)
                // this occluder sample sits — see PENUMBRA_* above.
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

void main() {
    float ez0 = depthAtUV(fragUV);
    // Same guards worldblit.frag used to apply before calling dynShadow():
    // no casters, shadows disabled, or a background/void pixel — leave the
    // term at 1 (unshadowed) rather than marching for nothing. Water/sky
    // exclusion still happens where the term is APPLIED (worldblit.frag
    // already has the scene alpha tag it needs for that); this pass doesn't
    // need to know about water/sky itself.
    if (pc.q2.w < 0.5 || pc.q3.x <= 0.0 || ez0 >= 20000.0) {
        outColor = vec4(1.0);
        return;
    }
    float s = dynShadow(worldFromUV(fragUV, ez0));
    outColor = vec4(s, s, s, 1.0);
}
