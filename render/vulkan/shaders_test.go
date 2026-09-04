package vulkan

import (
	"encoding/binary"
	"testing"
)

// The pipelines feed these embedded SPIR-V blobs straight to
// vkCreateShaderModule, which reads them as a stream of uint32 words. Catch
// a truncated / non-SPIR-V / wrong-endian file at test time rather than as
// a device-lost at startup.
func TestEmbeddedSPIRV(t *testing.T) {
	const spirvMagic = 0x07230203
	blobs := map[string][]byte{
		"blit.vert":      blitVertSPV,
		"blit.frag":      blitFragSPV,
		"light.frag":     litLightFragSPV,
		"bright.frag":    litBrightFragSPV,
		"blur.frag":      litBlurFragSPV,
		"composite.frag": litCompositeFragSPV,
		"world.vert":     worldVertSPV,
		"world.frag":     worldFragSPV,
		"worldblit.frag": worldBlitFragSPV,
		"depthonly.vert": depthOnlyVertSPV,
		"depth.frag":     depthFragSPV,
	}
	for name, b := range blobs {
		if len(b) == 0 {
			t.Errorf("%s: embedded SPIR-V is empty", name)
			continue
		}
		if len(b)%4 != 0 {
			t.Errorf("%s: SPIR-V length %d is not a multiple of 4", name, len(b))
			continue
		}
		if got := binary.LittleEndian.Uint32(b[:4]); got != spirvMagic {
			t.Errorf("%s: first word 0x%08x, want SPIR-V magic 0x%08x", name, got, spirvMagic)
		}
	}
}
