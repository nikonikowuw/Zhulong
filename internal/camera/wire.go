package camera

import (
	"encoding/binary"
	"errors"

	"github.com/nikonikowuw/Zhulong/internal/engine"
)

// ZLM2 帧头布局（全部大端序，共 32 字节）：
//
//	偏移  长度  字段
//	 0     4    magic      = 0x5A4C4D32 ("ZLM2")
//	 4     1    version    = 2
//	 5     1    codec      0x01=H.264, 0x02=H.265
//	 6     1    frame_class Zhulong_FRAME_* 取值
//	 7     1    flags      WireFlag* 位组合
//	 8     8    pts_us     显示时间戳微秒值，保留 B 帧重排，不保证按包序单调
//	16     8    dts_us     解码时间戳微秒值，按包序单调（缺失时合成）
//	24     4    seq        流级帧序号，逐包递增，接收方可据此统计真实丢帧
//	28     4    gop_id     关键帧序号，变化即代表可以重建解码器
//
// 与 ZLM1 的差别是将源时基归一到微秒；PTS 保留显示顺序，DTS 保留解码顺序。
// 前端不再猜源时基，也不会把 B 帧 PTS 当作递增序号。
const (
	// WireMagicZLM2 是 ZLM2 协议的 4 字节魔数（ASCII "ZLM2"）。
	WireMagicZLM2 uint32 = 0x5A4C4D32

	// WireHeaderSize 是 ZLM2 二进制固定帧头长度。
	WireHeaderSize = 32

	// WireProtocolVersion 是当前协议版本。
	WireProtocolVersion byte = 2
)

// WireCodec 编码标识。
const (
	WireCodecUnknown byte = 0x00
	WireCodecH264    byte = 0x01
	WireCodecH265    byte = 0x02
)

// WireFrameClass 与 Native Zhulong_FRAME_* 及 Go engine.FrameClass 一一对应。
//
// 接收方只可无条件丢弃 WireFrameNonReference；丢弃 WireFramePredictive
// 会破坏后续帧的参考链，必须整段丢到下一个 IDR。
const (
	WireFrameUnknown      byte = 0
	WireFrameIDR          byte = 1
	WireFramePredictive   byte = 2
	WireFrameNonReference byte = 3
)

// WireFlag 标志位掩码。
const (
	// WireFlagKeyFrame 冗余标注关键帧，便于与 frame_class 交叉校验。
	WireFlagKeyFrame byte = 0x01
	// WireFlagDiscontinuity 源流时间轴跳变，接收方必须重置解码器。
	WireFlagDiscontinuity byte = 0x02
	// WireFlagParamInjected 服务端已为本帧前置注入 VPS/SPS/PPS。
	WireFlagParamInjected byte = 0x04
	// WireFlagTimestampSynthetic PTS 由 DTS 合成（源流缺失 PTS）。
	WireFlagTimestampSynthetic byte = 0x08
	// WireFlagDtsSynthetic DTS 由原生层合成（源流缺失 DTS）。
	WireFlagDtsSynthetic byte = 0x10
)

var (
	ErrWireBufferTooShort     = errors.New("wire buffer too short")
	ErrWireInvalidMagic       = errors.New("invalid wire magic")
	ErrWireUnsupportedVersion = errors.New("unsupported wire protocol version")
)

// WireHeader 描述 ZLM2 帧头。
type WireHeader struct {
	Version    byte
	Codec      byte
	FrameClass byte
	Flags      byte
	PTSUS      int64
	DTSUS      int64
	Seq        uint32
	GOPID      uint32
}

// IsKeyFrame 返回是否为关键帧。
func (h WireHeader) IsKeyFrame() bool {
	return h.Flags&WireFlagKeyFrame != 0 || h.FrameClass == WireFrameIDR
}

// HasDiscontinuity 返回源流时间轴是否发生跳变。
func (h WireHeader) HasDiscontinuity() bool {
	return h.Flags&WireFlagDiscontinuity != 0
}

// packFrame 同时返回封装后的字节与解析出的帧头。
//
// 分发热路径需要帧头的 PTS / 帧分类 / GOP 序号做背压判定。若先 PackPacket
// 再 UnpackPacket，每帧会白做一次往返解析；这里直接复用同一份计算值。
func packFrame(pkt engine.Packet, seq uint32) ([]byte, WireHeader) {
	header := WireHeader{
		Version:    WireProtocolVersion,
		Codec:      wireCodec(pkt.Codec),
		FrameClass: wireFrameClass(pkt.FrameClass, pkt.KeyFrame),
		Flags:      wireFlags(pkt.Flags, pkt.KeyFrame),
		PTSUS:      pkt.PTSUS,
		DTSUS:      pkt.DTSUS,
		Seq:        seq,
		GOPID:      pkt.GOPID,
	}

	buf := make([]byte, WireHeaderSize+len(pkt.Data))
	binary.BigEndian.PutUint32(buf[0:4], WireMagicZLM2)
	buf[4] = header.Version
	buf[5] = header.Codec
	buf[6] = header.FrameClass
	buf[7] = header.Flags
	binary.BigEndian.PutUint64(buf[8:16], uint64(header.PTSUS))
	binary.BigEndian.PutUint64(buf[16:24], uint64(header.DTSUS))
	binary.BigEndian.PutUint32(buf[24:28], header.Seq)
	binary.BigEndian.PutUint32(buf[28:32], header.GOPID)
	if len(pkt.Data) > 0 {
		copy(buf[WireHeaderSize:], pkt.Data)
	}
	return buf, header
}

// PackPacket 将一帧 engine.Packet 封包为 [32 字节 WireHeader] + [Annex B Payload]。
//
// seq 是流级帧序号（由 StreamDispatcher 维护），用于接收方统计真实丢帧率；
// 传 0 表示调用方不追踪序号（仅测试场景）。
func PackPacket(pkt engine.Packet, seq uint32) []byte {
	buf, _ := packFrame(pkt, seq)
	return buf
}

func wireCodec(codec engine.Codec) byte {
	switch codec {
	case engine.CodecH264:
		return WireCodecH264
	case engine.CodecH265:
		return WireCodecH265
	default:
		return WireCodecUnknown
	}
}

func wireFrameClass(class engine.FrameClass, keyFrame bool) byte {
	switch class {
	case engine.FrameIDR:
		return WireFrameIDR
	case engine.FrameNonReference:
		return WireFrameNonReference
	case engine.FramePredictive:
		return WireFramePredictive
	default:
		// 旧版原生库不填 frame_class：退回按 key_frame 判定，
		// 其余一律视为参考帧（保守，绝不把不可丢的帧标成可丢）
		if keyFrame {
			return WireFrameIDR
		}
		return WireFramePredictive
	}
}

func wireFlags(flags engine.PacketFlag, keyFrame bool) byte {
	var out byte
	if keyFrame {
		out |= WireFlagKeyFrame
	}
	if flags.Has(engine.PacketFlagDiscontinuity) {
		out |= WireFlagDiscontinuity
	}
	if flags.Has(engine.PacketFlagParameterSetsInjected) {
		out |= WireFlagParamInjected
	}
	if flags.Has(engine.PacketFlagTimestampSynthetic) {
		out |= WireFlagTimestampSynthetic
	}
	if flags.Has(engine.PacketFlagDtsSynthetic) {
		out |= WireFlagDtsSynthetic
	}
	return out
}

// UnpackPacket 解析 ZLM2 二进制推流消息，返回头部与 Payload 切片。
//
// Payload 是输入缓冲的零拷贝视图，与传入切片共享底层数组。
func UnpackPacket(data []byte) (WireHeader, []byte, error) {
	if len(data) < WireHeaderSize {
		return WireHeader{}, nil, ErrWireBufferTooShort
	}
	if magic := binary.BigEndian.Uint32(data[0:4]); magic != WireMagicZLM2 {
		return WireHeader{}, nil, ErrWireInvalidMagic
	}
	if version := data[4]; version != WireProtocolVersion {
		return WireHeader{}, nil, ErrWireUnsupportedVersion
	}

	h := WireHeader{
		Version:    data[4],
		Codec:      data[5],
		FrameClass: data[6],
		Flags:      data[7],
		PTSUS:      int64(binary.BigEndian.Uint64(data[8:16])),
		DTSUS:      int64(binary.BigEndian.Uint64(data[16:24])),
		Seq:        binary.BigEndian.Uint32(data[24:28]),
		GOPID:      binary.BigEndian.Uint32(data[28:32]),
	}
	return h, data[WireHeaderSize:], nil
}
