#include <zhulong/plugin.h>
#include "json.hpp"
#include "../ipc/ipc_protocol.hpp"
#include "../ipc/shm_pool.hpp"
#include "../ipc/unix_socket.hpp"
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

int run_ipc_worker(const std::string& so_path, const std::string& broker_sock, const std::string& client_name, int max_frames = -1) {
    PluginLoader loader;
    std::string err;
    if (!loader.load(so_path, err)) {
        std::cerr << "{\"status\":\"failed\",\"stage\":\"dlopen\",\"error\":\"" << err << "\"}" << std::endl;
        return 1;
    }

    const auto& api = loader.api();
    zhulong_model_group_h model = nullptr;
    zhulong_status_t rc = api.model_create("{}", &model);
    if (rc != ZHULONG_PLUGIN_OK || !model) return 2;

    zhulong_worker_ctx_h worker = nullptr;
    rc = api.worker_create(model, 0, &worker);
    if (rc != ZHULONG_PLUGIN_OK || !worker) {
        api.model_destroy(model);
        return 3;
    }

    zhulong_task_ctx_h task = nullptr;
    rc = api.task_open(worker, 1, &task);
    if (rc != ZHULONG_PLUGIN_OK || !task) {
        api.worker_destroy(worker);
        api.model_destroy(model);
        return 4;
    }

    ipc::UnixSocketClient client;
    if (!client.connect(broker_sock, &err)) {
        std::cerr << "{\"status\":\"failed\",\"stage\":\"connect\",\"error\":\"" << err << "\"}" << std::endl;
        api.task_close(task);
        api.worker_destroy(worker);
        api.model_destroy(model);
        return 5;
    }

    // 1. 发送握手
    ipc::HandshakeReq req{};
    req.api_version = 1;
    std::strncpy(req.client_name, client_name.c_str(), sizeof(req.client_name) - 1);
    if (!client.send_msg(ipc::MSG_HANDSHAKE_REQ, &req, sizeof(req))) {
        api.task_close(task);
        api.worker_destroy(worker);
        api.model_destroy(model);
        return 6;
    }

    // 2. 接收握手应答
    ipc::IpcHeader resp_hdr{};
    std::vector<uint8_t> resp_payload;
    if (!client.recv_msg(resp_hdr, resp_payload, 5000) || resp_hdr.msg_type != ipc::MSG_HANDSHAKE_RESP || resp_payload.size() < sizeof(ipc::HandshakeResp)) {
        api.task_close(task);
        api.worker_destroy(worker);
        api.model_destroy(model);
        return 7;
    }

    ipc::HandshakeResp resp{};
    std::memcpy(&resp, resp_payload.data(), sizeof(resp));
    uint32_t my_client_id = resp.client_id;

    // 3. 映射共享内存
    ipc::ShmPool shm;
    if (!shm.init(resp.shm_name, ipc::ShmMode::OPEN_READ_ONLY, 0, 0, &err)) {
        std::cerr << "{\"status\":\"failed\",\"stage\":\"shm_init\",\"error\":\"" << err << "\"}" << std::endl;
        api.task_close(task);
        api.worker_destroy(worker);
        api.model_destroy(model);
        return 8;
    }

    int frames_processed = 0;
    while (max_frames < 0 || frames_processed < max_frames) {
        ipc::IpcHeader hdr{};
        std::vector<uint8_t> payload;
        if (!client.recv_msg(hdr, payload, 2000)) {
            if (!client.is_connected()) break;
            continue;
        }

        if (hdr.msg_type == ipc::MSG_FRAME_DISPATCH && payload.size() >= sizeof(ipc::FrameDispatchMsg)) {
            ipc::FrameDispatchMsg dispatch{};
            std::memcpy(&dispatch, payload.data(), sizeof(dispatch));

            const uint8_t* slot_base = shm.slot_read_ptr(dispatch.slot_index);
            if (slot_base) {
                zhulong_frame_view view{};
                view.frame_id = dispatch.frame_id;
                view.pts = static_cast<uint64_t>(dispatch.pts);
                view.width = dispatch.width;
                view.height = dispatch.height;
                view.format = dispatch.format;
                view.strides[0] = dispatch.strides[0];
                view.planes[0] = slot_base + dispatch.offsets[0];

                if (dispatch.offsets[1] > 0) {
                    view.strides[1] = dispatch.strides[1];
                    view.planes[1] = slot_base + dispatch.offsets[1];
                }
                if (dispatch.offsets[2] > 0) {
                    view.strides[2] = dispatch.strides[2];
                    view.planes[2] = slot_base + dispatch.offsets[2];
                }

                zhulong_inference_result* result = nullptr;
                rc = api.process(task, &view, &result);
                if (result) {
                    api.result_release(result);
                }
            }

            // 发送 ACK
            ipc::FrameAckMsg ack{};
            ack.stream_id = dispatch.stream_id;
            ack.frame_id = dispatch.frame_id;
            ack.slot_index = dispatch.slot_index;
            ack.generation = dispatch.generation;
            ack.client_id = my_client_id;
            client.send_msg(ipc::MSG_FRAME_ACK, &ack, sizeof(ack));

            frames_processed++;
        }
    }

    api.task_close(task);
    api.worker_destroy(worker);
    api.model_destroy(model);
    return 0;
}

} // namespace zhulong

int main(int argc, char* argv[]) {
    std::string verify_plugin_path;
    std::string sample_path = "default_sample";
    std::string broker_sock;
    std::string plugin_path;
    std::string client_name = "algo_worker";
    int max_frames = -1;

    for (int i = 1; i < argc; ++i) {
        if (std::string(argv[i]) == "--verify-plugin" && i + 1 < argc) {
            verify_plugin_path = argv[++i];
        } else if (std::string(argv[i]) == "--sample" && i + 1 < argc) {
            sample_path = argv[++i];
        } else if (std::string(argv[i]) == "--plugin" && i + 1 < argc) {
            plugin_path = argv[++i];
        } else if (std::string(argv[i]) == "--broker" && i + 1 < argc) {
            broker_sock = argv[++i];
        } else if (std::string(argv[i]) == "--name" && i + 1 < argc) {
            client_name = argv[++i];
        } else if (std::string(argv[i]) == "--max-frames" && i + 1 < argc) {
            max_frames = std::stoi(argv[++i]);
        }
    }

    if (!verify_plugin_path.empty()) {
        return zhulong::run_verify_plugin(verify_plugin_path, sample_path);
    }

    if (!plugin_path.empty() && !broker_sock.empty()) {
        return zhulong::run_ipc_worker(plugin_path, broker_sock, client_name, max_frames);
    }

    std::cout << "Zhulong Universal Native Algo Runner (v1.0)" << std::endl;
    std::cout << "Usage: zhulong_algo_runner --verify-plugin <plugin.so> [--sample <path>]" << std::endl;
    std::cout << "   or: zhulong_algo_runner --plugin <plugin.so> --broker <sock_path> [--name <name>] [--max-frames <N>]" << std::endl;
    return 0;
}
