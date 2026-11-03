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

// Consumer 标识一个流消费者：唯一 ID 与用途类型。
//
// 两者必须成对合法（ID 非零且 Kind 已知），因此作为整体在拉流接口与校验之间传递，
// 而不是拆成两个平行参数 —— 那样下一次漏传一个就是静默的跨流串用。
type Consumer struct {
	ID   uint64
	Kind ConsumerKind
}

// Codec 定义媒体编码格式（视频或音频）。
//
// 视频与音频共用同一枚举：取值空间由 C ABI 的 Zhulong_CODEC_* 决定，互不重叠，
// 轨道语义由 Packet.TrackType 给出。拆成两个类型会让「同一字段在不同轨道上
// 是不同 Go 类型」这件事渗进所有下游代码，而它们本来只需要透传。
type Codec int32

const (
	CodecH264 Codec = 1
	CodecH265 Codec = 2
	CodecAAC  Codec = 16
	CodecPCMA Codec = 17
	CodecPCMU Codec = 18
)

func (c Codec) String() string {
	switch c {
	case CodecH264:
		return "h264"
	case CodecH265:
		return "h265"
	case CodecAAC:
		return "aac"
	case CodecPCMA:
		return "pcma"
	case CodecPCMU:
		return "pcmu"
	default:
		return fmt.Sprintf("codec(%d)", int32(c))
	}
}

// IsAudio 报告该编码是否属于音频轨。
func (c Codec) IsAudio() bool {
	switch c {
	case CodecAAC, CodecPCMA, CodecPCMU:
		return true
	default:
		return false
	}
}

// TrackType 标识数据包所属轨道，与 C ABI 的 ZHULONG_TRACK_* 一一对应。
type TrackType uint32

const (
	TrackVideo TrackType = 0
	TrackAudio TrackType = 1
)

func (t TrackType) String() string {
	switch t {
	case TrackVideo:
		return "video"
	case TrackAudio:
		return "audio"
	default:
		return fmt.Sprintf("track(%d)", uint32(t))
	}
}

// TrackMask 是订阅轨道掩码，与 C ABI 的 ZHULONG_TRACK_MASK_* 一一对应。
type TrackMask uint32

const (
	TrackMaskVideo TrackMask = 1 << 0
	TrackMaskAudio TrackMask = 1 << 1
)

// Has 报告掩码是否包含指定轨道。
func (m TrackMask) Has(track TrackMask) bool {
	return m&track != 0
}

// Accepts 报告该掩码是否接收指定轨道的数据包。
//
// 零值按「仅视频」处理：这是 ABI v4 的既有语义，也是 SubscriptionOptions 的默认值，
// 若把零值解释成「什么都不收」，所有未显式传掩码的老代码都会静默收不到包。
func (m TrackMask) Accepts(track TrackType) bool {
	if m == 0 {
		m = TrackMaskVideo
	}
	switch track {
	case TrackVideo:
		return m.Has(TrackMaskVideo)
	case TrackAudio:
		return m.Has(TrackMaskAudio)
	default:
		return false
	}
}

// AudioInfo 描述一路物理流的音频元数据。
//
// 这些是**流级常量**，不随包变化：每包都带一份采样率只会让热路径白白变大，
// 因此它们由 Engine 一次性查询得到，而不是塞进 Packet。
// Config 是 Go 拥有的副本（AAC 为 AudioSpecificConfig，G.711 为空）。
type AudioInfo struct {
	Config     []byte
	Codec      Codec
	SampleRate int
	Channels   int
	HasAudio   bool
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

// MaxExtraDataBytes 是探测结果 extradata（VPS/SPS/PPS 等参数集）允许的最大字节数。
const MaxExtraDataBytes = 1024 * 1024 // 1 MiB 上限

// VideoInfo 描述视频流探测获取的元数据。
type VideoInfo struct {
	Codec     Codec
	Width     int
	Height    int
	FPS       Rational
	TimeBase  Rational
	ExtraData []byte // Go-owned 副本，非底层 C 借用指针
}

// Packet 封装一帧编码压缩包（视频或音频轨）。
type Packet struct {
	// TrackType 标识轨道；音频包只带 PtsUS/DtsUS 与编解码，帧分类与 GOP 恒为零值。
	TrackType TrackType
	Codec     Codec
	PTS       int64
	DTS       int64
	HasPTS    bool
	HasDTS    bool
	TimeBase  Rational
	KeyFrame  bool
	Data      []byte // Go-owned 副本

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

// BrokerClientStats 是单个算法 Runner 客户端的帧投递统计快照（FrameBroker 视角）。
//
// Name 来自 Runner 握手时上报的 `--name`，即算法包名；拿它就能直接判断
// 是哪一路算法在拖、丢了多少帧，而不必去猜 CPU 占用。
type BrokerClientStats struct {
	ClientID        uint32 // Broker 分配的客户端 ID（仅单次 Broker 生命周期内唯一）
	Name            string // 握手上报的客户端名，未命名时为空串
	InFlight        uint32 // 当前已下发但尚未 ACK 的帧数（瞬时值）
	MaxInFlight     uint32 // 该客户端的在途配额上限（瞬时值，即背压阈值）
	DeliveredFrames uint64 // 累计成功投递帧数
	DroppedFrames   uint64 // 累计因背压/发送失败丢弃的帧数
}

// SubscriptionOptions 定义包订阅选项与有界队列限制。
type SubscriptionOptions struct {
	MaxPackets       int       // 队列最大包数（默认 32，上限 256）
	MaxPacketBytes   int       // 单包最大字节数（默认 4 MiB，上限 16 MiB）
	MaxBufferedBytes int       // 队列最大总字节数（默认 16 MiB，上限 64 MiB）
	TrackMask        TrackMask // 订阅轨道掩码；零值代表仅视频（与 ABI v4 语义一致）
}

const (
	DefaultMaxPackets         = 32
	HardLimitMaxPackets       = 256
	DefaultMaxPacketBytes     = 4 * 1024 * 1024
	HardLimitMaxPacketBytes   = 16 * 1024 * 1024
	DefaultMaxBufferedBytes   = 16 * 1024 * 1024
	HardLimitMaxBufferedBytes = 64 * 1024 * 1024
)
