// 独立进程 OOM 回归：只在调用 load_model 前失败一次堆分配。
// 路径 basename 足够长，确保 file_basename 不走 string 的小字符串优化。

#include <backends/onnx/onnx_inference_backend.hpp>

#include <cstddef>
#include <cstdio>
#include <cstdlib>
#include <new>
#include <string>

namespace {
bool fail_next_allocation = false;
unsigned injected_failures = 0;

void require(bool condition, const char* label) {
    if (!condition) {
        std::fprintf(stderr, "FAIL %s\n", label);
        std::exit(1);
    }
}
} // namespace

// 只链接进本测试目标，不向生产 Runtime 添加故障注入开关。
void* operator new(std::size_t size) {
    if (fail_next_allocation) {
        fail_next_allocation = false;
        ++injected_failures;
        throw std::bad_alloc{};
    }
    if (void* pointer = std::malloc(size == 0 ? 1 : size)) {
        return pointer;
    }
    throw std::bad_alloc{};
}

void* operator new[](std::size_t size) { return ::operator new(size); }
void operator delete(void* pointer) noexcept { std::free(pointer); }
void operator delete(void* pointer, std::size_t) noexcept { std::free(pointer); }
void operator delete[](void* pointer) noexcept { std::free(pointer); }
void operator delete[](void* pointer, std::size_t) noexcept { std::free(pointer); }

int main() {
    zhulong::OnnxInferenceBackend backend;
    const std::string path = "/nonexistent/" + std::string(120, 'a') + ".onnx";
    fail_next_allocation = true;
    try {
        require(backend.load_model(path) == nullptr, "OOM load returns null");
    } catch (...) {
        std::fprintf(stderr, "FAIL exception escaped load_model\n");
        return 1;
    }
    require(injected_failures == 1 && !fail_next_allocation, "basename allocation actually failed once");
    require(backend.last_load_status() == ZHULONG_PLUGIN_ERR_OUT_OF_MEMORY, "basename OOM recorded");
    require(backend.load_model("") == nullptr && backend.last_load_status() == ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT,
            "later invalid path replaces stale OOM status");
    std::puts("PASS ONNX_CONTRACT basename_oom_contained");
    return 0;
}
