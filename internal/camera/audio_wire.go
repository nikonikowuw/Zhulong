package camera

import (
	"encoding/base64"
	"encoding/binary"
	"errors"

	"github.com/nikonikowuw/Zhulong/internal/engine"
)

// ZLA1 音频帧头布局（全部大端序，共 20 字节）：
//
//	偏移  长度  字段
//	 0     4    magic   = 0x5A4C4131 ("ZLA1")
//	 4     1    codec   与 C ABI 的 Zhulong_CODEC_* 一致（0x10=AAC, 0x11=PCMA, 0x12=PCMU）
//	 5     1    flags   AudioWireFlag* 位组合
//	 6     2    reserved 恒为 0
//	 8     4    seq     音频轨独立计数，逐包递增
//	12     8    pts_us  与视频同一条输出时间轴的微秒值（原生层已归一化，前端不得重算）
//	20     N    payload G.711 原始采样 / AAC raw AU
//
// 为什么不复用 ZLM2 头：那里的 frame_class / gop_id 对音频没有意义，复用只会让
// 「这个字段对音频该填什么」变成一个永远没有正确回答的问题。
const (
	// WireMagicZLA1 是音频通道的 4 字节魔数（ASCII "ZLA1"）。
	WireMagicZLA1 uint32 = 0x5A4C4131

	// AudioWireHeaderSize 是 ZLA1 二进制固定帧头长度。
	AudioWireHeaderSize = 20
)

// AudioWireFlag 音频帧标志位。
const (
	// AudioWireFlagDiscontinuity 源流时间轴跳变（或视频侧重锚），接收方应清空缓冲重新对齐。
	AudioWireFlagDiscontinuity byte = 0x01
	// AudioWireFlagTimestampSynthetic pts 由原生层按包长/帧长递推合成（源缺失 PTS）。
	AudioWireFlagTimestampSynthetic byte = 0x02
)

var (
	ErrAudioWireBufferTooShort = errors.New("audio wire buffer too short")
	ErrAudioWireInvalidMagic   = errors.New("invalid audio wire magic")
)

// AudioWireHeader 描述 ZLA1 音频帧头。
type AudioWireHeader struct {
	Codec engine.Codec
	Flags byte
	Seq   uint32
	PTSUS int64
}

// HasDiscontinuity 返回源流时间轴是否跳变。
func (h AudioWireHeader) HasDiscontinuity() bool {
	return h.Flags&AudioWireFlagDiscontinuity != 0
}

// PackAudioPacket 将一帧音频包封包为 [20 字节 AudioWireHeader] + [裸露负载]。
//
// seq 由音频通道自己维护：视频与音频是两条独立的序列，共用一个计数器只会让
// 「哪条轨真的丢了包」无法区分。
func PackAudioPacket(pkt engine.Packet, seq uint32) []byte {
	buf := make([]byte, AudioWireHeaderSize+len(pkt.Data))
	binary.BigEndian.PutUint32(buf[0:4], WireMagicZLA1)
	buf[4] = byte(pkt.Codec)
	buf[5] = audioWireFlags(pkt.Flags)
	// buf[6:8] reserved，make() 已置零
	binary.BigEndian.PutUint32(buf[8:12], seq)
	binary.BigEndian.PutUint64(buf[12:20], uint64(pkt.PTSUS))
	copy(buf[AudioWireHeaderSize:], pkt.Data)
	return buf
}

// UnpackAudioPacket 解析 ZLA1 帧，返回帧头与负载视图（共享底层数组）。
func UnpackAudioPacket(data []byte) (AudioWireHeader, []byte, error) {
	if len(data) < AudioWireHeaderSize {
		return AudioWireHeader{}, nil, ErrAudioWireBufferTooShort
	}
	if magic := binary.BigEndian.Uint32(data[0:4]); magic != WireMagicZLA1 {
		return AudioWireHeader{}, nil, ErrAudioWireInvalidMagic
	}
	header := AudioWireHeader{
		Codec: engine.Codec(data[4]),
		Flags: data[5],
		Seq:   binary.BigEndian.Uint32(data[8:12]),
		PTSUS: int64(binary.BigEndian.Uint64(data[12:20])),
	}
	return header, data[AudioWireHeaderSize:], nil
}

func audioWireFlags(flags engine.PacketFlag) byte {
	var out byte
	if flags.Has(engine.PacketFlagDiscontinuity) {
		out |= AudioWireFlagDiscontinuity
	}
	if flags.Has(engine.PacketFlagTimestampSynthetic) {
		out |= AudioWireFlagTimestampSynthetic
	}
	return out
}

// 音频通道的文本帧。三种帧共用 type 字段区分，二进制帧不带 type。
const (
	// AudioFrameTypeInfo 建连后的第一条文本帧，携带流级常量。
	AudioFrameTypeInfo = "audio_info"
	// AudioFrameTypeError 无法提供音频时的终止性文本帧（随后连接被关闭）。
	AudioFrameTypeError = "error"
	// StreamFrameTypeInfo 预览 WS 建连后的一次性能力帧（旧前端按 ArrayBuffer 判据忽略它）。
	StreamFrameTypeInfo = "stream_info"
)

// StreamInfoMessage 是预览 WebSocket 建连时下发的能力帧。
//
// 单独的文本帧而不是塞进二进制头：二进制帧必须逐字节不变（旧 bundle 不能因为
// 「多了一个字段」而黑屏），而文本帧对老前端天然不可见。
type StreamInfoMessage struct {
	Type     string              `json:"type"`
	CameraID string              `json:"cameraId"`
	Role     string              `json:"role"`
	Audio    AudioCapabilityInfo `json:"audio"`
	Version  int                 `json:"v"`
}

// AudioCapabilityInfo 描述一路流是否可听。
//
// Config 是 AudioSpecificConfig 的 base64（G.711 为空串）。注意它与 SDP 里
// fmtp 的 config=（十六进制）不是一回事，前端不要拿去直接当十六进制解析。
type AudioCapabilityInfo struct {
	Reason     string `json:"reason,omitempty"`
	Codec      string `json:"codec"`
	Config     string `json:"config"`
	SampleRate int    `json:"sampleRate"`
	Channels   int    `json:"channels"`
	Present    bool   `json:"present"`
}

// AudioInfoMessage 是音频通道建连后的第一条文本帧。
type AudioInfoMessage struct {
	Type       string `json:"type"`
	Codec      string `json:"codec"`
	Config     string `json:"config"`
	SampleRate int    `json:"sampleRate"`
	Channels   int    `json:"channels"`
	HasAudio   bool   `json:"hasAudio"`
}

// AudioErrorMessage 是无法提供音频时的终止性文本帧。
type AudioErrorMessage struct {
	Type   string `json:"type"`
	Code   string `json:"code"`
	Reason string `json:"reason,omitempty"`
}

// 音频通道不可用时的稳定机器码（前端据此决定置灰还是重试）。
const (
	AudioErrorNoAudio         = "NO_AUDIO"
	AudioErrorLimit           = "TOO_MANY_CHANNELS"
	AudioErrorSubscribeFailed = "AUDIO_SUBSCRIBE_FAILED"
)

// audioCapability 把引擎元数据投影成能力帧。
func audioCapability(info engine.AudioInfo, reason string) AudioCapabilityInfo {
	capability := AudioCapabilityInfo{
		Present:    info.HasAudio,
		SampleRate: info.SampleRate,
		Channels:   info.Channels,
		Codec:      info.Codec.String(),
	}
	if len(info.Config) > 0 {
		capability.Config = base64.StdEncoding.EncodeToString(info.Config)
	}
	if !info.HasAudio {
		if reason == "" {
			reason = "camera reports no audio track"
		}
		capability.Reason = reason
	}
	return capability
}
