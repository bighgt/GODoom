#version 450

// Hardware bloom — Kawase blur iteration. Four bilinear taps at the corners
// of a square whose half-size is params.z destination texels. recordBloom
// ping-pongs this between the two half-res targets with a growing then
// tapering offset (0.5, 1.5, 2.5, 3.5, 3.5, 2.5); each pass roughly doubles
// the perceived blur radius, so a handful of 4-tap passes approximate a
// very wide, soft Gaussian far more cheaply than one big kernel — the
// layered, filmic look a multi-mip bloom gives, without the extra targets.
//
// params.xy = 1 / target size; params.z = offset in texels.

layout(location = 0) in vec2 fragUV;
layout(location = 0) out vec4 outColor;

layout(binding = 0) uniform sampler2D uTex;

layout(push_constant) uniform PC { vec4 params; } pc;

void main() {
    vec2 uv = gl_FragCoord.xy * pc.params.xy;
    vec2 o = pc.params.xy * pc.params.z;

    vec3 c  = texture(uTex, uv + vec2( o.x,  o.y)).rgb;
    c      += texture(uTex, uv + vec2(-o.x,  o.y)).rgb;
    c      += texture(uTex, uv + vec2( o.x, -o.y)).rgb;
    c      += texture(uTex, uv + vec2(-o.x, -o.y)).rgb;

    outColor = vec4(c * 0.25, 1.0);
}
