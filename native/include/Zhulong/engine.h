#ifndef ZHULONG_ENGINE_H
#define ZHULONG_ENGINE_H

#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef struct Zhulong_engine_t *Zhulong_engine_h;

typedef int32_t Zhulong_status_t;

enum {
    Zhulong_OK = 0,
    Zhulong_ERR_INVALID_ARGUMENT = -1,
    Zhulong_ERR_OUT_OF_MEMORY = -2,
    Zhulong_ERR_INTERNAL = -99
};

Zhulong_status_t Zhulong_engine_create(Zhulong_engine_h *out_engine);
Zhulong_status_t Zhulong_engine_start(Zhulong_engine_h engine);
Zhulong_status_t Zhulong_engine_stop(Zhulong_engine_h engine);
void Zhulong_engine_destroy(Zhulong_engine_h engine);

#ifdef __cplusplus
}
#endif

#endif
