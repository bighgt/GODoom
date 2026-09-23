#version 450

// Hardware renderer — cube shadow-map vertex stage. Positions only (same
// static-geometry vertex buffer/stride the opaque depth pre-pass reads,
// see depthonly.vert), but also passes the world-space position through
// unmodified so shadow.frag can measure its distance to the light — a
// plain depth pre-pass has no need for that, which is why this isn't just
// depthonly.vert reused as-is.

layout(location = 0) in vec3 inPos;

layout(location = 0) out vec3 vWorldPos;

layout(push_constant) uniform PC {
    mat4 viewProj;     // one cube face's 90-degree view-projection, from the light's own position
    vec4 lightPosFar;  // xyz = light world position, w = far plane (world units)
} pc;

void main() {
    vWorldPos = inPos;
    gl_Position = pc.viewProj * vec4(inPos, 1.0);
}
