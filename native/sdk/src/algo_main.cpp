/**
 * @file algo_main.cpp
 * @brief 算法包可执行文件骨架的实现（静态链接进包，宿主不再 dlopen）
 *
 * 代码自宿主的通用 runner 迁移而来（该 runner 已随 dlopen 通道一起下线），语义必须与交付形态一致：
 *   - stage token、退出码表、ACK 时机、去噪策略（只在有目标时上报）逐字保留；
 *   - 唯一差异：本文件**不**加载 `.so`，API 表由 `ZHULONG_ALGO_MAIN` 生成的
 *     `PluginApi<...>::get_api` 直接提供（算法与骨架在同一个二进制里）。
 *
 * 归属：编译进 `libzhulong_algo_sdk.a`，随 SDK 前缀分发给包作者。
 */

#include <zhulong/algo_main.hpp>
#include <zhulong/plugin.h>

#include "common/log.hpp"
#include "internal/json.hpp"
#include "internal/gallery_index.hpp"
#include "internal/gallery_parser.hpp"
#include "internal/motion_gate.hpp"
#include "internal/stall_watch.hpp"
#include "internal/worker_guard.hpp"
#include "algorithm/ipc/ipc_protocol.hpp"
#include "algorithm/ipc/shm_pool.hpp"
#include "algorithm/ipc/unix_socket.hpp"
// DMA_BUF_IOCTL_SYNC 的 UAPI 兜底定义全仓唯一来源，不要在这里再抄一份
#include "platform/dmabuf/dmabuf_sync.hpp"
#include <zhulong/runtime/runtime_factory.hpp>

#include <pthread.h>
#include <unistd.h>
#include <sys/mman.h>

#include <atomic>
#include <chrono>
#include <cmath>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <exception>
#include <fstream>
#include <iomanip>
#include <iostream>
#include <memory>
#include <sstream>
#include <string>
#include <thread>
#include <utility>
#include <vector>

namespace zhulong
{
namespace sdk
{
namespace detail
{
    using namespace zhulong::runner;

    template <typename... Parts>
    void write_runner_error_line(Parts &&...parts)
    {
        std::ostringstream output;
        (output << ... << std::forward<Parts>(parts));
        std::string line = output.str();
        line.push_back('\n');
        write_runner_stderr(line.data(), line.size());
    }

    /**
     * @brief 协议写端（`--verify` / `--extract-feature` / broker 首行 JSON）
     *
     * 为何不直接用 `std::cout`：stdout 是宿主按行解析的**协议通道**，而厂商运行库会
     * 往里打启动横幅 —— 实测 librga 在建后端时打印 `rga_api version 1.10.1_[10]`，
     * 宿主读到非 JSON 行就判 `errProtocolDesync` 并杀掉热进程，最终表现为
     * 「算法包 --extract-feature 永远失败」（`internal/faceextract/client.go` 的 parseReply）。
     *
     * 因此启动时先 dup 一份 fd 专供协议，再把 fd1 改道 stderr：C 层 `printf`、厂商库
     * 横幅、误用的 `std::cout` 全部归日志（宿主 `cmd.Stderr = os.Stderr` 会继承并落盘），
     * 协议与噪声物理隔离，不再靠「库应该守规矩」这种假设。
     */
    std::FILE *g_protocol_stream = nullptr;

    bool install_protocol_channel()
    {
        if (g_protocol_stream != nullptr)
        {
            return true;
        }
        const int protocol_fd = ::dup(STDOUT_FILENO);
        if (protocol_fd < 0)
        {
            return false;
        }
        std::FILE *stream = ::fdopen(protocol_fd, "w");
        if (stream == nullptr)
        {
            ::close(protocol_fd);
            return false;
        }
        // 行缓冲：每条结果行立即送达宿主，不等 4KB 缓冲满。
        std::setvbuf(stream, nullptr, _IOLBF, 0);
        if (::dup2(STDERR_FILENO, STDOUT_FILENO) < 0)
        {
            std::fclose(stream);
            return false;
        }
        g_protocol_stream = stream;
        return true;
    }

    /** @brief 往协议通道写一整行（自带换行与 flush）；未初始化时退回 stdout） */
    void write_protocol_line(const std::string &line)
    {
        std::FILE *stream = g_protocol_stream != nullptr ? g_protocol_stream : stdout;
        std::fwrite(line.data(), 1, line.size(), stream);
        std::fputc('\n', stream);
        std::fflush(stream);
    }

    /**
     * @brief 给当前线程设一个可分辨的内核线程名（`ps -L` / gdb / perf 用）
     *
     * 平台差异收敛在这里：Linux 的 `pthread_setname_np` 作用于**指定**线程（需显式传
     * `pthread_self()`），macOS 的同名函数只作用于**调用线程**且不接受线程参数。两者都在
     * 线程入口调用，因此语义一致。
     */
    inline void set_thread_name(const char *name) noexcept
    {
        // 内核上限 16 字节（含结尾 NUL）。超长会被截断或返回 ERANGE，调用方自己裁短。
        char trimmed[16] = {};
        std::snprintf(trimmed, sizeof(trimmed), "%s", name != nullptr ? name : "");
#if defined(__linux__)
        ::pthread_setname_np(::pthread_self(), trimmed);
#elif defined(__APPLE__)
        ::pthread_setname_np(trimmed);
#else
        (void)trimmed;
#endif
    }

    /**
     * @brief Worker 线程名：`algo-w<idx>`，下标与控制消息里的 `index` 字段一致
     *
     * 多路订阅下所有 Worker 都是同一个可执行文件的线程，没有名字时排障只能靠 CPU 占用
     * 猜「哪一路卡住了」。
     */
    inline void set_worker_thread_name(size_t idx) noexcept
    {
        char name[16] = {};
        std::snprintf(name, sizeof(name), "algo-w%zu", idx);
        set_thread_name(name);
    }

    /**
     * @brief 安装最后一道 terminate 兜底
     *
     * Worker 线程体内的异常由 `run_worker_guarded` 收口；但仍有几处例外不在它的边界内：
     * `std::thread` 构造抛 `std::system_error`（线程资源耗尽）、`noexcept` 函数里抛异常、
     * 以及未来新增代码的疏漏。这些路径的默认结局是 `std::terminate` → SIGABRT(134)：
     * 退出码没有语义、stage 日志一条都没有，宿主只能记「runner exited unexpectedly」。
     *
     * 这个 handler 把同一种故障翻译成与 `run_worker_guarded` **同一个**退出码和 stage，
     * 让宿主侧不需要区分两条路径。
     *
     * ⚠️ 这里**不能**用 `write_runner_error_line`：它构造 `ostringstream`（二次分配失败会
     *    再抛一次，直接嵌套 terminate → abort），并且要拿 log.cpp 的 `g_stderr_write_mutex`
     *    —— terminate 发生时该锁很可能正被当前线程持有，那就是死锁。只允许一次不分配的
     *    `::write` 把诊断钉在 stderr 上。
     */
    inline void install_terminate_guard() noexcept
    {
        std::set_terminate([]() noexcept {
            static constexpr char kRecord[] =
                "{\"status\":\"failed\",\"stage\":\"worker_exception\",\"error\":\"terminate\"}\n";
            const ssize_t written = ::write(STDERR_FILENO, kRecord, sizeof(kRecord) - 1);
            (void)written;
            std::_Exit(kExitWorkerCrashed);
        });
    }

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
                                                  const zhulong_inference_result* result,
                                                  const std::string& algorithm_id = "")
    {
        std::ostringstream ss;
        ss << "{\"stream_id\":" << stream_id
           << ",\"pts\":" << pts
           << ",\"capture_time_ms\":" << capture_time_ms
           << ",\"cost_ms\":" << cost_ms
           << ",\"algorithm_id\":" << runner::json_quote(algorithm_id);

        if (!result || result->count == 0)
        {
            ss << ",\"task_type\":\"\""
               << ",\"detections\":[]"
               << ",\"classifications\":[]"
               << ",\"persons\":[]"
               << ",\"vehicles\":[]}";
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
            ss << "],\"classifications\":[],\"persons\":[],\"vehicles\":[]}";
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
            ss << "],\"persons\":[],\"vehicles\":[]}";
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
            ss << "],\"vehicles\":[]}";
        }
        else if (result->result_type == ZHULONG_RESULT_TYPE_VEHICLE_STRUCTURE)
        {
            ss << ",\"task_type\":\"vehicle_structure\""
               << ",\"detections\":[],\"classifications\":[],\"persons\":[]"
               << ",\"vehicles\":[";
            const auto* vehicles = static_cast<const zhulong_vehicle_item*>(result->data);
            for (uint32_t i = 0; i < result->count; ++i)
            {
                if (i > 0) ss << ",";
                const auto& v = vehicles[i];
                ss << "{\"track_id\":" << v.track_id
                   << ",\"vehicle_box\":[" << v.vehicle_box[0] << "," << v.vehicle_box[1] << ","
                   << v.vehicle_box[2] << "," << v.vehicle_box[3] << "]"
                   << ",\"vehicle_score\":" << v.vehicle_score
                   << ",\"vehicle_type\":" << runner::json_quote(v.vehicle_type)
                   << ",\"vehicle_color\":" << runner::json_quote(v.vehicle_color)
                   << ",\"plates\":[";
                for (uint32_t j = 0; j < v.plate_count; ++j)
                {
                    if (j > 0) ss << ",";
                    const auto& pl = v.plates[j];
                    ss << "{\"box\":[" << pl.box[0] << "," << pl.box[1] << ","
                       << pl.box[2] << "," << pl.box[3] << "]"
                       << ",\"score\":" << pl.score
                       << ",\"plate_number\":" << runner::json_quote(pl.plate_number)
                       << ",\"plate_color\":" << runner::json_quote(pl.plate_color)
                       << ",\"plate_type\":" << pl.plate_type << "}";
                }
                ss << "]}";
            }
            ss << "]}";
        }
        else
        {
            ss << ",\"task_type\":\"custom\",\"detections\":[],\"classifications\":[],\"persons\":[],\"vehicles\":[]}";
        }

        return ss.str();
    }

    /**
     * @brief 验包用的样本帧（PPM/PGM 解码结果）
     */
    struct SampleFrame
    {
        int width{0};
        int height{0};
        std::vector<uint8_t> rgb; /**< 紧凑 RGB24（PGM 单通道展开为 RGB） */
        bool decoded() const noexcept { return width > 0 && height > 0 && !rgb.empty(); }
    };

    /**
     * @brief 读取 netpbm 头字段，跳过 PPM/PGM 注释行
     */
    bool read_netpbm_header(std::istream &input, std::string &magic, int &width, int &height, int &max_value)
    {
        if (!(input >> magic) || (magic != "P6" && magic != "P5"))
        {
            return false;
        }
        auto next_int = [&input](int &value) -> bool {
            while (true)
            {
                input >> std::ws;
                if (input.peek() == '#')
                {
                    std::string ignored;
                    std::getline(input, ignored);
                    continue;
                }
                return static_cast<bool>(input >> value);
            }
        };
        if (!next_int(width) || !next_int(height) || !next_int(max_value))
        {
            return false;
        }
        return width > 0 && height > 0 && max_value > 0 && max_value <= 255;
    }

    /**
     * @brief 解码 netpbm 样本帧（P6 RGB / P5 灰度）为紧凑 RGB24
     *
     * ⚠️ 刻意不引入 JPEG/PNG 解码器：Runner 不链接 FFmpeg，也不应有图像解码依赖。
     * 需要「真实样本帧」的调用方（Go 侧验包门禁、开发机自测）负责把图转成 netpbm。
     * 解码失败不是致命错误：回退合成帧并输出 sample_decoded=false，绝不假装验过样本。
     */
    SampleFrame load_sample_frame(const std::string &path)
    {
        SampleFrame frame;
        std::ifstream input(path, std::ios::binary);
        if (!input.is_open())
        {
            return frame;
        }

        std::string magic;
        int max_value = 0;
        if (!read_netpbm_header(input, magic, frame.width, frame.height, max_value))
        {
            frame.width = 0;
            frame.height = 0;
            return frame;
        }
        input.get(); // 头部与像素数据之间的单个空白符

        const size_t pixels = static_cast<size_t>(frame.width) * static_cast<size_t>(frame.height);
        if (magic == "P6")
        {
            frame.rgb.resize(pixels * 3);
            input.read(reinterpret_cast<char *>(frame.rgb.data()), static_cast<std::streamsize>(frame.rgb.size()));
            if (input.gcount() != static_cast<std::streamsize>(frame.rgb.size()))
            {
                frame.rgb.clear();
            }
        }
        else
        {
            std::vector<uint8_t> gray(pixels);
            input.read(reinterpret_cast<char *>(gray.data()), static_cast<std::streamsize>(gray.size()));
            if (input.gcount() != static_cast<std::streamsize>(gray.size()))
            {
                return SampleFrame{};
            }
            frame.rgb.resize(pixels * 3);
            for (size_t i = 0; i < pixels; ++i)
            {
                frame.rgb[i * 3 + 0] = gray[i];
                frame.rgb[i * 3 + 1] = gray[i];
                frame.rgb[i * 3 + 2] = gray[i];
            }
        }
        if (frame.rgb.size() != pixels * 3)
        {
            return SampleFrame{};
        }
        return frame;
    }

    /**
     * @brief 当前进程的推理后端标识（诊断用，来自编译期后端选择）
     */
    std::string inference_backend_name()
    {
        return std::string(zhulong::default_inference_backend_name());
    }

    /**
     * @brief 单次黑盒验包路径：加载插件并执行一次完整推理生命周期
     *
     * 依次执行 model_create -> worker_create -> task_open -> process -> result_release，
     * 任一步失败即输出对应 stage 的 JSON 诊断并提前返回。退出码 0~5 对应
     * api_table / model_create / worker_create / task_open / process 各阶段失败。
     *
     * 样本帧语义：`--sample` 指向 PPM/PGM 时**真的**把这张图喂给插件（`sample_decoded=true`）；
     * 其余格式（如包里约定的 sample.jpg）Runner 无法解码，回退合成帧并显式标记
     * `sample_decoded=false` —— 宁可暴露「没验到真实样本」，也不静默伪造一次通过。
     *
     * @param api 已构造好的 C ABI 表（算法与骨架同进程，没有 .so 可加载）
     * @param sample_path 样本帧路径（PPM/PGM 会被真实解码）
     * @return int 0 全链路通过；非 0 为失败阶段序号
     */
    // ======================================================================
    //  --extract-feature：宿主底库登记用的单次特征提取通道
    // ======================================================================
    //
    // 为什么是弱符号：能力由**包**提供（face 包实现了它，plate 包不实现）。宿主的旧通道
    // 走 `dlopen(.so) + dlsym("zhulong_face_extract_feature")`，而 PIE 可执行文件无法
    // 被 dlopen，所以改成「骨架弱引用 + 包内定义静态链入」。
    //
    // ⚠️ 弱引用必须由包**直接编译的源文件**提供定义：GNU ld 不会为满足弱未定义引用去
    //    抽取静态归档成员。把定义搬进 .a 会让 face 包静默退化成 UNSUPPORTED（底库登记
    //    功能消失且没有错误日志）——验收门禁就是「face 包喂一帧不得返回 UNSUPPORTED」。
    extern "C" __attribute__((weak)) zhulong_status_t zhulong_face_extract_feature(
        const uint8_t *pixel_data,
        int32_t width,
        int32_t height,
        int32_t stride,
        int32_t format,
        float *out_features,
        uint32_t *feature_dim);

    namespace
    {
        constexpr uint32_t kExtractFrameMagic = 0x45464C5AU; // ASCII "ZLFE"（小端读作 u32）
        constexpr uint32_t kExtractHeaderBytes = 28;
        constexpr uint32_t kExtractMaxDimension = 16384;
        constexpr uint32_t kExtractMaxFeatureDim = 512;

        struct ExtractFrameHeader
        {
            uint32_t magic{0};
            uint32_t request_id{0};
            uint32_t width{0};
            uint32_t height{0};
            uint32_t stride{0};
            uint32_t format{0};
            uint32_t payload_len{0};
        };

        uint32_t read_le_u32(const uint8_t *bytes)
        {
            return static_cast<uint32_t>(bytes[0]) | (static_cast<uint32_t>(bytes[1]) << 8) |
                   (static_cast<uint32_t>(bytes[2]) << 16) | (static_cast<uint32_t>(bytes[3]) << 24);
        }

        /** @brief 读一个完整帧头；EOF（干净的流结束）与半帧（截断）分别返回 false */
        bool read_extract_header(std::istream &input, ExtractFrameHeader &header, bool &clean_eof)
        {
            uint8_t raw[kExtractHeaderBytes];
            input.read(reinterpret_cast<char *>(raw), sizeof(raw));
            const auto got = static_cast<uint32_t>(input.gcount());
            if (got == 0)
            {
                clean_eof = true;
                return false;
            }
            clean_eof = false;
            if (got != sizeof(raw))
            {
                return false;
            }
            header.magic = read_le_u32(raw + 0);
            header.request_id = read_le_u32(raw + 4);
            header.width = read_le_u32(raw + 8);
            header.height = read_le_u32(raw + 12);
            header.stride = read_le_u32(raw + 16);
            header.format = read_le_u32(raw + 20);
            header.payload_len = read_le_u32(raw + 24);
            return true;
        }

        void emit_extract_failure(uint32_t request_id, const char *code, const std::string &error)
        {
            std::ostringstream output;
            output << "{\"request_id\":" << request_id << ",\"status\":\"failed\",\"code\":"
                   << runner::json_quote(code) << ",\"error\":" << runner::json_quote(error) << "}";
            write_protocol_line(output.str());
        }

        void emit_extract_result(uint32_t request_id, const float *features, uint32_t dim)
        {
            std::ostringstream output;
            output << "{\"request_id\":" << request_id << ",\"status\":\"passed\",\"feature_dim\":" << dim
                   << ",\"features\":[";
            for (uint32_t i = 0; i < dim; ++i)
            {
                if (i != 0)
                {
                    output << ',';
                }
                // 固定 9 位有效数字：特征向量的用途是相似度比较，float 精度的完整十进制
                // 表示没有必要，反而让协议行变长。
                output << std::setprecision(9) << features[i];
            }
            output << "]}";
            write_protocol_line(output.str());
        }

        /** @brief zhulong_status_t → 协议 code；失败语义见 design §11.8 */
        const char *extract_code_for_status(zhulong_status_t status)
        {
            switch (status)
            {
            case ZHULONG_PLUGIN_OK:
                return "OK";
            case ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT:
                return "INVALID_INPUT";
            case ZHULONG_PLUGIN_ERR_NOT_FOUND:
                return "NO_FACE_DETECTED";
            case ZHULONG_PLUGIN_ERR_NOT_SUPPORTED:
                return "MODEL_UNAVAILABLE";
            default:
                return "INTERNAL";
            }
        }
    } // namespace

    /**
     * @brief 执行 `--extract-feature`：stdin 帧流 → stdout 每帧一行 JSON
     *
     * 帧级错误（无脸/输入非法）**不退出进程**——一张坏图毁掉热进程会让后续登记全部失败。
     * 只有「能力缺失 / 流截断」这类进程级问题才返回非 0。
     *
     * @return 0 = stdin 正常 EOF；5 = 能力缺失（弱符号未解析）或帧流截断
     */
    int run_extract_feature()
    {
        const bool has_capability = &zhulong_face_extract_feature != nullptr;

        while (true)
        {
            ExtractFrameHeader header{};
            bool clean_eof = false;
            if (!read_extract_header(std::cin, header, clean_eof))
            {
                if (clean_eof)
                {
                    return 0;
                }
                write_runner_error_line("{\"status\":\"failed\",\"stage\":\"extract\",\"code\":\"INVALID_INPUT\",",
                                        "\"error\":\"truncated frame header\"}");
                return 5;
            }

            const bool header_valid = header.magic == kExtractFrameMagic &&
                                      header.width > 0 && header.height > 0 &&
                                      header.width <= kExtractMaxDimension && header.height <= kExtractMaxDimension &&
                                      header.stride >= header.width * 3 &&
                                      header.payload_len == header.stride * header.height;
            if (!header_valid)
            {
                emit_extract_failure(header.request_id, "INVALID_INPUT", "malformed frame header");
                // 帧体长度不可信：无法跳过，只能结束（否则会把像素当帧头解析）。
                return 5;
            }

            std::vector<uint8_t> pixels(header.payload_len);
            std::cin.read(reinterpret_cast<char *>(pixels.data()), static_cast<std::streamsize>(pixels.size()));
            if (static_cast<uint32_t>(std::cin.gcount()) != header.payload_len)
            {
                emit_extract_failure(header.request_id, "INVALID_INPUT", "truncated frame payload");
                return 5;
            }

            if (!has_capability)
            {
                // 包没实现该能力：逐帧如实回答，然后以进程级码退出，宿主据此映射 500。
                emit_extract_failure(header.request_id, "UNSUPPORTED", "package does not provide zhulong_face_extract_feature");
                return 5;
            }

            float features[kExtractMaxFeatureDim]{};
            uint32_t dim = kExtractMaxFeatureDim;
            const zhulong_status_t status = zhulong_face_extract_feature(
                pixels.data(), static_cast<int32_t>(header.width), static_cast<int32_t>(header.height),
                static_cast<int32_t>(header.stride), static_cast<int32_t>(header.format), features, &dim);
            if (status != ZHULONG_PLUGIN_OK)
            {
                emit_extract_failure(header.request_id, extract_code_for_status(status), "feature extraction failed");
                if (status == ZHULONG_PLUGIN_ERR_NOT_SUPPORTED)
                {
                    // 后端不具备真实人脸能力（桩/缺模型）：进程级不可用，别让宿主反复重试。
                    return 5;
                }
                continue;
            }
            if (dim == 0 || dim > kExtractMaxFeatureDim)
            {
                // 维度非法按 internal 处理：宿主侧拒绝写入底库（严禁零向量）。
                emit_extract_failure(header.request_id, "INTERNAL", "provider returned an invalid feature dimension");
                continue;
            }

            emit_extract_result(header.request_id, features, dim);
        }
    }

    int run_verify(const zhulong_plugin_api_v1 &api, const std::string &sample_path,
                   const std::string &config_json = "{}",
                   const std::string &algorithm_id = "")
    {
        std::ostringstream verified;
        verified << "{\"step\":\"abi_verified\",\"name\":"
                 << runner::json_quote(api.plugin_name ? api.plugin_name : "")
                 << ",\"version\":" << runner::json_quote(api.plugin_version ? api.plugin_version : "")
                 << "}";
        write_protocol_line(verified.str());

        // 1. 创建模型
        zhulong_model_group_h model = nullptr;
        zhulong_status_t rc = api.model_create(config_json.c_str(), &model);
        if (rc != ZHULONG_PLUGIN_OK || !model)
        {
            // 测试夹具后端（test_stub）按设计拒绝真实模型：这时的“模型加载失败”不是插件缺陷，
            // 而是本构建不含真实推理能力。把后端名一并记入日志，否则运维会在
            // “模型没拷全”与“host 后端选错”之间瞎猜。
            ZL_LOGE(0, "algorithm.runner", "plugin_model_create_failed rc=%d model_present=%d backend=%s",
                    static_cast<int>(rc), model != nullptr ? 1 : 0, inference_backend_name().c_str());
            write_runner_error_line("{\"status\":\"failed\",\"stage\":\"model_create\",\"rc\":", rc, "}");
            return 2;
        }

        // 2. 创建 Worker
        zhulong_worker_ctx_h worker = nullptr;
        rc = api.worker_create(model, 0, &worker);
        if (rc != ZHULONG_PLUGIN_OK || !worker)
        {
            ZL_LOGE(0, "algorithm.runner", "plugin_worker_create_failed rc=%d worker_present=%d",
                    static_cast<int>(rc), worker != nullptr ? 1 : 0);
            api.model_destroy(model);
            write_runner_error_line("{\"status\":\"failed\",\"stage\":\"worker_create\",\"rc\":", rc, "}");
            return 3;
        }

        // 3. 打开任务
        zhulong_task_ctx_h task = nullptr;
        rc = api.task_open(worker, 1, &task);
        if (rc != ZHULONG_PLUGIN_OK || !task)
        {
            ZL_LOGE(0, "algorithm.runner", "plugin_task_open_failed rc=%d task_present=%d",
                    static_cast<int>(rc), task != nullptr ? 1 : 0);
            api.worker_destroy(worker);
            api.model_destroy(model);
            write_runner_error_line("{\"status\":\"failed\",\"stage\":\"task_open\",\"rc\":", rc, "}");
            return 4;
        }

        // 4. 准备样本帧：`--sample` 为 PPM/PGM 时真实解码，否则回退合成帧并如实标记
        const SampleFrame sample = load_sample_frame(sample_path);
        const bool sample_decoded = sample.decoded();
        if (!sample_decoded)
        {
            ZL_LOGW(0, "algorithm.runner", "verify_sample_undecodable reason=unsupported_or_unreadable");
        }

        int width = sample_decoded ? sample.width : 640;
        int height = sample_decoded ? sample.height : 480;
        std::vector<uint8_t> synthetic;
        const uint8_t *pixels = nullptr;
        zhulong_pixel_format_t format = ZHULONG_PIXEL_FORMAT_RGB24;
        if (sample_decoded)
        {
            pixels = sample.rgb.data();
        }
        else
        {
            synthetic.assign(static_cast<size_t>(width) * static_cast<size_t>(height) * 3, 128);
            pixels = synthetic.data();
            format = ZHULONG_PIXEL_FORMAT_BGR24;
        }

        zhulong_frame_view frame;
        std::memset(&frame, 0, sizeof(frame));
        frame.frame_id = 1;
        frame.pts = 1000;
        frame.width = width;
        frame.height = height;
        frame.format = format;
        frame.planes[0] = pixels;
        frame.strides[0] = width * 3;
        frame.dma_buf_fd = -1;

        // 5. 单次标准推理调用
        zhulong_inference_result *result = nullptr;
        const auto started = std::chrono::steady_clock::now();
        rc = api.process(task, &frame, &result);
        const double cost_ms_double =
            std::chrono::duration<double, std::milli>(std::chrono::steady_clock::now() - started).count();
        const int64_t cost_ms = static_cast<int64_t>(std::llround(cost_ms_double));

        if (rc != ZHULONG_PLUGIN_OK)
        {
            ZL_LOGE(0, "algorithm.runner", "plugin_process_failed rc=%d", static_cast<int>(rc));
            api.task_close(task);
            api.worker_destroy(worker);
            api.model_destroy(model);
            write_runner_error_line("{\"status\":\"failed\",\"stage\":\"process\",\"rc\":", rc, "}");
            return 5;
        }

        uint32_t count = result ? result->count : 0;
        int32_t res_type = result ? result->result_type : 0;
        std::string result_json = serialize_inference_result(0, 1000, 0, cost_ms, result, algorithm_id);

        // 6. 释放结果
        if (result)
        {
            api.result_release(result);
        }

        // 7. 析构资源
        api.task_close(task);
        api.worker_destroy(worker);
        api.model_destroy(model);

        std::ostringstream passed;
        passed << "{\"status\":\"passed\",\"sample\":" << runner::json_quote(sample_path)
               << ",\"sample_decoded\":" << (sample_decoded ? "true" : "false")
               << ",\"width\":" << width
               << ",\"height\":" << height
               << ",\"result_type\":" << res_type
               << ",\"count\":" << count
               << ",\"cost_ms\":" << cost_ms
               << ",\"result\":" << result_json << "}";
        write_protocol_line(passed.str());
        return 0;
    }

    /**
     * @brief 帧边界把已收到的任务级配置挂到 `TaskContext` 上
     *
     * 分工是刻意的：**读取**控制消息只发生在 Worker 自己的收包循环里（单读者，不与帧
     * 竞争），这里只做「挂视图」，且**不解析 JSON** —— 字段语义属于各算法自己的
     * `config.schema.json`，骨架对算法必须无知（见 `zhulong::TaskConfig`）。
     *
     * @param task_context 该路流的任务上下文（每次推理前刷新）
     * @param task_config_json 该路流最近一次收到的配置原文（空表示未下发）
     */
    void drain_control_messages(::zhulong::TaskContext &task_context, const std::string &task_config_json)
    {
        // 每帧重挂视图：底层 string 可能刚被收包循环替换过内容，悬垂与半新半旧都不允许。
        task_context.params.json = std::string_view(task_config_json);
    }

    /**
     * @brief IPC 长驻消费路径：连接一个或多个 FrameBroker 并持续消费共享内存帧
     *
     * ⚠️ 退出码表（运维与 Go 看门狗依赖此表区分故障类型）：
     * | 码 | 含义 |
     * | -- | ---- |
     * | 0  | 正常退出（收到停止信号或达到 --max-frames） |
     * | 1  | client name 超长（HandshakeReq.client_name[32]）或骨架内部错误 |
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
     * @param api 已构造好的 C ABI 表（算法与骨架同进程，没有 .so 可加载）
     * @param options 启动参数（brokers / client_name / algorithm_id / max_frames）
     * @return int 退出码（见上表）
     */
    int run_ipc_workers(const zhulong_plugin_api_v1 &api, const ::zhulong::sdk::Options &options)
    {
        const std::vector<std::string> &broker_socks = options.brokers;
        const std::string &client_name = options.client_name;
        const int max_frames = options.max_frames;
        const std::string &algorithm_id = options.algorithm_id;
        const bool disable_motion_gate = options.disable_motion_gate;

        if (broker_socks.empty())
        {
            write_runner_error_line("{\"status\":\"failed\",\"stage\":\"args\",\"error\":\"no broker socks provided\"}");
            return 5;
        }

        zhulong_model_group_h model = nullptr;
        zhulong_status_t model_rc = api.model_create(options.config_json.c_str(), &model);
        if (model_rc != ZHULONG_PLUGIN_OK || !model)
        {
            ZL_LOGE(0, "algorithm.runner", "plugin_model_create_failed rc=%d model_present=%d backend=%s",
                    static_cast<int>(model_rc), model != nullptr ? 1 : 0, inference_backend_name().c_str());
            return 2;
        }

        std::atomic<bool> stop_all{false};
        std::atomic<int> exit_code{0};
        GalleryIndex gallery_index;

        // ── 无进展看门狗 ──────────────────────────────────────────────
        // 卡在 process() 里的线程无法被协作式回收（它不读标志位、也不会发 ACK），
        // 所以这里只做两件事：让卡死可诊断（stage=worker_stalled），以及让整包
        // 受控退出、由宿主按既有退避/熔断链路重启。
        //
        // ⚠️ 判据是「已收帧、未 ACK」的这段时间，不是「距上次 ACK 多久」：
        //    相机断流时 Worker 会长期空转在 500ms recv 超时循环里，那不是卡死。
        const int64_t stall_timeout_ms = resolve_stall_timeout_ms(::getenv(kStallTimeoutEnv));
        std::vector<StallTracker> stall_trackers(broker_socks.size());
        std::atomic<bool> watchdog_stop{false};
        std::thread watchdog;
        if (stall_timeout_ms > 0)
        {
            const int64_t poll_ms = stall_poll_interval_ms(stall_timeout_ms);
            ZL_LOGI(0, "algorithm.runner", "stall_watchdog_enabled timeout_ms=%lld poll_ms=%lld workers=%zu",
                    static_cast<long long>(stall_timeout_ms), static_cast<long long>(poll_ms),
                    stall_trackers.size());
            try
            {
                // ⚠️ `poll_ms` 必须**按值**捕获：它声明在 `if` 块内，而这个 lambda 在块结束
                //    之后仍在运行。引用捕获会让它读到一个生命周期已终止的栈槽，睡眠时长
                //    变成垃圾值 —— 实测表现为看门狗永久睡死、主线程 join 卡住整包不退出。
                watchdog = std::thread([&, poll_ms]()
                {
                    set_thread_name("algo-wd");
                    while (!watchdog_stop.load(std::memory_order_relaxed))
                    {
                        std::this_thread::sleep_for(std::chrono::milliseconds(poll_ms));
                        if (watchdog_stop.load(std::memory_order_relaxed))
                        {
                            break;
                        }
                        const int64_t now_ms = monotonic_now_ms();
                        for (size_t i = 0; i < stall_trackers.size(); ++i)
                        {
                            const int64_t stalled_ms = stall_trackers[i].stalled_ms(now_ms);
                            if (stalled_ms >= stall_timeout_ms)
                            {
                                // 不返回：_Exit(kExitWorkerStalled)
                                report_stall_and_exit(i, parse_stream_id_from_socket(broker_socks[i]),
                                                       stalled_ms);
                            }
                        }
                    }
                });
            }
            catch (const std::exception &watchdog_error)
            {
                // 线程资源耗尽：看门狗起不来是保护降级，不是致命错。不能因此
                // 把整个包打死——那会从一个「可选保护」变成启动失败。
                ZL_LOGW(0, "algorithm.runner", "stall_watchdog_unavailable reason=%s",
                        watchdog_error.what());
            }
        }

        // 单路 Worker 的**完整工作体**。刻意保持原样（只由 run_single_worker 在异常边界内调用），
        // 不要把连接/握手/收帧逻辑拆出去：这段里的任何一个分配失败都必须被同一个边界接住。
        auto run_single_worker_body = [&](size_t idx, const std::string &broker_sock)
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
                write_runner_error_line("{\"status\":\"failed\",\"stage\":\"args\",\"index\":", idx,
                                        ",\"path\":\"", broker_sock,
                                        "\",\"error\":\"cannot derive stream id from broker socket path\"}");
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
                write_runner_error_line("{\"status\":\"failed\",\"stage\":\"name_check\",\"error\":\"client name too long: ", name, "\"}");
                int expected = 0;
                exit_code.compare_exchange_strong(expected, 1);
                stop_all.store(true);
                return;
            }

            zhulong_worker_ctx_h worker = nullptr;
            zhulong_status_t wrc = api.worker_create(model, static_cast<uint32_t>(idx), &worker);
            if (wrc != ZHULONG_PLUGIN_OK || !worker)
            {
                ZL_LOGE(stream_id, "algorithm.runner", "plugin_worker_create_failed rc=%d worker_present=%d",
                        static_cast<int>(wrc), worker != nullptr ? 1 : 0);
                write_runner_error_line("{\"status\":\"failed\",\"stage\":\"worker_create\",\"rc\":", wrc, "}");
                int expected = 0;
                exit_code.compare_exchange_strong(expected, 3);
                stop_all.store(true);
                return;
            }

            zhulong_task_ctx_h task = nullptr;
            wrc = api.task_open(worker, stream_id, &task);
            if (wrc != ZHULONG_PLUGIN_OK || !task)
            {
                ZL_LOGE(stream_id, "algorithm.runner", "plugin_task_open_failed rc=%d task_present=%d",
                        static_cast<int>(wrc), task != nullptr ? 1 : 0);
                write_runner_error_line("{\"status\":\"failed\",\"stage\":\"task_open\",\"rc\":", wrc, "}");
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
                write_runner_error_line("{\"status\":\"failed\",\"stage\":\"connect\",\"index\":", idx,
                                        ",\"path\":\"", broker_sock,
                                        "\",\"error\":\"", conn_err, "\"}");
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
                write_runner_error_line("{\"status\":\"failed\",\"stage\":\"shm_init\",\"error\":\"", shm_err, "\"}");
                api.task_close(task);
                api.worker_destroy(worker);
                int expected = 0;
                exit_code.compare_exchange_strong(expected, 8);
                stop_all.store(true);
                return;
            }

            // 3.0d：一路 broker 一个 Worker 一个 TaskContext（不做 task map，也不多路复用
            // 同一个 Worker）。task_id 取该路的 stream_id，与 task_open 的入参一致。
            ::zhulong::TaskContext task_context;
            task_context.task_id = stream_id;
            // 本路流的任务级配置原文（MSG_TASK_CONFIG）。生命周期覆盖整个 Worker 线程：
            // TaskContext.params.json 只是指向它的视图。
            std::string task_config_json;

            int frames_processed = 0;
            bool last_had_targets = false;
            bool process_failure_reported = false;
            ShardedSyncContext sync_ctx;
            // ⚠️ 退出门禁：recv_msg_with_fd 返回 false 有两种含义——「500ms 内没等到帧」
            //    （可重试）与「连接已不可恢复」（EOF / POLLHUP / 流脱节）。后者由客户端内部
            //    置脏，is_connected() 随即变假。必须在检测到失联时立刻 break，绝不可无条件
            //    `continue` 重试：宿主被 kill -9 / OOM 杀掉时，那会让 runner 退化成永久
            //    100% 占用一个 CPU 核的孤儿进程（实测 20s 内烧满 19.98s CPU）。
            while (!stop_all.load(std::memory_order_relaxed) && (max_frames < 0 || frames_processed < max_frames))
            {
                ipc::IpcHeader hdr{};
                std::vector<uint8_t> payload;
                int received_fd = -1;
                if (!client.recv_msg_with_fd(hdr, payload, received_fd, 500))
                {
                    if (!client.is_connected())
                    {
                        // 宿主进程已消失（kill -9 / OOM / 崩溃）或流已脱节：干净收摊退出，
                        // 释放插件 worker/task 与共享内存映射，不留孤儿。
                        std::cerr << "{\"status\":\"info\",\"stage\":\"broker_disconnected\",\"index\":"
                                  << idx << ",\"frames_processed\":" << frames_processed << "}" << std::endl;
                        break;
                    }
                    continue;
                }

                // ⚠️ 前缀兼容：旧宿主没有运动字段，解码后 has_motion 恒为 1（全帧推理）。
                //    若这里直接按 sizeof(FrameDispatchMsg) 卡长度，新 Runner 连旧宿主会一条报文都不认，
                //    既不推理也不发 ACK，静默退化成「每租约超时收 2 帧」。
                ipc::FrameDispatchMsg dispatch{};
                const bool is_frame_dispatch =
                    (hdr.msg_type == ipc::MSG_FRAME_DISPATCH) &&
                    gate::decode_frame_dispatch(payload.data(), payload.size(), dispatch);

                if (is_frame_dispatch)
                {
                    // 从这里到 ACK 发出为止算「在处理」；看门狗只对这段时间计时。
                    // RAII：块内所有 continue / break / 异常退出路径都会自动结束计时，
                    // 漏掉任何一条都会让该路被永久判为在处理，进而被误杀。
                    StallTracker::ProcessingScope processing_scope(stall_trackers[idx]);

                    // ⚠️ 只接受 SCM_RIGHTS 传来的本地副本 fd。绝不回退到 dispatch.reserved_fd：
                    //    那是宿主进程的 fd 号，在本进程中可能指向任意无关资源。
                    const int active_dma_fd = received_fd;
                    void* dma_mmap = nullptr;

                    if (gate::should_skip_inference(dispatch.has_motion, !disable_motion_gate))
                    {
                        // 画面完全静止：跳过模型前向推理，实现 0 NPU/GPU 算力开销
                        if (last_had_targets)
                        {
                            // 若上一帧有目标，发送一次空结果作为消失信标
                            std::string beacon = serialize_inference_result(dispatch.stream_id, dispatch.pts, dispatch.capture_time_ms, 0, nullptr, algorithm_id);
                            client.send_msg(ipc::MSG_RESULT, beacon.data(), beacon.size());
                            last_had_targets = false;
                        }
                    }
                    else
                    {
                        // ⚠️ DMA-BUF 映射必须在门禁之后：静止帧连 mmap/SYNC 都不该碰，
                        //    否则「省算力」只是省了 NPU，系统调用照样每帧三次。
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

                            // 帧边界：先排控制消息，再推理（TaskContext 由骨架按流维护）
                            drain_control_messages(task_context, task_config_json);

                            zhulong_inference_result *result = nullptr;
                            auto t0 = std::chrono::steady_clock::now();
                            rc = api.process(task, &view, &result);
                            auto t1 = std::chrono::steady_clock::now();
                            int64_t cost_ms = std::chrono::duration_cast<std::chrono::milliseconds>(t1 - t0).count();

                            if (rc != ZHULONG_PLUGIN_OK)
                            {
                                if (!process_failure_reported)
                                {
                                    ZL_LOGE(stream_id, "algorithm.runner", "plugin_process_failed rc=%d",
                                            static_cast<int>(rc));
                                }
                                process_failure_reported = true;
                            }
                            else
                            {
                                process_failure_reported = false;
                            }

                            uint32_t count = (result != nullptr && rc == ZHULONG_PLUGIN_OK) ? result->count : 0;
                            bool has_targets = (count > 0);

                            // 仅在本次或上次有目标时才上报结果，避免连续空帧刷屏（去噪）
                            if (has_targets || last_had_targets)
                            {
                                std::string payload = serialize_inference_result(dispatch.stream_id, dispatch.pts, dispatch.capture_time_ms, cost_ms, result, algorithm_id);
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
                        write_runner_error_line("{\"status\":\"warning\",\"stage\":\"gallery_sync\","
                                                "\"error\":\"shard_index out of range\",\"shard_index\":",
                                                shard.shard_index, ",\"shard_total\":", shard.shard_total, "}");
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
                else if (hdr.msg_type == ipc::MSG_TASK_CONFIG && payload.size() >= sizeof(ipc::TaskConfigMsg))
                {
                    // 任务级配置：只搬运 + 按 algorithm_id 过滤，**不解析 JSON**。
                    // 广播（broadcast_message）会同时到达同一路流上的所有算法包，所以
                    // 归属不匹配是**正常情况**，静默丢弃；只有长度自相矛盾才是协议故障。
                    ipc::TaskConfigMsg cfg{};
                    std::memcpy(&cfg, payload.data(), sizeof(cfg));
                    const size_t json_len = payload.size() - sizeof(cfg);
                    const size_t target_len = ::strnlen(cfg.algorithm_id, sizeof(cfg.algorithm_id));
                    const bool for_me = !algorithm_id.empty() &&
                                        target_len == algorithm_id.size() &&
                                        std::memcmp(cfg.algorithm_id, algorithm_id.data(), target_len) == 0;
                    if (for_me)
                    {
                        if (cfg.config_json_len != json_len)
                        {
                            write_runner_error_line("{\"status\":\"warning\",\"stage\":\"task_config\","
                                                    "\"error\":\"config_json_len mismatch\"}");
                        }
                        else
                        {
                            task_config_json.assign(reinterpret_cast<const char *>(payload.data()) + sizeof(cfg), json_len);
                        }
                    }
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

        // 单路 Worker 的异常边界（线程入口）。
        //
        // 之前这段是直接交给 std::thread 的：骨架自身的 C++ 异常（结果 JSON 序列化的
        // ostringstream、数组增长、socket 路径拼接）一旦逃出线程函数就 std::terminate →
        // SIGABRT(134)，既没有 stage 日志也没有退出码语义。现在翻译成与「broker 连不上」
        // 同一类的**受控失败**：置 stop_all + 退出码 kExitWorkerCrashed，进程照常走宿主的
        // 退避 / CRASH_LOOP 熔断 / 版本回滚。
        //
        // ⚠️ 这里保持 D6「任一路失败即整包退出」的语义不变：让宿主用一次几秒的重启换回
        //    全部路，远好于一路静默缺失而宿主以为一切正常。
        auto run_single_worker = [&](size_t idx, const std::string &broker_sock)
        {
            set_worker_thread_name(idx);
            run_worker_guarded(
                [&]() { run_single_worker_body(idx, broker_sock); },
                [&](const char *detail)
                {
                    // 先置状态位再写日志：诊断字符串的构造本身也可能在 OOM 上再抛一次，
                    // 而 stop_all / 退出码在任何情况下都必须是准确的。
                    int expected = 0;
                    exit_code.compare_exchange_strong(expected, kExitWorkerCrashed);
                    stop_all.store(true);
                    write_runner_error_line("{\"status\":\"failed\",\"stage\":\"worker_exception\",\"index\":",
                                            idx, ",\"error\":",
                                            runner::json_quote(detail != nullptr ? detail : ""));
                });
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

        watchdog_stop.store(true, std::memory_order_relaxed);
        if (watchdog.joinable())
        {
            watchdog.join();
        }

        api.model_destroy(model);
        return exit_code.load();
    }


    void print_start_line(const std::string &plugin_label)
    {
        std::ostringstream line;
        line << "{\"step\":\"start\",\"plugin\":" << runner::json_quote(plugin_label) << "}";
        write_protocol_line(line.str());
    }


    bool parse_options(int argc, char **argv, Options &options, std::string &error)
    {
        for (int i = 1; i < argc; ++i)
        {
            const std::string arg = argv[i];
            const auto take_value = [&](std::string &out) -> bool {
                if (i + 1 >= argc)
                {
                    error = "missing value for " + arg;
                    return false;
                }
                out = argv[++i];
                return true;
            };
            std::string value;
            if (arg == "--broker")
            {
                if (!take_value(value)) return false;
                options.brokers.push_back(value);
            }
            else if (arg == "--name")
            {
                if (!take_value(options.client_name)) return false;
            }
            else if (arg == "--algorithm-id")
            {
                if (!take_value(options.algorithm_id)) return false;
            }
            else if (arg == "--config")
            {
                // 进程级配置：inline JSON（不是文件路径）。值被原样交给 Plugin::init；
                // 骨架不解析、不校验它的内容——那是算法自己的 config.schema.json 约定。
                if (!take_value(options.config_json)) return false;
            }
            else if (arg == "--sample")
            {
                if (!take_value(options.sample_path)) return false;
            }
            else if (arg == "--verify")
            {
                options.verify = true;
            }
            else if (arg == "--extract-feature")
            {
                options.extract_feature = true;
            }
            else if (arg == "--max-frames")
            {
                if (!take_value(value)) return false;
                options.max_frames = std::atoi(value.c_str());
                if (options.max_frames <= 0)
                {
                    error = "--max-frames must be positive";
                    return false;
                }
            }
            else if (arg == "--disable-motion-gate")
            {
                options.disable_motion_gate = true;
            }
            else
            {
                error = "unknown argument: " + arg;
                return false;
            }
        }

        if (options.verify)
        {
            if (options.sample_path.empty()) options.sample_path = "sample.jpg";
            return true;
        }
        if (options.extract_feature)
        {
            if (!options.brokers.empty())
            {
                error = "--extract-feature does not take --broker (stdin carries the frames)";
                return false;
            }
            return true;
        }
        if (options.brokers.empty())
        {
            error = "no --broker provided (and not in --verify / --extract-feature mode)";
            return false;
        }
        return true;
    }

    int run_api(const zhulong_plugin_api_v1 &api, const Options &options)
    {
        // 最后一道兜底必须在三条业务路径（verify / extract-feature / broker）之前安装。
        // 唯一早于本函数的窗口是 argv 解析——那里只有几个定长 string 操作，为它去改公开
        // 头 `ZHULONG_ALGO_MAIN` 不划算。
        install_terminate_guard();

        // 诊断日志由 log.cpp 的写线程统一落到日志文件（stderr 兜底）；
        // stdout 只承载结果/验包 JSON，宿主按行解析，两者不可混用。
        //
        // ⚠️ `start_runner_log_writer()` 返回 false 有两种含义：写线程已由宿主/测试
        //    启动，或进程内已装了 log sink。两种都不是错误——此时**不归我们所有**，
        //    因此也不能在这里 stop（会把宿主的日志通道关掉）。
        const bool owns_log_writer = zhulong::start_runner_log_writer();
        int rc = 0;
        if (options.verify)
        {
            rc = run_verify(api, options.sample_path, options.config_json, options.algorithm_id);
        }
        else if (options.extract_feature)
        {
            rc = run_extract_feature();
        }
        else
        {
            rc = run_ipc_workers(api, options);
        }
        if (owns_log_writer)
        {
            zhulong::stop_runner_log_writer();
        }
        return rc;
    }

} // namespace detail
} // namespace sdk
} // namespace zhulong
