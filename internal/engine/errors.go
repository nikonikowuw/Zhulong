package engine

import (
	"errors"
	"fmt"
)

// StatusCode 定义 Native 与 Go 桥接状态与错误码。
type StatusCode int32

const (
	StatusOK              StatusCode = 0
	StatusInvalidArgument StatusCode = -1
	StatusOutOfMemory     StatusCode = -2
	StatusUnsupported     StatusCode = -3
	StatusNotFound        StatusCode = -4
	StatusDuplicate       StatusCode = -5
	StatusConfigConflict  StatusCode = -6
	StatusTimeout         StatusCode = -7
	StatusCallbackContext StatusCode = -8
	StatusNotRunning      StatusCode = -9
	StatusBusy            StatusCode = -10
	StatusIO              StatusCode = -11
	StatusCancelled       StatusCode = -12
	StatusEOF             StatusCode = -13
	StatusBufferTooSmall  StatusCode = -14
	StatusInternal        StatusCode = -99

	// Go 桥接特有状态码
	StatusClosed            StatusCode = -100
	StatusBackpressure      StatusCode = -101
	StatusPacketTooLarge    StatusCode = -102
	StatusStaleResource     StatusCode = -103
	StatusResourceExhausted StatusCode = -104
)

// NativeError 结构化错误表示。
// ⚠️ 保证不会包含原始 RTSP URL、密码凭据或未脱敏底层 FFmpeg 字符串。
type NativeError struct {
	Op      string
	Code    StatusCode
	Raw     int32
	Message string
}

func (e *NativeError) Error() string {
	// Op 为空表示这是哨兵值被 fmt.Errorf 直接包装（如 `%w: detail`），
	// 而不是某次原生调用的返回。此时不能输出空占位，否则日志里会出现
	// 「native engine  failed:」这样的双空格。
	if e.Op == "" {
		if e.Message != "" {
			return fmt.Sprintf("native engine failed: %s (code %d)", e.Message, e.Code)
		}
		return fmt.Sprintf("native engine failed with status %d", e.Code)
	}
	if e.Message != "" {
		return fmt.Sprintf("native engine %s failed: %s (code %d)", e.Op, e.Message, e.Code)
	}
	return fmt.Sprintf("native engine %s failed with status %d", e.Op, e.Code)
}

func (e *NativeError) Is(target error) bool {
	var te *NativeError
	if errors.As(target, &te) {
		return e.Code == te.Code
	}
	return false
}

var (
	ErrClosed            = &NativeError{Code: StatusClosed, Message: "engine or resource is closed"}
	ErrBackpressure      = &NativeError{Code: StatusBackpressure, Message: "subscription queue backpressure limit exceeded"}
	ErrPacketTooLarge    = &NativeError{Code: StatusPacketTooLarge, Message: "packet exceeds maximum allowed size"}
	ErrStaleResource     = &NativeError{Code: StatusStaleResource, Message: "operation attempted on stale resource generation"}
	ErrResourceExhausted = &NativeError{Code: StatusResourceExhausted, Message: "probe concurrency limit reached"}
	ErrInvalidArgument   = &NativeError{Code: StatusInvalidArgument, Message: "invalid argument"}
	ErrTimeout           = &NativeError{Code: StatusTimeout, Message: "operation timed out"}
	ErrCancelled         = &NativeError{Code: StatusCancelled, Message: "operation cancelled"}
	ErrNotRunning        = &NativeError{Code: StatusNotRunning, Message: "engine is not running"}
	ErrDuplicate         = &NativeError{Code: StatusDuplicate, Message: "duplicate consumer or resource"}
	ErrConfigConflict    = &NativeError{Code: StatusConfigConflict, Message: "stream configuration conflict"}
	ErrBusy              = &NativeError{Code: StatusBusy, Message: "resource is busy"}
	ErrNotFound          = &NativeError{Code: StatusNotFound, Message: "stream or consumer not found"}
	ErrUnsupported       = &NativeError{Code: StatusUnsupported, Message: "unsupported codec or format"}
	ErrIO                = &NativeError{Code: StatusIO, Message: "native I/O failure"}
	ErrOutOfMemory       = &NativeError{Code: StatusOutOfMemory, Message: "out of memory"}
	ErrBufferTooSmall    = &NativeError{Code: StatusBufferTooSmall, Message: "buffer too small"}
	ErrInternal          = &NativeError{Code: StatusInternal, Message: "internal engine error"}
)

// ErrUnsupportedImageFormat 表示调用方提供的图片字节流无法被解码（格式不受支持或文件损坏）。
//
// ⚠️ 它与 StatusUnsupported（原生能力缺失，如 CPU 桩后端没有人脸识别能力）是**两类**问题：
// 本错误归咎于上传的输入，HTTP 层应回答 4xx；StatusUnsupported 是服务端能力问题，仍是 5xx。
var ErrUnsupportedImageFormat = errors.New("unsupported image format")

func mapNativeStatus(op string, raw int32) error {
	code := StatusCode(raw)
	switch code {
	case StatusOK:
		return nil
	case StatusInvalidArgument:
		return &NativeError{Op: op, Code: code, Raw: raw, Message: "invalid argument"}
	case StatusOutOfMemory:
		return &NativeError{Op: op, Code: code, Raw: raw, Message: "out of memory"}
	case StatusUnsupported:
		return &NativeError{Op: op, Code: code, Raw: raw, Message: "unsupported codec or format"}
	case StatusNotFound:
		return &NativeError{Op: op, Code: code, Raw: raw, Message: "resource not found"}
	case StatusDuplicate:
		return &NativeError{Op: op, Code: code, Raw: raw, Message: "duplicate resource or consumer"}
	case StatusConfigConflict:
		return &NativeError{Op: op, Code: code, Raw: raw, Message: "stream configuration conflict"}
	case StatusTimeout:
		return &NativeError{Op: op, Code: code, Raw: raw, Message: "operation timed out"}
	case StatusCallbackContext:
		return &NativeError{Op: op, Code: code, Raw: raw, Message: "cannot call control API inside callback context"}
	case StatusNotRunning:
		return &NativeError{Op: op, Code: code, Raw: raw, Message: "engine is not running"}
	case StatusBusy:
		return &NativeError{Op: op, Code: code, Raw: raw, Message: "resource is busy"}
	case StatusIO:
		return &NativeError{Op: op, Code: code, Raw: raw, Message: "I/O communication error"}
	case StatusCancelled:
		return &NativeError{Op: op, Code: code, Raw: raw, Message: "operation cancelled"}
	case StatusEOF:
		return &NativeError{Op: op, Code: code, Raw: raw, Message: "end of stream"}
	case StatusBufferTooSmall:
		return &NativeError{Op: op, Code: code, Raw: raw, Message: "buffer too small"}
	case StatusInternal:
		return &NativeError{Op: op, Code: code, Raw: raw, Message: "internal error"}
	default:
		return &NativeError{Op: op, Code: code, Raw: raw, Message: "unknown error"}
	}
}
