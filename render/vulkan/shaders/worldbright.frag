#version 450

// Hardware bloom — bright-pass + downsample prefilter (scene -> half-res
// bloom mip 0). Unlike the shared bright.frag (one bilinear tap, which
// aliases a lone hot pixel into a crawling firefly), this takes four taps
// spanning the source footprint and combines them with a Karis weight
// (1 / (1 + luma)) so a single blown-out texel can't dominate the average.
// Then keeps only the part above the luminance threshold.
//
// params.xy = 1 / half-res target size (the destination texel, ~2 source
// texels); params.z = luminance threshold.

layout(location = 0) in vec2 fragUV;
layout(location = 0) out vec4 outColor;

layout(binding = 0) uniform sampler2D uHDR;

layout(push_constant) uniform PC { vec4 params; } pc;

float karis(vec3 c) { return 1.0 / (1.0 + dot(c, vec3(0.299, 0.587, 0.114))); }

void main() {
    vec2 uv = gl_FragCoord.xy * pc.params.xy;
    vec2 d = pc.params.xy * 0.5; // half a destination texel = ~one source texel

    vec3 a = texture(uHDR, uv + vec2(-d.x, -d.y)).rgb;
    vec3 b = texture(uHDR, uv + vec2( d.x, -d.y)).rgb;
    vec3 c = texture(uHDR, uv + vec2(-d.x,  d.y)).rgb;
    vec3 e = texture(uHDR, uv + vec2( d.x,  d.y)).rgb;

    float wa = karis(a), wb = karis(b), wc = karis(c), we = karis(e);
    vec3 col = (a * wa + b * wb + c * wc + e * we) / (wa + wb + wc + we);

    float l = dot(col, vec3(0.2126, 0.7152, 0.0722));
    float over = max(l - pc.params.z, 0.0);
    outColor = vec4(col * (over / max(l, 1e-4)), 1.0);
}
