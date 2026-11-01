package engine

/*
#cgo CFLAGS: -I${SRCDIR}/../../native/include
#include <stdint.h>
#include <Zhulong/engine.h>

void zhulongPacketCallbackBridge(uintptr_t token, const Zhulong_packet_view *packet);
void zhulongAIResultCallbackBridge(uintptr_t token, Zhulong_stream_id stream, const char *json_payload, size_t payload_len);
*/
import "C"

import (
	"runtime/cgo"
	"sync/atomic"
	"unsafe"
)

// cgoToken 是一次 cgo.NewHandle 的整数令牌，保证 Delete 幂等。
//
// ⚠️ release 的调用时机才是关键：只有原生侧确认同步排空之后才允许释放，
// 否则残余回调会命中已删除的句柄（cgo.Handle.Value 会 panic）。
type cgoToken struct {
	value   uintptr
	deleted atomic.Bool
}

// release 幂等释放底层 cgo.Handle；零值（从未注册）为空操作。
func (t *cgoToken) release() {
	if t.value == 0 {
		return
	}
	if t.deleted.CompareAndSwap(false, true) {
		cgo.Handle(t.value).Delete()
	}
}

//export zhulongGoPacketCallback
func zhulongGoPacketCallback(token C.uintptr_t, packet *C.Zhulong_packet_view) {
	if token == 0 || packet == nil {
		return
	}

	// 回调解引用链（含 cgo.Handle.Value）全部发生在 defer 之后，
	// 保证任何 panic 都在 ABI 边界内被拦住。
	//
	// ⚠️ sub 为 nil 时 recover 分支必须直接放弃：句柄已删除会让
	// cgo.Handle.Value 自己 panic，此时再调 nil 接收者的方法就是二次 panic，
	// 而它发生在 deferred 函数里，会直接冲出 ABI 边界。
	var sub *Subscription
	defer func() {
		if recovered := recover(); recovered != nil && sub != nil {
			sub.reportCallbackPanic(recovered)
		}
	}()

	if value, ok := cgo.Handle(token).Value().(*Subscription); ok {
		sub = value
	}
	if sub == nil {
		return
	}
	sub.onPacket(packet)
}

//export zhulongGoAIResultCallback
func zhulongGoAIResultCallback(
	token C.uintptr_t,
	stream C.Zhulong_stream_id,
	jsonPayload *C.char,
	payloadLen C.size_t,
) {
	if token == 0 || jsonPayload == nil || payloadLen == 0 {
		return
	}

	// 同 zhulongGoPacketCallback：nil 接收者上的二次 panic 绝不允许出口。
	var sub *AIResultSubscription
	defer func() {
		if recovered := recover(); recovered != nil && sub != nil {
			sub.reportCallbackPanic(recovered)
		}
	}()

	if value, ok := cgo.Handle(token).Value().(*AIResultSubscription); ok {
		sub = value
	}
	if sub == nil {
		return
	}

	// JSON 载荷借用自原生缓冲区，必须立即深拷贝成 Go 内存。
	payload := C.GoBytes(unsafe.Pointer(jsonPayload), C.int(payloadLen))
	sub.onResult(uint64(stream), payload)
}
