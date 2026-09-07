#version 450

// Isometric viewer — fragment stage. Deliberately much simpler than
// world.frag: no HDR/tonemap, no per-pixel dynamic lights, no distance fade
// (there is no "camera distance" in an orthographic projection the way
// there is in a perspective one), no view-dependent sky-column mapping (a
// top-down orthographic camera has no meaningful "sky column" the way a
// first-person one does — a KIND_SKY quad here is just sampled and shaded
// like any other opaque surface). One draw per texture, same as world.frag
// (uTex/uBright bound per draw, no bindless array).

layout(location = 0) in vec2 vUV;
layout(location = 1) in float vLight; // sector light 0..1
layout(location = 2) in flat uint vKind;
layout(location = 3) in vec3 vNormal; // unit surface normal

layout(location = 0) out vec4 outColor;

layout(set = 0, binding = 0) uniform sampler2D uTex;
layout(set = 0, binding = 1) uniform sampler2D uBright;

// Reveal UBO (set 1): the screen position + clip depth of every monster/
// item/barrel/other occluded thing this frame's front end wants a wall to
// fade for if one stands between it and the camera — see
// isoRevealUBO/isoCreateRevealResources (Go side) and occluderFadeAt below.
// The player doesn't need an entry here: the camera tracks the player every
// frame (see cmd/isoengine's main loop), so eye-space (0,0,0) — screen
// centre, clip Z 0.5 — IS the player always, which is what playerFade below
// already exploits for free.
layout(set = 1, binding = 0) uniform Reveal {
    vec4 count; // x = number of valid entries in points, rounded to int
    vec4 points[64]; // xy = screen pos (px), z = clip depth [0,1], w unused
} R;

layout(push_constant) uniform PC {
    mat4 viewProj;
    vec4 sun; // xyz = normalized "toward the sun" direction (world space), w = sun strength
    // x = ambient floor (0..1): what a pitch-black (vLight==0) sector still
    // reads as. y/z = the offscreen render target's own width/height in
    // pixels, w = player-occlusion fade radius in those same pixels — see
    // playerFade() below.
    vec4 amb;
} pc;

const uint KIND_WALL        = 0u;
const uint KIND_MASKED      = 1u;
const uint KIND_SPRITE      = 4u;
const uint KIND_SPRITE_FULL = 5u;

// bayer4x4 is the classic 4x4 ordered-dither matrix (values 0..15,
// normalised to 0..1) indexed by a fragment's screen position modulo 4.
// Used instead of per-pixel hash noise for playerFade's partial discard: a
// hash looks like scattered random specks ("a lot of dots"), since
// neighbouring pixels get uncorrelated thresholds; an ordered dither's
// regular, evenly-spaced pattern reads as a much smoother, more even fade
// at the same discard fraction — the standard fix for exactly this
// "dithered transparency looks noisy" complaint.
float bayer4x4(vec2 fragCoord) {
    const float m[16] = float[16](
        0.0,  8.0,  2.0, 10.0,
        12.0, 4.0, 14.0,  6.0,
        3.0, 11.0,  1.0,  9.0,
        15.0, 7.0, 13.0,  5.0
    );
    ivec2 p = ivec2(mod(fragCoord, 4.0));
    return m[p.y * 4 + p.x] / 16.0;
}

// playerFade discards a dithered fraction of a wall/masked fragment that
// sits both (a) within pc.amb.w pixels of screen centre and (b) nearer to
// the camera than the player, so a wall between the camera and the player
// doesn't hide them. Screen centre is always exactly where the player
// projects to: the camera tracks the player's position every frame (see
// cmd/isoengine's main loop), so eye-space (0,0,0) — dead centre, clip Z
// 0.5 under this orthographic projection's symmetric near/far — IS the
// player, with no separate "player screen position" to pass in. A dithered
// partial discard (rather than real alpha blending) needs no blend state
// or back-to-front sorting of this otherwise-opaque, depth-tested pass.
// occluderFadeAt is playerFade's same dithered partial-discard test, against
// an arbitrary screen position/depth instead of the fixed screen-centre/0.5
// the player always lands at. Shared by playerFade (screenPos = centre,
// depth = 0.5) and the R.points loop below (one monster/item/barrel/etc a
// frame) so a wall fades identically for either kind of occluded thing.
// Returns true if this fragment should be discarded.
bool occluderFadeAt(vec2 screenPos, float depth) {
    if (gl_FragCoord.z >= depth) return false; // not nearer to the camera than the thing
    float dist = length(gl_FragCoord.xy - screenPos);
    if (dist >= pc.amb.w) return false;
    // Capped well under half so the surviving wall pixels always outnumber
    // the discarded ones — a clearly-still-there, gently-faded wall rather
    // than a hole-punched one.
    float amt = mix(0.35, 0.0, dist / pc.amb.w);
    return bayer4x4(gl_FragCoord.xy) < amt;
}

// playerFade discards a dithered fraction of a wall/masked fragment that
// sits both (a) within pc.amb.w pixels of screen centre and (b) nearer to
// the camera than the player, so a wall between the camera and the player
// doesn't hide them. Screen centre is always exactly where the player
// projects to: the camera tracks the player's position every frame (see
// cmd/isoengine's main loop), so eye-space (0,0,0) — dead centre, clip Z
// 0.5 under this orthographic projection's symmetric near/far — IS the
// player, with no separate "player screen position" to pass in. A dithered
// partial discard (rather than real alpha blending) needs no blend state
// or back-to-front sorting of this otherwise-opaque, depth-tested pass.
//
// occluderFade extends the same treatment to everything else — monsters,
// items, barrels, other objects — via the small per-frame Reveal UBO (see
// above): each is projected to screen+depth CPU-side (worldgeo.
// IsoProjectToScreenDepth) once a frame, so a wall standing between the
// camera and any of them fades exactly like one standing between the
// camera and the player already does.
void occluderFade() {
    if (vKind != KIND_WALL && vKind != KIND_MASKED) return;
    vec2 center = pc.amb.yz * 0.5;
    if (occluderFadeAt(center, 0.5)) { discard; return; }
    int n = min(int(R.count.x), 64);
    for (int i = 0; i < n; i++) {
        if (occluderFadeAt(R.points[i].xy, R.points[i].z)) { discard; return; }
    }
}

void main() {
    vec4 tex = texture(uTex, vUV);

    if (vKind == KIND_MASKED) {
        if (vUV.y < 0.0 || vUV.y > 1.0 || tex.a < 0.5) discard;
    } else if (vKind == KIND_SPRITE || vKind == KIND_SPRITE_FULL) {
        if (tex.a < 0.5) discard;
    }
    occluderFade();

    if (vKind == KIND_SPRITE_FULL) {
        outColor = vec4(tex.rgb, 1.0);
        return;
    }

    // Brightmap: a masked texel (a lit panel / light / EXIT sign) reads at
    // least its mask value regardless of the directional term below —
    // matches world.frag's model, minus the bloom kicker (no HDR here).
    float bmask = dot(texture(uBright, vUV).rgb, vec3(0.299, 0.587, 0.114));

    // A pitch-black sector (vLight == 0) still reads as pc.amb.x, a fully
    // lit one (vLight == 1) as 1.0, linearly between — then a fixed
    // directional "sun" term on top purely for shape (so two walls at a
    // right angle in the same sector still read as different planes),
    // clamped high enough to allow a little overbright without the tonemap
    // this pipeline doesn't have.
    float base = max(mix(pc.amb.x, 1.0, clamp(vLight, 0.0, 1.0)), bmask);
    float lambert = max(dot(normalize(vNormal), pc.sun.xyz), 0.0);
    float lit = clamp(base + lambert * pc.sun.w, 0.0, 1.3);

    outColor = vec4(tex.rgb * lit, 1.0);
}
