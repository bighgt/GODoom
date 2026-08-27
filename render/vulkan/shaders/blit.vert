#version 450

// Classic "fullscreen triangle from nothing" trick: three synthesized
// vertices, no vertex buffer, covering the whole clip-space square (and
// then some — the extra area is clipped away for free by the rasterizer).
// This is the entire geometry side of the blit pipeline that presents
// raster.Renderer's CPU-rendered frame (see render/vulkan/pipeline.go).

layout(location = 0) out vec2 fragUV;

void main() {
    vec2 pos = vec2((gl_VertexIndex << 1) & 2, gl_VertexIndex & 2);
    fragUV = pos;
    gl_Position = vec4(pos * 2.0 - 1.0, 0.0, 1.0);
}
