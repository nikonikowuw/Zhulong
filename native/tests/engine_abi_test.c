#include "Zhulong/engine.h"

int main(void) {
    Zhulong_engine_h engine = 0;

    if (Zhulong_engine_create(0) != Zhulong_ERR_INVALID_ARGUMENT) return 1;
    if (Zhulong_engine_create(&engine) != Zhulong_OK || engine == 0) return 2;
    if (Zhulong_engine_start(engine) != Zhulong_OK) return 3;
    if (Zhulong_engine_stop(engine) != Zhulong_OK) return 4;

    Zhulong_engine_destroy(engine);
    Zhulong_engine_destroy(0);
    return 0;
}
