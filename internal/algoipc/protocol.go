package algoipc

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	// IpcMagic matches native IPC_MAGIC ('ZHLG' = 0x5A484C47).
	IpcMagic uint32 = 0x5A484C47
	// IpcVersion matches native IPC_VERSION = 1.
	IpcVersion uint16 = 1

	// MaxIpcPayloadSize mirrors native ipc_protocol.hpp's MAX_IPC_PAYLOAD_SIZE.
	// Both the send path (EncodeIpcMessage) and the receive path (DecodeIpcHeader)
	// must reject payloads beyond this limit.
	MaxIpcPayloadSize = 64 * 1024

	// FailureStageBrokerConnect marks a runner that exited because it could not
	// connect to at least one frame broker socket.
	FailureStageBrokerConnect = "connect"

	// RunnerExitBrokerUnavailable is the runner's exit code for a failed broker connect.
	RunnerExitBrokerUnavailable = 5

	// MsgResult is the inference result egress message type (Runner -> Broker/Go).
	MsgResult uint16 = 7

	// Gallery IPC Message Types (synchronized with native MessageType in ipc_protocol.hpp).
	MsgGallerySync   uint16 = 10
	MsgGalleryUpsert uint16 = 11
	MsgGalleryDelete uint16 = 12
	MsgGalleryAck    uint16 = 13
)

// ErrIpcPayloadTooLarge is returned when a payload exceeds MaxIpcPayloadSize.
var ErrIpcPayloadTooLarge = errors.New("IPC payload exceeds the 64KB protocol limit")

// IpcHeader models the 16-byte fixed-size IPC header.
type IpcHeader struct {
	Magic       uint32
	MsgType     uint16
	Version     uint16
	PayloadSize uint32
	Sequence    uint32
}

// EncodeIpcMessage frames a payload with a 16-byte binary header.
// It returns ErrIpcPayloadTooLarge if the payload exceeds MaxIpcPayloadSize.
func EncodeIpcMessage(msgType uint16, seq uint32, payload []byte) ([]byte, error) {
	if len(payload) > MaxIpcPayloadSize {
		return nil, fmt.Errorf("%w: got %d bytes", ErrIpcPayloadTooLarge, len(payload))
	}
	buf := make([]byte, 16+len(payload))
	binary.LittleEndian.PutUint32(buf[0:4], IpcMagic)
	binary.LittleEndian.PutUint16(buf[4:6], msgType)
	binary.LittleEndian.PutUint16(buf[6:8], IpcVersion)
	binary.LittleEndian.PutUint32(buf[8:12], uint32(len(payload)))
	binary.LittleEndian.PutUint32(buf[12:16], seq)
	if len(payload) > 0 {
		copy(buf[16:], payload)
	}
	return buf, nil
}

// DecodeIpcHeader parses a 16-byte header from a byte slice.
func DecodeIpcHeader(data []byte) (*IpcHeader, error) {
	if len(data) < 16 {
		return nil, errors.New("insufficient data for IPC header")
	}
	hdr := &IpcHeader{
		Magic:       binary.LittleEndian.Uint32(data[0:4]),
		MsgType:     binary.LittleEndian.Uint16(data[4:6]),
		Version:     binary.LittleEndian.Uint16(data[6:8]),
		PayloadSize: binary.LittleEndian.Uint32(data[8:12]),
		Sequence:    binary.LittleEndian.Uint32(data[12:16]),
	}
	if hdr.Magic != IpcMagic {
		return nil, fmt.Errorf("invalid magic 0x%X (expected 0x%X)", hdr.Magic, IpcMagic)
	}
	if hdr.Version != IpcVersion {
		return nil, fmt.Errorf("unsupported IPC version %d (expected %d)", hdr.Version, IpcVersion)
	}
	if hdr.PayloadSize > MaxIpcPayloadSize {
		return nil, fmt.Errorf("%w: declared size %d", ErrIpcPayloadTooLarge, hdr.PayloadSize)
	}
	return hdr, nil
}
