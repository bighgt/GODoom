package wad

import (
	"encoding/binary"
	"fmt"
)

// MusEventType identifies what kind of MIDI-equivalent event a MusEvent
// carries. MUS (Paul Radek's DMX library format id used for Doom's music
// lumps, D_*) is deliberately close to MIDI — it only had to be translated,
// not reinterpreted — so these map onto MIDI channel-voice messages almost
// one-to-one; see DecodeMus's doc comment for the format itself.
type MusEventType int

const (
	MusNoteOff MusEventType = iota
	MusNoteOn
	MusPitchBend
	MusControlChange
	MusProgramChange
)

// MusEvent is one decoded MUS event, already translated to (almost)
// straight MIDI terms: Channel is the *MIDI* channel (MUS channel 15,
// percussion, is remapped to MIDI channel 9, matching General MIDI's own
// percussion-channel convention). DeltaTicks is how many MUS ticks to wait
// *before* this event, relative to the previous one in the stream.
type MusEvent struct {
	DeltaTicks int
	Type       MusEventType
	Channel    byte
	Data1      byte   // note number / MIDI CC number / program number
	Data2      byte   // velocity / MIDI CC value (unused for PitchBend/ProgramChange)
	Bend14     uint16 // 14-bit pitch bend value (PitchBend only)
}

// musControllerToMIDI maps a MUS controller number (0-14) to the MIDI
// controller-change number it corresponds to (or, for index 0, is handled
// specially as a Program Change instead — see the loop in DecodeMus).
// Indices 10-14 double as the "system event" controller numbers (all
// sounds off, all notes off, mono, poly, reset all controllers).
var musControllerToMIDI = [15]byte{
	0x00, 0x20, 0x01, 0x07, 0x0A, 0x0B, 0x5B, 0x5D, 0x40, 0x43,
	0x78, 0x7B, 0x7E, 0x7F, 0x79,
}

// DecodeMus parses a D_* music lump in Paul Radek's MUS format: a 14-byte
// header (4-byte "MUS\x1A" magic, uint16 score length, uint16 score
// offset, uint16 primary channel count, uint16 secondary channel count,
// uint16 instrument count — only the score offset is needed to decode),
// followed by the event stream itself.
//
// Each event starts with one descriptor byte: bit 7 is the "last event in
// this group" flag (a delta-time VLQ follows when set), bits 6-4 are the
// event type (0 release note, 1 play note, 2 pitch bend, 3 "system" event,
// 4 controller change, 6 score end), and bits 3-0 are the MUS channel.
// Delta time, when present, is a base-128 varint: each byte contributes 7
// bits (value&0x7F), accumulating until a byte with the high bit clear.
func DecodeMus(b []byte) ([]MusEvent, error) {
	if len(b) < 14 || string(b[0:3]) != "MUS" || b[3] != 0x1A {
		return nil, fmt.Errorf("wad: not a MUS lump (bad magic)")
	}
	scoreLen := int(binary.LittleEndian.Uint16(b[4:6]))
	scoreOfs := int(binary.LittleEndian.Uint16(b[6:8]))
	if scoreOfs < 0 || scoreOfs+scoreLen > len(b) {
		return nil, fmt.Errorf("wad: MUS score (offset %d, length %d) runs past the end of the lump (%d bytes)", scoreOfs, scoreLen, len(b))
	}

	var events []MusEvent
	var lastVelocity [16]byte
	for i := range lastVelocity {
		lastVelocity[i] = 64
	}

	pos := scoreOfs
	end := scoreOfs + scoreLen
	pendingDelta := 0 // ticks accumulated since the last event, applied to the next one

	readByte := func() (byte, error) {
		if pos >= end {
			return 0, fmt.Errorf("wad: MUS stream ran past the end of the score")
		}
		v := b[pos]
		pos++
		return v, nil
	}

	for pos < end {
		desc, err := readByte()
		if err != nil {
			return nil, err
		}
		last := desc&0x80 != 0
		eventType := (desc >> 4) & 0x07
		musChannel := desc & 0x0F

		channel := musChannel
		if musChannel == 15 {
			channel = 9 // MUS's dedicated percussion channel -> General MIDI's
		}

		switch eventType {
		case 0: // release note
			note, err := readByte()
			if err != nil {
				return nil, err
			}
			events = append(events, MusEvent{DeltaTicks: pendingDelta, Type: MusNoteOff, Channel: channel, Data1: note & 0x7F})
			pendingDelta = 0

		case 1: // play note
			noteByte, err := readByte()
			if err != nil {
				return nil, err
			}
			note := noteByte & 0x7F
			velocity := lastVelocity[musChannel]
			if noteByte&0x80 != 0 {
				v, err := readByte()
				if err != nil {
					return nil, err
				}
				velocity = v & 0x7F
				lastVelocity[musChannel] = velocity
			}
			events = append(events, MusEvent{DeltaTicks: pendingDelta, Type: MusNoteOn, Channel: channel, Data1: note, Data2: velocity})
			pendingDelta = 0

		case 2: // pitch bend
			value, err := readByte()
			if err != nil {
				return nil, err
			}
			// MUS's pitch value is an unsigned byte (0..255, ~128 = center);
			// scale into MIDI's 14-bit range the same way mus2mid does.
			bend := uint16(value) * 64
			events = append(events, MusEvent{DeltaTicks: pendingDelta, Type: MusPitchBend, Channel: channel, Bend14: bend})
			pendingDelta = 0

		case 3: // system event (all sounds off, all notes off, mono, poly, reset controllers)
			ctrl, err := readByte()
			if err != nil {
				return nil, err
			}
			if int(ctrl) < len(musControllerToMIDI) {
				events = append(events, MusEvent{DeltaTicks: pendingDelta, Type: MusControlChange, Channel: channel, Data1: musControllerToMIDI[ctrl]})
				pendingDelta = 0
			}

		case 4: // controller change (0 is special-cased as a program/patch change)
			ctrl, err := readByte()
			if err != nil {
				return nil, err
			}
			value, err := readByte()
			if err != nil {
				return nil, err
			}
			if ctrl == 0 {
				events = append(events, MusEvent{DeltaTicks: pendingDelta, Type: MusProgramChange, Channel: channel, Data1: value & 0x7F})
			} else if int(ctrl) < len(musControllerToMIDI) {
				events = append(events, MusEvent{DeltaTicks: pendingDelta, Type: MusControlChange, Channel: channel, Data1: musControllerToMIDI[ctrl], Data2: value & 0x7F})
			}
			pendingDelta = 0

		case 6: // score end
			return events, nil

		default: // 5 and 7 are unused/reserved; nothing to read
		}

		if last {
			delta := 0
			for {
				db, err := readByte()
				if err != nil {
					return nil, err
				}
				delta = delta*128 + int(db&0x7F)
				if db&0x80 == 0 {
					break
				}
			}
			pendingDelta += delta
		}
	}

	return events, nil
}
