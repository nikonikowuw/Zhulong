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

type nativeLogBridge struct {
	queue *nativelog.Queue
}

// zapLevel translates the public C enum explicitly; its values do not overlap zap's.
func zapLevel(level int32) zapcore.Level {
	switch level {
	case 0:
		return zapcore.DebugLevel
	case 1:
		return zapcore.InfoLevel
	case 2:
		return zapcore.WarnLevel
	case 3:
		return zapcore.ErrorLevel
	default:
		return zapcore.InfoLevel
	}
}

func cLogLevel(level zapcore.Level) int32 {
	switch level {
	case zapcore.DebugLevel:
		return 0
	case zapcore.InfoLevel:
		return 1
	case zapcore.WarnLevel:
		return 2
	case zapcore.ErrorLevel:
		return 3
	default:
		return 1
	}
}

func nativeLogLevel(level int32) nativelog.Level {
	switch level {
	case 0:
		return nativelog.Debug
	case 1:
		return nativelog.Info
	case 2:
		return nativelog.Warn
	case 3:
		return nativelog.Error
	default:
		return nativelog.Info
	}
}

// SetNativeLogSink installs the process-wide callback before any native engine handle is created.
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
	if status := C.Zhulong_set_log_callback(C.Zhulong_log_callback(C.zhulongLogCallbackBridge), C.uintptr_t(handle)); status != C.Zhulong_OK {
		handle.Delete()
		return mapNativeStatus("register native log callback", int32(status))
	}
	e.logHandle = handle
	return nil
}

// CloseNativeLogSink unregisters only after all native engine handles have been destroyed.
func (e *Engine) CloseNativeLogSink() error {
	if e == nil {
		return nil
	}
	e.lifecycleMu.Lock()
	defer e.lifecycleMu.Unlock()
	if e.logHandle == 0 {
		return nil
	}
	if status := C.Zhulong_set_log_callback(nil, 0); status != C.Zhulong_OK {
		return mapNativeStatus("unregister native log callback", int32(status))
	}
	e.logHandle.Delete()
	e.logHandle = 0
	return nil
}

//export zhulongLogTrampoline
func zhulongLogTrampoline(token C.uintptr_t, level C.int32_t, streamID C.uint64_t,
	module *C.char, moduleLen C.size_t, text *C.char, textLen C.size_t) {
	defer func() { _ = recover() }()
	if token == 0 {
		return
	}
	bridge, ok := cgo.Handle(uintptr(token)).Value().(*nativeLogBridge)
	if !ok || bridge == nil || bridge.queue == nil {
		return
	}
	queue := bridge.queue
	moduleSize := uint64(moduleLen)
	textSize := uint64(textLen)
	if module == nil || moduleSize == 0 || moduleSize > 128 || textSize > 2048 || (text == nil && textSize != 0) {
		queue.RecordDrop()
		return
	}

	moduleValue := C.GoStringN(module, C.int(moduleSize))
	textValue := ""
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

func invokeNativeLogCallbackStaticForBenchmark(token uintptr) {
	C.zhulongInvokeStaticLogCallback(C.uintptr_t(token))
}

func invokeNativeLogCallbackForTest(token uintptr, level int32, streamID uint64,
	module []byte, nullModule bool, text []byte, nullText bool, textLen uint64) {
	var modulePointer *C.char
	var textPointer *C.char
	var moduleAllocation unsafe.Pointer
	var textAllocation unsafe.Pointer
	if !nullModule {
		if len(module) == 0 {
			moduleAllocation = C.malloc(1)
		} else {
			moduleAllocation = C.CBytes(module)
		}
		if moduleAllocation == nil {
			panic("allocate native module test bytes")
		}
		modulePointer = (*C.char)(moduleAllocation)
		defer C.free(moduleAllocation)
	}
	if !nullText {
		if len(text) == 0 {
			textAllocation = C.malloc(1)
		} else {
			textAllocation = C.CBytes(text)
		}
		if textAllocation == nil {
			panic("allocate native text test bytes")
		}
		textPointer = (*C.char)(textAllocation)
		defer C.free(textAllocation)
	}
	C.zhulongInvokeLogCallback(C.uintptr_t(token), C.int32_t(level), C.uint64_t(streamID),
		modulePointer, C.size_t(len(module)), textPointer, C.size_t(textLen))
}
