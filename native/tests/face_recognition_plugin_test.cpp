#include <zhulong/plugin.h>
#include "detector.hpp"
#include <dlfcn.h>
#include <cassert>
#include <cstring>
#include <iostream>
#include <vector>

static zhulong_status_t load_plugin(const char* so_path, void** out_handle, zhulong_plugin_api_v1* out_api) {
    void* handle = dlopen(so_path, RTLD_NOW | RTLD_LOCAL);
    if (!handle) {
        std::cerr << "dlopen failed: " << dlerror() << std::endl;
        return ZHULONG_PLUGIN_ERR_NOT_FOUND;
    }
    *out_handle = handle;

    auto get_api = reinterpret_cast<zhulong_status_t (*)(uint32_t, void*, size_t)>(
        dlsym(handle, ZHULONG_PLUGIN_ENTRY_POINT)
    );
    if (!get_api) {
        dlclose(handle);
        return ZHULONG_PLUGIN_ERR_NOT_SUPPORTED;
    }

    std::memset(out_api, 0, sizeof(*out_api));
    zhulong_status_t rc = get_api(1, out_api, sizeof(*out_api));
    if (rc != ZHULONG_PLUGIN_OK) {
        dlclose(handle);
        return rc;
    }
    return ZHULONG_PLUGIN_OK;
}

int main(int argc, char* argv[]) {
    const char* plugin_so = (argc > 1) ? argv[1] : "./libFaceRecognitionPlugin.so";

    void* handle = nullptr;
    zhulong_plugin_api_v1 api;
    zhulong_status_t rc = load_plugin(plugin_so, &handle, &api);
    if (rc != ZHULONG_PLUGIN_OK) {
        std::cerr << "Failed to load plugin: " << plugin_so << std::endl;
        return 1;
    }

    assert(api.api_version == 1);
    assert(std::strcmp(api.plugin_name, "FaceRecognition") == 0);

    zhulong_model_group_h model = nullptr;
    rc = api.model_create("{\"threshold\":0.40,\"top_k\":3}", &model);
    assert(rc == ZHULONG_PLUGIN_OK && model != nullptr);

    zhulong_worker_ctx_h worker = nullptr;
    rc = api.worker_create(model, 0, &worker);
    assert(rc == ZHULONG_PLUGIN_OK && worker != nullptr);

    zhulong_task_ctx_h task = nullptr;
    rc = api.task_open(worker, 100, &task);
    assert(rc == ZHULONG_PLUGIN_OK && task != nullptr);

    // 构造有效人脸像素（100x100 BGR）
    std::vector<uint8_t> test_pixels(100 * 100 * 3, 0);
    for (size_t i = 0; i < test_pixels.size(); ++i) {
        test_pixels[i] = static_cast<uint8_t>((i * 7 + 13) % 256);
    }

    zhulong_frame_view frame{};
    frame.frame_id = 1;
    frame.width = 100;
    frame.height = 100;
    frame.format = ZHULONG_PIXEL_FORMAT_BGR24;
    frame.planes[0] = test_pixels.data();
    frame.strides[0] = 300;

    // Bright pixels alone are not evidence of a face. The CPU stub returns an incompatible
    // output shape, so both direct detection and the plugin must fail closed.
    zhulong::face::FaceDetector detector;
    zhulong::Image img_view(frame);
    assert(detector.detect(img_view).empty());

    auto assert_no_detections = [&](const zhulong_frame_view& sample) {
        zhulong_inference_result* result = nullptr;
        zhulong_status_t process_rc = api.process(task, &sample, &result);
        assert(process_rc == ZHULONG_PLUGIN_OK && result != nullptr);
        assert(result->result_type == ZHULONG_RESULT_TYPE_PERSON_STRUCTURE);
        assert(result->count == 0);
        api.result_release(result);
    };
    assert_no_detections(frame);

    std::vector<uint8_t> black_pixels(100 * 100 * 3, 0);
    zhulong_frame_view black_frame = frame;
    black_frame.planes[0] = black_pixels.data();
    assert_no_detections(black_frame);

    // Release resources.
    api.task_close(task);
    api.worker_destroy(worker);
    api.model_destroy(model);
    dlclose(handle);

    std::cout << "All FaceRecognitionPlugin tests passed successfully!" << std::endl;
    return 0;
}
