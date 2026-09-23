#version 450

// Hardware renderer — fragment stage. One draw per texture (the CPU groups
// the triangle list into worldgeo.Draws by texture), so this samples a
// single bound sampler2D rather than a bindless array — plain Vulkan 1.0.
// Applies the continuous sector-and-distance fade (see fade() below), then
// adds this frame's dynamic point lights on top.

layout(location = 0) in vec2 vUV;
layout(location = 1) in float vLight;   // sector light 0..1
layout(location = 2) in flat uint vKind;
layout(location = 3) in float vDepth;   // camera-forward distance, map units
layout(location = 4) in vec3 vWorldPos; // world position, map units
layout(location = 5) in vec3 vNormal;   // unit surface normal
layout(location = 6) in float vAlpha;   // KindSpriteTrans's real blend weight

layout(location = 0) out vec4 outColor;

// Per-draw: the current wall/flat/sky/sprite texture, and its brightmap
// mask (a 1x1 black dummy when the texture has none).
layout(set = 0, binding = 0) uniform sampler2D uTex;
layout(set = 0, binding = 1) uniform sampler2D uBright;

// Real cube shadow maps (config realShadows) for up to 2 leading dynamic
// lights — see render/vulkan/worldshadow.go's own doc comment. Always
// bound (a 1x1 dummy when realShadows is off or a slot is unused this
// frame — see worldCreateDescriptors); W.spotCosInner.z gates whether
// they're actually sampled below.
layout(set = 1, binding = 0) uniform samplerCube uShadowCube0;
layout(set = 1, binding = 1) uniform samplerCube uShadowCube1;

const int MAX_LIGHTS = 64; // keep == vulkan.worldMaxLights and render.MaxLights
const int SHADOW_MAX = 24; // keep == vulkan.shadowCasterMax and engine.hwGroundShadowCap

layout(set = 1, binding = 2) uniform World {
    vec4 cam;      // sinA, cosA, focal, camX
    vec4 screen;   // width, height, skyPixPerTurn, pitchShear
    vec4 light;    // lightScale, extraLight, scaleRef, ambientLight
    vec4 nLights;  // x = active dynamic light count, y = water clock, zw = camY,camZ
    vec4 fog;      // rgb fog colour, w = density (0 = off)
    vec4 amb;      // rgb = sky-averaged hemisphere ambient, w = strength (0 = flat fallback)
    // The player's flashlight: a single forward cone light, kept apart from
    // lightPos/lightColor (isotropic point lights) because a beam needs a
    // direction and cone angle neither array carries. spotColor.a <= 0
    // means off.
    vec4 spotPosInvR2;  // xyz world pos, w = 1/radius^2
    vec4 spotDirCosOuter; // xyz normalized direction, w = cos(outer half-angle)
    vec4 spotColor;      // rgb colour * intensity, a = intensity (>0 = on)
    // x = cos(inner half-angle); y = cartoon-shading band count (config
    // cartoonShading — fade() posterizes to this many steps when > 0, 0 =
    // off); z = how many of lightPos[0]/[1] have a real shadow map this
    // frame (0/1/2 — config realShadows), w = that shadow map's shared far
    // plane (world units).
    vec4 spotCosInner;
    vec4 lightPos[MAX_LIGHTS];      // xyz = world pos, w = 1 / radius^2
    vec4 lightColor[MAX_LIGHTS];    // rgb = colour * intensity
    vec4 shadowCaster[SHADOW_MAX];  // xyz = floor pos, w = radius (0 = list end)
} W;

#define CAM_POS vec3(W.cam.w, W.nLights.z, W.nLights.w)

const uint KIND_WALL         = 0u;
const uint KIND_MASKED       = 1u;
const uint KIND_FLAT         = 2u;
const uint KIND_SKY          = 3u;
const uint KIND_SPRITE       = 4u; // camera-facing billboard, sector/distance fade
const uint KIND_SPRITE_FULL  = 5u; // billboard, full bright (projectile / attack flash)
const uint KIND_LIQUID_WATER = 6u; // water floor: fresnel sky reflection
const uint KIND_LIQUID_LAVA  = 7u; // lava floor: warm emissive glow
const uint KIND_LIQUID_NUKAGE= 8u; // nukage/slime floor: green emissive tint
const uint KIND_SPRITE_TRANS = 9u; // billboard, real alpha blend (vAlpha) — additive-pipeline particles' translucent sibling
const uint KIND_TELEPORT     = 10u; // teleporter-pad floor: procedural blue/white shimmer, ignores uTex entirely

// AMBIENT: the fallback faint cool floor a fully-shadowed world pixel
// settles to instead of pure black — used only when the engine hasn't
// supplied a sky-derived hemisphere ambient (W.amb.w <= 0). Kept tiny so
// lit areas are untouched.
const vec3 AMBIENT = vec3(0.020, 0.028, 0.045);

// hemisphereAmbient: the faint fill a fully shadowed pixel settles to.
// Up-facing surfaces (floors) take the sky-tinted colour the engine
// averaged from THIS level's sky; down-facing (ceilings) get a darker,
// hue-kept version — they see the floor, not the sky; walls sit between.
// So a shadowed corner in a hell map goes murky-warm, one under an open
// sky goes cool blue, without any per-map tuning.
vec3 hemisphereAmbient() {
    if (W.amb.w <= 0.0) return AMBIENT;
    float up = clamp(vNormal.z * 0.5 + 0.5, 0.0, 1.0); // 1 floor / 0.5 wall / 0 ceiling
    return mix(W.amb.rgb * 0.30, W.amb.rgb, up) * W.amb.w;
}

// fade is the base sector-and-distance shading. The vanilla LUT approach
// (48 discrete distance columns, since removed) showed as an abrupt edge
// and stair-stepped dimming down a corridor. This is the *continuous*
// version the enhanced software path uses
// (light.frag's baseFade): sector light -> lightnum -> startmap, minus a
// smooth 1/distance term, so brightness ramps down without bands. isFlat is
// ignored (light.frag doesn't split flats from walls either).
float fade(bool isFlat) {
    float ll = clamp(vLight * 255.0 * W.light.x, 0.0, 255.0);   // sector light * lightScale
    float lnum = clamp(floor(ll / 16.0) + W.light.y, 0.0, 15.0); // + extralight
    float startmap = (15.0 - lnum) * 4.0;
    float k = clamp((W.light.z / max(vDepth, 1.0)) * 0.5, 0.0, 23.5); // W.light.z = 2560 ref
    float row = clamp(startmap - k, 0.0, 31.0);
    float f = (32.0 - row) / 32.0;
    // Cartoon shading (config cartoonShading, W.spotCosInner.y = band
    // count): collapse the smooth distance/sector gradient into flat steps
    // — the classic cel-shaded look — rather than a continuous ramp. Round
    // to the nearest step rather than floor/ceil so a band's midpoint reads
    // as that band's own brightness, not its darker or lighter neighbour's.
    float bands = W.spotCosInner.y;
    if (bands > 0.5) {
        f = floor(f * bands + 0.5) / bands;
    }
    return f;
}

// AMBIENT_GAMMA shapes sectorAmbientFrac's curve: <1 lifts the midtones (a
// half-lit room reads noticeably more than half as bright) while keeping the
// 0 and 1 endpoints fixed, so a truly dark sector still reads dark next to a
// merely-dim one instead of everything above near-black bunching together.
const float AMBIENT_GAMMA = 0.6;

// sectorAmbientFrac is the sector's OWN authored brightness (light level *
// lightScale, extralight excluded), as a 0..1 fraction — deliberately NOT
// run through fade()'s distance-diminish term. Real ambient fill in a room
// doesn't fall off with how far across it you're standing; only a specific
// light source's falloff should do that. Used to scale the ambient-light
// floor below so a well-lit room stays legible at any range while a dark
// room stays dark, instead of one flat floor erasing the difference.
float sectorAmbientFrac() {
    float frac = clamp(vLight * W.light.x, 0.0, 1.0);
    return pow(frac, AMBIENT_GAMMA);
}

// skyColorAt samples the sky texture by view direction for a given screen
// pixel (see game_design.txt section 10): horizontally skyPixPerTurn
// texture columns per 360 deg yaw; vertically the vanilla skytexturemid
// mapping —
// row 100 (of the 128-tall reference) at the pitch-shifted horizon, stepped
// at the vanilla 200-line rate scaled to the render height, wrapped (REPEAT
// sampler). Water passes a rippled frag coord so the reflection shimmers.
vec3 skyColorAt(vec2 fc) {
    float w = W.screen.x, h = W.screen.y, focal = W.cam.z;
    float pitchShear = W.screen.w;
    float colAngle = atan((fc.x - w * 0.5) / focal);
    float yaw = atan(W.cam.x, W.cam.y); // sinA, cosA -> angle
    ivec2 sz = textureSize(uTex, 0);

    float u = 0.5 + (colAngle - yaw) * W.screen.z / (2.0 * 3.14159265) / float(sz.x);

    float skyScale = float(sz.y) / 128.0;             // raster skyReferenceHeight
    float horizonY = h * 0.5 + pitchShear;
    float row = skyScale * (100.0 + (fc.y - horizonY) * (200.0 / h));
    float v = row / float(sz.y);

    return texture(uTex, vec2(u, v)).rgb;
}

vec3 skyColor() { return skyColorAt(gl_FragCoord.xy); }

// applyFog blends c toward W.fog.rgb. The base term is the vanilla
// exponential of camera-forward distance (W.fog.w = density; 0 disables,
// early-out so a no-fog config pays nothing). On top of that a HEIGHT term
// makes the fog a low-lying mist: densest at/below a plane a little under
// the eye, thinning as a surface (or the view ray's midpoint) rises above
// it — so it pools in pits and low rooms and clears as you climb. Not
// applied to the sky.
const float FOG_BASE       = -48.0;  // mist plane, world units relative to the eye
const float FOG_FALLOFF    = 220.0;  // rise over which it thins to ~1/e
const float FOG_HEIGHT_AMT = 0.7;    // 0 = pure distance fog, 1 = fully height-gated

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

// waterWave is a small multi-octave sum-of-sines height field over world XY
// at time t, shared (same constants) with raster.waterWave so both
// renderers ripple alike: three fast short ripples + one slow large swell.
// Returns .xy = a slope vector (distorts the reflection and the surface
// texel), .z = a 0..1 crest factor (the glint).
vec3 waterWave(vec2 wp, float t) {
    vec2 p = wp * 0.030;
    float a = sin(p.x * 1.00 + p.y * 0.60 + t * 1.6);
    float b = sin(p.x * -0.70 + p.y * 1.30 + t * 2.1);
    float c = sin(p.x * 1.70 + p.y * -0.40 + t * 1.1);
    vec2 q = wp * 0.008;
    float e = sin(q.x * 1.00 + q.y * 0.70 + t * 0.5);      // slow swell
    float hgt = (a + 0.6 * b + 0.4 * c) / 2.0 + 0.35 * e;
    vec2 grad = vec2(a - c + 0.4 * e, b - 0.5 * c + 0.3 * e);
    float crest = clamp(hgt * 0.4 + 0.5, 0.0, 1.0);
    return vec3(grad, crest);
}

// teleportHash turns a 2D coordinate into a pseudo-random 0..1 value — used
// by the KIND_TELEPORT branch below so each patch of a teleporter pad
// twinkles on its own independent, uncorrelated phase instead of sharing
// one continuous phase gradient across the surface (a shared gradient is
// exactly what a sine of a raw linear x/y combination produces, and reads
// as a moving line/band, not a scattered twinkle). Not shared with
// worldblit.frag's own similar hash12 — this codebase has no GLSL
// #include, so every fragment shader here already duplicates small
// helpers like this rather than sharing them across files.
float teleportHash(vec2 p) {
    p = fract(p * vec2(127.1, 311.7));
    p += dot(p, p + 34.23);
    return fract(p.x * p.y);
}

// lavaWave is waterWave's molten-rock counterpart: one bespoke function per
// liquid kind (not a shared parameterized helper) so each stays tunable on
// its own terms, same as waterWave itself. Slow, large-wavelength, low-
// frequency swell — thick and viscous, not choppy. .xy distorts the crust
// texture sample so it seems to slowly churn; .z is a 0..1 "crest" factor
// used to make bright cracks glow hotter than the rest of the pool.
vec3 lavaWave(vec2 wp, float t) {
    vec2 p = wp * 0.012;
    float a = sin(p.x * 1.0 + p.y * 0.5 + t * 0.35);
    float b = sin(p.x * -0.6 + p.y * 1.1 + t * 0.22);
    float hgt = (a + 0.6 * b) * 0.5;
    vec2 grad = vec2(a - b * 0.4, b - a * 0.3) * 0.5;
    float crest = clamp(hgt * 0.5 + 0.5, 0.0, 1.0);
    return vec3(grad, crest);
}

// nukageWave is waterWave's thin-toxic-pool counterpart: quicker and
// tighter-wavelength than lavaWave, lower-amplitude than waterWave (a
// shallow pool, not open water). .z is a sparse bubbling "fizz" glint
// factor rather than a broad crest.
vec3 nukageWave(vec2 wp, float t) {
    vec2 p = wp * 0.045;
    float a = sin(p.x * 1.3 + p.y * 0.8 + t * 2.6);
    float b = sin(p.x * -0.9 + p.y * 1.4 + t * 3.4);
    vec2 grad = vec2(a - b * 0.5, b - a * 0.4) * 0.5;
    float fizz = clamp(a * 0.5 + b * 0.3 + 0.5, 0.0, 1.0);
    return vec3(grad, fizz);
}

// dynamicDiffuse sums this frame's point lights at vWorldPos: a smooth
// (1 - (d/R)^2)^2 distance falloff times a Lambert N.L term, so a surface
// facing a light is bright and one facing away stays at its sector level —
// the shape a flat isotropic glow was missing. Colour comes through, so a
// red torch tints the wall red. Added to (not multiplied with) the sector
// fade, so a close light punches past full brightness.
// DYN_GAIN scales the dynamic-light term: Doom light radii are small (~190u
// for a torch) and (1-(d/R)^2)^2 falls off hard, so at room distances the
// raw contribution is a few percent — a gain is needed for a torch to pool
// visible colour. Past the gain the summed term is rolled off by its
// brightest channel (a plain scalar divide, so hue is preserved) so a
// point-blank light saturates to a strong *coloured* value instead of
// blowing to flat white.
const float DYN_GAIN = 2.2;

// Fake surface relief for the dynamic-light term. Doom has no normal maps,
// so treat the diffuse texture's own luminance as a tiny heightfield: a
// torch or muzzle flash then rakes across brick / panel / grille detail
// instead of washing a flat plane. Only the dynamic lights see it — the
// baked sector fade stays flat, it's painted-in art. SPEC_* add a moving
// Blinn-Phong glint off the same (bumped) normal, scaled per surface so
// bright/metal texels read shinier than matte ones.
const float BUMP_SCALE    = 1.5;
const float SPEC_POWER     = 40.0;
const float SPEC_STRENGTH  = 0.6;

// cotangentFrame — Christian Schuler's screen-derivative TBN, so no
// per-vertex tangents are needed (worldgeo.Vert carries none).
mat3 cotangentFrame(vec3 N, vec3 p, vec2 uv) {
    vec3 dp1 = dFdx(p);
    vec3 dp2 = dFdy(p);
    vec2 duv1 = dFdx(uv);
    vec2 duv2 = dFdy(uv);
    vec3 dp2perp = cross(dp2, N);
    vec3 dp1perp = cross(N, dp1);
    vec3 T = dp2perp * duv1.x + dp1perp * duv2.x;
    vec3 B = dp2perp * duv1.y + dp1perp * duv2.y;
    float invmax = inversesqrt(max(max(dot(T, T), dot(B, B)), 1e-8));
    return mat3(T * invmax, B * invmax, N);
}

vec3 bumpedNormal(vec3 N) {
    vec2 ts = 1.0 / vec2(textureSize(uTex, 0));
    vec3 L = vec3(0.299, 0.587, 0.114);
    float hL = dot(texture(uTex, vUV - vec2(ts.x, 0.0)).rgb, L);
    float hR = dot(texture(uTex, vUV + vec2(ts.x, 0.0)).rgb, L);
    float hD = dot(texture(uTex, vUV - vec2(0.0, ts.y)).rgb, L);
    float hU = dot(texture(uTex, vUV + vec2(0.0, ts.y)).rgb, L);
    vec3 tsn = normalize(vec3((hL - hR) * BUMP_SCALE, (hD - hU) * BUMP_SCALE, 1.0));
    return normalize(cotangentFrame(N, vWorldPos, vUV) * tsn);
}

// anyLightNear is a cheap (no texture fetch, no normalize) pre-check for
// whether ANY of this frame's dynamic point lights actually reach
// vWorldPos, using the exact same 1-(d/R)^2 test dynamicDiffuse's own loop
// uses to skip an out-of-range light. bumpedNormal() above costs 4 extra
// texture samples; gating it on this means a wall/flat far from every
// light (the common case through most of a room lit by one distant torch)
// pays for those samples only when a light is actually close enough to
// visibly matter, instead of on every fragment merely because at least one
// light exists somewhere in view — W.nLights.x counts what
// cullLightsToView left in the camera's view cone, not what's near THIS
// fragment.
bool anyLightNear(vec3 P) {
    int n = min(int(W.nLights.x), MAX_LIGHTS);
    for (int i = 0; i < n; ++i) {
        vec3 d = W.lightPos[i].xyz - P;
        if (dot(d, d) * W.lightPos[i].w < 1.0) return true;
    }
    return false;
}

// groundShadow darkens a floor pixel under a grounded thing — a soft round
// blob, faded by horizontal distance and by how far the pixel's floor sits
// from the caster's own (so it doesn't bleed onto a step above/below).
// Multiplied into the floor colour only.
float groundShadow() {
    float s = 1.0;
    for (int i = 0; i < SHADOW_MAX; ++i) {
        float r = W.shadowCaster[i].w;
        if (r <= 0.0) break;
        vec3 c = W.shadowCaster[i].xyz;
        float dz = abs(vWorldPos.z - c.z);
        if (dz > 40.0) continue;
        float d = length(vWorldPos.xy - c.xy) / r;
        if (d >= 1.0) continue;
        float blob = 1.0 - d * d;
        blob *= blob * (1.0 - dz / 40.0);
        s *= 1.0 - 0.5 * blob;
    }
    return s;
}

// shadowCubeSample compares the real linear distance from lightPos to P
// against the closest surface the shadow map saw in that direction
// (written by shadow.frag as distance/farPlane — see its own doc comment),
// returning a dimmed factor when something closer occludes P. bias is a
// flat-plus-proportional slop (not a rasteriser depth bias — the shadow
// pass overwrites gl_FragDepth itself, so a fixed-function bias would be
// silently discarded) against acne from the map's own finite resolution;
// 0.35 rather than 0.0 so a real shadow reads as a dimming, not a hole —
// matching dynamicDiffuse's own 0.25 ambient-floor wraparound feel.
float shadowCubeSample(samplerCube sc, vec3 P, vec3 lightPos, float farPlane) {
    vec3 toFrag = P - lightPos;
    float dist = length(toFrag);
    float closest = texture(sc, toFrag).r * farPlane;
    float bias = max(0.05 * dist, 4.0);
    return (dist - bias > closest) ? 0.35 : 1.0;
}

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
        float atten = 1.0 - d2 * W.lightPos[i].w; // 1 - (dist/R)^2
        if (atten <= 0.0) continue;
        atten *= atten;
        vec3 Ld = d * inversesqrt(max(d2, 1e-8));
        // 0.25 ambient floor so a light wraps around and its colour tints
        // faces turned partly away — keep == light.frag's point-light term,
        // so the hardware and enhanced-software tiers read alike. A sprite
        // billboard additionally gets light.frag's WRAP lambert (never goes
        // fully dark-sided): its camera-facing normal is not a real
        // geometric normal, so the harsh max(N.L,0) an ordinary wall gets
        // made monsters read as wrongly dark-sided by a nearby torch. Uses
        // the UNCLAMPED dot so a face-away sample (-1) still reads darker
        // than a grazing one (0) — wrapping the already-clamped value would
        // flatten that distinction away.
        float ndlRaw = dot(N, Ld);
        float ndotl = max(ndlRaw, 0.0); // still gates the spec term below
        float lambert = (vKind == KIND_SPRITE) ? (0.5 + 0.5 * ndlRaw) : ndotl;
        float spec = 0.0;
        if (ndotl > 0.0 && specMask > 0.0) {
            float nh = max(dot(N, normalize(Ld + V)), 0.0);
            spec = pow(nh, SPEC_POWER) * SPEC_STRENGTH * specMask;
        }
        float sh = 1.0;
        if (i == 0 && W.spotCosInner.z >= 1.0) sh = shadowCubeSample(uShadowCube0, vWorldPos, W.lightPos[0].xyz, W.spotCosInner.w);
        else if (i == 1 && W.spotCosInner.z >= 2.0) sh = shadowCubeSample(uShadowCube1, vWorldPos, W.lightPos[1].xyz, W.spotCosInner.w);
        sum += W.lightColor[i].rgb * (atten * (0.25 + 0.75 * lambert + spec) * sh);
    }

    // --- flashlight spot: same falloff/lambert shape as a point light,
    // times an angular term fading from the hotspot to the beam edge.
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
    return g / (1.0 + 0.30 * max(m - 0.9, 0.0)); // gentle knee only above ~0.9
}

void main() {
    if (vKind == KIND_SKY) {
        // Alpha 0.5 tags sky for the present pass (water = 0, everything
        // else = 1): worldblit.frag skips SSAO on it and lets god-ray
        // scattering gather from it.
        outColor = vec4(skyColor(), 0.5);
        return;
    }

    if (vKind == KIND_TELEPORT) {
        // Teleporter pad: a fully procedural blue/white cut-diamond
        // surface — no sampling of uTex (the real GATE1-4 texture) at all,
        // so the old art is completely replaced rather than tinted/warped
        // like the liquid floors are. Three earlier cuts of this each
        // fixed a "blocky" bug (a sine of a raw linear world-X/Y
        // combination reads as a moving line; a per-64-unit-tile radial
        // glow resets hard at every tile seam) before landing on the
        // jittered-point (Voronoi) technique both the facets and the
        // sparkle below now share — it has no grid/tile edge at any scale.
        //
        // Facets: the pad is partitioned into irregular cells (a jittered
        // point per cell, nearest-point-wins — real Voronoi, not a plain
        // grid), each standing in for one flat face of a cut gem with its
        // own randomly tilted fake normal. A facet's own highlight is only
        // bright when the view ray happens to line up with THAT facet's
        // own tilt, so as the camera moves different facets flash in turn
        // instead of one uniform sheen — a two-tier pow(dot(N,V)) (broad
        // soft flash plus a much tighter hard glint riding the same
        // normal) so a lit facet shows both a sheen and a pinprick sparkle
        // at once, a thin bright cut-line where neighbouring facets meet
        // (from the Voronoi cell's own edge distance), a per-facet flat
        // brightness variance so it reads as faceted even between flashes,
        // and a per-facet "fire" tint drifting across a narrow icy-blue /
        // violet / cyan spectrum so neighbouring flashes don't all read as
        // the exact same colour — real cut gems disperse light into
        // exactly this kind of shifting colour, not a flat white glint.
        // Sparkle then layers finer, faster-twinkling points on top, and a
        // Fresnel rim brightens the whole surface at grazing view angles
        // (the real floor normal, not a facet's fake one) the same way the
        // mirror reflection below does. That reflection (alpha 0, same tag
        // water uses) still runs on top of all of this, plus the real
        // rising glitter particles (engine/teleportambience.go) for the
        // upward motion a flat floor plane can't itself show.
        float t = W.nLights.y;
        vec3 V = normalize(CAM_POS - vWorldPos);

        float shimmer = mix(0.9, 1.1, 0.5 + 0.5 * sin(t * 1.1));

        // Nearest-point Voronoi over a 3x3 neighbourhood — bestCellID
        // identifies which facet this pixel belongs to; bestDist/secondDist
        // (nearest/second-nearest point distance) locate the cell's own
        // edge, for the thin bright cut-lines below.
        vec2 fUV = vWorldPos.xy / 7.0;
        vec2 fBase = floor(fUV);
        vec2 fLocal = fract(fUV);
        float bestDist = 8.0, secondDist = 8.0;
        vec2 bestCellID = fBase;
        for (int oy = -1; oy <= 1; oy++) {
            for (int ox = -1; ox <= 1; ox++) {
                vec2 cellID = fBase + vec2(float(ox), float(oy));
                vec2 pointPos = vec2(float(ox), float(oy)) + vec2(teleportHash(cellID), teleportHash(cellID + 91.0));
                float dd = distance(fLocal, pointPos);
                if (dd < bestDist) {
                    secondDist = bestDist;
                    bestDist = dd;
                    bestCellID = cellID;
                } else if (dd < secondDist) {
                    secondDist = dd;
                }
            }
        }
        vec2 tilt = (vec2(teleportHash(bestCellID + 3.0), teleportHash(bestCellID + 7.0)) - 0.5) * 1.6;
        vec3 facetN = normalize(vec3(tilt, 1.0));
        float ndv = max(dot(facetN, V), 0.0);
        // Two-tier highlight: a broad soft flash plus a much tighter hard
        // glint riding the same facet normal — real cut gems show both a
        // wide sheen and pinprick-sharp sparkle points at once, not just
        // one size of highlight.
        float facetSpec = pow(ndv, 40.0) * 0.6 + pow(ndv, 220.0) * 1.6;
        float facetShade = 0.8 + 0.35 * teleportHash(bestCellID + 13.0);
        // Cut-line: a thin bright seam where facets meet (secondDist and
        // bestDist converge), like the visible edges between a diamond's
        // faces.
        float facetEdge = 1.0 - smoothstep(0.0, 0.10, secondDist - bestDist);
        // Fire: each facet's highlight leans toward a different point on a
        // narrow icy-blue -> violet -> cyan spectrum (never far off the
        // established blue/white theme) — a diamond's dispersion "fire",
        // where neighbouring facets flash different tints instead of one
        // flat colour.
        float hue = teleportHash(bestCellID + 23.0);
        vec3 fireA = vec3(0.60, 0.85, 1.00);
        vec3 fireB = vec3(0.75, 0.60, 1.00);
        vec3 fireC = vec3(0.55, 1.00, 0.95);
        vec3 facetColor = mix(mix(fireA, fireB, clamp(hue * 2.0, 0.0, 1.0)), fireC, clamp(hue * 2.0 - 1.0, 0.0, 1.0));
        // Rim: the real (not fake-tilted) floor normal brightens at
        // grazing view angles, same Fresnel shape the mirror reflection
        // below already uses — a gem-like edge glow independent of any one
        // facet's own flash.
        float rim = pow(1.0 - clamp(dot(vNormal, V), 0.0, 1.0), 5.0);

        // Fine sparkle: a second, smaller-scale jittered-point field,
        // twinkling faster than any one facet flash lasts.
        vec2 gv = vWorldPos.xy / 3.0;
        vec2 gid = floor(gv);
        vec2 gf = fract(gv) - 0.5;
        vec2 jitter = vec2(teleportHash(gid + 41.0), teleportHash(gid + 59.0)) - 0.5;
        float d = length(gf - jitter * 0.7);
        float pt = pow(clamp(1.0 - d * 2.2, 0.0, 1.0), 3.0);
        float ph = teleportHash(gid + 3.0) * 6.2831853;
        float spd = 1.5 + 3.0 * teleportHash(gid + 9.0);
        float sparkle = pt * max(sin(t * spd + ph), 0.0);

        vec3 deep = vec3(0.04, 0.09, 0.22); // dark blue mirror base
        vec3 bright = vec3(0.75, 0.9, 1.0); // sparkle / edge / rim colour
        vec3 col = deep * shimmer * facetShade
                 + facetColor * facetSpec * 2.2
                 + bright * facetEdge * 0.5
                 + bright * sparkle * 1.2
                 + bright * rim * 0.4;

        float lit = max(fade(true), 0.5); // always reads as its own light source, even in a dark room
        // Alpha 0 tags this pixel the same way water does, so
        // worldblit.frag's existing screen-space reflection march runs on
        // it unchanged — the pad is a flat, Z-up plane exactly like a
        // water floor is, so that same reflection math (Fresnel-gated:
        // strong at grazing angles, faint looking straight down) turns
        // this into a genuine mirror of the room around it.
        outColor = vec4(applyFog(col * lit), 0.0);
        return;
    }

    vec4 tex = texture(uTex, vUV);

    // Liquid floors. Same intent as the software rasterizer's drawFlatSpan /
    // blendWater / shadeLiquid so both renderers read alike.
    if (vKind == KIND_LIQUID_WATER) {
        float t = W.nLights.y;                              // animated-water clock (s)
        vec3 wv = waterWave(vWorldPos.xy, t);

        float h = W.screen.y;
        float horizonY = h * 0.5 + W.screen.w;              // == skyColor()'s
        float graz = clamp(1.0 - (gl_FragCoord.y - horizonY) / max(h - horizonY, 1.0), 0.0, 1.0);

        // Depth cue: view angle PLUS view distance — a wide lake reads
        // deeper toward its far side, a puddle underfoot stays shallow.
        float distFrac = clamp(vDepth / 620.0, 0.0, 1.0);
        float depth = 0.14 + 0.55 * graz + 0.34 * distFrac;

        // The FWATER texel is "the bottom seen THROUGH the water" (translucent
        // dominant term); the sample is refracted by the wave slope so the
        // bottom shimmers. Light through water is absorbed — red first, blue
        // least — so it fades to dark blue-green with depth; crests focus a
        // little light back (caustics).
        vec3 body = texture(uTex, vUV + wv.xy * (0.010 + 0.02 * depth)).rgb * fade(true);
        float caustic = 0.86 + 0.5 * (wv.z - 0.5);
        vec3 absorb = vec3(exp(-2.3 * depth), exp(-1.15 * depth), exp(-0.5 * depth));
        body = body * absorb * caustic + vec3(0.02, 0.06, 0.09) * depth;

        // Reflection: a rippled hint of sky, sharp fresnel — pushed
        // stronger at grazing angles than a physically-minimal fresnel
        // curve would (real water gets close to a full mirror near the
        // horizon; the old 0.18 cap read as barely more than a tint) and
        // desaturated less, so the reflection reads as sky-colored rather
        // than washed toward gray.
        float fres = 0.03 + 0.5 * pow(graz, 4.0);
        vec3 refl = skyColorAt(gl_FragCoord.xy + wv.xy * (3.0 + 6.0 * graz));
        refl = mix(refl, vec3(dot(refl, vec3(0.30, 0.59, 0.11))), 0.12);

        vec3 outc = mix(body, refl, fres);
        // Dynamic light as a damped crest glint, not a wash. Flat normal +
        // no spec here — water has its own reflection / sun-glint model.
        outc += dynamicDiffuse(vec3(0.0, 0.0, 1.0), 0.0) * (0.08 + 0.4 * pow(wv.z, 4.0) * (0.3 + graz));

        // Reflected lights: the scene's dynamic emitters (torches, lava glow,
        // muzzle flash) reflect off the surface as wobbling coloured
        // streaks — a cheap stand-in for true SSR that catches the most
        // important reflectors. Reflect the view ray about the water's up
        // normal, ripple it by the wave slope, and score each light by how
        // closely the reflected ray points at it.
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

        // Sun glint: one tight bright spot near the horizon whose column
        // drifts slowly (time + camera yaw), shattered into glitter by the
        // wave crests — a coherent highlight, not a uniform sheen.
        float yaw = atan(W.cam.x, W.cam.y);
        float sunX = W.screen.x * 0.5 + sin(t * 0.06 - yaw * 1.5) * W.screen.x * 0.32;
        vec2 dd = vec2((gl_FragCoord.x - sunX) / (W.screen.x * 0.05),
                       (gl_FragCoord.y - horizonY - 6.0) / 7.0);
        float sun = exp(-dot(dd, dd)) * pow(wv.z, 5.0) * (0.25 + 0.75 * graz);
        outc += vec3(1.0, 0.97, 0.9) * sun * 0.9;
        // Alpha 0 tags this pixel as water for the present pass — worldblit.frag
        // marches the depth buffer from here for a true screen-space
        // reflection. Every other surface writes alpha 1.
        outColor = vec4(applyFog(outc), 0.0);
        return;
    }
    // Lava and nukage/acid share one shape — wave-churned UV sample, a
    // bright-crest glint, a tinted emissive glow, a dynamic-light nudge —
    // with different wave functions (lavaWave/nukageWave, kept separate:
    // lava's is slow/viscous, nukage's is quick/thin) and constants
    // (isLava below) rather than two near-duplicate branches. All
    // fragments of one liquid surface take the same isLava path, so this
    // costs nothing extra over the two-branch version it replaces.
    if (vKind == KIND_LIQUID_LAVA || vKind == KIND_LIQUID_NUKAGE) {
        bool isLava = (vKind == KIND_LIQUID_LAVA);
        float t = W.nLights.y;
        vec3 wv = isLava ? lavaWave(vWorldPos.xy, t) : nukageWave(vWorldPos.xy, t);
        vec4 texW = texture(uTex, vUV + wv.xy * (isLava ? 0.010 : 0.014));
        float lit = max(fade(true), isLava ? 0.55 : 0.35); // lava never fully dark; nukage a little dimmer
        vec3 tint = isLava ? vec3(1.15, 0.50, 0.18) : vec3(0.10, 0.42, 0.14);
        float glint = pow(wv.z, isLava ? 3.0 : 4.0); // lava: sharp cracks; nukage: sparse bubbling fizz
        float glowShape = isLava
            ? 0.9 * (0.92 + 0.13 * sin(vWorldPos.x * 0.018 + vWorldPos.y * 0.021 + t * 2.3)) + 0.6 * glint
            : 1.0 + 0.8 * glint;
        vec3 glow = texW.rgb * tint * glowShape;
        // Lava is already self-lit; a dynamic light (muzzle flash) only
        // nudges it, so firing over lava keeps the molten look. Nukage gets
        // the ordinary full weight.
        float dynWeight = isLava ? 0.35 : 1.0;
        outColor = vec4(applyFog(texW.rgb * lit + glow + texW.rgb * dynamicDiffuse(normalize(vNormal), 0.0) * dynWeight), 1.0);
        return;
    }

    if (vKind == KIND_SPRITE_FULL) {
        if (tex.a < 0.5) discard;
        outColor = vec4(applyFog(tex.rgb * 1.35), 1.0); // emissive: pushed >1 so bloom catches it
        return;
    }
    if (vKind == KIND_SPRITE_TRANS) {
        // Real alpha blend (pipeParticleTrans: SrcAlpha/OneMinusSrcAlpha),
        // unlike every other sprite kind's hard tex.a<0.5 cutout — shaded by
        // vLight like an ordinary surface (not pushed emissive/bloomed like
        // KIND_SPRITE_FULL), so it reads as haze lit by its surroundings
        // rather than another light source.
        float a = tex.a * vAlpha;
        if (a < 0.01) discard;
        outColor = vec4(applyFog(tex.rgb * vLight), a);
        return;
    }
    if (vKind == KIND_MASKED) {
        if (vUV.y < 0.0 || vUV.y > 1.0 || tex.a < 0.5) discard;
    } else if (vKind == KIND_SPRITE) {
        if (tex.a < 0.5) discard;
    }

    // HDR out — dynamic lights add on top of the sector fade and may push
    // past 1.0; worldblit.frag bright-passes, blurs and tonemaps it.
    float base = fade(vKind == KIND_FLAT);
    // Brightmap: a masked texel of a lit panel / light / EXIT sign is lit to
    // AT LEAST its mask value (never dark with the sector) plus a small
    // bloom kicker — bounded, so highlights don't blow out. Matches
    // light.frag's model.
    float bmask = dot(texture(uBright, vUV).rgb, vec3(0.299, 0.587, 0.114));
    float litAmt = max(base, bmask);
    // Ambient floor (config ambientLight, W.light.w), scaled by how bright
    // THIS sector actually is (sectorAmbientFrac, undiminished by camera
    // distance): a monster standing across a well-lit room still reads
    // detailed instead of fading to near-black by mid-room, while one in a
    // genuinely dark sector gets little to no lift — "less ambient reaches
    // them" — and only a real light source (torch, muzzle flash, the
    // flashlight) picks it out. 0 reproduces the old crush-to-black
    // behaviour regardless of sector brightness.
    litAmt = max(litAmt, W.light.w * sectorAmbientFrac());
    // Hemisphere tint: a deep shadow settles toward a faint sky-tinted fill
    // instead of crushing to pure black, so unlit corners read as "in
    // shadow" with some environment bounce rather than as void. Fades out
    // as the pixel lights up.
    vec3 amb = hemisphereAmbient() * (1.0 - litAmt);

    // Perturb the normal the dynamic lights see by the texture's luminance
    // heightfield (walls / flats only — a sprite silhouette isn't a
    // heightfield, and KIND_MASKED's alpha discard makes screen derivatives
    // unreliable). specMask scales the glint: bright / metal texels shinier.
    vec3 N = normalize(vNormal);
    float specMask = 0.15;
    if (W.nLights.x > 0.5 && (vKind == KIND_WALL || vKind == KIND_FLAT) && anyLightNear(vWorldPos)) {
        N = bumpedNormal(N);
        specMask = 0.20 + 0.80 * dot(tex.rgb, vec3(0.299, 0.587, 0.114));
    }
    vec3 lit3 = tex.rgb * (vec3(litAmt) + amb) + tex.rgb * dynamicDiffuse(N, specMask) + tex.rgb * bmask * 0.20;
    // Contact-shadow blobs, floors only (up-facing flat).
    if (vKind == KIND_FLAT && vNormal.z > 0.5) lit3 *= groundShadow();
    outColor = vec4(applyFog(lit3), 1.0);
}
