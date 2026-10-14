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
	"unsafe"
)

//export zhulongGoPacketCallback
func zhulongGoPacketCallback(token C.uintptr_t, packet *C.Zhulong_packet_view) {
	if token == 0 || packet == nil {
		return
	}
	defer func() {
		_ = recover()
	}()

	h := cgo.Handle(token)
	sub, ok := h.Value().(*Subscription)
	if !ok || sub == nil {
		return
	}
	sub.onPacket(packet)
}

//export zhulongGoAIResultCallback
func zhulongGoAIResultCallback(token C.uintptr_t, stream C.Zhulong_stream_id, json_payload *C.char, payload_len C.size_t) {
	if token == 0 || json_payload == nil || payload_len == 0 {
		return
	}
	defer func() {
		_ = recover()
	}()

	h := cgo.Handle(token)
	sub, ok := h.Value().(*AIResultSubscription)
	if !ok || sub == nil {
		return
	}
	data := C.GoBytes(unsafe.Pointer(json_payload), C.int(payload_len))
	sub.onResult(uint64(stream), data)
}
