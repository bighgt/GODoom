#version 450

// Hardware bloom — mip pyramid downsample (Call of Duty ACM 13-tap). Four
// inner taps at +/-1 source texel, four edge taps at +/-2, four corner taps
// at +/-2, centre; the weighting is a partial Karis average that suppresses
// fireflies without a per-tap luma weight. Run once per mip step, each
// target half the size of its source.
//
// params.xy = 1 / SOURCE size (the tap offsets).

layout(location = 0) in vec2 fragUV;
layout(location = 0) out vec4 outColor;

layout(binding = 0) uniform sampler2D uSrc;

layout(push_constant) uniform PC { vec4 params; } pc;

void main() {
    vec2 uv = fragUV;
    vec2 t = pc.params.xy;

    vec3 a = texture(uSrc, uv + t * vec2(-2.0, -2.0)).rgb;
    vec3 b = texture(uSrc, uv + t * vec2( 0.0, -2.0)).rgb;
    vec3 c = texture(uSrc, uv + t * vec2( 2.0, -2.0)).rgb;
    vec3 d = texture(uSrc, uv + t * vec2(-2.0,  0.0)).rgb;
    vec3 e = texture(uSrc, uv + t * vec2( 0.0,  0.0)).rgb;
    vec3 f = texture(uSrc, uv + t * vec2( 2.0,  0.0)).rgb;
    vec3 g = texture(uSrc, uv + t * vec2(-2.0,  2.0)).rgb;
    vec3 h = texture(uSrc, uv + t * vec2( 0.0,  2.0)).rgb;
    vec3 i = texture(uSrc, uv + t * vec2( 2.0,  2.0)).rgb;
    vec3 j = texture(uSrc, uv + t * vec2(-1.0, -1.0)).rgb;
    vec3 k = texture(uSrc, uv + t * vec2( 1.0, -1.0)).rgb;
    vec3 l = texture(uSrc, uv + t * vec2(-1.0,  1.0)).rgb;
    vec3 m = texture(uSrc, uv + t * vec2( 1.0,  1.0)).rgb;

    vec3 col = e * 0.125;
    col += (a + c + g + i) * 0.03125;
    col += (b + d + f + h) * 0.0625;
    col += (j + k + l + m) * 0.125;

    outColor = vec4(max(col, vec3(0.0)), 1.0);
}
