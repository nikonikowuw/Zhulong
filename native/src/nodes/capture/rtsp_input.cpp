/**
 * @file rtsp_input.cpp
 * @brief Zhulong RTSP 采集节点底层 FFmpeg 接入与拉流实现
 *
 * 核心实现与安全设计：
 * 1. FFmpeg 中断与网络超时控制：
 *    - 针对底层阻塞式网络套接字操作，绑定 interrupt_callback。当超时到达或被外部取消时，返回 1 立即中断 FFmpeg；
 *    - 析构守卫（Input RAII）：关闭连接前先置 cancel.stopped=true，防止在 RTSP TEARDOWN 时对端不响应造成无限期挂起。
 * 2. 凭据安全与日志静默：
 *    - FFmpeg 默认日志会打印完整 URL（包含用户名和密码凭据）。通过 initialize_ffmpeg 全局将日志等级设为 QUIET 并清空回调，防止凭据泄露到标准输出。
 * 3. 严格的 URL 规范化与参数安全过滤：
 *    - 完整解析并检验 URI 格式，剥离非法空白字符、规范化端口与大小写，避免由于 URL 不规范引起的注入或重复拉流问题。
 * 4. 零拷贝与内存借用视图：
 *    - 读取到的视频帧直接以借用指针封装为 Zhulong_packet_view 供回调读取，避免数据流管道中的多余内存拷贝。
 */

#include "rtsp_input.hpp"
#include "common/log.hpp"

extern "C" {
#include <libavcodec/avcodec.h>
#include <libavformat/avformat.h>
#include <libavutil/log.h>
}

#include <algorithm>
#include <atomic>
#include <cctype>
#include <cerrno>
#include <chrono>
#include <cstdarg>
#include <cstdio>
#include <limits>
#include <map>
#include <memory>
#include <mutex>
#include <thread>

namespace zhulong {
namespace {

/** @brief URL 最大长度上限（防御性上界，避免对超长输入做无界扫描） */
constexpr size_t max_url_length = 8192;

/** @brief RTSP scheme 字面量 */
constexpr char rtsp_scheme[] = "rtsp://";

/** @brief RTSP scheme 的字符数 */
constexpr size_t scheme_length = sizeof(rtsp_scheme) - 1;

/** @brief RTSP 默认端口，与默认端口等价时从规范化结果中省略 */
constexpr int default_rtsp_port = 554;

/** @brief 端口上限（TCP/UDP 端口最大值） */
constexpr int max_port = 65535;

/**
 * @brief 流信息探测时长上界（微秒）。
 *
 * ⚠️ 这 **不是** 只是探测窗口，而是**每次冷建连的固定开销**：avformat_find_stream_info
 * 会一直读到累计该时长的流数据才返回，对直播流等价于实打实等 1 秒。
 *
 * 而 RTSP 的 SDP 已经给出了 codec、分辨率与时基（H.264/H.265 的 SPS/PPS 通常由
 * sprop-parameter-sets 提供），本代码只用探测结果补 avg_frame_rate 这类展示字段。
 * 300ms 足够覆盖多包确认，又不把首帧拖长；参数集缺失时由关键帧自带的
 * 内联 SPS/PPS 兜底（zhulong::HasCompleteParameterSets）。
 */
constexpr int probe_analyze_duration_us = 300000;

/**
 * @brief 流信息探测字节量上界。
 *
 * ⚠️ 与 analyzeduration 是「谁先到谁结束」，实测**往往是本值在卡**：
 * 1080p H.265 低码率下读满 1 MiB 需要 2~3 秒，日志里 info_ms=3175 而
 * analyzeduration 只配了 300ms 就是这种情况。RTSP 的 SDP 已给出 codec 与分辨率，
 * 把上限降到 128 KiB 后探测通常在百毫秒级返回。
 *
 * 语义兜底：extradata（VPS/SPS/PPS）本就依赖 SDP 或码流内联，缺失时由关键帧
 * 自带的参数集兜底（zhulong::HasCompleteParameterSets）。
 *
 * ⚠️ 窗口收紧必须配兼容档重试（见 legacy_probe_* 与 read_rtsp 的 fallback 分支）：
 *    SDP 不带 sprop-parameter-sets、首包不是 IDR 的相机可能读不到完整参数集，
 *    此时「探测不足」绝不能被当成「相机不可用」。
 */
constexpr int probe_size_bytes = 128 * 1024;

/**
 * @brief 兼容档探测预算：快速档读不出完整参数集时的一次性重试上限。
 *
 * 取收紧前的旧值（1 MiB / 1s）。它慢，但能救回「SDP 不带 sprop-parameter-sets、
 * 首包也不是 IDR」这类相机；"探测不足"绝不能等同于"相机不可用"。
 */
constexpr int legacy_probe_analyze_duration_us = 1000000;
constexpr int legacy_probe_size_bytes = 1048576;

/** @brief 表示「未设置截止时间」的纳秒滴答值 */
constexpr int64_t never_expires_at = std::numeric_limits<int64_t>::max();

/** @brief 当前单调时钟时刻的纳秒滴答值 */
int64_t now_ticks() noexcept {
    return std::chrono::duration_cast<std::chrono::nanoseconds>(Clock::now().time_since_epoch()).count();
}

/**
 * @brief FFmpeg 底层中断回调函数
 *
 * 在 avformat_open_input、avformat_find_stream_info 和 av_read_frame 内部周期性调用。
 * 返回 1 即可让 FFmpeg 立即终止当前阻塞操作并返回 AVERROR_EXIT。
 */
int interrupt(void *opaque) noexcept {
    return static_cast<Cancellation *>(opaque)->interrupted() ? 1 : 0;
}

/**
 * @brief RAII 管理的 AVFormatContext 包装结构
 *
 * 保证在生命周期结束时安全释放上下文。
 * ⚠️ 注意：即使在探测成功或正常结束时，也必须在 avformat_close_input 之前置 cancel.stopped=true，
 * 因为某些 RTSP 服务器在接收 TEARDOWN 命令后不发响应，提前打断可防止关闭时挂死。
 *
 * ⚠️ 本类型独占底层指针，拷贝会导致双重释放，因此显式禁用拷贝与移动。
 */
struct Input {
    AVFormatContext *context = avformat_alloc_context();
    Cancellation &cancel;

    explicit Input(Cancellation &state) : cancel(state) {
        if (!context) throw std::bad_alloc();
        // 绑定协作式中断控制器
        context->interrupt_callback = {interrupt, &cancel};
    }

    ~Input() {
        // 确保在 TEARDOWN / 关闭网络流时不会因对端停顿而挂起
        cancel.stopped.store(true, std::memory_order_relaxed);
        avformat_close_input(&context);
    }

    /**
     * @brief 丢弃当前上下文并重新分配，供「换探测预算重试」使用。
     *
     * 重试是重新建连，因此不能置 cancel.stopped（那是终态取消）。
     * ⚠️ 截止点由调用方决定：音频降级重试**不**刷新预算（open 截止点保留），
     *    兼容档重试是既有的补偿机制、仍需完整预算（见 read_rtsp）。
     */
    void reopen() {
        if (context) avformat_close_input(&context);
        context = avformat_alloc_context();
        if (!context) throw std::bad_alloc();
        context->interrupt_callback = {interrupt, &cancel};
    }

    Input(const Input &) = delete;
    Input &operator=(const Input &) = delete;
    Input(Input &&) = delete;
    Input &operator=(Input &&) = delete;
};

/**
 * @brief RAII 管理的 AVDictionary 字典包装
 *
 * 与 Input 同理，独占所有权并禁用拷贝/移动，避免 av_dict_free 被重复调用。
 */
struct Dictionary {
    AVDictionary *value = nullptr;

    Dictionary() = default;

    ~Dictionary() {
        av_dict_free(&value);
    }

    Dictionary(const Dictionary &) = delete;
    Dictionary &operator=(const Dictionary &) = delete;
    Dictionary(Dictionary &&) = delete;
    Dictionary &operator=(Dictionary &&) = delete;

    void set(const char *key, const std::string &text) {
        if (av_dict_set(&value, key, text.c_str(), 0) < 0) {
            throw std::bad_alloc();
        }
    }
};

/**
 * @brief AVPacket 智能指针自定义删除器
 */
struct PacketDeleter {
    void operator()(AVPacket *packet) const {
        av_packet_free(&packet);
    }
};

/**
 * @brief 校验 FFmpeg 函数执行结果，结合取消状态与超时时间映射异常
 *
 * ⚠️ stream_info 可能在被打断后依然返回非负值（部分元数据成功），
 * 因此必须优先检查 cancel.stopped 与绝对 deadline。
 */
void check(int result, const Cancellation &cancel, uint64_t stream_id, const char *operation) {
    if (cancel.stopped.load(std::memory_order_relaxed)) throw Failure{Zhulong_ERR_CANCELLED};
    if (cancel.expired() || result == AVERROR(ETIMEDOUT)) {
        ZL_LOGW(stream_id, "nodes.capture", "rtsp_timeout operation=%s rc=%d", operation, result);
        throw Failure{Zhulong_ERR_TIMEOUT};
    }
    if (result >= 0) return;
    if (result == AVERROR_EOF) {
        ZL_LOGW(stream_id, "nodes.capture", "rtsp_eof operation=%s rc=%d", operation, result);
        throw Failure{Zhulong_ERR_EOF};
    }
    if (result == AVERROR(ENOMEM)) {
        ZL_LOGE(stream_id, "nodes.capture", "rtsp_out_of_memory operation=%s rc=%d", operation, result);
        throw std::bad_alloc();
    }
    ZL_LOGW(stream_id, "nodes.capture", "rtsp_io_failed operation=%s rc=%d", operation, result);
    throw Failure{Zhulong_ERR_IO};
}

/** @brief 在进入阻塞调用前确认未被取消（不从结果码推断，仅看取消标志） */
void throw_if_cancelled(const Cancellation &cancel) {
    if (cancel.stopped.load(std::memory_order_relaxed)) throw Failure{Zhulong_ERR_CANCELLED};
}

/**
 * @brief 将 FFmpeg 编解码枚举映射为 Zhulong C ABI 编解码枚举
 */
int codec_id(AVCodecID codec) {
    if (codec == AV_CODEC_ID_H264) return Zhulong_CODEC_H264;
    if (codec == AV_CODEC_ID_HEVC) return Zhulong_CODEC_H265;
    throw Failure{Zhulong_ERR_UNSUPPORTED};
}

/** @brief URL 两端需要剥离的空白字符集 */
constexpr char url_blank_characters[] = " \t\r\n";

/** @brief 大小写不敏感地判断字符串是否以 RTSP scheme 开头（无额外分配） */
bool has_rtsp_scheme(const std::string &url) {
    if (url.size() < scheme_length) return false;
    return std::equal(url.begin(), url.begin() + static_cast<std::ptrdiff_t>(scheme_length), rtsp_scheme,
        [](char actual, char expected) {
            return expected == static_cast<char>(std::tolower(static_cast<unsigned char>(actual)));
        });
}

/** @brief 将字符串转换为小写 */
std::string lower(std::string value) {
    std::transform(value.begin(), value.end(), value.begin(), [](unsigned char character) {
        return static_cast<char>(std::tolower(character));
    });
    return value;
}

// ============================================================================
//                        RTSP 事务级计时（建连耗时归因）
// ============================================================================

/**
 * 为什么需要它：avformat_open_input 实测 1236ms，而项目自带的 RTSP DESCRIBE
 * 客户端对同一相机只要 ~288ms —— 说明这 1.2 秒不是「RTSP 建连固有成本」，
 * 而是某个具体往返被拖住了（重挑战、PLAY 后等首包、SETUP 卡住……）。
 *
 * FFmpeg 的 rtsp 解复用器在 AV_LOG_DEBUG 下会把每个请求/响应整体打成一条日志，
 * 因此在 open + find_stream_info 窗口内接管全局日志回调、只转发**本上下文**的
 * 事务并附上相对耗时，就能把每个往返逐条摊开。
 *
 * ⚠️ 安全：请求行里带完整 URL（含 user:pass@）。全局静默 FFmpeg 日志正是为了
 *    不泄露凭据，本类是那条策略下唯一允许放行的通道，因此必须脱敏后才能转出。
 */
constexpr size_t max_txn_line = 200;

} // namespace

/** @brief 抹掉 URL 中的 userinfo，避免凭据进入日志（单独导出以便单测覆盖） */
std::string redact_credentials(std::string text) {
    // 不区分大小写地定位 scheme：FFmpeg 打日志的是规范化后的 URL，但不能赌它一定是小写。
    auto find_scheme = [&text](size_t from) {
        const size_t limit = text.size();
        for (size_t position = from; position + scheme_length <= limit; ++position) {
            bool matched = true;
            for (size_t offset = 0; offset < scheme_length; ++offset) {
                const auto actual = static_cast<unsigned char>(text[position + offset]);
                if (std::tolower(actual) != rtsp_scheme[offset]) {
                    matched = false;
                    break;
                }
            }
            if (matched) return position;
        }
        return std::string::npos;
    };

    size_t position = find_scheme(0);
    while (position != std::string::npos) {
        const size_t authority_start = position + scheme_length;
        // authority 结束于路径 / 查询 / 空白
        const size_t authority_end = text.find_first_of("/?# \t\r\n", authority_start);
        // ⚠️ 取 authority 内**最后**一个 '@'，而不是第一个：本函数是防御性的，
        //    不能假设上游一定拒绝了含 '@' 的密码。`normalize_rtsp_url` 会直接
        //    拒绝多 '@' 的 URL（RFC 要求转义），但：
        //      - 相机厂商常用未转义的 `p@ss`，将来放宽校验时这里不能是漏洞；
        //      - 本通道会转发 FFmpeg 自己打印的 URL，不经过我们的校验器。
        //    取第一个只会把前半段替成 ***，尾部仍以明文落盘。
        size_t at = std::string::npos;
        const size_t scan_end = authority_end == std::string::npos ? text.size() : authority_end;
        for (size_t index = authority_start; index < scan_end; ++index) {
            if (text[index] == '@') at = index;
        }
        if (at != std::string::npos) {
            text.replace(authority_start, at - authority_start, "***");
            position = find_scheme(authority_start);
            continue;
        }
        // 没有 userinfo：从 authority 之后继续找，避免重复扫描同一个 scheme
        position = find_scheme(authority_start);
    }
    return text;
}

namespace {

/** @brief 把一整块请求/响应压成「首行 + CSeq」，避免把 SDP 与头部刷进日志 */
std::string summarize_rtsp_block(const std::string &block) {
    std::string summary;
    bool took_head = false;
    size_t position = 0;
    while (position <= block.size()) {
        const size_t end = block.find('\n', position);
        std::string line = block.substr(position, end == std::string::npos ? std::string::npos : end - position);
        while (!line.empty() && (line.back() == '\r' || line.back() == ' ')) line.pop_back();
        if (!line.empty()) {
            const bool is_marker = line == "Sending:" || line == "Receiving:";
            const bool is_cseq = line.rfind("CSeq", 0) == 0;
            if (!is_marker && (is_cseq || !took_head)) {
                if (!summary.empty()) summary += " | ";
                summary += line;
                if (!is_cseq) took_head = true;
            }
        }
        if (end == std::string::npos) break;
        position = end + 1;
    }
    if (summary.size() > max_txn_line) {
        summary.resize(max_txn_line);
        summary += "...";
    }
    return summary;
}

/** @brief 静默日志回调：本类的存在正是为了「任何 FFmpeg 输出都不外泄」 */
void silent_log_callback(void *, int, const char *, va_list) {}

struct TraceEntry {
    int64_t started_ticks;
    uint64_t stream_id;
    // token 让条目带身份：open 失败的上下文会被 FFmpeg 释放，指针可能被另一路复用，
    // 仅按指针 erase 会踩掉后来者的条目、让它的日志静默消失。
    uint64_t token;
};

/** @brief 单调递增的追踪序号，仅用于识别「同一个指针上换了一个上下文」。 */
std::atomic<uint64_t> trace_token_source{0};

struct TraceRegistry {
    std::mutex mutex;
    std::map<AVFormatContext *, TraceEntry> active;
    int saved_level = AV_LOG_QUIET;
    size_t users = 0;
};

TraceRegistry &trace_registry() {
    static TraceRegistry registry_instance;
    return registry_instance;
}

void trace_log_callback(void *avcl, int /*level*/, const char *format, va_list arguments) {
    if (avcl == nullptr) return;

    int64_t elapsed_ms = -1;
    uint64_t stream_id = 0;
    {
        auto &registry = trace_registry();
        std::lock_guard<std::mutex> lock(registry.mutex);
        const auto found = registry.active.find(static_cast<AVFormatContext *>(avcl));
        if (found == registry.active.end()) return;
        stream_id = found->second.stream_id;
        const auto elapsed = std::chrono::duration_cast<std::chrono::milliseconds>(
            Clock::now() - std::chrono::steady_clock::time_point(std::chrono::nanoseconds(found->second.started_ticks)));
        elapsed_ms = elapsed.count();
    }

    char buffer[1024];
    vsnprintf(buffer, sizeof(buffer), format, arguments);
    const std::string redacted = redact_credentials(buffer);
    // FFmpeg 把请求打在 AV_LOG_TRACE（"Sending:..."），应答打在 AV_LOG_DEBUG。
    // 为了不把 RTP 逐包 TRACE 也放进来，这里按内容再筛一道。
    if (redacted.find("Sending") == std::string::npos &&
        redacted.find("RTSP/1.") == std::string::npos) {
        return;
    }
    const std::string summary = summarize_rtsp_block(redacted);
    if (summary.empty()) return;
    ZL_LOGI(stream_id, "nodes.capture", "rtsp_txn t=%lldms %s",
        static_cast<long long>(elapsed_ms), summary.c_str());
}

/** @brief 在作用域内开启 RTSP 事务计时（可嵌套，最后一个析构时恢复原状） */
class RtspTransactionTrace {
public:
    RtspTransactionTrace(AVFormatContext *context, uint64_t stream_id) : context_(context) {
        if (!context_) return;
        auto &registry = trace_registry();
        std::lock_guard<std::mutex> lock(registry.mutex);
        token_ = ++trace_token_source;
        registry.active[context_] = TraceEntry{Clock::now().time_since_epoch().count(), stream_id, token_};
        if (registry.users++ == 0) {
            registry.saved_level = av_log_get_level();
            // 必须开到 TRACE：FFmpeg 的 RTSP 请求行只在 AV_LOG_TRACE 输出。
            av_log_set_level(AV_LOG_TRACE);
            av_log_set_callback(trace_log_callback);
        }
        installed_ = true;
    }

    ~RtspTransactionTrace() {
        if (!installed_) return;
        auto &registry = trace_registry();
        std::lock_guard<std::mutex> lock(registry.mutex);
        const auto found = registry.active.find(context_);
        if (found != registry.active.end() && found->second.token == token_) {
            registry.active.erase(found);
        }
        if (--registry.users == 0) {
            av_log_set_callback(silent_log_callback);
            av_log_set_level(registry.saved_level);
        }
    }

    RtspTransactionTrace(const RtspTransactionTrace &) = delete;
    RtspTransactionTrace &operator=(const RtspTransactionTrace &) = delete;
    RtspTransactionTrace(RtspTransactionTrace &&) = delete;
    RtspTransactionTrace &operator=(RtspTransactionTrace &&) = delete;

private:
    AVFormatContext *context_;
    uint64_t token_ = 0;
    bool installed_ = false;
};

} // namespace

// ============================================================================
//                               参数与 URL 规范化
// ============================================================================

bool Options::operator==(const Options &other) const {
    return transport == other.transport &&
           open_timeout_ms == other.open_timeout_ms &&
           idle_timeout_ms == other.idle_timeout_ms;
}

Options parse_options(const Zhulong_stream_options *options) {
    Options result;
    if (!options) return result;
    if (options->transport != Zhulong_TRANSPORT_TCP && options->transport != Zhulong_TRANSPORT_UDP) {
        throw Failure{Zhulong_ERR_INVALID_ARGUMENT};
    }
    result.transport = options->transport;
    if (options->open_timeout_ms) result.open_timeout_ms = options->open_timeout_ms;
    if (options->idle_timeout_ms) result.idle_timeout_ms = options->idle_timeout_ms;
    return result;
}

std::string normalize_rtsp_url(const char *raw) {
    if (!raw) throw Failure{Zhulong_ERR_INVALID_ARGUMENT};
    std::string url(raw);

    // 剥离两端空白字符：一次性定位边界，避免逐字符 erase 的 O(n²) 搬移
    const auto trim_begin = url.find_first_not_of(url_blank_characters);
    if (trim_begin == std::string::npos) throw Failure{Zhulong_ERR_INVALID_ARGUMENT};
    const auto trim_end = url.find_last_not_of(url_blank_characters);
    url = url.substr(trim_begin, trim_end - trim_begin + 1);

    // 长度与 scheme 校验（scheme 后至少需要 1 个主机字符）
    if (url.size() > max_url_length || url.size() <= scheme_length || !has_rtsp_scheme(url)) {
        throw Failure{Zhulong_ERR_INVALID_ARGUMENT};
    }

    // 检查非法字符及校验百分号编码
    for (size_t i = 0; i < url.size(); ++i) {
        const auto character = static_cast<unsigned char>(url[i]);
        if (character <= 32 || character >= 127 || character == '#' || character == '\\') {
            throw Failure{Zhulong_ERR_INVALID_ARGUMENT};
        }
        if (character == '%') {
            if (i + 2 >= url.size() ||
                !std::isxdigit(static_cast<unsigned char>(url[i + 1])) ||
                !std::isxdigit(static_cast<unsigned char>(url[i + 2]))) {
                throw Failure{Zhulong_ERR_INVALID_ARGUMENT};
            }
            i += 2;
        }
    }

    // 分离 authority 与路径/查询参数
    auto authority_end = url.find_first_of("/?", scheme_length);
    if (authority_end == std::string::npos) authority_end = url.size();
    std::string authority = url.substr(scheme_length, authority_end - scheme_length);

    // 提取 userinfo（用户名:密码）
    // ⚠️ 校验器比 `redact_credentials` **严格**：这里拒绝多个 '@'（RFC 3986 要求转义），
    //    因为规范化结果会直接交给 FFmpeg 建连，容忍未转义的 '@' 会把 host 解析歧义
    //    带进真实网络请求。脱敏侧的宽松（取最后一个 '@'）是防御性的，不反过来
    //    授权校验器放宽 —— 两边假设不同是有意为之。
    std::string userinfo;
    const auto at = authority.find('@');
    if (at != std::string::npos) {
        if (at == 0 || authority.find('@', at + 1) != std::string::npos) {
            throw Failure{Zhulong_ERR_INVALID_ARGUMENT};
        }
        userinfo = authority.substr(0, at + 1);
        authority.erase(0, at + 1);
    }

    std::string host;
    std::string port;

    // 解析 IPv6 字面量 [addr]:port 或标准 host:port
    if (!authority.empty() && authority.front() == '[') {
        const auto close = authority.find(']');
        if (close == std::string::npos || close == 1) {
            throw Failure{Zhulong_ERR_INVALID_ARGUMENT};
        }
        host = authority.substr(0, close + 1);
        if (close + 1 < authority.size()) {
            if (authority[close + 1] != ':') throw Failure{Zhulong_ERR_INVALID_ARGUMENT};
            port = authority.substr(close + 2);
            if (port.empty()) throw Failure{Zhulong_ERR_INVALID_ARGUMENT};
        }
        for (size_t i = 1; i < close; ++i) {
            if (!std::isxdigit(static_cast<unsigned char>(host[i])) && host[i] != ':' && host[i] != '.') {
                throw Failure{Zhulong_ERR_INVALID_ARGUMENT};
            }
        }
        if (host.find(':') == std::string::npos) throw Failure{Zhulong_ERR_INVALID_ARGUMENT};
    } else {
        const auto colon = authority.find(':');
        host = authority.substr(0, colon);
        if (colon != std::string::npos) {
            port = authority.substr(colon + 1);
            if (port.empty()) throw Failure{Zhulong_ERR_INVALID_ARGUMENT};
        }
        // 主机名只允许字母/数字/点/横线（IPv6 字面量在上一分支单独校验）
        for (char raw_character : host) {
            const auto character = static_cast<unsigned char>(raw_character);
            if (!std::isalnum(character) && character != '.' && character != '-') {
                throw Failure{Zhulong_ERR_INVALID_ARGUMENT};
            }
        }
    }

    if (host.empty()) throw Failure{Zhulong_ERR_INVALID_ARGUMENT};

    // 规范化端口号：554 为 RTSP 默认端口，省略不拼入以实现 URL 标准归一化
    if (!port.empty()) {
        if (port.size() > 5 || port.find_first_not_of("0123456789") != std::string::npos) {
            throw Failure{Zhulong_ERR_INVALID_ARGUMENT};
        }
        const int number = std::stoi(port);
        if (number < 1 || number > max_port) throw Failure{Zhulong_ERR_INVALID_ARGUMENT};
        port = number == default_rtsp_port ? "" : ":" + std::to_string(number);
    }

    return std::string(rtsp_scheme) + userinfo + lower(host) + port + url.substr(authority_end);
}

// ============================================================================
//                               Cancellation 实现
// ============================================================================

void Cancellation::expire_at(Clock::time_point deadline) noexcept {
    deadline_ticks_.store(
        std::chrono::duration_cast<std::chrono::nanoseconds>(deadline.time_since_epoch()).count(),
        std::memory_order_relaxed);
}

void Cancellation::expire_after(uint32_t milliseconds) noexcept {
    expire_at(Clock::now() + std::chrono::milliseconds(milliseconds));
}

bool Cancellation::expired() const noexcept {
    const int64_t deadline_ticks = deadline_ticks_.load(std::memory_order_relaxed);
    return deadline_ticks != never_expires_at && now_ticks() >= deadline_ticks;
}

bool Cancellation::interrupted() const noexcept {
    return stopped.load(std::memory_order_relaxed) || expired();
}

Zhulong_video_view Video::borrowed_view() const {
    auto result = view;
    result.extradata = extradata.empty() ? nullptr : extradata.data();
    result.extradata_size = extradata.size();
    return result;
}

void initialize_ffmpeg() {
    static std::once_flag once;
    std::call_once(once, [] {
        // 进程级别静默 FFmpeg 日志，防止 RTSP 认证凭据（URL 内 user:pass@）打入控制台或日志文件。
        // ⚠️ 刻意取舍：这里用「构造性保证」而非「正则脱敏」——只要没有任何输出，就不可能泄露凭据，
        //    代价是 FFmpeg 自身诊断全部丢失。因此本模块的错误路径必须自带 rc 与 operation 上下文
        //    （见 check()），不得依赖 FFmpeg 日志排障。
        av_log_set_level(AV_LOG_QUIET);
        av_log_set_callback(silent_log_callback);
        // 初始化网络子系统
        const int ret = avformat_network_init();
        if (ret < 0) {
            ZL_LOGE(0, "nodes.capture", "ffmpeg_network_init_failed rc=%d", ret);
            throw Failure{Zhulong_ERR_IO};
        }
    });
}

// ============================================================================
//                               核心 RTSP 拉流循环
// ============================================================================

namespace {

/** @brief 一次建连的探测预算。 */
struct ProbeLimits {
    int analyzeduration_us;
    int probesize_bytes;
};

/**
 * @brief 单枚音频包的最大字节数；超限丢包并落日志。
 *
 * 与 Go 侧音频通道的 `MaxPacketBytes: 8 * 1024` 对齐：原生若放行更大的包，
 * 上层订阅会以 ERR_PACKET_TOO_LARGE 整条断开，反而把“声音没了”变成“通道没了”。
 * 实测 G.711（160 B/20 ms）与 AAC（约 300 B/23 ms）都远小于此值。
 */
constexpr int max_audio_packet_bytes = 8192;

/** @brief 音频 extradata（AAC 的 ASC / LATM 的 config）最大保留字节数 */
constexpr size_t max_audio_extradata_bytes = 256;

/** @brief 一次 open + find_stream_info 的产物。 */
struct ProbeSession {
    int64_t open_ms = 0;
    int64_t info_ms = 0;
    int index = -1;
    int audio_index = -1;
    int info_rc = 0;
    bool info_ok = false;
};

bool is_supported_audio_sample_rate(int rate) {
    switch (rate) {
        case 8000:
        case 11025:
        case 16000:
        case 22050:
        case 24000:
        case 32000:
        case 44100:
        case 48000:
            return true;
        default:
            return false;
    }
}

int32_t audio_codec_id(AVCodecID id) {
    switch (id) {
        case AV_CODEC_ID_AAC: return Zhulong_CODEC_AAC;
        case AV_CODEC_ID_PCM_ALAW: return Zhulong_CODEC_PCMA;
        case AV_CODEC_ID_PCM_MULAW: return Zhulong_CODEC_PCMU;
        default: return 0;
    }
}

/**
 * @brief 从 AAC AudioSpecificConfig 前两字节解析真实采样率与声道数
 *
 * ⚠️ 不能直接信任 `codecpar->sample_rate` / `ch_layout.nb_channels`：RTSP 的 RTP 时钟
 *    与容器音频道并不等价，`mpeg4-generic/90000` 这种写法会让 FFmpeg 报出 90000Hz，
 *    而 ASC 里的 samplingFrequencyIndex 才是解码器真正需要的采样率。解析失败时保留原值。
 */
void apply_aac_asc(const uint8_t *asc, int asc_size, int &sample_rate, int &channels) {
    if (!asc || asc_size < 2) return;
    static const int aac_sample_rates[] = {
        96000, 88200, 64000, 48000, 44100, 32000, 24000, 22050, 16000, 12000, 11025, 8000, 7350
    };
    const int frequency_index = ((asc[0] & 0x07) << 1) | ((asc[1] & 0x80) >> 7);
    if (frequency_index >= 0 && frequency_index < static_cast<int>(sizeof(aac_sample_rates) / sizeof(aac_sample_rates[0]))) {
        sample_rate = aac_sample_rates[frequency_index];
    }
    const int channel_config = (asc[1] >> 3) & 0x0f;
    if (channel_config >= 1 && channel_config <= 2) {
        channels = channel_config;
    }
}

/**
 * @brief 用给定探测预算完成 open + find_stream_info（可重试一次）。
 *
 * 除「探测不足」（info_rc < 0）以外全部直接抛：超时、取消、编解码不支持
 * 都不是「换个预算就能好」的问题，重试只会把失败时间翻倍。
 * 探测不足的判定交给调用方，因为只有它知道还能不能再试。
 *
 * @throws Failure 建连失败 / 取消 / 超时 / 不支持的编解码
 */
ProbeSession open_and_probe(Input &input, const std::string &url, const Options &options,
                            Cancellation &cancel, uint64_t stream_id, const ProbeLimits &limits,
                            bool request_audio = false) {
    Dictionary dictionary;
    dictionary.set("rtsp_transport", options.transport == Zhulong_TRANSPORT_TCP ? "tcp" : "udp");
    if (options.transport == Zhulong_TRANSPORT_TCP) {
        dictionary.set("rtsp_flags", "prefer_tcp");
    }
    dictionary.set("timeout", std::to_string(static_cast<int64_t>(std::max(options.open_timeout_ms, options.idle_timeout_ms)) * 1000));
    dictionary.set("allowed_media_types", request_audio ? "video+audio" : "video");
    dictionary.set("analyzeduration", std::to_string(limits.analyzeduration_us));
    dictionary.set("probesize", std::to_string(limits.probesize_bytes));
    dictionary.set("fflags", "nobuffer");
    dictionary.set("max_delay", "0");
    dictionary.set("buffer_size", "1048576");

    ProbeSession session;

    // 建连窗口内开启 RTSP 事务级计时：把 open/info 的耗时摊到每个往返上。
    // 作用域刻意只覆盖到 find_stream_info 结束，读流循环不再受全局日志回调影响。
    RtspTransactionTrace trace(input.context, stream_id);

    // 1. 打开 RTSP 输入
    const auto open_started = Clock::now();
    const int open_result = avformat_open_input(&input.context, url.c_str(), av_find_input_format("rtsp"), &dictionary.value);
    check(open_result, cancel, stream_id, "open");
    session.open_ms = std::chrono::duration_cast<std::chrono::milliseconds>(Clock::now() - open_started).count();

    // 2. 定位最佳视频流轨道
    session.index = av_find_best_stream(input.context, AVMEDIA_TYPE_VIDEO, -1, -1, nullptr, 0);
    if (session.index < 0) {
        ZL_LOGW(stream_id, "nodes.capture", "rtsp_video_stream_unavailable rc=%d", session.index);
        throw Failure{Zhulong_ERR_UNSUPPORTED};
    }

    if (request_audio && session.index >= 0) {
        session.audio_index = av_find_best_stream(input.context, AVMEDIA_TYPE_AUDIO, -1, session.index, nullptr, 0);
    }

    // 快速前置编解码格式检查（在解析深度流信息前拒绝不支持的格式）
    // ⚠️ RTSP 的 SDP 可能尚未解析出 codec（AV_CODEC_ID_NONE），此时不得过早拒绝；
    //    权威判定在 avformat_find_stream_info 之后使用 codecpar->codec_id 完成。
    const auto early_codec = input.context->streams[session.index]->codecpar->codec_id;
    if (early_codec != AV_CODEC_ID_NONE && early_codec != AV_CODEC_ID_H264 && early_codec != AV_CODEC_ID_HEVC) {
        ZL_LOGW(stream_id, "nodes.capture", "rtsp_codec_unsupported codec_id=%d", static_cast<int>(early_codec));
        throw Failure{Zhulong_ERR_UNSUPPORTED};
    }

    // 3. 读取流参数信息
    const auto info_started = Clock::now();
    session.info_rc = avformat_find_stream_info(input.context, nullptr);
    session.info_ms = std::chrono::duration_cast<std::chrono::milliseconds>(Clock::now() - info_started).count();
    session.info_ok = session.info_rc >= 0;

    if (request_audio && session.index >= 0) {
        session.audio_index = av_find_best_stream(input.context, AVMEDIA_TYPE_AUDIO, -1, session.index, nullptr, 0);
    }
    return session;
}

/**
 * @brief 判定一次探测是否真的拿到了可用元数据。
 *
 * ⚠️ find_stream_info 返回 0 不等于探测成功：SDP 不带 sprop-parameter-sets、
 *    首包又不是 IDR 时，它会「成功」返回但 width/height 仍为 0。
 *    这种不完整元数据属于**探测不足**（可重试），而不是相机不支持。
 */
bool probe_metadata_usable(const AVFormatContext *context, int index, bool require_dimensions = true) {
    if (!context || index < 0 || static_cast<size_t>(index) >= context->nb_streams) return false;
    const auto *stream = context->streams[index];
    const auto *parameters = stream->codecpar;
    if (parameters->codec_id != AV_CODEC_ID_H264 && parameters->codec_id != AV_CODEC_ID_HEVC) return false;
    if (require_dimensions && (parameters->width <= 0 || parameters->height <= 0)) return false;
    return stream->time_base.num > 0 && stream->time_base.den > 0;
}

} // namespace

Video read_rtsp(const std::string &url, const Options &options, Cancellation &cancel,
                const std::function<void(const Zhulong_packet_view &)> &consume,
                const std::function<void(const Zhulong_packet_view &)> &consume_audio,
                const std::function<void(const Media &)> &ready, uint64_t stream_id) {
    // 打开阶段的**绝对**预算：音频降级只允许在这条预算内重开，绝不重新计时。
    //
    // Go 侧 Acquire 是同步阻塞的 CGO 调用，已把 ctx deadline 折叠进 open_timeout_ms
    // （internal/engine/engine.go: convertStreamOptions）。原生若在降级分支里再来一次
    // expire_after，就变成「原生最多花 2 × open_timeout_ms」，直接超出调用方预算——
    // 现场表现是点开预览等了两倍超时才报错。
    const auto open_deadline = Clock::now() + std::chrono::milliseconds(options.open_timeout_ms);
    cancel.expire_at(open_deadline);
    throw_if_cancelled(cancel);

    Input input(cancel);

    const bool is_probe_only = !consume;
    bool request_audio = !is_probe_only;
    const ProbeLimits fast_limits{probe_analyze_duration_us, probe_size_bytes};
    ProbeSession session;
    try {
        session = open_and_probe(input, url, options, cancel, stream_id, fast_limits, request_audio);
    } catch (const Failure &f) {
        // 只有「IO 失败且预算还没花完」才值得换纯视频重开：
        //   - SETUP 被拒在 FFmpeg 里就是 avformat_open_input 失败，映射为 IO；
        //   - TIMEOUT 且 cancel 已过期 = 我们自己的 open 截止点到了，重试只会把
        //     调用方多等一轮；
        //   - UNSUPPORTED 是编码不支持，换纯视频也一样失败，重试只是白付一次握手
        //     并把原因错记成“音频建连失败”，让现场排障跑偏。
        const bool audio_fallback_worthwhile = f.status == Zhulong_ERR_IO &&
                                               !cancel.stopped.load(std::memory_order_relaxed) &&
                                               !cancel.expired();
        if (request_audio && audio_fallback_worthwhile) {
            ZL_LOGW(stream_id, "nodes.capture", "rtsp_audio_open_failed rc=%d falling back to video-only", f.status);
            request_audio = false;
            // 注意：不调 expire_after——第二次握手只能消耗首次剩余的预算。
            input.reopen();
            session = open_and_probe(input, url, options, cancel, stream_id, fast_limits, false);
        } else {
            throw;
        }
    }
    bool probe_fallback = false;
    // 「探测不足」有两种形态：find_stream_info 直接报错，以及它「成功」但元数据不可用。
    // 两者都只能用兼容档重试一次来判断，绝不能直接判成相机不支持。
    // ⚠️ 仅在独立探测（is_probe_only）且未获得分辨率，或探流彻底失败时才降级到兼容档重试；
    //    拉流消费模式下只要具备 codec 与时基即可立即起播，首个 IDR 到达时自包含参数集，
    //    严禁重连 RTSP 产生数秒的无谓阻塞。
    if (!session.info_ok || !probe_metadata_usable(input.context, session.index, is_probe_only)) {
        // 超时/取消优先抛：把「被打断」当成「探测不足」去重连，会白白再花一轮超时。
        check(session.info_rc, cancel, stream_id, "stream_info");

        ZL_LOGW(stream_id, "nodes.capture", "rtsp_probe_fallback rc=%d metadata_ok=%d probesize=%d analyzeduration_us=%d",
            session.info_rc, probe_metadata_usable(input.context, session.index, is_probe_only) ? 1 : 0,
            probe_size_bytes, probe_analyze_duration_us);
        probe_fallback = true;

        // 重连会重走 TEARDOWN + DESCRIBE + SETUP + PLAY；兼容档探测比快档慢一个
        // 数量级（1 MiB / 1 s），必须在完整预算下跑，否则 <late-probe> 这类相机
        // 会被“预算不够”误判成不可用。这是既有补偿机制，与音频降级的取舍不同：
        // 音频降级是**新增失败面**，只能消耗首次 open 的剩余预算。
        cancel.expire_after(options.open_timeout_ms);
        input.reopen();
        const ProbeLimits legacy_limits{legacy_probe_analyze_duration_us, legacy_probe_size_bytes};
        session = open_and_probe(input, url, options, cancel, stream_id, legacy_limits, request_audio);
        check(session.info_rc, cancel, stream_id, "stream_info");
    }

    const int index = session.index;
    const int64_t open_ms = session.open_ms;
    const int64_t info_ms = session.info_ms;

    const auto *stream = input.context->streams[index];
    const auto *parameters = stream->codecpar;

    // 提取并校验视频流元数据
    Video video;
    // 使用流信息解析后的权威 codec_id；codec_id() 已包含不支持格式的拒绝逻辑
    video.view.codec = codec_id(parameters->codec_id);
    if ((is_probe_only && (parameters->width <= 0 || parameters->height <= 0)) ||
        stream->time_base.num <= 0 || stream->time_base.den <= 0) {
        ZL_LOGW(stream_id, "nodes.capture", "rtsp_video_metadata_invalid width=%d height=%d time_base_num=%d time_base_den=%d",
                parameters->width, parameters->height, stream->time_base.num, stream->time_base.den);
        throw Failure{Zhulong_ERR_UNSUPPORTED};
    }
    video.view.width = std::max(0, parameters->width);
    video.view.height = std::max(0, parameters->height);
    video.view.time_base_num = stream->time_base.num;
    video.view.time_base_den = stream->time_base.den;
    video.view.fps_den = 1;
    if (stream->avg_frame_rate.num > 0 && stream->avg_frame_rate.den > 0) {
        video.view.fps_num = stream->avg_frame_rate.num;
        video.view.fps_den = stream->avg_frame_rate.den;
    }
    // 保存 SPS/PPS 参数集（深拷贝）
    if (parameters->extradata_size > 0) {
        video.extradata.assign(parameters->extradata, parameters->extradata + parameters->extradata_size);
    }

    // 提取并校验音频流元数据 (v5)
    Audio audio;
    const int audio_index = session.audio_index;
    const AVStream *audio_stream = nullptr;
    if (request_audio && audio_index >= 0 && static_cast<size_t>(audio_index) < input.context->nb_streams) {
        const auto *a_stream = input.context->streams[audio_index];
        const auto *a_par = a_stream->codecpar;
        if (a_par->codec_id == AV_CODEC_ID_AAC_LATM) {
            // ⚠️ 防御性分支：实测（固定 FFmpeg 7.1.5 libavformat/rtpdec_latm.c）
            //    MP4A-LATM 的 dynamic handler 声明的是 AV_CODEC_ID_AAC，并把 LATM 帧
            //    拆成裸 AAC AU、把 ASC 写进 extradata，因此正常相机走不到这里。
            //    若真被报成 AAC_LATM，说明是浏览器无法解码的封装，明确拒绝并单列一条 WARN。
            ZL_LOGW(stream_id, "nodes.capture", "rtsp_audio_latm_unsupported");
        } else {
            const int32_t a_codec = audio_codec_id(a_par->codec_id);
            int sample_rate = a_par->sample_rate;
            int channels = a_par->ch_layout.nb_channels;
            if (a_codec == Zhulong_CODEC_AAC) {
                apply_aac_asc(a_par->extradata, a_par->extradata_size, sample_rate, channels);
            }
            if (a_codec != 0 &&
                is_supported_audio_sample_rate(sample_rate) &&
                (channels == 1 || channels == 2) &&
                a_stream->time_base.num > 0 && a_stream->time_base.den > 0) {
                audio.has_audio = true;
                audio.codec = a_codec;
                audio.sample_rate = sample_rate;
                audio.channels = channels;
                audio.time_base_num = a_stream->time_base.num;
                audio.time_base_den = a_stream->time_base.den;
                if (a_par->extradata_size > 0) {
                    if (static_cast<size_t>(a_par->extradata_size) <= max_audio_extradata_bytes) {
                        audio.extradata.assign(a_par->extradata, a_par->extradata + a_par->extradata_size);
                    } else {
                        // 刻意**不**因此把 has_audio 判成 0：G.711 本来就没有 extradata，
                        // “降级成无声”的决定权在前端（缺 ASC 时 AudioDecoder 配置不出来）。
                        // 但静默丢 config 会让“有音轨却没声音”既无日志也无错误码，
                        // 因此至少把丢弃原因落一条日志。
                        ZL_LOGW(stream_id, "nodes.capture", "rtsp_audio_extradata_too_large size=%d limit=%zu",
                                a_par->extradata_size, max_audio_extradata_bytes);
                    }
                }
                audio_stream = a_stream;
                // 首次建流必可见的能力日志（AC-6：定位“这路到底有没有音频、是什么音频”）
                ZL_LOGI(stream_id, "nodes.capture", "rtsp_audio_detected codec=%d rate=%d channels=%d tb=%d/%d extradata=%zu",
                        audio.codec, audio.sample_rate, audio.channels,
                        audio.time_base_num, audio.time_base_den, audio.extradata.size());
            } else {
                ZL_LOGW(stream_id, "nodes.capture", "rtsp_audio_rejected codec_id=%d rate=%d channels=%d tb_num=%d tb_den=%d",
                        static_cast<int>(a_par->codec_id), sample_rate,
                        channels, a_stream->time_base.num, a_stream->time_base.den);
            }
        }
    }

    // 若无消费回调，说明仅执行探测（Probe），直接返回元数据
    if (!consume) return video;

    // 通知外部调用方流已就绪（状态变更为 RUNNING），并传递复合媒体元数据
    if (ready) ready(Media{video, audio});

    std::unique_ptr<AVPacket, PacketDeleter> packet(av_packet_alloc());
    if (!packet) throw std::bad_alloc();

    // 4. 帧读取循环（使用 idle_timeout_ms 监控两帧之间的间隔）
    cancel.expire_after(options.idle_timeout_ms);
    const auto read_started = Clock::now();
    bool logged_first_packet = false;
    while (!cancel.stopped.load(std::memory_order_relaxed)) {
        if (cancel.expired()) {
            ZL_LOGW(stream_id, "nodes.capture", "rtsp_idle_timeout timeout_ms=%u", options.idle_timeout_ms);
            throw Failure{Zhulong_ERR_TIMEOUT};
        }

        const int result = av_read_frame(input.context, packet.get());
        if (result == AVERROR(EAGAIN)) {
            // 阻塞式 RTSP 套接字理论上不会返回 EAGAIN；若出现则主动退让 1ms，避免空转占满 CPU
            std::this_thread::sleep_for(std::chrono::milliseconds(1));
            continue;
        }
        // 成功读取的包不再参与超时判定：否则恰好跨越截止点的合法帧会被当作超时丢弃
        if (result < 0) check(result, cancel, stream_id, "read");

        // 双 sink 分发：仅处理所选视频轨道或音频轨道
        if (packet->stream_index == index && packet->size > 0) {
            if (!logged_first_packet) {
                logged_first_packet = true;
                // 起播耗时拆解：这一步之后才可能有帧派发给订阅者
                ZL_LOGI(stream_id, "nodes.capture", "rtsp_startup_timing open_ms=%lld info_ms=%lld first_packet_ms=%lld analyzeduration_us=%d probe_fallback=%d",
                    static_cast<long long>(open_ms), static_cast<long long>(info_ms),
                    static_cast<long long>(std::chrono::duration_cast<std::chrono::milliseconds>(Clock::now() - read_started).count()),
                    probe_analyze_duration_us, probe_fallback ? 1 : 0);
            }
            // ⚠️ 时间戳保真透传：保留原流时间戳有效性标记，禁止将缺失时间戳默认视为 0
            Zhulong_packet_view view{};
            view.track_type = ZHULONG_TRACK_VIDEO;
            view.codec = video.view.codec;
            view.has_pts = packet->pts != AV_NOPTS_VALUE;
            view.has_dts = packet->dts != AV_NOPTS_VALUE;
            view.pts = view.has_pts ? packet->pts : 0;
            view.dts = view.has_dts ? packet->dts : 0;
            view.time_base_num = stream->time_base.num;
            view.time_base_den = stream->time_base.den;
            view.key_frame = (packet->flags & AV_PKT_FLAG_KEY) != 0;
            view.data = packet->data;
            view.size = static_cast<size_t>(packet->size);

            // 以借用指针封装为视图结构体派发给同步消费回调（回调执行期间有效）
            consume(view);

            // ⚠️ 空闲超时在**回调返回之后**刷新：consume 是同步扇出，慢订阅者/慢 normalizer
            //    一旦耗时超过 idle_timeout_ms，若在回调前刷新，下一次 av_read_frame 的
            //    interrupt 就会看到已过期 deadline，把「下游慢」误判成「相机断流」。
            cancel.expire_after(options.idle_timeout_ms);
        } else if (consume_audio && audio.has_audio && audio_stream && packet->stream_index == audio_index && packet->size > 0) {
            if (packet->size <= max_audio_packet_bytes) {
                Zhulong_packet_view view{};
                view.track_type = ZHULONG_TRACK_AUDIO;
                view.codec = audio.codec;
                view.has_pts = packet->pts != AV_NOPTS_VALUE;
                view.has_dts = packet->dts != AV_NOPTS_VALUE;
                view.pts = view.has_pts ? packet->pts : 0;
                view.dts = view.has_dts ? packet->dts : 0;
                view.time_base_num = audio_stream->time_base.num;
                view.time_base_den = audio_stream->time_base.den;
                view.key_frame = 0;
                view.frame_class = Zhulong_FRAME_UNKNOWN;
                view.data = packet->data;
                view.size = static_cast<size_t>(packet->size);

                consume_audio(view);
            } else {
                ZL_LOGW(stream_id, "nodes.capture", "rtsp_audio_packet_too_large size=%d limit=%d",
                        packet->size, max_audio_packet_bytes);
            }
            // ⚠️ 音频包不刷新空闲超时：空闲监测专用于视频活性
        }
        av_packet_unref(packet.get());
    }

    // 循环退出只可能是因为停止标志被置位；与 check() 的错误语义保持一致
    throw Failure{Zhulong_ERR_CANCELLED};
}

} // namespace zhulong
