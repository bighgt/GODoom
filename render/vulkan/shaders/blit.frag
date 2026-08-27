#version 450

// Samples raster.Renderer's uploaded frame with nearest filtering (set on
// the sampler, not here) so the internal 320x200 raster image keeps its
// chunky, vanilla-Doom pixel look when stretched to the real window size.

layout(location = 0) in vec2 fragUV;
layout(location = 0) out vec4 outColor;

layout(binding = 0) uniform sampler2D tex;

void main() {
    outColor = texture(tex, fragUV);
}
