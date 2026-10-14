#include <stdint.h>
#include <Zhulong/engine.h>

// 由 Go 侧 //export 声明的函数
extern void zhulongGoPacketCallback(uintptr_t token, const Zhulong_packet_view *packet);
extern void zhulongGoAIResultCallback(uintptr_t token, Zhulong_stream_id stream, const char *json_payload, size_t payload_len);

// C 回调跳转桥，供 Zhulong_stream_subscribe 注册
void zhulongPacketCallbackBridge(uintptr_t token, const Zhulong_packet_view *packet) {
    zhulongGoPacketCallback(token, packet);
}

// C 回调跳转桥，供 Zhulong_stream_subscribe_ai_results 注册
void zhulongAIResultCallbackBridge(uintptr_t token, Zhulong_stream_id stream, const char *json_payload, size_t payload_len) {
    zhulongGoAIResultCallback(token, stream, json_payload, payload_len);
}
