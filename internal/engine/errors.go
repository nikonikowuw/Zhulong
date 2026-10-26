package engine

import (
	"errors"
	"fmt"
)

// StatusCode 定义 Native 与 Go 桥接状态与错误码。
//
// 负数区间是 ABI 契约（与 native/include/Zhulong/engine.h 的 Zhulong_ERR_* 一一对应）；
// -100 以下为 Go 桥接私有码，绝不跨越 ABI 边界。
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
	StatusClosed              StatusCode = -100
	StatusBackpressure        StatusCode = -101
	StatusPacketTooLarge      StatusCode = -102
	StatusStaleResource       StatusCode = -103
	StatusSnapshotUnavailable StatusCode = -104
)

// statusMessages 是状态码文案的单一事实来源。
//
// 新增状态码只需在这里补一行：运行时错误（mapNativeStatus）与包级哨兵错误
// 共用同一份文案，避免两处措辞漂移；每个哨兵码必须唯一，
// 否则 errors.Is 会在不相干的两类失败之间互相为真。
var statusMessages = map[StatusCode]string{
	StatusOK:              "ok",
	StatusInvalidArgument: "invalid argument",
	StatusOutOfMemory:     "out of memory",
	StatusUnsupported:     "unsupported codec or format",
	StatusNotFound:        "resource not found",
	StatusDuplicate:       "duplicate resource or consumer",
	StatusConfigConflict:  "stream configuration conflict",
	StatusTimeout:         "operation timed out",
	StatusCallbackContext: "cannot call control API inside callback context",
	StatusNotRunning:      "engine is not running",
	StatusBusy:            "resource is busy",
	StatusIO:              "I/O communication error",
	StatusCancelled:       "operation cancelled",
	StatusEOF:             "end of stream",
	StatusBufferTooSmall:  "buffer too small",
	StatusInternal:        "internal engine error",

	StatusClosed:              "engine or resource is closed",
	StatusBackpressure:        "subscription queue backpressure limit exceeded",
	StatusPacketTooLarge:      "packet exceeds maximum allowed size",
	StatusStaleResource:       "operation attempted on stale resource generation",
	StatusSnapshotUnavailable: "jpeg encoder is not available in this build",
}

// statusMessage 返回状态码的稳定文案；未知码统一收敛，避免把原始数值当文案。
func statusMessage(code StatusCode) string {
	if message, ok := statusMessages[code]; ok {
		return message
	}
	return "unknown error"
}

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

// Is 按状态码判等：调用点用 errors.Is(err, ErrXxx) 匹配被 %w 包装过的原生错误。
//
// ⚠️ 判等只看 Code，因此每个哨兵必须独占一个状态码。
func (e *NativeError) Is(target error) bool {
	var te *NativeError
	if errors.As(target, &te) {
		return e.Code == te.Code
	}
	return false
}

// sentinel 构造包级哨兵错误，文案取自 statusMessages。
func sentinel(code StatusCode) *NativeError {
	return &NativeError{Code: code, Message: statusMessage(code)}
}

var (
	ErrClosed          = sentinel(StatusClosed)
	ErrBackpressure    = sentinel(StatusBackpressure)
	ErrPacketTooLarge  = sentinel(StatusPacketTooLarge)
	ErrStaleResource   = sentinel(StatusStaleResource)
	ErrInvalidArgument = sentinel(StatusInvalidArgument)
	ErrTimeout         = sentinel(StatusTimeout)
	ErrCancelled       = sentinel(StatusCancelled)
	ErrNotRunning      = sentinel(StatusNotRunning)
	ErrDuplicate       = sentinel(StatusDuplicate)
	ErrConfigConflict  = sentinel(StatusConfigConflict)
	ErrBusy            = sentinel(StatusBusy)
	ErrNotFound        = sentinel(StatusNotFound)
	ErrUnsupported     = sentinel(StatusUnsupported)
	ErrIO              = sentinel(StatusIO)
	ErrOutOfMemory     = sentinel(StatusOutOfMemory)
	ErrBufferTooSmall  = sentinel(StatusBufferTooSmall)
	ErrInternal        = sentinel(StatusInternal)
)

// ErrABIMismatch 表示 Go 侧编译所用头文件与原生静态库的 ABI 版本不一致。
//
// 这是构建期错配而非运行时故障：Make 与 wrapper 已对 Engine 归档做内容寻址，
// 但交叉产物、遗留 build 目录或手工链接仍可能把新头文件与旧归档混在一起。
// 这种错配在链接期不会报错，只会让 cgo 按错误的结构体偏移读内存 ——
// 表现为随机崩溃或莫名其妙的字段值，排查成本极高。宁可启动直接拒绝。
var ErrABIMismatch = errors.New("native engine ABI version mismatch")

// nativeCallbackPanic 把 CGO 回调里的 panic 收敛成可上报的错误。
//
// 回调内 panic 说明 Go 侧违反了自己的契约（越界、空指针、句柄误用）。
// 异常不能穿越 ABI，但也绝不能被 recover 静默吞掉 —— 那会让「偶发丢帧」
// 变成没有日志、没有错误码的幽灵问题。这里把它转成订阅终态。
func nativeCallbackPanic(op string, recovered any) error {
	return &NativeError{
		Op:      op,
		Code:    StatusInternal,
		Raw:     int32(StatusInternal),
		Message: fmt.Sprintf("callback panicked: %v", recovered),
	}
}

// mapNativeStatus 把原生状态码映射为结构化错误；OK 返回 nil。
func mapNativeStatus(op string, raw int32) error {
	if raw == int32(StatusOK) {
		return nil
	}
	code := StatusCode(raw)
	return &NativeError{
		Op:      op,
		Code:    code,
		Raw:     raw,
		Message: statusMessage(code),
	}
}
