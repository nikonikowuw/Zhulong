#include <stddef.h>
#include <stdint.h>

extern void zhulongLogTrampoline(uintptr_t token, int32_t level, uint64_t stream_id,
    char *module, size_t module_len, char *text, size_t text_len);

void zhulongLogCallbackBridge(uintptr_t token, int32_t level, uint64_t stream_id,
    const char *module, size_t module_len, const char *text, size_t text_len) {
    zhulongLogTrampoline(token, level, stream_id, (char *)module, module_len, (char *)text, text_len);
}

void zhulongInvokeLogCallback(uintptr_t token, int32_t level, uint64_t stream_id,
    const char *module, size_t module_len, const char *text, size_t text_len) {
    zhulongLogTrampoline(token, level, stream_id, (char *)module, module_len, (char *)text, text_len);
}

void zhulongInvokeStaticLogCallback(uintptr_t token) {
    static const char module[] = "pipeline.benchmark";
    static const char text[] = "callback_benchmark";
    zhulongLogTrampoline(token, 1, 9, (char *)module, sizeof(module) - 1,
        (char *)text, sizeof(text) - 1);
}
