#include "Zhulong/engine.h"

int main() {
    Zhulong_engine_h engine = nullptr;

    if (Zhulong_engine_create(nullptr) != Zhulong_ERR_INVALID_ARGUMENT) return 1;
    if (Zhulong_engine_create(&engine) != Zhulong_OK || engine == nullptr) return 2;
    if (Zhulong_engine_start(nullptr) != Zhulong_ERR_INVALID_ARGUMENT) return 3;
    if (Zhulong_engine_stop(nullptr) != Zhulong_ERR_INVALID_ARGUMENT) return 4;
    if (Zhulong_engine_start(engine) != Zhulong_OK) return 5;
    if (Zhulong_engine_start(engine) != Zhulong_OK) return 6;
    if (Zhulong_engine_stop(engine) != Zhulong_OK) return 7;
    if (Zhulong_engine_stop(engine) != Zhulong_OK) return 8;
    if (Zhulong_engine_start(engine) != Zhulong_OK) return 9;

    char path_buf[256];
    size_t path_len = sizeof(path_buf);
    if (Zhulong_stream_get_broker_path(engine, 999, path_buf, &path_len) != Zhulong_ERR_NOT_FOUND) return 10;
    if (Zhulong_stream_set_publish_rate(engine, 999, 15) != Zhulong_ERR_NOT_FOUND) return 11;

    // 客户端统计：流不存在（或该流没有 AI 消费者／未建 FrameBroker）时必须是 NOT_FOUND，
    // 而不是安静地返回 0 条 —— 那会让「流没起来」和「没有算法订阅」变得无法区分。
    uint32_t client_count = 7;
    Zhulong_broker_client_stats client_buf[4];
    if (Zhulong_stream_get_broker_clients(engine, 999, client_buf, 4, &client_count) != Zhulong_ERR_NOT_FOUND) return 12;
    if (client_count != 0) return 13;

    Zhulong_engine_destroy(engine);
    Zhulong_engine_destroy(nullptr);
    return 0;
}
