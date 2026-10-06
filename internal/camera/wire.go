package camera

import (
	"encoding/binary"
	"errors"

	"github.com/nikonikowuw/Zhulong/internal/engine"
)

const (
	// WireMagic 是 ZLM1 协议的 4 字节魔数 (ASCII "ZLM1")。
	WireMagic uint32 = 0x5A4C4D31

	// WireHeaderSize 是 ZLM1 二进制固定帧头长度（24 字节）。
	WireHeaderSize = 24

	// WireCodec 编码标识。
	WireCodecUnknown byte = 0x00
	WireCodecH264    byte = 0x01
	WireCodecH265    byte = 0x02

	// WireFlag 标志位掩码。
	WireFlagKeyFrame byte = 0x01
	WireFlagHasPTS   byte = 0x02
	WireFlagHasDTS   byte = 0x04
)

var (
	ErrWireBufferTooShort = errors.New("wire buffer too short")
	ErrWireInvalidMagic   = errors.New("invalid wire magic")
)

// WireHeader 描述 24 字节大端序二进制帧头。
type WireHeader struct {
	Codec    byte
	Flags    byte
	Reserved uint16
	PTS      int64
	DTS      int64
}

// IsKeyFrame 返回是否为关键帧。
func (h WireHeader) IsKeyFrame() bool {
	return (h.Flags & WireFlagKeyFrame) != 0
}

// HasPTS 返回 PTS 是否有效。
func (h WireHeader) HasPTS() bool {
	return (h.Flags & WireFlagHasPTS) != 0
}

// HasDTS 返回 DTS 是否有效。
func (h WireHeader) HasDTS() bool {
	return (h.Flags & WireFlagHasDTS) != 0
}

// PackPacket 将一帧 engine.Packet 封包为 [24 字节 WireHeader] + [Annex B Payload] 的单一内存切片。
func PackPacket(pkt engine.Packet) []byte {
	buf := make([]byte, WireHeaderSize+len(pkt.Data))
	binary.BigEndian.PutUint32(buf[0:4], WireMagic)

	var codecByte byte
	switch pkt.Codec {
	case engine.CodecH264:
		codecByte = WireCodecH264
	case engine.CodecH265:
		codecByte = WireCodecH265
	default:
		codecByte = WireCodecUnknown
	}
	buf[4] = codecByte

	var flags byte
	if pkt.KeyFrame {
		flags |= WireFlagKeyFrame
	}
	if pkt.HasPTS {
		flags |= WireFlagHasPTS
	}
	if pkt.HasDTS {
		flags |= WireFlagHasDTS
	}
	buf[5] = flags

	binary.BigEndian.PutUint16(buf[6:8], 0)
	binary.BigEndian.PutUint64(buf[8:16], uint64(pkt.PTS))
	binary.BigEndian.PutUint64(buf[16:24], uint64(pkt.DTS))

	if len(pkt.Data) > 0 {
		copy(buf[24:], pkt.Data)
	}

	return buf
}

// UnpackPacket 解析二进制推流消息，返回头部与 Payload 切片。
func UnpackPacket(data []byte) (WireHeader, []byte, error) {
	if len(data) < WireHeaderSize {
		return WireHeader{}, nil, ErrWireBufferTooShort
	}

	magic := binary.BigEndian.Uint32(data[0:4])
	if magic != WireMagic {
		return WireHeader{}, nil, ErrWireInvalidMagic
	}

	h := WireHeader{
		Codec:    data[4],
		Flags:    data[5],
		Reserved: binary.BigEndian.Uint16(data[6:8]),
		PTS:      int64(binary.BigEndian.Uint64(data[8:16])),
		DTS:      int64(binary.BigEndian.Uint64(data[16:24])),
	}

	return h, data[WireHeaderSize:], nil
}
