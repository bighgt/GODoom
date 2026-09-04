#version 450

// Hardware renderer — depth pre-pass fragment stage: writes no colour
// (the pipeline masks the colour attachment off), just lets the depth
// test/write happen.

void main() {}
