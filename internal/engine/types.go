package engine

import (
	"fmt"
	"time"
)

// Transport 定义 RTSP 底层传输协议。
type Transport int32

const (
	TransportTCP Transport = 0
	TransportUDP Transport = 1
)

func (t Transport) String() string {
	switch t {
	case TransportTCP:
		return "tcp"
	case TransportUDP:
		return "udp"
	default:
		return fmt.Sprintf("transport(%d)", int32(t))
	}
}

// ConsumerKind 定义流消费者类型。
type ConsumerKind int32

const (
	ConsumerPreview   ConsumerKind = 1
	ConsumerRecording ConsumerKind = 2
	ConsumerAI        ConsumerKind = 3
)

func (k ConsumerKind) String() string {
	switch k {
	case ConsumerPreview:
		return "preview"
	case ConsumerRecording:
		return "recording"
	case ConsumerAI:
		return "ai"
	default:
		return fmt.Sprintf("consumer(%d)", int32(k))
	}
}

// Codec 定义视频编码格式。
type Codec int32

const (
	CodecH264 Codec = 1
	CodecH265 Codec = 2
)

func (c Codec) String() string {
	switch c {
	case CodecH264:
		return "h264"
	case CodecH265:
		return "h265"
	default:
		return fmt.Sprintf("codec(%d)", int32(c))
	}
}

// FrameClass 描述视频帧在当前参考链中的角色，供预览路径决定能否安全丢弃。
//
// 语义约定（由 Native PreviewNormalizer 生成）：
//   - FrameNonReference 可以无条件丢弃；
//   - FramePredictive 被丢弃会破坏后续帧的参考链，只能整段丢到下一个 FrameIDR；
//   - 原生层无法可靠判定时一律归类为 FramePredictive，绝不误判为可丢。
//
// 这些取值与 C ABI 的 Zhulong_FRAME_* 枚举一一对应，两边改动必须同步。
type FrameClass int32

const (
	FrameUnknown      FrameClass = 0
	FrameIDR          FrameClass = 1
	FramePredictive   FrameClass = 2
	FrameNonReference FrameClass = 3
)

func (f FrameClass) String() string {
	switch f {
	case FrameIDR:
		return "idr"
	case FramePredictive:
		return "predictive"
	case FrameNonReference:
		return "non_reference"
	default:
		return "unknown"
	}
}

// PacketFlag 位标记，与 C ABI 的 ZHULONG_PKT_FLAG_* 一一对应。
type PacketFlag uint32

const (
	// PacketFlagTimestampSynthetic 表示 PtsUS 由 DTS 合成（源流缺失 PTS）。
	PacketFlagTimestampSynthetic PacketFlag = 0x0001
	// PacketFlagParameterSetsInjected 表示原生层已为本帧前置注入 VPS/SPS/PPS，
	// 接收方无需再等待码流内自带参数集。
	PacketFlagParameterSetsInjected PacketFlag = 0x0002
	// PacketFlagDiscontinuity 表示源流时间轴发生跳变（相机或 RTSP 会话重建），
	// 接收方必须重置解码器并从关键帧重新开始。
	PacketFlagDiscontinuity PacketFlag = 0x0004
	// PacketFlagDtsSynthetic 表示源流缺失 DTS，DtsUS 是合成的单调解码时钟。
	PacketFlagDtsSynthetic PacketFlag = 0x0008
)

// Has 返回是否包含指定标记位。
func (f PacketFlag) Has(flag PacketFlag) bool {
	return f&flag != 0
}

// StreamState 定义物理流运行状态。
type StreamState int32

const (
	StreamStarting StreamState = 0
	StreamRunning  StreamState = 1
	StreamFailed   StreamState = 2
)

func (s StreamState) String() string {
	switch s {
	case StreamStarting:
		return "starting"
	case StreamRunning:
		return "running"
	case StreamFailed:
		return "failed"
	default:
		return fmt.Sprintf("stream_state(%d)", int32(s))
	}
}

// StreamOptions 定义拉流配置选项。
type StreamOptions struct {
	Transport   Transport
	OpenTimeout time.Duration
	IdleTimeout time.Duration
}

// Rational 表示有理数（分子 / 分母）。
type Rational struct {
	Num int32
	Den int32
}

func (r Rational) Float64() float64 {
	if r.Den == 0 {
		return 0
	}
	return float64(r.Num) / float64(r.Den)
}

func (r Rational) String() string {
	return fmt.Sprintf("%d/%d", r.Num, r.Den)
}

// VideoInfo 描述视频流探测获取的元数据。
type VideoInfo struct {
	Codec     Codec
	Width     int
	Height    int
	FPS       Rational
	TimeBase  Rational
	ExtraData []byte // Go-owned 副本，非底层 C 借用指针
}

// Packet 封装一帧视频编码压缩包。
type Packet struct {
	Codec    Codec
	PTS      int64
	DTS      int64
	HasPTS   bool
	HasDTS   bool
	TimeBase Rational
	KeyFrame bool
	Data     []byte // Go-owned 副本

	// PtsUS 是显示时间戳微秒值；保留 B 帧重排，不保证按包序单调（ABI v3 起提供）。
	// 预览路径应只消费此字段；源流 time_base 与重连时间轴已在原生层归一化。
	PtsUS int64
	// DtsUS 是按包序单调的解码时间戳微秒值；若由原生层合成，由 PacketFlagDtsSynthetic 标记。
	DtsUS int64
	// FrameClass 标注本帧在参考链中的角色，决定接收方能否安全丢弃。
	FrameClass FrameClass
	// GOPID 每个关键帧或时间轴跳变自增；跨值即代表可以重建解码器。
	GOPID uint32
	// Flags 携带参数集注入、时间轴不连续等属性。
	Flags PacketFlag
}

// RequiresKeyframeResync 报告该包是否要求接收方丢弃当前解码状态并从关键帧重建。
func (p Packet) RequiresKeyframeResync() bool {
	return p.Flags.Has(PacketFlagDiscontinuity)
}

// StreamStatus 描述流的当前状态与错误。
type StreamStatus struct {
	State StreamState
	Error error
}

// SubscriptionOptions 定义包订阅选项与有界队列限制。
type SubscriptionOptions struct {
	MaxPackets       int // 队列最大包数（默认 32，上限 256）
	MaxPacketBytes   int // 单包最大字节数（默认 4 MiB，上限 16 MiB）
	MaxBufferedBytes int // 队列最大总字节数（默认 16 MiB，上限 64 MiB）
}

const (
	DefaultMaxPackets         = 32
	HardLimitMaxPackets       = 256
	DefaultMaxPacketBytes     = 4 * 1024 * 1024
	HardLimitMaxPacketBytes   = 16 * 1024 * 1024
	DefaultMaxBufferedBytes   = 16 * 1024 * 1024
	HardLimitMaxBufferedBytes = 64 * 1024 * 1024
	MaxExtraDataBytes         = 1024 * 1024 // 1 MiB 上限
	MaxFaceImageDimension     = 4096        // 4096 像素单边上限，防止解压炸弹 OOM
)
