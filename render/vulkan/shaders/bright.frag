#version 450

// Bloom bright-pass: keep only the part of each HDR pixel above a
// luminance threshold, written to a half-resolution target that blur.frag
// then smears. params.xy = 1/targetSize, params.z = threshold.

layout(location = 0) in vec2 fragUV;
layout(location = 0) out vec4 outColor;

layout(binding = 0) uniform sampler2D uHDR;

layout(push_constant) uniform PC { vec4 params; } pc;

void main() {
    vec2 uv = gl_FragCoord.xy * pc.params.xy;
    vec3 c = texture(uHDR, uv).rgb;
    float l = dot(c, vec3(0.2126, 0.7152, 0.0722));
    float over = max(l - pc.params.z, 0.0);
    outColor = vec4(c * (over / max(l, 1e-4)), 1.0);
}
