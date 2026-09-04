#version 450

// Separable 9-tap Gaussian, run twice (horizontal then vertical) over the
// half-res bloom target. params.xy = 1/targetSize (for the UV),
// params.zw = per-tap step in UV space (texel size * direction).

layout(location = 0) in vec2 fragUV;
layout(location = 0) out vec4 outColor;

layout(binding = 0) uniform sampler2D uTex;

layout(push_constant) uniform PC { vec4 params; } pc;

const float W0 = 0.227027;
const float W1 = 0.194595;
const float W2 = 0.121622;
const float W3 = 0.054054;
const float W4 = 0.016216;

void main() {
    vec2 uv = gl_FragCoord.xy * pc.params.xy;
    vec2 step = pc.params.zw;
    vec3 acc = texture(uTex, uv).rgb * W0;
    acc += (texture(uTex, uv + step * 1.0).rgb + texture(uTex, uv - step * 1.0).rgb) * W1;
    acc += (texture(uTex, uv + step * 2.0).rgb + texture(uTex, uv - step * 2.0).rgb) * W2;
    acc += (texture(uTex, uv + step * 3.0).rgb + texture(uTex, uv - step * 3.0).rgb) * W3;
    acc += (texture(uTex, uv + step * 4.0).rgb + texture(uTex, uv - step * 4.0).rgb) * W4;
    outColor = vec4(acc, 1.0);
}
