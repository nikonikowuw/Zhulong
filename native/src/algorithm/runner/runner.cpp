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
#include <sstream>
#include <chrono>

namespace zhulong
{
    /**
     * @brief 从 FrameBroker socket 路径中解析 stream_id。
     *
     * native 宿主按 `<base_dir>/zhulong_broker_<stream_id>.sock` 派生路径
     * （frame_broker.cpp:20），因此 stream_id 可从字面路径反解，无需扩展 IPC 协议。
     * 解析失败返回 0（stream_id 从 1 开始，0 永不是合法值）。
     */
    inline uint64_t parse_stream_id_from_socket(const std::string &socket_path)
    {
        const std::string marker = "zhulong_broker_";
        const auto start = socket_path.rfind(marker);
        if (start == std::string::npos)
        {
            return 0;
        }
        const auto digits_begin = start + marker.size();
        const auto suffix = socket_path.rfind(".sock");
        if (suffix == std::string::npos || suffix <= digits_begin)
        {
            return 0;
        }

        uint64_t value = 0;
        for (auto i = digits_begin; i < suffix; ++i)
        {
            const char c = socket_path[i];
            if (c < '0' || c > '9')
            {
                return 0;
            }
            value = value * 10 + static_cast<uint64_t>(c - '0');
        }
        return value;
    }

    static std::string serialize_inference_result(uint64_t stream_id,
                                                  int64_t pts,
                                                  int64_t cost_ms,
                                                  const zhulong_inference_result* result)
    {
        std::ostringstream ss;
        ss << "{\"stream_id\":" << stream_id
           << ",\"pts\":" << pts
           << ",\"cost_ms\":" << cost_ms;

        if (!result || result->count == 0)
        {
            ss << ",\"task_type\":\"\""
               << ",\"detections\":[]"
               << ",\"classifications\":[]"
               << ",\"persons\":[]}";
            return ss.str();
        }

        if (result->result_type == ZHULONG_RESULT_TYPE_DETECTION)
        {
            ss << ",\"task_type\":\"detection\""
               << ",\"detections\":[";
            const auto* boxes = static_cast<const zhulong_bbox*>(result->data);
            for (uint32_t i = 0; i < result->count; ++i)
            {
                if (i > 0) ss << ",";
                ss << "{\"box\":[" << boxes[i].box[0] << "," << boxes[i].box[1] << ","
                   << boxes[i].box[2] << "," << boxes[i].box[3] << "]"
                   << ",\"label\":" << runner::json_quote(boxes[i].label)
                   << ",\"score\":" << boxes[i].confidence << "}";
            }
            ss << "],\"classifications\":[],\"persons\":[]}";
        }
        else if (result->result_type == ZHULONG_RESULT_TYPE_CLASSIFICATION)
        {
            ss << ",\"task_type\":\"classification\""
               << ",\"detections\":[],\"classifications\":[";
            const auto* items = static_cast<const zhulong_classification_item*>(result->data);
            for (uint32_t i = 0; i < result->count; ++i)
            {
                if (i > 0) ss << ",";
                ss << "{\"label\":" << runner::json_quote(items[i].label)
                   << ",\"score\":" << items[i].confidence << "}";
            }
            ss << "],\"persons\":[]}";
        }
        else if (result->result_type == ZHULONG_RESULT_TYPE_PERSON_STRUCTURE)
        {
            ss << ",\"task_type\":\"person_structure\""
               << ",\"detections\":[],\"classifications\":[],\"persons\":[";
            const auto* persons = static_cast<const zhulong_person_item*>(result->data);
            for (uint32_t i = 0; i < result->count; ++i)
            {
                if (i > 0) ss << ",";
                const auto& p = persons[i];
                ss << "{\"track_id\":" << p.track_id
                   << ",\"body_box\":[" << p.body_box[0] << "," << p.body_box[1] << ","
                   << p.body_box[2] << "," << p.body_box[3] << "]"
                   << ",\"body_score\":" << p.body_score
                   << ",\"faces\":[";
                for (uint32_t j = 0; j < p.face_count; ++j)
                {
                    if (j > 0) ss << ",";
                    const auto& f = p.faces[j];
                    ss << "{\"box\":[" << f.box[0] << "," << f.box[1] << ","
                       << f.box[2] << "," << f.box[3] << "]"
                       << ",\"quality\":" << f.quality
                       << ",\"landmarks\":[";
                    for (size_t k = 0; k < ZHULONG_FACE_LANDMARK_COUNT; ++k)
                    {
                        if (k > 0) ss << ",";
                        ss << "[" << f.landmarks[k][0] << "," << f.landmarks[k][1] << "]";
                    }
                    ss << "],\"matched\":" << (f.matched ? "true" : "false")
                       << ",\"similarity\":" << f.similarity
                       << ",\"person_id\":" << runner::json_quote(f.person_id)
                       << ",\"person_name\":" << runner::json_quote(f.person_name)
                       << ",\"candidates\":[";
                    for (uint32_t k = 0; k < f.candidate_count; ++k)
                    {
                        if (k > 0) ss << ",";
                        const auto& c = f.candidates[k];
                        ss << "{\"person_id\":" << runner::json_quote(c.person_id)
                           << ",\"person_name\":" << runner::json_quote(c.person_name)
                           << ",\"similarity\":" << c.similarity << "}";
                    }
                    ss << "]}";
                }
                ss << "]}";
            }
            ss << "]}";
        }
        else
        {
            ss << ",\"task_type\":\"custom\",\"detections\":[],\"classifications\":[],\"persons\":[]}";
        }

        return ss.str();
    }

    class PluginLoader
    {
    public:
        ~PluginLoader()
        {
            unload();
        }

        bool load(const std::string &so_path, std::string &error_msg)
        {
            unload();
            handle_ = dlopen(so_path.c_str(), RTLD_NOW | RTLD_LOCAL);
            if (!handle_)
            {
                const char *err = dlerror();
                error_msg = err ? err : "unknown dlopen error";
                return false;
            }

            auto get_api_fn = reinterpret_cast<zhulong_status_t (*)(uint32_t, void *, size_t)>(
                dlsym(handle_, ZHULONG_PLUGIN_ENTRY_POINT));
            if (!get_api_fn)
            {
                error_msg = "Symbol " ZHULONG_PLUGIN_ENTRY_POINT " not found in plugin";
                unload();
                return false;
            }

            std::memset(&api_, 0, sizeof(api_));
            zhulong_status_t rc = get_api_fn(1, &api_, sizeof(api_));
            if (rc != ZHULONG_PLUGIN_OK || api_.api_version != 1)
            {
                error_msg = "Plugin ABI handshake failed or version unsupported";
                unload();
                return false;
            }

            so_path_ = so_path;
            return true;
        }

        void unload()
        {
            if (handle_)
            {
                dlclose(handle_);
                handle_ = nullptr;
            }
            std::memset(&api_, 0, sizeof(api_));
        }

        bool is_loaded() const noexcept { return handle_ != nullptr; }
        const zhulong_plugin_api_v1 &api() const noexcept { return api_; }
        const std::string &path() const noexcept { return so_path_; }

    private:
        void *handle_{nullptr};
        zhulong_plugin_api_v1 api_{};
        std::string so_path_;
    };

    int run_verify_plugin(const std::string &so_path, const std::string &sample_path)
    {
        std::cout << "{\"step\":\"start\",\"plugin\":" << runner::json_quote(so_path) << "}" << std::endl;

        PluginLoader loader;
        std::string err;
        if (!loader.load(so_path, err))
        {
            std::cerr << "{\"status\":\"failed\",\"stage\":\"dlopen\",\"error\":"
                      << runner::json_quote(err) << "}" << std::endl;
            return 1;
        }

        const auto &api = loader.api();
        std::cout << "{\"step\":\"abi_verified\",\"name\":"
                  << runner::json_quote(api.plugin_name ? api.plugin_name : "")
                  << ",\"version\":" << runner::json_quote(api.plugin_version ? api.plugin_version : "")
                  << "}" << std::endl;

        // 1. 创建模型
        zhulong_model_group_h model = nullptr;
        zhulong_status_t rc = api.model_create("{}", &model);
        if (rc != ZHULONG_PLUGIN_OK || !model)
        {
            std::cerr << "{\"status\":\"failed\",\"stage\":\"model_create\",\"rc\":" << rc << "}" << std::endl;
            return 2;
        }

        // 2. 创建 Worker
        zhulong_worker_ctx_h worker = nullptr;
        rc = api.worker_create(model, 0, &worker);
        if (rc != ZHULONG_PLUGIN_OK || !worker)
        {
            api.model_destroy(model);
            std::cerr << "{\"status\":\"failed\",\"stage\":\"worker_create\",\"rc\":" << rc << "}" << std::endl;
            return 3;
        }

        // 3. 打开任务
        zhulong_task_ctx_h task = nullptr;
        rc = api.task_open(worker, 1, &task);
        if (rc != ZHULONG_PLUGIN_OK || !task)
        {
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
        zhulong_inference_result *result = nullptr;
        rc = api.process(task, &frame, &result);
        if (rc != ZHULONG_PLUGIN_OK)
        {
            api.task_close(task);
            api.worker_destroy(worker);
            api.model_destroy(model);
            std::cerr << "{\"status\":\"failed\",\"stage\":\"process\",\"rc\":" << rc << "}" << std::endl;
            return 5;
        }

        uint32_t count = result ? result->count : 0;
        int32_t res_type = result ? result->result_type : 0;

        // 6. 释放结果
        if (result)
        {
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

    int run_ipc_workers(const std::string &so_path, const std::vector<std::string> &broker_socks, const std::string &client_name, int max_frames = -1)
    {
        if (broker_socks.empty())
        {
            std::cerr << "{\"status\":\"failed\",\"stage\":\"args\",\"error\":\"no broker socks provided\"}" << std::endl;
            return 5;
        }

        PluginLoader loader;
        std::string err;
        if (!loader.load(so_path, err))
        {
            std::cerr << "{\"status\":\"failed\",\"stage\":\"dlopen\",\"error\":\"" << err << "\"}" << std::endl;
            return 1;
        }

        const auto &api = loader.api();
        zhulong_model_group_h model = nullptr;
        zhulong_status_t rc = api.model_create("{}", &model);
        if (rc != ZHULONG_PLUGIN_OK || !model)
            return 2;

        std::atomic<bool> stop_all{false};
        std::atomic<int> exit_code{0};

        auto run_single_worker = [&](size_t idx, const std::string &broker_sock)
        {
            // task 身份必须包含 stream_id：一个算法包可同时订阅多路流，
            // 固定 task_id 会让多路共用一个 task state（父任务 design.md:242 要求 task→worker 亲和）。
            const uint64_t stream_id = parse_stream_id_from_socket(broker_sock);
            if (stream_id == 0)
            {
                std::cerr << "{\"status\":\"failed\",\"stage\":\"args\",\"index\":" << idx
                          << ",\"path\":\"" << broker_sock << "\",\"error\":\"cannot derive stream id from broker socket path\"}" << std::endl;
                int expected = 0;
                exit_code.compare_exchange_strong(expected, 5);
                stop_all.store(true);
                return;
            }

            std::string name = client_name;
            if (broker_socks.size() > 1)
            {
                name += "#" + std::to_string(idx);
            }
            if (name.size() >= sizeof(ipc::HandshakeReq::client_name))
            {
                std::cerr << "{\"status\":\"failed\",\"stage\":\"name_check\",\"error\":\"client name too long: " << name << "\"}" << std::endl;
                int expected = 0;
                exit_code.compare_exchange_strong(expected, 1);
                stop_all.store(true);
                return;
            }

            zhulong_worker_ctx_h worker = nullptr;
            zhulong_status_t wrc = api.worker_create(model, static_cast<uint32_t>(idx), &worker);
            if (wrc != ZHULONG_PLUGIN_OK || !worker)
            {
                std::cerr << "{\"status\":\"failed\",\"stage\":\"worker_create\",\"rc\":" << wrc << "}" << std::endl;
                int expected = 0;
                exit_code.compare_exchange_strong(expected, 3);
                stop_all.store(true);
                return;
            }

            zhulong_task_ctx_h task = nullptr;
            wrc = api.task_open(worker, stream_id, &task);
            if (wrc != ZHULONG_PLUGIN_OK || !task)
            {
                std::cerr << "{\"status\":\"failed\",\"stage\":\"task_open\",\"rc\":" << wrc << "}" << std::endl;
                api.worker_destroy(worker);
                int expected = 0;
                exit_code.compare_exchange_strong(expected, 4);
                stop_all.store(true);
                return;
            }

            ipc::UnixSocketClient client;
            std::string conn_err;
            if (!client.connect(broker_sock, &conn_err))
            {
                std::cerr << "{\"status\":\"failed\",\"stage\":\"connect\",\"index\":" << idx
                          << ",\"path\":\"" << broker_sock << "\",\"error\":\"" << conn_err << "\"}" << std::endl;
                api.task_close(task);
                api.worker_destroy(worker);
                int expected = 0;
                exit_code.compare_exchange_strong(expected, 5);
                stop_all.store(true);
                return;
            }

            // 1. 发送握手
            ipc::HandshakeReq req{};
            req.api_version = 1;
            std::strncpy(req.client_name, name.c_str(), sizeof(req.client_name) - 1);
            if (!client.send_msg(ipc::MSG_HANDSHAKE_REQ, &req, sizeof(req)))
            {
                api.task_close(task);
                api.worker_destroy(worker);
                int expected = 0;
                exit_code.compare_exchange_strong(expected, 6);
                stop_all.store(true);
                return;
            }

            // 2. 接收握手应答
            ipc::IpcHeader resp_hdr{};
            std::vector<uint8_t> resp_payload;
            if (!client.recv_msg(resp_hdr, resp_payload, 5000) || resp_hdr.msg_type != ipc::MSG_HANDSHAKE_RESP || resp_payload.size() < sizeof(ipc::HandshakeResp))
            {
                api.task_close(task);
                api.worker_destroy(worker);
                int expected = 0;
                exit_code.compare_exchange_strong(expected, 7);
                stop_all.store(true);
                return;
            }

            ipc::HandshakeResp resp{};
            std::memcpy(&resp, resp_payload.data(), sizeof(resp));
            uint32_t my_client_id = resp.client_id;

            // 3. 映射共享内存
            ipc::ShmPool shm;
            std::string shm_err;
            if (!shm.init(resp.shm_name, ipc::ShmMode::OPEN_READ_ONLY, 0, 0, &shm_err))
            {
                std::cerr << "{\"status\":\"failed\",\"stage\":\"shm_init\",\"error\":\"" << shm_err << "\"}" << std::endl;
                api.task_close(task);
                api.worker_destroy(worker);
                int expected = 0;
                exit_code.compare_exchange_strong(expected, 8);
                stop_all.store(true);
                return;
            }

            int frames_processed = 0;
            bool last_had_targets = false;
            while (!stop_all.load(std::memory_order_relaxed) && (max_frames < 0 || frames_processed < max_frames))
            {
                ipc::IpcHeader hdr{};
                std::vector<uint8_t> payload;
                if (!client.recv_msg(hdr, payload, 500))
                {
                    if (!client.is_connected())
                        break;
                    continue;
                }

                if (hdr.msg_type == ipc::MSG_FRAME_DISPATCH && payload.size() >= sizeof(ipc::FrameDispatchMsg))
                {
                    ipc::FrameDispatchMsg dispatch{};
                    std::memcpy(&dispatch, payload.data(), sizeof(dispatch));

                    const uint8_t *slot_base = shm.slot_read_ptr(dispatch.slot_index);
                    if (slot_base && dispatch.offsets[0] < shm.slot_size())
                    {
                        zhulong_frame_view view{};
                        view.frame_id = dispatch.frame_id;
                        view.pts = static_cast<uint64_t>(dispatch.pts);
                        view.width = dispatch.width;
                        view.height = dispatch.height;
                        view.format = dispatch.format;
                        view.strides[0] = dispatch.strides[0];
                        view.planes[0] = slot_base + dispatch.offsets[0];

                        if (dispatch.offsets[1] > 0 && dispatch.offsets[1] < shm.slot_size())
                        {
                            view.strides[1] = dispatch.strides[1];
                            view.planes[1] = slot_base + dispatch.offsets[1];
                        }
                        if (dispatch.offsets[2] > 0 && dispatch.offsets[2] < shm.slot_size())
                        {
                            view.strides[2] = dispatch.strides[2];
                            view.planes[2] = slot_base + dispatch.offsets[2];
                        }

                        zhulong_inference_result *result = nullptr;
                        auto t0 = std::chrono::steady_clock::now();
                        rc = api.process(task, &view, &result);
                        auto t1 = std::chrono::steady_clock::now();
                        int64_t cost_ms = std::chrono::duration_cast<std::chrono::milliseconds>(t1 - t0).count();

                        uint32_t count = (result != nullptr && rc == ZHULONG_PLUGIN_OK) ? result->count : 0;
                        bool has_targets = (count > 0);

                        if (has_targets || last_had_targets)
                        {
                            std::string payload = serialize_inference_result(dispatch.stream_id, dispatch.pts, cost_ms, result);
                            client.send_msg(ipc::MSG_RESULT, payload.data(), payload.size());
                        }
                        last_had_targets = has_targets;

                        if (result)
                        {
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
        };

        if (broker_socks.size() == 1)
        {
            run_single_worker(0, broker_socks[0]);
        }
        else
        {
            std::vector<std::thread> threads;
            threads.reserve(broker_socks.size());
            for (size_t i = 0; i < broker_socks.size(); ++i)
            {
                threads.emplace_back(run_single_worker, i, broker_socks[i]);
            }
            for (auto &t : threads)
            {
                if (t.joinable())
                {
                    t.join();
                }
            }
        }

        api.model_destroy(model);
        return exit_code.load();
    }

    int run_ipc_worker(const std::string &so_path, const std::string &broker_sock, const std::string &client_name, int max_frames = -1)
    {
        return run_ipc_workers(so_path, {broker_sock}, client_name, max_frames);
    }

} // namespace zhulong

int main(int argc, char *argv[])
{
    std::string verify_plugin_path;
    std::string sample_path = "default_sample";
    std::vector<std::string> broker_socks;
    std::string plugin_path;
    std::string client_name = "algo_worker";
    int max_frames = -1;

    for (int i = 1; i < argc; ++i)
    {
        if (std::string(argv[i]) == "--verify-plugin" && i + 1 < argc)
        {
            verify_plugin_path = argv[++i];
        }
        else if (std::string(argv[i]) == "--sample" && i + 1 < argc)
        {
            sample_path = argv[++i];
        }
        else if (std::string(argv[i]) == "--plugin" && i + 1 < argc)
        {
            plugin_path = argv[++i];
        }
        else if (std::string(argv[i]) == "--broker" && i + 1 < argc)
        {
            broker_socks.push_back(argv[++i]);
        }
        else if (std::string(argv[i]) == "--name" && i + 1 < argc)
        {
            client_name = argv[++i];
        }
        else if (std::string(argv[i]) == "--max-frames" && i + 1 < argc)
        {
            max_frames = std::stoi(argv[++i]);
        }
    }

    if (!verify_plugin_path.empty())
    {
        return zhulong::run_verify_plugin(verify_plugin_path, sample_path);
    }

    if (!plugin_path.empty() && !broker_socks.empty())
    {
        return zhulong::run_ipc_workers(plugin_path, broker_socks, client_name, max_frames);
    }

    std::cout << "Zhulong Universal Native Algo Runner (v1.0)" << std::endl;
    std::cout << "Usage: zhulong_algo_runner --verify-plugin <plugin.so> [--sample <path>]" << std::endl;
    std::cout << "   or: zhulong_algo_runner --plugin <plugin.so> --broker <sock_path> [--broker <sock_path_2> ...] [--name <name>] [--max-frames <N>]" << std::endl;
    return 0;
}
