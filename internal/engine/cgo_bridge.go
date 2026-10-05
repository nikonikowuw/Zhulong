package engine

/*
#cgo CFLAGS: -I${SRCDIR}/../../native/include
#include <stdint.h>
#include <Zhulong/engine.h>

void zhulongPacketCallbackBridge(uintptr_t token, const Zhulong_packet_view *packet);
*/
import "C"

import (
	"runtime/cgo"
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
