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
)
