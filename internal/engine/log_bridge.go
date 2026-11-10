package engine

/*
#cgo CFLAGS: -I${SRCDIR}/../../native/include
#include <stdint.h>
#include <stddef.h>
#include <stdlib.h>
#include <Zhulong/engine.h>

void zhulongLogTrampoline(uintptr_t token, int32_t level, uint64_t stream_id,
    char *module, size_t module_len, char *text, size_t text_len);
void zhulongLogCallbackBridge(uintptr_t token, int32_t level, uint64_t stream_id,
    const char *module, size_t module_len, const char *text, size_t text_len);

// 测试接缝：由 log_bridge_test.go 与基准调用。
// ⚠️ 这两个封装只能留在生产文件里（Go 不支持在 _test.go 中 use cgo）；
// 它们不在任何运行时路径上，也不含业务逻辑。
void zhulongInvokeLogCallback(uintptr_t token, int32_t level, uint64_t stream_id,
    const char *module, size_t module_len, const char *text, size_t text_len);
void zhulongInvokeStaticLogCallback(uintptr_t token);
*/
import "C"

import (
	"runtime/cgo"
	"time"
	"unicode/utf8"
	"unsafe"

	"github.com/nikonikowuw/Zhulong/internal/nativelog"
	"go.uber.org/zap/zapcore"
)

// 原生日志载荷的硬上限：超出即判定为异常调用，只计数丢弃，不做任何分配。
const (
	maxLogModuleBytes = 128
	maxLogTextBytes   = 2048
)

// nativeSeverities 是 C 侧 Zhulong_LOG_*（0..3）到两套 Go 枚举的显式映射。
//
// C 枚举、zapcore.Level 与 nativelog.Level 的数值区间互不相干
// （zap 的 DebugLevel 是 -1），因此禁止任何直接类型转换。
var nativeSeverities = [...]struct {
	zap    zapcore.Level
	native nativelog.Level
}{
	{zap: zapcore.DebugLevel, native: nativelog.Debug},
	{zap: zapcore.InfoLevel, native: nativelog.Info},
	{zap: zapcore.WarnLevel, native: nativelog.Warn},
	{zap: zapcore.ErrorLevel, native: nativelog.Error},
}

// severity 把 C 侧等级映射成两套 Go 枚举；越界值收敛到 INFO。
//
// 两套枚举必须同进同出：分开各写一份区间检查，早晚会出现「zap 记 debug、nativelog 记 info」的漂移。
func severity(level int32) (zapcore.Level, nativelog.Level) {
	if level < 0 || int(level) >= len(nativeSeverities) {
		return zapcore.InfoLevel, nativelog.Info
	}
	return nativeSeverities[level].zap, nativeSeverities[level].native
}

// zapLevel 把 C 侧等级映射为 zap 等级；越界值收敛到 INFO。
func zapLevel(level int32) zapcore.Level {
	zapLvl, _ := severity(level)
	return zapLvl
}

// nativeLogLevel 把 C 侧等级映射为 nativelog 等级；越界值收敛到 INFO。
func nativeLogLevel(level int32) nativelog.Level {
	_, nativeLvl := severity(level)
	return nativeLvl
}

// nativeLogBridge 是挂到原生日志回调上的 Go 端载体。
//
// 它只持有队列指针，不持有 Engine —— 日志回调是进程级的，与句柄生命周期无关。
type nativeLogBridge struct {
	queue *nativelog.Queue
}

// SetNativeLogSink 安装进程级原生日志回调；必须在任何引擎句柄创建之前调用。
func (e *Engine) SetNativeLogSink(queue *nativelog.Queue) error {
	if e == nil || queue == nil {
		return ErrInvalidArgument
	}

	e.lifecycleMu.Lock()
	defer e.lifecycleMu.Unlock()

	e.mu.Lock()
	busy := e.handle != nil || e.running || e.logHandle != 0
	e.mu.Unlock()
	if busy {
		return ErrBusy
	}

	handle := cgo.NewHandle(&nativeLogBridge{queue: queue})
	callback := C.Zhulong_log_callback(C.zhulongLogCallbackBridge)
	if status := C.Zhulong_set_log_callback(callback, C.uintptr_t(handle)); status != C.Zhulong_OK {
		handle.Delete()
		return mapNativeStatus("set_log_callback", int32(status))
	}

	e.mu.Lock()
	e.logHandle = handle
	e.mu.Unlock()
	return nil
}

// CloseNativeLogSink 卸载原生日志回调；必须在所有引擎句柄销毁之后调用。
func (e *Engine) CloseNativeLogSink() error {
	if e == nil {
		return nil
	}

	e.lifecycleMu.Lock()
	defer e.lifecycleMu.Unlock()

	e.mu.Lock()
	handle := e.logHandle
	e.logHandle = 0
	e.mu.Unlock()
	if handle == 0 {
		return nil
	}

	if status := C.Zhulong_set_log_callback(nil, 0); status != C.Zhulong_OK {
		e.mu.Lock()
		e.logHandle = handle
		e.mu.Unlock()
		return mapNativeStatus("clear_log_callback", int32(status))
	}

	handle.Delete()
	return nil
}

// zhulongLogTrampoline 是原生日志回调的 Go 入口，运行在原生工作线程上。
//
// ⚠️ 日志通道绝不能把 panic 抛回原生线程，但也绝不能静默：
// 任何异常/越界/非法 UTF-8 都记为一次丢弃，让「丢日志」本身可观测。
//
//export zhulongLogTrampoline
func zhulongLogTrampoline(token C.uintptr_t, level C.int32_t, streamID C.uint64_t,
	module *C.char, moduleLen C.size_t, text *C.char, textLen C.size_t) {
	if token == 0 {
		return
	}

	var queue *nativelog.Queue
	defer func() {
		if recovered := recover(); recovered != nil && queue != nil {
			queue.RecordDrop()
		}
	}()

	if bridge, ok := cgo.Handle(uintptr(token)).Value().(*nativeLogBridge); ok && bridge != nil {
		queue = bridge.queue
	}
	if queue == nil {
		return
	}

	moduleSize := uint64(moduleLen)
	textSize := uint64(textLen)
	moduleInvalid := module == nil || moduleSize == 0 || moduleSize > maxLogModuleBytes
	textInvalid := textSize > maxLogTextBytes || (text == nil && textSize != 0)
	if moduleInvalid || textInvalid {
		queue.RecordDrop()
		return
	}

	moduleValue := C.GoStringN(module, C.int(moduleSize))
	var textValue string
	if textSize != 0 {
		textValue = C.GoStringN(text, C.int(textSize))
	}
	if !utf8.ValidString(moduleValue) || !utf8.ValidString(textValue) {
		queue.RecordDrop()
		return
	}

	queue.TryEnqueue(nativelog.Record{
		Level:    nativeLogLevel(int32(level)),
		StreamID: uint64(streamID),
		Module:   moduleValue,
		Message:  textValue,
		SeenAt:   time.Now(),
	})
}

// logCallbackCall 描述一次模拟的原生日志回调载荷。
//
// 用结构体而不是 8 个位置参数：`nullModule bool` / `nullText bool` / `textLen`
// 这类位置布尔与裸长度在调用点无法自证含义，错位也不会编译报错。
type logCallbackCall struct {
	Token      uintptr
	Level      int32
	StreamID   uint64
	Module     []byte
	NullModule bool
	Text       []byte
	NullText   bool
	TextLen    uint64
}

// invokeNativeLogCallbackForTest 从 C 侧模拟一次日志回调，用来验证跨界边界检查。
func invokeNativeLogCallbackForTest(call logCallbackCall) {
	modulePointer := allocNativeBytes(call.Module, call.NullModule)
	if modulePointer != nil {
		defer C.free(unsafe.Pointer(modulePointer))
	}
	textPointer := allocNativeBytes(call.Text, call.NullText)
	if textPointer != nil {
		defer C.free(unsafe.Pointer(textPointer))
	}

	C.zhulongInvokeLogCallback(
		C.uintptr_t(call.Token),
		C.int32_t(call.Level),
		C.uint64_t(call.StreamID),
		modulePointer,
		C.size_t(len(call.Module)),
		textPointer,
		C.size_t(call.TextLen),
	)
}

// invokeNativeLogCallbackStaticForBenchmark 走静态载荷的 C 跳板，避免把分配算进基准。
func invokeNativeLogCallbackStaticForBenchmark(token uintptr) {
	C.zhulongInvokeStaticLogCallback(C.uintptr_t(token))
}

// allocNativeBytes 把一个 Go 字节切片复制成 C 侧内存；null 为真时返回 NULL。
// 调用方负责 free 非空返回值。
func allocNativeBytes(data []byte, null bool) *C.char {
	if null {
		return nil
	}
	if len(data) == 0 {
		// 空切片也要给出非 nil 指针，才能区分「空载荷」与「空指针」。
		return (*C.char)(C.malloc(1))
	}
	return (*C.char)(C.CBytes(data))
}
