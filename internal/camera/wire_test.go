package camera

import (
	"bytes"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/engine"
)

func TestWireProtocolPackUnpack(t *testing.T) {
	tests := []struct {
		name     string
		packet   engine.Packet
		expCodec byte
		expKey   bool
		expPTS   bool
		expDTS   bool
	}{
		{
			name: "H264 KeyFrame with PTS and DTS",
			packet: engine.Packet{
				Codec:    engine.CodecH264,
				PTS:      90000,
				DTS:      90000,
				HasPTS:   true,
				HasDTS:   true,
				KeyFrame: true,
				Data:     []byte{0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0x00},
			},
			expCodec: WireCodecH264,
			expKey:   true,
			expPTS:   true,
			expDTS:   true,
		},
		{
			name: "H265 Non-KeyFrame with PTS only",
			packet: engine.Packet{
				Codec:    engine.CodecH265,
				PTS:      180000,
				DTS:      0,
				HasPTS:   true,
				HasDTS:   false,
				KeyFrame: false,
				Data:     []byte{0x00, 0x00, 0x01, 0x02, 0x03},
			},
			expCodec: WireCodecH265,
			expKey:   false,
			expPTS:   true,
			expDTS:   false,
		},
		{
			name: "Unknown Codec without timestamps and empty payload",
			packet: engine.Packet{
				Codec:    engine.Codec(99),
				PTS:      0,
				DTS:      0,
				HasPTS:   false,
				HasDTS:   false,
				KeyFrame: false,
				Data:     nil,
			},
			expCodec: WireCodecUnknown,
			expKey:   false,
			expPTS:   false,
			expDTS:   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			packed := PackPacket(tc.packet)
			if len(packed) != WireHeaderSize+len(tc.packet.Data) {
				t.Fatalf("expected packed len %d, got %d", WireHeaderSize+len(tc.packet.Data), len(packed))
			}

			header, payload, err := UnpackPacket(packed)
			if err != nil {
				t.Fatalf("unexpected unpack error: %v", err)
			}

			if header.Codec != tc.expCodec {
				t.Errorf("expected codec %d, got %d", tc.expCodec, header.Codec)
			}
			if header.IsKeyFrame() != tc.expKey {
				t.Errorf("expected keyframe %v, got %v", tc.expKey, header.IsKeyFrame())
			}
			if header.HasPTS() != tc.expPTS {
				t.Errorf("expected hasPTS %v, got %v", tc.expPTS, header.HasPTS())
			}
			if header.HasDTS() != tc.expDTS {
				t.Errorf("expected hasDTS %v, got %v", tc.expDTS, header.HasDTS())
			}
			if header.PTS != tc.packet.PTS {
				t.Errorf("expected PTS %d, got %d", tc.packet.PTS, header.PTS)
			}
			if header.DTS != tc.packet.DTS {
				t.Errorf("expected DTS %d, got %d", tc.packet.DTS, header.DTS)
			}
			if !bytes.Equal(payload, tc.packet.Data) {
				t.Errorf("payload mismatch: expected %x, got %x", tc.packet.Data, payload)
			}
		})
	}
}

func TestWireProtocolUnpackErrors(t *testing.T) {
	// Too short
	shortBuf := make([]byte, 10)
	_, _, err := UnpackPacket(shortBuf)
	if err != ErrWireBufferTooShort {
		t.Errorf("expected ErrWireBufferTooShort, got %v", err)
	}

	// Invalid magic
	badMagic := make([]byte, WireHeaderSize)
	badMagic[0] = 0x12
	badMagic[1] = 0x34
	_, _, err = UnpackPacket(badMagic)
	if err != ErrWireInvalidMagic {
		t.Errorf("expected ErrWireInvalidMagic, got %v", err)
	}
}
