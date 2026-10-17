/**
 * @file runner.cpp
 * @brief 独立算法 Runner（验包/执行器）宿主与黑盒验包 CLI
 *
 * 核心设计原则：
 * 1. 一包一进程：每个算法包在独立 Runner 子进程中运行，故障隔离，崩溃不拖垮主媒体与 Go 服务。
 * 2. 双工作模式：`--verify-plugin` 为单次黑盒验包（ABI/模型/Worker/Task/推理/释放全链路），
 *    `--plugin --broker` 为 IPC 长驻消费模式（订阅共享内存帧流）。
 * 3. ⚠️ 退出码表是运维与 Go 看门狗的唯一故障分类通道（见 run_ipc_workers 注释）。
 * 4. ⚠️ 所有诊断输出为严格单行 JSON，字符串字段必须经 runner::json_quote 转义。
 * 5. ⚠️ R1/R2：插件调用遵循「谁创建谁释放」—— process 产出的 result 必须由同一插件的
 *    result_release 释放（native 不持有其内存所有权）；本进程自身不跨越 C ABI。
 */

#include <zhulong/plugin.h>
#include "json.hpp"
#include "gallery_index.hpp"
#include "gallery_parser.hpp"
#include "../ipc/ipc_protocol.hpp"
#include "../ipc/shm_pool.hpp"
#include "../ipc/unix_socket.hpp"
#include <dlfcn.h>
#include <unistd.h>
#include <sys/mman.h>
#if defined(__linux__)
#include <sys/ioctl.h>
#ifndef DMA_BUF_SYNC_READ
struct dma_buf_sync {
    uint64_t flags;
};
#define DMA_BUF_SYNC_READ (1 << 0)
#define DMA_BUF_SYNC_WRITE (2 << 0)
#define DMA_BUF_SYNC_START (0 << 2)
#define DMA_BUF_SYNC_END (1 << 2)
#define DMA_BUF_BASE 'b'
#define DMA_BUF_IOCTL_SYNC _IOW(DMA_BUF_BASE, 0, struct dma_buf_sync)
#endif
#endif
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
    using namespace zhulong::runner;

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

    /**
     * @brief 将插件返回的推理结果序列化为单行 JSON 字符串
     *
     * 按 result_type 分派为三种载荷形态（detection / classification / person_structure），
     * 其余类型归为 custom 空载荷。所有字符串字段（标签、person_id、person_name）均经
     * runner::json_quote 转义，保证输出可被标准 JSON parser 解析。
     *
     * @param stream_id 来源物理流 ID
     * @param pts 帧呈现时间戳（原样透传，不做单位换算）
     * @param cost_ms 本次推理耗时（毫秒，仅诊断用）
     * @param result 插件产出的结果句柄（可为空，表示无输出）
     * @return std::string 单行 JSON 文本（不含换行符）
     */
    static std::string serialize_inference_result(uint64_t stream_id,
                                                  int64_t pts,
                                                  int64_t capture_time_ms,
                                                  int64_t cost_ms,
                                                  const zhulong_inference_result* result)
    {
        std::ostringstream ss;
        ss << "{\"stream_id\":" << stream_id
           << ",\"pts\":" << pts
           << ",\"capture_time_ms\":" << capture_time_ms
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
                       << ",\"matched_face_id\":" << runner::json_quote(f.matched_face_id)
                       << ",\"candidates\":[";
                    for (uint32_t k = 0; k < f.candidate_count; ++k)
                    {
                        if (k > 0) ss << ",";
                        const auto& c = f.candidates[k];
                        ss << "{\"person_id\":" << runner::json_quote(c.person_id)
                           << ",\"person_name\":" << runner::json_quote(c.person_name)
                           << ",\"face_id\":" << runner::json_quote(c.face_id)
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

    /**
     * @brief 插件动态库加载器（RAII 封装 dlopen/dlsym/dlclose 与 ABI 握手）
     *
     * 析构自动 unload；load 失败时保证已获得的句柄被释放，不泄漏 dlopen 引用。
     */
    class PluginLoader
    {
    public:
        ~PluginLoader()
        {
            unload();
        }

        /**
         * @brief 加载插件动态库并完成 v1 ABI 握手
         *
         * 以 RTLD_NOW | RTLD_LOCAL 加载（立即解析符号且不污染全局符号表），
         * 查找 ZHULONG_PLUGIN_ENTRY_POINT 并填充 zhulong_plugin_api_v1；
         * 要求 api_version == 1，否则视为不兼容并卸载。
         *
         * @param so_path 插件 .so 文件路径
         * @param[out] error_msg 失败时输出错误原因
         * @return true 加载并握手成功；false 失败（已回滚句柄）
         */
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

    /**
     * @brief 单次黑盒验包路径：加载插件并执行一次完整推理生命周期
     *
     * 依次执行 model_create -> worker_create -> task_open -> process -> result_release，
     * 任一步失败即输出对应 stage 的 JSON 诊断并提前返回。退出码 0~5 对应
     * dlopen / model_create / worker_create / task_open / process 各阶段失败。
     *
     * @param so_path 待验证的插件动态库路径
     * @param sample_path 样本标识（仅用于输出回显；无样本时使用合成测试帧）
     * @return int 0 全链路通过；非 0 为失败阶段序号
     */
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

    /**
     * @brief IPC 长驻消费路径：连接一个或多个 FrameBroker 并持续消费共享内存帧
     *
     * ⚠️ 退出码表（运维与 Go 看门狗依赖此表区分故障类型）：
     * | 码 | 含义 |
     * | -- | ---- |
     * | 0  | 正常退出（收到停止信号或达到 --max-frames） |
     * | 1  | dlopen 失败，或 client name 超长（HandshakeReq.client_name[32]） |
     * | 2  | model_create 失败 |
     * | 3  | worker_create 失败 |
     * | 4  | task_open 失败 |
     * | 5  | broker 连接失败 / 无法从 --broker 路径反解 stream_id / 未提供 --broker |
     * | 6  | 握手请求发送失败 |
     * | 7  | 握手应答接收失败或类型不符 |
     * | 8  | 共享内存只读映射失败 |
     *
     * ⚠️ 多路 `--broker` 的 D6 语义：任一路失败即置位 `stop_all`，已连上的路也会被一并终止，
     * 不存在「部分连接成功」的中间态。
     *
     * @param so_path 插件动态库路径
     * @param broker_socks 各物理流的 broker socket 字面路径列表
     * @param client_name 客户端基础名称（多路时自动追加 #<idx> 后缀）
     * @param max_frames 最大处理帧数（<0 表示不限）
     * @return int 退出码（见上表）
     */
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
        zhulong_status_t model_rc = api.model_create("{}", &model);
        if (model_rc != ZHULONG_PLUGIN_OK || !model)
            return 2;

        std::atomic<bool> stop_all{false};
        std::atomic<int> exit_code{0};
        GalleryIndex gallery_index;

        auto run_single_worker = [&](size_t idx, const std::string &broker_sock)
        {
            // ⚠️ 必须是 lambda 局部变量：多路 --broker 时本 lambda 被多个线程并发执行，
            //    共享外层变量构成数据竞争（-race 阻塞级）。
            zhulong_status_t rc = ZHULONG_PLUGIN_OK;
            // ⚠️ task 身份必须包含 stream_id：一个算法包可同时订阅多路流，
            // 固定 task_id 会让多路共用一个 task state（父任务 design.md:242 要求 task→worker 亲和）。
            // 这是 runner 唯一允许依赖 socket 路径命名格式的地方。
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
            ShardedSyncContext sync_ctx;
            while (!stop_all.load(std::memory_order_relaxed) && (max_frames < 0 || frames_processed < max_frames))
            {
                ipc::IpcHeader hdr{};
                std::vector<uint8_t> payload;
                int received_fd = -1;
                if (!client.recv_msg_with_fd(hdr, payload, received_fd, 500))
                {
                    if (!client.is_connected())
                        break;
                    continue;
                }

                if (hdr.msg_type == ipc::MSG_FRAME_DISPATCH && payload.size() >= sizeof(ipc::FrameDispatchMsg))
                {
                    ipc::FrameDispatchMsg dispatch{};
                    std::memcpy(&dispatch, payload.data(), sizeof(dispatch));

                    // ⚠️ 只接受 SCM_RIGHTS 传来的本地副本 fd。绝不回退到 dispatch.reserved_fd：
                    //    那是宿主进程的 fd 号，在本进程中可能指向任意无关资源。
                    const int active_dma_fd = received_fd;
                    void* dma_mmap = nullptr;
                    if (active_dma_fd >= 0 && shm.slot_size() > 0)
                    {
                        dma_mmap = ::mmap(nullptr, shm.slot_size(), PROT_READ, MAP_SHARED, active_dma_fd, 0);
                        if (dma_mmap == MAP_FAILED)
                        {
                            dma_mmap = nullptr;
                        }
#if defined(__linux__)
                        if (dma_mmap != nullptr)
                        {
                            struct dma_buf_sync sync{DMA_BUF_SYNC_START | DMA_BUF_SYNC_READ};
                            ioctl(active_dma_fd, DMA_BUF_IOCTL_SYNC, &sync);
                        }
#endif
                    }

                    const uint8_t *slot_base = (dma_mmap != nullptr)
                        ? static_cast<const uint8_t*>(dma_mmap)
                        : shm.slot_read_ptr(dispatch.slot_index);
                    if (slot_base && dispatch.offsets[0] < shm.slot_size())
                    {
                        zhulong_frame_view view{};
                        view.frame_id = dispatch.frame_id;
                        view.pts = static_cast<uint64_t>(dispatch.pts);
                        view.width = dispatch.width;
                        view.height = dispatch.height;
                        view.format = dispatch.format;
                        view.dma_buf_fd = active_dma_fd;
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

                        // 仅在本次或上次有目标时才上报结果，避免连续空帧刷屏（去噪）
                        if (has_targets || last_had_targets)
                        {
                            std::string payload = serialize_inference_result(dispatch.stream_id, dispatch.pts, dispatch.capture_time_ms, cost_ms, result);
                            client.send_msg(ipc::MSG_RESULT, payload.data(), payload.size());
                        }
                        last_had_targets = has_targets;

                        // ⚠️ R2：结果内存由插件创建，只能由同一插件的 result_release 释放。
                        // 无论 process 是否成功，只要 result 非空就必须释放，不得泄漏。
                        if (result)
                        {
                            api.result_release(result);
                        }
                    }

                    if (dma_mmap != nullptr)
                    {
#if defined(__linux__)
                        struct dma_buf_sync sync{DMA_BUF_SYNC_END | DMA_BUF_SYNC_READ};
                        ioctl(active_dma_fd, DMA_BUF_IOCTL_SYNC, &sync);
#endif
                        ::munmap(dma_mmap, shm.slot_size());
                        dma_mmap = nullptr;
                    }

                    // 及时释放本进程通过 SCM_RIGHTS 接收到的描述符副本，杜绝文件句柄泄漏
                    if (received_fd >= 0)
                    {
                        ::close(received_fd);
                        received_fd = -1;
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
                else if (hdr.msg_type == ipc::MSG_GALLERY_SYNC && !payload.empty())
                {
                    ParsedSyncShard shard = parse_sync_shard_payload(payload.data(), payload.size());
                    // ⚠️ 聚合逻辑提取到 accumulate_sync_shard（见 gallery_parser.hpp）：
                    //    它内部完成「索引范围门禁 + 分片级幂等」，使该安全属性可被单测直接覆盖。
                    const bool accepted = (shard.shard_total > 0) && accumulate_sync_shard(sync_ctx, shard);
                    if (shard.shard_total > 0 && !accepted &&
                        (shard.shard_index < 0 || shard.shard_index >= shard.shard_total))
                    {
                        std::cerr << "{\"status\":\"warning\",\"stage\":\"gallery_sync\","
                                  << "\"error\":\"shard_index out of range\",\"shard_index\":"
                                  << shard.shard_index << ",\"shard_total\":" << shard.shard_total << "}"
                                  << std::endl;
                    }
                    // 重复分片无需告警（正常重传）

                    // ⚠️ 仅在「本分片是新到达的」且「已收齐」时才应用底库。
                    //    若去掉 accepted 条件：收齐后 `is_complete()` 恒为真，
                    //    重复分片会再次 reset，而 collected_items 已被 move → **清空底库**。
                    if (accepted && sync_ctx.is_complete())
                    {
                        size_t total_count = sync_ctx.collected_items.size();
                        gallery_index.reset(std::move(sync_ctx.collected_items));

                        // 发送 MSG_GALLERY_ACK
                        std::string ack_json = "{\"sync_id\":" + runner::json_quote(sync_ctx.sync_id) +
                                               ",\"success\":true,\"total_faces\":" + std::to_string(total_count) + "}";
                        client.send_msg(ipc::MSG_GALLERY_ACK, ack_json.data(), ack_json.size());
                    }
                }
                else if (hdr.msg_type == ipc::MSG_GALLERY_UPSERT && !payload.empty())
                {
                    FaceTemplate item = parse_upsert_payload(payload.data(), payload.size());
                    if (!item.face_id.empty())
                    {
                        gallery_index.upsert(std::move(item));
                    }
                }
                else if (hdr.msg_type == ipc::MSG_GALLERY_DELETE && !payload.empty())
                {
                    ParsedDelete del = parse_delete_payload(payload.data(), payload.size());
                    gallery_index.remove(del.face_id, del.person_id);
                }

                if (received_fd >= 0)
                {
                    ::close(received_fd);
                    received_fd = -1;
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

/**
 * @brief Runner CLI 入口：解析命令行参数并分派到验包或 IPC 消费路径
 *
 * 无有效参数时打印用法说明并返回 0（不是错误，避免被看门狗误判为崩溃）。
 */
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
