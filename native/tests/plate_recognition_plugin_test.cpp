#include <zhulong/plugin.h>
#include <dlfcn.h>
#include <cassert>
#include <cmath>
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
    const char* plugin_so = (argc > 1) ? argv[1] : "./libPlateRecognitionPlugin.so";

    void* handle = nullptr;
    zhulong_plugin_api_v1 api;
    zhulong_status_t rc = load_plugin(plugin_so, &handle, &api);
    if (rc != ZHULONG_PLUGIN_OK) {
        std::cerr << "Failed to load plugin: " << plugin_so << std::endl;
        return 1;
    }

    assert(api.api_version == 1);
    assert(std::strcmp(api.plugin_name, "PlateRecognition") == 0);
    assert(std::strcmp(api.plugin_version, "1.0.0") == 0);

    // 1. model_create
    zhulong_model_group_h model = nullptr;
    rc = api.model_create("{}", &model);
    assert(rc == ZHULONG_PLUGIN_OK && model != nullptr);

    // 2. worker_create
    zhulong_worker_ctx_h worker = nullptr;
    rc = api.worker_create(model, 0, &worker);
    assert(rc == ZHULONG_PLUGIN_OK && worker != nullptr);

    // 3. task_open
    zhulong_task_ctx_h task = nullptr;
    rc = api.task_open(worker, 1, &task);
    assert(rc == ZHULONG_PLUGIN_OK && task != nullptr);

    // 4. process 样本帧
    int width = 640;
    int height = 480;
    std::vector<uint8_t> dummy_buf(width * height * 3, 200);

    zhulong_frame_view frame;
    std::memset(&frame, 0, sizeof(frame));
    frame.frame_id = 42;
    frame.pts = 100000;
    frame.width = width;
    frame.height = height;
    frame.format = ZHULONG_PIXEL_FORMAT_RGB24;
    frame.planes[0] = dummy_buf.data();
    frame.strides[0] = width * 3;

    zhulong_inference_result* result = nullptr;
    rc = api.process(task, &frame, &result);
    assert(rc == ZHULONG_PLUGIN_OK && result != nullptr);
    assert(result->result_type == ZHULONG_RESULT_TYPE_VEHICLE_STRUCTURE);
    assert(result->count >= 1);

    auto* vehicles = static_cast<const zhulong_vehicle_item*>(result->data);
    assert(vehicles != nullptr);
    assert(vehicles[0].plate_count >= 1);
    assert(vehicles[0].plates != nullptr);
    assert(std::strlen(vehicles[0].plates[0].plate_number) > 0);
    assert(std::strlen(vehicles[0].plates[0].plate_color) > 0);
    std::cout << "[✓] VehicleStructure output: vehicle_box=["
              << vehicles[0].vehicle_box[0] << "," << vehicles[0].vehicle_box[1] << ","
              << vehicles[0].vehicle_box[2] << "," << vehicles[0].vehicle_box[3] << "], plate="
              << vehicles[0].plates[0].plate_number << " (" << vehicles[0].plates[0].plate_color << ")"
              << std::endl;

    // 5. 释放结果
    api.result_release(result);

    // 6. 销毁上下文
    api.task_close(task);
    api.worker_destroy(worker);
    api.model_destroy(model);

    dlclose(handle);

    std::cout << "[✓] PlateRecognitionPlugin test passed successfully!" << std::endl;
    return 0;
}
