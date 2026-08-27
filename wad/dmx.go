package wad

import (
	"encoding/binary"
	"fmt"
)

// Sound is a decoded DMX PCM sound effect (a DS* lump): mono, 8-bit
// unsigned samples at SampleRate Hz. Vanilla Doom's sounds are almost all
// 11025 Hz, with a handful at 22050 Hz — nothing else needs to be assumed
// about the rate.
type Sound struct {
	SampleRate int
	Samples    []byte // 8-bit unsigned PCM, mono
}

// DecodeDMX parses a DS* sound effect lump: an 8-byte header (uint16
// format — must be 3 for PCM, the only format vanilla Doom's IWADs use;
// uint16 sample rate; uint32 sample count) followed by that many 8-bit
// unsigned PCM samples. The original DMX driver pads the first/last 16
// samples for its own mixer's benefit; they're still valid audio and are
// kept as-is here rather than trimmed.
func DecodeDMX(b []byte) (*Sound, error) {
	if len(b) < 8 {
		return nil, fmt.Errorf("wad: sound lump too small (%d bytes)", len(b))
	}
	format := binary.LittleEndian.Uint16(b[0:2])
	if format != 3 {
		return nil, fmt.Errorf("wad: sound lump has format %d, only PCM format 3 is supported", format)
	}
	sampleRate := int(binary.LittleEndian.Uint16(b[2:4]))
	sampleCount := int(binary.LittleEndian.Uint32(b[4:8]))
	if sampleRate <= 0 {
		return nil, fmt.Errorf("wad: sound lump has invalid sample rate %d", sampleRate)
	}
	if sampleCount < 0 || 8+sampleCount > len(b) {
		return nil, fmt.Errorf("wad: sound lump claims %d samples but the lump is only %d bytes", sampleCount, len(b))
	}

	samples := make([]byte, sampleCount)
	copy(samples, b[8:8+sampleCount])
	return &Sound{SampleRate: sampleRate, Samples: samples}, nil
}
