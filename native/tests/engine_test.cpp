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

    Zhulong_engine_destroy(engine);
    Zhulong_engine_destroy(nullptr);
    return 0;
}
