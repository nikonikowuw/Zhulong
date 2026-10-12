#include <zhulong/plugin.h>
#include "json.hpp"
#include <dlfcn.h>
#include <iostream>
#include <string>
#include <vector>
#include <thread>
#include <mutex>
#include <memory>
#include <cstring>
#include <fstream>

namespace zhulong {

class PluginLoader {
public:
    ~PluginLoader() {
        unload();
    }

    bool load(const std::string& so_path, std::string& error_msg) {
        unload();
        handle_ = dlopen(so_path.c_str(), RTLD_NOW | RTLD_LOCAL);
        if (!handle_) {
            const char* err = dlerror();
            error_msg = err ? err : "unknown dlopen error";
            return false;
        }

        auto get_api_fn = reinterpret_cast<zhulong_status_t (*)(uint32_t, void*, size_t)>(
            dlsym(handle_, ZHULONG_PLUGIN_ENTRY_POINT)
        );
        if (!get_api_fn) {
            error_msg = "Symbol " ZHULONG_PLUGIN_ENTRY_POINT " not found in plugin";
            unload();
            return false;
        }

        std::memset(&api_, 0, sizeof(api_));
        zhulong_status_t rc = get_api_fn(1, &api_, sizeof(api_));
        if (rc != ZHULONG_PLUGIN_OK || api_.api_version != 1) {
            error_msg = "Plugin ABI handshake failed or version unsupported";
            unload();
            return false;
        }

        so_path_ = so_path;
        return true;
    }

    void unload() {
        if (handle_) {
            dlclose(handle_);
            handle_ = nullptr;
        }
        std::memset(&api_, 0, sizeof(api_));
    }

    bool is_loaded() const noexcept { return handle_ != nullptr; }
    const zhulong_plugin_api_v1& api() const noexcept { return api_; }
    const std::string& path() const noexcept { return so_path_; }

private:
    void* handle_{nullptr};
    zhulong_plugin_api_v1 api_{};
    std::string so_path_;
};

int run_verify_plugin(const std::string& so_path, const std::string& sample_path) {
    std::cout << "{\"step\":\"start\",\"plugin\":" << runner::json_quote(so_path) << "}" << std::endl;

    PluginLoader loader;
    std::string err;
    if (!loader.load(so_path, err)) {
        std::cerr << "{\"status\":\"failed\",\"stage\":\"dlopen\",\"error\":"
                  << runner::json_quote(err) << "}" << std::endl;
        return 1;
    }

    const auto& api = loader.api();
    std::cout << "{\"step\":\"abi_verified\",\"name\":"
              << runner::json_quote(api.plugin_name ? api.plugin_name : "")
              << ",\"version\":" << runner::json_quote(api.plugin_version ? api.plugin_version : "")
              << "}" << std::endl;

    // 1. 创建模型
    zhulong_model_group_h model = nullptr;
    zhulong_status_t rc = api.model_create("{}", &model);
    if (rc != ZHULONG_PLUGIN_OK || !model) {
        std::cerr << "{\"status\":\"failed\",\"stage\":\"model_create\",\"rc\":" << rc << "}" << std::endl;
        return 2;
    }

    // 2. 创建 Worker
    zhulong_worker_ctx_h worker = nullptr;
    rc = api.worker_create(model, 0, &worker);
    if (rc != ZHULONG_PLUGIN_OK || !worker) {
        api.model_destroy(model);
        std::cerr << "{\"status\":\"failed\",\"stage\":\"worker_create\",\"rc\":" << rc << "}" << std::endl;
        return 3;
    }

    // 3. 打开任务
    zhulong_task_ctx_h task = nullptr;
    rc = api.task_open(worker, 1, &task);
    if (rc != ZHULONG_PLUGIN_OK || !task) {
        api.worker_destroy(worker);
        api.model_destroy(model);
        std::cerr << "{\"status\":\"failed\",\"stage\":\"task_open\",\"rc\":" << rc << "}" << std::endl;
        return 4;
    }

    // 4. 准备样本帧 (若指定文件读取测试样本，否则构造合成测试帧)
    int width = 640;
    int height = 480;
    std::vector<uint8_t> dummy_buf(width * height * 3, 128);

    zhulong_frame_view frame;
    std::memset(&frame, 0, sizeof(frame));
    frame.frame_id = 1;
    frame.pts = 1000;
    frame.width = width;
    frame.height = height;
    frame.format = ZHULONG_PIXEL_FORMAT_BGR24;
    frame.planes[0] = dummy_buf.data();
    frame.strides[0] = width * 3;

    // 5. 单次标准推理调用
    zhulong_inference_result* result = nullptr;
    rc = api.process(task, &frame, &result);
    if (rc != ZHULONG_PLUGIN_OK) {
        api.task_close(task);
        api.worker_destroy(worker);
        api.model_destroy(model);
        std::cerr << "{\"status\":\"failed\",\"stage\":\"process\",\"rc\":" << rc << "}" << std::endl;
        return 5;
    }

    uint32_t count = result ? result->count : 0;
    int32_t res_type = result ? result->result_type : 0;

    // 6. 释放结果
    if (result) {
        api.result_release(result);
    }

    // 7. 析构资源
    api.task_close(task);
    api.worker_destroy(worker);
    api.model_destroy(model);

    std::cout << "{\"status\":\"passed\",\"sample\":" << runner::json_quote(sample_path)
              << ",\"result_type\":" << res_type
              << ",\"count\":" << count << "}" << std::endl;
    return 0;
}

} // namespace zhulong

int main(int argc, char* argv[]) {
    std::string verify_plugin_path;
    std::string sample_path = "default_sample";

    for (int i = 1; i < argc; ++i) {
        if (std::string(argv[i]) == "--verify-plugin" && i + 1 < argc) {
            verify_plugin_path = argv[++i];
        } else if (std::string(argv[i]) == "--sample" && i + 1 < argc) {
            sample_path = argv[++i];
        }
    }

    if (!verify_plugin_path.empty()) {
        return zhulong::run_verify_plugin(verify_plugin_path, sample_path);
    }

    std::cout << "Zhulong Universal Native Algo Runner (v1.0)" << std::endl;
    std::cout << "Usage: zhulong_algo_runner --verify-plugin <plugin.so> [--sample <path>]" << std::endl;
    return 0;
}
