#version 450

// Hardware renderer — depth pre-pass vertex stage. Positions only, same
// view-projection push constant as world.vert, so gl_Position is
// bit-identical and the colour pass can use a LESS_OR_EQUAL depth test.

layout(location = 0) in vec3 inPos;

layout(push_constant) uniform PC {
    mat4 viewProj;
} pc;

void main() {
    gl_Position = pc.viewProj * vec4(inPos, 1.0);
}
