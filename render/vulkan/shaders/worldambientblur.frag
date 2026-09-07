#version 450

// Hardware renderer — AO denoise pass. worldambient.frag's SSAO term (its
// green channel) is a 12-tap disc estimate at quarter render resolution:
// too few samples to be smooth on its own, so each pixel's sample disc is
// rotated by a per-pixel dither so the shortfall reads as noise rather than
// banding. A pure hash rotation showed as scattered "salt and pepper" grey
// dots (worst in corners, where AO darkens the most and any noise in it
// reads strongest); switching the rotation to interleaved gradient noise
// (see worldambient.frag) traded that for a visible, regular grid pattern —
// IGN is spatially *structured*, not blurred, so on its own it just changes
// what the undersampling looks like, not whether it's visible. Neither is a
// substitute for what every production SSAO implementation actually uses to
// clean this up: a real blur pass on the raw AO term. This is that pass —
// a small 4-tap box blur (same shape as a single Kawase iteration), reading
// worldambient.frag's output image and writing the buffer worldblit.frag
// actually samples.
//
// Only the AO channel (g) is blurred; shadow (r) and god-ray (b) pass
// through the centre texel untouched — both are already soft/low-frequency
// by construction (a shadow penumbra, a light-shaft glow), so blurring them
// too would only soften a shadow's edge more than intended for no benefit.
//
// A full 3x3 box (9 taps) rather than just the 4 diagonal corners: the
// corner-only pattern is itself a sparse, axis-blind sample of the noise
// it's trying to smooth, and a fixed-radius blur with too few taps can
// alias against a structured noise source (interleavedGradientNoise, in
// worldambient.frag) almost as easily as too few AO samples alias in the
// first place — the axis-aligned taps here catch what the corner-only
// version could systematically miss. Still cheap: quarter render
// resolution, 9 single-channel-relevant texture fetches.

layout(location = 0) in vec2 fragUV;
layout(location = 0) out vec4 outColor;

layout(binding = 0) uniform sampler2D uAmbient;

// params.xy = 1 / (quarter-res target size); params.z = tap spacing in texels.
layout(push_constant) uniform PC { vec4 params; } pc;

void main() {
    vec2 uv = fragUV;
    vec2 step = pc.params.xy * pc.params.z;

    vec4 centre = texture(uAmbient, uv);
    float ao = 0.0;
    for (int y = -1; y <= 1; ++y) {
        for (int x = -1; x <= 1; ++x) {
            ao += texture(uAmbient, uv + vec2(float(x), float(y)) * step).g;
        }
    }
    ao *= 1.0 / 9.0;

    outColor = vec4(centre.r, ao, centre.b, 1.0);
}
