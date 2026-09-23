#version 450

// Hardware renderer — cube shadow-map fragment stage. Writes the LINEAR
// distance from this fragment to the light (normalised by the far plane)
// to gl_FragDepth instead of letting the rasteriser's own projective z
// through — the standard point-shadow technique: later, world.frag samples
// this cube map along (fragment - light) and compares that same linear
// distance directly, rather than needing to redo a projective un-mapping
// per cube face at sample time.

layout(location = 0) in vec3 vWorldPos;

layout(push_constant) uniform PC {
    mat4 viewProj;
    vec4 lightPosFar; // xyz = light world position, w = far plane (world units)
} pc;

void main() {
    float dist = length(vWorldPos - pc.lightPosFar.xyz);
    gl_FragDepth = dist / max(pc.lightPosFar.w, 1e-4);
}
