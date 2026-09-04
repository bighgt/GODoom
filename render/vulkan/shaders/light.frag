#version 450

// Enhanced-mode deferred lighting (config lightingMode "enhanced").
//
// Reads the G-buffer package raster produced this frame (unlit albedo,
// packed normal + material key, per-pixel sector light, camera-space
// depth), reconstructs each pixel's world position from depth the exact
// inverse of raster's projection, applies a smooth version of the vanilla
// PrBoom sector/distance fade as the base layer, then adds every dynamic
// point light (muzzle flash, in-flight rockets/plasma/BFG, explosions).
// Output is HDR (may exceed 1) in the same "palette" colour space the
// vanilla blit works in — the composite pass tonemaps and presents it.

layout(location = 0) in vec2 fragUV; // unused here; we texelFetch by pixel
layout(location = 0) out vec4 outColor;

layout(binding = 0) uniform sampler2D uAlbedo;
layout(binding = 1) uniform sampler2D uNormal;
layout(binding = 2) uniform sampler2D uLightParam;
layout(binding = 3) uniform sampler2D uDepth;

struct GPULight {
    vec4 posRadius;       // xyz world pos, w = 1/radius^2 (inv-square, for a
                          //   sqrt-free cull + falloff; radius is the hard cutoff)
    vec4 colorIntensity;  // rgb linear-ish colour, a intensity
};

layout(binding = 4) uniform Scene {
    vec4 camPos_extra;   // xyz camera world pos, w extralight
    vec4 view;           // sinA, cosA, focal, w = water clock (s)
    vec4 screen;         // width, height, horizonY, lightCount
    vec4 tuning;         // lightScale, exposure (composite pass), shadowStrength, shadowN
    vec4 fog;            // rgb fog colour, w = density (0 = off)
    // The player's flashlight: a single forward cone light, kept apart from
    // lights[] (isotropic point lights) because a beam needs a direction and
    // cone angle neither GPULight nor the general array carry.
    // colorIntensity.a <= 0 means off.
    vec4 spotPosRadius;      // xyz world pos, w = 1/radius^2
    vec4 spotDirCosOuter;    // xyz normalized direction, w = cos(outer half-angle)
    vec4 spotColorIntensity; // rgb colour, a intensity
    vec4 spotCosInner;       // x = cos(inner half-angle), y = ambientLight floor, zw unused
    GPULight lights[64];
} S;

const float BACKGROUND_DEPTH = 1.0e30; // sky / void: depth left at +Inf

// applyFog blends c toward the fog colour by an exponential of camera
// distance (S.fog.w = density; 0 disables). Applied to every lit / emissive
// world pixel — not the sky (which is the horizon itself).
vec3 applyFog(vec3 c, float dist) {
    float dens = S.fog.w;
    if (dens <= 0.0) return c;
    float f = 1.0 - exp(-dens * max(dist, 0.0));
    return mix(c, S.fog.rgb, clamp(f, 0.0, 1.0));
}

// worldAt reconstructs the world-space point drawn at screen pixel `frag`
// given its camera-forward depth `dd` — the inverse of raster's projection
// (kx = cosA + colF*sinA, worldZ = camZ - (py-horizonY)/focal*dd).
vec3 worldAt(vec2 frag, float dd) {
    float w = S.screen.x, horizonY = S.screen.z;
    float sinA = S.view.x, cosA = S.view.y, focal = S.view.z;
    float cf = (frag.x - w * 0.5) / focal;
    return vec3(
        S.camPos_extra.x + dd * (cosA + cf * sinA),
        S.camPos_extra.y + dd * (sinA - cf * cosA),
        S.camPos_extra.z - (frag.y - horizonY) / focal * dd);
}

// ssao: a small screen-space ambient-occlusion estimate from the depth
// buffer. For a handful of samples in a depth-scaled screen disc, the
// world point there is reconstructed; if it rises above this surface's
// plane (along N) within a short range it occludes. Returns 1 (open) down
// to ~0.4 (a deep crease). Cheap and a touch noisy — kept subtle, and only
// ever attenuates the AMBIENT term, never a direct light.
float ssao(vec3 P, vec3 N, float dd, ivec2 res) {
    if (dd <= 0.0) return 1.0;
    float radiusPx = clamp(900.0 / dd, 4.0, 40.0);
    float occ = 0.0;
    const int NS = 8;
    for (int s = 0; s < NS; ++s) {
        float a = float(s) * 2.3999632;                 // golden angle
        float r = radiusPx * (0.35 + 0.65 * float(s) / float(NS));
        ivec2 sp = ivec2(gl_FragCoord.xy + vec2(cos(a), sin(a)) * r);
        if (sp.x < 0 || sp.y < 0 || sp.x >= res.x || sp.y >= res.y) continue;
        float sd = texelFetch(uDepth, sp, 0).r;
        if (sd <= 0.0 || sd >= BACKGROUND_DEPTH) continue;
        vec3 v = worldAt(vec2(sp) + 0.5, sd) - P;
        float dist = length(v);
        if (dist < 1.0 || dist > 88.0) continue;
        occ += max(dot(N, v / dist) - 0.15, 0.0) * (1.0 - dist / 88.0);
    }
    return clamp(1.0 - occ * (3.4 / float(NS)), 0.4, 1.0);
}

// waterWave — the multi-octave height field, identical to raster.waterWave
// and world.frag, so the software water ripples the same everywhere.
vec3 waterWave(vec2 wp, float t) {
    vec2 p = wp * 0.030;
    float a = sin(p.x * 1.00 + p.y * 0.60 + t * 1.6);
    float b = sin(p.x * -0.70 + p.y * 1.30 + t * 2.1);
    float c = sin(p.x * 1.70 + p.y * -0.40 + t * 1.1);
    vec2 q = wp * 0.008;
    float e = sin(q.x * 1.00 + q.y * 0.70 + t * 0.5);
    float hgt = (a + 0.6 * b + 0.4 * c) / 2.0 + 0.35 * e;
    vec2 grad = vec2(a - c + 0.4 * e, b - 0.5 * c + 0.3 * e);
    return vec3(grad, clamp(hgt * 0.4 + 0.5, 0.0, 1.0));
}

// ssr marches the linear depth buffer from a surface point P along the
// reflected ray Rd, returning the on-screen colour where the ray first
// passes just behind a surface (rgb) and a 0/1 hit flag (a). Sky / off
// screen / no hit -> a = 0 (caller keeps its sky reflection). ~12 steps
// with a growing stride — coarse but enough for a nearby wall or a
// monster mirrored in the water.
vec4 ssr(vec3 P, vec3 Rd) {
    float w = S.screen.x, h = S.screen.y, horizonY = S.screen.z;
    float sinA = S.view.x, cosA = S.view.y, focal = S.view.z;
    vec3 camPos = S.camPos_extra.xyz;
    float step = 12.0;
    vec3 p = P + Rd * 4.0;
    for (int s = 0; s < 12; ++s) {
        p += Rd * step;
        step *= 1.32;
        vec3 rel = p - camPos;
        float pd = rel.x * cosA + rel.y * sinA;
        if (pd <= 1.0) return vec4(0.0);
        float sx = w * 0.5 + (rel.x * sinA - rel.y * cosA) / pd * focal;
        float sy = horizonY - (p.z - camPos.z) / pd * focal;
        if (sx < 0.0 || sx >= w || sy < 0.0 || sy >= h) return vec4(0.0);
        float sd = texelFetch(uDepth, ivec2(sx, sy), 0).r;
        if (sd <= 0.0 || sd >= BACKGROUND_DEPTH) continue;   // sky/void — keep going
        if (pd > sd + 1.5 && pd < sd + step * 2.0) {          // passed just behind a surface
            return vec4(texelFetch(uAlbedo, ivec2(sx, sy), 0).rgb, 1.0);
        }
    }
    return vec4(0.0);
}

// The screen-space occlusion march is only run for S.tuning.w leading
// lights (the engine's gameplay-critical dynamic ones — muzzle flash,
// in-flight shots), never the static map emitters: at per-pixel-per-light-
// per-step cost, marching every torch every frame is what tanked the frame
// rate. SHADOW_MAXCASTERS is just a compile-time loop bound.
const int   SHADOW_MAXCASTERS = 4;
const int   SHADOW_STEPS      = 8;
const float SHADOW_MAXLEN      = 200.0; // map units marched toward the light, at most

// ssShadow marches the depth buffer from a lit world point toward a light.
// Returns 1.0 unshadowed, 0.0 fully blocked by nearer geometry. The camera
// basis comes from S. A small step offset keeps a surface from shadowing
// itself.
float ssShadow(vec3 world, vec3 lightPos) {
    float w = S.screen.x, h = S.screen.y, horizonY = S.screen.z;
    float sinA = S.view.x, cosA = S.view.y, focal = S.view.z;
    vec3 camPos = S.camPos_extra.xyz;

    vec3 toL = lightPos - world;
    float ld = length(toL);
    if (ld < 1.0) return 1.0;
    vec3 dir = toL / ld;
    float march = min(ld, SHADOW_MAXLEN);
    float step = march / float(SHADOW_STEPS);

    for (int s = 1; s <= SHADOW_STEPS; ++s) {
        vec3 p = world + dir * (step * float(s));
        vec3 rel = p - camPos;
        float depth = rel.x * cosA + rel.y * sinA;
        if (depth <= 1.0) continue;
        float horiz = rel.x * sinA - rel.y * cosA;
        float sx = w * 0.5 + horiz / depth * focal;
        float sy = horizonY - (p.z - camPos.z) / depth * focal;
        if (sx < 0.0 || sx >= w || sy < 0.0 || sy >= h) continue;
        float sceneD = texelFetch(uDepth, ivec2(sx, sy), 0).r;
        if (sceneD <= 0.0 || sceneD >= BACKGROUND_DEPTH) continue;
        // a surface here is closer to the camera than our ray sample by more
        // than a slack band -> it's between the point and the light.
        float bias = 1.5 + depth * 0.03;
        if (sceneD < depth - bias) return 0.0;
    }
    return 1.0;
}

void main() {
    ivec2 ip = ivec2(gl_FragCoord.xy);
    vec3 albedo = texelFetch(uAlbedo, ip, 0).rgb;
    float d = texelFetch(uDepth, ip, 0).r;

    // Nothing was rasterised here (sky, void backdrop): pass the pixel
    // through unlit — it is already the final colour.
    if (d >= BACKGROUND_DEPTH || d <= 0.0) {
        outColor = vec4(albedo, 1.0);
        return;
    }

    vec4 nrm = texelFetch(uNormal, ip, 0);
    float mat = nrm.w; // 0 world, ~0.16 water, ~0.5 sprite, ~1 emissive
    bool isWater = (mat > 0.10 && mat < 0.22);

    // Emissive (muzzle flash / explosion / projectile sprite): unlit, and
    // pushed slightly over 1 so the composite bloom pass catches it.
    if (mat > 0.75) {
        outColor = vec4(applyFog(albedo * 1.35, d), 1.0);
        return;
    }

    vec3 N = normalize(nrm.xyz * 2.0 - 1.0);

    // world position (inverse of raster's projection — see worldAt).
    vec3 world = worldAt(gl_FragCoord.xy, d);

    // --- base sector + distance fade (smooth take on scalelight): from the
    //     8-bit sector light -> lightnum 0..15 -> startmap, minus a
    //     continuous 1/distance term (the vanilla wall curve, unquantised).
    float lightLevel = texelFetch(uLightParam, ip, 0).r * 255.0 * S.tuning.x;
    lightLevel = clamp(lightLevel, 0.0, 255.0);
    float lnum = clamp(floor(lightLevel / 16.0) + S.camPos_extra.w, 0.0, 15.0);
    // (extralight is S.camPos_extra.w)
    float startmap = (15.0 - lnum) * 4.0;
    float k = clamp((2560.0 / max(d, 1.0)) * 0.5, 0.0, 23.5);
    float row = clamp(startmap - k, 0.0, 31.0);
    float baseFade = (32.0 - row) / 32.0;

    // Ambient occlusion: attenuate only the ambient (sector-fade) term in
    // creases — a wall/floor junction, a doorway edge, under a step. Direct
    // lights and the brightmap term are untouched. Skipped for sprites
    // (mat ~0.5) and water (~0.16), which are not wall/flat geometry.
    if (mat < 0.10) {
        baseFade *= ssao(world, N, d, textureSize(uDepth, 0));
    }

    // --- point lights
    float shadowStrength = clamp(S.tuning.z, 0.0, 1.0);
    int shadowLights = min(int(S.tuning.w), SHADOW_MAXCASTERS);
    vec3 accum = vec3(0.0);
    int n = int(S.screen.w);
    for (int i = 0; i < n; ++i) {
        vec3 Lv = S.lights[i].posRadius.xyz - world;
        float invR2 = S.lights[i].posRadius.w; // 1/radius^2
        float d2 = dot(Lv, Lv);
        float x = d2 * invR2;
        if (x >= 1.0) continue;           // outside the radius — no sqrt needed
        float att = 1.0 - x;
        att *= att;                       // smooth inverse-square-ish falloff
        vec3 Ld = Lv * inversesqrt(max(d2, 1e-8)); // rsqrt: cheaper than sqrt+divide
        float ndl = dot(N, Ld);
        float lambert = (mat > 0.25 && mat < 0.75)
            ? (0.5 + 0.5 * ndl)          // sprite: wrap lighting
            : max(ndl, 0.0);
        float shadow = 1.0;
        if (shadowStrength > 0.0 && i < shadowLights) {
            shadow = mix(1.0, ssShadow(world, S.lights[i].posRadius.xyz), shadowStrength);
        }
        accum += S.lights[i].colorIntensity.rgb * S.lights[i].colorIntensity.a
               * att * (0.25 + 0.75 * lambert) * shadow;
    }

    // --- flashlight spot: same falloff/lambert shape as a point light,
    // times an angular term that fades from the hotspot (spotCosInner) to
    // the beam edge (spotDirCosOuter.w), zero outside it.
    if (S.spotColorIntensity.a > 0.0) {
        vec3 Lv = S.spotPosRadius.xyz - world;
        float d2 = dot(Lv, Lv);
        float x = d2 * S.spotPosRadius.w;
        if (x < 1.0) {
            float att = 1.0 - x;
            att *= att;
            vec3 Ld = Lv * inversesqrt(max(d2, 1e-8));
            float ndl = dot(N, Ld);
            float lambert = (mat > 0.25 && mat < 0.75) ? (0.5 + 0.5 * ndl) : max(ndl, 0.0);
            float cosAngle = dot(S.spotDirCosOuter.xyz, normalize(world - S.spotPosRadius.xyz));
            float cone = smoothstep(S.spotDirCosOuter.w, S.spotCosInner.x, cosAngle);
            if (cone > 0.0) {
                float shadow = shadowStrength > 0.0
                    ? mix(1.0, ssShadow(world, S.spotPosRadius.xyz), shadowStrength)
                    : 1.0;
                accum += S.spotColorIntensity.rgb * S.spotColorIntensity.a
                       * att * (0.25 + 0.75 * lambert) * cone * shadow;
            }
        }
    }

    // Water is near-specular: a dynamic light barely washes it (the surface
    // look is the reflection/tint raster.blendWater already baked into the
    // albedo). Without this, firing a weapon over water blows it out.
    if (isWater) {
        accum *= 0.15;

        // Reflections of the real scene, on top of the sky reflection that
        // raster.blendWater already baked in. Reflect the view ray about the
        // water's up normal, ripple it by the wave slope.
        vec3 wv = waterWave(world.xy, S.view.w);
        vec3 Vv = normalize(world - S.camPos_extra.xyz);
        vec3 Rd = normalize(reflect(Vv, vec3(0.0, 0.0, 1.0)) + vec3(wv.xy * 0.14, 0.0));
        float fres = 0.04 + 0.5 * pow(1.0 - max(-Vv.z, 0.0), 4.0); // grazing -> more

        // (a) geometry: march the depth buffer for a nearby wall / monster.
        vec4 hit = ssr(world, Rd);
        albedo = mix(albedo, hit.rgb * 0.9, hit.a * clamp(fres, 0.0, 0.55));

        // (b) dynamic lights: torches / lava glow / muzzle flash streak
        //     across the surface as wobbling coloured highlights.
        vec3 lref = vec3(0.0);
        for (int i = 0; i < n; ++i) {
            vec3 toL = S.lights[i].posRadius.xyz - world;
            float dl2 = dot(toL, toL);
            float al = max(dot(Rd, toL * inversesqrt(max(dl2, 1e-6))), 0.0);
            lref += S.lights[i].colorIntensity.rgb * S.lights[i].colorIntensity.a
                  * pow(al, 48.0) / (1.0 + dl2 * 0.0006);
        }
        albedo += lref * 0.5;
    }

    // Brightmap: LightParam.g is the per-texel self-illumination mask the
    // raster stamped (drawWallSpan/drawFlatSpan). A masked texel is lit to
    // AT LEAST its mask value (so it never goes dark with the sector) plus a
    // small bloom kicker — not an unbounded add, which blew highlights out.
    float bright = texelFetch(uLightParam, ip, 0).g;
    float litAmt = max(baseFade, bright);

    // Ambient floor (config ambientLight, S.spotCosInner.y): a fully-shadowed
    // surface never reads darker than this fraction of its real texture
    // colour — the actual visibility fix. 0 reproduces the old behaviour.
    litAmt = max(litAmt, S.spotCosInner.y);

    // Cool ambient floor: a deep shadow settles toward a faint blue rather
    // than crushing to pure black — matches world.frag. Fades out as the
    // pixel lights up; a sprite (wrap-lit) gets a touch less.
    vec3 amb = vec3(0.020, 0.028, 0.045) * (1.0 - litAmt);
    if (mat > 0.25 && mat < 0.75) amb *= 0.5;

    outColor = vec4(applyFog(albedo * (litAmt + accum) + amb * albedo + albedo * bright * 0.20, d), 1.0);
}
