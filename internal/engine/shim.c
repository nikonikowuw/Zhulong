#include <stdint.h>
#include <Zhulong/engine.h>

// 由 Go 侧 //export 声明的函数
extern void zhulongGoPacketCallback(uintptr_t token, const Zhulong_packet_view *packet);

// C 回调跳转桥，供 Zhulong_stream_subscribe 注册
void zhulongPacketCallbackBridge(uintptr_t token, const Zhulong_packet_view *packet) {
    zhulongGoPacketCallback(token, packet);
}
