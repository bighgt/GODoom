#version 450

// Enhanced-mode final pass: combine the HDR lighting result with the
// blurred bloom, roll off only the highlights (values <= the knee are left
// exactly as vanilla would have them, so "enhanced with nothing happening"
// still looks like the vanilla frame), then blend the un-composited 2D
// overlay (HUD, weapon) on top so it stays crisp and untonemapped.
//
// fragUV is 0..1 across the source frame (blit.vert), so this pass also
// does the letterboxed upscale to the window, same as the vanilla blit.
// The swapchain image is sRGB, so we output linear-ish "palette space"
// values and let the hardware encode — exactly what blit.frag does.

layout(location = 0) in vec2 fragUV;
layout(location = 0) out vec4 outColor;

layout(binding = 0) uniform sampler2D uHDR;
layout(binding = 1) uniform sampler2D uBloom;
layout(binding = 2) uniform sampler2D uOverlay;

layout(push_constant) uniform PC { vec4 params; } pc; // x exposure, y bloomStrength

vec3 softKnee(vec3 c) {
    // Identity up to KNEE; smooth compression of anything above toward 1.
    const float KNEE = 0.8;
    vec3 over = max(c - vec3(KNEE), 0.0);
    return min(c, vec3(KNEE)) + (1.0 - KNEE) * (over / (over + (1.0 - KNEE)));
}

void main() {
    vec3 hdr = texture(uHDR, fragUV).rgb;
    vec3 bloom = texture(uBloom, fragUV).rgb;
    vec3 col = (hdr + bloom * pc.params.y) * pc.params.x;
    col = softKnee(col);

    vec4 ov = texture(uOverlay, fragUV);
    col = mix(col, ov.rgb, ov.a);

    outColor = vec4(col, 1.0);
}
