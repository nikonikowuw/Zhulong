package camera

import (
	"bytes"
	"encoding/base64"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/engine"
)

// TestAudioWireRoundTrip 钉住 ZLA1 的 20 字节头布局。
//
// 前端要按偏移直接读这块内存，任何字段挪位都必须当场红，而不是等浏览器解码出噪声。
func TestAudioWireRoundTrip(t *testing.T) {
	pkt := engine.Packet{
		TrackType: engine.TrackAudio,
		Codec:     engine.CodecPCMA,
		PtsUS:     1_234_567,
		DtsUS:     1_234_567,
		Flags:     engine.PacketFlagDiscontinuity,
		Data:      bytes.Repeat([]byte{0xD5}, 160),
	}

	frame := PackAudioPacket(pkt, 7)
	if len(frame) != AudioWireHeaderSize+160 {
		t.Fatalf("expected %d bytes, got %d", AudioWireHeaderSize+160, len(frame))
	}
	if !bytes.Equal(frame[0:4], []byte("ZLA1")) {
		t.Fatalf("expected ZLA1 magic, got %x", frame[0:4])
	}
	if frame[4] != byte(engine.CodecPCMA) {
		t.Fatalf("expected codec byte %d, got %d", byte(engine.CodecPCMA), frame[4])
	}
	// reserved 必须为零：前端可以据此断言「没有意外的扩展字段」
	if frame[6] != 0 || frame[7] != 0 {
		t.Fatalf("expected zeroed reserved bytes, got %x", frame[6:8])
	}

	header, payload, err := UnpackAudioPacket(frame)
	if err != nil {
		t.Fatalf("unpack failed: %v", err)
	}
	if header.Codec != engine.CodecPCMA {
		t.Fatalf("expected PCMA, got %v", header.Codec)
	}
	if header.Seq != 7 {
		t.Fatalf("expected seq 7, got %d", header.Seq)
	}
	if header.PTSUS != 1_234_567 {
		t.Fatalf("expected pts_us 1234567, got %d", header.PTSUS)
	}
	if !header.HasDiscontinuity() {
		t.Fatal("expected discontinuity flag")
	}
	if !bytes.Equal(payload, pkt.Data) {
		t.Fatal("payload mismatch")
	}
}

// TestAudioWireFlags 钉住标志位与原生 PacketFlag 的映射。
func TestAudioWireFlags(t *testing.T) {
	header, _, err := UnpackAudioPacket(PackAudioPacket(engine.Packet{
		TrackType: engine.TrackAudio,
		Codec:     engine.CodecAAC,
		Flags:     engine.PacketFlagTimestampSynthetic,
	}, 0))
	if err != nil {
		t.Fatalf("unpack failed: %v", err)
	}
	if header.HasDiscontinuity() {
		t.Fatal("synthetic timestamp must not imply discontinuity")
	}
	if header.Flags&AudioWireFlagTimestampSynthetic == 0 {
		t.Fatal("expected synthetic timestamp flag")
	}
}

// TestAudioWireRejectsForeignFrames 防止把视频帧当音频帧解析。
func TestAudioWireRejectsForeignFrames(t *testing.T) {
	video := PackPacket(engine.Packet{Codec: engine.CodecH264, Data: []byte{1, 2, 3}}, 1)
	if _, _, err := UnpackAudioPacket(video); err != ErrAudioWireInvalidMagic {
		t.Fatalf("expected ErrAudioWireInvalidMagic, got %v", err)
	}
	if _, _, err := UnpackAudioPacket([]byte("ZLA1")); err != ErrAudioWireBufferTooShort {
		t.Fatalf("expected ErrAudioWireBufferTooShort, got %v", err)
	}
}

// TestAudioCapabilityProjectsConfig 钉住能力帧的字段语义。
func TestAudioCapabilityProjectsConfig(t *testing.T) {
	asc := []byte{0x12, 0x10}
	capability := audioCapability(engine.AudioInfo{
		HasAudio:   true,
		Codec:      engine.CodecAAC,
		SampleRate: 44100,
		Channels:   2,
		Config:     asc,
	}, "")

	if !capability.Present {
		t.Fatal("expected Present=true")
	}
	if capability.Codec != "aac" || capability.SampleRate != 44100 || capability.Channels != 2 {
		t.Fatalf("unexpected capability: %+v", capability)
	}
	if capability.Config != base64.StdEncoding.EncodeToString(asc) {
		t.Fatalf("expected base64 ASC, got %q", capability.Config)
	}
	if capability.Reason != "" {
		t.Fatalf("present audio must not carry a reason, got %q", capability.Reason)
	}

	absent := audioCapability(engine.AudioInfo{}, "")
	if absent.Present {
		t.Fatal("expected Present=false")
	}
	if absent.Reason == "" {
		t.Fatal("absent audio must explain why, otherwise the UI can only grey out silently")
	}

	// G.711 没有 extradata：必须是空串而不是 base64("") 之类的东西
	pcma := audioCapability(engine.AudioInfo{HasAudio: true, Codec: engine.CodecPCMA, SampleRate: 8000, Channels: 1}, "")
	if pcma.Config != "" {
		t.Fatalf("expected empty config for G.711, got %q", pcma.Config)
	}
}
