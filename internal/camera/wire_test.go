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
		seq      uint32
		expCodec byte
		expClass byte
		expKey   bool
		expPTSUS int64
		expGOPID uint32
	}{
		{
			name: "H264 IDR with injected parameter sets",
			packet: engine.Packet{
				Codec:      engine.CodecH264,
				PTS:        90000,
				DTS:        90000,
				HasPTS:     true,
				HasDTS:     true,
				KeyFrame:   true,
				PtsUS:      1000000,
				DtsUS:      1000000,
				FrameClass: engine.FrameIDR,
				GOPID:      7,
				Flags:      engine.PacketFlagParameterSetsInjected,
				Data:       []byte{0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0x00},
			},
			seq:      42,
			expCodec: WireCodecH264,
			expClass: WireFrameIDR,
			expKey:   true,
			expPTSUS: 1000000,
			expGOPID: 7,
		},
		{
			name: "H265 predictive frame",
			packet: engine.Packet{
				Codec:      engine.CodecH265,
				PTS:        180000,
				HasPTS:     true,
				PtsUS:      2000000,
				DtsUS:      2000000,
				FrameClass: engine.FramePredictive,
				GOPID:      3,
				Data:       []byte{0x00, 0x00, 0x01, 0x02, 0x03},
			},
			seq:      43,
			expCodec: WireCodecH265,
			expClass: WireFramePredictive,
			expKey:   false,
			expPTSUS: 2000000,
			expGOPID: 3,
		},
		{
			name: "Non-reference frame is marked droppable",
			packet: engine.Packet{
				Codec:      engine.CodecH264,
				PtsUS:      3000000,
				DtsUS:      3000000,
				FrameClass: engine.FrameNonReference,
				GOPID:      3,
				Data:       []byte{0x00, 0x00, 0x01, 0x41},
			},
			seq:      44,
			expCodec: WireCodecH264,
			expClass: WireFrameNonReference,
			expKey:   false,
			expPTSUS: 3000000,
			expGOPID: 3,
		},
		{
			name: "Unknown codec without timestamps and empty payload",
			packet: engine.Packet{
				Codec: engine.Codec(99),
			},
			seq:      0,
			expCodec: WireCodecUnknown,
			expClass: WireFramePredictive, // 未分类时保守视为参考帧，绝不标成可丢
			expKey:   false,
			expPTSUS: 0,
			expGOPID: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			packed := PackPacket(tc.packet, tc.seq)
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
			if header.FrameClass != tc.expClass {
				t.Errorf("expected frame class %d, got %d", tc.expClass, header.FrameClass)
			}
			if header.IsKeyFrame() != tc.expKey {
				t.Errorf("expected keyframe %v, got %v", tc.expKey, header.IsKeyFrame())
			}
			if header.PTSUS != tc.expPTSUS {
				t.Errorf("expected pts_us %d, got %d", tc.expPTSUS, header.PTSUS)
			}
			if header.GOPID != tc.expGOPID {
				t.Errorf("expected gop id %d, got %d", tc.expGOPID, header.GOPID)
			}
			if header.Seq != tc.seq {
				t.Errorf("expected seq %d, got %d", tc.seq, header.Seq)
			}
			if !bytes.Equal(payload, tc.packet.Data) {
				t.Errorf("payload mismatch: expected %x, got %x", tc.packet.Data, payload)
			}
		})
	}
}

func TestWireProtocolFlagMapping(t *testing.T) {
	pkt := engine.Packet{
		Codec:      engine.CodecH264,
		KeyFrame:   true,
		FrameClass: engine.FrameIDR,
		Flags: engine.PacketFlagParameterSetsInjected |
			engine.PacketFlagDiscontinuity |
			engine.PacketFlagTimestampSynthetic |
			engine.PacketFlagDtsSynthetic,
		Data: []byte{0x00, 0x00, 0x01, 0x65},
	}

	header, _, err := UnpackPacket(PackPacket(pkt, 1))
	if err != nil {
		t.Fatalf("unexpected unpack error: %v", err)
	}

	for _, flag := range []struct {
		name string
		bit  byte
	}{
		{"keyframe", WireFlagKeyFrame},
		{"discontinuity", WireFlagDiscontinuity},
		{"parameter sets injected", WireFlagParamInjected},
		{"synthetic timestamp", WireFlagTimestampSynthetic},
		{"synthetic DTS", WireFlagDtsSynthetic},
	} {
		if header.Flags&flag.bit == 0 {
			t.Errorf("expected %s flag to be set, flags=%#x", flag.name, header.Flags)
		}
	}
	if !header.HasDiscontinuity() {
		t.Errorf("expected HasDiscontinuity to report true")
	}
}

func TestWireProtocolUnpackErrors(t *testing.T) {
	t.Run("too short", func(t *testing.T) {
		if _, _, err := UnpackPacket(make([]byte, 10)); err != ErrWireBufferTooShort {
			t.Errorf("expected ErrWireBufferTooShort, got %v", err)
		}
	})

	t.Run("invalid magic", func(t *testing.T) {
		badMagic := make([]byte, WireHeaderSize)
		badMagic[0], badMagic[1] = 0x12, 0x34
		if _, _, err := UnpackPacket(badMagic); err != ErrWireInvalidMagic {
			t.Errorf("expected ErrWireInvalidMagic, got %v", err)
		}
	})

	t.Run("legacy ZLM1 header rejected", func(t *testing.T) {
		// 回归保护：旧的 24 字节 ZLM1 帧必须以「不支持的版本」被拒绝，
		// 而不是被当成 ZLM2 解析出一堆垃圾时间戳（前端会静默错时序）。
		legacy := make([]byte, 24)
		legacy[0], legacy[1], legacy[2], legacy[3] = 0x5A, 0x4C, 0x4D, 0x31
		if _, _, err := UnpackPacket(legacy); err != ErrWireBufferTooShort {
			t.Errorf("expected ErrWireBufferTooShort for 24-byte ZLM1 frame, got %v", err)
		}
	})

	t.Run("unsupported version", func(t *testing.T) {
		buf := PackPacket(engine.Packet{Codec: engine.CodecH264}, 0)
		buf[4] = 1 // 魔数正确但版本回退
		if _, _, err := UnpackPacket(buf); err != ErrWireUnsupportedVersion {
			t.Errorf("expected ErrWireUnsupportedVersion, got %v", err)
		}
	})
}
