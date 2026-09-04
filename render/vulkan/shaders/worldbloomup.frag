#version 450

// Hardware bloom — mip pyramid upsample (3x3 tent). Reads the smaller mip
// and writes a blurred, spread copy into the next larger one. The pipeline
// blends ONE:ONE, so this ADDS onto the larger mip's own downsampled
// content — walking back up the chain accumulates every scale into mip 0,
// the wide soft falloff a single-resolution blur can't reach.
//
// params.xy = 1 / SOURCE (smaller mip) size; params.z = tent radius in
// source texels.

layout(location = 0) in vec2 fragUV;
layout(location = 0) out vec4 outColor;

layout(binding = 0) uniform sampler2D uSrc;

layout(push_constant) uniform PC { vec4 params; } pc;

void main() {
    vec2 uv = fragUV;
    vec2 t = pc.params.xy * pc.params.z;

    vec3 col = texture(uSrc, uv + vec2(-t.x, -t.y)).rgb;
    col += texture(uSrc, uv + vec2( 0.0, -t.y)).rgb * 2.0;
    col += texture(uSrc, uv + vec2( t.x, -t.y)).rgb;
    col += texture(uSrc, uv + vec2(-t.x,  0.0)).rgb * 2.0;
    col += texture(uSrc, uv + vec2( 0.0,  0.0)).rgb * 4.0;
    col += texture(uSrc, uv + vec2( t.x,  0.0)).rgb * 2.0;
    col += texture(uSrc, uv + vec2(-t.x,  t.y)).rgb;
    col += texture(uSrc, uv + vec2( 0.0,  t.y)).rgb * 2.0;
    col += texture(uSrc, uv + vec2( t.x,  t.y)).rgb;

    outColor = vec4(col * (1.0 / 16.0), 1.0);
}
